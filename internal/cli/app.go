package cli

import (
	stdctx "context"
	"flag"
	"fmt"
	"os"
	"strings"
)

// AppCmd dispatches app subcommands.
func AppCmd(client *Client, jsonOutput bool, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: rementorctl app <list|register|unregister|toggle|alias|resolve> [options]")
		os.Exit(1)
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list":
		appList(client, jsonOutput, rest)
	case "register":
		appRegister(client, jsonOutput, rest)
	case "unregister":
		appUnregister(client, jsonOutput, rest)
	case "toggle":
		appToggle(client, jsonOutput, rest)
	case "alias":
		appAlias(client, jsonOutput, rest)
	case "resolve":
		appResolve(client, jsonOutput, rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown app subcommand: %s\n", sub)
		os.Exit(1)
	}
}

func appAlias(client *Client, jsonOutput bool, args []string) {
	fs := flag.NewFlagSet("app alias", flag.ExitOnError)
	if err := parseFlags(fs, args); err != nil {
		Die("%v", err)
	}
	if fs.NArg() < 3 {
		fmt.Fprintln(os.Stderr, "usage: rementorctl app alias <workspace> <app> <alias>")
		os.Exit(1)
	}
	app, err := client.RegisterApplicationAlias(stdctx.Background(), fs.Arg(0), fs.Arg(1), fs.Arg(2))
	if err != nil {
		Die("%v", err)
	}
	if jsonOutput {
		PrintJSON(app)
		return
	}
	fmt.Printf("alias %q registered for app %q\n", fs.Arg(2), app.ID)
}

func appResolve(client *Client, jsonOutput bool, args []string) {
	fs := flag.NewFlagSet("app resolve", flag.ExitOnError)
	if err := parseFlags(fs, args); err != nil {
		Die("%v", err)
	}
	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "usage: rementorctl app resolve <workspace> <app-or-alias>")
		os.Exit(1)
	}
	app, err := client.ResolveApplication(stdctx.Background(), fs.Arg(0), fs.Arg(1))
	if err != nil {
		Die("%v", err)
	}
	if jsonOutput {
		PrintJSON(app)
		return
	}
	fmt.Printf("%s -> %s\n", fs.Arg(1), app.ID)
}

func appList(client *Client, jsonOutput bool, args []string) {
	fs := flag.NewFlagSet("app list", flag.ExitOnError)
	if err := parseFlags(fs, args); err != nil {
		Die("%v", err)
	}

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: rementorctl app list <workspace>")
		os.Exit(1)
	}
	ws := fs.Arg(0)

	apps, err := client.ListApplications(stdctx.Background(), ws)
	if err != nil {
		Die("%v", err)
	}

	if jsonOutput {
		PrintJSON(apps)
		return
	}

	w := NewTabWriter()
	fmt.Fprintln(w, "ID\tNAME\tPORT\tPATH\tDOMAIN\tACTIVE\tHEALTH")
	for _, app := range apps {
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%v\t%s\n",
			app.ID, app.Name, app.Port, app.Path, app.Domain, app.Active, app.HealthStatus)
	}
	w.Flush()
}

func appRegister(client *Client, jsonOutput bool, args []string) {
	fs := flag.NewFlagSet("app register", flag.ExitOnError)
	port := fs.Int("port", 0, "local port (required)")
	path := fs.String("path", "", "URL path (e.g. /myapp)")
	domain := fs.String("domain", "", "hostname for local-apps workspaces")
	remoteBaseURL := fs.String("remote-base-url", "", "per-app remote base URL")
	context := fs.String("context", "", "application context path")
	name := fs.String("name", "", "display name")
	health := fs.String("health", "", "health endpoint (default: actuator/health)")
	appIdentityID := fs.String("app-id", "", "canonical application identity (defaults to <app>)")
	serviceID := fs.String("service-id", "", "canonical service identity")
	repository := fs.String("repository", "", "source repository identity")
	aliases := fs.String("aliases", "", "comma-separated application aliases")
	if err := parseFlags(fs, args); err != nil {
		Die("%v", err)
	}

	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "usage: rementorctl app register <workspace> <app> [options]")
		os.Exit(1)
	}
	wsID := fs.Arg(0)
	appID := fs.Arg(1)

	if *port == 0 {
		Die("--port is required")
	}

	input := ApplicationConfigInput{
		ID:            appID,
		AppID:         *appIdentityID,
		ServiceID:     *serviceID,
		Repository:    *repository,
		Aliases:       splitAliases(*aliases),
		Name:          *name,
		Path:          *path,
		Domain:        *domain,
		RemoteBaseUrl: *remoteBaseURL,
		Port:          *port,
		Health:        *health,
		Context:       *context,
	}

	result, err := client.UpsertApplication(stdctx.Background(), wsID, input)
	if err != nil {
		Die("%v", err)
	}
	status := "updated"
	if result.Created {
		status = "registered"
	}

	if jsonOutput {
		PrintJSON(map[string]any{"status": status, "workspace": wsID, "application": result.Application})
		return
	}
	fmt.Printf("app %q %s in workspace %q\n", appID, status, wsID)
}

