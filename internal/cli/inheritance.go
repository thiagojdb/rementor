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

type inheritanceAction struct {
	Name, Workspace, Peer, Source, Application string
	Expected                                   uint64
	Selection                                  *rementorv1.InheritedRouteSelection
}

func (c *Client) inheritance(ctx context.Context, action inheritanceAction) (proto.Message, error) {
	switch action.Name {
	case "list":
		response, err := c.rpc.ListPeerWorkspaces(ctx, connect.NewRequest(&rementorv1.ListPeerWorkspacesRequest{Fingerprint: action.Peer}))
		if err != nil {
			return nil, apiError(err)
		}
		return response.Msg, nil
	case "status":
		response, err := c.rpc.GetWorkspaceInheritance(ctx, connect.NewRequest(&rementorv1.GetWorkspaceInheritanceRequest{WorkspaceId: action.Workspace}))
		if err != nil {
			return nil, apiError(err)
		}
		return response.Msg, nil
	case "follow":
		catalog, err := c.rpc.ListPeerWorkspaces(ctx, connect.NewRequest(&rementorv1.ListPeerWorkspacesRequest{Fingerprint: action.Peer}))
		if err != nil {
			return nil, apiError(err)
		}
		var scope *rementorv1.PeerWorkspaceScope
		for _, workspace := range catalog.Msg.Workspaces {
			if workspace.GetScope().GetWorkspaceId() == action.Source {
				scope = workspace.Scope
				break
			}
		}
		if scope == nil {
			return nil, fmt.Errorf("source environment or session %q not found on computer", action.Source)
		}
		response, err := c.rpc.FollowPeerWorkspace(ctx, connect.NewRequest(&rementorv1.FollowPeerWorkspaceRequest{WorkspaceId: action.Workspace, Fingerprint: action.Peer, Scope: scope, ExpectedRouteVersion: action.Expected}))
		if err != nil {
			return nil, apiError(err)
		}
		return response.Msg, nil
	case "sync":
		response, err := c.rpc.SyncPeerWorkspace(ctx, connect.NewRequest(&rementorv1.SyncPeerWorkspaceRequest{WorkspaceId: action.Workspace}))
		if err != nil {
			return nil, apiError(err)
		}
		return response.Msg, nil
	case "override", "reset":
		response, err := c.rpc.OverrideInheritedRoute(ctx, connect.NewRequest(&rementorv1.OverrideInheritedRouteRequest{WorkspaceId: action.Workspace, ApplicationId: action.Application, Selection: action.Selection, ExpectedRouteVersion: action.Expected}))
		if err != nil {
			return nil, apiError(err)
		}
		return response.Msg, nil
	case "stop":
		response, err := c.rpc.StopFollowingWorkspace(ctx, connect.NewRequest(&rementorv1.StopFollowingWorkspaceRequest{WorkspaceId: action.Workspace, ExpectedRouteVersion: action.Expected}))
		if err != nil {
			return nil, apiError(err)
		}
		return response.Msg, nil
	default:
		return nil, fmt.Errorf("unknown inheritance action %q", action.Name)
	}
}

