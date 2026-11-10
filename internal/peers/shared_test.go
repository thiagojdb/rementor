package peers

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/thiagojdb/rementor/internal/access"
	"github.com/thiagojdb/rementor/internal/config"
	"github.com/thiagojdb/rementor/internal/shared"
)

type interruptedSharedPeer struct {
	shared.Peer
	dropPrepareAck bool
	stopActivation bool
}

func (p *interruptedSharedPeer) PrepareShared(ctx context.Context, id string, before, next shared.Record) (string, error) {
	ack, err := p.Peer.PrepareShared(ctx, id, before, next)
	if err == nil && p.dropPrepareAck {
		return "", errors.New("prepared response was lost")
	}
	return ack, err
}
func (p *interruptedSharedPeer) ActivateShared(ctx context.Context, id, digest string) error {
	if p.stopActivation {
		return errors.New("activation request was interrupted")
	}
	return p.Peer.ActivateShared(ctx, id, digest)
}
func sharedTestAuthority(t *testing.T, m *Manager) (*shared.Authority, *config.SharedIngressStore) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), m.State().Name))
	store, err := config.NewSharedIngressStore()
	if err != nil {
		t.Fatal(err)
	}
	a, err := shared.Open(m.State().Fingerprint, store)
	if err != nil {
		t.Fatal(err)
	}
	m.SetSharedAuthority(a)
	return a, store
}
func sharedTestPolicy() access.Document {
	return access.Document{SchemaVersion: 1, Origins: []access.Origin{{ID: "frontend", URL: "https://shared.example.test"}}, Bindings: []access.Binding{{WorkspaceID: "dev", OriginID: "frontend", LocalHost: "dev.giss.localhost"}}, AllowedUserIDs: []uint64{99}, SharedWorkspaceID: "dev"}
}
func pairSharedTest(t *testing.T, client, owner *Manager) {
	t.Helper()
	code, invitation := invite(t, owner)
	if _, err := client.Pair(context.Background(), code, invitation.Fingerprint); err != nil {
		t.Fatal(err)
	}
}

func TestSharedOwnershipUsesPinnedPeersExplicitEnrollmentAndDurableHandoff(t *testing.T) {
	ctx := context.Background()
	a, b, stranger := testManager(t, "selector"), testManager(t, "successor"), testManager(t, "paired-stranger")
	aa, _ := sharedTestAuthority(t, a)
	ba, bs := sharedTestAuthority(t, b)
	aID, bID := a.State().Fingerprint, b.State().Fingerprint
	pairSharedTest(t, b, a)
	pairSharedTest(t, stranger, a)
	pairSharedTest(t, stranger, b)
	before, err := aa.Create(sharedTestPolicy(), aID, []string{aID, bID}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stranger.FetchShared(ctx, aID); err == nil {
		t.Fatal("pairing alone authorized a shared gateway")
	}
	if got := status(t, b, a, "/rpc/rementor.v1.ControlPlaneService/SavePublicAccess"); got != http.StatusNotFound {
		t.Fatalf("shared peer exposed management: %d", got)
	}
	if _, err := ba.Join(ctx, aID, 0, b); err != nil {
		t.Fatal(err)
	}
	interrupted := &interruptedSharedPeer{Peer: a, dropPrepareAck: true}
	if _, err := aa.Transfer(ctx, bID, before.Version, interrupted); err == nil {
		t.Fatal("lost acknowledgement ignored")
	}
	next := aa.State().Pending.Next
	if err := a.ActivateShared(ctx, bID, next.Digest); err == nil {
		t.Fatal("successor accepted activation before durable predecessor commit")
	}
	if ba.State().Record.Owner != aID {
		t.Fatal("prepared successor took over early")
	}
	if _, err := stranger.PrepareShared(ctx, bID, before, next); err == nil {
		t.Fatal("another paired computer prepared a transfer")
	}
	interrupted.dropPrepareAck = false
	interrupted.stopActivation = true
	if _, err := aa.Recover(ctx, interrupted); err == nil {
		t.Fatal("activation interruption ignored")
	}
	if aa.State().Record.Owner != bID || !aa.State().Pending.Committed || ba.State().Record.Owner != aID {
		t.Fatal("interrupted commit did not retain its two durable stages")
	}
	// Restart the successor with its same SQLite path and pinned identity. The
	// private listener independently verifies the predecessor's commit before
	// activation; no credentials or management routes cross that listener.
	directory := filepath.Dir(b.path)
	b.Close()
	b, err = Open(directory, "ignored-name")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	ba, err = shared.Open(bID, bs)
	if err != nil {
		t.Fatal(err)
	}
	b.SetSharedAuthority(ba)
	b.Restore()
	if _, err := ba.Recover(ctx, b); err != nil {
		t.Fatal(err)
	}
	interrupted.stopActivation = false
	if _, err := aa.Recover(ctx, interrupted); err != nil {
		t.Fatal(err)
	}
	if aa.State().Pending != nil || ba.State().Pending != nil || aa.State().Record.Digest != ba.State().Record.Digest || aa.State().Record.RoutingOwner != aID {
		t.Fatal("restart changed selected routing computer or did not converge")
	}
	changed := sharedTestPolicy()
	changed.AllowedUserIDs = []uint64{100}
	updated, err := ba.Update(changed, aID, next.Gateways, next.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := aa.Sync(ctx, a); err != nil {
		t.Fatal(err)
	}
	if aa.State().Record.Digest != updated.Digest {
		t.Fatal("former owner did not follow new authority")
	}
	if err := b.Revoke(aID); err != nil {
		t.Fatal(err)
	}
	if _, err := aa.Sync(ctx, a); err == nil {
		t.Fatal("revoked gateway synchronized shared ownership")
	}
	if _, err := aa.Update(sharedTestPolicy(), aID, next.Gateways, updated.Version); !errors.Is(err, shared.ErrNotOwner) {
		t.Fatalf("revocation let old owner write offline: %v", err)
	}
	if aa.State().Record.Digest != updated.Digest {
		t.Fatal("revocation changed cached selection")
	}
}
