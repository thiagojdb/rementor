// Package fleet defines the portable intent exchanged by authenticated peers.
// A snapshot describes endpoints in its owner's namespace, never the receiver's.
package fleet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/thiagojdb/rementor/internal/models"
	"github.com/thiagojdb/rementor/internal/validation"
)

const SchemaVersion = 1
const MaxSnapshotBytes = 4 << 20

var ErrWorkspaceNotFound = errors.New("workspace not found")

// Scope identifies both the environment and, when selected, its isolated session.
type Scope = models.PeerScope

type WorkspaceIntent struct {
	Parents      []models.PeerWorkspaceReference `json:"parents,omitempty"`
	Scope        Scope                           `json:"scope"`
	Name         string                          `json:"name"`
	Type         string                          `json:"type"`
	Routing      models.RoutingConfig            `json:"routing"`
	Applications []models.ApplicationConfig      `json:"applications"`
}

type Snapshot struct {
	SchemaVersion    int             `json:"schemaVersion"`
	OwnerFingerprint string          `json:"ownerFingerprint"`
	Version          uint64          `json:"version"`
	Digest           string          `json:"digest"`
	Intent           WorkspaceIntent `json:"intent"`
}

type WorkspaceSummary struct {
	Scope   Scope  `json:"scope"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Version uint64 `json:"version"`
}

type Catalog struct {
	SchemaVersion    int                `json:"schemaVersion"`
	OwnerFingerprint string             `json:"ownerFingerprint"`
	Workspaces       []WorkspaceSummary `json:"workspaces"`
}

// WorkspaceSource returns detached intent. No health timestamps, operation
// journals, management credentials or mutable runtime objects cross this seam.
type WorkspaceSource interface {
	ExportPeerWorkspaces() []WorkspaceSummary
	ExportPeerWorkspace(string) (WorkspaceIntent, uint64, error)
}

func validDigest(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == sha256.Size && hex.EncodeToString(raw) == value
}

func ValidateScope(scope Scope) error {
	if err := validation.Identifier("workspace ID", scope.WorkspaceID); err != nil {
		return err
	}
	if err := validation.Identifier("environment ID", scope.EnvironmentID); err != nil {
		return err
	}
	if scope.SessionID == "" {
		if scope.WorkspaceID != scope.EnvironmentID {
			return errors.New("environment scope does not match its workspace")
		}
	} else if scope.SessionID != scope.WorkspaceID {
		return errors.New("session scope does not match its workspace")
	}
	return nil
}

func (intent WorkspaceIntent) Validate() error {
	if err := ValidateScope(intent.Scope); err != nil {
		return err
	}
	if len(intent.Applications) > 4096 {
		return errors.New("too many applications in peer snapshot")
	}
	if len(intent.Parents) > 32 {
		return errors.New("inheritance chain exceeds 32 scopes")
	}
	for _, parent := range intent.Parents {
		if !validDigest(parent.OwnerFingerprint) || !validDigest(parent.Digest) {
			return errors.New("invalid inheritance parent identity or digest")
		}
		if err := ValidateScope(parent.Scope); err != nil {
			return err
		}
	}
	for _, app := range intent.Applications {
		if app.Route != (models.RouteState{}) || app.LastOperation != nil {
			return errors.New("peer snapshot contains runtime route state")
		}
		if err := validation.IdentityIdentifier("application binding ID", app.ID); err != nil {
			return err
		}
	}
	return validation.Workspace(intent.Type, intent.Routing.LocalDomain, intent.Routing.DefaultRemoteBaseURL, intent.Applications)
}

func (snapshot Snapshot) contentDigest() (string, error) {
	snapshot.Digest = ""
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	if len(raw) > MaxSnapshotBytes {
		return "", errors.New("peer snapshot exceeds the size limit")
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}

func NewSnapshot(owner string, version uint64, intent WorkspaceIntent) (Snapshot, error) {
	snapshot := Snapshot{SchemaVersion: SchemaVersion, OwnerFingerprint: owner, Version: version, Intent: intent}
	if !validDigest(owner) {
		return snapshot, errors.New("invalid snapshot owner")
	}
	if err := intent.Validate(); err != nil {
		return snapshot, err
	}
	if err := snapshot.validateParents(); err != nil {
		return snapshot, err
	}
	digest, err := snapshot.contentDigest()
	snapshot.Digest = digest
	return snapshot, err
}

func (snapshot Snapshot) Validate(owner, workspace string) error {
	if snapshot.SchemaVersion != SchemaVersion || snapshot.OwnerFingerprint != owner || !validDigest(owner) {
		return errors.New("peer snapshot identity or schema mismatch")
	}
	if snapshot.Intent.Scope.WorkspaceID != workspace {
		return errors.New("peer returned a different workspace")
	}
	if err := snapshot.Intent.Validate(); err != nil {
		return fmt.Errorf("invalid peer intent: %w", err)
	}
	if err := snapshot.validateParents(); err != nil {
		return err
	}
	digest, err := snapshot.contentDigest()
	if err != nil {
		return err
	}
	if !validDigest(snapshot.Digest) || snapshot.Digest != digest {
		return errors.New("peer snapshot digest mismatch")
	}
	return nil
}

func (snapshot Snapshot) validateParents() error {
	seen := map[string]bool{snapshot.OwnerFingerprint + "/" + snapshot.Intent.Scope.WorkspaceID: true}
	for _, parent := range snapshot.Intent.Parents {
		key := parent.OwnerFingerprint + "/" + parent.Scope.WorkspaceID
		if seen[key] {
			return errors.New("environment inheritance cycle")
		}
		seen[key] = true
	}
	return nil
}

func (catalog Catalog) Validate(owner string) error {
	if catalog.SchemaVersion != SchemaVersion || catalog.OwnerFingerprint != owner || !validDigest(owner) || len(catalog.Workspaces) > 4096 {
		return errors.New("invalid peer workspace catalog")
	}
	seen := map[string]bool{}
	for _, workspace := range catalog.Workspaces {
		if err := ValidateScope(workspace.Scope); err != nil {
			return err
		}
		if seen[workspace.Scope.WorkspaceID] || (workspace.Type != models.WorkspaceTypeRouting && workspace.Type != models.WorkspaceTypeLocalApps) {
			return errors.New("duplicate or invalid peer workspace")
		}
		seen[workspace.Scope.WorkspaceID] = true
	}
	return nil
}
