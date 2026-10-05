package tailshare

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/thiagojdb/rementor/internal/access"
)

type memoryStore struct {
	c    Configuration
	fail bool
}

func (s *memoryStore) Load() (Configuration, error) { return s.c.Clone(), nil }
func (s *memoryStore) Save(c Configuration, expected uint64) (Configuration, error) {
	if s.fail {
		return c, errors.New("disk unavailable")
	}
	if expected != s.c.Version {
		return c, ErrConflict
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	c.Version = expected + 1
	s.c = c.Clone()
	return c, nil
}

type fakeProvider struct {
	gateway        Gateway
	entries        map[string]Origin
	failRemove     bool
	failAfterClaim bool
	failVerify     bool
	updates        int
	owner          string
}

func (p *fakeProvider) Inspect(context.Context) (Gateway, error) { return p.gateway, nil }
func (p *fakeProvider) Ensure(_ context.Context, o Origin, owner Ownership, claim func() error) error {
	if existing, ok := p.entries[o.Name]; ok && (!owner.Claimed || existing != o) {
		return errors.New("conflict")
	}
	if err := claim(); err != nil {
		return err
	}
	p.owner = owner.Publication
	if _, ok := p.entries[o.Name]; !ok {
		p.entries[o.Name] = o
		p.updates++
	}
	if p.failAfterClaim {
		return errors.New("interrupted after registration")
	}
	return nil
}
func (p *fakeProvider) Remove(_ context.Context, o Origin) error {
	if p.failRemove {
		return errors.New("offline")
	}
	if current, ok := p.entries[o.Name]; ok && current != o {
		return errors.New("externally changed")
	}
	delete(p.entries, o.Name)
	return nil
}
func (p *fakeProvider) Verify(context.Context, Origin, access.Configuration) error {
	if p.failVerify {
		return errors.New("unverified")
	}
	return nil
}
func sharingFixture(t *testing.T) (*Manager, *memoryStore, *fakeProvider, *access.Configuration, Origin) {
	t.Helper()
	origin := Origin{ID: "api", Name: "dev-giss", URL: "https://dev-giss.tail.test.ts.net", Target: "http://127.0.0.1:9302"}
	policy := &access.Configuration{Revision: 1, Document: access.Document{SchemaVersion: 1, Origins: []access.Origin{{ID: origin.ID, URL: origin.URL}}, Bindings: []access.Binding{{WorkspaceID: "dev", OriginID: "api", LocalHost: "dev.giss.localhost"}}, SharedWorkspaceID: "dev", AllowedUserIDs: []uint64{99}}}
	store := &memoryStore{}
	provider := &fakeProvider{gateway: Gateway{DNSSuffix: "tail.test.ts.net", NodeID: "gateway"}, entries: map[string]Origin{}}
	m, err := New(store, provider, func() access.Configuration { return *policy })
	if err != nil {
		t.Fatal(err)
	}
	return m, store, provider, policy, origin
}
func TestSharingRetriesAndPreservesOwnershipAcrossCrashAndDisable(t *testing.T) {
	m, store, provider, policy, o := sharingFixture(t)
	provider.gateway.Setup = "dedicated gateway needed"
	if _, err := m.Set(true, []Origin{o}, 0); err != nil {
		t.Fatal(err)
	}
	m.Reconcile(context.Background())
	if !store.c.Enabled || len(provider.entries) != 0 || m.State().Publications[0].Status != "needs_setup" {
		t.Fatal("setup requirement caused network mutation or lost intent")
	}
	provider.gateway.Setup = ""
	provider.failAfterClaim = true
	m.Reconcile(context.Background())
	if len(store.c.Owned) != 1 || len(provider.entries) != 1 {
		t.Fatal("partial registration was not durably claimed")
	}
	provider.failAfterClaim = false
	provider.failVerify = true
	restarted, err := New(store, provider, func() access.Configuration { return *policy })
	if err != nil {
		t.Fatal(err)
	}
	restarted.Reconcile(context.Background())
	if restarted.State().Publications[0].Status != "awaiting_verification" {
		t.Fatal("unverified URL reported ready")
	}
	provider.failVerify = false
	restarted.Reconcile(context.Background())
	if restarted.State().Publications[0].Status != "ready" || provider.updates != 1 {
		t.Fatal("restart recreated registration")
	}
	if _, err := restarted.Set(false, nil, 0); !errors.Is(err, ErrConflict) {
		t.Fatal("stale toggle accepted", err)
	}
	provider.failRemove = true
	if _, err := restarted.Set(false, nil, 1); err != nil {
		t.Fatal(err)
	}
	restarted.Reconcile(context.Background())
	if len(store.c.Owned) != 1 || restarted.State().Publications[0].Status != "cleanup_failed" {
		t.Fatal("lost failed cleanup")
	}
	provider.failRemove = false
	again, err := New(store, provider, func() access.Configuration { return *policy })
	if err != nil {
		t.Fatal(err)
	}
	again.Reconcile(context.Background())
	if len(provider.entries) != 0 || len(store.c.Owned) != 0 || again.State().Publications[0].Status != "off" {
		t.Fatal("restart did not finish disabled cleanup")
	}
}
func TestPublicationChangesFollowPolicyWithoutReconfiguringBackendToggles(t *testing.T) {
	m, store, p, policy, o := sharingFixture(t)
	if _, err := m.Set(true, []Origin{o}, 0); err != nil {
		t.Fatal(err)
	}
	m.Reconcile(context.Background())
	policy.Revision++
	m.Reconcile(context.Background())
	if p.updates != 1 {
		t.Fatal("route/policy revision caused network rewrite")
	}
	previousOwner := store.c.Owner
	m.SharedOwner = func() string { return "shared-group" }
	m.Reconcile(context.Background())
	if p.owner != "shared-group" || store.c.Owner != previousOwner {
		t.Fatal("shared identity depended on serving host")
	}
	policy.Document.Origins[0].URL = "https://feature-dev-giss.tail.test.ts.net"
	m.Reconcile(context.Background())
	if _, ok := p.entries[o.Name]; ok || len(p.entries) != 1 || len(store.c.Owned) != 1 || store.c.Origins[0].Name != "feature-dev-giss" {
		t.Fatal("did not reconcile changed owner URLs")
	}
	policy.Document = access.Empty()
	m.Reconcile(context.Background())
	if len(p.entries) != 0 || len(store.c.Owned) != 0 {
		t.Fatal("removed policy retained network publication")
	}
}
func TestNoProviderMutationWhenPersistenceFailsOrNameIsForeign(t *testing.T) {
	m, store, p, _, o := sharingFixture(t)
	store.fail = true
	if _, err := m.Set(true, []Origin{o}, 0); err == nil {
		t.Fatal("failed persistence reported success")
	}
	m.Reconcile(context.Background())
	if len(p.entries) != 0 {
		t.Fatal("mutated provider before persisting toggle")
	}
	store.fail = false
	if _, err := m.Set(true, []Origin{o}, 0); err != nil {
		t.Fatal(err)
	}
	store.fail = true
	m.Reconcile(context.Background())
	if len(p.entries) != 0 {
		t.Fatal("mutated provider before persisting claim")
	}
	store.fail = false
	p.entries[o.Name] = o
	m.Reconcile(context.Background())
	if len(store.c.Owned) != 0 || m.State().Publications[0].Status == "ready" {
		t.Fatal("adopted a foreign Serve entry")
	}
}
func TestNamesAndOriginsRespectTailnetAndIndependentTopology(t *testing.T) {
	names := SuggestedNames([]string{"dev.giss.localhost", "dev-giss.localhost", "feature-x.dev.giss.localhost", strings.Repeat("a", 80) + ".localhost"})
	if names["dev.giss.localhost"] == names["dev-giss.localhost"] || names["feature-x.dev.giss.localhost"] != "feature-x-dev-giss" {
		t.Fatal("names collide or lose recognizable route labels", names)
	}
	for _, name := range names {
		if !labelPattern.MatchString(name) {
			t.Fatal("invalid service label", name)
		}
	}
	duplicates := SuggestedNames([]string{"dev.giss.localhost", "dev-giss.localhost", "dev.giss.localhost"})
	if duplicates["dev.giss.localhost"] != names["dev.giss.localhost"] {
		t.Fatal("aliases changed stable suggestions")
	}
	doc := access.Document{Origins: []access.Origin{{ID: "frontend", URL: "https://dev-giss.tail.test.ts.net"}, {ID: "api", URL: "https://api-giss.tail.test.ts.net"}, {ID: "alias", URL: "https://dev-giss.tail.test.ts.net"}}}
	origins, err := Origins(doc, "tail.test.ts.net", "http://127.0.0.1:9302")
	if err != nil || len(origins) != 2 {
		t.Fatal("collapsed independent origin or failed alias deduplication", err, origins)
	}
	for _, bad := range []string{"https://dev-giss.other.ts.net", "https://dev-giss.tail.test.ts.net:8444", "http://giss.localhost", "https://dev.giss.tail.test.ts.net"} {
		doc.Origins = []access.Origin{{ID: "api", URL: bad}}
		if _, err := Origins(doc, "tail.test.ts.net", "http://127.0.0.1:9302"); err == nil {
			t.Fatal("accepted non-service origin", bad)
		}
	}
	copy := Configuration{Origins: origins}.Clone()
	copy.Origins[0].Name = "mutated"
	if reflect.DeepEqual(copy.Origins, origins) {
		t.Fatal("state aliases caller data")
	}
}

func TestChangedGatewayCannotMutateAnotherNodesServices(t *testing.T) {
	m, store, p, _, o := sharingFixture(t)
	if _, err := m.Set(true, []Origin{o}, 0); err != nil {
		t.Fatal(err)
	}
	m.Reconcile(context.Background())
	if store.c.GatewayID != "gateway" {
		t.Fatal("claim lost gateway identity")
	}
	p.gateway.NodeID = "another-gateway"
	if _, err := m.Set(false, nil, 1); err != nil {
		t.Fatal(err)
	}
	m.Reconcile(context.Background())
	if len(p.entries) != 1 || len(store.c.Owned) != 1 || !strings.Contains(m.State().Error, "identity changed") {
		t.Fatal("changed gateway removed the original publication")
	}
	p.gateway.NodeID = "gateway"
	m.Reconcile(context.Background())
	if len(p.entries) != 0 {
		t.Fatal("original gateway could not finish cleanup")
	}
}
