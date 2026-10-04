package routing

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func fleet(t *testing.T) Topology {
	t.Helper()
	data, err := os.ReadFile("../../examples/fleet/topology.json")
	if err != nil {
		t.Fatal(err)
	}
	var topology Topology
	if err := json.Unmarshal(data, &topology); err != nil {
		t.Fatal(err)
	}
	return topology
}

func compileFleet(t *testing.T, topology Topology) *Resolver {
	t.Helper()
	r, err := Compile(topology)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestFleetInheritanceAndFailure(t *testing.T) {
	r := compileFleet(t, fleet(t))
	for _, tc := range []struct {
		name, url, instance, target, source, owner string
		availability                               Availability
		err                                        error
	}{
		{name: "phone consumes shared ingress", url: "https://services.rementor.example.test/orders/42", target: "orders-brutus", source: "instance", owner: "brutus"},
		{name: "Marcus inherits Brutus endpoint", url: "https://services.rementor.example.test/orders/42", instance: "marcus-dev", target: "orders-brutus", source: "inherited", owner: "brutus"},
		{name: "Marcus overrides billing", url: "https://services.rementor.example.test/billing", instance: "marcus-dev", target: "billing-marcus", source: "instance", owner: "marcus"},
		{name: "shared billing stays remote", url: "https://services.rementor.example.test/billing", target: "billing-remote", source: "default"},
		{name: "unrelated login origin", url: "https://login.other.example.test/oauth/authorize?client=web", instance: "marcus-dev", target: "login-remote", source: "default"},
		{name: "websocket shares HTTPS origin", url: "wss://events.rementor.example.test/socket", instance: "marcus-dev", target: "events-brutus", source: "default", owner: "brutus"},
		{name: "explicit endpoint fails without fallback", url: "https://services.rementor.example.test/orders", instance: "marcus-dev", target: "orders-brutus", source: "inherited", owner: "brutus", availability: Availability{UnavailableTargets: map[string]bool{"orders-brutus": true}}, err: ErrTargetUnavailable},
		{name: "offline owner fails without fallback", url: "https://services.rementor.example.test/orders", instance: "marcus-dev", target: "orders-brutus", source: "inherited", owner: "brutus", availability: Availability{UnavailableNodes: map[string]bool{"brutus": true}}, err: ErrTargetUnavailable},
		{name: "unknown instance never falls back", url: "https://services.rementor.example.test/orders", instance: "typo", err: ErrInstanceNotFound},
		{name: "path segment boundary", url: "https://services.rementor.example.test/orders-other", err: ErrRouteNotFound},
		{name: "different scheme", url: "http://services.rementor.example.test/orders", err: ErrRouteNotFound},
		{name: "different port", url: "https://services.rementor.example.test:8443/orders", err: ErrRouteNotFound},
		{name: "canonical default port and hostname", url: "https://SERVICES.REMENTOR.EXAMPLE.TEST.:443/orders", target: "orders-brutus", source: "instance", owner: "brutus"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := r.Resolve(tc.url, tc.instance, tc.availability)
			if !errors.Is(err, tc.err) {
				t.Fatalf("error = %v, want %v", err, tc.err)
			}
			if tc.target == "" {
				return
			}
			if got.PublicURL != tc.url || got.Target.ID != tc.target || got.Target.NodeID != tc.owner || got.SelectionSource != tc.source {
				t.Fatalf("unexpected resolution: %+v", got)
			}
			if tc.source == "inherited" && (got.SourceInstance != "brutus-dev" || !reflect.DeepEqual(got.InstanceChain, []string{"marcus-dev", "brutus-dev"})) {
				t.Fatalf("missing inheritance provenance: %+v", got)
			}
		})
	}
}

