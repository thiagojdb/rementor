package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"connectrpc.com/connect"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
)

// PeerCmd generates an invitation using the running instance's identity.
func PeerCmd(client *Client, jsonOutput bool, args []string) {
	if err := runPeer(client, os.Stdout, os.Stderr, jsonOutput, args); err != nil {
		Die("%v", err)
	}
}

func runPeer(client *Client, output, details io.Writer, jsonOutput bool, args []string) error {
	if len(args) != 1 || args[0] != "invite" {
		return fmt.Errorf("usage: rementorctl peer invite [--server <url>] [--json]")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	response, err := client.rpc.CreatePeerInvitation(ctx, connect.NewRequest(&rementorv1.CreatePeerInvitationRequest{}))
	if err != nil {
		return apiError(err)
	}
	invitation := response.Msg
	expires := invitation.ExpiresAt.AsTime().Format(time.RFC3339)
	if jsonOutput {
		return json.NewEncoder(output).Encode(struct {
			Invitation  string `json:"invitation"`
			Fingerprint string `json:"fingerprint"`
			ExpiresAt   string `json:"expiresAt"`
		}{invitation.Invitation, invitation.Fingerprint, expires})
	}
	if _, err := fmt.Fprintln(output, invitation.Invitation); err != nil {
		return err
	}
	_, err = fmt.Fprintf(details, "Fingerprint: %s\nExpires at: %s (single use)\nPaste the invitation into Configuration → Connect to computer on the other computer. Compare fingerprints before confirming.\n", invitation.Fingerprint, expires)
	return err
}
