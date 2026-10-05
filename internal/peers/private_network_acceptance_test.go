package peers

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thiagojdb/rementor/internal/fleet"
	"github.com/thiagojdb/rementor/internal/models"
)

// Opt in with a machine's LAN/VPN address. This exercises the real interface
// instead of assuming loopback tests prove non-Tailscale network reachability.
func TestPrivateNetworkAcceptance(t *testing.T) {
	address := os.Getenv("REMENTOR_ACCEPTANCE_PRIVATE_ADDRESS")
	if address == "" {
		t.Skip("set REMENTOR_ACCEPTANCE_PRIVATE_ADDRESS to a local LAN/VPN address")
	}
	ip := net.ParseIP(address)
	if ip == nil || !ip.IsPrivate() || ip.IsLoopback() {
		t.Fatal("acceptance address must be a private LAN/VPN address, excluding Tailscale and loopback")
	}
	create := func(name string) *Manager {
		manager, err := Open(filepath.Join(t.TempDir(), name), name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(manager.Close)
		listener, err := net.Listen("tcp", net.JoinHostPort(address, "0"))
		if err != nil {
			t.Fatal(err)
		}
		bind := listener.Addr().String()
		listener.Close()
		if err := manager.Configure(bind, "https://"+bind); err != nil {
			t.Fatal(err)
		}
		return manager
	}
	owner, receiver, stranger := create("lan-owner"), create("lan-receiver"), create("unpaired-lan-computer")
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/orders/health" {
			t.Error("forwarding changed owner health path")
		}
		_, _ = io.WriteString(w, "owner application")
	}))
	defer app.Close()
	_, rawPort, _ := net.SplitHostPort(strings.TrimPrefix(app.URL, "http://"))
	port, _ := strconv.Atoi(rawPort)
	source := &snapshotSource{version: 4, intent: fleet.WorkspaceIntent{Scope: fleet.Scope{WorkspaceID: "dev", EnvironmentID: "dev"}, Type: "routing", Routing: models.RoutingConfig{LocalDomain: "dev.localhost"}, Applications: []models.ApplicationConfig{{ID: "orders", PublicPath: "/orders", Port: port, Active: true}}}}
	owner.SetWorkspaceSource(source)
	if status(t, stranger, owner, "/v1/workspaces") != http.StatusForbidden {
		t.Fatal("LAN reachability bypassed peer authorization")
	}
	code, invitation := invite(t, owner)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := receiver.Pair(ctx, code, invitation.Fingerprint); err != nil {
		t.Fatal(err)
	}
	snapshot, err := receiver.FetchWorkspace(ctx, invitation.Fingerprint, "dev", "")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := snapshot.Endpoint("orders")
	if healthy, err := receiver.ProbeEndpoint(ctx, endpoint, "/orders/health"); err != nil || !healthy {
		t.Fatalf("LAN owner forwarding failed: %v", err)
	}
	if status(t, receiver, owner, "/rpc/rementor.v1.ControlPlaneService/ListWorkspaces") != http.StatusNotFound {
		t.Fatal("LAN peer exposed management RPC")
	}
	if err := owner.Revoke(receiver.State().Fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := receiver.FetchWorkspace(ctx, invitation.Fingerprint, "dev", snapshot.Digest); err == nil {
		t.Fatal("revoked LAN peer still fetched snapshots")
	}
	if healthy, err := receiver.ProbeEndpoint(ctx, endpoint, "/orders/health"); err == nil || healthy {
		t.Fatal("revoked LAN peer still forwarded")
	}
	t.Log("Verified certificate-pinned enrollment, snapshots, owner forwarding, management separation and revocation on a non-Tailscale private interface; both instances ran on this host")
}
