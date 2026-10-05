package ingress

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/thiagojdb/rementor/internal/access"
	"github.com/thiagojdb/rementor/internal/peers"
	"github.com/thiagojdb/rementor/internal/services"
	"github.com/thiagojdb/rementor/internal/shared"
)

type sharedSource interface {
	BeginSharedPublicRequest(context.Context, access.Configuration, access.Identity, string, string, bool) (context.Context, services.AccessRequest, func(), error)
}

func (handler *Handler) ConfigureShared(authority *shared.Authority, manager *peers.Manager) error {
	if authority == nil || manager == nil {
		return errors.New("shared ingress requires an authority and paired transport")
	}
	if _, ok := handler.source.(sharedSource); !ok {
		return errors.New("routing source cannot resolve shared ingress policies")
	}
	handler.authority, handler.peers = authority, manager
	manager.SetSharedIngress(handler)
	return nil
}

func published(record shared.Record, origin string) bool {
	for _, entry := range record.Policy.Origins {
		canonical, err := access.CanonicalOrigin(entry.URL)
		if err == nil && canonical == origin {
			return true
		}
	}
	return false
}

// Shared origins cannot fall through to a gateway's private publication, even
// when synchronization fails or removes that origin from the owner's policy.
func (handler *Handler) serveSharedGateway(w http.ResponseWriter, r *http.Request, address, origin string) bool {
	if handler.authority == nil {
		return false
	}
	state := handler.authority.State()
	if state.Record == nil || !published(*state.Record, origin) {
		return false
	}
	check, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	record, err := handler.authority.Sync(check, handler.peers)
	cancel()
	if err != nil {
		publicError(w, err)
		return true
	}
	if !published(record, origin) {
		publicError(w, access.ErrNotPublished)
		return true
	}
	self := handler.peers.State().Fingerprint
	ctx, record, done, err := handler.authority.Begin(r.Context(), record.Digest, self, shared.Gateway)
	if err != nil {
		publicError(w, err)
		return true
	}
	defer done()
	metadata := shared.Request{GroupID: record.GroupID, Digest: record.Digest, SourceAddress: address, Origin: origin}
	if self == record.Owner {
		handler.ServeSharedHTTP(w, r.WithContext(ctx), self, shared.Selector, metadata)
	} else {
		handler.peers.ForwardShared(w, r.WithContext(ctx), record.Owner, shared.Selector, record, metadata)
	}
	return true
}

// ServeSharedHTTP runs only behind a pinned paired listener or a captured local
// gateway scope. Every hop re-verifies the source using its own tailscaled.
func (handler *Handler) ServeSharedHTTP(w http.ResponseWriter, r *http.Request, caller string, role shared.Role, metadata shared.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if handler.authority == nil || handler.peers == nil || metadata.Validate() != nil {
		http.Error(w, "shared ingress unavailable", http.StatusServiceUnavailable)
		return
	}
	origin, manifestOnly, ok := validateRequest(w, r)
	if !ok {
		return
	}
	if origin != metadata.Origin || (role != shared.Selector && role != shared.Routing) {
		http.Error(w, "invalid shared ingress origin or role", http.StatusBadRequest)
		return
	}
	if role == shared.Routing {
		// A routing replica never uses its cached policy to serve through an
		// unavailable selector. This also retires old scopes after owner updates.
		check, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		_, err := handler.authority.Sync(check, handler.peers)
		cancel()
		if err != nil {
			publicError(w, err)
			return
		}
	}
	ctx, record, done, err := handler.authority.Begin(r.Context(), metadata.Digest, caller, role)
	if err != nil {
		publicError(w, err)
		return
	}
	defer done()
	if record.GroupID != metadata.GroupID || !published(record, origin) {
		publicError(w, shared.ErrConflict)
		return
	}
	identity, err := handler.identity.Resolve(ctx, metadata.SourceAddress)
	if err != nil {
		http.Error(w, "device identity could not be verified", http.StatusForbidden)
		return
	}
	if _, err := record.Policy.Select(identity); err != nil {
		publicError(w, err)
		return
	}
	if role == shared.Selector {
		if record.RoutingOwner == handler.peers.State().Fingerprint {
			handler.ServeSharedHTTP(w, r.WithContext(ctx), record.Owner, shared.Routing, metadata)
		} else {
			handler.peers.ForwardShared(w, r.WithContext(ctx), record.RoutingOwner, shared.Routing, record, metadata)
		}
		return
	}
	configuration := access.Configuration{Revision: record.Version, Document: record.Policy.Clone()}
	request, selection, release, err := handler.source.(sharedSource).BeginSharedPublicRequest(ctx, configuration, identity, origin, r.URL.Path, manifestOnly)
	if err != nil {
		publicError(w, err)
		return
	}
	defer release()
	handler.serveSelection(w, r, metadata.SourceAddress, request, selection, manifestOnly, &record)
}

var _ peers.SharedIngress = (*Handler)(nil)
