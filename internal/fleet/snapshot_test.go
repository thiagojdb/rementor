package fleet

import (
	"strings"
	"testing"

	"github.com/thiagojdb/rementor/internal/models"
)

func fixtureIntent() WorkspaceIntent {
	return WorkspaceIntent{Scope: Scope{WorkspaceID: "dev", EnvironmentID: "dev"}, Type: models.WorkspaceTypeRouting, Name: "Dev", Routing: models.RoutingConfig{LocalDomain: "dev.localhost"}, Applications: []models.ApplicationConfig{{ID: "orders", Path: "/orders", PublicPath: "/orders", Port: 8080, Active: true}}}
}

func TestSnapshotRejectsChangedContentIdentitySchemaAndScope(t *testing.T) {
	owner := strings.Repeat("a", 64)
	snapshot, err := NewSnapshot(owner, 7, fixtureIntent())
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Validate(owner, "dev"); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Snapshot){
		func(s *Snapshot) { s.Version++ },
		func(s *Snapshot) { s.SchemaVersion++ },
		func(s *Snapshot) { s.OwnerFingerprint = strings.Repeat("b", 64) },
		func(s *Snapshot) { s.Intent.Scope.WorkspaceID = "different" },
		func(s *Snapshot) { s.Intent.Scope.SessionID = "different" },
		func(s *Snapshot) { s.Intent.Routing.LocalDomain = "other.localhost" },
		func(s *Snapshot) { s.Digest = "bad" },
	} {
		candidate := snapshot
		change(&candidate)
		if candidate.Validate(owner, "dev") == nil {
			t.Fatal("accepted a modified snapshot")
		}
	}
	intent := fixtureIntent()
	intent.Applications[0].LastOperation = &models.OperationMetadata{OperationID: "internal"}
	if _, err := NewSnapshot(owner, 7, intent); err == nil {
		t.Fatal("exported internal operation data")
	}
}
