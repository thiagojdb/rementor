package services

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/thiagojdb/rementor/internal/access"
	"github.com/thiagojdb/rementor/internal/config"
	"github.com/thiagojdb/rementor/internal/models"
)

type AccessRequest struct {
	Manifest  access.Manifest
	LocalHost string
	Guard     string
	Route     *Route
}

func (r *Registry) PublicAccessConfiguration() access.Configuration {
	r.accessMu.RLock()
	defer r.accessMu.RUnlock()
	result := r.publicAccess
	result.Document = result.Document.Clone()
	if result.Document.SchemaVersion == 0 {
		result.Document = access.Empty()
	}
	return result
}
func validatePublicBindings(doc access.Document, workspaces []*models.Workspace) error {
	if err := doc.Validate(); err != nil {
		return err
	}
	for _, binding := range doc.Bindings {
		ws := findWorkspace(workspaces, binding.WorkspaceID)
		if ws == nil {
			return fmt.Errorf("public origin references missing workspace %q", binding.WorkspaceID)
		}
		if binding.LocalHost == config.Config.RementorDomain || binding.LocalHost == config.DefaultRementorDomain {
			return errors.New("management hostname cannot be published")
		}
		found := false
		for _, route := range NormalizedRoutes(ws) {
			if route.PublicHost == binding.LocalHost {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("host %q is not routed in workspace %q", binding.LocalHost, binding.WorkspaceID)
		}
	}
	return nil
}
func (r *Registry) SavePublicAccessConfiguration(doc access.Document, expected uint64) (access.Configuration, error) {
	r.mutationMu.Lock()
	defer r.mutationMu.Unlock()
	if err := validatePublicBindings(doc, r.workspaceSnapshot()); err != nil {
		return access.Configuration{}, err
	}
	saved, err := config.SavePublicAccessConfiguration(doc, expected)
	if err != nil {
		return access.Configuration{}, err
	}
	r.accessMu.Lock()
	r.publicAccess = saved
	for _, cancel := range r.accessRequests {
		cancel()
	}
	r.accessMu.Unlock()
	return saved, nil
}
func (r *Registry) validatePublicAccessWorkspaces(candidate []*models.Workspace) error {
	return validatePublicBindings(r.PublicAccessConfiguration().Document, candidate)
}

// BeginPublicRequest captures configuration and workspace intent together. The
// guard lets nginx reject a stale projection before contacting an application.
// Every access-policy change cancels existing requests, including WebSockets.
func (r *Registry) BeginPublicRequest(ctx context.Context, identity access.Identity, publicOrigin, path string, manifestOnly bool) (context.Context, AccessRequest, func(), error) {
	r.mutationMu.Lock()
	defer r.mutationMu.Unlock()
	return r.beginPublicRequest(ctx, r.PublicAccessConfiguration(), identity, publicOrigin, path, manifestOnly)
}

// BeginSharedPublicRequest resolves an owner-captured shared policy against this
// routing computer. Its caller holds the authority scope that cancels requests
// when shared membership, policy, or ownership changes.
func (r *Registry) BeginSharedPublicRequest(ctx context.Context, current access.Configuration, identity access.Identity, publicOrigin, path string, manifestOnly bool) (context.Context, AccessRequest, func(), error) {
	r.mutationMu.Lock()
	defer r.mutationMu.Unlock()
	if err := validatePublicBindings(current.Document, r.workspaceSnapshot()); err != nil {
		return nil, AccessRequest{}, nil, err
	}
	return r.beginPublicRequest(ctx, current, identity, publicOrigin, path, manifestOnly)
}

func (r *Registry) beginPublicRequest(ctx context.Context, current access.Configuration, identity access.Identity, publicOrigin, path string, manifestOnly bool) (context.Context, AccessRequest, func(), error) {
	selected, err := current.Document.Select(identity)
	if err != nil {
		return nil, AccessRequest{}, nil, err
	}
	host, err := current.Document.LocalHost(selected.WorkspaceID, publicOrigin)
	if err != nil {
		return nil, AccessRequest{}, nil, err
	}
	ws := r.GetWorkspaceView(selected.WorkspaceID)
	if ws == nil {
		return nil, AccessRequest{}, nil, errors.New("selected workspace is unavailable")
	}
	origins, sockets, err := current.Document.OriginsFor(ws.WorkspaceID)
	if err != nil {
		return nil, AccessRequest{}, nil, err
	}
	routingHosts := make(map[string]string, len(origins))
	for _, binding := range current.Document.Bindings {
		if binding.WorkspaceID == ws.WorkspaceID {
			routingHosts[binding.OriginID] = binding.LocalHost
		}
	}
	result := AccessRequest{LocalHost: host, Manifest: access.Manifest{SchemaVersion: access.SchemaVersion, Revision: current.Revision, WorkspaceID: ws.WorkspaceID, EnvironmentID: ws.EnvironmentID(), SessionID: ws.SessionID(), SelectionSource: selected.Source, RouteVersion: ws.Route.RouteVersion, Origins: origins, RoutingHosts: routingHosts, WebSocketOrigins: sockets}}
	if !manifestOnly {
		resolution, err := resolveRoutes(NormalizedRoutes(ws), ws.WorkspaceID, host, path)
		if err != nil {
			return nil, AccessRequest{}, nil, err
		}
		result.Route = resolution.Route
		appID := resolution.Route.CanonicalAppID
		if appID == "" {
			appID = "unknown"
		}
		version := ws.Route.RouteVersion
		if version == 0 {
			for _, app := range ws.Applications {
				if app.CanonicalAppID() == appID {
					version = app.Route.RouteVersion
					if version == 0 && app.LastOperation != nil {
						version = app.LastOperation.RouteVersion
					}
					break
				}
			}
		}
		result.Guard = ws.WorkspaceID + ":" + appID + ":" + strconv.FormatUint(version, 10)
	}
	request, cancel := context.WithCancel(ctx)
	r.accessMu.Lock()
	if r.accessRequests == nil {
		r.accessRequests = map[uint64]context.CancelFunc{}
	}
	r.accessSequence++
	id := r.accessSequence
	r.accessRequests[id] = cancel
	r.accessMu.Unlock()
	return request, result, func() { cancel(); r.accessMu.Lock(); delete(r.accessRequests, id); r.accessMu.Unlock() }, nil
}

// PublicOriginForHost adapts browser URL resolution without changing the
// workspace's local hosts or its pinned session baseline.
func (r *Registry) PublicOriginForHost(workspace, host string) string {
	document := r.PublicAccessConfiguration().Document
	for _, binding := range document.Bindings {
		if binding.WorkspaceID != workspace || binding.LocalHost != host {
			continue
		}
		for _, origin := range document.Origins {
			if origin.ID == binding.OriginID {
				canonical, _ := access.CanonicalOrigin(origin.URL)
				return canonical
			}
		}
	}
	return PublicOrigin(host)
}

// AccessTargetAllowed prevents a public route from exposing local management,
// even when a paired source supplies an absolute remote URL for that listener.
func AccessTargetAllowed(ctx context.Context, route *Route, protectedPort func(int) bool) error {
	if route == nil || route.Target == "" {
		return errors.New("selected application target is unavailable")
	}
	if strings.HasPrefix(route.Target, "peer://") {
		return nil
	} // The owner enforces its declared endpoint protections.
	target, err := url.Parse(route.Target)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") {
		return errors.New("invalid selected application target")
	}
	hostname := target.Hostname()
	ip := net.ParseIP(hostname)
	if route.EffectiveMode != models.RouteModeLocal && (hostname == "localhost" || strings.HasSuffix(hostname, ".localhost") || (ip != nil && ip.IsLoopback())) {
		return errors.New("publish a registered local endpoint instead of a remote localhost URL")
	}
	port := 80
	if target.Scheme == "https" {
		port = 443
	}
	if target.Port() != "" {
		port, err = strconv.Atoi(target.Port())
		if err != nil {
			return err
		}
	}
	if protectedPort == nil || !protectedPort(port) {
		return nil
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, hostname)
	if err != nil {
		return errors.New("cannot verify the selected target address")
	}
	local := map[string]bool{}
	interfaces, _ := net.InterfaceAddrs()
	for _, address := range interfaces {
		if value, _, err := net.ParseCIDR(address.String()); err == nil {
			local[value.String()] = true
		}
	}
	for _, address := range addresses {
		if address.IP.IsLoopback() || local[address.IP.String()] {
			return errors.New("management or ingress listener cannot be published as an application")
		}
	}
	return nil
}

