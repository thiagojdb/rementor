package ingress_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/thiagojdb/rementor/internal/access"
	"github.com/thiagojdb/rementor/internal/config"
	"github.com/thiagojdb/rementor/internal/fleet"
	"github.com/thiagojdb/rementor/internal/ingress"
	"github.com/thiagojdb/rementor/internal/models"
	"github.com/thiagojdb/rementor/internal/nginx"
	"github.com/thiagojdb/rementor/internal/peers"
	"github.com/thiagojdb/rementor/internal/services"
	"golang.org/x/net/websocket"
)

type identityStub struct{}

func (identityStub) Resolve(_ context.Context, address string) (access.Identity, error) {
	switch address {
	case "100.82.17.7":
		return access.Identity{NodeID: "computer-a", UserID: 99}, nil
	case "100.74.196.105":
		return access.Identity{NodeID: "computer-b", UserID: 99}, nil
	case "100.110.60.50":
		return access.Identity{NodeID: "phone", UserID: 99}, nil
	}
	return access.Identity{NodeID: "outsider", UserID: 100}, nil
}

type noProxy struct{}

func (noProxy) IsAvailable() bool                           { return true }
func (noProxy) LoadInitialConfig([]*models.Workspace) error { return nil }
func (noProxy) Close() error                                { return nil }

type declaredSource struct{ port int }

func (declaredSource) ExportPeerWorkspaces() []fleet.WorkspaceSummary { return nil }
func (s declaredSource) ExportPeerWorkspace(string) (fleet.WorkspaceIntent, uint64, error) {
	return fleet.WorkspaceIntent{Scope: models.PeerScope{WorkspaceID: "owned", EnvironmentID: "owned"}, Type: "routing", Routing: models.RoutingConfig{LocalDomain: "owned.localhost"}, Applications: []models.ApplicationConfig{{ID: "orders", Port: s.port, Active: true, PublicPath: "/orders"}}}, 17, nil
}
func freeAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}
func serverPort(server *httptest.Server) int {
	u, _ := url.Parse(server.URL)
	p, _ := strconv.Atoi(u.Port())
	return p
}

