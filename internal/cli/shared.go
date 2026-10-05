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

func (c *Client) sharedIngress(ctx context.Context, action string, config *rementorv1.SharedIngressConfiguration, peer string, expected uint64) (proto.Message, error) {
	var result proto.Message
	var err error
	switch action {
	case "workspaces":
		r, e := c.rpc.GetRoutingWorkspaces(ctx, connect.NewRequest(&rementorv1.GetRoutingWorkspacesRequest{RoutingOwnerFingerprint: peer}))
		err = e
		if r != nil {
			result = r.Msg
		}
	case "show":
		r, e := c.rpc.GetSharedIngress(ctx, connect.NewRequest(&rementorv1.GetSharedIngressRequest{}))
		err = e
		if r != nil {
			result = r.Msg
		}
	case "create":
		r, e := c.rpc.CreateSharedIngress(ctx, connect.NewRequest(&rementorv1.CreateSharedIngressRequest{Configuration: config, ExpectedRevision: expected}))
		err = e
		if r != nil {
			result = r.Msg
		}
	case "save":
		r, e := c.rpc.SaveSharedIngress(ctx, connect.NewRequest(&rementorv1.SaveSharedIngressRequest{Configuration: config, ExpectedVersion: expected}))
		err = e
		if r != nil {
			result = r.Msg
		}
	case "join":
		r, e := c.rpc.JoinSharedIngress(ctx, connect.NewRequest(&rementorv1.JoinSharedIngressRequest{OwnerFingerprint: peer, ExpectedRevision: expected}))
		err = e
		if r != nil {
			result = r.Msg
		}
	case "transfer":
		r, e := c.rpc.TransferSharedIngress(ctx, connect.NewRequest(&rementorv1.TransferSharedIngressRequest{DestinationFingerprint: peer, ExpectedVersion: expected}))
		err = e
		if r != nil {
			result = r.Msg
		}
	case "recover":
		r, e := c.rpc.RecoverSharedIngress(ctx, connect.NewRequest(&rementorv1.RecoverSharedIngressRequest{ExpectedRevision: expected}))
		err = e
		if r != nil {
			result = r.Msg
		}
	case "cancel":
		r, e := c.rpc.CancelSharedIngress(ctx, connect.NewRequest(&rementorv1.CancelSharedIngressRequest{ExpectedRevision: expected}))
		err = e
		if r != nil {
			result = r.Msg
		}
	case "sync":
		r, e := c.rpc.SyncSharedIngress(ctx, connect.NewRequest(&rementorv1.SyncSharedIngressRequest{}))
		err = e
		if r != nil {
			result = r.Msg
		}
	default:
		return nil, fmt.Errorf("unknown shared ingress action %q", action)
	}
	if err != nil {
		return nil, apiError(err)
	}
	return result, nil
}

func SharedIngressCmd(client *Client, args []string) {
	if err := runSharedIngress(client, os.Stdout, args); err != nil {
		Die("%v", err)
	}
}

