package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
)

type SessionRefreshDTO struct {
	Workspace    WorkspaceDTO `json:"workspace"`
	Changes      []string     `json:"changes"`
	Conflicts    []string     `json:"conflicts"`
	PreviewToken string       `json:"previewToken"`
}

func (c *Client) CreateRoutingSession(ctx context.Context, environment, name string) (WorkspaceDTO, error) {
	res, err := c.rpc.CreateRoutingSession(ctx, connect.NewRequest(&rementorv1.CreateRoutingSessionRequest{EnvironmentId: environment, Name: name}))
	if err != nil {
		return WorkspaceDTO{}, apiError(err)
	}
	return workspaceFromProto(res.Msg.Workspace), nil
}
func (c *Client) RefreshRoutingSession(ctx context.Context, id, token string, apply bool) (SessionRefreshDTO, error) {
	res, err := c.rpc.RefreshRoutingSession(ctx, connect.NewRequest(&rementorv1.RefreshRoutingSessionRequest{Id: id, PreviewToken: token, Apply: apply}))
	if err != nil {
		return SessionRefreshDTO{}, apiError(err)
	}
	return SessionRefreshDTO{workspaceFromProto(res.Msg.Workspace), res.Msg.Changes, res.Msg.Conflicts, res.Msg.PreviewToken}, nil
}
func SessionCmd(client *Client, jsonOutput bool, args []string) {
	if len(args) == 0 {
		Die("usage: rementorctl session <create|list|inspect|refresh|close>")
	}
	fs := flag.NewFlagSet("session "+args[0], flag.ExitOnError)
	environment := fs.String("workspace", "", "environment workspace ID")
	token := fs.String("preview-token", "", "token from refresh preview")
	apply := fs.Bool("apply", false, "apply the reviewed refresh preview")
	if err := parseFlags(fs, args[1:]); err != nil {
		Die("%v", err)
	}
	ctx := context.Background()
	// Lifecycle commands address session IDs directly, independent of a process default.
	plain := NewClient(client.BaseURL())
	switch args[0] {
	case "create":
		if fs.NArg() != 1 || *environment == "" {
			Die("usage: session create <name> --workspace <environment>")
		}
		ws, err := plain.CreateRoutingSession(ctx, *environment, fs.Arg(0))
		if err != nil {
			Die("%v", err)
		}
		if jsonOutput {
			PrintJSON(ws)
		} else {
			fmt.Printf("Session %s created under %s\n%s\nRegister ports, then toggle applications locally when needed.\n", ws.ID, *environment, ws.BrowserURL)
		}
	case "list":
		all, err := plain.ListWorkspaces(ctx)
		if err != nil {
			Die("%v", err)
		}
		sessions := []WorkspaceDTO{}
		for _, w := range all {
			if w.Session != nil && (*environment == "" || w.Session.EnvironmentId == *environment) {
				sessions = append(sessions, w)
			}
		}
		if jsonOutput {
			PrintJSON(sessions)
			return
		}
		w := NewTabWriter()
		fmt.Fprintln(w, "ID\tENVIRONMENT\tNAME\tDOMAIN")
		for _, s := range sessions {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.ID, s.Session.EnvironmentId, s.Name, s.Routing.LocalDomain)
		}
		w.Flush()
	case "inspect", "close", "refresh":
		if fs.NArg() != 1 {
			Die("usage: session %s <session-id>", args[0])
		}
		id := fs.Arg(0)
		ws, err := plain.GetWorkspace(ctx, id)
		if err != nil {
			Die("%v", err)
		}
		if ws.Session == nil {
			Die("%q is not a routing session", id)
		}
		if *environment != "" && ws.Session.EnvironmentId != *environment {
			Die("session belongs to %s", ws.Session.EnvironmentId)
		}
		switch args[0] {
		case "inspect":
			PrintJSON(ws)
		case "close":
			op, err := plain.DeleteWorkspaceWithMetadata(ctx, id)
			if err != nil {
				Die("%v", err)
			}
			if jsonOutput {
				PrintJSON(map[string]any{"closed": id, "operation": op})
			} else {
				fmt.Printf("Session %s closed\n", id)
			}
		case "refresh":
			preview, err := plain.RefreshRoutingSession(ctx, id, *token, *apply)
			if err != nil {
				Die("%v", err)
			}
			if jsonOutput {
				PrintJSON(preview)
				return
			}
			fmt.Printf("Changes: %s\n", strings.Join(preview.Changes, "; "))
			if len(preview.Conflicts) > 0 {
				fmt.Printf("Conflicts: %s\n", strings.Join(preview.Conflicts, "; "))
			}
			if *apply {
				fmt.Println("Refresh applied")
			} else {
				fmt.Printf("Preview token: %s\nApply with: rementorctl session refresh %s --apply --preview-token %s\n", preview.PreviewToken, id, preview.PreviewToken)
			}
		}
	default:
		Die("unknown session command: %s", args[0])
	}
}