func TestRecompiledParentChangesPropagateAndSnapshotsStayDetached(t *testing.T) {
	topology := fleet(t)
	previous := compileFleet(t, topology)
	topology.Instances[0].Overrides["orders"] = "orders-remote"
	next := compileFleet(t, topology)
	for _, instance := range []string{"brutus-dev", "marcus-dev"} {
		got, err := next.Resolve("https://services.rementor.example.test/orders", instance, Availability{})
		if err != nil || got.Target.ID != "orders-remote" {
			t.Fatalf("%s did not follow parent: %+v, %v", instance, got, err)
		}
	}
	got, err := previous.Resolve("https://services.rementor.example.test/orders", "marcus-dev", Availability{})
	if err != nil || got.Target.ID != "orders-brutus" {
		t.Fatalf("published snapshot was mutated: %+v, %v", got, err)
	}
	got, err = next.Resolve("https://services.rementor.example.test/billing", "marcus-dev", Availability{})
	if err != nil || got.Target.ID != "billing-marcus" {
		t.Fatalf("child override lost after parent change: %+v, %v", got, err)
	}
}

func TestRoutePrecedenceAndAliases(t *testing.T) {
	topology := fleet(t)
	topology.Targets = append(topology.Targets, Target{ID: "orders-alias", Kind: TargetRoute, RouteID: "orders"})
	topology.Routes = append(topology.Routes,
		Route{ID: "fallback", OriginID: "services", Path: "/", Match: "prefix", AppID: "web", ServiceID: "web", TargetID: "billing-remote"},
		Route{ID: "special", OriginID: "services", Path: "/orders/special", Match: "prefix", AppID: "orders", ServiceID: "orders-api", TargetID: "login-remote"},
		Route{ID: "exact", OriginID: "services", Path: "/orders/special", Match: "exact", AppID: "orders", ServiceID: "orders-api", TargetID: "billing-remote"},
		Route{ID: "alias", OriginID: "services", Path: "/alias", Match: "prefix", AppID: "orders", ServiceID: "orders-api", TargetID: "orders-alias"},
	)
	r := compileFleet(t, topology)
	for _, tc := range []struct{ path, route, target string }{
		{"/elsewhere", "fallback", "billing-remote"},
		{"/orders/other", "orders", "orders-brutus"},
		{"/orders/special/child", "special", "login-remote"},
		{"/orders/special", "exact", "billing-remote"},
		{"/alias", "alias", "orders-brutus"},
	} {
		got, err := r.Resolve("https://services.rementor.example.test"+tc.path, "marcus-dev", Availability{})
		if err != nil || got.RouteID != tc.route || got.Target.ID != tc.target {
			t.Fatalf("%s: %+v, %v", tc.path, got, err)
		}
		if tc.route == "alias" && !reflect.DeepEqual(got.RouteChain, []string{"alias", "orders"}) {
			t.Fatalf("missing alias chain: %+v", got)
		}
	}
	got, err := r.Resolve("https://services.rementor.example.test/alias", "marcus-dev", Availability{UnavailableNodes: map[string]bool{"brutus": true}})
	if !errors.Is(err, ErrTargetUnavailable) || got.Target.ID != "orders-brutus" {
		t.Fatalf("alias hid endpoint failure: %+v, %v", got, err)
	}
}

func TestInvalidTopologyIsRejectedBeforeResolution(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Topology)
	}{
		{"unknown version", func(x *Topology) { x.Version = 2 }},
		{"duplicate origin", func(x *Topology) {
			x.Origins = append(x.Origins, Origin{ID: "other", URL: "https://WEB.REMENTOR.EXAMPLE.TEST:443/"})
		}},
		{"origin with path", func(x *Topology) { x.Origins[0].URL += "/foo" }},
		{"origin credentials", func(x *Topology) { x.Origins[0].URL = "https://user:password@example.test" }},
		{"invalid port", func(x *Topology) { x.Origins[0].URL = "https://example.test:65536" }},
		{"unknown node", func(x *Topology) { x.Targets[0].NodeID = "missing" }},
		{"unknown target", func(x *Topology) { x.Routes[0].TargetID = "missing" }},
		{"contradictory target", func(x *Topology) { x.Targets[0].RouteID = "orders" }},
		{"duplicate matcher", func(x *Topology) { route := x.Routes[0]; route.ID = "duplicate"; x.Routes = append(x.Routes, route) }},
		{"invalid route path", func(x *Topology) { x.Routes[0].Path = "/foo/../bar" }},
		{"unknown inherited instance", func(x *Topology) { x.Instances[1].Inherit = "missing" }},
		{"inheritance cycle", func(x *Topology) { x.Instances[0].Inherit = "marcus-dev" }},
		{"unknown override route", func(x *Topology) { x.Instances[0].Overrides["missing"] = "orders-remote" }},
		{"unknown shared instance", func(x *Topology) { x.SharedInstance = "missing" }},
		{"route alias cycle in child", func(x *Topology) {
			x.Targets = append(x.Targets, Target{ID: "self", Kind: TargetRoute, RouteID: "orders"})
			x.Instances[1].Overrides["orders"] = "self"
		}},
		{"baseline alias cycle", func(x *Topology) {
			x.Targets = append(x.Targets, Target{ID: "self", Kind: TargetRoute, RouteID: "orders"})
			x.Routes[1].TargetID = "self"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			topology := fleet(t)
			tc.change(&topology)
			if _, err := Compile(topology); err == nil {
				t.Fatal("invalid topology accepted")
			}
		})
	}
}

