package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"connectrpc.com/connect"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func (c *Client) tailscaleSharing(ctx context.Context, action, workspace string, expected uint64) (proto.Message, error) {
	switch action {
	case "show":
		r, err := c.rpc.GetTailscaleSharing(ctx, connect.NewRequest(&rementorv1.GetTailscaleSharingRequest{}))
		if err != nil {
			return nil, apiError(err)
		}
		return r.Msg, nil
	case "prepare":
		r, err := c.rpc.PrepareTailscaleSharing(ctx, connect.NewRequest(&rementorv1.PrepareTailscaleSharingRequest{WorkspaceId: workspace}))
		if err != nil {
			return nil, apiError(err)
		}
		return r.Msg, nil
	case "on", "off":
		r, err := c.rpc.SetTailscaleSharing(ctx, connect.NewRequest(&rementorv1.SetTailscaleSharingRequest{Enabled: action == "on", ExpectedRevision: expected}))
		if err != nil {
			return nil, apiError(err)
		}
		return r.Msg, nil
	default:
		return nil, fmt.Errorf("unknown Tailscale sharing action %q", action)
	}
}
func TailscaleSharingCmd(client *Client, args []string) {
	if err := runTailscaleSharing(client, os.Stdout, args); err != nil {
		Die("%v", err)
	}
}
func runTailscaleSharing(client *Client, output io.Writer, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: tailscale-sharing <show|prepare|on|off>")
	}
	fs := flag.NewFlagSet("tailscale-sharing "+args[0], flag.ContinueOnError)
	workspace := fs.String("workspace", "", "environment or session for initial URL suggestions")
	expected := fs.Uint64("expected-revision", 0, "sharing revision from show")
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional argument")
	}
	if args[0] == "on" || args[0] == "off" {
		given := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "expected-revision" {
				given = true
			}
		})
		if !given {
			return fmt.Errorf("--expected-revision is required; read tailscale-sharing show first")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := client.tailscaleSharing(ctx, args[0], *workspace, *expected)
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
func (s *mcpServer) toolTailscaleSharing(name string, args map[string]any) (any, error) {
	action := "show"
	var expected uint64
	if name == "rementor.tailscale_sharing_prepare" {
		action = "prepare"
	}
	if name == "rementor.tailscale_sharing_set" {
		enabled, ok := args["enabled"].(bool)
		if !ok {
			return nil, fmt.Errorf("enabled must be a boolean")
		}
		action = "off"
		if enabled {
			action = "on"
		}
		var err error
		expected, err = strconv.ParseUint(requiredString(args, "expected_revision"), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("expected_revision must be an unsigned decimal string")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := s.client.tailscaleSharing(ctx, action, optionalString(args, "workspace"), expected)
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
func tailscaleSharingTools() []map[string]any {
	return []map[string]any{
		toolSchema("rementor.tailscale_sharing_show", "Read the persistent sharing toggle, suggested domain, setup requirements and verified publication status.", objectSchema(map[string]any{}, nil)),
		toolSchema("rementor.tailscale_sharing_prepare", "Preview named HTTPS URLs without changing access or Tailscale. Save the returned document with access_save before enabling sharing.", objectSchema(map[string]any{"workspace": map[string]any{"type": "string", "description": "Environment or session ID for initial publication."}}, nil)),
		toolSchema("rementor.tailscale_sharing_set", "Enable or disable named Services on this gateway. Setup runs automatically in the background; inspect status for readiness. Does not reset unrelated Serve configuration.", objectSchema(map[string]any{"enabled": map[string]any{"type": "boolean"}, "expected_revision": map[string]any{"type": "string", "description": "Sharing revision from tailscale_sharing_show, as an unsigned decimal string."}}, []string{"enabled", "expected_revision"})),
	}
}
