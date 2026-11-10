package fleet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"github.com/thiagojdb/rementor/internal/models"
)

const MaxConfigurationBytes = 16 << 20

type ConfigurationEnvironment struct {
	Snapshot Snapshot `json:"snapshot"`
	Color    string   `json:"color,omitempty"`
}
type ConfigurationSession struct {
	ConfigurationEnvironment
	Session models.RoutingSession `json:"session"`
}
type ConfigurationSnapshot struct {
	SchemaVersion    int                        `json:"schemaVersion"`
	OwnerFingerprint string                     `json:"ownerFingerprint"`
	Parents          []string                   `json:"parents,omitempty"`
	Environments     []ConfigurationEnvironment `json:"environments"`
	Sessions         []ConfigurationSession     `json:"sessions,omitempty"`
	Digest           string                     `json:"digest"`
}

func (s ConfigurationSnapshot) contentDigest() string {
	s.Digest = ""
	raw, _ := json.Marshal(s)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func NewConfiguration(owner string, parents []string, environments []ConfigurationEnvironment, sessions ...ConfigurationSession) (ConfigurationSnapshot, error) {
	s := ConfigurationSnapshot{SchemaVersion: 1, OwnerFingerprint: owner, Parents: append([]string(nil), parents...), Environments: append([]ConfigurationEnvironment(nil), environments...), Sessions: append([]ConfigurationSession(nil), sessions...)}
	sort.Slice(s.Environments, func(i, j int) bool {
		return s.Environments[i].Snapshot.Intent.Scope.WorkspaceID < s.Environments[j].Snapshot.Intent.Scope.WorkspaceID
	})
	sort.Slice(s.Sessions, func(i, j int) bool {
		return s.Sessions[i].Snapshot.Intent.Scope.WorkspaceID < s.Sessions[j].Snapshot.Intent.Scope.WorkspaceID
	})
	s.Digest = s.contentDigest()
	return s, s.Validate(owner)
}
func (s ConfigurationSnapshot) Validate(owner string) error {
	if s.SchemaVersion != 1 || !validDigest(owner) || s.OwnerFingerprint != owner || !validDigest(s.Digest) || s.Digest != s.contentDigest() {
		return errors.New("invalid computer configuration identity, schema or digest")
	}
	if len(s.Environments) > 256 || len(s.Sessions) > 4096 || len(s.Parents) > 32 {
		return errors.New("computer configuration exceeds limits")
	}
	seen := map[string]bool{owner: true}
	for _, id := range s.Parents {
		if !validDigest(id) || seen[id] {
			return errors.New("invalid computer inheritance ancestry")
		}
		seen[id] = true
	}
	seen = map[string]bool{}
	for _, environment := range s.Environments {
		snapshot := environment.Snapshot
		id := snapshot.Intent.Scope.WorkspaceID
		if snapshot.Intent.Scope.SessionID != "" || seen[id] {
			return errors.New("computer configuration contains a session or duplicate environment")
		}
		if err := snapshot.Validate(owner, id); err != nil {
			return err
		}
		if len(environment.Color) > 64 {
			return errors.New("invalid environment color")
		}
		seen[id] = true
	}
	environments := make(map[string]bool, len(seen))
	for id := range seen {
		environments[id] = true
	}
	for _, entry := range s.Sessions {
		snapshot := entry.Snapshot
		scope := snapshot.Intent.Scope
		if scope.SessionID == "" || seen[scope.WorkspaceID] || !environments[scope.EnvironmentID] || entry.Session.EnvironmentID != scope.EnvironmentID || snapshot.Intent.Type != models.WorkspaceTypeRouting {
			return errors.New("invalid session or parent in computer configuration")
		}
		if err := snapshot.Validate(owner, scope.WorkspaceID); err != nil {
			return err
		}
		if len(entry.Color) > 64 || len(entry.Session.HostPrefix) > 63 || len(entry.Session.Baseline) > 4096 {
			return errors.New("invalid session metadata")
		}
		baseline := WorkspaceIntent{Scope: scope, Type: models.WorkspaceTypeRouting, Routing: models.RoutingConfig{LocalDomain: snapshot.Intent.Routing.LocalDomain, DefaultRemoteBaseURL: entry.Session.BaselineRemoteURL}, Applications: entry.Session.Baseline}
		if err := baseline.Validate(); err != nil {
			return err
		}
		for _, app := range entry.Session.Baseline {
			if app.Port != 0 || app.PeerEndpoint != nil || app.Active {
				return errors.New("session baseline contains a device registration")
			}
		}
		seen[scope.WorkspaceID] = true
	}
	raw, _ := json.Marshal(s)
	if len(raw) > MaxConfigurationBytes {
		return errors.New("computer configuration exceeds size limit")
	}
	return nil
}
