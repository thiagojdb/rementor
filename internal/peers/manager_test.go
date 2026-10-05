package peers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testManager(t *testing.T, name string) *Manager {
	t.Helper()
	m, err := Open(filepath.Join(t.TempDir(), name), name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listen := reservation.Addr().String()
	_ = reservation.Close()
	if err = m.Configure(listen, "https://"+listen); err != nil {
		t.Fatal(err)
	}
	return m
}
func invite(t *testing.T, m *Manager) (string, Invitation) {
	t.Helper()
	code, i, err := m.CreateInvitation()
	if err != nil {
		t.Fatal(err)
	}
	return code, i
}
func encodeInvitation(i Invitation) string {
	raw, _ := json.Marshal(i)
	return "rementor:" + base64.RawURLEncoding.EncodeToString(raw)
}
func status(t *testing.T, client, server *Manager, path string) int {
	t.Helper()
	httpClient := client.client(server.State().Fingerprint)
	defer httpClient.CloseIdleConnections()
	response, err := httpClient.Get(server.State().URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	return response.StatusCode
}
func TestEnrollmentTrustPersistenceAndRevocation(t *testing.T) {
	a, b, stranger := testManager(t, "marcus"), testManager(t, "brutus"), testManager(t, "stranger")
	if got := status(t, a, b, "/v1/status"); got != http.StatusForbidden {
		t.Fatalf("unpaired client received %d", got)
	}
	code, i := invite(t, b)
	if _, err := a.Pair(context.Background(), code, "wrong"); err == nil {
		t.Fatal("paired without identity confirmation")
	}
	p, err := a.Pair(context.Background(), code, i.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "brutus" || len(a.State().Peers) != 1 || len(b.State().Peers) != 1 {
		t.Fatal("mutual trust was not established")
	}
	if err = a.Check(context.Background(), p.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if err = b.Check(context.Background(), a.State().Fingerprint); err != nil {
		t.Fatal(err)
	}
	if got := status(t, stranger, b, "/v1/status"); got != http.StatusForbidden {
		t.Fatalf("stranger received %d", got)
	}
	if got := status(t, a, b, "/rpc/rementor.v1.ControlPlaneService/SaveFleetConfiguration"); got != http.StatusNotFound {
		t.Fatalf("peer exposed management RPC: %d", got)
	}
	if _, err = stranger.Pair(context.Background(), code, i.Fingerprint); err == nil {
		t.Fatal("reused single-use invitation")
	}
	// Identity and trust survive a new manager instance; no invitation survives.
	directory := filepath.Dir(b.path)
	before := b.State()
	b.Close()
	restored, err := Open(directory, "ignored-new-hostname")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restored.Close)
	restored.Restore()
	if restored.State().Fingerprint != before.Fingerprint || restored.State().Name != "brutus" || len(restored.State().Peers) != 1 {
		t.Fatal("lost persisted identity/trust")
	}
	if err = a.Check(context.Background(), p.Fingerprint); err != nil {
		t.Fatalf("trust lost after restart: %v", err)
	}
	if _, err = stranger.Pair(context.Background(), code, i.Fingerprint); err == nil {
		t.Fatal("invitation survived restart")
	}
	if err = restored.Revoke(a.State().Fingerprint); err != nil {
		t.Fatal(err)
	}
	if err = a.Check(context.Background(), p.Fingerprint); err == nil {
		t.Fatal("revoked client still connected")
	}
	info, err := os.Stat(restored.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("identity permissions: %v %v", info, err)
	}
	info, err = os.Stat(directory)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("directory permissions: %v %v", info, err)
	}
}
func TestInvitationExpiryCancellationReplacementAndIdentityPin(t *testing.T) {
	a, b := testManager(t, "a"), testManager(t, "b")
	code, i := invite(t, b)
	tampered := i
	tampered.Fingerprint = strings.Repeat("0", 64)
	if _, err := a.Pair(context.Background(), encodeInvitation(tampered), tampered.Fingerprint); err == nil {
		t.Fatal("accepted a different server certificate")
	}
	if len(b.State().Peers) != 0 {
		t.Fatal("failed identity verification sent enrollment credentials")
	}
	// The legitimate invite remains usable after a rejected connection.
	if _, err := a.Pair(context.Background(), code, i.Fingerprint); err != nil {
		t.Fatal(err)
	}
	code, i = invite(t, b)
	b.CancelInvitation()
	if _, err := a.Pair(context.Background(), code, i.Fingerprint); err == nil {
		t.Fatal("accepted cancelled invitation")
	}
	code, i = invite(t, b)
	_, _ = invite(t, b)
	if _, err := a.Pair(context.Background(), code, i.Fingerprint); err == nil {
		t.Fatal("accepted replaced invitation")
	}
	code, i = invite(t, b)
	if _, err := ParseInvitation(code, i.ExpiresAt); err == nil {
		t.Fatal("accepted expired invite")
	}
	b.mu.Lock()
	b.inviteExpires = time.Now().Add(-time.Second)
	b.mu.Unlock()
	if _, err := a.Pair(context.Background(), code, i.Fingerprint); err == nil {
		t.Fatal("server accepted expired invite")
	}
}
func TestPeerListenerOptInAndTLSClientCertificate(t *testing.T) {
	m := testManager(t, "private")
	listen, advertised := m.State().Listen, m.State().URL
	for _, input := range []struct{ listen, url string }{{"0.0.0.0:9443", advertised}, {"8.8.8.8:9443", advertised}, {listen, "http://host:9443"}, {listen, "https://user:secret@host:9443"}, {listen, "https://host:9443/path"}, {listen, "https://host:9443?token=secret"}} {
		if err := m.Configure(input.listen, input.url); err == nil {
			t.Fatalf("accepted unsafe connection settings: %v", input)
		}
	}
	// The pinned client without our identity certificate cannot complete TLS.
	client := m.client(m.State().Fingerprint)
	client.Transport.(*http.Transport).TLSClientConfig.Certificates = nil
	response, err := client.Get(advertised + "/v1/status")
	if response != nil {
		response.Body.Close()
	}
	client.CloseIdleConnections()
	if err == nil {
		t.Fatal("accepted connection without client certificate")
	}
	code, i := invite(t, m)
	if _, err = m.Pair(context.Background(), code, i.Fingerprint); err == nil {
		t.Fatal("paired with self")
	}
	if err = m.Configure("", ""); err != nil {
		t.Fatal(err)
	}
	if m.State().Active {
		t.Fatal("listener still active")
	}
	restored, err := Open(filepath.Dir(m.path), "ignored")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restored.Close)
	restored.Restore()
	if restored.State().Active {
		t.Fatal("disabled listener restored active")
	}
}

func TestConcurrentEnrollmentConsumesInvitationOnce(t *testing.T) {
	server, a, b := testManager(t, "server"), testManager(t, "a"), testManager(t, "b")
	code, i := invite(t, server)
	invalid := i
	invalid.Token = strings.Repeat("0", 64)
	if _, err := a.Pair(context.Background(), encodeInvitation(invalid), i.Fingerprint); err == nil {
		t.Fatal("accepted wrong token")
	}
	results := make(chan error, 2)
	for _, client := range []*Manager{a, b} {
		go func(m *Manager) { _, err := m.Pair(context.Background(), code, i.Fingerprint); results <- err }(client)
	}
	successes := 0
	for range 2 {
		if <-results == nil {
			successes++
		}
	}
	if successes != 1 || len(server.State().Peers) != 1 {
		t.Fatal("invitation authorized multiple peers")
	}
}
