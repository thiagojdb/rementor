package tailshare

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/thiagojdb/rementor/internal/access"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func redirectedClient(server *httptest.Server) *http.Client {
	client := server.Client()
	transport := client.Transport
	target, _ := url.Parse(server.URL)
	client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		u := *clone.URL
		u.Host = target.Host
		u.Scheme = target.Scheme
		clone.URL = &u
		return transport.RoundTrip(clone)
	})
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client
}

type localFixture struct {
	mu     sync.Mutex
	doc    map[string]any
	stale  bool
	posts  int
	tagged bool
	https  bool
}

func (f *localFixture) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/localapi/v0/status":
		tags := []string{}
		if f.tagged {
			tags = []string{"tag:rementor-gateway"}
		}
		certs := []string{}
		if f.https {
			certs = []string{"gateway.tailtest.ts.net"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"BackendState": "Running", "Self": map[string]any{"ID": "gateway", "HostName": "gateway", "Tags": tags}, "CurrentTailnet": map[string]any{"MagicDNSSuffix": "tailtest.ts.net", "MagicDNSEnabled": true}, "CertDomains": certs})
	case "/localapi/v0/services":
		io.WriteString(w, `{"svc:dev-giss":{"Ports":["tcp:443"]}}`)
	case "/localapi/v0/serve-config":
		if r.Method == "GET" {
			w.Header().Set("ETag", `"fixture-version"`)
			_ = json.NewEncoder(w).Encode(f.doc)
			return
		}
		if r.Header.Get("If-Match") != `"fixture-version"` || f.stale {
			w.WriteHeader(412)
			return
		}
		var doc map[string]any
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			w.WriteHeader(400)
			return
		}
		f.doc = doc
		f.posts++
		io.WriteString(w, `{}`)
	default:
		w.WriteHeader(404)
	}
}
func (f *localFixture) snapshot() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, _ := json.Marshal(f.doc)
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	return result
}
func localProvider(t *testing.T) (*Tailscale, *localFixture, Origin, string) {
	t.Helper()
	f := &localFixture{tagged: true, https: true, doc: map[string]any{"TCP": map[string]any{"443": map[string]any{"HTTPS": true}}, "Web": map[string]any{"computer.tailtest.ts.net:443": map[string]any{"Handlers": map[string]any{"/": map[string]any{"Proxy": "http://127.0.0.1:3774"}}}}, "FutureField": map[string]any{"preserved": true}}}
	s := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(s.Close)
	provider := NewTailscale("/gateway/socket", "")
	provider.local = redirectedClient(s)
	t.Cleanup(provider.Close)
	dir := t.TempDir()
	log := filepath.Join(dir, "commands.log")
	if err := os.WriteFile(filepath.Join(dir, "tailscale"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$TAILSCALE_TEST_LOG\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TAILSCALE_TEST_LOG", log)
	o := Origin{ID: "api", Name: "dev-giss", URL: "https://dev-giss.tailtest.ts.net", Target: "http://127.0.0.1:9302"}
	return provider, f, o, log
}
func TestServeMutationsUseCASAndPreserveUnrelatedConfiguration(t *testing.T) {
	p, f, o, log := localProvider(t)
	before := f.snapshot()
	claimed := false
	if err := p.Ensure(context.Background(), o, Ownership{Publication: "owner"}, func() error { claimed = true; return nil }); err != nil {
		t.Fatal(err)
	}
	after := f.snapshot()
	for _, key := range []string{"TCP", "Web", "FutureField"} {
		if !reflect.DeepEqual(before[key], after[key]) {
			t.Fatal("overwrote unrelated Serve configuration", key)
		}
	}
	if !claimed {
		t.Fatal("registration bypassed durable claim")
	}
	if err := p.Ensure(context.Background(), o, Ownership{Publication: "owner", Claimed: true}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if f.posts != 1 {
		t.Fatal("idempotent registration rewrote configuration")
	}
	if err := p.Remove(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	final := f.snapshot()
	if len(final["Services"].(map[string]any)) != 0 {
		t.Fatal("did not remove owned service")
	}
	for _, key := range []string{"TCP", "Web", "FutureField"} {
		if !reflect.DeepEqual(before[key], final[key]) {
			t.Fatal("cleanup reset unrelated Serve", key)
		}
	}
	commands, err := os.ReadFile(log)
	if err != nil || !strings.Contains(string(commands), "--socket=/gateway/socket serve advertise svc:dev-giss") || !strings.Contains(string(commands), "serve drain svc:dev-giss") || strings.Contains(string(commands), "reset") {
		t.Fatal("wrong scoped commands", err, string(commands))
	}
}
func TestServeRejectsForeignOrExternallyChangedEntryAndStaleWrites(t *testing.T) {
	p, f, o, _ := localProvider(t)
	f.mu.Lock()
	f.doc["Services"] = map[string]any{"svc:" + o.Name: serviceConfig(o)}
	f.mu.Unlock()
	claims := 0
	claim := func() error { claims++; return nil }
	if err := p.Ensure(context.Background(), o, Ownership{Publication: "owner"}, claim); err == nil || claims != 0 {
		t.Fatal("adopted unowned Serve entry")
	}
	f.mu.Lock()
	f.doc["Services"] = map[string]any{"svc:" + o.Name: map[string]any{"Tun": true}}
	f.mu.Unlock()
	if err := p.Remove(context.Background(), o); err == nil {
		t.Fatal("removed externally changed configuration")
	}
	f.mu.Lock()
	delete(f.doc, "Services")
	f.stale = true
	f.mu.Unlock()
	if err := p.Ensure(context.Background(), o, Ownership{Publication: "owner"}, claim); err == nil || !strings.Contains(err.Error(), "concurrently") {
		t.Fatal("accepted concurrent Serve write", err)
	}
	if claims != 1 || f.posts != 0 {
		t.Fatal("mutation did not preserve recoverable claim or CAS")
	}
}
func TestProviderChecksTaggingAndHTTPSWithoutChangingIdentity(t *testing.T) {
	p, f, _, _ := localProvider(t)
	f.mu.Lock()
	f.tagged = false
	f.mu.Unlock()
	g, err := p.Inspect(context.Background())
	if err != nil || g.DNSSuffix != "tailtest.ts.net" || !strings.Contains(g.Setup, "tagged") {
		t.Fatal("missing dedicated gateway requirement", g, err)
	}
	f.mu.Lock()
	f.tagged = true
	f.https = false
	f.mu.Unlock()
	g, err = p.Inspect(context.Background())
	if err != nil || !strings.Contains(g.Setup, "HTTPS") {
		t.Fatal("did not check HTTPS prerequisites", g, err)
	}
	if f.posts != 0 {
		t.Fatal("preflight mutated gateway")
	}
}
func TestServiceDefinitionOwnershipCredentialsAndPromotionPreserveAddresses(t *testing.T) {
	p, _, o, _ := localProvider(t)
	p.token = "secret-fixture-token"
	var definition map[string]any
	puts := 0
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != p.token || password != "" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path != "/svc:dev-giss" {
			t.Errorf("wrong service URL %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if r.Method == "GET" {
			if definition == nil {
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(definition)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&definition); err != nil {
			t.Error(err)
		}
		puts++
		io.WriteString(w, `{}`)
	}))
	defer cloud.Close()
	p.apiBase = cloud.URL + "/"
	p.cloud = cloud.Client()
	if err := p.ensureDefinition(context.Background(), o, Ownership{Publication: "installation", Installation: "installation"}); err != nil {
		t.Fatal(err)
	}
	if puts != 1 {
		t.Fatal("definition not automatically created")
	}
	definition["addrs"] = []any{"100.100.100.100", "fd7a:115c:a1e0::1"}
	definition["tags"] = []any{"tag:application"}
	before, _ := json.Marshal(definition)
	if err := p.ensureDefinition(context.Background(), o, Ownership{Publication: "other"}); err == nil || strings.Contains(err.Error(), p.token) {
		t.Fatal("foreign definition accepted or credentials leaked", err)
	}
	after, _ := json.Marshal(definition)
	if string(before) != string(after) || puts != 1 {
		t.Fatal("changed foreign definition")
	}
	if err := p.ensureDefinition(context.Background(), o, Ownership{Publication: "shared-group", Installation: "installation", Claimed: true}); err != nil {
		t.Fatal(err)
	}
	if puts != 2 || definition["annotations"].(map[string]any)["rementor-owner"] != "shared-group" || !reflect.DeepEqual(definition["addrs"], []any{"100.100.100.100", "fd7a:115c:a1e0::1"}) || !reflect.DeepEqual(definition["tags"], []any{"tag:application"}) {
		t.Fatal("shared promotion lost existing fields", definition)
	}
	if err := p.ensureDefinition(context.Background(), o, Ownership{Publication: "shared-group"}); err != nil || puts != 2 {
		t.Fatal("replica rewrote existing definition", err)
	}
}
func TestReadyRequiresAuthenticatedManifestAndApplicationProof(t *testing.T) {
	p, _, o, _ := localProvider(t)
	policy := access.Configuration{Revision: 9, Document: access.Document{SchemaVersion: 1, Origins: []access.Origin{{ID: "api", URL: o.URL}}, Bindings: []access.Binding{{WorkspaceID: "dev", OriginID: "api", LocalHost: "dev.localhost"}}, SharedWorkspaceID: "dev", AllowedUserIDs: []uint64{99}}}
	mode := "generic"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == "denied" {
			w.WriteHeader(403)
			return
		}
		if mode == "redirect" {
			http.Redirect(w, r, "https://elsewhere.example", 302)
			return
		}
		if r.URL.Path != "/__rementor/origins" {
			if mode == "missing-proof" {
				io.WriteString(w, "application")
				return
			}
			w.Header().Set("X-Rementor-Workspace", "dev")
			w.Header().Set("X-Rementor-App-ID", "orders")
			w.WriteHeader(401)
			return
		}
		if mode == "generic" {
			io.WriteString(w, `{}`)
			return
		}
		revision := uint64(9)
		if mode == "stale" {
			revision = 8
		}
		_ = json.NewEncoder(w).Encode(access.Manifest{SchemaVersion: 1, Revision: revision, WorkspaceID: "dev", Origins: map[string]string{"api": o.URL}})
	}))
	defer server.Close()
	p.probe = redirectedClient(server)
	for _, failure := range []string{"generic", "denied", "redirect", "stale", "missing-proof"} {
		mode = failure
		if err := p.Verify(context.Background(), o, policy); err == nil {
			t.Fatal("unverified service marked ready", failure)
		}
	}
	mode = "valid"
	if err := p.Verify(context.Background(), o, policy); err != nil {
		t.Fatal("valid authenticated route proof rejected", err)
	}
	p.ApplicationPath = func(access.Manifest, Origin) string { return "//elsewhere.example" }
	if err := p.Verify(context.Background(), o, policy); err == nil {
		t.Fatal("unsafe verification path accepted")
	}
	p.probe.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network detail including sensitive data")
	})
	if err := p.Verify(context.Background(), o, policy); err == nil || strings.Contains(err.Error(), "sensitive") {
		t.Fatal("network failure leaked details", err)
	}
}
