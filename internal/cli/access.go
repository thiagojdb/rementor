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

func (c *Client) publicAccess(ctx context.Context, document *rementorv1.PublicAccessDocument, expected uint64) (proto.Message, error) {
	if document == nil {
		r, err := c.rpc.GetPublicAccess(ctx, connect.NewRequest(&rementorv1.GetPublicAccessRequest{}))
		if err != nil {
			return nil, apiError(err)
		}
		return r.Msg, nil
	}
	r, err := c.rpc.SavePublicAccess(ctx, connect.NewRequest(&rementorv1.SavePublicAccessRequest{Document: document, ExpectedRevision: expected}))
	if err != nil {
		return nil, apiError(err)
	}
	return r.Msg, nil
}
func AccessCmd(client *Client, args []string) {
	if err := runAccess(client, os.Stdout, args); err != nil {
		Die("%v", err)
	}
}
func runAccess(client *Client, output io.Writer, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: access <show|save|clear>")
	}
	fs := flag.NewFlagSet("access "+args[0], flag.ContinueOnError)
	file := fs.String("file", "", "public access document JSON file")
	expected := fs.Uint64("expected-revision", 0, "revision from access show")
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional argument")
	}
	var document *rementorv1.PublicAccessDocument
	switch args[0] {
	case "show":
	case "save", "clear":
		given := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "expected-revision" {
				given = true
			}
		})
		if !given {
			return fmt.Errorf("--expected-revision is required; read access show first")
		}
		document = &rementorv1.PublicAccessDocument{SchemaVersion: 1}
		if args[0] == "save" {
			if *file == "" {
				return fmt.Errorf("save requires --file")
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
				return fmt.Errorf("public access document exceeds 1 MiB")
			}
			if err := protojson.Unmarshal(raw, document); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unknown access action %q", args[0])
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := client.publicAccess(ctx, document, *expected)
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
func (s *mcpServer) toolPublicAccess(name string, args map[string]any) (any, error) {
	var document *rementorv1.PublicAccessDocument
	var expected uint64
	if name == "rementor.access_save" {
		revision, err := strconv.ParseUint(requiredString(args, "expected_revision"), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("expected_revision must be an unsigned decimal string")
		}
		expected = revision
		raw, err := json.Marshal(args["document"])
		if err != nil {
			return nil, err
		}
		document = &rementorv1.PublicAccessDocument{}
		if err := protojson.Unmarshal(raw, document); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := s.client.publicAccess(ctx, document, expected)
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
func publicAccessTools() []map[string]any {
	return []map[string]any{
		toolSchema("rementor.access_show", "Read public origins, workspace bindings, device selections and ingress state.", objectSchema(map[string]any{}, nil)),
		toolSchema("rementor.access_save", "Save public access policy with its current revision. Clearing origins revokes publication and closes existing streams.", objectSchema(map[string]any{"expected_revision": map[string]any{"type": "string", "description": "Current revision as an unsigned decimal string."}, "document": map[string]any{"type": "object", "description": "schemaVersion: 1; origins [{id,url}], bindings [{workspaceId,originId,localHost}], devices [{nodeId,workspaceId}], sharedWorkspaceId, allowedUserIds (decimal strings)."}}, []string{"expected_revision", "document"})),
	}
}
