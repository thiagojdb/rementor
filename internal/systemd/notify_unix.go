//go:build unix

package systemd

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	readyState    = "READY=1"
	stoppingState = "STOPPING=1"
	watchdogState = "WATCHDOG=1"
)

// NotifyReady tells systemd the service has completed startup.
func NotifyReady() error {
	return notify(readyState)
}

// NotifyStopping tells systemd the service is shutting down.
func NotifyStopping() error {
	return notify(stoppingState)
}

// NotifyWatchdog tells systemd the service main loop is still alive.
func NotifyWatchdog() error {
	return notify(watchdogState)
}

// WatchdogInterval returns the configured systemd watchdog interval.
func WatchdogInterval() (time.Duration, bool, error) {
	usec := strings.TrimSpace(os.Getenv("WATCHDOG_USEC"))
	if usec == "" {
		return 0, false, nil
	}

	value, err := strconv.ParseUint(usec, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("parse WATCHDOG_USEC: %w", err)
	}

	return time.Duration(value) * time.Microsecond, true, nil
}

func notify(state string) error {
	socket := os.Getenv("NOTIFY_SOCKET")
	if socket == "" {
		return nil
	}

	addr := &net.UnixAddr{Name: socket, Net: "unixgram"}
	if strings.HasPrefix(socket, "@") {
		addr.Name = "\x00" + socket[1:]
	}

	conn, err := net.DialUnix("unixgram", nil, addr)
	if err != nil {
		return fmt.Errorf("dial NOTIFY_SOCKET: %w", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte(state)); err != nil {
		return fmt.Errorf("write NOTIFY_SOCKET: %w", err)
	}

	return nil
}
