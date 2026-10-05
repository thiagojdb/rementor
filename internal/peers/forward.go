package peers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/thiagojdb/rementor/internal/fleet"
)

const forwardPrefix = "/v1/forward"
const endpointHeader = "X-Rementor-Peer-Endpoint"

type activeForward struct {
	peer   string
	cancel context.CancelFunc
}

// SetManagementPort prevents even a declared application binding from exposing
// the unauthenticated local management API through the peer data plane.
func (m *Manager) SetManagementPort(port int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.managementPort = port
	m.cancelForwardsLocked("")
}

// ProtectLocalPorts prevents declared bindings from reaching this instance's
// gateway or another local listener that exposes its management API.
func (m *Manager) ProtectLocalPorts(ports ...int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.protectedPorts = make(map[int]bool, len(ports))
	for _, port := range ports {
		m.protectedPorts[port] = true
	}
	m.cancelForwardsLocked("")
}

// Registration and the trust check are atomic with revocation. Context
// cancellation closes streaming bodies and upgraded connections as well as
// ordinary requests; http.Server.Close alone does not close hijacked sockets.
func (m *Manager) beginForward(ctx context.Context, peer string) (context.Context, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.state.Peers[peer]; !ok || m.server == nil {
		return nil, nil, errors.New("peer connection is disabled or computer is not paired")
	}
	ctx, cancel := context.WithCancel(ctx)
	if m.forwards == nil {
		m.forwards = make(map[uint64]activeForward)
	}
	m.forwardSequence++
	sequence := m.forwardSequence
	m.forwards[sequence] = activeForward{peer: peer, cancel: cancel}
	return ctx, func() {
		cancel()
		m.mu.Lock()
		delete(m.forwards, sequence)
		m.mu.Unlock()
	}, nil
}

func (m *Manager) cancelForwardsLocked(peer string) {
	for _, request := range m.forwards {
		if peer == "" || request.peer == peer {
			request.cancel()
		}
	}
}

func stripPeerHeaders(headers http.Header) {
	for name := range headers {
		if strings.HasPrefix(strings.ToLower(name), "x-rementor-") {
			delete(headers, name)
		}
	}
}

func endpointProof(headers http.Header, endpoint fleet.EndpointRef) {
	stripPeerHeaders(headers)
	headers.Set("X-Rementor-Endpoint-Owner", endpoint.OwnerFingerprint)
	headers.Set("X-Rementor-Endpoint-Workspace", endpoint.WorkspaceID)
	headers.Set("X-Rementor-Endpoint-App-ID", endpoint.ApplicationID)
	headers.Set("X-Rementor-Endpoint-Version", strconv.FormatUint(endpoint.Version, 10))
	headers.Set("X-Rementor-Endpoint-Digest", endpoint.Digest)
}

func forwardError(w http.ResponseWriter, _ *http.Request, _ error) {
	http.Error(w, "selected application endpoint unavailable", http.StatusBadGateway)
}

// Forward relays a request to its declared owning peer, including upgrades.
// Callers supply the selected endpoint reference, never a caller-provided URL.
// The local gateway remains responsible for public path rewriting and proofs.
func (m *Manager) Forward(w http.ResponseWriter, r *http.Request, endpoint fleet.EndpointRef) {
	if fleet.ValidateEndpointRef(endpoint) != nil || !strings.HasPrefix(r.URL.Path, "/") {
		http.Error(w, "invalid application endpoint", http.StatusBadRequest)
		return
	}
	peer, err := m.trustedPeer(endpoint.OwnerFingerprint)
	if err != nil {
		http.Error(w, "selected endpoint owner is not paired", http.StatusForbidden)
		return
	}
	ctx, done, err := m.beginForward(r.Context(), endpoint.OwnerFingerprint)
	if err != nil {
		http.Error(w, "selected endpoint owner is not paired", http.StatusForbidden)
		return
	}
	defer done()
	address, _ := url.Parse(peer.URL)
	raw, _ := json.Marshal(endpoint)
	client := m.client(endpoint.OwnerFingerprint)
	defer client.CloseIdleConnections()
	proxy := &httputil.ReverseProxy{
		// Use the pinned transport without the management client's total timeout.
		Transport: client.Transport,
		Rewrite: func(request *httputil.ProxyRequest) {
			request.Out.URL.Scheme = address.Scheme
			request.Out.URL.Host = address.Host
			request.Out.URL.User = nil
			request.Out.URL.Opaque = ""
			request.Out.URL.Path = forwardPrefix + request.In.URL.Path
			if request.In.URL.RawPath != "" {
				request.Out.URL.RawPath = forwardPrefix + request.In.URL.RawPath
			}
			request.Out.Host = request.In.Host
			stripPeerHeaders(request.Out.Header)
			request.Out.Header.Set(endpointHeader, base64.RawURLEncoding.EncodeToString(raw))
			request.SetXForwarded()
			if request.In.Context().Value(gatewayHeadersKey{}) == true {
				if proto := request.In.Header.Get("X-Forwarded-Proto"); proto == "http" || proto == "https" {
					request.Out.Header.Set("X-Forwarded-Proto", proto)
				}
				if host := request.In.Header.Get("X-Forwarded-Host"); host != "" {
					request.Out.Header.Set("X-Forwarded-Host", host)
				}
			}
		},
		ModifyResponse: func(response *http.Response) error {
			if _, err := m.trustedPeer(endpoint.OwnerFingerprint); err != nil {
				return err
			}
			endpointProof(response.Header, endpoint)
			return nil
		},
		ErrorHandler: forwardError,
	}
	proxy.ServeHTTP(w, r.WithContext(ctx))
}

