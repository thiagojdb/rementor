//go:build !unix

package systemd

import "time"

func NotifyReady() error {
	return nil
}

func NotifyStopping() error {
	return nil
}

func NotifyWatchdog() error {
	return nil
}

func WatchdogInterval() (time.Duration, bool, error) {
	return 0, false, nil
}
