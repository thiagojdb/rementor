package fleet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
)

const MaxConfigurationBytes = 16 << 20

type ConfigurationEnvironment struct {
	Snapshot Snapshot `json:"snapshot"`
	Color    string   `json:"color,omitempty"`
}
type ConfigurationSnapshot struct {
	SchemaVersion    int                        `json:"schemaVersion"`
	OwnerFingerprint string                     `json:"ownerFingerprint"`
	Parents          []string                   `json:"parents,omitempty"`
	Environments     []ConfigurationEnvironment `json:"environments"`
	Digest           string                     `json:"digest"`
}

func (s ConfigurationSnapshot) contentDigest() string {
	s.Digest = ""
	raw, _ := json.Marshal(s)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func NewConfiguration(owner string, parents []string, environments []ConfigurationEnvironment) (ConfigurationSnapshot, error) {
	s := ConfigurationSnapshot{SchemaVersion: 1, OwnerFingerprint: owner, Parents: append([]string(nil), parents...), Environments: append([]ConfigurationEnvironment(nil), environments...)}
	sort.Slice(s.Environments, func(i, j int) bool {
		return s.Environments[i].Snapshot.Intent.Scope.WorkspaceID < s.Environments[j].Snapshot.Intent.Scope.WorkspaceID
	})
	s.Digest = s.contentDigest()
	return s, s.Validate(owner)
}
func (s ConfigurationSnapshot) Validate(owner string) error {
	if s.SchemaVersion != 1 || !validDigest(owner) || s.OwnerFingerprint != owner || !validDigest(s.Digest) || s.Digest != s.contentDigest() {
		return errors.New("invalid computer configuration identity, schema or digest")
	}
	if len(s.Environments) > 256 || len(s.Parents) > 32 {
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
	raw, _ := json.Marshal(s)
	if len(raw) > MaxConfigurationBytes {
		return errors.New("computer configuration exceeds size limit")
	}
	return nil
}
