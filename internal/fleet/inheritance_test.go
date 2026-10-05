package fleet

import (
	"strings"
	"testing"
	"time"

	"github.com/thiagojdb/rementor/internal/models"
)

func inheritanceSnapshot(t *testing.T, owner string, version uint64, parents []models.PeerWorkspaceReference) Snapshot {
	t.Helper()
	snapshot, err := NewSnapshot(owner, version, WorkspaceIntent{Scope: Scope{WorkspaceID: "dev", EnvironmentID: "dev"}, Type: models.WorkspaceTypeRouting, Routing: models.RoutingConfig{LocalDomain: "source.localhost", DefaultRemoteBaseURL: "https://remote.example.test"}, Parents: parents, Applications: []models.ApplicationConfig{{ID: "orders", PublicPath: "/orders", Port: 8080, Active: true}, {ID: "billing", Domain: "billing.source.localhost", PublicPath: "/billing", Port: 8081, Active: true}}})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestInheritedProjectionTracksParentWithoutDialingReceiverPorts(t *testing.T) {
	owner, receiver := strings.Repeat("a", 64), strings.Repeat("b", 64)
	first := inheritanceSnapshot(t, owner, 7, nil)
	base := models.WorkspaceConfig{ID: "receiving", Type: models.WorkspaceTypeRouting, Routing: models.RoutingConfig{LocalDomain: "receiver.localhost"}, Applications: []models.ApplicationConfig{{ID: "orders", PublicPath: "/orders", Port: 9090}, {ID: "billing", PublicPath: "/billing", Domain: "billing.receiver.localhost"}}}
	scope := Scope{WorkspaceID: "receiving", EnvironmentID: "receiving"}
	projected, err := ProjectInheritance(receiver, scope, base, first, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if projected.Applications[0].Port != 0 || projected.Applications[0].PeerEndpoint == nil || projected.Applications[0].PeerEndpoint.OwnerFingerprint != owner || projected.Applications[1].Domain != "billing.receiver.localhost" || projected.Routing.LocalDomain != "receiver.localhost" {
		t.Fatal("projection lost ownership or receiver hostnames")
	}
	projected.Inheritance.Overrides["billing"] = models.RouteSelection{Mode: "local", Port: 9091}
	second := first.Intent
	second.Applications = models.CloneApplicationConfigs(second.Applications)
	second.Applications[0].Active = false
	second.Applications[0].Port = 8888
	next, err := NewSnapshot(owner, 8, second)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := ProjectInheritance(receiver, scope, projected, next, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if updated.Applications[0].Active || updated.Applications[0].PeerEndpoint.Version != 8 || updated.Applications[0].Port != 0 || !updated.Applications[1].Active || updated.Applications[1].Port != 9091 || updated.Applications[1].PeerEndpoint != nil {
		t.Fatal("parent update lost override or moved source port to receiver")
	}
	if projected.Applications[0].PeerEndpoint.Version != 7 || base.Applications[0].Port != 9090 {
		t.Fatal("projection mutated its inputs")
	}
	remote := updated
	remote.Inheritance = updated.Inheritance.Clone()
	remote.Inheritance.Overrides["billing"] = models.RouteSelection{Mode: "remote", Port: 9091, RemoteBaseURL: "https://custom.example.test"}
	retained, err := ProjectInheritance(receiver, scope, remote, next, time.Now())
	if err != nil || retained.Applications[1].Active || retained.Applications[1].Port != 9091 || retained.Applications[1].PeerEndpoint != nil {
		t.Fatalf("remote selection lost own local registration: %v", err)
	}
	remote.Inheritance.Overrides["billing"] = models.RouteSelection{Mode: "local", Port: 9091, RemoteBaseURL: "https://custom.example.test"}
	local, err := ProjectInheritance(receiver, scope, remote, next, time.Now())
	if err != nil || !local.Applications[1].Active || local.Applications[1].RemoteBaseUrl != "https://custom.example.test" {
		t.Fatalf("local selection lost the configured remote registration: %v", err)
	}
	delete(updated.Inheritance.Overrides, "billing")
	reset, err := ProjectInheritance(receiver, scope, updated, next, time.Now())
	if err != nil || reset.Applications[1].PeerEndpoint == nil || reset.Applications[1].Port != 0 {
		t.Fatalf("reset failed: %v", err)
	}
	if _, err := ProjectInheritance(receiver, scope, updated, first, time.Now()); err == nil {
		t.Fatal("accepted regressed source version")
	}
	changed := next.Intent
	changed.Applications = models.CloneApplicationConfigs(changed.Applications)
	changed.Applications[0].Port++
	sameVersion, err := NewSnapshot(owner, 8, changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ProjectInheritance(receiver, scope, updated, sameVersion, time.Now()); err == nil {
		t.Fatal("accepted changed content at the same version")
	}
}

func TestInheritanceRejectsCyclesCorruptCachesAndMissingOverrides(t *testing.T) {
	owner, receiver := strings.Repeat("a", 64), strings.Repeat("b", 64)
	scope := Scope{WorkspaceID: "receiving", EnvironmentID: "receiving"}
	base := models.WorkspaceConfig{ID: "receiving", Type: models.WorkspaceTypeRouting, Routing: models.RoutingConfig{LocalDomain: "receiver.localhost"}}
	cyclic := inheritanceSnapshot(t, owner, 7, []models.PeerWorkspaceReference{{OwnerFingerprint: receiver, Scope: scope, Version: 1, Digest: strings.Repeat("c", 64)}})
	if _, err := ProjectInheritance(receiver, scope, base, cyclic, time.Now()); err == nil {
		t.Fatal("accepted transitive inheritance cycle")
	}
	snapshot := inheritanceSnapshot(t, owner, 7, nil)
	projected, err := ProjectInheritance(receiver, scope, base, snapshot, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	broken := projected.Inheritance.Clone()
	broken.Snapshot[len(broken.Snapshot)-2] = 'x'
	if _, err := CachedSnapshot(broken); err == nil {
		t.Fatal("accepted corrupted cache")
	}
	projected.Inheritance.Overrides["missing"] = models.RouteSelection{Mode: "remote"}
	if _, err := ProjectInheritance(receiver, scope, projected, snapshot, time.Now()); err == nil {
		t.Fatal("accepted missing override reference")
	}
	self := snapshot.Intent
	self.Parents = []models.PeerWorkspaceReference{SnapshotReference(snapshot)}
	if _, err := NewSnapshot(owner, 8, self); err == nil {
		t.Fatal("source published a cyclic chain")
	}
}
