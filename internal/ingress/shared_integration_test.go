package ingress_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/thiagojdb/rementor/internal/access"
	"github.com/thiagojdb/rementor/internal/config"
	"github.com/thiagojdb/rementor/internal/ingress"
	"github.com/thiagojdb/rementor/internal/models"
	"github.com/thiagojdb/rementor/internal/nginx"
	"github.com/thiagojdb/rementor/internal/peers"
	"github.com/thiagojdb/rementor/internal/services"
	"github.com/thiagojdb/rementor/internal/shared"
	"golang.org/x/net/websocket"
)

// A gateway's own workspace is deliberately unusable. Every successful response
// must be selected by the shared owner and resolved on the routing computer.
type gatewaySource struct{}

func (gatewaySource) PublicAccessConfiguration() access.Configuration {
	return access.Configuration{Document: access.Empty()}
}
func (gatewaySource) BeginPublicRequest(context.Context, access.Identity, string, string, bool) (context.Context, services.AccessRequest, func(), error) {
	return nil, services.AccessRequest{}, nil, errors.New("gateway local publication must not be used")
}
func (gatewaySource) BeginSharedPublicRequest(context.Context, access.Configuration, access.Identity, string, string, bool) (context.Context, services.AccessRequest, func(), error) {
	return nil, services.AccessRequest{}, nil, errors.New("gateway local workspace must not be used")
}

type staleSharedResponse struct{}

func (staleSharedResponse) ServeSharedHTTP(w http.ResponseWriter, _ *http.Request, _ string, _ shared.Role, _ shared.Request) {
	w.Header().Set("X-Rementor-Shared-Digest", strings.Repeat("0", 64))
	_, _ = io.WriteString(w, "unproved application body")
}

type pausedSharedActivation struct{ shared.Peer }

func (pausedSharedActivation) ActivateShared(context.Context, string, string) error {
	return errors.New("activation interrupted")
}

