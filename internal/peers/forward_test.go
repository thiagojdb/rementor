package peers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thiagojdb/rementor/internal/fleet"
	"github.com/thiagojdb/rementor/internal/models"
	"golang.org/x/net/websocket"
)

func forwardingFixture(t *testing.T, handler http.Handler) (*Manager, *Manager, *snapshotSource, fleet.EndpointRef, *httptest.Server, *httptest.Server) {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	_, portText, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	port, _ := strconv.Atoi(portText)
	client, owner := testManager(t, "client"), testManager(t, "owner")
	fixture := &snapshotSource{version: 7, intent: fleet.WorkspaceIntent{Scope: fleet.Scope{WorkspaceID: "dev", EnvironmentID: "dev"}, Type: models.WorkspaceTypeRouting, Routing: models.RoutingConfig{LocalDomain: "dev.localhost"}, Applications: []models.ApplicationConfig{{ID: "orders", Path: "/orders", Port: port, Active: true}}}}
	owner.SetWorkspaceSource(fixture)
	code, invitation := invite(t, owner)
	if _, err := client.Pair(context.Background(), code, invitation.Fingerprint); err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.FetchWorkspace(context.Background(), invitation.Fingerprint, "dev", "")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := snapshot.Endpoint("orders")
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { client.Forward(w, r, endpoint) }))
	t.Cleanup(gateway.Close)
	return client, owner, fixture, endpoint, gateway, upstream
}

func TestOwnerForwardPreservesHTTPAndRejectsStaleOrUndeclaredTargets(t *testing.T) {
	var calls atomic.Int32
	client, owner, source, endpoint, gateway, upstream := forwardingFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.RequestURI != "/orders/a%2Fb?value=a%2Bb&value=two" || r.Host != "services.giss.localhost" || r.Method != http.MethodPost {
			t.Error("forwarding changed method, escaped path, query or public host")
		}
		if r.Header.Get("Origin") != "https://frontend.giss.localhost" || r.Header.Get("Authorization") != "Bearer test-value" || r.Header.Get("Cookie") != "test-session=test-value" || r.Header.Get("X-Request-ID") != "test-request" {
			t.Error("forwarding lost application headers")
		}
		if r.Header.Get(endpointHeader) != "" || r.Header.Get("X-Rementor-App-ID") != "" || r.Header.Get("X-Forwarded-Host") != r.Host || r.Header.Get("X-Forwarded-Proto") != "http" || r.Header.Get("X-Forwarded-For") == "spoofed" {
			t.Error("forwarding leaked internal headers or trusted browser spoofing")
		}
		w.Header().Set("Set-Cookie", "test-session=test-value; HttpOnly; SameSite=Lax")
		w.Header().Set("Location", "https://login.giss.localhost/callback")
		w.Header().Set("X-Rementor-Endpoint-Owner", "upstream-spoof")
		w.Header().Set("X-Rementor-App-ID", "upstream-spoof")
		w.Header().Set("Access-Control-Allow-Origin", "https://frontend.giss.localhost")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.Copy(w, r.Body)
	}))
	request, _ := http.NewRequest(http.MethodPost, gateway.URL+"/orders/a%2Fb?value=a%2Bb&value=two", strings.NewReader("request body"))
	request.Host = "services.giss.localhost"
	for name, value := range map[string]string{"Origin": "https://frontend.giss.localhost", "Authorization": "Bearer test-value", "Cookie": "test-session=test-value", "X-Request-ID": "test-request", "X-Forwarded-For": "spoofed", "X-Rementor-App-ID": "caller-spoof", endpointHeader: "caller-spoof"} {
		request.Header.Set(name, value)
	}
	response, err := gateway.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusCreated || string(body) != "request body" || response.Header.Get("Location") != "https://login.giss.localhost/callback" || response.Header.Get("Set-Cookie") == "" || response.Header.Get("Access-Control-Allow-Origin") != "https://frontend.giss.localhost" {
		t.Fatal("application response changed")
	}
	if response.Header.Get("X-Rementor-Endpoint-Owner") != endpoint.OwnerFingerprint || response.Header.Get("X-Rementor-App-ID") != "" || response.Header.Get("X-Rementor-Endpoint-Digest") != endpoint.Digest {
		t.Fatal("upstream forged endpoint proof")
	}
	// Request the peer interface directly to exercise authorization without a
	// gateway overwriting an invalid reference first.
	peerClient := client.client(endpoint.OwnerFingerprint)
	defer peerClient.CloseIdleConnections()
	check := func(ref fleet.EndpointRef, want int) {
		t.Helper()
		raw, _ := json.Marshal(ref)
		req, _ := http.NewRequest(http.MethodGet, owner.State().URL+forwardPrefix+"/orders", nil)
		req.Header.Set(endpointHeader, base64.RawURLEncoding.EncodeToString(raw))
		resp, err := peerClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("expected %d, got %d", want, resp.StatusCode)
		}
	}
	missing := endpoint
	missing.ApplicationID = "undeclared"
	check(missing, http.StatusNotFound)
	wrongOwner := endpoint
	wrongOwner.OwnerFingerprint = client.State().Fingerprint
	check(wrongOwner, http.StatusBadRequest)
	owner.SetManagementPort(source.intent.Applications[0].Port)
	check(endpoint, http.StatusForbidden)
	owner.SetManagementPort(9300)
	owner.ProtectLocalPorts(source.intent.Applications[0].Port)
	check(endpoint, http.StatusForbidden)
	owner.ProtectLocalPorts()
	source.mu.Lock()
	source.intent.Applications[0].Port++ // Reassigned port must never be dialed by a stale caller.
	source.version++
	source.mu.Unlock()
	check(endpoint, http.StatusConflict)
	if calls.Load() != 1 {
		t.Fatal("invalid endpoint requests reached the application")
	}
	stranger := testManager(t, "stranger")
	if status(t, stranger, owner, forwardPrefix+"/orders") != http.StatusForbidden {
		t.Fatal("unpaired computer reached the forwarding handler")
	}
	// Restore the current reference and remove its process. There is no remote
	// fallback even when the workspace has a remote baseline.
	source.mu.Lock()
	source.intent.Applications[0].Port--
	source.version--
	source.mu.Unlock()
	upstream.Close()
	check(endpoint, http.StatusBadGateway)
}

