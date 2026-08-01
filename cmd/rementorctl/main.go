package main

import (
	"fmt"
	"os"

	"github.com/thiagojdb/rementor/internal/cli"
)

const defaultServerURL = "http://localhost:9300"

func main() {
	args := os.Args[1:]

	// Extract --json and --server from anywhere in args (before or after subcommand)
	jsonOutput := false
	serverURL := ""
	filtered := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOutput = true
		case "--server":
			if i+1 < len(args) {
				serverURL = args[i+1]
				i++
			}
		default:
			filtered = append(filtered, args[i])
		}
	}

	// Resolve server URL: flag > env var > default.
	url := serverURL
	if url == "" {
		url = os.Getenv("RMENTOR_URL")
	}
	if url == "" {
		url = defaultServerURL
	}

	if len(filtered) == 0 {
		usage()
		os.Exit(1)
	}

	client := cli.NewClient(url)
	cmd := filtered[0]
	rest := filtered[1:]

	switch cmd {
	case "workspace":
		cli.WorkspaceCmd(client, jsonOutput, rest)
	case "app":
		cli.AppCmd(client, jsonOutput, rest)
	case "announce":
		cli.AnnounceCmd(client, jsonOutput, rest)
	case "mcp":
		cli.MCPCmd(client, url)
	case "nginx":
		cli.NginxCmd(rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `rementorctl - CLI for the rementor service routing dashboard

Usage:
  rementorctl [--server <url>] [--json] <command> [options]
  or: rementorctl <command> [options] [--server <url>] [--json]

Global options (can be placed before or after the command):
  --server <url>   rementor server URL (default: $RMENTOR_URL or http://localhost:9300)
  --json           output JSON

Commands:
  workspace list
  workspace create <id> --local-domain <d> [--type routing|local-apps] [--name <n>] [--color <c>] [--default-remote-base-url <url>]
  workspace delete <id>

  app list <workspace>
  app register <workspace> <app> --port <N> [--path /path] [--domain d.localhost] [--remote-base-url <url>] [--context /ctx] [--name <n>] [--health endpoint] [--app-id <id>] [--service-id <id>] [--repository <name>] [--aliases <a,b>]
  app unregister <workspace> <app>
  app toggle <workspace> <app>
  app alias <workspace> <app-or-alias> <alias>
  app resolve <workspace> <app-or-alias>

  announce --workspace <id> --app <id> --port <N> [--type routing|local-apps]
           [--local-domain <d>] [--path /path] [--domain <d>] [--remote-base-url <url>] [--context /ctx] [--name <n>]
           [--health <endpoint>] [--no-activate]`)
	fmt.Fprintln(os.Stderr, "  mcp")
	fmt.Fprintln(os.Stderr, "  nginx load-routes")
}
