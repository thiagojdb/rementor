package main

import (
	"context"
	"flag"
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
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/thiagojdb/rementor/internal/config"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/gen/rementor/v1/rementorv1connect"
	"github.com/thiagojdb/rementor/internal/models"
	"github.com/thiagojdb/rementor/internal/peers"
)

func TestIngressGuardDaemonProcess(t *testing.T) {
	if os.Getenv("REMENTOR_TEST_INGRESS_GUARD") != "1" {
		return
	}
	flag.CommandLine = flag.NewFlagSet("rementor-test-daemon", flag.ExitOnError)
	os.Args = []string{"rementor", "--host", "127.0.0.1", "--port", os.Getenv("REMENTOR_TEST_PORT")}
	main()
}

func reserveIngressTestPorts(t *testing.T) (int, []net.Listener) {
	t.Helper()
	for attempt := 0; attempt < 64; attempt++ {
		first, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := first.Addr().(*net.TCPAddr).Port
		listeners := []net.Listener{first}
		for offset := 1; offset < 4 && port+offset <= 65535; offset++ {
			listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port+offset))
			if err != nil {
				break
			}
			listeners = append(listeners, listener)
		}
		if len(listeners) == 4 {
			return port, listeners
		}
		for _, listener := range listeners {
			listener.Close()
		}
	}
	t.Fatal("could not reserve consecutive daemon listener ports")
	return 0, nil
}

func TestStartupKeepsAllIngressListenersProtectedFromPeerForwarding(t *testing.T) {
	port, reservations := reserveIngressTestPorts(t)
	for _, listener := range reservations {
		defer listener.Close()
	}
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	dist := filepath.Join(root, "frontend")
	if err := os.MkdirAll(dist, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte(`<meta name="rementor-csrf" content="%REMENTOR_CSRF_TOKEN%">`), 0600); err != nil {
		t.Fatal(err)
	}
	ws := models.WorkspaceConfig{ID: "guard", Name: "Listener guard", Type: models.WorkspaceTypeRouting, Routing: models.RoutingConfig{LocalDomain: "guard.localhost", DefaultRemoteBaseURL: "https://remote.example.test"}}
	for offset, name := range map[int]string{0: "management", 1: "peer-gateway", 3: "nginx-ingress"} {
		ws.Applications = append(ws.Applications, models.ApplicationConfig{ID: name, AppID: name, ServiceID: name, Name: name, Path: "/" + name, Port: port + offset})
	}
	if err := config.ReplaceWorkspaces([]*models.Workspace{config.WorkspaceFromConfig(ws)}); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.Create(filepath.Join(root, "daemon.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	process := exec.Command(os.Args[0], "-test.run=^TestIngressGuardDaemonProcess$")
	process.Env = append(os.Environ(), "REMENTOR_TEST_INGRESS_GUARD=1", "REMENTOR_TEST_PORT="+strconv.Itoa(port), "REMENTOR_FRONTEND_DIST="+dist, "XDG_CONFIG_HOME="+filepath.Join(root, "config"), "XDG_CACHE_HOME="+filepath.Join(root, "cache"), "REMENTOR_NGINX_BINARY="+filepath.Join(root, "missing-nginx"), "REMENTOR_NGINX_CONF_DIR="+filepath.Join(root, "nginx"), "REMENTOR_NGINX_LISTEN_PORTS="+strconv.Itoa(port+3), "REMENTOR_PUBLIC_NGINX_ADDRESS=http://127.0.0.1:"+strconv.Itoa(port+3), "REMENTOR_PEER_GATEWAY_PORT="+strconv.Itoa(port+1), "REMENTOR_PUBLIC_INGRESS_PORT="+strconv.Itoa(port+2), "NOTIFY_SOCKET=")
	process.Stdout, process.Stderr = logFile, logFile
	for _, listener := range reservations {
		listener.Close()
	}
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- process.Wait() }()
	t.Cleanup(func() {
		_ = process.Process.Signal(syscall.SIGTERM)
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			_ = process.Process.Kill()
			<-finished
		}
	})
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: time.Second}
	ready := false
	for attempt := 0; attempt < 80; attempt++ {
		response, err := client.Get(base + "/healthz")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK || response.StatusCode == http.StatusNoContent {
				ready = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		log, _ := os.ReadFile(logFile.Name())
		t.Fatalf("isolated daemon did not start: %s", log)
	}
	// A saved publishing port must not start a retired public listener.
	retired, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port+2))
	if err != nil {
		t.Fatalf("retired public listener is still running: %v", err)
	}
	retired.Close()
	for _, procedure := range []string{"GetTailnetPublishing", "GetTailscaleSharing", "GetPublicAccess", "GetSharedIngress"} {
		response, err := client.Post(base+"/rpc/rementor.v1.ControlPlaneService/"+procedure, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("retired %s remains callable: HTTP %d", procedure, response.StatusCode)
		}
	}
	api := rementorv1connect.NewControlPlaneServiceClient(client, base+"/rpc")
	peerReservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	peerAddress := peerReservation.Addr().String()
	peerReservation.Close()
	if _, err := api.ConfigurePeerConnection(context.Background(), connect.NewRequest(&rementorv1.ConfigurePeerConnectionRequest{ListenAddress: peerAddress, Url: "https://" + peerAddress})); err != nil {
		t.Fatal(err)
	}
	invitation, err := api.CreatePeerInvitation(context.Background(), connect.NewRequest(&rementorv1.CreatePeerInvitationRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	caller, err := peers.Open(filepath.Join(root, "caller"), "caller")
	if err != nil {
		t.Fatal(err)
	}
	defer caller.Close()
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := reservation.Addr().String()
	reservation.Close()
	if err := caller.Configure(address, "https://"+address); err != nil {
		t.Fatal(err)
	}
	owner, err := caller.Pair(context.Background(), invitation.Msg.Invitation, invitation.Msg.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := caller.FetchWorkspace(context.Background(), owner.Fingerprint, "guard", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, application := range ws.Applications {
		t.Run(application.ID, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://guard.localhost/"+application.ID, nil)
			response := httptest.NewRecorder()
			caller.Forward(response, request, snapshot.Endpoint(application.ID))
			body, _ := io.ReadAll(response.Result().Body)
			if response.Code != http.StatusForbidden || strings.TrimSpace(string(body)) != "control plane cannot be forwarded" {
				t.Fatalf("reserved %s reached a local listener: HTTP %d %q", application.ID, response.Code, body)
			}
		})
	}
}
