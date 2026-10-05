package models

import "encoding/json"

// ComputerFollowing mirrors environment definitions. Local sessions and extra
// environments remain owned by this computer; originals allow an exact detach.
type ComputerFollowing struct {
	Revision         uint64                `json:"revision"`
	OwnerFingerprint string                `json:"ownerFingerprint,omitempty"`
	Snapshot         json.RawMessage       `json:"snapshot,omitempty"`
	Originals        map[string]*Workspace `json:"originals,omitempty"`
	Excluded         map[string]bool       `json:"excluded,omitempty"`
}
