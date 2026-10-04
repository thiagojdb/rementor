// Package routing compiles provider-independent routing intent for inspection.
// It does not expose listeners, authenticate clients, or apply proxy changes.
package routing

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/thiagojdb/rementor/internal/validation"
)

const (
	TargetRemote = "remote"
	TargetNode   = "node"
	TargetRoute  = "route"
)

// Topology is an experimental file contract, separate from the live workspace
// and protobuf contracts. An instance selects its own environment; phones use
// the environment of the gateway they reach.
type Topology struct {
	Version        int        `json:"version"`
	Nodes          []Node     `json:"nodes"`
	Origins        []Origin   `json:"origins"`
	Targets        []Target   `json:"targets"`
	Routes         []Route    `json:"routes"`
	Instances      []Instance `json:"instances"`
	SharedInstance string     `json:"sharedInstance"`
}

type Node struct {
	ID string `json:"id"`
}

type Origin struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// A node target's URL is relative to its owning node's network namespace.
// For example, http://127.0.0.1:8080 must be dialed by that node's agent.
type Target struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	URL     string `json:"url,omitempty"`
	NodeID  string `json:"nodeId,omitempty"`
	RouteID string `json:"routeId,omitempty"`
}

type Route struct {
	ID        string `json:"id"`
	OriginID  string `json:"originId"`
	Path      string `json:"path"`
	Match     string `json:"match"` // exact or segment-bounded prefix
	AppID     string `json:"appId"`
	ServiceID string `json:"serviceId"`
	TargetID  string `json:"targetId"`
}

type Instance struct {
	ID        string            `json:"id"`
	NodeID    string            `json:"nodeId"`
	Inherit   string            `json:"inherit,omitempty"`
	Overrides map[string]string `json:"overrides,omitempty"` // route ID -> target ID
}

// Resolver owns a detached, validated snapshot. A caller can reuse it across
// requests and concurrently resolve with different availability observations.
type Resolver struct {
	nodes          map[string]bool
	origins        map[string]string
	targets        map[string]Target
	routes         map[string]Route
	ordered        []Route
	instances      map[string]Instance
	sharedInstance string
}

// Load rejects unknown fields and extra JSON documents so misspelled routing
// intent cannot silently become a shared-default selection.
func Load(reader io.Reader) (*Resolver, error) {
	var topology Topology
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&topology); err != nil {
		return nil, fmt.Errorf("decode topology: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("topology must contain one JSON document")
		}
		return nil, fmt.Errorf("decode trailing topology data: %w", err)
	}
	return Compile(topology)
}

