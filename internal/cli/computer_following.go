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

func (c *Client) computerFollowing(ctx context.Context, action, peer string, revision uint64, workspace string, workspaceVersion uint64) (proto.Message, error) {
	switch action {
	case "status":
		result, err := c.rpc.GetComputerFollowing(ctx, connect.NewRequest(&rementorv1.GetComputerFollowingRequest{}))
		if err != nil {
			return nil, apiError(err)
		}
		return result.Msg, nil
	case "follow":
		result, err := c.rpc.FollowComputer(ctx, connect.NewRequest(&rementorv1.FollowComputerRequest{Fingerprint: peer, ExpectedRevision: revision}))
		if err != nil {
			return nil, apiError(err)
		}
		return result.Msg, nil
	case "sync":
		result, err := c.rpc.SyncComputerConfiguration(ctx, connect.NewRequest(&rementorv1.SyncComputerConfigurationRequest{}))
		if err != nil {
			return nil, apiError(err)
		}
		return result.Msg, nil
	case "exclude", "include":
		result, err := c.rpc.SetEnvironmentFollowing(ctx, connect.NewRequest(&rementorv1.SetEnvironmentFollowingRequest{WorkspaceId: workspace, Following: action == "include", ExpectedRevision: revision, ExpectedWorkspaceVersion: workspaceVersion}))
		if err != nil {
			return nil, apiError(err)
		}
		return result.Msg, nil
	case "stop":
		result, err := c.rpc.StopFollowingComputer(ctx, connect.NewRequest(&rementorv1.StopFollowingComputerRequest{ExpectedRevision: revision}))
		if err != nil {
			return nil, apiError(err)
		}
		return result.Msg, nil
	default:
		return nil, fmt.Errorf("unknown computer-following action %q", action)
	}
}

func ComputerFollowingCmd(client *Client, args []string) {
	if err := runComputerFollowing(client, os.Stdout, args); err != nil {
		Die("%v", err)
	}
}
func runComputerFollowing(client *Client, output io.Writer, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: computer-following <status|follow|sync|stop|exclude|include>")
	}
	action := args[0]
	fs := flag.NewFlagSet("computer-following "+action, flag.ContinueOnError)
	peer := fs.String("peer", "", "paired source computer fingerprint")
	workspace := fs.String("workspace", "", "environment to exclude or include")
	workspaceVersion := fs.Uint64("expected-workspace-version", 0, "current environment route version")
	revision := fs.Uint64("expected-revision", 0, "revision from computer-following status")
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("computer-following does not take environment or session arguments")
	}
	given := false
	versionGiven := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "expected-workspace-version" {
			versionGiven = true
		}
		if f.Name == "expected-revision" {
			given = true
		}
	})
	if (action == "follow" || action == "stop" || action == "exclude" || action == "include") && !given {
		return fmt.Errorf("--expected-revision is required; read computer-following status first")
	}
	if action == "follow" && *peer == "" {
		return fmt.Errorf("--peer is required")
	}
	if (action == "exclude" || action == "include") && (*workspace == "" || !versionGiven) {
		return fmt.Errorf("--workspace and --expected-workspace-version are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := client.computerFollowing(ctx, action, *peer, *revision, *workspace, *workspaceVersion)
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

func (s *mcpServer) toolComputerFollowing(name string, args map[string]any) (any, error) {
	action := strings.TrimPrefix(name, "rementor.computer_")
	revision := uint64(0)
	if action == "follow" || action == "stop" || action == "exclude" || action == "include" {
		var err error
		revision, err = strconv.ParseUint(requiredString(args, "expected_revision"), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("expected_revision must be an unsigned decimal string")
		}
	}
	workspace := optionalString(args, "workspace")
	workspaceVersion := uint64(0)
	if action == "exclude" || action == "include" {
		var err error
		workspaceVersion, err = strconv.ParseUint(requiredString(args, "expected_workspace_version"), 10, 64)
		if err != nil || workspace == "" {
			return nil, fmt.Errorf("workspace and expected_workspace_version are required")
		}
	}
	peer := optionalString(args, "peer")
	if action == "follow" && peer == "" {
		return nil, fmt.Errorf("peer is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := s.client.computerFollowing(ctx, action, peer, revision, workspace, workspaceVersion)
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
func computerFollowingTools() []map[string]any {
	var tools []map[string]any
	for _, action := range []string{"status", "follow", "sync", "stop", "exclude", "include"} {
		properties := map[string]any{}
		required := []string{}
		if action == "follow" {
			properties["peer"] = map[string]any{"type": "string"}
			required = append(required, "peer")
		}
		if action == "follow" || action == "stop" || action == "exclude" || action == "include" {
			properties["expected_revision"] = map[string]any{"type": "string", "description": "Revision from computer_status, as an unsigned decimal string."}
			required = append(required, "expected_revision")
		}
		if action == "exclude" || action == "include" {
			properties["workspace"] = map[string]any{"type": "string"}
			properties["expected_workspace_version"] = map[string]any{"type": "string", "description": "Environment route version from workspace status."}
			required = append(required, "workspace", "expected_workspace_version")
		}
		description := map[string]string{"exclude": "Keep an environment locally without following future source changes.", "include": "Resume following an environment, replacing local settings with source configuration.", "status": "Read whole-computer following status.", "follow": "Follow all environments on a paired computer, keeping local extras and pinned sessions.", "sync": "Refresh the complete followed configuration.", "stop": "Stop following the computer and restore saved local environments."}[action]
		tools = append(tools, toolSchema("rementor.computer_"+action, description, objectSchema(properties, required)))
	}
	return tools
}
