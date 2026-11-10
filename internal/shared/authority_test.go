package shared

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/thiagojdb/rementor/internal/access"
)

var injectedFailure = errors.New("injected persistence or transport failure")

type memoryStore struct {
	mu    sync.Mutex
	state State
	fail  bool
}

func (s *memoryStore) Load() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Clone(), nil
}
func (s *memoryStore) CompareAndSwap(next State, expected uint64) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		s.fail = false
		return State{}, injectedFailure
	}
	if s.state.Revision != expected {
		return State{}, ErrConflict
	}
	next.Revision = expected + 1
	s.state = next.Clone()
	return s.state.Clone(), nil
}
func (s *memoryStore) failNext() { s.mu.Lock(); defer s.mu.Unlock(); s.fail = true }

type network struct {
	caller         string
	nodes          map[string]*Authority
	beforePrepare  func() error
	afterPrepare   func() error
	beforeActivate func() error
	afterActivate  func() error
}

func (n *network) FetchShared(ctx context.Context, owner string) (Record, error) {
	if node := n.nodes[owner]; node != nil {
		return node.Export(n.caller)
	}
	return Record{}, injectedFailure
}
func (n *network) PrepareShared(ctx context.Context, destination string, before, next Record) (string, error) {
	if n.beforePrepare != nil {
		if err := n.beforePrepare(); err != nil {
			return "", err
		}
	}
	ack, err := n.nodes[destination].Prepare(n.caller, before, next)
	if err == nil && n.afterPrepare != nil {
		err = n.afterPrepare()
	}
	return ack, err
}
func (n *network) ActivateShared(ctx context.Context, destination, digest string) error {
	if n.beforeActivate != nil {
		if err := n.beforeActivate(); err != nil {
			return err
		}
	}
	node := n.nodes[destination]
	if !node.ActivationKnown(n.caller, digest) {
		committed, err := n.nodes[n.caller].Export(destination)
		if err != nil {
			return err
		}
		if committed.Digest != digest {
			return ErrConflict
		}
		if err := node.Activate(n.caller, committed); err != nil {
			return err
		}
	}
	if n.afterActivate != nil {
		return n.afterActivate()
	}
	return nil
}
func (n *network) AbortShared(ctx context.Context, destination, digest string) error {
	return n.nodes[destination].Abort(n.caller, digest)
}

func policy() access.Document {
	return access.Document{SchemaVersion: 1, Origins: []access.Origin{{ID: "frontend", URL: "https://shared.example.test"}, {ID: "api", URL: "https://api.shared.example.test"}}, Bindings: []access.Binding{{WorkspaceID: "dev", OriginID: "frontend", LocalHost: "dev.giss.localhost"}, {WorkspaceID: "dev", OriginID: "api", LocalHost: "dev.giss.localhost"}}, AllowedUserIDs: []uint64{99}, SharedWorkspaceID: "dev"}
}
func authority(t *testing.T, self string, store Store) *Authority {
	t.Helper()
	a, err := Open(self, store)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func setup(t *testing.T) (*Authority, *Authority, *memoryStore, *memoryStore, *network, *network, Record) {
	t.Helper()
	aID, bID, source := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	as, bs := &memoryStore{}, &memoryStore{}
	a, b := authority(t, aID, as), authority(t, bID, bs)
	nodes := map[string]*Authority{aID: a, bID: b}
	an, bn := &network{caller: aID, nodes: nodes}, &network{caller: bID, nodes: nodes}
	r, err := a.Create(policy(), source, []string{aID, bID}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Join(context.Background(), aID, 0, bn); err != nil {
		t.Fatal(err)
	}
	return a, b, as, bs, an, bn, r
}

func TestTransferPreservesSelectedRoutingComputerAndExplicitMembership(t *testing.T) {
	a, b, _, _, an, bn, before := setup(t)
	if _, err := b.Update(policy(), b.self, before.Gateways, before.Version); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("replica edited selector: %v", err)
	}
	if _, err := a.Transfer(context.Background(), before.RoutingOwner, before.Version, an); !errors.Is(err, ErrNotMember) {
		t.Fatalf("routing host became gateway owner implicitly: %v", err)
	}
	if _, err := a.Transfer(context.Background(), b.self, before.Version+1, an); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale transfer: %v", err)
	}
	after, err := a.Transfer(context.Background(), b.self, before.Version, an)
	if err != nil {
		t.Fatal(err)
	}
	if after.Owner != b.self || after.RoutingOwner != before.RoutingOwner || after.GroupID != before.GroupID || after.Version != 2 || after.Epoch != 2 || after.Predecessor != before.Digest {
		t.Fatalf("transfer changed routing intent: %+v", after)
	}
	if a.State().Pending != nil || b.State().Pending != nil || a.State().Record.Digest != b.State().Record.Digest {
		t.Fatal("handoff did not converge")
	}
	if _, err := a.Update(policy(), a.self, before.Gateways, after.Version); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("former owner kept editing: %v", err)
	}
	changed := policy()
	changed.AllowedUserIDs = []uint64{100}
	updated, err := b.Update(changed, after.RoutingOwner, after.Gateways, after.Version)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != 3 || updated.Epoch != 2 {
		t.Fatal("policy edit changed ownership epoch")
	}
	if _, err := a.Sync(context.Background(), an); err != nil {
		t.Fatal(err)
	}
	if a.State().Record.Digest != updated.Digest {
		t.Fatal("gateway did not follow new authority")
	}
	if _, err := b.Export(strings.Repeat("d", 64)); !errors.Is(err, ErrNotMember) {
		t.Fatalf("unlisted computer read group: %v", err)
	}
	// Detached public state cannot mutate the persisted selector.
	state := a.State()
	state.Record.Policy.AllowedUserIDs[0] = 101
	state.Record.Gateways[0] = strings.Repeat("e", 64)
	if a.State().Record.Digest != updated.Digest || a.State().Record.Policy.AllowedUserIDs[0] != 100 {
		t.Fatal("state leaked mutable record")
	}
	_ = bn
}