func TestLoadRejectsUnknownAndTrailingJSON(t *testing.T) {
	for _, input := range []string{`{"version":1,"profiels":[]}`, `{"version":1} {}`, `{"version":1} garbage`} {
		if _, err := Load(strings.NewReader(input)); err == nil {
			t.Fatalf("accepted invalid input %s", input)
		}
	}
}

func TestRequestValidation(t *testing.T) {
	r := compileFleet(t, fleet(t))
	for _, raw := range []string{
		"https://services.rementor.example.test/orders%2Fother",
		"https://services.rementor.example.test/orders/../billing",
		"https://user:pass@services.rementor.example.test/orders",
		"https://services.rementor.example.test/orders#fragment",
		"ftp://services.rementor.example.test/orders",
	} {
		if _, err := r.Resolve(raw, "", Availability{}); err == nil {
			t.Fatalf("accepted ambiguous URL %s", raw)
		}
	}
	if _, err := r.Resolve("https://services.rementor.example.test/orders", "", Availability{UnavailableTargets: map[string]bool{"typo": true}}); err == nil {
		t.Fatal("unknown availability target accepted")
	}
}

func TestTransitiveInheritanceAndReset(t *testing.T) {
	topology := fleet(t)
	topology.Instances = append(topology.Instances, Instance{ID: "marcus-review", NodeID: "marcus", Inherit: "marcus-dev", Overrides: map[string]string{"orders": "orders-remote"}})
	r := compileFleet(t, topology)
	url := "https://services.rementor.example.test/orders"
	got, err := r.Resolve(url, "marcus-review", Availability{})
	if err != nil || got.Target.ID != "orders-remote" {
		t.Fatalf("nearest override did not win: %+v, %v", got, err)
	}
	delete(topology.Instances[2].Overrides, "orders")
	r = compileFleet(t, topology)
	got, err = r.Resolve(url, "marcus-review", Availability{})
	if err != nil || got.Target.ID != "orders-brutus" || !reflect.DeepEqual(got.InstanceChain, []string{"marcus-review", "marcus-dev", "brutus-dev"}) {
		t.Fatalf("reset did not resume transitive inheritance: %+v, %v", got, err)
	}
}

func TestSingleDeviceWithoutPrivateNetworkProvider(t *testing.T) {
	topology := Topology{
		Version: 1, Nodes: []Node{{ID: "computer"}}, SharedInstance: "dev",
		Origins:   []Origin{{ID: "api", URL: "http://api.localhost:8080"}},
		Targets:   []Target{{ID: "local", Kind: TargetNode, NodeID: "computer", URL: "http://[::1]:9000"}},
		Routes:    []Route{{ID: "api", OriginID: "api", Path: "/", Match: "prefix", AppID: "api", ServiceID: "api", TargetID: "local"}},
		Instances: []Instance{{ID: "dev", NodeID: "computer"}},
	}
	r := compileFleet(t, topology)
	got, err := r.Resolve("http://api.localhost:8080/items", "", Availability{})
	if err != nil || got.Target.NodeID != "computer" || got.Target.URL != "http://[::1]:9000" {
		t.Fatalf("local-only topology failed: %+v, %v", got, err)
	}
}
