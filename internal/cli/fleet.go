package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type fleetAction struct {
	Name        string
	Topology    *rementorv1.FleetTopology
	Expected    uint64
	ReviewToken string
}

func (c *Client) fleet(ctx context.Context, action fleetAction) (proto.Message, error) {
	switch action.Name {
	case "show":
		response, err := c.rpc.GetFleetConfiguration(ctx, connect.NewRequest(&rementorv1.GetFleetConfigurationRequest{}))
		if err != nil {
			return nil, apiError(err)
		}
		return response.Msg, nil
	case "review":
		response, err := c.rpc.ReviewFleetConfiguration(ctx, connect.NewRequest(&rementorv1.ReviewFleetConfigurationRequest{Topology: action.Topology, ExpectedRevision: action.Expected}))
		if err != nil {
			return nil, apiError(err)
		}
		return response.Msg, nil
	case "save":
		response, err := c.rpc.SaveFleetConfiguration(ctx, connect.NewRequest(&rementorv1.SaveFleetConfigurationRequest{Topology: action.Topology, ExpectedRevision: action.Expected, ReviewToken: action.ReviewToken}))
		if err != nil {
			return nil, apiError(err)
		}
		return response.Msg, nil
	default:
		return nil, fmt.Errorf("unknown fleet action %q", action.Name)
	}
}

func FleetCmd(client *Client, args []string) {
	if err := runFleet(client, os.Stdout, args); err != nil {
		Die("%v", err)
	}
}

func runFleet(client *Client, output io.Writer, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: fleet <show|review|save>")
	}
	fs := flag.NewFlagSet("fleet "+args[0], flag.ContinueOnError)
	file := fs.String("file", "", "fleet topology JSON file")
	expected := fs.Uint64("expected-revision", 0, "revision from fleet show")
	token := fs.String("review-token", "", "token from reviewing this exact topology and revision")
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional argument")
	}
	action := fleetAction{Name: args[0], Expected: *expected, ReviewToken: *token}
	switch action.Name {
	case "show":
		if *file != "" || *token != "" {
			return fmt.Errorf("fleet show does not accept a file or review token")
		}
	case "review", "save":
		given := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "expected-revision" {
				given = true
			}
		})
		if !given {
			return fmt.Errorf("--expected-revision is required; read fleet show first")
		}
		if *file == "" {
			return fmt.Errorf("%s requires --file", action.Name)
		}
		if action.Name == "save" && *token == "" {
			return fmt.Errorf("save requires --review-token; review this exact file and revision first")
		}
		reader, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer reader.Close()
		raw, err := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
		if err != nil {
			return err
		}
		if len(raw) > 1<<20 {
			return fmt.Errorf("fleet topology exceeds 1 MiB")
		}
		action.Topology = &rementorv1.FleetTopology{}
		if err := protojson.Unmarshal(raw, action.Topology); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown fleet action %q", action.Name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := client.fleet(ctx, action)
	if err != nil {
		return err
	}
	raw, err := protojson.MarshalOptions{Indent: "  ", EmitUnpopulated: true}.Marshal(result)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, string(raw))
	return err
}

func (s *mcpServer) toolFleet(name string, args map[string]any) (any, error) {
	action := fleetAction{}
	switch name {
	case "rementor.fleet_show":
		action.Name = "show"
	case "rementor.fleet_review", "rementor.fleet_save":
		action.Name = "review"
		if name == "rementor.fleet_save" {
			action.Name = "save"
			action.ReviewToken, _ = args["review_token"].(string)
			action.ReviewToken = strings.TrimSpace(action.ReviewToken)
			if action.ReviewToken == "" {
				return nil, fmt.Errorf("review_token is required; review this exact topology and revision first")
			}
		}
		revisionString, _ := args["expected_revision"].(string)
		revision, err := strconv.ParseUint(revisionString, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("expected_revision must be an unsigned decimal string")
		}
		action.Expected = revision
		raw, err := json.Marshal(args["topology"])
		if err != nil {
			return nil, err
		}
		if len(raw) > 1<<20 {
			return nil, fmt.Errorf("fleet topology exceeds 1 MiB")
		}
		action.Topology = &rementorv1.FleetTopology{}
		if err := protojson.Unmarshal(raw, action.Topology); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown fleet tool %q", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := s.client.fleet(ctx, action)
	if err != nil {
		return nil, err
	}
	raw, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(result)
	if err != nil {
		return nil, err
	}
	var payload any
	err = json.Unmarshal(raw, &payload)
	return payload, err
}

func fleetTools() []map[string]any {
	revision := map[string]any{"type": "string", "description": "Current revision as an unsigned decimal string from fleet_show."}
	topology := map[string]any{"type": "object", "description": "version: 1; nodes, origins, targets, routes, instances, sharedInstance and localInstance. Use the topology object from fleet_show."}
	return []map[string]any{
		toolSchema("rementor.fleet_show", "Read the saved fleet topology and revision.", objectSchema(map[string]any{}, nil)),
		toolSchema("rementor.fleet_review", "Validate a candidate topology and inspect before/after route selections. Returns a token for saving these exact changes.", objectSchema(map[string]any{"expected_revision": revision, "topology": topology}, []string{"expected_revision", "topology"})),
		toolSchema("rementor.fleet_save", "Save the reviewed topology. This stores fleet intent; active workspace routes use the inheritance controls.", objectSchema(map[string]any{"expected_revision": revision, "topology": topology, "review_token": map[string]any{"type": "string", "description": "Token returned by fleet_review for this topology and revision."}}, []string{"expected_revision", "topology", "review_token"})),
	}
}