func splitAliases(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func appUnregister(client *Client, jsonOutput bool, args []string) {
	fs := flag.NewFlagSet("app unregister", flag.ExitOnError)
	if err := parseFlags(fs, args); err != nil {
		Die("%v", err)
	}

	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "usage: rementorctl app unregister <workspace> <app>")
		os.Exit(1)
	}
	wsID := fs.Arg(0)
	appID := fs.Arg(1)

	if err := client.DeleteApplication(stdctx.Background(), wsID, appID); err != nil {
		Die("%v", err)
	}

	if jsonOutput {
		PrintJSON(map[string]string{"status": "unregistered", "app": appID, "workspace": wsID})
		return
	}
	fmt.Printf("app %q unregistered from workspace %q\n", appID, wsID)
}

func appToggle(client *Client, jsonOutput bool, args []string) {
	fs := flag.NewFlagSet("app toggle", flag.ExitOnError)
	if err := parseFlags(fs, args); err != nil {
		Die("%v", err)
	}

	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "usage: rementorctl app toggle <workspace> <app>")
		os.Exit(1)
	}
	wsID := fs.Arg(0)
	appID := fs.Arg(1)

	app, err := client.ToggleApplication(stdctx.Background(), wsID, appID)
	if err != nil {
		Die("%v", err)
	}

	if jsonOutput {
		PrintJSON(app)
		return
	}
	activeStr := "remote"
	if app.Active {
		activeStr = "local"
	}
	fmt.Printf("app %q is now %s\n", appID, activeStr)
}

// upsertApp returns the updated app list and a status string: "registered", "updated", or "unchanged".
func upsertApp(existing []ApplicationDTO, input ApplicationConfigInput) ([]ApplicationConfigInput, string) {
	result := make([]ApplicationConfigInput, 0, len(existing)+1)
	status := "registered"

	for _, app := range existing {
		if app.ID == input.ID {
			// Check if anything changed.
			same := app.Port == input.Port &&
				app.Path == input.Path &&
				app.Domain == input.Domain &&
				(input.RemoteBaseUrl == "" || app.RemoteBaseUrl == input.RemoteBaseUrl) &&
				(input.Context == "" || app.Context == input.Context) &&
				app.Health == input.Health &&
				(input.Name == "" || app.Name == input.Name) &&
				(input.AppID == "" || app.AppID == input.AppID) &&
				(input.ServiceID == "" || app.ServiceID == input.ServiceID) &&
				(input.Repository == "" || app.Repository == input.Repository) &&
				(input.Aliases == nil || sameAliases(app.Aliases, input.Aliases))

			if same {
				return nil, "unchanged"
			}

			// Merge: keep name if not specified.
			if input.Name == "" {
				input.Name = app.Name
			}
			if input.RemoteBaseUrl == "" {
				input.RemoteBaseUrl = app.RemoteBaseUrl
			}
			if input.Context == "" {
				input.Context = app.Context
			}
			if input.AppID == "" {
				input.AppID = app.AppID
			}
			if input.ServiceID == "" {
				input.ServiceID = app.ServiceID
			}
			if input.Repository == "" {
				input.Repository = app.Repository
			}
			if input.Aliases == nil {
				input.Aliases = append([]string(nil), app.Aliases...)
			}
			status = "updated"
			result = append(result, input)
			continue
		}
		result = append(result, ApplicationConfigInput{
			ID:            app.ID,
			AppID:         app.AppID,
			ServiceID:     app.ServiceID,
			Repository:    app.Repository,
			Aliases:       append([]string(nil), app.Aliases...),
			Name:          app.Name,
			Path:          app.Path,
			Domain:        app.Domain,
			RemoteBaseUrl: app.RemoteBaseUrl,
			Port:          app.Port,
			Health:        app.Health,
			Context:       app.Context,
		})
	}

	if status == "registered" {
		result = append(result, input)
	}

	return result, status
}

func sameAliases(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if strings.ToLower(strings.TrimSpace(left[i])) != strings.ToLower(strings.TrimSpace(right[i])) {
			return false
		}
	}
	return true
}
