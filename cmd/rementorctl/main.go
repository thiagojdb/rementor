package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/thiagojdb/rementor/internal/cli"
)

const defaultServerURL = "http://localhost:9300"

func main() {
	args := os.Args[1:]

	// Extract --json and --server from anywhere in args (before or after subcommand)
	jsonOutput := false
	serverURL := ""
	sessionID := os.Getenv("REMENTOR_SESSION")
	filtered := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOutput = true
		case "--session":
			if i+1 >= len(args) {
				cli.Die("--session requires an ID")
			}
			sessionID = args[i+1]
			i++
		case "--server":
			if i+1 < len(args) {
				serverURL = args[i+1]
				i++
			}
		default:
			if strings.HasPrefix(args[i], "--session=") {
				sessionID = strings.TrimPrefix(args[i], "--session=")
			} else {
				filtered = append(filtered, args[i])
			}
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
	client.SessionID = sessionID
	cmd := filtered[0]
	rest := filtered[1:]
	if sessionID != "" && (cmd == "announce" || (cmd == "workspace" && len(rest) > 0 && rest[0] == "create")) {
		cli.Die("use session registration; announce/workspace creation is not session-scoped")
	}

	switch cmd {
	case "session":
		cli.SessionCmd(client, jsonOutput, rest)
	case "workspace":
		cli.WorkspaceCmd(client, jsonOutput, rest)
	case "app":
		cli.AppCmd(client, jsonOutput, rest)
	case "announce":
		cli.AnnounceCmd(client, jsonOutput, rest)
	case "mcp":
		cli.MCPCmd(client, url, rest)
	case "nginx":
		cli.NginxCmd(rest)
	case "route":
		cli.RouteCmd(client, jsonOutput, rest)
	case "url", "rementor_url":
		cli.URLCmd(client, jsonOutput, rest)
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
  --session <id>   isolated routing session (default: $REMENTOR_SESSION)

Commands:
  session create <name> --workspace <environment>
  session list [--workspace <environment>]
  session inspect|close <session-id>
  session refresh <session-id> [--apply --preview-token <token>]
  workspace list
  workspace create <id> --local-domain <d> [--type routing|local-apps] [--name <n>] [--color <c>] [--default-remote-base-url <url>]
  workspace delete <id>

  app list <workspace>
  app register <workspace> <app> --port <N> [--path /path] [--public-path /path] [--domain d.localhost] [--remote-base-url <url>] [--context /ctx] [--upstream-context /ctx] [--frontend-root /root] [--strict-metadata] [--name <n>] [--health endpoint] [--app-id <id>] [--service-id <id>] [--repository <name>] [--aliases <a,b>] [--route-override]
  app unregister <workspace> <app>
  app toggle <workspace> <app>
  app alias <workspace> <app-or-alias> <alias>
  app resolve <workspace> <app-or-alias>

  announce --workspace <id> --app <id> --port <N> [--type routing|local-apps] [--public-path /path] [--upstream-context /ctx] [--frontend-root /root] [--strict-metadata]
           [--local-domain <d>] [--path /path] [--domain <d>] [--remote-base-url <url>] [--context /ctx] [--name <n>]
           [--health <endpoint>] [--route-override] [--no-activate]`)
	fmt.Fprintln(os.Stderr, "  mcp [--protocol auto|modern|legacy]")
	fmt.Fprintln(os.Stderr, "  nginx load-routes")
	fmt.Fprintln(os.Stderr, "  route get|conflicts|resolve|plan|apply|sync")
	fmt.Fprintln(os.Stderr, "  url --workspace <id> --app <app-or-alias>")
}