func InheritanceCmd(client *Client, args []string) {
	if err := runInheritance(client, os.Stdout, args); err != nil {
		Die("%v", err)
	}
}
func runInheritance(client *Client, output io.Writer, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: inheritance <list|status|follow|sync|override|reset|stop>")
	}
	fs := flag.NewFlagSet("inheritance "+args[0], flag.ContinueOnError)
	peer := fs.String("peer", "", "paired computer fingerprint")
	source := fs.String("source", "", "source environment or session ID")
	expected := fs.Uint64("expected-version", 0, "receiver route version from inheritance status")
	mode := fs.String("mode", "", "local, remote or inherit")
	port := fs.Int("port", 0, "port on this computer; zero selects the source registration")
	remote := fs.String("remote-url", "", "remote URL; empty follows the source baseline")
	pattern := fs.String("pattern", "", "explicit route pattern; omit to inherit")
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	action := inheritanceAction{Name: args[0], Peer: *peer, Source: *source, Expected: *expected}
	mutation := action.Name == "follow" || action.Name == "override" || action.Name == "reset" || action.Name == "stop"
	count := 1
	if action.Name == "override" || action.Name == "reset" {
		count = 2
	}
	if fs.NArg() != count {
		return fmt.Errorf("inheritance %s needs %d positional arguments", action.Name, count)
	}
	if action.Name == "list" {
		action.Peer = fs.Arg(0)
	} else {
		action.Workspace = fs.Arg(0)
	}
	if count == 2 {
		action.Application = fs.Arg(1)
	}
	versionGiven, patternGiven := false, false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "expected-version" {
			versionGiven = true
		}
		if f.Name == "pattern" {
			patternGiven = true
		}
	})
	if mutation && !versionGiven {
		return fmt.Errorf("--expected-version is required; read inheritance status first")
	}
	if action.Name == "follow" && (action.Peer == "" || action.Source == "") {
		return fmt.Errorf("follow requires --peer and --source")
	}
	if action.Name == "override" {
		if *mode != "local" && *mode != "remote" && *mode != "inherit" {
			return fmt.Errorf("--mode must be local, remote or inherit")
		}
		action.Selection = &rementorv1.InheritedRouteSelection{Mode: *mode, Port: int32(*port), RemoteBaseUrl: *remote}
		if *port < 0 || *port > 65535 {
			return fmt.Errorf("--port must be between 0 and 65535")
		}
		if patternGiven {
			action.Selection.RoutePattern = pattern
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := client.inheritance(ctx, action)
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

func (s *mcpServer) toolInheritance(name string, args map[string]any) (any, error) {
	action := inheritanceAction{Name: strings.TrimPrefix(name, "rementor.inheritance_")}
	if action.Name == "list" {
		action.Peer = requiredString(args, "peer")
	} else {
		action.Workspace = requiredString(args, "workspace")
	}
	if action.Name == "follow" || action.Name == "override" || action.Name == "reset" || action.Name == "stop" {
		// A string retains all uint64 values, including versions above 2^53.
		expected, err := strconv.ParseUint(requiredString(args, "expected_version"), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("expected_version must be an unsigned decimal string")
		}
		action.Expected = expected
	}
	if action.Name == "follow" {
		action.Peer = requiredString(args, "peer")
		action.Source = requiredString(args, "source")
	}
	if action.Name == "override" || action.Name == "reset" {
		action.Application = requiredString(args, "application")
	}
	if action.Name == "override" {
		action.Selection = &rementorv1.InheritedRouteSelection{Mode: requiredString(args, "mode"), RemoteBaseUrl: optionalString(args, "remote_url")}
		if value, exists := args["port"]; exists {
			number, ok := value.(float64)
			if !ok || number < 0 || number > 65535 || number != float64(int(number)) {
				return nil, fmt.Errorf("port must be an integer between 0 and 65535")
			}
			action.Selection.Port = int32(number)
		}
		if value, exists := args["pattern"]; exists {
			pattern, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("pattern must be a string")
			}
			action.Selection.RoutePattern = &pattern
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := s.client.inheritance(ctx, action)
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

func inheritanceTools() []map[string]any {
	var tools []map[string]any
	for _, name := range []string{"list", "status", "follow", "sync", "override", "reset", "stop"} {
		properties := map[string]any{}
		required := []string{}
		add := func(key string) { properties[key] = map[string]any{"type": "string"}; required = append(required, key) }
		if name == "list" {
			add("peer")
		} else {
			add("workspace")
		}
		if name == "follow" || name == "override" || name == "reset" || name == "stop" {
			add("expected_version")
		}
		if name == "follow" {
			add("peer")
			add("source")
		}
		if name == "override" || name == "reset" {
			add("application")
		}
		if name == "override" {
			add("mode")
			properties["mode"] = map[string]any{"type": "string", "enum": []string{"local", "remote", "inherit"}}
			properties["port"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 65535, "description": "Port on this computer. Zero selects the source registration."}
			properties["remote_url"] = map[string]any{"type": "string"}
			properties["pattern"] = map[string]any{"type": "string"}
		}
		descriptions := map[string]string{"list": "List environments and sessions on a paired computer.", "status": "Read source sync status, overrides and receiver route version.", "follow": "Follow a peer environment or session. expected_version is the receiver version as a decimal string.", "sync": "Synchronize a followed workspace now.", "override": "Override one inherited route on this computer. expected_version is a decimal string.", "reset": "Reset a binding to its cached source configuration. expected_version is a decimal string.", "stop": "Stop following and restore saved original routes. expected_version is a decimal string."}
		tools = append(tools, toolSchema("rementor.inheritance_"+name, descriptions[name], objectSchema(properties, required)))
	}
	return tools
}
