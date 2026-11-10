// Package ingress serves authenticated application traffic and runtime origins.
// It does not register management RPC, enrollment or arbitrary proxy targets.
package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/thiagojdb/rementor/internal/access"
	"github.com/thiagojdb/rementor/internal/peers"
	"github.com/thiagojdb/rementor/internal/services"
	"github.com/thiagojdb/rementor/internal/shared"
)

const ManifestPath = "/__rementor/origins"
const RuntimePath = "/__rementor/origins.js"
const GuardHeader = "X-Rementor-Ingress-Guard"
const PublicHostHeader = "X-Rementor-Ingress-Host"
const PublicProtoHeader = "X-Rementor-Ingress-Proto"

var ErrProjectionStale = errors.New("selected routing projection changed; retry the request")

type Source interface {
	BeginPublicRequest(context.Context, access.Identity, string, string, bool) (context.Context, services.AccessRequest, func(), error)
	PublicAccessConfiguration() access.Configuration
}
type Handler struct {
	source        Source
	identity      IdentityResolver
	upstream      *url.URL
	protectedPort func(int) bool
	transport     *http.Transport
	authority     *shared.Authority
	peers         *peers.Manager
}

func New(source Source, identity IdentityResolver, nginxAddress string, protectedPort func(int) bool) (*Handler, error) {
	parsed, err := url.Parse(nginxAddress)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Port() == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || !net.ParseIP(parsed.Hostname()).IsLoopback() {
		return nil, errors.New("nginx application ingress must be an explicit loopback HTTP address")
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return nil, errors.New("invalid nginx ingress port")
	}
	if source == nil || identity == nil {
		return nil, errors.New("public ingress requires routing and identity sources")
	}
	return &Handler{source: source, identity: identity, upstream: parsed, protectedPort: protectedPort, transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, ResponseHeaderTimeout: 30 * time.Second}}, nil
}
func (handler *Handler) Close() { handler.transport.CloseIdleConnections() }
func (handler *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	address, err := SourceAddress(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	origin, manifestOnly, ok := validateRequest(w, r)
	if !ok {
		return
	}
	identity, err := handler.identity.Resolve(r.Context(), address)
	if err != nil {
		http.Error(w, "device identity could not be verified", http.StatusForbidden)
		return
	}
	if handler.serveSharedGateway(w, r, address, origin) {
		return
	}
	ctx, selection, done, err := handler.source.BeginPublicRequest(r.Context(), identity, origin, r.URL.Path, manifestOnly)
	if err != nil {
		publicError(w, err)
		return
	}
	defer done()
	handler.serveSelection(w, r, address, ctx, selection, manifestOnly, nil)
}

func validateRequest(w http.ResponseWriter, r *http.Request) (string, bool, bool) {
	origin, err := access.CanonicalOrigin("https://" + r.Host)
	if err != nil {
		http.Error(w, "invalid public origin", http.StatusBadRequest)
		return "", false, false
	}
	// Requests with reserved paths never reach an upstream application or RPC.
	manifestOnly := r.URL.Path == ManifestPath || r.URL.Path == RuntimePath
	if (strings.HasPrefix(r.URL.Path, "/__rementor/") && !manifestOnly) || strings.HasPrefix(r.URL.Path, "/_rementor/") {
		http.NotFound(w, r)
		return "", false, false
	}
	if manifestOnly && r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return "", false, false
	}
	if r.URL.RawPath != "" {
		decoded, err := url.PathUnescape(r.URL.RawPath)
		if err != nil || decoded != r.URL.Path {
			http.Error(w, "ambiguous request path", http.StatusBadRequest)
			return "", false, false
		}
	}
	// Prevent path normalization from selecting a different published route.
	for _, segment := range strings.Split(r.URL.Path, "/") {
		if segment == "." || segment == ".." {
			http.Error(w, "ambiguous request path", http.StatusBadRequest)
			return "", false, false
		}
	}
	if strings.Contains(r.URL.Path, "//") || strings.ContainsAny(r.URL.Path, "\\\x00") {
		http.Error(w, "ambiguous request path", http.StatusBadRequest)
		return "", false, false
	}
	return origin, manifestOnly, true
}

func publicError(w http.ResponseWriter, err error) {
	status := http.StatusServiceUnavailable
	if errors.Is(err, access.ErrNotAuthorized) || errors.Is(err, shared.ErrNotMember) || errors.Is(err, shared.ErrNotOwner) {
		status = http.StatusForbidden
	} else if errors.Is(err, access.ErrNotPublished) || strings.Contains(err.Error(), "route not found") {
		status = http.StatusNotFound
	} else if errors.Is(err, shared.ErrConflict) || errors.Is(err, shared.ErrTransferPending) {
		status = http.StatusConflict
	}
	http.Error(w, err.Error(), status)
}