func TestSharedIngressPinnedHTTPWebSocketsAndTransferPreserveRoutingComputer(t *testing.T) {
	binary, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx required")
	}
	ctx := context.Background()
	var calls atomic.Int32
	newApplication := func(label string) *httptest.Server {
		mux := http.NewServeMux()
		mux.Handle("/orders/socket", websocket.Handler(func(conn *websocket.Conn) {
			defer conn.Close()
			for {
				var value string
				if websocket.Message.Receive(conn, &value) != nil {
					return
				}
				if websocket.Message.Send(conn, label+":"+value) != nil {
					return
				}
			}
		}))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			for name := range r.Header {
				lower := strings.ToLower(name)
				if strings.HasPrefix(lower, "x-rementor-") || strings.HasPrefix(lower, "tailscale-") {
					t.Errorf("internal header leaked to application: %s", name)
				}
			}
			w.Header().Set("Set-Cookie", "session=fixture; Secure; HttpOnly; SameSite=None")
			w.Header().Set("Access-Control-Allow-Origin", "https://forged.example.test")
			w.Header().Set("X-Rementor-Shared-Owner", "forged")
			if r.URL.Path == "/orders/login" {
				w.Header().Set("Location", "https://api.example.test/orders/callback")
				w.WriteHeader(http.StatusFound)
				return
			}
			body, _ := io.ReadAll(r.Body)
			_ = json.NewEncoder(w).Encode(map[string]string{"application": label, "body": string(body), "query": r.URL.RawQuery, "host": r.Host, "forwardedHost": r.Header.Get("X-Forwarded-Host"), "proto": r.Header.Get("X-Forwarded-Proto"), "cookie": r.Header.Get("Cookie"), "authorization": r.Header.Get("Authorization")})
		})
		server := httptest.NewServer(mux)
		t.Cleanup(server.Close)
		return server
	}
	dev, branch := newApplication("dev"), newApplication("branch")
	managerPaths := map[string]string{}
	newManager := func(name string) *peers.Manager {
		directory := filepath.Join(t.TempDir(), name)
		m, err := peers.Open(directory, name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(m.Close)
		managerPaths[m.State().Fingerprint] = directory
		address := freeAddress(t)
		if err := m.Configure(address, "https://"+address); err != nil {
			t.Fatal(err)
		}
		return m
	}
	selector, router, replica := newManager("selector"), newManager("router"), newManager("replica")
	pair := func(client, server *peers.Manager) {
		code, invite, err := server.CreateInvitation()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Pair(ctx, code, invite.Fingerprint); err != nil {
			t.Fatal(err)
		}
	}
	pair(router, selector)
	pair(replica, selector)
	pair(replica, router)
	stores := map[string]*config.SharedIngressStore{}
	newAuthority := func(m *peers.Manager) *shared.Authority {
		t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), m.State().Name))
		store, err := config.NewSharedIngressStore()
		if err != nil {
			t.Fatal(err)
		}
		a, err := shared.Open(m.State().Fingerprint, store)
		if err != nil {
			t.Fatal(err)
		}
		stores[m.State().Fingerprint] = store
		m.SetSharedAuthority(a)
		return a
	}
	sa, ra, ga := newAuthority(selector), newAuthority(router), newAuthority(replica)
	sID, rID, gID := selector.State().Fingerprint, router.State().Fingerprint, replica.State().Fingerprint
	doc := access.Document{SchemaVersion: 1, Origins: []access.Origin{{ID: "api", URL: "https://api.example.test"}}, Bindings: []access.Binding{{WorkspaceID: "dev", OriginID: "api", LocalHost: "dev.localhost"}, {WorkspaceID: "branch", OriginID: "api", LocalHost: "branch.localhost"}}, AllowedUserIDs: []uint64{99}, SharedWorkspaceID: "dev", Devices: []access.Device{{NodeID: "computer-b", WorkspaceID: "branch"}}}
	before, err := sa.Create(doc, rID, []string{sID, gID}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ra.Join(ctx, sID, 0, router); err != nil {
		t.Fatal(err)
	}
	if _, err := ga.Join(ctx, sID, 0, replica); err != nil {
		t.Fatal(err)
	}

	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "workspaces"))
	previousConfig := config.Config
	config.Config.RementorDomain = config.DefaultRementorDomain
	t.Cleanup(func() { config.Config = previousConfig })
	for _, item := range []struct {
		id  string
		app *httptest.Server
	}{{"dev", dev}, {"branch", branch}} {
		ws := models.WorkspaceConfig{ID: item.id, Type: "routing", Route: models.RouteState{RouteVersion: 7}, Routing: models.RoutingConfig{LocalDomain: item.id + ".localhost"}, Applications: []models.ApplicationConfig{{ID: "orders", Active: true, PublicPath: "/orders", Port: serverPort(item.app)}}}
		if err := config.AppendWorkspace(ws); err != nil {
			t.Fatal(err)
		}
	}
	registry := &services.Registry{}
	registry.SetRoutingProvider(noProxy{})
	if err := registry.Load(); err != nil {
		t.Fatal(err)
	}
	// The routing computer's private publication chooses a different workspace.
	// Shared selection must come from the captured authority document instead.
	localDoc := doc.Clone()
	localDoc.SharedWorkspaceID, localDoc.Devices = "branch", nil
	if _, err := registry.SavePublicAccessConfiguration(localDoc, 0); err != nil {
		t.Fatal(err)
	}
	nginxAddress := freeAddress(t)
	_, port, _ := net.SplitHostPort(nginxAddress)
	t.Setenv("REMENTOR_NGINX_LISTEN_HOST", "127.0.0.1")
	t.Setenv("REMENTOR_NGINX_LISTEN_PORTS", port)
	rendered, err := nginx.RenderConfig(registry.GetWorkspaces(), config.DefaultRementorDomain)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	routes, conf := filepath.Join(dir, "routes.conf"), filepath.Join(dir, "nginx.conf")
	if err := os.WriteFile(routes, []byte(rendered), 0600); err != nil {
		t.Fatal(err)
	}
	text := fmt.Sprintf("pid %s; error_log %s; events {} http {access_log off; include %s;}", filepath.Join(dir, "nginx.pid"), filepath.Join(dir, "nginx.log"), routes)
	if err := os.WriteFile(conf, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-p", dir, "-c", conf, "-g", "daemon off;")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Signal(syscall.SIGTERM); _ = cmd.Wait() })
	for i := 0; ; i++ {
		conn, err := net.DialTimeout("tcp", nginxAddress, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if i == 80 {
			t.Fatal("nginx did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	newHandler := func(m *peers.Manager, a *shared.Authority, source ingress.Source) *ingress.Handler {
		h, err := ingress.New(source, identityStub{}, "http://"+nginxAddress, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(h.Close)
		if err := h.ConfigureShared(a, m); err != nil {
			t.Fatal(err)
		}
		return h
	}
	selectorHandler := newHandler(selector, sa, gatewaySource{})
	routingHandler := newHandler(router, ra, registry)
	gateway := httptest.NewServer(newHandler(replica, ga, gatewaySource{}))
	t.Cleanup(gateway.Close)
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	requestAt := func(serverURL, method, path, address string) *http.Response {
		r, _ := http.NewRequest(method, serverURL+path, strings.NewReader("payload"))
		r.Host = "api.example.test"
		r.Header.Set("X-Forwarded-For", address)
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("Origin", "https://api.example.test")
		r.Header.Set("Cookie", "session=fixture")
		r.Header.Set("Authorization", "Bearer fixture")
		r.Header.Set("X-Rementor-Shared-Request", "forged")
		r.Header.Set("Tailscale-User-Login", "forged")
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	request := func(method, path, address string) *http.Response {
		return requestAt(gateway.URL, method, path, address)
	}
	assertResponse := func(record shared.Record, ip, label string) {
		response := request("POST", "/orders/42?q=one%2Btwo", ip)
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("shared request: %d %s", response.StatusCode, body)
		}
		if err := shared.CheckProof(response.Header, record); err != nil {
			t.Fatal("missing captured shared proof", err)
		}
		if response.Header.Get("Access-Control-Allow-Origin") != "https://api.example.test" || len(response.Header.Values("Access-Control-Allow-Origin")) != 1 || response.Header.Get("Set-Cookie") != "session=fixture; Secure; HttpOnly; SameSite=None" {
			t.Fatal("CORS or cookie changed", response.Header)
		}
		var body map[string]string
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["application"] != label || body["body"] != "payload" || body["query"] != "q=one%2Btwo" || body["forwardedHost"] != "api.example.test" || body["proto"] != "https" || body["cookie"] != "session=fixture" || body["authorization"] != "Bearer fixture" {
			t.Fatalf("shared forwarding changed request: %#v", body)
		}
	}
	assertResponse(before, "100.82.17.7", "dev")
	assertResponse(before, "100.74.196.105", "branch")
	assertResponse(before, "100.110.60.50", "dev")
	response := request("GET", "/orders/login", "100.82.17.7")
	if response.StatusCode != http.StatusFound || response.Header.Get("Location") != "https://api.example.test/orders/callback" {
		t.Fatal("redirect changed", response.StatusCode, response.Header)
	}
	response.Body.Close()
	response = request("GET", ingress.ManifestPath, "100.74.196.105")
	var manifest access.Manifest
	if err := json.NewDecoder(response.Body).Decode(&manifest); err != nil || manifest.WorkspaceID != "branch" || manifest.Revision != before.Version {
		t.Fatal("runtime used gateway/routing-host private selection", manifest, err)
	}
	response.Body.Close()
	response = request("OPTIONS", "/orders/42", "100.82.17.7")
	if response.StatusCode != http.StatusNoContent {
		t.Fatal("preflight failed", response.StatusCode)
	}
	response.Body.Close()

	openSocket := func() *websocket.Conn {
		wsURL := "ws" + strings.TrimPrefix(gateway.URL, "http") + "/orders/socket"
		cfg, err := websocket.NewConfig(wsURL, "https://api.example.test")
		if err != nil {
			t.Fatal(err)
		}
		cfg.Location.Host = "api.example.test"
		cfg.Header.Set("Host", "api.example.test")
		cfg.Header.Set("X-Forwarded-For", "100.110.60.50")
		cfg.Header.Set("X-Forwarded-Proto", "https")
		tcp, err := net.DialTimeout("tcp", strings.TrimPrefix(gateway.URL, "http://"), 3*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		conn, err := websocket.NewClient(cfg, tcp)
		if err != nil {
			tcp.Close()
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		if err := websocket.Message.Send(conn, "hello"); err != nil {
			t.Fatal(err)
		}
		var value string
		if err := websocket.Message.Receive(conn, &value); err != nil || value != "dev:hello" {
			t.Fatal("shared WebSocket failed", value, err)
		}
		return conn
	}
	conn := openSocket()
	if _, err := sa.Transfer(ctx, gID, before.Version, pausedSharedActivation{Peer: selector}); err == nil {
		t.Fatal("handoff interruption ignored")
	}
	after := sa.State().Record.Clone()
	previousCalls := calls.Load()
	response = request("POST", "/orders/42", "100.82.17.7")
	if response.StatusCode == http.StatusOK || calls.Load() != previousCalls {
		t.Fatal("prepared successor served before activation")
	}
	response.Body.Close()
	if _, err := ga.Recover(ctx, replica); err != nil {
		t.Fatal(err)
	}
	if _, err := sa.Recover(ctx, selector); err != nil {
		t.Fatal(err)
	}
	if after.RoutingOwner != rID || after.Owner != gID {
		t.Fatal("transfer changed routing computer")
	}
	var value string
	if err := websocket.Message.Receive(conn, &value); err == nil {
		t.Fatal("transfer did not close old WebSocket")
	}
	assertResponse(after, "100.82.17.7", "dev")
	assertResponse(after, "100.74.196.105", "branch")
	// Reopen the new selector's pinned identity and durable shared record. The
	// routing computer and other gateway retain their original configuration.
	replica.Close()
	gateway.Close()
	replica, err = peers.Open(managerPaths[gID], "ignored-name")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(replica.Close)
	ga, err = shared.Open(gID, stores[gID])
	if err != nil {
		t.Fatal(err)
	}
	replica.SetSharedAuthority(ga)
	replica.Restore()
	gateway = httptest.NewServer(newHandler(replica, ga, gatewaySource{}))
	t.Cleanup(gateway.Close)
	assertResponse(after, "100.82.17.7", "dev")
	conn = openSocket()
	if err := replica.Revoke(rID); err != nil {
		t.Fatal(err)
	}
	if err := websocket.Message.Receive(conn, &value); err == nil {
		t.Fatal("revocation did not close shared WebSocket")
	}
	previous := calls.Load()
	response = request("POST", "/orders/42", "100.82.17.7")
	if response.StatusCode == http.StatusOK || calls.Load() != previous {
		t.Fatal("revocation served or replayed POST through another computer")
	}
	response.Body.Close()
	pair(replica, router)
	assertResponse(after, "100.82.17.7", "dev")
	router.SetSharedIngress(staleSharedResponse{})
	response = request("GET", "/orders/42", "100.82.17.7")
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusConflict || strings.Contains(string(body), "unproved application body") {
		t.Fatal("stale response accepted", response.StatusCode, string(body))
	}
	router.SetSharedIngress(routingHandler)
	// An unavailable selector cannot be replaced by a gateway's local workspace.
	formerGateway := httptest.NewServer(selectorHandler)
	t.Cleanup(formerGateway.Close)
	assertResponse(after, "100.82.17.7", "dev")
	previous = calls.Load()
	replica.Close()
	response = requestAt(formerGateway.URL, "POST", "/orders/42", "100.82.17.7")
	if response.StatusCode == http.StatusOK || calls.Load() != previous {
		t.Fatal("offline owner silently fell back")
	}
	response.Body.Close()
}
