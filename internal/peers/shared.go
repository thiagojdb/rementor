package peers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/thiagojdb/rementor/internal/access"
	"github.com/thiagojdb/rementor/internal/shared"
)

const sharedPrefix = "/v1/shared-ingress"

func (m *Manager) SetSharedAuthority(authority *shared.Authority) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sharedAuthority = authority
}

type sharedPolicyValidator interface{ ValidateSharedPublicPolicy(access.Document) error }

func (m *Manager) ValidateSharedPolicy(ctx context.Context, id string, policy access.Document) error {
	var response sharedResult
	if err := m.sharedMutation(ctx, id, "validate-policy", policy, &response); err != nil {
		return err
	}
	if !response.OK {
		return errors.New("routing computer rejected shared ingress bindings")
	}
	return nil
}

func (m *Manager) FetchShared(ctx context.Context, id string) (shared.Record, error) {
	var record shared.Record
	_, err := m.fetchWorkspaceDocument(ctx, id, sharedPrefix, "", &record)
	if err != nil {
		return record, err
	}
	return record, record.Validate()
}

func (m *Manager) sharedMutation(ctx context.Context, id, action string, input, output any) error {
	peer, err := m.trustedPeer(id)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, peer.URL+sharedPrefix+"/"+action, bytes.NewReader(raw))
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
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("peer shared ingress %s failed (HTTP %d)", action, response.StatusCode)
	}
	if err := readSnapshotJSON(response.Body, output); err != nil {
		return err
	}
	current, err := m.trustedPeer(id)
	if err != nil || current.URL != peer.URL {
		return errors.New("peer trust changed during shared ingress handoff")
	}
	return nil
}

type sharedPrepare struct {
	Before shared.Record `json:"before"`
	Next   shared.Record `json:"next"`
}
type sharedDigest struct {
	Digest string `json:"digest"`
}
type sharedResult struct {
	OK bool `json:"ok"`
}

func (m *Manager) PrepareShared(ctx context.Context, id string, before, next shared.Record) (string, error) {
	var response sharedDigest
	err := m.sharedMutation(ctx, id, "prepare", sharedPrepare{Before: before, Next: next}, &response)
	return response.Digest, err
}
func (m *Manager) ActivateShared(ctx context.Context, id, digest string) error {
	var response sharedResult
	if err := m.sharedMutation(ctx, id, "activate", sharedDigest{Digest: digest}, &response); err != nil {
		return err
	}
	if !response.OK {
		return errors.New("successor did not activate shared ingress ownership")
	}
	return nil
}
func (m *Manager) AbortShared(ctx context.Context, id, digest string) error {
	var response sharedResult
	if err := m.sharedMutation(ctx, id, "abort", sharedDigest{Digest: digest}, &response); err != nil {
		return err
	}
	if !response.OK {
		return errors.New("successor did not cancel prepared shared ingress ownership")
	}
	return nil
}

func sharedError(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	if errors.Is(err, shared.ErrNotOwner) || errors.Is(err, shared.ErrNotMember) {
		code = http.StatusForbidden
	}
	if errors.Is(err, shared.ErrUnavailable) {
		code = http.StatusServiceUnavailable
	}
	if errors.Is(err, shared.ErrConflict) || errors.Is(err, shared.ErrTransferPending) {
		code = http.StatusConflict
	}
	http.Error(w, err.Error(), code)
}

func (m *Manager) handleShared(w http.ResponseWriter, r *http.Request, caller string) {
	if r.URL.RawPath != "" || r.URL.RawQuery != "" {
		http.Error(w, "invalid shared ingress path", http.StatusBadRequest)
		return
	}
	m.mu.Lock()
	authority := m.sharedAuthority
	m.mu.Unlock()
	if authority == nil {
		http.Error(w, "shared ingress unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == sharedPrefix {
		record, err := authority.Export(caller)
		if err != nil {
			sharedError(w, err)
			return
		}
		if _, err := m.trustedPeer(caller); err != nil {
			http.Error(w, "computer is not paired", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(record)
		return
	}
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	action := strings.TrimPrefix(r.URL.Path, sharedPrefix+"/")
	if action == "validate-policy" {
		var policy access.Document
		if err := readSnapshotJSON(r.Body, &policy); err != nil {
			sharedError(w, err)
			return
		}
		if err := policy.Validate(); err != nil {
			sharedError(w, err)
			return
		}
		m.mu.Lock()
		source := m.workspaceSource
		m.mu.Unlock()
		validator, ok := source.(sharedPolicyValidator)
		if !ok {
			http.Error(w, "routing policy validation unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := validator.ValidateSharedPublicPolicy(policy); err != nil {
			sharedError(w, err)
			return
		}
		if _, err := m.trustedPeer(caller); err != nil {
			http.Error(w, "computer is not paired", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sharedResult{OK: true})
		return
	}
	var prepare sharedPrepare
	var digest sharedDigest
	var committed shared.Record
	knownActivation := false
	switch action {
	case "prepare":
		if err := readSnapshotJSON(r.Body, &prepare); err != nil {
			sharedError(w, err)
			return
		}
	case "activate", "abort":
		if err := readSnapshotJSON(r.Body, &digest); err != nil || !shared.ValidFingerprint(digest.Digest) {
			http.Error(w, "invalid handoff digest", http.StatusBadRequest)
			return
		}
	default:
		http.NotFound(w, r)
		return
	}
	if action == "activate" {
		knownActivation = authority.ActivationKnown(caller, digest.Digest)
		if !knownActivation {
			var err error
			committed, err = m.FetchShared(r.Context(), caller)
			if err != nil {
				sharedError(w, err)
				return
			}
			if committed.Digest != digest.Digest {
				sharedError(w, shared.ErrConflict)
				return
			}
		}
	}
	// Serialize the trust check with persistence. Revocation cannot return while
	// a now-untrusted peer commits an ownership mutation in the background.
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.state.Peers[caller]; !ok || m.server == nil || m.sharedAuthority != authority {
		http.Error(w, "computer is not paired", http.StatusForbidden)
		return
	}
	var result any
	var err error
	switch action {
	case "prepare":
		var ack string
		ack, err = authority.Prepare(caller, prepare.Before, prepare.Next)
		result = sharedDigest{Digest: ack}
	case "activate":
		if !knownActivation {
			err = authority.Activate(caller, committed)
		}
		result = sharedResult{OK: err == nil}
	case "abort":
		err = authority.Abort(caller, digest.Digest)
		result = sharedResult{OK: err == nil}
	}
	if err != nil {
		sharedError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

var _ shared.Peer = (*Manager)(nil)
