package nginx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/thiagojdb/rementor/internal/fleet"
	"github.com/thiagojdb/rementor/internal/models"
	"github.com/thiagojdb/rementor/internal/peers"
	"golang.org/x/net/websocket"
)

type gatewaySource struct {
	mu      sync.Mutex
	intent  fleet.WorkspaceIntent
	version uint64
}

type handshakeCapture struct {
	net.Conn
	response bytes.Buffer
}

func (c *handshakeCapture) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.response.Write(p[:n])
	return n, err
}

func (s *gatewaySource) ExportPeerWorkspaces() []fleet.WorkspaceSummary { return nil }
func (s *gatewaySource) ExportPeerWorkspace(id string) (fleet.WorkspaceIntent, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.intent.Scope.WorkspaceID {
		return fleet.WorkspaceIntent{}, 0, fleet.ErrWorkspaceNotFound
	}
	return s.intent, s.version, nil
}

func TestNginxPeerGatewayRoutesHTTPWebSocketsAndFailures(t *testing.T) {
	binary, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx is required")
	}
	var calls atomic.Int32
	mux := http.NewServeMux()
	socketHandler := websocket.Handler(func(conn *websocket.Conn) {
		defer conn.Close()
		for {
			var value string
			if websocket.Message.Receive(conn, &value) != nil {
				return
			}
			if websocket.Message.Send(conn, value) != nil {
				return
			}
		}
	})
	mux.HandleFunc("/service-orders/socket", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" || !strings.EqualFold(r.Header.Get("Connection"), "upgrade") {
			t.Error("gateway lost websocket upgrade headers")
		}
		socketHandler.ServeHTTP(w, r)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("X-Rementor-Endpoint-Owner", "spoof")
		w.Header().Set("Set-Cookie", "test-session=value; HttpOnly; SameSite=Lax")
		w.Header().Set("Location", "http://login.localhost/callback")
		if r.URL.Path == "/service-orders/42" {
			if r.Method != http.MethodPost || r.URL.RawQuery != "a=one%2Btwo&b=2" || r.Header.Get("Origin") != "http://frontend.localhost" || r.Header.Get("Cookie") != "test-session=value" || r.Header.Get("Authorization") != "Bearer test-value" {
				t.Error("gateway changed application request")
			}
			if r.Host != "localhost" || r.Header.Get("X-Forwarded-Host") != "localhost" || r.Header.Get("X-Forwarded-Proto") != "http" || r.Header.Get("X-Rementor-Peer-Endpoint") != "" {
				t.Error("gateway changed local header policy or leaked internal metadata")
			}
			_, _ = io.Copy(w, r.Body)
			return
		}
		_, _ = io.WriteString(w, r.URL.Path)
	})
	upstream := httptest.NewServer(mux)
	t.Cleanup(upstream.Close)
	_, upstreamPortText, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	upstreamPort, _ := strconv.Atoi(upstreamPortText)
	newManager := func(name string) *peers.Manager {
		m, err := peers.Open(filepath.Join(t.TempDir(), name), name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(m.Close)
		address := fmt.Sprintf("127.0.0.1:%d", proofFreeTCPPort(t))
		if err := m.Configure(address, "https://"+address); err != nil {
			t.Fatal(err)
		}
		return m
	}
	receiver, owner := newManager("receiver"), newManager("owner")
	source := &gatewaySource{version: 7, intent: fleet.WorkspaceIntent{Scope: fleet.Scope{WorkspaceID: "dev", EnvironmentID: "dev"}, Type: models.WorkspaceTypeRouting, Routing: models.RoutingConfig{LocalDomain: "dev.localhost"}}}
	for _, id := range []string{"orders", "billing", "search", "reports", "frontend"} {
		source.intent.Applications = append(source.intent.Applications, models.ApplicationConfig{ID: id, Path: "/" + id, Port: upstreamPort, Active: true})
	}
	owner.SetWorkspaceSource(source)
	code, invitation, err := owner.CreateInvitation()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := receiver.Pair(context.Background(), code, invitation.Fingerprint); err != nil {
		t.Fatal(err)
	}
	snapshot, err := receiver.FetchWorkspace(context.Background(), invitation.Fingerprint, "dev", "")
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(receiver.Gateway())
	t.Cleanup(gateway.Close)
	gatewayPort := strings.Split(gateway.URL, ":")[2]
	t.Setenv("REMENTOR_PEER_GATEWAY_PORT", gatewayPort)
	nginxPort := proofFreeTCPPort(t)
	t.Setenv("REMENTOR_NGINX_LISTEN_HOST", "127.0.0.1")
	t.Setenv("REMENTOR_NGINX_LISTEN_PORTS", strconv.Itoa(nginxPort))
	ws := &models.Workspace{WorkspaceID: "following", Type: models.WorkspaceTypeRouting, RoutingConfig: &models.RoutingConfig{LocalDomain: "follow.localhost", DefaultRemoteBaseURL: upstream.URL}}
	for _, id := range []string{"orders", "billing", "search", "reports"} {
		ref := snapshot.Endpoint(id)
		ws.Applications = append(ws.Applications, &models.Application{ID: id, Path: "/" + id, PublicPath: "/" + id, UpstreamContext: "/service-" + id, Active: true, PeerEndpoint: &ref})
	}
	ref := snapshot.Endpoint("frontend")
	ws.Applications = append(ws.Applications, &models.Application{ID: "frontend", Domain: "frontend.localhost", Path: "/", PublicPath: "/", UpstreamContext: "/console", Active: true, PeerEndpoint: &ref})
	config, err := RenderConfig([]*models.Workspace{ws}, "rementor.localhost")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "nginx.conf")
	main := fmt.Sprintf("pid %s;\nerror_log %s;\nevents {}\nhttp { access_log off;\n%s\n}\n", filepath.Join(dir, "nginx.pid"), filepath.Join(dir, "error.log"), config)
	if err := os.WriteFile(mainPath, []byte(main), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-p", dir, "-c", mainPath, "-g", "daemon off;")
	if output, err := exec.Command(binary, "-p", dir, "-c", mainPath, "-t").CombinedOutput(); err != nil {
		t.Fatalf("invalid peer gateway config: %v %s", err, output)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Signal(syscall.SIGTERM); _ = cmd.Wait() })
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{DisableKeepAlives: true}}
	base := fmt.Sprintf("http://127.0.0.1:%d", nginxPort)
	for attempts := 0; attempts < 80; attempts++ {
		resp, err := client.Get(base + "/__rementor/config-ready")
		if err == nil {
			resp.Body.Close()
			break
		}
		if attempts == 79 {
			log, _ := os.ReadFile(filepath.Join(dir, "error.log"))
			t.Fatalf("nginx not ready: %s", log)
		}
		time.Sleep(25 * time.Millisecond)
	}
	request := func(method, path, host string) *http.Response {
		req, _ := http.NewRequest(method, base+path, strings.NewReader("body"))
		req.Host = host
		for key, value := range map[string]string{"Origin": "http://frontend.localhost", "Authorization": "Bearer test-value", "Cookie": "test-session=value", "X-Rementor-Peer-Endpoint": "spoof", "X-Forwarded-Proto": "spoof"} {
			req.Header.Set(key, value)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	assertProof := func(resp *http.Response, id string, wantStatus int) {
		if resp.StatusCode != wantStatus || resp.Header.Get("X-Rementor-Endpoint-Owner") != invitation.Fingerprint || resp.Header.Get("X-Rementor-Endpoint-App-ID") != id || resp.Header.Get("X-Rementor-Workspace") != "following" || resp.Header.Get("X-Rementor-Endpoint-Version") != "7" || resp.Header.Get("X-Rementor-Endpoint-Digest") != snapshot.Digest {
			t.Fatalf("incorrect gateway response/proof: %d %v", resp.StatusCode, resp.Header)
		}
	}
	resp := request(http.MethodPost, "/orders/42?a=one%2Btwo&b=2", "follow.localhost")
	assertProof(resp, "orders", 200)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "body" || resp.Header.Get("Set-Cookie") == "" || resp.Header.Get("Location") != "http://login.localhost/callback" || resp.Header.Get("Access-Control-Allow-Origin") != "http://frontend.localhost" {
		t.Fatal("gateway lost body, cookies, redirect or CORS")
	}
	for _, id := range []string{"billing", "search", "reports"} {
		resp := request(http.MethodGet, "/"+id+"/42", "follow.localhost")
		assertProof(resp, id, 200)
		resp.Body.Close()
	}
	resp = request(http.MethodGet, "/console/ping", "frontend.localhost")
	assertProof(resp, "frontend", 200)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "/ping" {
		t.Fatalf("frontend context stripping changed: %s", body)
	}
	wsConfig, err := websocket.NewConfig(fmt.Sprintf("ws://follow.localhost:%d/orders/socket", nginxPort), "http://frontend.localhost")
	if err != nil {
		t.Fatal(err)
	}
	socket, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", nginxPort), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	capture := &handshakeCapture{Conn: socket}
	conn, err := websocket.NewClient(wsConfig, capture)
	if err != nil {
		_ = socket.Close()
		t.Fatalf("websocket handshake: %v %s", err, capture.response.String())
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err := websocket.Message.Send(conn, "through nginx"); err != nil {
		t.Fatal(err)
	}
	var reply string
	if err := websocket.Message.Receive(conn, &reply); err != nil || reply != "through nginx" {
		t.Fatalf("gateway websocket: %v %q", err, reply)
	}
	source.mu.Lock()
	source.version++
	source.mu.Unlock()
	before := calls.Load()
	resp = request(http.MethodGet, "/orders/42", "follow.localhost")
	assertProof(resp, "orders", 409)
	resp.Body.Close()
	if calls.Load() != before {
		t.Fatal("stale target reached upstream")
	}
	source.mu.Lock()
	source.version--
	source.mu.Unlock()
	if err := owner.Revoke(receiver.State().Fingerprint); err != nil {
		t.Fatal(err)
	}
	if err := websocket.Message.Receive(conn, &reply); err == nil {
		t.Fatal("revoked websocket stayed open")
	} else if timeout, ok := err.(interface{ Timeout() bool }); ok && timeout.Timeout() {
		t.Fatal("revoked socket survived until deadline")
	}
	resp = request(http.MethodGet, "/orders/42", "follow.localhost")
	assertProof(resp, "orders", 403)
	resp.Body.Close()
	code, invitation, err = owner.CreateInvitation()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := receiver.Pair(context.Background(), code, invitation.Fingerprint); err != nil {
		t.Fatal(err)
	}
	owner.SetWorkspaceSource(nil)
	resp = request(http.MethodGet, "/orders/42", "follow.localhost")
	assertProof(resp, "orders", 503)
	resp.Body.Close()
	owner.SetWorkspaceSource(source)
	upstream.Close()
	resp = request(http.MethodGet, "/orders/42", "follow.localhost")
	assertProof(resp, "orders", 502)
	resp.Body.Close()
	gateway.Close()
	resp = request(http.MethodGet, "/orders/42", "follow.localhost")
	assertProof(resp, "orders", 502)
	resp.Body.Close()
}
