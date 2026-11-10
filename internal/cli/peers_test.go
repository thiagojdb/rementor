package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thiagojdb/rementor/internal/gen/rementor/v1/rementorv1connect"
	"github.com/thiagojdb/rementor/internal/peers"
	"github.com/thiagojdb/rementor/internal/rpc"
)

func cliPeerManager(t *testing.T, name string) *peers.Manager {
	t.Helper()
	manager, err := peers.Open(t.TempDir(), name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	return manager
}

func TestPeerCLIInvitationEnrollsWithRunningIdentity(t *testing.T) {
	source, target := cliPeerManager(t, "source"), cliPeerManager(t, "target")
	service := rpc.NewControlPlaneService(nil)
	service.SetPeerManager(source)
	path, handler := rementorv1connect.NewControlPlaneServiceHandler(service)
	mux := http.NewServeMux()
	mux.Handle("/rpc"+path, http.StripPrefix("/rpc", handler))
	server := httptest.NewServer(mux)
	defer server.Close()
	client := NewClient(server.URL)
	var output, details bytes.Buffer
	if err := runPeer(client, &output, &details, false, []string{"invite"}); err != nil {
		t.Fatal(err)
	}
	if !source.State().Active || target.State().Active {
		t.Fatal("invite did not enable its own listener")
	}
	firstCode := strings.TrimSpace(output.String())
	first, err := peers.ParseInvitation(firstCode, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint != source.State().Fingerprint || !strings.Contains(details.String(), first.Fingerprint) || strings.Contains(details.String(), firstCode) {
		t.Fatal("CLI omitted the running identity or mixed the invitation into metadata")
	}
	output.Reset()
	details.Reset()
	if err := runPeer(client, &output, &details, true, []string{"invite"}); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Invitation  string    `json:"invitation"`
		Fingerprint string    `json:"fingerprint"`
		ExpiresAt   time.Time `json:"expiresAt"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if details.Len() != 0 || result.ExpiresAt.Before(time.Now()) || result.Fingerprint != first.Fingerprint {
		t.Fatal("invalid JSON invitation metadata")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := target.Pair(ctx, firstCode, first.Fingerprint); err == nil {
		t.Fatal("new CLI invitation did not invalidate the previous invitation")
	}
	if _, err := target.Pair(ctx, result.Invitation, result.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if !target.State().Active {
		t.Fatal("joining did not enable the target listener")
	}
	if err := source.Check(ctx, target.State().Fingerprint); err != nil {
		t.Fatal(err)
	}
	if err := target.Check(ctx, source.State().Fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := target.Pair(ctx, result.Invitation, result.Fingerprint); err == nil {
		t.Fatal("CLI invitation could be reused")
	}
}
