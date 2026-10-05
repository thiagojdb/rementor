package peers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/thiagojdb/rementor/internal/fleet"
	"github.com/thiagojdb/rementor/internal/validation"
)

func (m *Manager) SetWorkspaceSource(source fleet.WorkspaceSource) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workspaceSource = source
}

func (m *Manager) trustedPeer(id string) (Peer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	peer, ok := m.state.Peers[id]
	if !ok || m.server == nil {
		return Peer{}, errors.New("peer connection is disabled or computer is not paired")
	}
	return peer, nil
}

func readSnapshotJSON(reader io.Reader, output any) error {
	return readSnapshotJSONLimit(reader, output, fleet.MaxSnapshotBytes)
}
func readSnapshotJSONLimit(reader io.Reader, output any, limit int64) error {
	raw, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > limit {
		return errors.New("peer snapshot exceeds the size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("decode peer snapshot: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("unexpected data after peer snapshot")
	}
	return nil
}

// fetchWorkspaceDocument checks current trust before and after the request. A
// peer revoked while a fetch was in flight cannot deliver a usable candidate.
func (m *Manager) fetchWorkspaceDocument(ctx context.Context, id, path, digest string, output any) (bool, error) {
	peer, err := m.trustedPeer(id)
	if err != nil {
		return false, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, peer.URL+path, nil)
	if err != nil {
		return false, err
	}
	if digest != "" {
		if !validFingerprint(digest) {
			return false, errors.New("invalid cached snapshot digest")
		}
		request.Header.Set("If-None-Match", `"`+digest+`"`)
	}
	client := m.client(id)
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	unchanged := response.StatusCode == http.StatusNotModified
	if unchanged {
		if digest == "" || response.Header.Get("ETag") != `"`+digest+`"` {
			return false, errors.New("invalid unchanged peer snapshot response")
		}
	} else {
		if response.StatusCode != http.StatusOK {
			return false, fmt.Errorf("peer workspace request failed (HTTP %d)", response.StatusCode)
		}
		limit := int64(fleet.MaxSnapshotBytes)
		if path == "/v1/configuration" {
			limit = fleet.MaxConfigurationBytes
		}
		if err := readSnapshotJSONLimit(response.Body, output, limit); err != nil {
			return false, err
		}
	}
	current, err := m.trustedPeer(id)
	if err != nil || current.URL != peer.URL {
		return false, errors.New("peer trust changed during snapshot fetch")
	}
	return unchanged, nil
}

func (m *Manager) PeerWorkspaces(ctx context.Context, id string) (fleet.Catalog, error) {
	var catalog fleet.Catalog
	_, err := m.fetchWorkspaceDocument(ctx, id, "/v1/workspaces", "", &catalog)
	if err != nil {
		return catalog, err
	}
	return catalog, catalog.Validate(id)
}

// FetchWorkspace returns nil when the verified content digest is unchanged.
func (m *Manager) FetchWorkspace(ctx context.Context, id, workspaceID, digest string) (*fleet.Snapshot, error) {
	if err := validation.Identifier("workspace ID", workspaceID); err != nil {
		return nil, err
	}
	var snapshot fleet.Snapshot
	unchanged, err := m.fetchWorkspaceDocument(ctx, id, "/v1/workspaces/"+workspaceID, digest, &snapshot)
	if err != nil || unchanged {
		return nil, err
	}
	if err := snapshot.Validate(id, workspaceID); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (m *Manager) handleWorkspace(w http.ResponseWriter, r *http.Request, caller, owner string, source fleet.WorkspaceSource) {
	if source == nil {
		http.Error(w, "workspace source unavailable", http.StatusServiceUnavailable)
		return
	}
	var document any
	var etag string
	if r.URL.Path == "/v1/workspaces" {
		catalog := fleet.Catalog{SchemaVersion: fleet.SchemaVersion, OwnerFingerprint: owner, Workspaces: source.ExportPeerWorkspaces()}
		if err := catalog.Validate(owner); err != nil {
			http.Error(w, "invalid workspace catalog", http.StatusInternalServerError)
			return
		}
		document = catalog
	} else {
		workspace := strings.TrimPrefix(r.URL.Path, "/v1/workspaces/")
		if validation.Identifier("workspace ID", workspace) != nil || r.URL.RawPath != "" || r.URL.RawQuery != "" {
			http.Error(w, "invalid workspace ID", http.StatusBadRequest)
			return
		}
		intent, version, err := source.ExportPeerWorkspace(workspace)
		if errors.Is(err, fleet.ErrWorkspaceNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "workspace snapshot unavailable", http.StatusInternalServerError)
			return
		}
		snapshot, err := fleet.NewSnapshot(owner, version, intent)
		if err != nil {
			http.Error(w, "invalid workspace snapshot", http.StatusInternalServerError)
			return
		}
		document = snapshot
		etag = `"` + snapshot.Digest + `"`
	}
	// Exporting can block on a concurrent route transaction. Recheck enrollment
	// before releasing data or reporting a cached version as current.
	if _, err := m.trustedPeer(caller); err != nil {
		http.Error(w, "computer is not paired", http.StatusForbidden)
		return
	}
	raw, err := json.Marshal(document)
	if err != nil || len(raw) > fleet.MaxSnapshotBytes {
		http.Error(w, "workspace snapshot exceeds the size limit", http.StatusInternalServerError)
		return
	}
	if etag != "" {
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}
