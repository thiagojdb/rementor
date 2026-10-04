package routing

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/thiagojdb/rementor/internal/validation"
)

var (
	ErrRouteNotFound     = errors.New("route not found")
	ErrTargetUnavailable = errors.New("target unavailable")
	ErrInstanceNotFound  = errors.New("instance not found")
)

type Resolution struct {
	PublicURL        string   `json:"publicUrl"`
	RouteID          string   `json:"routeId"`
	AppID            string   `json:"appId"`
	ServiceID        string   `json:"serviceId"`
	InstanceID       string   `json:"instanceId"`
	SourceInstance   string   `json:"sourceInstance,omitempty"`
	InstanceChain    []string `json:"instanceChain"`
	SelectionSource  string   `json:"selectionSource"`
	SelectedTargetID string   `json:"selectedTargetId"`
	Target           Target   `json:"target"`
	RouteChain       []string `json:"routeChain"`
}

type Availability struct {
	UnavailableTargets map[string]bool
	UnavailableNodes   map[string]bool
}

// Resolve selects a target for a URL at the serving Rementor instance.
// Empty instanceID selects the declared shared ingress environment.
// availability is an observation for this inspection: omitted targets have
// unknown health, and selection is not a claim of live reachability.
// WebSocket URLs share the corresponding HTTP(S) origin's routing table.
func (r *Resolver) Resolve(publicURL, instanceID string, availability Availability) (Resolution, error) {
	if instanceID == "" {
		instanceID = r.sharedInstance
	}
	if _, ok := r.instances[instanceID]; !ok {
		return Resolution{}, fmt.Errorf("%w: %s", ErrInstanceNotFound, instanceID)
	}
	for targetID := range availability.UnavailableTargets {
		if _, ok := r.targets[targetID]; !ok {
			return Resolution{}, fmt.Errorf("availability references unknown target %q", targetID)
		}
	}
	for nodeID := range availability.UnavailableNodes {
		if !r.nodes[nodeID] {
			return Resolution{}, fmt.Errorf("availability references unknown node %q", nodeID)
		}
	}
	u, err := url.Parse(publicURL)
	if err != nil {
		return Resolution{}, fmt.Errorf("invalid request URL: %w", err)
	}
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	}
	key, err := originKey(u.String(), true)
	if err != nil {
		return Resolution{}, fmt.Errorf("invalid request URL: %w", err)
	}
	path := u.Path
	if path == "" {
		path = "/"
	}
	// Encoded separators/dot segments need gateway-specific handling; refuse
	// ambiguous spellings in this inspector rather than choose a different route.
	if u.RawPath != "" {
		return Resolution{}, fmt.Errorf("encoded request paths are not supported by the topology inspector")
	}
	path, err = validation.NormalizeRoutePath("request path", path, false)
	if err != nil {
		return Resolution{}, err
	}
	for _, route := range r.ordered {
		if r.origins[route.OriginID] != key {
			continue
		}
		matches := path == route.Path
		if route.Match == "prefix" {
			matches = matches || route.Path == "/" || strings.HasPrefix(path, route.Path+"/")
		}
		if !matches {
			continue
		}
		targetID, sourceInstance, instanceChain := r.selection(route.ID, instanceID)
		result := Resolution{PublicURL: publicURL, RouteID: route.ID, AppID: route.AppID, ServiceID: route.ServiceID, InstanceID: instanceID, SourceInstance: sourceInstance, InstanceChain: instanceChain, SelectionSource: "default", SelectedTargetID: targetID}
		if sourceInstance == instanceID {
			result.SelectionSource = "instance"
		} else if sourceInstance != "" {
			result.SelectionSource = "inherited"
		}
		target, chain, err := r.resolveTarget(route.ID, instanceID, availability)
		result.Target, result.RouteChain = target, chain
		return result, err
	}
	return Resolution{}, fmt.Errorf("%w for %s%s", ErrRouteNotFound, key, path)
}

func (r *Resolver) resolveTarget(routeID, instanceID string, availability Availability) (Target, []string, error) {
	targetID, _, _ := r.selection(routeID, instanceID)
	target := r.targets[targetID]
	chain := []string{routeID}
	if availability.UnavailableTargets[targetID] || (target.Kind == TargetNode && availability.UnavailableNodes[target.NodeID]) {
		if target.Kind == TargetNode {
			return target, chain, fmt.Errorf("%w: %s (node %q)", ErrTargetUnavailable, targetID, target.NodeID)
		}
		return target, chain, fmt.Errorf("%w: %s", ErrTargetUnavailable, targetID)
	}
	if target.Kind == TargetRoute {
		endpoint, tail, err := r.resolveTarget(target.RouteID, instanceID, availability)
		return endpoint, append(chain, tail...), err
	}
	return target, chain, nil
}