func (handler *Handler) serveSelection(w http.ResponseWriter, r *http.Request, address string, ctx context.Context, selection services.AccessRequest, manifestOnly bool, record *shared.Record) {
	if record != nil {
		shared.SetProof(w.Header(), *record)
	}
	w.Header().Set("X-Rementor-Access-Revision", strconv.FormatUint(selection.Manifest.Revision, 10))
	w.Header().Set("X-Rementor-Access-Selection", selection.Manifest.SelectionSource)
	if err := cors(w.Header(), r, selection.Manifest.Origins); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS")
		// Reflect requested header names only after origin and device authorization.
		w.Header().Set("Access-Control-Allow-Headers", r.Header.Get("Access-Control-Request-Headers"))
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if manifestOnly {
		if r.URL.Path == RuntimePath {
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			if r.Method != http.MethodHead {
				writeRuntime(w, selection.Manifest)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodHead {
			_ = json.NewEncoder(w).Encode(selection.Manifest)
		}
		return
	}
	check, cancel := context.WithTimeout(ctx, 3*time.Second)
	err := services.AccessTargetAllowed(check, selection.Route, handler.protectedPort)
	cancel()
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	proxy := &httputil.ReverseProxy{
		Transport: handler.transport,
		Rewrite: func(request *httputil.ProxyRequest) {
			request.Out.URL.Scheme = handler.upstream.Scheme
			request.Out.URL.Host = handler.upstream.Host
			request.Out.URL.User = nil
			request.Out.URL.Opaque = ""
			request.Out.Host = selection.LocalHost
			for name := range request.Out.Header {
				lower := strings.ToLower(name)
				if strings.HasPrefix(lower, "x-rementor-") || strings.HasPrefix(lower, "tailscale-") || strings.HasPrefix(lower, "x-forwarded-") {
					delete(request.Out.Header, name)
				}
			}
			request.SetXForwarded()
			request.Out.Header.Set(GuardHeader, selection.Guard)
			request.Out.Header.Set(PublicHostHeader, r.Host)
			request.Out.Header.Set(PublicProtoHeader, "https")
			request.Out.Header.Set("X-Forwarded-For", address)
			request.Out.Header.Set("X-Forwarded-Proto", "https")
		},
		ModifyResponse: func(response *http.Response) error {
			// nginx enforces this before contacting a target. Verify the response too
			// so an older/unconfigured proxy fails instead of returning an unproved body.
			proof := response.Header.Get("X-Rementor-Workspace") + ":" + response.Header.Get("X-Rementor-App-ID") + ":" + response.Header.Get("X-Rementor-Route-Version")
			if proof != selection.Guard {
				return ErrProjectionStale
			}
			for name := range response.Header {
				lower := strings.ToLower(name)
				if strings.HasPrefix(lower, "access-control-") {
					delete(response.Header, name)
				}
			}
			for name := range response.Header {
				if strings.HasPrefix(strings.ToLower(name), "x-rementor-shared-") {
					delete(response.Header, name)
				}
			}
			// The writer already carries the authorized CORS/policy headers.
			// ReverseProxy adds upstream values rather than replacing them; adding
			// these again would produce duplicate ACAO and break credentialed CORS.
			response.Header.Del("X-Rementor-Access-Revision")
			response.Header.Del("X-Rementor-Access-Selection")
			response.Header.Del("Cache-Control")
			return ctx.Err()
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			if errors.Is(err, ErrProjectionStale) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			http.Error(w, "selected public application route unavailable", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r.WithContext(ctx))
}
func cors(header http.Header, request *http.Request, origins map[string]string) error {
	supplied := request.Header.Values("Origin")
	if len(supplied) == 0 {
		return nil
	}
	if len(supplied) != 1 {
		return errors.New("ambiguous browser origin")
	}
	normalized, err := access.CanonicalOrigin(supplied[0])
	if err != nil {
		return errors.New("browser origin is not allowed")
	}
	allowed := false
	for _, origin := range origins {
		if origin == normalized {
			allowed = true
			break
		}
	}
	if !allowed {
		return errors.New("browser origin is not published")
	}
	header.Set("Access-Control-Allow-Origin", supplied[0])
	header.Set("Access-Control-Allow-Credentials", "true")
	header.Add("Vary", "Origin")
	header.Set("Access-Control-Expose-Headers", "X-Rementor-Workspace, X-Rementor-Environment, X-Rementor-Session-ID, X-Rementor-App-ID, X-Rementor-Effective-Mode, X-Rementor-Route-Version, X-Rementor-Endpoint-Owner, X-Rementor-Endpoint-Version, X-Rementor-Access-Revision, X-Rementor-Access-Selection, X-Rementor-Shared-Group, X-Rementor-Shared-Digest, X-Rementor-Shared-Version, X-Rementor-Shared-Epoch, X-Rementor-Shared-Owner, X-Rementor-Shared-Routing-Owner")
	return nil
}