func runSharedIngress(client *Client, output io.Writer, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: shared-ingress <show|workspaces|create|save|join|transfer|recover|cancel|sync>")
	}
	action := args[0]
	fs := flag.NewFlagSet("shared-ingress "+action, flag.ContinueOnError)
	file := fs.String("file", "", "shared ingress configuration JSON")
	routingComputer := fs.String("routing-computer", "", "paired routing computer fingerprint; defaults to this computer")
	owner := fs.String("owner", "", "paired selector owner fingerprint")
	destination := fs.String("destination", "", "enrolled successor fingerprint")
	revision := fs.Uint64("expected-revision", 0, "current local storage revision")
	version := fs.Uint64("expected-version", 0, "current shared record version")
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional argument")
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	expected, peer := *revision, *owner
	if action == "workspaces" {
		peer = *routingComputer
	}
	switch action {
	case "create", "join", "recover", "cancel":
		if !given["expected-revision"] {
			return fmt.Errorf("--expected-revision is required; read shared-ingress show first")
		}
	case "save", "transfer":
		if !given["expected-version"] {
			return fmt.Errorf("--expected-version is required; read shared-ingress show first")
		}
		expected = *version
	case "show", "workspaces", "sync":
	default:
		return fmt.Errorf("unknown shared ingress action %q", action)
	}
	if action == "join" && *owner == "" {
		return fmt.Errorf("join requires --owner")
	}
	if action == "transfer" {
		if *destination == "" {
			return fmt.Errorf("transfer requires --destination")
		}
		peer = *destination
	}
	var config *rementorv1.SharedIngressConfiguration
	if action == "create" || action == "save" {
		if *file == "" {
			return fmt.Errorf("%s requires --file", action)
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
			return fmt.Errorf("shared ingress configuration exceeds 1 MiB")
		}
		config = &rementorv1.SharedIngressConfiguration{}
		if err := protojson.Unmarshal(raw, config); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := client.sharedIngress(ctx, action, config, peer, expected)
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

func (s *mcpServer) toolSharedIngress(name string, args map[string]any) (any, error) {
	stringArgument := func(key string) string { value, _ := args[key].(string); return value }
	action := strings.TrimPrefix(name, "rementor.shared_ingress_")
	var expected uint64
	var err error
	if action == "create" || action == "join" || action == "recover" || action == "cancel" {
		expected, err = strconv.ParseUint(stringArgument("expected_revision"), 10, 64)
	} else if action == "save" || action == "transfer" {
		expected, err = strconv.ParseUint(stringArgument("expected_version"), 10, 64)
	}
	if err != nil {
		return nil, fmt.Errorf("expected revision/version must be an unsigned decimal string")
	}
	var config *rementorv1.SharedIngressConfiguration
	if action == "create" || action == "save" {
		raw, err := json.Marshal(args["configuration"])
		if err != nil {
			return nil, err
		}
		if len(raw) > 1<<20 {
			return nil, fmt.Errorf("shared ingress configuration exceeds 1 MiB")
		}
		config = &rementorv1.SharedIngressConfiguration{}
		if err := protojson.Unmarshal(raw, config); err != nil {
			return nil, err
		}
	}
	peer := stringArgument("owner_fingerprint")
	if action == "workspaces" {
		peer = stringArgument("routing_owner_fingerprint")
	}
	if action == "transfer" {
		peer = stringArgument("destination_fingerprint")
	}
	if (action == "transfer" || action == "join") && peer == "" {
		return nil, fmt.Errorf("paired computer fingerprint is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := s.client.sharedIngress(ctx, action, config, peer, expected)
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

func sharedIngressTools() []map[string]any {
	var result []map[string]any
	descriptions := map[string]string{"workspaces": "Read names and routable local hosts from the selected paired computer; omit the fingerprint to read this computer.", "show": "Read shared ingress policy, owner and pending handoff.", "create": "Create shared ingress with explicit paired gateways and a validated routing computer.", "save": "Update the owner-controlled shared policy with its current record version.", "join": "Join the policy fetched directly from an owner that has enrolled this computer.", "transfer": "Transfer selector ownership to an enrolled gateway, preserving the routing computer.", "recover": "Resume the durable pending handoff at its current local revision.", "cancel": "Cancel an uncommitted outgoing handoff at its current local revision.", "sync": "Synchronize from the cached owner; never elect a replacement."}
	for _, action := range []string{"show", "workspaces", "create", "save", "join", "transfer", "recover", "cancel", "sync"} {
		fields := map[string]any{}
		if action == "workspaces" {
			fields["routing_owner_fingerprint"] = map[string]any{"type": "string"}
		}
		var required []string
		addString := func(name string) { fields[name] = map[string]any{"type": "string"}; required = append(required, name) }
		if action == "create" || action == "join" || action == "recover" || action == "cancel" {
			addString("expected_revision")
		}
		if action == "save" || action == "transfer" {
			addString("expected_version")
		}
		if action == "join" {
			addString("owner_fingerprint")
		}
		if action == "transfer" {
			addString("destination_fingerprint")
		}
		if action == "create" || action == "save" {
			fields["configuration"] = map[string]any{"type": "object", "description": "policy: PublicAccessDocument; routingOwnerFingerprint; gateways: paired fingerprints. Use decimal strings for allowedUserIds."}
			required = append(required, "configuration")
		}
		result = append(result, toolSchema("rementor.shared_ingress_"+action, descriptions[action], objectSchema(fields, required)))
	}
	return result
}
