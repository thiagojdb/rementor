package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/thiagojdb/rementor/internal/routing"
)

// TopologyCmd inspects experimental fleet intent without contacting the daemon
// or modifying its persisted workspace state.
func TopologyCmd(jsonOutput bool, args []string) {
	if err := runTopology(os.Stdout, jsonOutput, args); err != nil {
		Die("%v", err)
	}
}

func runTopology(output io.Writer, jsonOutput bool, args []string) error {
	if len(args) == 0 || args[0] != "resolve" {
		return fmt.Errorf("usage: rementorctl topology resolve --file <topology.json> --url <public-url> [--instance <id>] [--unavailable-targets <ids>] [--unavailable-nodes <ids>]")
	}
	fs := flag.NewFlagSet("topology resolve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fileName := fs.String("file", "", "experimental topology JSON file")
	publicURL := fs.String("url", "", "browser URL to inspect")
	instanceID := fs.String("instance", "", "serving Rementor instance (default: shared instance)")
	unavailableTargets := fs.String("unavailable-targets", "", "comma-separated target IDs to simulate as unavailable")
	unavailableNodes := fs.String("unavailable-nodes", "", "comma-separated node IDs to simulate as unavailable")
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 || *fileName == "" || *publicURL == "" {
		return fmt.Errorf("--file and --url are required; positional arguments are not supported")
	}
	file, err := os.Open(*fileName)
	if err != nil {
		return err
	}
	defer file.Close()
	resolver, err := routing.Load(file)
	if err != nil {
		return err
	}
	result, resolveErr := resolver.Resolve(*publicURL, *instanceID, routing.Availability{
		UnavailableTargets: topologyIDs(*unavailableTargets),
		UnavailableNodes:   topologyIDs(*unavailableNodes),
	})
	if resolveErr != nil && !errors.Is(resolveErr, routing.ErrTargetUnavailable) {
		return resolveErr
	}
	status := "selected"
	message := ""
	if resolveErr != nil {
		status, message = "unavailable", resolveErr.Error()
	}
	if jsonOutput {
		if err := json.NewEncoder(output).Encode(struct {
			Status     string             `json:"status"`
			Resolution routing.Resolution `json:"resolution"`
			Error      string             `json:"error,omitempty"`
		}{status, result, message}); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(output, "%s -> %s (%s, instance %s, source %s)\n", result.PublicURL, result.Target.ID, status, result.InstanceID, result.SelectionSource); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(output, "route: %s; inheritance: %s; target owner: %s; endpoint: %s\n", strings.Join(result.RouteChain, " -> "), strings.Join(result.InstanceChain, " -> "), result.Target.NodeID, result.Target.URL); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(output, "Inspection only: availability is simulated; no network request or route change was made."); err != nil {
			return err
		}
	}
	return resolveErr
}

func topologyIDs(value string) map[string]bool {
	ids := map[string]bool{}
	for _, id := range strings.Split(value, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids[id] = true
		}
	}
	return ids
}
