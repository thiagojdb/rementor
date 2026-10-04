package nginx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/thiagojdb/rementor/internal/config"
	"github.com/thiagojdb/rementor/internal/models"
	"github.com/thiagojdb/rementor/internal/services"
)

const workspacesConfigFile = "workspaces.conf"

type RoutingProvider struct {
	mu           sync.Mutex
	lastRendered string
	renderCache  map[string]renderCacheEntry
	configFiles  map[string][32]byte
	pidFile      string
	confDir      string
	binary       string
}

func NewRoutingProvider(confDir, binary string) services.RoutingProvider {
	if confDir == "" {
		confDir = config.GetNginxConfDir()
	}
	if binary == "" {
		binary = config.DefaultNginxBinary
	}
	return &RoutingProvider{confDir: confDir, binary: binary}
}

func (rp *RoutingProvider) IsAvailable() bool {
	return rp.run("-t") == nil
}

func (rp *RoutingProvider) LoadInitialConfig(workspaces []*models.Workspace) error {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	rendered, err := rp.render(workspaces)
	if err != nil {
		return fmt.Errorf("render nginx config: %w", err)
	}
	return rp.install(rendered, false)
}

func (rp *RoutingProvider) ApplyRouting(workspaces []*models.Workspace) error {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	rendered, err := rp.render(workspaces)
	if err != nil {
		return fmt.Errorf("render nginx config: %w", err)
	}
	return rp.install(rendered, true)
}

func (rp *RoutingProvider) install(rendered string, verify bool) error {
	if err := os.MkdirAll(rp.confDir, 0o755); err != nil {
		return fmt.Errorf("create nginx config directory: %w", err)
	}

	target := filepath.Join(rp.confDir, workspacesConfigFile)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, []byte(rendered), 0o644); err != nil {
		return fmt.Errorf("write nginx config: %w", err)
	}

	previous, readErr := os.ReadFile(target)
	hadPrevious := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		_ = os.Remove(tmp)
		return fmt.Errorf("read existing nginx config: %w", readErr)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("install nginx config: %w", err)
	}

	fast := verify && string(previous) == rp.lastRendered && rp.canSignal()
	if !fast {
		if err := rp.verifyLoaded(target); err != nil {
			rp.restore(target, previous, hadPrevious)
			return err
		}
	}
	oldWorkers := rp.workers()
	reload := func() error {
		if fast {
			return rp.signalReload()
		}
		return rp.run("-s", "reload")
	}
	if err := reload(); err != nil {
		rp.restore(target, previous, hadPrevious)
		_ = reload()
		return fmt.Errorf("reload nginx: %w", err)
	}
	if verify {
		if err := rp.verifyServing(rendered, oldWorkers); err != nil {
			// A HUP is asynchronous: nginx can reject a candidate after accepting
			// the signal, leaving the previous worker in place. Re-test the candidate
			// before restoring it so callers receive nginx's actual validation error.
			if validationErr := rp.run("-t"); validationErr != nil {
				err = fmt.Errorf("validate nginx candidate: %w", validationErr)
			}
			rp.restore(target, previous, hadPrevious)
			if reloadErr := reload(); reloadErr != nil {
				return fmt.Errorf("%w; rollback reload: %v", err, reloadErr)
			}
			return err
		}
	}

	rp.lastRendered = rendered
	return nil
}

// VerifyRouting closes the provider boundary used by atomic route
// operations.  LoadInitialConfig already validates and reloads nginx; this
// hook additionally confirms that the generated file still matches the
// candidate and that nginx reports the file as part of its loaded config.
func (rp *RoutingProvider) VerifyRouting(workspaces []*models.Workspace) error {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	target := filepath.Join(rp.confDir, workspacesConfigFile)
	rendered, err := rp.render(workspaces)
	if err != nil {
		return fmt.Errorf("render route verification config: %w", err)
	}
	loaded, err := os.ReadFile(target)
	if err != nil {
		return fmt.Errorf("read generated nginx config: %w", err)
	}
	if string(loaded) != rendered {
		return fmt.Errorf("generated nginx config differs from the candidate")
	}
	if err := rp.verifyLoaded(target); err != nil {
		return fmt.Errorf("verify loaded nginx config: %w", err)
	}
	return nil
}

