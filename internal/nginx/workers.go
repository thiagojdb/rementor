package nginx

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type nginxWorker struct {
	pid       int
	identity  string
	listeners map[string]bool
}

func workerIdentity(pid, master int) (string, bool) {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", false
	}
	end := strings.LastIndexByte(string(stat), ')')
	if end < 0 {
		return "", false
	}
	fields := strings.Fields(string(stat)[end+1:])
	if len(fields) < 20 || fields[1] != strconv.Itoa(master) {
		return "", false
	}
	cmd, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil || !strings.HasPrefix(string(cmd), "nginx: worker process") {
		return "", false
	}
	return fields[19], true // process start time prevents PID-reuse confusion
}

func (rp *RoutingProvider) masterPID() int {
	data, err := os.ReadFile(rp.pidFile)
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if pid <= 1 {
		return 0
	}
	cmd, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil || !strings.HasPrefix(string(cmd), "nginx: master process") {
		return 0
	}
	return pid
}

// Capture only worker children of the configured master before reload.
// Platforms without /proc use nginx's normal retirement delay instead.
func (rp *RoutingProvider) workers() []nginxWorker {
	master := rp.masterPID()
	if master <= 1 {
		return nil
	}
	paths, _ := filepath.Glob(fmt.Sprintf("/proc/%d/task/*/children", master))
	var workers []nginxWorker
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, value := range strings.Fields(string(data)) {
			pid, _ := strconv.Atoi(value)
			if identity, ok := workerIdentity(pid, master); ok {
				workers = append(workers, nginxWorker{pid: pid, identity: identity, listeners: workerListeners(pid)})
			}
		}
	}
	return workers
}

// After a candidate worker serves proof, gracefully stop old workers accepting
// new connections. This is the same QUIT nginx's master sends after its fixed
// 100ms grace period; in-flight requests continue draining normally.
func (rp *RoutingProvider) retireWorkers(workers []nginxWorker) bool {
	if len(workers) == 0 {
		return false
	}
	for _, worker := range workers {
		if worker.listeners == nil {
			return false
		}
	}
	master := rp.masterPID()
	if master <= 1 {
		return false
	}
	for _, worker := range workers {
		identity, ok := workerIdentity(worker.pid, master)
		if !ok {
			continue
		}
		if identity != worker.identity {
			return false
		}
		if err := syscall.Kill(worker.pid, syscall.SIGQUIT); err != nil {
			return false
		}
	}
	deadline := time.Now().Add(50 * time.Millisecond)
	for time.Now().Before(deadline) {
		done := true
		for _, worker := range workers {
			identity, ok := workerIdentity(worker.pid, master)
			if !ok || identity != worker.identity {
				continue
			}
			cmd, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", worker.pid))
			if err == nil && !strings.Contains(string(cmd), "shutting down") {
				done = false
				break
			}
			descriptors, err := filepath.Glob(fmt.Sprintf("/proc/%d/fd/*", worker.pid))
			if err != nil {
				return false
			}
			for _, descriptor := range descriptors {
				target, err := os.Readlink(descriptor)
				if err == nil && worker.listeners[target] {
					done = false
					break
				}
			}
		}
		if done {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

// Remember listening socket identities so the shutdown title alone cannot
// race the worker closing its accept sockets.
func workerListeners(pid int) map[string]bool {
	result := make(map[string]bool)
	for _, protocol := range []string{"tcp", "tcp6"} {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/net/%s", pid, protocol))
		if err != nil {
			return nil
		}
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) > 9 && fields[3] == "0A" {
				result["socket:["+fields[9]+"]"] = true
			}
		}
	}
	return result
}
