package models

// PeerEndpoint identifies a declared application at its owning computer and
// exact snapshot revision. Port is resolved exclusively by that owner.
type PeerEndpoint struct {
	OwnerFingerprint string `json:"ownerFingerprint"`
	WorkspaceID      string `json:"workspaceId"`
	ApplicationID    string `json:"applicationId"`
	Version          uint64 `json:"version"`
	Digest           string `json:"digest"`
}

func (endpoint *PeerEndpoint) Clone() *PeerEndpoint {
	if endpoint == nil {
		return nil
	}
	copy := *endpoint
	return &copy
}
