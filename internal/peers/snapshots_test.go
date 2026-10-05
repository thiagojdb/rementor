package peers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thiagojdb/rementor/internal/fleet"
	"github.com/thiagojdb/rementor/internal/models"
)

type snapshotSource struct {
	mu      sync.Mutex
	intent  fleet.WorkspaceIntent
	version uint64
}

func (s *snapshotSource) ExportPeerWorkspaces() []fleet.WorkspaceSummary {
	intent, version, _ := s.ExportPeerWorkspace("dev")
	return []fleet.WorkspaceSummary{{Scope: intent.Scope, Type: intent.Type, Name: intent.Name, Version: version}}
}

func (s *snapshotSource) ExportPeerWorkspace(id string) (fleet.WorkspaceIntent, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.intent.Scope.WorkspaceID {
		return fleet.WorkspaceIntent{}, 0, fleet.ErrWorkspaceNotFound
	}
	raw, _ := json.Marshal(s.intent)
	var detached fleet.WorkspaceIntent
	_ = json.Unmarshal(raw, &detached)
	return detached, s.version, nil
}

func TestAuthenticatedWorkspaceSnapshotsTrackChangesAndRevocation(t *testing.T) {
	client, source, stranger := testManager(t, "client"), testManager(t, "source"), testManager(t, "stranger")
	fixture := &snapshotSource{version: 1, intent: fleet.WorkspaceIntent{Scope: fleet.Scope{WorkspaceID: "dev", EnvironmentID: "dev"}, Type: models.WorkspaceTypeRouting, Name: "Dev", Routing: models.RoutingConfig{LocalDomain: "dev.localhost"}, Applications: []models.ApplicationConfig{{ID: "orders", Path: "/orders", Port: 8080, Active: true}}}}
	source.SetWorkspaceSource(fixture)
	if status(t, stranger, source, "/v1/workspaces/dev") != http.StatusForbidden {
		t.Fatal("unpaired computer read workspace intent")
	}
	code, invitation := invite(t, source)
	if _, err := client.Pair(context.Background(), code, invitation.Fingerprint); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	catalog, err := client.PeerWorkspaces(ctx, invitation.Fingerprint)
	if err != nil || len(catalog.Workspaces) != 1 {
		t.Fatalf("catalog failed: %v", err)
	}
	first, err := client.FetchWorkspace(ctx, invitation.Fingerprint, "dev", "")
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 || first.OwnerFingerprint != source.State().Fingerprint || first.Intent.Applications[0].Port != 8080 {
		t.Fatal("snapshot lost version or endpoint ownership")
	}
	if unchanged, err := client.FetchWorkspace(ctx, invitation.Fingerprint, "dev", first.Digest); err != nil || unchanged != nil {
		t.Fatalf("conditional fetch did not preserve the known snapshot: %v", err)
	}
	fixture.mu.Lock()
	fixture.intent.Applications[0].Active = false
	fixture.intent.Routing.DefaultRemoteBaseURL = "https://remote.example.test"
	fixture.version++
	fixture.mu.Unlock()
	second, err := client.FetchWorkspace(ctx, invitation.Fingerprint, "dev", first.Digest)
	if err != nil || second == nil {
		t.Fatalf("changed snapshot was missed: %v", err)
	}
	if second.Version != 2 || second.Digest == first.Digest || second.Intent.Applications[0].Active || !first.Intent.Applications[0].Active {
		t.Fatal("snapshot change or detachment was lost")
	}
	if _, err := client.FetchWorkspace(ctx, invitation.Fingerprint, "missing", ""); err == nil {
		t.Fatal("missing workspace resolved to another scope")
	}
	if _, err := client.FetchWorkspace(ctx, invitation.Fingerprint, "../dev", ""); err == nil {
		t.Fatal("accepted path traversal")
	}
	if status(t, client, source, "/rpc/rementor.v1.ControlPlaneService/ListWorkspaces") != http.StatusNotFound {
		t.Fatal("management RPC exposed on peer listener")
	}
	if err := source.Revoke(client.State().Fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := client.FetchWorkspace(ctx, invitation.Fingerprint, "dev", second.Digest); err == nil {
		t.Fatal("revoked client got a current snapshot")
	}
}

type blockedSource struct {
	fleet.WorkspaceSource
	started chan struct{}
	release chan struct{}
}

func (s blockedSource) ExportPeerWorkspace(id string) (fleet.WorkspaceIntent, uint64, error) {
	close(s.started)
	<-s.release
	return s.WorkspaceSource.ExportPeerWorkspace(id)
}

func TestSnapshotFetchRejectsTrustRevokedWhileExporting(t *testing.T) {
	for _, revokeOnServer := range []bool{false, true} {
		t.Run(map[bool]string{false: "client", true: "server"}[revokeOnServer], func(t *testing.T) {
			client, source := testManager(t, "client"), testManager(t, "source")
			fixture := &snapshotSource{intent: fleet.WorkspaceIntent{Scope: fleet.Scope{WorkspaceID: "dev", EnvironmentID: "dev"}, Type: models.WorkspaceTypeRouting, Routing: models.RoutingConfig{LocalDomain: "dev.localhost"}}}
			blocked := blockedSource{WorkspaceSource: fixture, started: make(chan struct{}), release: make(chan struct{})}
			source.SetWorkspaceSource(blocked)
			code, invitation := invite(t, source)
			if _, err := client.Pair(context.Background(), code, invitation.Fingerprint); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := client.FetchWorkspace(ctx, invitation.Fingerprint, "dev", ""); done <- err }()
			select {
			case <-blocked.started:
			case <-ctx.Done():
				t.Fatal("snapshot request never reached source")
			}
			if revokeOnServer {
				if err := source.Revoke(client.State().Fingerprint); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := client.Revoke(invitation.Fingerprint); err != nil {
					t.Fatal(err)
				}
			}
			close(blocked.release)
			if err := <-done; err == nil {
				t.Fatal("snapshot was accepted after revocation")
			}
		})
	}
}

func TestSnapshotDecoderRejectsExtraUnknownAndOversizedData(t *testing.T) {
	for _, raw := range []string{`{} {}`, `{"unknown":true}`, strings.Repeat(" ", fleet.MaxSnapshotBytes+1)} {
		var snapshot fleet.Snapshot
		if err := readSnapshotJSON(strings.NewReader(raw), &snapshot); err == nil {
			t.Fatal("invalid peer document was accepted")
		}
	}
}
