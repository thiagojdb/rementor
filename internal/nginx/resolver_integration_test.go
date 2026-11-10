package nginx

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/thiagojdb/rementor/internal/models"
	"golang.org/x/net/dns/dnsmessage"
)

func TestRemoteDNSAtRequestTimePreservesRoutingAndRecovers(t *testing.T) {
	nginx, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx binary not available")
	}
	var recovered atomic.Bool
	dns, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer dns.Close()
	go func() {
		packet := make([]byte, 4096)
		for {
			n, addr, err := dns.ReadFrom(packet)
			if err != nil {
				return
			}
			var query dnsmessage.Message
			if query.Unpack(packet[:n]) != nil || len(query.Questions) != 1 {
				continue
			}
			q := query.Questions[0]
			response := dnsmessage.Message{Header: dnsmessage.Header{ID: query.ID, Response: true, RecursionDesired: true, RecursionAvailable: true}, Questions: query.Questions}
			if q.Name.String() == "unavailable.rementor.invalid." && !recovered.Load() {
				response.Header.RCode = dnsmessage.RCodeNameError
			} else if q.Type == dnsmessage.TypeA {
				response.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 1}, Body: &dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}}}}
			}
			data, err := response.Pack()
			if err == nil {
				_, _ = dns.WriteTo(data, addr)
			}
		}
	}()
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s|%s|%s", r.RequestURI, r.Host, r.TLS.ServerName)
	}))
	upstream.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	upstream.StartTLS()
	defer upstream.Close()
	_, backendPort, _ := net.SplitHostPort(upstream.Listener.Addr().String())
	remote := "https://healthy.rementor.invalid:" + backendPort
	port := proofFreeTCPPort(t)
	t.Setenv("REMENTOR_NGINX_LISTEN_HOST", "127.0.0.1")
	t.Setenv("REMENTOR_NGINX_LISTEN_PORTS", fmt.Sprint(port))
	ws := &models.Workspace{WorkspaceID: "dev", Type: models.WorkspaceTypeRouting,
		RoutingConfig: &models.RoutingConfig{LocalDomain: "api.localhost", DefaultRemoteBaseURL: remote},
		Applications: []*models.Application{
			{ID: "web", AppID: "web", Path: "/", Context: "/portal", RemoteBaseUrl: remote},
			{ID: "orders", AppID: "orders", PublicPath: "/orders", UpstreamContext: "/service-orders", RemoteBaseUrl: remote},
			{ID: "raw", AppID: "raw", Path: "/raw", Context: "/raw", RemoteBaseUrl: remote},
		},
	}
	unavailable := &models.Workspace{WorkspaceID: "missing", Type: models.WorkspaceTypeRouting,
		RoutingConfig: &models.RoutingConfig{LocalDomain: "missing.localhost", DefaultRemoteBaseURL: "https://unavailable.rementor.invalid:" + backendPort},
	}
	rendered, err := RenderConfig([]*models.Workspace{ws, unavailable}, "rementor.localhost")
	if err != nil {
		t.Fatal(err)
	}
	// An isolated DNS fixture replaces only the host's resolver addresses.
	rendered = regexp.MustCompile(`(?m)^resolver .*;$`).ReplaceAllString(rendered, "resolver "+dns.LocalAddr().String()+" valid=1s;")
	dir := t.TempDir()
	routes := filepath.Join(dir, "routes.conf")
	if err := os.WriteFile(routes, []byte(rendered), 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "nginx.conf")
	main := fmt.Sprintf("pid %s;\nerror_log %s;\nevents {}\nhttp { access_log off; include %s; }\n", filepath.Join(dir, "nginx.pid"), filepath.Join(dir, "error.log"), routes)
	if err := os.WriteFile(config, []byte(main), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(nginx, "-p", dir, "-c", config, "-t").CombinedOutput(); err != nil {
		t.Fatalf("DNS-dependent config validation: %v\n%s", err, out)
	}
	cmd := exec.Command(nginx, "-p", dir, "-c", config, "-g", "daemon off;")
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Signal(syscall.SIGTERM); _ = cmd.Wait() })
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	defer client.CloseIdleConnections()
	request := func(host, path string) (int, string, http.Header) {
		t.Helper()
		for i := 0; i < 80; i++ {
			req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
			req.Host = host
			response, err := client.Do(req)
			if err != nil {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			return response.StatusCode, string(body), response.Header
		}
		logs, _ := os.ReadFile(filepath.Join(dir, "error.log"))
		t.Fatalf("nginx unreachable: %s", logs)
		return 0, "", nil
	}
	for _, tc := range []struct{ path, upstream, app, mode string }{
		{"/?query=1", "/portal/home?query=1", "web", "remote"},
		{"/portal/page?query=2", "/portal/page?query=2", "web", "remote"},
		{"/orders/item?x=a%2Fb", "/service-orders/item?x=a%2Fb", "orders", "remote"},
		{"/raw/a%2Fb?x=1", "/raw/a%2Fb?x=1", "raw", "remote"},
		{"/other?x=3", "/portal/other?x=3", "web", "fallback"},
	} {
		code, body, headers := request("api.localhost", tc.path)
		want := tc.upstream + "|healthy.rementor.invalid|healthy.rementor.invalid"
		if code != 200 || body != want {
			t.Fatalf("%s: status=%d body=%q, want %q", tc.path, code, body, want)
		}
		if headers.Get("X-Rementor-App-ID") != tc.app || headers.Get("X-Rementor-Effective-Mode") != tc.mode {
			t.Fatalf("%s: incorrect proofs", tc.path)
		}
	}
	code, _, headers := request("missing.localhost", "/unavailable")
	if code != 502 || headers.Get("X-Rementor-Workspace") != "missing" || headers.Get("X-Rementor-Effective-Mode") != "fallback" {
		t.Fatalf("unavailable upstream: status=%d proof=%v", code, headers)
	}
	recovered.Store(true)
	// DNS recovery alone must restore this route, with no config apply/reload.
	deadline := time.Now().Add(4 * time.Second)
	for {
		code, body, _ := request("missing.localhost", "/recovered?q=1")
		if code == 200 {
			if !strings.HasPrefix(body, "/recovered?q=1|unavailable.rementor.invalid|") {
				t.Fatalf("recovery changed target: %q", body)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("upstream did not recover without reloading")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
