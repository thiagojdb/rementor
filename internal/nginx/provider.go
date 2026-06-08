package nginx

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/thiagojdb/rementor/internal/config"
	"github.com/thiagojdb/rementor/internal/models"
	"github.com/thiagojdb/rementor/internal/services"
)

const workspacesConfigFile = "workspaces.conf"

type RoutingProvider struct {
	confDir string
	binary  string
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
	if err := os.MkdirAll(rp.confDir, 0o755); err != nil {
		return fmt.Errorf("create nginx config directory: %w", err)
	}

	rendered, err := RenderConfig(workspaces, config.Config.RementorDomain)
	if err != nil {
		return fmt.Errorf("render nginx config: %w", err)
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

	if err := rp.run("-t"); err != nil {
		rp.restore(target, previous, hadPrevious)
		return fmt.Errorf("validate nginx config: %w", err)
	}
	if err := rp.run("-s", "reload"); err != nil {
		rp.restore(target, previous, hadPrevious)
		_ = rp.run("-s", "reload")
		return fmt.Errorf("reload nginx: %w", err)
	}
	return nil
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

func (rp *RoutingProvider) run(args ...string) error {
	cmd := exec.Command(rp.binary, args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}

	sudoArgs := append([]string{"-n", rp.binary}, args...)
	sudoCmd := exec.Command("sudo", sudoArgs...)
	sudoOut, sudoErr := sudoCmd.CombinedOutput()
	if sudoErr == nil {
		return nil
	}

	return fmt.Errorf("%s %v failed: %w: %s; sudo %v failed: %w: %s", rp.binary, args, err, string(out), sudoArgs, sudoErr, string(sudoOut))
}