// InspectRouting is the read-only counterpart used by route sync.  It does
// not reload nginx; it only compares the generated candidate with the file
// nginx currently includes and confirms that include is visible in -T.
func (rp *RoutingProvider) InspectRouting(workspaces []*models.Workspace) (bool, error) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	target := filepath.Join(rp.confDir, workspacesConfigFile)
	rendered, err := rp.render(workspaces)
	if err != nil {
		return false, err
	}
	loaded, err := os.ReadFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if string(loaded) != rendered {
		return false, nil
	}
	if err := rp.verifyLoaded(target); err != nil {
		return false, err
	}
	return true, nil
}

func (rp *RoutingProvider) restore(target string, previous []byte, hadPrevious bool) {
	if hadPrevious {
		_ = os.WriteFile(target, previous, 0o644)
		return
	}
	_ = os.Remove(target)
}

func (rp *RoutingProvider) Close() error {
	return nil
}

func (rp *RoutingProvider) verifyLoaded(target string) error {
	out, err := rp.output("-T")
	if err != nil {
		return fmt.Errorf("inspect nginx config: %w", err)
	}

	absoluteTarget, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve nginx config path: %w", err)
	}
	marker := "# configuration file " + filepath.Clean(absoluteTarget) + ":"
	if strings.Contains(string(out), marker) {
		rp.rememberConfigFiles(string(out), absoluteTarget)
		return nil
	}

	return fmt.Errorf(
		"nginx does not load generated config %s; add %q to the nginx http block",
		absoluteTarget,
		"include "+filepath.Join(filepath.Clean(rp.confDir), "*.conf")+";",
	)
}

func (rp *RoutingProvider) run(args ...string) error {
	_, err := rp.output(args...)
	return err
}

func (rp *RoutingProvider) output(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, rp.binary, args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return out, nil
	}

	sudoArgs := append([]string{"-n", rp.binary}, args...)
	sudoCmd := exec.CommandContext(ctx, "sudo", sudoArgs...)
	sudoOut, sudoErr := sudoCmd.CombinedOutput()
	if sudoErr == nil {
		return sudoOut, nil
	}

	return nil, fmt.Errorf("%s %v failed: %w: %s; sudo %v failed: %w: %s", rp.binary, args, err, string(out), sudoArgs, sudoErr, string(sudoOut))
}

var configDigestPattern = regexp.MustCompile(`X-Rementor-Config "([0-9a-f]{64})"`)

