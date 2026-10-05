package peers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/thiagojdb/rementor/internal/access"
)

const publicWorkspaceCatalogPath = "/v1/public-workspaces"

type publicWorkspaceSource interface {
	PublicWorkspaceHosts() []access.WorkspaceHosts
}

func (m *Manager) PublicWorkspaceCatalog(ctx context.Context, owner string) (access.WorkspaceCatalog, error) {
	var catalog access.WorkspaceCatalog
	if _, err := m.fetchWorkspaceDocument(ctx, owner, publicWorkspaceCatalogPath, "", &catalog); err != nil {
		return catalog, err
	}
	return catalog, catalog.Validate(owner)
}

func (m *Manager) handlePublicWorkspaceCatalog(w http.ResponseWriter, r *http.Request, caller, owner string) {
	if r.Method != http.MethodGet || r.URL.RawPath != "" || r.URL.RawQuery != "" {
		http.NotFound(w, r)
		return
	}
	m.mu.Lock()
	source, ok := m.workspaceSource.(publicWorkspaceSource)
	m.mu.Unlock()
	if !ok {
		http.Error(w, "routing workspace catalog unavailable", http.StatusServiceUnavailable)
		return
	}
	catalog := access.WorkspaceCatalog{SchemaVersion: access.SchemaVersion, OwnerFingerprint: owner, Workspaces: source.PublicWorkspaceHosts()}
	if err := catalog.Validate(owner); err != nil {
		http.Error(w, "invalid routing workspace catalog", http.StatusInternalServerError)
		return
	}
	if _, err := m.trustedPeer(caller); err != nil {
		http.Error(w, "computer is not paired", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(catalog)
}
