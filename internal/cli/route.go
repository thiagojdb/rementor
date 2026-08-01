package cli

import (
	stdctx "context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

// RouteCmd exposes the normalized route lifecycle. The positional form is
// convenient for humans while --workspace remains available to scripts.
func RouteCmd(client *Client, jsonOutput bool, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: rementorctl route <get|conflicts|resolve|plan|apply|sync> [options]")
		os.Exit(1)
	}
	switch args[0] {
	case "get":
		routeGet(client, jsonOutput, args[1:])
	case "conflicts":
		routeConflicts(client, jsonOutput, args[1:])
	case "resolve":
		routeResolve(client, jsonOutput, args[1:])
	case "plan":
		routePlan(client, jsonOutput, args[1:])
	case "apply":
		routeApply(client, jsonOutput, args[1:])
	case "sync":
		routeSync(client, jsonOutput, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown route subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func routeConflicts(client *Client, jsonOutput bool, args []string) {
	fs := flag.NewFlagSet("route conflicts", flag.ExitOnError)
	workspace := fs.String("workspace", "", "workspace/environment ID")
	if err := parseFlags(fs, args); err != nil {
		Die("%v", err)
	}
	wsID := firstRouteArg(fs, *workspace)
	if wsID == "" {
		Die("workspace is required")
	}
	result, err := client.GetRouteConflicts(stdctx.Background(), wsID)
	if err != nil {
		Die("%v", err)
	}
	if jsonOutput {
		PrintJSON(result)
		return
	}
	fmt.Printf("workspace %q route version %d (%d conflict(s))\n", wsID, result.RouteVersion, len(result.Conflicts))
	if len(result.Conflicts) == 0 {
		return
	}
	w := NewTabWriter()
	fmt.Fprintln(w, "WORKSPACE\tENVIRONMENT\tHOST\tWINNING_APP\tWINNING_SERVICE\tSHADOWED_APP\tSHADOWED_SERVICE\tWINNING_PATTERN\tSHADOWED_PATTERN\tPRECEDENCE_REASON\tINTENTIONAL")
	for _, conflict := range result.Conflicts {
		shadowed := conflict.ShadowedAppID
		if shadowed == "" {
			shadowed = conflict.ConflictingAppID
		}
		winningPattern := conflict.WinningPattern
		if winningPattern == "" {
			winningPattern = conflict.Pattern
		}
		shadowedPattern := conflict.ShadowedPattern
		if shadowedPattern == "" {
			shadowedPattern = conflict.Pattern
		}
		precedenceReason := conflict.PrecedenceReason
		if precedenceReason == "" {
			precedenceReason = conflict.Reason
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%v\n",
			conflict.WorkspaceID, conflict.Environment, conflict.PublicHost,
			conflict.WinningAppID, conflict.WinningServiceID, shadowed, conflict.ShadowedServiceID,
			winningPattern, shadowedPattern, precedenceReason, conflict.Intentional)
	}
	w.Flush()
}

func routeGet(client *Client, jsonOutput bool, args []string) {
	fs := flag.NewFlagSet("route get", flag.ExitOnError)
	workspace := fs.String("workspace", "", "workspace/environment ID")
	if err := parseFlags(fs, args); err != nil {
		Die("%v", err)
	}
	wsID := firstRouteArg(fs, *workspace)
	if wsID == "" {
		Die("workspace is required")
	}
	result, err := client.GetRoutes(stdctx.Background(), wsID)
	if err != nil {
		Die("%v", err)
	}
	if jsonOutput {
		PrintJSON(result)
		return
	}
	fmt.Printf("workspace %q route version %d (%d routes)\n", wsID, result.RouteVersion, len(result.Routes))
	w := NewTabWriter()
	fmt.Fprintln(w, "HOST\tPATTERN\tAPP\tMODE\tTARGET\tPRECEDENCE")
	for _, route := range result.Routes {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\n", route.PublicHost, route.Pattern, route.CanonicalAppID, route.DesiredMode, route.Target, route.Precedence)
	}
	w.Flush()
	if len(result.Warnings) > 0 || len(result.Conflicts) > 0 {
		fmt.Fprintf(os.Stderr, "warnings: %d, conflicts: %d\n", len(result.Warnings), len(result.Conflicts))
	}
}

func routeResolve(client *Client, jsonOutput bool, args []string) {
	fs := flag.NewFlagSet("route resolve", flag.ExitOnError)
	workspace := fs.String("workspace", "", "workspace/environment ID")
	host := fs.String("host", "", "public host")
	path := fs.String("path", "/", "request path")
	if err := parseFlags(fs, args); err != nil {
		Die("%v", err)
	}
	wsID := firstRouteArg(fs, *workspace)
	if wsID == "" {
		Die("workspace is required")
	}
	if fs.NArg() > 1 && *host == "" {
		*host = fs.Arg(1)
	}
	if fs.NArg() > 2 && *path == "/" {
		*path = fs.Arg(2)
	}
	result, err := client.ResolveRoute(stdctx.Background(), wsID, *host, *path)
	if err != nil {
		Die("%v", err)
	}
	if jsonOutput {
		PrintJSON(result)
		return
	}
	fmt.Printf("%s%s -> %s (%s, precedence %d)\n", result.Host, result.Path, result.Target, result.MatchingPattern, result.Precedence)
}

func routePlan(client *Client, jsonOutput bool, args []string) {
	fs := flag.NewFlagSet("route plan", flag.ExitOnError)
	workspace := fs.String("workspace", "", "workspace/environment ID")
	mode := fs.String("mode", "", "desired route mode: local or remote")
	pattern := fs.String("pattern", "", "optional route pattern; an empty value clears it")
	clearPattern := fs.Bool("clear-pattern", false, "clear the configured route pattern")
	expected := fs.Uint64("expected-version", 0, "expected route version")
	if err := parseFlags(fs, args); err != nil {
		Die("%v", err)
	}
	wsID, appID := routeWorkspaceApp(fs, *workspace)
	if wsID == "" || appID == "" {
		Die("workspace and application are required")
	}
	if *mode == "" {
		Die("--mode is required")
	}
	var routePattern *string
	if *clearPattern || *pattern != "" {
		value := *pattern
		routePattern = &value
	}
	result, err := client.PlanRoute(stdctx.Background(), PlanRouteRequest{WorkspaceID: wsID, ApplicationRef: appID, DesiredMode: *mode, RoutePattern: routePattern, ExpectedVersion: *expected})
	if err != nil {
		Die("%v", err)
	}
	if jsonOutput {
		PrintJSON(result)
		return
	}
	fmt.Printf("route plan %s: version %d, %d change(s), %d warning(s), %d conflict(s)\n", result.Fingerprint, result.BaseRouteVersion, len(result.Changes), len(result.Warnings), len(result.Conflicts))
}

func routeApply(client *Client, jsonOutput bool, args []string) {
	fs := flag.NewFlagSet("route apply", flag.ExitOnError)
	workspace := fs.String("workspace", "", "workspace/environment ID")
	mode := fs.String("mode", "", "desired route mode: local or remote")
	pattern := fs.String("pattern", "", "optional route pattern; an empty value clears it")
	clearPattern := fs.Bool("clear-pattern", false, "clear the configured route pattern")
	idempotency := fs.String("idempotency-key", "", "idempotency key")
	planPath := fs.String("plan", "", "JSON route plan file (or - for stdin)")
	expected := fs.Uint64("expected-version", 0, "expected route version")
	if err := parseFlags(fs, args); err != nil {
		Die("%v", err)
	}
	wsID, appID := routeWorkspaceApp(fs, *workspace)
	request := ApplyRouteRequest{WorkspaceID: wsID, ApplicationRef: appID, DesiredMode: *mode, ExpectedVersion: *expected, IdempotencyKey: *idempotency}
	if *clearPattern || *pattern != "" {
		value := *pattern
		request.RoutePattern = &value
	}
	if *planPath != "" {
		payload, err := readRoutePlan(*planPath)
		if err != nil {
			Die("%v", err)
		}
		var plan RoutePlanDTO
		if err := json.Unmarshal(payload, &plan); err != nil {
			Die("invalid route plan: %v", err)
		}
		request.Plan = &plan
		if request.WorkspaceID == "" {
			request.WorkspaceID = plan.WorkspaceID
		}
		if request.ApplicationRef == "" {
			request.ApplicationRef = plan.ApplicationID
		}
	}
	if request.WorkspaceID == "" {
		Die("workspace is required")
	}
	if request.Plan == nil && (request.ApplicationRef == "" || request.DesiredMode == "") {
		Die("application and --mode are required unless --plan is supplied")
	}
	result, err := client.ApplyRoute(stdctx.Background(), request)
	if err != nil {
		Die("%v", err)
	}
	if jsonOutput {
		PrintJSON(result)
		return
	}
	state := "unchanged"
	if result.Changed {
		state = "applied"
	}
	fmt.Printf("route %s for %q (verification: %s)\n", state, request.ApplicationRef, result.VerificationStatus)
}

func routeSync(client *Client, jsonOutput bool, args []string) {
	fs := flag.NewFlagSet("route sync", flag.ExitOnError)
	workspace := fs.String("workspace", "", "workspace/environment ID")
	if err := parseFlags(fs, args); err != nil {
		Die("%v", err)
	}
	wsID := firstRouteArg(fs, *workspace)
	if wsID == "" {
		Die("workspace is required")
	}
	result, err := client.SyncRoute(stdctx.Background(), wsID, true, "")
	if err != nil {
		Die("%v", err)
	}
	if jsonOutput {
		PrintJSON(result)
		return
	}
	fmt.Printf("route sync %s for %q\n", result.Status, wsID)
}

func firstRouteArg(fs *flag.FlagSet, workspace string) string {
	if workspace != "" {
		return workspace
	}
	if fs.NArg() > 0 {
		return fs.Arg(0)
	}
	return ""
}

func routeWorkspaceApp(fs *flag.FlagSet, workspace string) (string, string) {
	wsID := workspace
	appID := ""
	if fs.NArg() > 0 {
		if wsID == "" {
			wsID = fs.Arg(0)
			if fs.NArg() > 1 {
				appID = fs.Arg(1)
			}
		} else {
			appID = fs.Arg(0)
		}
	}
	return wsID, appID
}

func readRoutePlan(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}