// Fresh connections avoid proving an old graceful-shutdown worker on a reused
// keep-alive socket. The endpoint is nginx-local and never waits for an app.
func (rp *RoutingProvider) verifyServing(rendered string, oldWorkers []nginxWorker) error {
	match := configDigestPattern.FindStringSubmatch(rendered)
	if len(match) != 2 {
		return fmt.Errorf("generated config has no worker proof")
	}
	address := strings.Fields(listenDirectives()[0])[0]
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		host = "127.0.0.1"
		port = address
	}
	if host == "0.0.0.0" || host == "" {
		host = "127.0.0.1"
	}
	if host == "::" {
		host = "::1"
	}
	transport := &http.Transport{DisableKeepAlives: true, Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 100 * time.Millisecond}
	deadline := time.Now().Add(2 * time.Second)
	var firstProof time.Time
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, "http://"+net.JoinHostPort(host, port)+"/__rementor/config-ready", nil)
		if err != nil {
			return err
		}
		req.Host = config.Config.RementorDomain
		response, err := client.Do(req)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusNoContent && response.Header.Get("X-Rementor-Config") == match[1] {
				// nginx keeps the previous workers accepting for 100ms after spawning
				// their replacements. A single new-worker response is not sufficient.
				if firstProof.IsZero() {
					workerPID, _ := strconv.Atoi(response.Header.Get("X-Rementor-Worker"))
					old := false
					for _, worker := range oldWorkers {
						if worker.pid == workerPID {
							old = true
							break
						}
					}
					if old {
						time.Sleep(time.Millisecond)
						continue
					}
					firstProof = time.Now()
					if rp.retireWorkers(oldWorkers) {
						return nil
					}
				}
				if time.Since(firstProof) >= 105*time.Millisecond {
					return nil
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("nginx worker did not serve the candidate configuration within 2s")
}

var configFilePattern = regexp.MustCompile(`(?m)^# configuration file (.+):$`)
var pidPattern = regexp.MustCompile(`(?m)^\s*pid\s+([^;\s]+)\s*;`)

func (rp *RoutingProvider) rememberConfigFiles(dump, target string) {
	rp.configFiles = make(map[string][32]byte)
	rp.pidFile = ""
	if p := pidPattern.FindStringSubmatch(dump); len(p) == 2 && filepath.IsAbs(p[1]) {
		rp.pidFile = p[1]
	}
	for _, match := range configFilePattern.FindAllStringSubmatch(dump, -1) {
		path := filepath.Clean(match[1])
		if path == target {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			rp.configFiles = nil
			return
		}
		rp.configFiles[path] = sha256.Sum256(data)
	}
}

// Warm applies need no speculative nginx parse: the master validates the
// candidate on HUP, and live worker proof gates success. Only use this path
// while the previously inspected base configuration is unchanged.
func (rp *RoutingProvider) canSignal() bool {
	if rp.pidFile == "" || len(rp.configFiles) == 0 || rp.masterPID() <= 1 {
		return false
	}
	for path, hash := range rp.configFiles {
		data, err := os.ReadFile(path)
		if err != nil || sha256.Sum256(data) != hash {
			return false
		}
	}
	return true
}

func (rp *RoutingProvider) signalReload() error {
	pid := rp.masterPID()
	if pid <= 1 {
		return fmt.Errorf("nginx master process is unavailable")
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	defer process.Release()
	return process.Signal(syscall.SIGHUP)
}

type renderCacheEntry struct {
	key  [32]byte
	text string
}

func (rp *RoutingProvider) render(workspaces []*models.Workspace) (string, error) {
	if rp.renderCache == nil {
		rp.renderCache = make(map[string]renderCacheEntry)
	}
	control, err := RenderConfig(nil, config.Config.RementorDomain)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	out.WriteString(configDigestPattern.ReplaceAllString(control, `X-Rementor-Config "REMENTOR_CONFIG_DIGEST"`))
	next := make(map[string]renderCacheEntry, len(workspaces))
	for _, ws := range workspaces {
		// Runtime health/projection timestamps must not invalidate routing output.
		copyWS := *ws
		copyWS.Route = models.RouteState{RouteVersion: ws.Route.RouteVersion, OperationID: ws.Route.OperationID}
		copyWS.Applications = nil
		var legacy []bool
		for _, app := range ws.Applications {
			copyApp := appWithRemoteBase(app, app.RemoteBaseUrl)
			copyApp.Route = models.RouteState{RouteVersion: app.Route.RouteVersion, OperationID: app.Route.OperationID}
			copyWS.Applications = append(copyWS.Applications, copyApp)
			legacy = append(legacy, app.LegacyPublicPath, app.LegacyUpstreamContext)
		}
		encoded, err := json.Marshal(struct {
			Workspace *models.Workspace
			Legacy    []bool
			Domain    string
			Listen    []string
		}{&copyWS, legacy, config.Config.RementorDomain, listenDirectives()})
		if err != nil {
			return "", err
		}
		key := sha256.Sum256(encoded)
		entry, ok := rp.renderCache[ws.WorkspaceID]
		if !ok || entry.key != key {
			fragment, err := renderConfig([]*models.Workspace{ws}, config.Config.RementorDomain, true)
			if err != nil {
				return "", err
			}
			namespace := sha256.Sum256([]byte(ws.WorkspaceID))
			prefix := "$rementor_" + hex.EncodeToString(namespace[:8]) + "_"
			fragment = strings.ReplaceAll(fragment, "$rementor_proof", prefix+"proof")
			fragment = strings.ReplaceAll(fragment, "$rementor_location", prefix+"location")
			fragment = configDigestPattern.ReplaceAllString(fragment, `X-Rementor-Config "REMENTOR_CONFIG_DIGEST"`)
			entry = renderCacheEntry{key: key, text: fragment}
		}
		next[ws.WorkspaceID] = entry
		out.WriteString(entry.text)
	}
	rp.renderCache = next
	raw := out.String()
	digest := sha256.Sum256([]byte(raw))
	return strings.ReplaceAll(raw, "REMENTOR_CONFIG_DIGEST", hex.EncodeToString(digest[:])), nil
}
