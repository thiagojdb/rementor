package cli

import (
	stdctx "context"
	"flag"
	"fmt"
	"os"
)

// AnnounceCmd implements the announce subcommand: idempotent register + activate.
func AnnounceCmd(client *Client, jsonOutput bool, args []string) {
	fs := flag.NewFlagSet("announce", flag.ExitOnError)
	wsID := fs.String("workspace", "", "workspace ID (required)")
	appID := fs.String("app", "", "application ID (required)")
	port := fs.Int("port", 0, "local port (required)")
	wsType := fs.String("type", "routing", "workspace type for creation: routing|local-apps")
	localDomain := fs.String("local-domain", "", "local domain (required when creating a routing workspace)")
	path := fs.String("path", "", "URL path for routing workspaces (default: /<app-id>)")
	domain := fs.String("domain", "", "hostname for local-apps workspaces (default: <app-id>.localhost)")
	remoteBaseURL := fs.String("remote-base-url", "", "per-app remote base URL")
	context := fs.String("context", "", "application context path")
	name := fs.String("name", "", "display name for the application")
	health := fs.String("health", "", "health endpoint")
	appIdentityID := fs.String("app-id", "", "canonical application identity (defaults to --app)")
	serviceID := fs.String("service-id", "", "canonical service identity")
	repository := fs.String("repository", "", "source repository identity")
	aliases := fs.String("aliases", "", "comma-separated application aliases")
	noActivate := fs.Bool("no-activate", false, "skip toggling app to local/active after registration")
	if err := parseFlags(fs, args); err != nil {
		Die("%v", err)
	}

	if *wsID == "" {
		Die("--workspace is required")
	}
	if *appID == "" {
		Die("--app is required")
	}
	if *port == 0 {
		Die("--port is required")
	}

	// Step 1: Ensure workspace exists.
	ws, err := ensureWorkspace(client, *wsID, *wsType, *localDomain)
	if err != nil {
		Die("%v", err)
	}

	// Apply defaults for path / domain based on workspace type.
	resolvedPath := *path
	resolvedDomain := *domain
	if ws.Type == "local-apps" {
		if resolvedDomain == "" {
			resolvedDomain = *appID + ".localhost"
		}
	} else {
		if resolvedPath == "" {
			resolvedPath = "/" + *appID
		}
	}

	input := ApplicationConfigInput{
		ID:            *appID,
		AppID:         *appIdentityID,
		ServiceID:     *serviceID,
		Repository:    *repository,
		Aliases:       splitAliases(*aliases),
		Name:          *name,
		Path:          resolvedPath,
		Domain:        resolvedDomain,
		RemoteBaseUrl: *remoteBaseURL,
		Port:          *port,
		Health:        *health,
		Context:       *context,
	}

	upserted, err := client.UpsertApplication(stdctx.Background(), *wsID, input)
	if err != nil {
		Die("failed to register application: %v", err)
	}
	regStatus := "updated"
	if upserted.Created {
		regStatus = "registered"
	}

	// Step 3: Activate unless --no-activate.
	activated := false
	if !*noActivate {
		app, err := client.GetApplication(stdctx.Background(), *wsID, *appID)
		if err != nil {
			Die("failed to get app after registration: %v", err)
		}
		if !app.Active {
			app, err = client.ToggleApplication(stdctx.Background(), *wsID, *appID)
			if err != nil {
				Die("failed to activate app: %v", err)
			}
			activated = true
		}
	}

	// Step 4: Build URL.
	appURL := buildAppURL(ws, resolvedPath, resolvedDomain)

	if jsonOutput {
		PrintJSON(map[string]any{
			"workspace": *wsID,
			"app":       *appID,
			"status":    regStatus,
			"activated": activated,
			"url":       appURL,
			"operation": upserted.Operation,
		})
		return
	}

	fmt.Printf("workspace: %s\n", *wsID)
	fmt.Printf("app:       %s\n", *appID)
	fmt.Printf("status:    %s\n", regStatus)
	if !*noActivate {
		fmt.Printf("activated: %v\n", activated)
	}
	fmt.Printf("url:       %s\n", appURL)
}

// ensureWorkspace fetches the workspace; if 404, creates it.
func ensureWorkspace(client *Client, wsID, wsType, localDomain string) (*WorkspaceDTO, error) {
	ws, err := client.GetWorkspace(stdctx.Background(), wsID)
	if err == nil {
		return &ws, nil
	}

	apiErr, ok := err.(*APIError)
	if !ok || apiErr.StatusCode != 404 {
		return nil, fmt.Errorf("failed to get workspace %q: %w", wsID, err)
	}

	// Workspace doesn't exist — create it.
	if wsType == "routing" && localDomain == "" {
		return nil, fmt.Errorf("--local-domain is required when creating a routing workspace")
	}

	req := CreateWorkspaceRequest{
		ID:          wsID,
		Type:        wsType,
		LocalDomain: localDomain,
	}

	created, err := client.CreateWorkspace(stdctx.Background(), req)
	if err != nil {
		// Handle 409 race: another process created it simultaneously.
		apiErr2, ok2 := err.(*APIError)
		if ok2 && apiErr2.StatusCode == 409 {
			// Re-fetch.
			ws2, err2 := client.GetWorkspace(stdctx.Background(), wsID)
			if err2 != nil {
				return nil, fmt.Errorf("workspace creation conflict, re-fetch failed: %w", err2)
			}
			return &ws2, nil
		}
		return nil, fmt.Errorf("failed to create workspace %q: %w", wsID, err)
	}

	return &created, nil
}

// buildAppURL constructs the public URL for the announced app.
func buildAppURL(ws *WorkspaceDTO, path, domain string) string {
	if ws.Type == "local-apps" {
		if domain == "" {
			return ""
		}
		return "http://" + domain
	}
	// routing workspace
	localDomain := ""
	if ws.Routing != nil {
		localDomain = ws.Routing.LocalDomain
	}
	if localDomain == "" {
		return ""
	}
	return "http://" + localDomain + path
}

// printAnnounceUsage prints usage for the announce command.
func printAnnounceUsage() {
	fmt.Fprintln(os.Stderr, `usage: rementorctl announce --workspace <id> --app <id> --port <N> [options]

Options:
  --workspace <id>       workspace ID (required)
  --app <id>             application ID (required)
  --port <N>             local port (required)
  --type <type>          workspace type for creation: routing|local-apps (default: routing)
  --local-domain <d>     local domain (required when creating a routing workspace)
  --path <path>          URL path for routing workspaces (default: /<app-id>)
  --domain <d>           hostname for local-apps workspaces (default: <app-id>.localhost)
  --remote-base-url <u>  per-app remote base URL
  --context <path>       application context path
  --name <name>          display name for the application
  --health <endpoint>    health endpoint
  --app-id <id>          canonical application identity
  --service-id <id>      canonical service identity
  --repository <name>    source repository identity
  --aliases <a,b>        comma-separated aliases
  --no-activate          skip toggling app to local/active`)
	os.Exit(1)
}
