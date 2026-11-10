package peers

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func automaticManager(t *testing.T, name string) *Manager {
	t.Helper()
	m, err := Open(filepath.Join(t.TempDir(), name), name)
	if err != nil {
		t.Fatal(err)
	}
	m.autoAddresses = func() []string { return []string{"127.0.0.1"} }
	t.Cleanup(m.Close)
	return m
}

func TestInvitationAndJoiningEnableConnectionsAndPersistSettings(t *testing.T) {
	source, target := automaticManager(t, "source"), automaticManager(t, "target")
	if source.State().Active || target.State().Active {
		t.Fatal("opening an instance opted in to a network listener")
	}
	code, invitation := invite(t, source)
	state := source.State()
	if !state.Active || state.URL != invitation.URL || state.Listen == "" {
		t.Fatal("invitation did not enable its advertised endpoint")
	}
	if _, err := target.Pair(context.Background(), code, "wrong"); err == nil || target.State().Active {
		t.Fatal("invalid identity confirmation enabled a listener")
	}
	if _, err := target.Pair(context.Background(), code, invitation.Fingerprint); err != nil || !target.State().Active {
		t.Fatalf("joining did not enable its connection: %v", err)
	}
	if err := source.Check(context.Background(), target.State().Fingerprint); err != nil {
		t.Fatal(err)
	}
	source.Close()
	restored, err := Open(filepath.Dir(source.path), "ignored")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restored.Close)
	restored.Restore()
	if restored.State().Listen != state.Listen || !restored.State().Active || restored.State().Fingerprint != state.Fingerprint {
		t.Fatal("automatic connection settings or identity were lost at restart")
	}
	if err := target.Check(context.Background(), restored.State().Fingerprint); err != nil {
		t.Fatal(err)
	}
	if err := restored.Configure("", ""); err != nil {
		t.Fatal(err)
	}
	newCode, next := invite(t, restored)
	if !restored.State().Active || newCode == code || next.Fingerprint != invitation.Fingerprint {
		t.Fatal("inviting after disable did not re-enable with the same identity")
	}
}

func TestAutomaticConnectionSkipsOccupiedPortAndRejectsPublicAddresses(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:9443")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	m := automaticManager(t, "free-port")
	_, invitation := invite(t, m)
	if m.State().Listen == occupied.Addr().String() || invitation.URL != m.State().URL {
		t.Fatal("invitation advertised an occupied listener")
	}
	unsafe := automaticManager(t, "unsafe")
	unsafe.autoAddresses = func() []string { return []string{"0.0.0.0", "8.8.8.8"} }
	if _, _, err := unsafe.CreateInvitation(); err == nil || unsafe.State().Active {
		t.Fatal("automatic invitation bound a wildcard or public interface")
	}
	unsafe.autoAddresses = func() []string { return nil }
	if _, _, err := unsafe.CreateInvitation(); err == nil || unsafe.State().Active {
		t.Fatal("no private network still generated an unusable invitation")
	}
}

func TestAutomaticConnectionFailureDoesNotIssueInvitation(t *testing.T) {
	m := automaticManager(t, "failed-save")
	m.path = filepath.Join(t.TempDir(), "missing-directory", "identity.json")
	if code, _, err := m.CreateInvitation(); err == nil || code != "" || m.State().Active || m.State().Listen != "" || m.inviteExpires.After(time.Now()) {
		t.Fatal("failed persistence left an active listener or usable invitation")
	}
}