func TestOwnershipHandoffRecoversEveryInterruptedStageAfterRestart(t *testing.T) {
	for _, stage := range []string{"before-prepare", "prepared-ack-lost", "predecessor-commit-fails", "before-activation", "successor-persist-fails", "activation-ack-lost"} {
		t.Run(stage, func(t *testing.T) {
			a, b, as, bs, an, bn, before := setup(t)
			switch stage {
			case "before-prepare":
				an.beforePrepare = func() error { return injectedFailure }
			case "prepared-ack-lost":
				an.afterPrepare = func() error { return injectedFailure }
			case "predecessor-commit-fails":
				an.afterPrepare = func() error { as.failNext(); return nil }
			case "before-activation":
				an.beforeActivate = func() error { return injectedFailure }
			case "successor-persist-fails":
				an.beforeActivate = func() error { bs.failNext(); return nil }
			case "activation-ack-lost":
				an.afterActivate = func() error { return injectedFailure }
			}
			if _, err := a.Transfer(context.Background(), b.self, before.Version, an); err == nil {
				t.Fatal("injected failure was ignored")
			}
			if a.State().Pending == nil {
				t.Fatal("failed handoff lost durable recovery information")
			}
			if stage == "prepared-ack-lost" || stage == "predecessor-commit-fails" {
				if b.State().Record.Owner != a.self || a.State().Record.Owner != a.self {
					t.Fatal("prepared successor became owner before commit")
				}
				if _, err := b.Recover(context.Background(), bn); err == nil {
					t.Fatal("successor activated without predecessor commit")
				}
			}
			// Reconstruct both processes from only their stores.
			a = authority(t, a.self, as)
			b = authority(t, b.self, bs)
			an = &network{caller: a.self, nodes: map[string]*Authority{a.self: a, b.self: b}}
			if _, err := a.Recover(context.Background(), an); err != nil {
				t.Fatal(err)
			}
			if a.State().Pending != nil || b.State().Pending != nil || a.State().Record.Owner != b.self || a.State().Record.Digest != b.State().Record.Digest || a.State().Record.RoutingOwner != before.RoutingOwner {
				t.Fatal("restart recovery did not converge without changing target")
			}
		})
	}
}

