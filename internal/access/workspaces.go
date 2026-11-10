package access

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/thiagojdb/rementor/internal/validation"
)

// WorkspaceCatalog contains names and published-host candidates in the owning
// computer's namespace. It contains no upstream URLs, ports or credentials.
type WorkspaceHosts struct {
	WorkspaceID string   `json:"workspaceId"`
	Name        string   `json:"name"`
	LocalHosts  []string `json:"localHosts"`
}
type WorkspaceCatalog struct {
	SchemaVersion    int              `json:"schemaVersion"`
	OwnerFingerprint string           `json:"ownerFingerprint"`
	Workspaces       []WorkspaceHosts `json:"workspaces"`
}

func (c WorkspaceCatalog) Validate(owner string) error {
	identity, err := hex.DecodeString(owner)
	if err != nil || len(identity) != 32 || hex.EncodeToString(identity) != owner || c.OwnerFingerprint != owner || c.SchemaVersion != SchemaVersion || len(c.Workspaces) > 1024 {
		return errors.New("invalid routing workspace catalog identity or schema")
	}
	seen := map[string]bool{}
	for _, ws := range c.Workspaces {
		if validation.Identifier("workspace ID", ws.WorkspaceID) != nil || seen[ws.WorkspaceID] || len(ws.Name) > 512 || strings.ContainsAny(ws.Name, "\x00\r\n") || len(ws.LocalHosts) == 0 || len(ws.LocalHosts) > 8192 {
			return errors.New("invalid routing workspace catalog entry")
		}
		seen[ws.WorkspaceID] = true
		hosts := map[string]bool{}
		for _, host := range ws.LocalHosts {
			if validation.LocalHostname(host) != nil || hosts[host] {
				return errors.New("invalid or duplicate routing workspace host")
			}
			hosts[host] = true
		}
	}
	raw, _ := json.Marshal(c)
	if len(raw) > MaxDocumentBytes {
		return errors.New("routing workspace catalog exceeds 1 MiB")
	}
	return nil
}
