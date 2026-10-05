package ingress

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServeSourceRejectsForgedOrAmbiguousBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, remote       string
		sources, protocols []string
		ok                 bool
	}{
		{"valid", "127.0.0.1:9302", []string{"100.82.17.7"}, []string{"https"}, true},
		{"v6", "[::1]:9302", []string{"fd7a:115c:a1e0::1"}, []string{"https"}, true},
		{"network caller", "100.82.17.7:1234", []string{"100.82.17.7"}, []string{"https"}, false},
		{"chain", "127.0.0.1:1234", []string{"100.82.17.7,100.74.196.105"}, []string{"https"}, false},
		{"duplicate", "127.0.0.1:1234", []string{"100.82.17.7", "100.74.196.105"}, []string{"https"}, false},
		{"space", "127.0.0.1:1234", []string{" 100.82.17.7"}, []string{"https"}, false},
		{"public", "127.0.0.1:1234", []string{"8.8.8.8"}, []string{"https"}, false},
		{"lan", "127.0.0.1:1234", []string{"192.168.1.1"}, []string{"https"}, false},
		{"missing", "127.0.0.1:1234", nil, []string{"https"}, false},
		{"http", "127.0.0.1:1234", []string{"100.82.17.7"}, []string{"http"}, false},
		{"duplicate proto", "127.0.0.1:1234", []string{"100.82.17.7"}, []string{"https", "https"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "https://api.example.test/", nil)
			r.RemoteAddr = tc.remote
			r.Header["X-Forwarded-For"] = tc.sources
			r.Header["X-Forwarded-Proto"] = tc.protocols
			r.Header.Set("Tailscale-User-Login", "forged")
			_, err := SourceAddress(r)
			if (err == nil) != tc.ok {
				t.Fatal(err)
			}
		})
	}
}

func TestWhoIsUsesAuthorizedExactSourceNode(t *testing.T) {
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	for _, tc := range []struct {
		name, body string
		status     int
		ok         bool
	}{
		{"human", fmt.Sprintf(`{"Node":{"StableID":"device-a","User":99,"MachineAuthorized":true,"KeyExpiry":%q,"Addresses":["100.82.17.7/32"]}}`, future), 200, true},
		{"peer omitted", fmt.Sprintf(`{"Node":{"StableID":"device-a","User":99,"KeyExpiry":%q,"Addresses":["100.82.17.7/32"]}}`, future), 200, true},
		{"expired flag", fmt.Sprintf(`{"Node":{"StableID":"device-a","User":99,"MachineAuthorized":true,"Expired":true,"KeyExpiry":%q,"Addresses":["100.82.17.7/32"]}}`, future), 200, false},
		{"tagged", `{"Node":{"StableID":"device-a","Tags":["tag:gateway"],"MachineAuthorized":true,"Addresses":["100.82.17.7/32"]}}`, 200, true},
		{"wrong address", `{"Node":{"StableID":"device-a","User":99,"MachineAuthorized":true,"Addresses":["100.82.17.8/32"]}}`, 200, false},
		{"revoked", `{"Node":{"StableID":"device-a","MachineAuthorized":false,"Addresses":["100.82.17.7/32"]}}`, 200, false},
		{"expired", fmt.Sprintf(`{"Node":{"StableID":"device-a","MachineAuthorized":true,"KeyExpiry":%q,"Addresses":["100.82.17.7/32"]}}`, past), 200, false},
		{"missing node", `{}`, 200, false},
		{"unknown", `{}`, 404, false},
		{"oversized", `{"Node":{"StableID":"device-a","MachineAuthorized":true,"Addresses":["100.82.17.7/32"]},"extra":"` + strings.Repeat("x", 1<<20) + `"}`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := filepath.Join(t.TempDir(), "tailscale.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/localapi/v0/whois" || r.URL.Query().Get("addr") != "100.82.17.7" {
					t.Error("wrong identity query")
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			server.Listener = listener
			server.Start()
			defer server.Close()
			resolver := NewTailscaleIdentityResolver(socket)
			defer resolver.client.CloseIdleConnections()
			identity, err := resolver.Resolve(context.Background(), "100.82.17.7")
			if (err == nil) != tc.ok {
				t.Fatalf("identity=%+v error=%v", identity, err)
			}
			if tc.ok && identity.NodeID != "device-a" {
				t.Fatal("lost stable identity")
			}
			if _, err := resolver.Resolve(context.Background(), "192.168.1.1"); err == nil {
				t.Fatal("queried LAN address as Tailscale identity")
			}
		})
	}
}