func TestPreparedSuccessorRequiresCommittedPredecessorAndCanRecoverItself(t *testing.T) {
	a, b, _, _, an, bn, before := setup(t)
	an.beforeActivate = func() error { return injectedFailure }
	if _, err := a.Transfer(context.Background(), b.self, before.Version, an); err == nil {
		t.Fatal("activation outage ignored")
	}
	if b.State().Record.Owner != a.self {
		t.Fatal("successor ignored activation outage")
	}
	if err := a.Cancel(context.Background(), an); !errors.Is(err, ErrTransferPending) {
		t.Fatalf("rolled back committed transfer: %v", err)
	}
	if _, err := b.Recover(context.Background(), bn); err != nil {
		t.Fatal(err)
	}
	// Lost activation acknowledgements remain valid after the new owner edits
	// its policy; a retry must not leave the predecessor stuck forever.
	current := b.State().Record.Clone()
	if _, err := b.Update(policy(), current.RoutingOwner, current.Gateways, current.Version); err != nil {
		t.Fatal(err)
	}
	an.beforeActivate = nil
	if _, err := a.Recover(context.Background(), an); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sync(context.Background(), an); err != nil {
		t.Fatal(err)
	}
	if a.State().Record.Digest != b.State().Record.Digest {
		t.Fatal("new-owner edits prevented acknowledgement recovery")
	}
}

func TestCancellationUnfreezesOnlyUncommittedTransfer(t *testing.T) {
	a, b, _, _, an, _, before := setup(t)
	an.afterPrepare = func() error { return injectedFailure }
	if _, err := a.Transfer(context.Background(), b.self, before.Version, an); err == nil {
		t.Fatal("prepare failure ignored")
	}
	if _, err := a.Update(policy(), before.RoutingOwner, before.Gateways, before.Version); !errors.Is(err, ErrTransferPending) {
		t.Fatalf("edited a frozen proposal: %v", err)
	}
	if err := a.Cancel(context.Background(), an); err != nil {
		t.Fatal(err)
	}
	if a.State().Pending != nil || b.State().Pending != nil || a.State().Record.Digest != before.Digest || b.State().Record.Digest != before.Digest {
		t.Fatal("cancellation changed selection or left successor staged")
	}
	if _, err := a.Update(policy(), before.RoutingOwner, before.Gateways, before.Version); err != nil {
		t.Fatal(err)
	}
}

func TestReplicaCannotEditDuringOwnerOutageOrAcceptChangedVersion(t *testing.T) {
	a, b, _, _, _, bn, before := setup(t)
	delete(bn.nodes, a.self)
	if _, err := b.Sync(context.Background(), bn); err == nil {
		t.Fatal("offline owner reported current")
	}
	if _, err := b.Update(policy(), b.self, before.Gateways, before.Version); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("offline replica took over: %v", err)
	}
	if _, err := b.Transfer(context.Background(), b.self, before.Version, bn); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("offline replica elected itself: %v", err)
	}
	if b.State().Record.Digest != before.Digest {
		t.Fatal("source outage changed cached selector")
	}
	bn.nodes[a.self] = a
	corrupt := before.Clone()
	corrupt.Policy.AllowedUserIDs = []uint64{100}
	corrupt, _ = seal(corrupt)
	a.mu.Lock()
	a.state.Record = &corrupt
	a.mu.Unlock()
	if _, err := b.Sync(context.Background(), bn); !errors.Is(err, ErrConflict) {
		t.Fatalf("accepted changed content at same version: %v", err)
	}
}

func TestTransferCannotEnrollAnUnjoinedGatewayOrChangeItsTarget(t *testing.T) {
	a, b, _, _, an, bn, before := setup(t)
	b = authority(t, b.self, &memoryStore{})
	an.nodes[b.self] = b
	if _, err := a.Transfer(context.Background(), b.self, before.Version, an); err == nil {
		t.Fatal("pairing implicitly enrolled successor")
	}
	if _, err := b.Join(context.Background(), a.self, 0, bn); err != nil {
		t.Fatal(err)
	}
	prepared := a.State().Pending
	wrong := prepared.Next.Clone()
	wrong.RoutingOwner = b.self
	wrong, _ = seal(wrong)
	if _, err := b.Prepare(a.self, before, wrong); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("transfer also selected another routing computer: %v", err)
	}
	if _, err := a.Recover(context.Background(), an); err != nil {
		t.Fatal(err)
	}
}

