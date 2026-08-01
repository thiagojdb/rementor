package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
)

// WorkspaceCmd dispatches workspace subcommands.
func WorkspaceCmd(client *Client, jsonOutput bool, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: rementorctl workspace <list|create|delete> [options]")
		os.Exit(1)
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list":
		workspaceList(client, jsonOutput, rest)
	case "create":
		workspaceCreate(client, jsonOutput, rest)
	case "delete":
		workspaceDelete(client, jsonOutput, rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown workspace subcommand: %s\n", sub)
		os.Exit(1)
	}
}

func workspaceList(client *Client, jsonOutput bool, args []string) {
	fs := flag.NewFlagSet("workspace list", flag.ExitOnError)
	if err := parseFlags(fs, args); err != nil {
		Die("%v", err)
	}

	workspaces, err := client.ListWorkspaces(context.Background())
	if err != nil {
		Die("%v", err)
	}

	if jsonOutput {
		PrintJSON(workspaces)
		return
	}

	w := NewTabWriter()
	fmt.Fprintln(w, "ID\tTYPE\tNAME\tDOMAIN")
	for _, ws := range workspaces {
		domain := ""
		if ws.Routing != nil {
			domain = ws.Routing.LocalDomain
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", ws.ID, ws.Type, ws.Name, domain)
	}
	w.Flush()
}

func workspaceCreate(client *Client, jsonOutput bool, args []string) {
	fs := flag.NewFlagSet("workspace create", flag.ExitOnError)
	localDomain := fs.String("local-domain", "", "local domain for routing workspaces (required for routing type)")
	wsType := fs.String("type", "routing", "workspace type: routing|local-apps")
	name := fs.String("name", "", "display name")
	color := fs.String("color", "", "color class (e.g. bg-blue-500)")
	defaultRemoteBaseUrl := fs.String("default-remote-base-url", "", "default remote base URL")
	if err := parseFlags(fs, args); err != nil {
		Die("%v", err)
	}

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: rementorctl workspace create <id> [options]")
		os.Exit(1)
	}
	id := fs.Arg(0)

	if *wsType == "routing" && *localDomain == "" {
		Die("--local-domain is required for routing workspaces")
	}

	req := CreateWorkspaceRequest{
		ID:                   id,
		Type:                 *wsType,
		Name:                 *name,
		Color:                *color,
		LocalDomain:          *localDomain,
		DefaultRemoteBaseURL: *defaultRemoteBaseUrl,
	}

	ws, err := client.CreateWorkspace(context.Background(), req)
	if err != nil {
		Die("%v", err)
	}

	if jsonOutput {
		PrintJSON(ws)
		return
	}
	fmt.Printf("workspace %q created\n", ws.ID)
}

func workspaceDelete(client *Client, jsonOutput bool, args []string) {
	fs := flag.NewFlagSet("workspace delete", flag.ExitOnError)
	if err := parseFlags(fs, args); err != nil {
		Die("%v", err)
	}

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: rementorctl workspace delete <id>")
		os.Exit(1)
	}
	id := fs.Arg(0)

	operation, err := client.DeleteWorkspaceWithMetadata(context.Background(), id)
	if err != nil {
		Die("%v", err)
	}

	if jsonOutput {
		PrintJSON(map[string]any{"status": "deleted", "workspace": id, "operation": operation})
		return
	}
	fmt.Printf("workspace %q deleted\n", id)
}
