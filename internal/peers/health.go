package peers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/thiagojdb/rementor/internal/fleet"
	"github.com/thiagojdb/rementor/internal/validation"
)

// ProbeEndpoint uses the same declared-binding and pinned TLS boundary as
// application traffic. The receiver never probes the owner's port locally.
func (m *Manager) ProbeEndpoint(ctx context.Context, endpoint fleet.EndpointRef, path string) (bool, error) {
	if err := fleet.ValidateEndpointRef(endpoint); err != nil {
		return false, err
	}
	if _, err := validation.NormalizeRoutePath("health path", path, false); err != nil {
		return false, err
	}
	peer, err := m.trustedPeer(endpoint.OwnerFingerprint)
	if err != nil {
		return false, err
	}
	ctx, done, err := m.beginForward(ctx, endpoint.OwnerFingerprint)
	if err != nil {
		return false, err
	}
	defer done()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, peer.URL+forwardPrefix+path, nil)
	if err != nil {
		return false, err
	}
	request.Host = "localhost"
	raw, _ := json.Marshal(endpoint)
	request.Header.Set(endpointHeader, base64.RawURLEncoding.EncodeToString(raw))
	client := m.client(endpoint.OwnerFingerprint)
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	current, err := m.trustedPeer(endpoint.OwnerFingerprint)
	if err != nil || current.URL != peer.URL {
		return false, errors.New("peer trust changed during endpoint health check")
	}
	if response.Header.Get("X-Rementor-Endpoint-Owner") != endpoint.OwnerFingerprint || response.Header.Get("X-Rementor-Endpoint-App-ID") != endpoint.ApplicationID || response.Header.Get("X-Rementor-Endpoint-Workspace") != endpoint.WorkspaceID || response.Header.Get("X-Rementor-Endpoint-Version") != strconv.FormatUint(endpoint.Version, 10) || response.Header.Get("X-Rementor-Endpoint-Digest") != endpoint.Digest {
		return false, errors.New("endpoint health response has invalid provenance")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false, fmt.Errorf("selected endpoint health check failed (HTTP %d)", response.StatusCode)
	}
	return true, nil
}