func TestSynchronizingAnOwnerClaimDoesNotReplacePreparedActivation(t *testing.T) {
	a, b, _, _, _, bn, before := setup(t)
	next := before.Clone()
	next.Owner, next.Version, next.Epoch = b.self, before.Version+1, before.Epoch+1
	next.PreviousOwner, next.Predecessor = a.self, before.Digest
	next, err := seal(next)
	if err != nil {
		t.Fatal(err)
	}
	// An authenticated authority's claim alone cannot create a writable local
	// owner: this successor has no durable prepared handoff to activate.
	a.mu.Lock()
	a.state.Record = &next
	a.mu.Unlock()
	if _, err := b.Sync(context.Background(), bn); !errors.Is(err, ErrTransferPending) {
		t.Fatalf("sync elected an unprepared local owner: %v", err)
	}
	if b.State().Record.Owner != a.self {
		t.Fatal("sync granted local ownership")
	}
}

func TestFailedPersistenceNeverPublishesASelectorMutation(t *testing.T) {
	a, b, as, _, an, _, before := setup(t)
	ctx, _, done, err := a.Begin(context.Background(), before.Digest, b.self, Selector)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	as.failNext()
	if _, err := a.Update(policy(), b.self, before.Gateways, before.Version); err == nil {
		t.Fatal("failed save accepted")
	}
	if a.State().Record.Digest != before.Digest {
		t.Fatal("failed update changed published record")
	}
	if ctx.Err() != nil {
		t.Fatal("failed selector update cancelled the still-current request")
	}
	called := false
	an.beforePrepare = func() error { called = true; return nil }
	as.failNext()
	if _, err := a.Transfer(context.Background(), b.self, before.Version, an); err == nil {
		t.Fatal("failed staging accepted")
	}
	if called || a.State().Pending != nil || a.State().Record.Digest != before.Digest {
		t.Fatal("unstored handoff reached successor or changed publication")
	}
}

func TestSharedRequestRolesAndVersionChangesFenceActiveRequests(t *testing.T) {
	a, b, _, _, an, _, before := setup(t)
	selector, _, selectorDone, err := a.Begin(context.Background(), before.Digest, b.self, Selector)
	if err != nil {
		t.Fatal(err)
	}
	defer selectorDone()
	gateway, _, gatewayDone, err := b.Begin(context.Background(), before.Digest, b.self, Gateway)
	if err != nil {
		t.Fatal(err)
	}
	defer gatewayDone()
	for _, attempt := range []struct {
		self  *Authority
		actor string
		role  Role
	}{
		{a, strings.Repeat("d", 64), Selector},
		{b, a.self, Selector},
		{a, b.self, Routing},
		{b, a.self, Gateway},
	} {
		if _, _, _, err := attempt.self.Begin(context.Background(), before.Digest, attempt.actor, attempt.role); !errors.Is(err, ErrNotMember) {
			t.Fatalf("unauthorized %s request: %v", attempt.role, err)
		}
	}
	an.afterPrepare = func() error { return injectedFailure }
	if _, err := a.Transfer(context.Background(), b.self, before.Version, an); err == nil {
		t.Fatal("prepare failure ignored")
	}
	if selector.Err() != nil || gateway.Err() != nil {
		t.Fatal("staging changed the still-committed serving record")
	}
	if err := a.Cancel(context.Background(), an); err != nil {
		t.Fatal(err)
	}
	if selector.Err() != nil || gateway.Err() != nil {
		t.Fatal("cancelling a proposal retired active routes")
	}
	if _, err := a.Update(policy(), before.RoutingOwner, before.Gateways, before.Version); err != nil {
		t.Fatal(err)
	}
	if selector.Err() == nil {
		t.Fatal("successful policy change left old selector requests alive")
	}
	if gateway.Err() != nil {
		t.Fatal("unsynchronized gateway guessed another authority's new version")
	}
	if _, err := b.Sync(context.Background(), &network{caller: b.self, nodes: an.nodes}); err != nil {
		t.Fatal(err)
	}
	if gateway.Err() == nil {
		t.Fatal("gateway retained streams after accepting new selector record")
	}
	if _, _, _, err := a.Begin(context.Background(), before.Digest, a.self, Selector); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale request reached selector: %v", err)
	}
}