func Compile(topology Topology) (*Resolver, error) {
	if topology.Version != 1 {
		return nil, fmt.Errorf("unsupported topology version %d (expected 1)", topology.Version)
	}
	r := &Resolver{
		nodes: map[string]bool{}, origins: map[string]string{},
		targets: map[string]Target{}, routes: map[string]Route{},
		instances: map[string]Instance{}, sharedInstance: topology.SharedInstance,
	}
	nodes := r.nodes
	for _, node := range topology.Nodes {
		if err := validation.Identifier("node ID", node.ID); err != nil {
			return nil, err
		}
		if nodes[node.ID] {
			return nil, fmt.Errorf("duplicate node %q", node.ID)
		}
		nodes[node.ID] = true
	}
	originOwners := map[string]string{}
	for _, origin := range topology.Origins {
		if err := validation.Identifier("origin ID", origin.ID); err != nil {
			return nil, err
		}
		if _, ok := r.origins[origin.ID]; ok {
			return nil, fmt.Errorf("duplicate origin %q", origin.ID)
		}
		key, err := originKey(origin.URL, false)
		if err != nil {
			return nil, fmt.Errorf("origin %q: %w", origin.ID, err)
		}
		if owner, ok := originOwners[key]; ok {
			return nil, fmt.Errorf("origins %q and %q name the same public origin", owner, origin.ID)
		}
		originOwners[key], r.origins[origin.ID] = origin.ID, key
	}
	for _, target := range topology.Targets {
		if err := validation.Identifier("target ID", target.ID); err != nil {
			return nil, err
		}
		if _, ok := r.targets[target.ID]; ok {
			return nil, fmt.Errorf("duplicate target %q", target.ID)
		}
		switch target.Kind {
		case TargetRemote, TargetNode:
			if target.URL == "" {
				return nil, fmt.Errorf("target %q requires a URL", target.ID)
			}
			if err := validation.OptionalHTTPURL(target.URL); err != nil {
				return nil, fmt.Errorf("target %q: %w", target.ID, err)
			}
			if _, err := originKey(target.URL, true); err != nil {
				return nil, fmt.Errorf("target %q: %w", target.ID, err)
			}
			if target.RouteID != "" {
				return nil, fmt.Errorf("endpoint target %q cannot reference a route", target.ID)
			}
			if target.Kind == TargetNode {
				if !nodes[target.NodeID] {
					return nil, fmt.Errorf("target %q references unknown node %q", target.ID, target.NodeID)
				}
			} else if target.NodeID != "" {
				return nil, fmt.Errorf("remote target %q cannot name a node", target.ID)
			}
		case TargetRoute:
			if target.RouteID == "" || target.URL != "" || target.NodeID != "" {
				return nil, fmt.Errorf("route target %q requires only a route ID", target.ID)
			}
		default:
			return nil, fmt.Errorf("target %q has unsupported kind %q", target.ID, target.Kind)
		}
		r.targets[target.ID] = target
	}
	matchOwners := map[string]string{}
	for _, route := range topology.Routes {
		if err := validation.Identifier("route ID", route.ID); err != nil {
			return nil, err
		}
		if _, ok := r.routes[route.ID]; ok {
			return nil, fmt.Errorf("duplicate route %q", route.ID)
		}
		if err := validation.Identifier("app ID", route.AppID); err != nil {
			return nil, err
		}
		if err := validation.Identifier("service ID", route.ServiceID); err != nil {
			return nil, err
		}
		if _, ok := r.origins[route.OriginID]; !ok {
			return nil, fmt.Errorf("route %q references unknown origin %q", route.ID, route.OriginID)
		}
		path, err := validation.NormalizeRoutePath("route path", route.Path, false)
		if err != nil {
			return nil, fmt.Errorf("route %q: %w", route.ID, err)
		}
		route.Path = path
		if route.Match != "exact" && route.Match != "prefix" {
			return nil, fmt.Errorf("route %q match must be exact or prefix", route.ID)
		}
		if _, ok := r.targets[route.TargetID]; !ok {
			return nil, fmt.Errorf("route %q references unknown target %q", route.ID, route.TargetID)
		}
		key := route.OriginID + "\x00" + route.Path + "\x00" + route.Match
		if owner, ok := matchOwners[key]; ok {
			return nil, fmt.Errorf("routes %q and %q have the same matcher", owner, route.ID)
		}
		matchOwners[key], r.routes[route.ID] = route.ID, route
		r.ordered = append(r.ordered, route)
	}
	for _, target := range r.targets {
		if target.Kind == TargetRoute {
			if _, ok := r.routes[target.RouteID]; !ok {
				return nil, fmt.Errorf("target %q references unknown route %q", target.ID, target.RouteID)
			}
		}
	}
	for _, instance := range topology.Instances {
		if err := validation.Identifier("instance ID", instance.ID); err != nil {
			return nil, err
		}
		if _, ok := r.instances[instance.ID]; ok {
			return nil, fmt.Errorf("duplicate instance %q", instance.ID)
		}
		if !nodes[instance.NodeID] {
			return nil, fmt.Errorf("instance %q references unknown node %q", instance.ID, instance.NodeID)
		}
		copyInstance := Instance{ID: instance.ID, NodeID: instance.NodeID, Inherit: instance.Inherit, Overrides: map[string]string{}}
		for routeID, targetID := range instance.Overrides {
			if _, ok := r.routes[routeID]; !ok {
				return nil, fmt.Errorf("instance %q references unknown route %q", instance.ID, routeID)
			}
			if _, ok := r.targets[targetID]; !ok {
				return nil, fmt.Errorf("instance %q references unknown target %q", instance.ID, targetID)
			}
			copyInstance.Overrides[routeID] = targetID
		}
		r.instances[instance.ID] = copyInstance
	}
	if _, ok := r.instances[r.sharedInstance]; !ok {
		return nil, fmt.Errorf("unknown shared instance %q", r.sharedInstance)
	}
	if err := r.checkInheritance(); err != nil {
		return nil, err
	}
	if err := r.checkCycles(""); err != nil {
		return nil, err
	}
	for instanceID := range r.instances {
		if err := r.checkCycles(instanceID); err != nil {
			return nil, err
		}
	}
	sort.Slice(r.ordered, func(i, j int) bool {
		a, b := r.ordered[i], r.ordered[j]
		if a.Match != b.Match {
			return a.Match == "exact"
		}
		if len(a.Path) != len(b.Path) {
			return len(a.Path) > len(b.Path)
		}
		return a.ID < b.ID
	})
	return r, nil
}

