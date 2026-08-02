package cli

import (
	stdctx "context"
	"flag"
	"fmt"
	"os"
)

// URLCmd resolves the stable browser entry point for a canonical app ID or
// alias. The command accepts both flags and positional values:
//
//	rementorctl url --workspace desenvolvimento --app front-giss-v2
//	rementorctl url desenvolvimento front-giss-v2
func URLCmd(client *Client, jsonOutput bool, args []string) {
	fs := flag.NewFlagSet("url", flag.ExitOnError)
	workspace := fs.String("workspace", "", "workspace/environment ID")
	app := fs.String("app", "", "canonical application ID or alias")
	if err := parseFlags(fs, args); err != nil {
		Die("%v", err)
	}
	wsID, appRef := *workspace, *app
	if wsID == "" && fs.NArg() > 0 {
		wsID = fs.Arg(0)
	}
	if appRef == "" && fs.NArg() > 1 {
		appRef = fs.Arg(1)
	}
	if wsID == "" || appRef == "" {
		fmt.Fprintln(os.Stderr, "usage: rementorctl url --workspace <workspace> --app <app-or-alias>")
		os.Exit(1)
	}
	result, err := client.ResolveBrowserURL(stdctx.Background(), wsID, appRef)
	if err != nil {
		Die("%v", err)
	}
	if jsonOutput {
		PrintJSON(result)
		return
	}
	fmt.Printf("%s (%s, route version %d)\n", result.URL, result.EffectiveMode, result.RouteVersion)
}
