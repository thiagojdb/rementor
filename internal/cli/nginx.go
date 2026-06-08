package cli

import (
	"fmt"
	"os"

	"github.com/thiagojdb/rementor/internal/config"
	nginxprovider "github.com/thiagojdb/rementor/internal/nginx"
)

// NginxCmd dispatches nginx administration subcommands.
func NginxCmd(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: rementorctl nginx load-routes")
		os.Exit(1)
	}

	switch args[0] {
	case "load-routes":
		loadNginxRoutes()
	default:
		fmt.Fprintf(os.Stderr, "unknown nginx subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func loadNginxRoutes() {
	if err := config.Load(); err != nil {
		Die("failed to load config: %v", err)
	}

	workspaces, err := config.LoadWorkspaces()
	if err != nil {
		Die("failed to load workspaces: %v", err)
	}
	if err := config.LoadState(workspaces); err != nil {
		Die("failed to load state: %v", err)
	}

	provider := nginxprovider.NewRoutingProvider(config.Config.NginxConfDir, config.Config.NginxBinary)
	if !provider.IsAvailable() {
		Die("nginx is not available or config validation failed")
	}
	if err := provider.LoadInitialConfig(workspaces); err != nil {
		Die("failed to load nginx routes: %v", err)
	}

	fmt.Printf("Loaded nginx routes for %d workspace(s)\n", len(workspaces))
}