func TestForwardWebSocketsCloseOnRevocationOrDisable(t *testing.T) {
	for _, action := range []string{"owner-revoke", "client-revoke", "owner-disable", "client-disable"} {
		t.Run(action, func(t *testing.T) {
			closed := make(chan struct{})
			client, owner, _, _, gateway, _ := forwardingFixture(t, websocket.Handler(func(conn *websocket.Conn) {
				defer close(closed)
				defer conn.Close()
				for {
					var message string
					if websocket.Message.Receive(conn, &message) != nil {
						return
					}
					if websocket.Message.Send(conn, message) != nil {
						return
					}
				}
			}))
			conn, err := websocket.Dial("ws"+strings.TrimPrefix(gateway.URL, "http")+"/orders/socket", "", "https://frontend.giss.localhost")
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			if err := websocket.Message.Send(conn, "across peers"); err != nil {
				t.Fatal(err)
			}
			var reply string
			if err := websocket.Message.Receive(conn, &reply); err != nil || reply != "across peers" {
				t.Fatalf("bidirectional websocket relay failed: %v", err)
			}
			switch action {
			case "owner-revoke":
				err = owner.Revoke(client.State().Fingerprint)
			case "client-revoke":
				err = client.Revoke(owner.State().Fingerprint)
			case "owner-disable":
				err = owner.Configure("", "")
			case "client-disable":
				err = client.Configure("", "")
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := websocket.Message.Receive(conn, &reply); err == nil {
				t.Fatal("revoked websocket remained open")
			} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				t.Fatal("revoked websocket was left open until deadline")
			}
			select {
			case <-closed:
			case <-time.After(5 * time.Second):
				t.Fatal("upstream websocket remained open after revocation")
			}
		})
	}
}

func TestForwardStreamingSurvivesManagementDeadline(t *testing.T) {
	second := make(chan struct{})
	client, owner, _, _, gateway, _ := forwardingFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-second:
			_, _ = io.WriteString(w, "data: second\n\n")
			w.(http.Flusher).Flush()
		case <-r.Context().Done():
			return
		}
		<-r.Context().Done()
	}))
	response, err := gateway.Client().Get(gateway.URL + "/orders/events")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	first := make([]byte, len("data: first\n\n"))
	if _, err := io.ReadFull(response.Body, first); err != nil {
		t.Fatal(err)
	}
	// The owner server's management timeout and the client's snapshot timeout
	// are both ten seconds; an application stream must outlive them.
	time.Sleep(11 * time.Second)
	owner.mu.Lock()
	active := len(owner.forwards)
	owner.mu.Unlock()
	if active != 1 {
		t.Fatal("stream ended at the management request deadline")
	}
	close(second)
	received := make(chan error, 1)
	go func() {
		data := make([]byte, len("data: second\n\n"))
		_, err := io.ReadFull(response.Body, data)
		if err == nil && string(data) != "data: second\n\n" {
			err = io.ErrUnexpectedEOF
		}
		received <- err
	}()
	select {
	case err := <-received:
		if err != nil {
			t.Fatalf("stream failed beyond management deadline: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream stopped delivering beyond management deadline")
	}
	if err := client.Revoke(owner.State().Fingerprint); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := io.ReadAll(response.Body); done <- err }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not close after revocation")
	}
}
