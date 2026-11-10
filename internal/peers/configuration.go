package peers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/thiagojdb/rementor/internal/fleet"
	"net/http"
)

type configurationSource interface {
	ExportPeerConfiguration() (fleet.ConfigurationSnapshot, error)
}

func (m *Manager) FetchConfiguration(ctx context.Context, id, digest string) (*fleet.ConfigurationSnapshot, error) {
	var snapshot fleet.ConfigurationSnapshot
	unchanged, err := m.fetchWorkspaceDocument(ctx, id, "/v1/configuration", digest, &snapshot)
	if err != nil || unchanged {
		return nil, err
	}
	if err = snapshot.Validate(id); err != nil {
		return nil, err
	}
	return &snapshot, nil
}
func (m *Manager) handleConfiguration(w http.ResponseWriter, r *http.Request, caller string, source fleet.WorkspaceSource) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		http.NotFound(w, r)
		return
	}
	exporter, ok := source.(configurationSource)
	if !ok {
		http.Error(w, "computer configuration unavailable", 503)
		return
	}
	snapshot, err := exporter.ExportPeerConfiguration()
	if err != nil {
		http.Error(w, "computer configuration unavailable", 503)
		return
	}
	if _, err = m.trustedPeer(caller); err != nil {
		http.Error(w, "computer is not paired", 403)
		return
	}
	raw, err := json.Marshal(snapshot)
	if err != nil || len(raw) > fleet.MaxConfigurationBytes {
		http.Error(w, "computer configuration exceeds limits", 500)
		return
	}
	etag := `"` + snapshot.Digest + `"`
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

type configurationEditor interface {
	EditPeerConfigurationWithTrust(context.Context, fleet.ConfigurationEdit, func(func() error) error) error
}

func (m *Manager) EditConfiguration(ctx context.Context, id string, edit fleet.ConfigurationEdit) error {
	if err := edit.Validate(); err != nil {
		return err
	}
	peer, err := m.trustedPeer(id)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(edit)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, peer.URL+"/v1/configuration/edit", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	client := m.client(id)
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if _, err = m.trustedPeer(id); err != nil {
		return err
	}
	if response.StatusCode == http.StatusConflict {
		return fleet.ErrConfigurationConflict
	}
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("source configuration edit failed (HTTP %d)", response.StatusCode)
	}
	return nil
}
func (m *Manager) handleConfigurationEdit(w http.ResponseWriter, r *http.Request, caller string, source fleet.WorkspaceSource) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" {
		http.NotFound(w, r)
		return
	}
	editor, ok := source.(configurationEditor)
	if !ok {
		http.Error(w, "source configuration editing unavailable", 503)
		return
	}
	var edit fleet.ConfigurationEdit
	if err := readSnapshotJSONLimit(r.Body, &edit, 16384); err != nil || edit.Validate() != nil {
		http.Error(w, "invalid configuration edit", 400)
		return
	}
	// Current paired trust authorizes this narrowly typed route mutation. No
	// management RPC or command execution is exposed. The selected remote URL
	// passes the same routing validation as a local source edit.
	err := editor.EditPeerConfigurationWithTrust(r.Context(), edit, func(commit func() error) error { return m.WithTrustedPeer(caller, commit) })
	if err != nil {
		if errors.Is(err, fleet.ErrConfigurationConflict) {
			http.Error(w, "source configuration changed; reload", 409)
			return
		}
		http.Error(w, err.Error(), 400)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
