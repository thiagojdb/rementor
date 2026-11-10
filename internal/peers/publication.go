package peers

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/thiagojdb/rementor/internal/publication"
)

const publicationForwardPrefix = "/v1/publication-forward"
const publicationRequestHeader = "X-Rementor-Publication-Request"

type PublicationIngress interface {
	ServePublishedHTTP(http.ResponseWriter, *http.Request, publication.Request)
}

func (m *Manager) SetPublicationIngress(handler PublicationIngress) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.publicationIngress = handler
	m.cancelForwardsLocked("")
}
func (m *Manager) ForwardPublication(w http.ResponseWriter, r *http.Request, metadata publication.Request) {
	peer, err := m.trustedPeer(metadata.Computer)
	if err != nil {
		http.Error(w, "Selected publishing computer is not paired", 403)
		return
	}
	ctx, done, err := m.beginForward(r.Context(), metadata.Computer)
	if err != nil {
		http.Error(w, "Selected publishing computer unavailable", 503)
		return
	}
	defer done()
	raw, err := json.Marshal(metadata)
	if err != nil || len(raw) > 22500 {
		http.Error(w, "Publication request is too large", 400)
		return
	}
	address, _ := url.Parse(peer.URL)
	client := m.client(metadata.Computer)
	defer client.CloseIdleConnections()
	proxy := &httputil.ReverseProxy{Transport: client.Transport, Rewrite: func(p *httputil.ProxyRequest) {
		p.Out.URL.Scheme = address.Scheme
		p.Out.URL.Host = address.Host
		p.Out.URL.User = nil
		p.Out.URL.Opaque = ""
		p.Out.Host = p.In.Host
		p.Out.URL.Path = publicationForwardPrefix + p.In.URL.Path
		if p.In.URL.RawPath != "" {
			p.Out.URL.RawPath = publicationForwardPrefix + p.In.URL.RawPath
		}
		for name := range p.Out.Header {
			lower := strings.ToLower(name)
			if strings.HasPrefix(lower, "x-rementor-") || strings.HasPrefix(lower, "x-forwarded-") || strings.HasPrefix(lower, "tailscale-") {
				delete(p.Out.Header, name)
			}
		}
		p.Out.Header.Set(publicationRequestHeader, base64.RawURLEncoding.EncodeToString(raw))
	}, ModifyResponse: func(response *http.Response) error {
		if _, err := m.trustedPeer(metadata.Computer); err != nil {
			return err
		}
		return ctx.Err()
	}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "Selected publishing computer unavailable", 502)
	}}
	proxy.ServeHTTP(w, r.WithContext(ctx))
}
func (m *Manager) handlePublicationForward(w http.ResponseWriter, r *http.Request, caller string) {
	values := r.Header.Values(publicationRequestHeader)
	if len(values) != 1 || len(values[0]) > 30000 {
		http.Error(w, "Invalid publication request", 400)
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(values[0])
	var metadata publication.Request
	if err != nil || readSnapshotJSON(strings.NewReader(string(raw)), &metadata) != nil || metadata.Computer != m.State().Fingerprint {
		http.Error(w, "Invalid publication owner", 400)
		return
	}
	ctx, done, err := m.beginForward(r.Context(), caller)
	if err != nil {
		http.Error(w, "Computer is not paired", 403)
		return
	}
	defer done()
	m.mu.Lock()
	handler := m.publicationIngress
	m.mu.Unlock()
	if handler == nil {
		http.Error(w, "Publishing data plane unavailable; update this computer", 503)
		return
	}
	request := r.Clone(ctx)
	request.URL.Path = strings.TrimPrefix(request.URL.Path, publicationForwardPrefix)
	if request.URL.RawPath != "" {
		if !strings.HasPrefix(request.URL.RawPath, publicationForwardPrefix+"/") {
			http.Error(w, "Ambiguous publication path", 400)
			return
		}
		request.URL.RawPath = strings.TrimPrefix(request.URL.RawPath, publicationForwardPrefix)
	}
	stripPeerHeaders(request.Header)
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Time{})
	_ = controller.SetWriteDeadline(time.Time{})
	handler.ServePublishedHTTP(w, request, metadata)
}