// ValidateSharedPublicPolicy checks bindings in this computer's namespace before
// another selector publishes them. It does not change local publication.
func (r *Registry) ValidateSharedPublicPolicy(doc access.Document) error {
	r.mutationMu.Lock()
	defer r.mutationMu.Unlock()
	return validatePublicBindings(doc, r.workspaceSnapshot())
}

// PublicWorkspaceHosts exports a detached catalog from this routing namespace.
// Publication candidates exclude management hosts and contain no target data.
func (r *Registry) PublicWorkspaceHosts() []access.WorkspaceHosts {
	workspaces := r.workspaceSnapshot()
	var entries []access.WorkspaceHosts
	for _, ws := range workspaces {
		hosts := map[string]bool{}
		for _, route := range NormalizedRoutes(ws) {
			if route.PublicHost != config.Config.RementorDomain && route.PublicHost != config.DefaultRementorDomain {
				hosts[route.PublicHost] = true
			}
		}
		if len(hosts) == 0 {
			continue
		}
		entry := access.WorkspaceHosts{WorkspaceID: ws.WorkspaceID, Name: ws.NameOrID()}
		for host := range hosts {
			entry.LocalHosts = append(entry.LocalHosts, host)
		}
		sort.Strings(entry.LocalHosts)
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].WorkspaceID < entries[j].WorkspaceID })
	return entries
}
