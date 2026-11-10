package ingress

import (
	"net/http"
	"strings"

	"github.com/thiagojdb/rementor/internal/access"
	"github.com/thiagojdb/rementor/internal/publication"
)

// ServePublishedHTTP is reached either from the direct TLS listener or the
// pinned peer-only listener. Caller-provided identity headers are ignored.
func (handler *Handler) ServePublishedHTTP(w http.ResponseWriter, r *http.Request, metadata publication.Request) {
	origin, manifestOnly, ok := validateRequest(w, r)
	if !ok {
		return
	}
	identity, err := handler.identity.Resolve(r.Context(), metadata.SourceAddress)
	if err != nil {
		http.Error(w, "Tailscale device identity could not be verified", http.StatusForbidden)
		return
	}
	doc, err := metadata.Document(identity)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// The existing guarded data plane revalidates declared hosts and selected
	// routes atomically; a missing or stale instance never selects another one.
	routing, ok := handler.source.(sharedSource)
	if !ok {
		http.Error(w, "routing publication unavailable", 503)
		return
	}
	ctx, selection, release, err := routing.BeginSharedPublicRequest(r.Context(), access.Configuration{Document: doc}, identity, origin, r.URL.Path, manifestOnly)
	if err != nil {
		publicError(w, err)
		return
	}
	defer release()
	if source, ok := handler.source.(interface {
		PublicationOriginAliases(string) map[string]string
	}); ok {
		for id, host := range source.PublicationOriginAliases(metadata.WorkspaceID) {
			for _, link := range metadata.Links {
				if link.LocalHost == host {
					selection.Manifest.Origins[id] = link.URL
					selection.Manifest.RoutingHosts[id] = host
					selection.Manifest.WebSocketOrigins[id] = "wss" + strings.TrimPrefix(link.URL, "https")
				}
			}
		}
	}
	selection.Manifest.SelectionSource = "hostname"
	handler.serveSelection(w, r, metadata.SourceAddress, ctx, selection, manifestOnly, nil)
}
