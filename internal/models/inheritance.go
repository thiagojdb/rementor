package models

import (
	"encoding/json"
	"time"
)

type PeerScope struct {
	WorkspaceID   string `json:"workspaceId"`
	EnvironmentID string `json:"environmentId"`
	SessionID     string `json:"sessionId,omitempty"`
}

type PeerWorkspaceReference struct {
	OwnerFingerprint string    `json:"ownerFingerprint"`
	Scope            PeerScope `json:"scope"`
	Version          uint64    `json:"version"`
	Digest           string    `json:"digest"`
}

type RouteSelection struct {
	RoutePattern  *string `json:"routePattern,omitempty"`
	Mode          string  `json:"mode"`
	Port          int     `json:"port,omitempty"`
	RemoteBaseURL string  `json:"remoteBaseUrl,omitempty"`
}

// Snapshot retains the exact versioned wire document. The fleet module validates
// its schema, scope and digest before projecting or accepting a persisted cache.
// Keeping it opaque here avoids coupling workspace persistence to that protocol.
type WorkspaceInheritance struct {
	SchemaVersion        int                       `json:"schemaVersion"`
	Source               PeerWorkspaceReference    `json:"source"`
	Snapshot             json.RawMessage           `json:"snapshot"`
	OriginalRouting      RoutingConfig             `json:"originalRouting"`
	OriginalApplications []ApplicationConfig       `json:"originalApplications"`
	Overrides            map[string]RouteSelection `json:"overrides"`
	UpdatedAt            time.Time                 `json:"updatedAt"`
}

func CloneApplicationConfigs(source []ApplicationConfig) []ApplicationConfig {
	result := append([]ApplicationConfig(nil), source...)
	for i := range result {
		result[i].Aliases = append([]string(nil), source[i].Aliases...)
		result[i].PeerEndpoint = source[i].PeerEndpoint.Clone()
		if source[i].RoutePattern != nil {
			value := *source[i].RoutePattern
			result[i].RoutePattern = &value
		}
		if source[i].LastOperation != nil {
			value := *source[i].LastOperation
			result[i].LastOperation = &value
		}
	}
	return result
}

func (source *WorkspaceInheritance) Clone() *WorkspaceInheritance {
	if source == nil {
		return nil
	}
	copy := *source
	copy.Snapshot = append(json.RawMessage(nil), source.Snapshot...)
	copy.OriginalApplications = CloneApplicationConfigs(source.OriginalApplications)
	copy.Overrides = make(map[string]RouteSelection, len(source.Overrides))
	for id, selection := range source.Overrides {
		if selection.RoutePattern != nil {
			pattern := *selection.RoutePattern
			selection.RoutePattern = &pattern
		}
		copy.Overrides[id] = selection
	}
	return &copy
}