func originKey(raw string, allowPath bool) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return "", fmt.Errorf("expected an absolute HTTP(S) URL without credentials or fragment")
	}
	if strings.IndexFunc(raw, func(c rune) bool { return c <= ' ' || c == 127 }) >= 0 {
		return "", fmt.Errorf("URL contains whitespace or control characters")
	}
	if !allowPath && ((u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery) {
		return "", fmt.Errorf("a public origin cannot include a path or query")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if net.ParseIP(host) == nil {
		if len(host) > 253 {
			return "", fmt.Errorf("invalid hostname")
		}
		for _, label := range strings.Split(host, ".") {
			if len(label) > 63 || strings.HasSuffix(label, "-") || validation.Identifier("hostname label", label) != nil {
				return "", fmt.Errorf("invalid hostname %q", host)
			}
		}
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("invalid port")
		}
		port = strconv.Itoa(n)
	}
	if strings.HasSuffix(u.Host, ":") {
		return "", fmt.Errorf("empty port")
	}
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return u.Scheme + "://" + host, nil
}

func (r *Resolver) selection(routeID, instanceID string) (targetID, sourceInstance string, chain []string) {
	for instanceID != "" {
		instance := r.instances[instanceID]
		chain = append(chain, instanceID)
		if targetID, ok := instance.Overrides[routeID]; ok {
			return targetID, instanceID, chain
		}
		instanceID = instance.Inherit
	}
	return r.routes[routeID].TargetID, "", chain
}

func (r *Resolver) checkInheritance() error {
	for instanceID := range r.instances {
		seen := map[string]bool{}
		for current := instanceID; current != ""; current = r.instances[current].Inherit {
			if _, ok := r.instances[current]; !ok {
				return fmt.Errorf("instance %q inherits unknown instance %q", instanceID, current)
			}
			if seen[current] {
				return fmt.Errorf("environment inheritance cycle at %q", current)
			}
			seen[current] = true
		}
	}
	return nil
}

func (r *Resolver) checkCycles(instanceID string) error {
	visiting, done := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(routeID string) error {
		if visiting[routeID] {
			return fmt.Errorf("route alias cycle at %q in instance %q", routeID, instanceID)
		}
		if done[routeID] {
			return nil
		}
		visiting[routeID] = true
		targetID, _, _ := r.selection(routeID, instanceID)
		target := r.targets[targetID]
		if target.Kind == TargetRoute {
			if err := visit(target.RouteID); err != nil {
				return err
			}
		}
		visiting[routeID], done[routeID] = false, true
		return nil
	}
	for routeID := range r.routes {
		if err := visit(routeID); err != nil {
			return err
		}
	}
	return nil
}