func TestAuthenticatedIngressSelectsOwnAndPeerRoutesAtOneOrigin(t *testing.T) {
	binary, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx required")
	}
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	previousConfig := config.Config
	config.Config.RementorDomain = config.DefaultRementorDomain
	t.Cleanup(func() { config.Config = previousConfig })
	var calls atomic.Int32
	upstream := func(label string) *httptest.Server {
		mux := http.NewServeMux()
		mux.Handle("/orders/socket", websocket.Handler(func(conn *websocket.Conn) {
			defer conn.Close()
			for {
				var text string
				if websocket.Message.Receive(conn, &text) != nil {
					return
				}
				if websocket.Message.Send(conn, label+":"+text) != nil {
					return
				}
			}
		}))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if r.Header.Get(ingress.GuardHeader) != "" || r.Header.Get(ingress.PublicHostHeader) != "" || r.Header.Get("Tailscale-User-Login") != "" {
				t.Error("internal identity/guard leaked")
			}
			w.Header().Set("Set-Cookie", "session=fixture; Secure; HttpOnly; SameSite=None")
			w.Header().Set("Access-Control-Allow-Origin", "https://spoof.example.test")
			if r.URL.Path == "/orders/login" {
				w.Header().Set("Location", "https://api.example.test/orders/callback")
				w.WriteHeader(302)
				return
			}
			body, _ := io.ReadAll(r.Body)
			_ = json.NewEncoder(w).Encode(map[string]string{"owner": label, "host": r.Host, "forwardedHost": r.Header.Get("X-Forwarded-Host"), "proto": r.Header.Get("X-Forwarded-Proto"), "cookie": r.Header.Get("Cookie"), "authorization": r.Header.Get("Authorization"), "body": string(body), "query": r.URL.RawQuery})
		})
		s := httptest.NewServer(mux)
		t.Cleanup(s.Close)
		return s
	}
	own, remote := upstream("own"), upstream("peer")
	manager := func(name string) *peers.Manager {
		m, err := peers.Open(filepath.Join(t.TempDir(), name), name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(m.Close)
		addr := freeAddress(t)
		if err := m.Configure(addr, "https://"+addr); err != nil {
			t.Fatal(err)
		}
		return m
	}
	owner, receiver := manager("owner"), manager("receiver")
	owner.SetWorkspaceSource(declaredSource{port: serverPort(remote)})
	code, invitation, err := owner.CreateInvitation()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := receiver.Pair(context.Background(), code, invitation.Fingerprint); err != nil {
		t.Fatal(err)
	}
	snapshot, err := receiver.FetchWorkspace(context.Background(), invitation.Fingerprint, "owned", "")
	if err != nil {
		t.Fatal(err)
	}
	ref := snapshot.Endpoint("orders")
	gateway := httptest.NewServer(receiver.Gateway())
	t.Cleanup(gateway.Close)
	t.Setenv("REMENTOR_PEER_GATEWAY_PORT", strconv.Itoa(serverPort(gateway)))
	for _, ws := range []models.WorkspaceConfig{
		{ID: "shared", Type: "routing", Route: models.RouteState{RouteVersion: 7}, Routing: models.RoutingConfig{LocalDomain: "shared.localhost"}, Applications: []models.ApplicationConfig{{ID: "orders", PublicPath: "/orders", Active: true, Port: serverPort(own)}}},
		{ID: "branch", Type: "routing", Route: models.RouteState{RouteVersion: 8}, Routing: models.RoutingConfig{LocalDomain: "branch.localhost"}, Applications: []models.ApplicationConfig{{ID: "orders", PublicPath: "/orders", Active: true, PeerEndpoint: &ref}}},
	} {
		if err := config.AppendWorkspace(ws); err != nil {
			t.Fatal(err)
		}
	}
	registry := &services.Registry{}
	registry.SetRoutingProvider(noProxy{})
	if err := registry.Load(); err != nil {
		t.Fatal(err)
	}
	doc := access.Document{SchemaVersion: 1, Origins: []access.Origin{{ID: "api", URL: "https://api.example.test"}}, Bindings: []access.Binding{{WorkspaceID: "shared", OriginID: "api", LocalHost: "shared.localhost"}, {WorkspaceID: "branch", OriginID: "api", LocalHost: "branch.localhost"}}, SharedWorkspaceID: "shared", AllowedUserIDs: []uint64{99}, Devices: []access.Device{{NodeID: "computer-a", WorkspaceID: "shared"}, {NodeID: "computer-b", WorkspaceID: "branch"}}}
	saved, err := registry.SavePublicAccessConfiguration(doc, 0)
	if err != nil {
		t.Fatal(err)
	}
	nginxAddr := freeAddress(t)
	_, port, _ := net.SplitHostPort(nginxAddr)
	t.Setenv("REMENTOR_NGINX_LISTEN_HOST", "127.0.0.1")
	t.Setenv("REMENTOR_NGINX_LISTEN_PORTS", port)
	rendered, err := nginx.RenderConfig([]*models.Workspace{registry.GetWorkspaceView("shared"), registry.GetWorkspaceView("branch")}, "rementor.localhost")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	conf := filepath.Join(dir, "nginx.conf")
	routesPath := filepath.Join(dir, "workspaces.conf")
	if err := os.WriteFile(routesPath, []byte(rendered), 0600); err != nil {
		t.Fatal(err)
	}
	text := fmt.Sprintf("pid %s; error_log %s; events {} http {access_log off; include %s; }", filepath.Join(dir, "nginx.pid"), filepath.Join(dir, "nginx.log"), routesPath)
	if err := os.WriteFile(conf, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(binary, "-p", dir, "-c", conf, "-t").CombinedOutput(); err != nil {
		t.Fatalf("nginx config: %v %s", err, output)
	}
	cmd := exec.Command(binary, "-p", dir, "-c", conf, "-g", "daemon off;")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Signal(syscall.SIGTERM); _ = cmd.Wait() })
	for attempt := 0; attempt < 80; attempt++ {
		c, err := net.DialTimeout("tcp", nginxAddr, time.Millisecond*50)
		if err == nil {
			c.Close()
			break
		}
		if attempt == 79 {
			t.Fatal("nginx did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	wrapper := filepath.Join(dir, "nginx-wrapper")
	if err := os.WriteFile(wrapper, []byte(fmt.Sprintf("#!/bin/sh\nexec %q -p %q -c %q \"$@\"\n", binary, dir, conf)), 0700); err != nil {
		t.Fatal(err)
	}
	provider := nginx.NewRoutingProvider(dir, wrapper)
	registry.SetRoutingProvider(provider)
	if err := provider.(*nginx.RoutingProvider).ApplyRouting(registry.GetWorkspaces()); err != nil {
		t.Fatal(err)
	}
	handler, err := ingress.New(registry, identityStub{}, "http://"+nginxAddr, func(p int) bool { return p == 9300 })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(handler.Close)
	public := httptest.NewServer(handler)
	t.Cleanup(public.Close)
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(method, path, address, origin string) *http.Response {
		req, _ := http.NewRequest(method, public.URL+path, strings.NewReader("payload"))
		req.Host = "api.example.test"
		req.Header.Set("X-Forwarded-For", address)
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("Cookie", "session=fixture")
		req.Header.Set("Authorization", "Bearer fixture")
		req.Header.Set("Tailscale-User-Login", "forged")
		req.Header.Set(ingress.GuardHeader, "forged")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	for _, tc := range []struct{ ip, scope, label, source string }{{"100.82.17.7", "shared", "own", "device"}, {"100.74.196.105", "branch", "peer", "device"}, {"100.110.60.50", "shared", "own", "shared"}} {
		response := request("POST", "/orders/42?a=one%2Btwo", tc.ip, "https://api.example.test")
		var body map[string]string
		err := json.NewDecoder(response.Body).Decode(&body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || body["owner"] != tc.label || body["forwardedHost"] != "api.example.test" || body["proto"] != "https" || body["cookie"] != "session=fixture" || body["body"] != "payload" || body["query"] != "a=one%2Btwo" || body["authorization"] != "Bearer fixture" {
			t.Fatalf("lost request or scope: %d %+v %v", response.StatusCode, body, err)
		}
		if response.Header.Get("X-Rementor-Workspace") != tc.scope || response.Header.Get("X-Rementor-Access-Selection") != tc.source || response.Header.Get("Access-Control-Allow-Origin") != "https://api.example.test" || len(response.Header.Values("Access-Control-Allow-Origin")) != 1 || len(response.Header.Values("X-Rementor-Access-Revision")) != 1 || response.Header.Get("Set-Cookie") == "" {
			t.Fatal("lost proof/cookie/CORS", response.Header)
		}
		manifest := request("GET", ingress.ManifestPath, tc.ip, "")
		var result access.Manifest
		err = json.NewDecoder(manifest.Body).Decode(&result)
		manifest.Body.Close()
		if err != nil || result.WorkspaceID != tc.scope || result.Origins["api"] != "https://api.example.test" || result.WebSocketOrigins["api"] != "wss://api.example.test" || result.RoutingHosts["api"] != tc.scope+".localhost" {
			t.Fatal("runtime manifest changed public origins", result, err)
		}
	}
	startCalls := calls.Load()
	for _, tc := range []struct {
		method, path, ip, origin string
		status                   int
	}{
		{"GET", "/orders/42", "100.1.2.3", "", 403},
		{"GET", "/orders/42", "100.82.17.7", "https://foreign.example.test", 403},
		{"GET", "/__rementor/trace", "100.82.17.7", "", 404},
		{"GET", "/_rementor/trace", "100.82.17.7", "", 404},
		{"GET", "/orders/../rpc", "100.82.17.7", "", 400},
		{"OPTIONS", "/orders/42", "100.82.17.7", "https://api.example.test", 204},
	} {
		r := request(tc.method, tc.path, tc.ip, tc.origin)
		r.Body.Close()
		if r.StatusCode != tc.status {
			t.Fatalf("%s status %d", tc.path, r.StatusCode)
		}
	}
	if calls.Load() != startCalls {
		t.Fatal("rejected request reached application")
	}
	login := request("GET", "/orders/login", "100.82.17.7", "")
	login.Body.Close()
	if login.StatusCode != 302 || login.Header.Get("Location") != "https://api.example.test/orders/callback" {
		t.Fatal("lost login redirect")
	}
	// Guard must fail before contacting an application, even if the response body
	// itself would prove the route. Exercise the real nginx map and positive guard.
	for _, guard := range []string{"shared:orders:6", "branch:orders:7", "shared:other:7"} {
		r, _ := http.NewRequest("GET", "http://"+nginxAddr+"/orders/42", nil)
		r.Host = "shared.localhost"
		r.Header.Set(ingress.GuardHeader, guard)
		before := calls.Load()
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 409 || calls.Load() != before {
			t.Fatal("stale nginx guard contacted target", guard, response.StatusCode)
		}
	}
	// Both owner paths support WebSockets. Access-policy edits retire the stream.
	for _, ip := range []string{"100.82.17.7", "100.74.196.105"} {
		socketURL := "ws" + strings.TrimPrefix(public.URL, "http") + "/orders/socket"
		cfg, err := websocket.NewConfig(socketURL, "https://api.example.test")
		if err != nil {
			t.Fatal(err)
		}
		cfg.Header.Set("Host", "api.example.test")
		cfg.Header.Set("X-Forwarded-For", ip)
		cfg.Header.Set("X-Forwarded-Proto", "https")
		// x/net websocket derives Host from Location; preserve the public authority
		// while dialing the isolated listener explicitly.
		cfg.Location.Host = "api.example.test"
		conn, err := net.Dial("tcp", strings.TrimPrefix(public.URL, "http://"))
		if err != nil {
			t.Fatal(err)
		}
		socket, err := websocket.NewClient(cfg, conn)
		if err != nil {
			conn.Close()
			t.Fatal(err)
		}
		if err := websocket.Message.Send(socket, "hello"); err != nil {
			t.Fatal(err)
		}
		var text string
		socket.SetDeadline(time.Now().Add(2 * time.Second))
		if err := websocket.Message.Receive(socket, &text); err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(text, ":hello") {
			t.Fatal("lost websocket data", text)
		}
		doc.SharedWorkspaceID = "branch"
		saved, err = registry.SavePublicAccessConfiguration(doc, saved.Revision)
		if err != nil {
			t.Fatal(err)
		}
		var frame []byte
		if err := websocket.Message.Receive(socket, &frame); err == nil {
			t.Fatal("policy change kept websocket alive")
		}
		socket.Close()
	}
	// An older renderer can never silently serve an unproved selection.
	stale := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer stale.Close()
	staleHandler, err := ingress.New(registry, identityStub{}, stale.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer staleHandler.Close()
	r := httptest.NewRequest("GET", "https://api.example.test/orders", bytes.NewReader(nil))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "100.82.17.7")
	r.Header.Set("X-Forwarded-Proto", "https")
	out := httptest.NewRecorder()
	staleHandler.ServeHTTP(out, r)
	if out.Code != 409 {
		t.Fatal("unproved renderer returned success", out.Code)
	}
}
