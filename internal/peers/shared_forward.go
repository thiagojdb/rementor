package peers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/thiagojdb/rementor/internal/shared"
)

const sharedForwardPrefix = "/v1/shared-forward"
const sharedRequestHeader = "X-Rementor-Shared-Request"

// SharedIngress is supplied by the public data plane. It exposes no local
// management operations and independently checks the shared serving role.
type SharedIngress interface {
	ServeSharedHTTP(http.ResponseWriter, *http.Request, string, shared.Role, shared.Request)
}

func (m *Manager) SetSharedIngress(handler SharedIngress) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sharedIngress = handler
	m.cancelForwardsLocked("")
}

func sharedRolePath(role shared.Role) string {
	switch role {
	case shared.Selector:
		return sharedForwardPrefix + "/select"
	case shared.Routing:
		return sharedForwardPrefix + "/route"
	}
	return ""
}

// ForwardShared uses a pinned paired destination selected by the captured
// authority, never a browser URL. Trust and authority contexts both encompass
// streamed responses and upgraded connections.
func (m *Manager) ForwardShared(w http.ResponseWriter, r *http.Request, destination string, role shared.Role, record shared.Record, metadata shared.Request) {
	prefix := sharedRolePath(role)
	if prefix == "" || record.Validate() != nil || metadata.Validate() != nil || metadata.GroupID != record.GroupID || metadata.Digest != record.Digest || (role == shared.Selector && destination != record.Owner) || (role == shared.Routing && destination != record.RoutingOwner) {
		http.Error(w, "invalid shared ingress destination", http.StatusBadRequest)
		return
	}
	peer, err := m.trustedPeer(destination)
	if err != nil {
		http.Error(w, "shared ingress computer is not paired", http.StatusForbidden)
		return
	}
	ctx, done, err := m.beginForward(r.Context(), destination)
	if err != nil {
		http.Error(w, "shared ingress computer is not paired", http.StatusForbidden)
		return
	}
	defer done()
	address, _ := url.Parse(peer.URL)
	raw, _ := json.Marshal(metadata)
	client := m.client(destination)
	defer client.CloseIdleConnections()
	proxy := &httputil.ReverseProxy{
		Transport: client.Transport,
		Rewrite: func(request *httputil.ProxyRequest) {
			request.Out.URL.Scheme = address.Scheme
			request.Out.URL.Host = address.Host
			request.Out.URL.User = nil
			request.Out.URL.Opaque = ""
			request.Out.URL.Path = prefix + request.In.URL.Path
			if request.In.URL.RawPath != "" {
				request.Out.URL.RawPath = prefix + request.In.URL.RawPath
			}
			request.Out.Host = request.In.Host
			stripPeerHeaders(request.Out.Header)
			for name := range request.Out.Header {
				lower := strings.ToLower(name)
				if strings.HasPrefix(lower, "tailscale-") || strings.HasPrefix(lower, "x-forwarded-") {
					delete(request.Out.Header, name)
				}
			}
			request.Out.Header.Set(sharedRequestHeader, base64.RawURLEncoding.EncodeToString(raw))
		},
		ModifyResponse: func(response *http.Response) error {
			if _, err := m.trustedPeer(destination); err != nil {
				return err
			}
			if err := shared.CheckProof(response.Header, record); err != nil {
				return err
			}
			return ctx.Err()
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			if errors.Is(err, shared.ErrConflict) {
				http.Error(w, "shared ingress changed; retry the request", http.StatusConflict)
				return
			}
			http.Error(w, "selected shared ingress computer unavailable", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r.WithContext(ctx))
}

func (m *Manager) handleSharedForward(w http.ResponseWriter, r *http.Request, caller string) {
	role, prefix := shared.Selector, sharedRolePath(shared.Selector)
	if strings.HasPrefix(r.URL.Path, sharedRolePath(shared.Routing)+"/") {
		role, prefix = shared.Routing, sharedRolePath(shared.Routing)
	}
	if !strings.HasPrefix(r.URL.Path, prefix+"/") {
		http.NotFound(w, r)
		return
	}
	values := r.Header.Values(sharedRequestHeader)
	if len(values) != 1 || len(values[0]) > 4096 {
		http.Error(w, "invalid shared ingress request", http.StatusBadRequest)
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(values[0])
	var metadata shared.Request
	if err != nil || readSnapshotJSON(strings.NewReader(string(raw)), &metadata) != nil || metadata.Validate() != nil {
		http.Error(w, "invalid shared ingress request", http.StatusBadRequest)
		return
	}
	ctx, done, err := m.beginForward(r.Context(), caller)
	if err != nil {
		http.Error(w, "computer is not paired", http.StatusForbidden)
		return
	}
	defer done()
	m.mu.Lock()
	handler := m.sharedIngress
	m.mu.Unlock()
	if handler == nil {
		http.Error(w, "shared ingress unavailable", http.StatusServiceUnavailable)
		return
	}
	request := r.Clone(ctx)
	request.URL.Path = strings.TrimPrefix(request.URL.Path, prefix)
	if request.URL.RawPath != "" {
		if !strings.HasPrefix(request.URL.RawPath, prefix+"/") {
			http.Error(w, "ambiguous shared ingress path", http.StatusBadRequest)
			return
		}
		request.URL.RawPath = strings.TrimPrefix(request.URL.RawPath, prefix)
	}
	stripPeerHeaders(request.Header)
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Time{})
	_ = controller.SetWriteDeadline(time.Time{})
	handler.ServeSharedHTTP(w, request, caller, role, metadata)
}