func (m *Manager) handleForward(w http.ResponseWriter, r *http.Request, caller, owner string, source fleet.WorkspaceSource) {
	values := r.Header.Values(endpointHeader)
	if len(values) != 1 || len(values[0]) > 4096 {
		http.Error(w, "invalid application endpoint", http.StatusBadRequest)
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(values[0])
	var endpoint fleet.EndpointRef
	if err != nil || readSnapshotJSON(strings.NewReader(string(raw)), &endpoint) != nil || fleet.ValidateEndpointRef(endpoint) != nil || endpoint.OwnerFingerprint != owner {
		http.Error(w, "invalid application endpoint", http.StatusBadRequest)
		return
	}
	if source == nil {
		http.Error(w, "workspace source unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx, done, err := m.beginForward(r.Context(), caller)
	if err != nil {
		http.Error(w, "computer is not paired", http.StatusForbidden)
		return
	}
	defer done()
	intent, version, err := source.ExportPeerWorkspace(endpoint.WorkspaceID)
	if errors.Is(err, fleet.ErrWorkspaceNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "workspace snapshot unavailable", http.StatusServiceUnavailable)
		return
	}
	snapshot, err := fleet.NewSnapshot(owner, version, intent)
	if err != nil {
		http.Error(w, "workspace snapshot unavailable", http.StatusServiceUnavailable)
		return
	}
	if snapshot.Digest != endpoint.Digest || version != endpoint.Version {
		http.Error(w, "selected endpoint snapshot is stale; synchronize the source", http.StatusConflict)
		return
	}
	port := 0
	for _, app := range intent.Applications {
		// Registration declares the owned endpoint; the receiving instance's
		// explicit selection decides whether to use it. An owner choosing remote
		// does not erase that registration or force followers to the same target.
		if app.ID == endpoint.ApplicationID && app.Port > 0 {
			port = app.Port
			break
		}
	}
	if port == 0 {
		http.Error(w, "selected local application endpoint is not available", http.StatusNotFound)
		return
	}
	m.mu.Lock()
	_, listenerPort, _ := net.SplitHostPort(m.state.Listen)
	protected := port == m.managementPort || strconv.Itoa(port) == listenerPort || m.protectedPorts[port]
	m.mu.Unlock()
	if protected {
		http.Error(w, "control plane cannot be forwarded", http.StatusForbidden)
		return
	}
	if _, err := m.trustedPeer(caller); err != nil || ctx.Err() != nil {
		http.Error(w, "computer is not paired", http.StatusForbidden)
		return
	}
	// The peer control listener has bounded management deadlines. Application
	// streaming and uploads instead live until cancellation or trust revocation.
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Time{})
	_ = controller.SetWriteDeadline(time.Time{})
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, ResponseHeaderTimeout: 30 * time.Second}
	defer transport.CloseIdleConnections()
	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(request *httputil.ProxyRequest) {
			request.Out.URL.Scheme = "http"
			request.Out.URL.Host = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
			request.Out.URL.User = nil
			request.Out.URL.Opaque = ""
			request.Out.URL.Path = strings.TrimPrefix(request.In.URL.Path, forwardPrefix)
			if request.In.URL.RawPath != "" {
				request.Out.URL.RawPath = strings.TrimPrefix(request.In.URL.RawPath, forwardPrefix)
			}
			request.Out.Host = request.In.Host
			stripPeerHeaders(request.Out.Header)
			// These are set by the authenticated sending gateway, not by a
			// browser's arbitrary forwarded headers.
			for _, header := range []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
				request.Out.Header[header] = append([]string(nil), request.In.Header.Values(header)...)
			}
		},
		ModifyResponse: func(response *http.Response) error {
			if _, err := m.trustedPeer(caller); err != nil {
				return err
			}
			endpointProof(response.Header, endpoint)
			return nil
		},
		ErrorHandler: forwardError,
	}
	endpointProof(w.Header(), endpoint)
	proxy.ServeHTTP(w, r.WithContext(ctx))
}

// IsProtectedLocalPort lets authenticated public ingress apply the same
// management separation as declared-owner peer forwarding.
func (m *Manager) IsProtectedLocalPort(port int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, listenerPort, _ := net.SplitHostPort(m.state.Listen)
	return port == m.managementPort || strconv.Itoa(port) == listenerPort || m.protectedPorts[port]
}
