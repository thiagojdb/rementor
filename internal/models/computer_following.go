package models

import "encoding/json"

// ComputerFollowing mirrors environments and their source sessions. Independent
// local sessions and extra environments retain their ownership and baselines.
type ComputerFollowing struct {
	Revision         uint64                `json:"revision"`
	OwnerFingerprint string                `json:"ownerFingerprint,omitempty"`
	Snapshot         json.RawMessage       `json:"snapshot,omitempty"`
	Originals        map[string]*Workspace `json:"originals,omitempty"`
	Excluded         map[string]bool       `json:"excluded,omitempty"`
	// SessionIDs maps source session IDs to receiver IDs, including sessions
	// imported before whole-computer following was enabled.
	SessionIDs map[string]string `json:"sessionIds,omitempty"`
}
