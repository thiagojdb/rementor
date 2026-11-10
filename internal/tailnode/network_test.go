package tailnode

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"testing"
)

func TestSuggestionAvoidsExistingAndOfflineNames(t *testing.T) {
	used := []string{"marcus", "REMENTOR-MARCUS.tail.example.ts.net.", "rementor-marcus-2"}
	if got := SuggestName("Marcus", used); got != "rementor-marcus-3" {
		t.Fatal(got)
	}
	for _, computer := range []string{"", "MY Computer!", string(make([]byte, 100))} {
		if got := SuggestName(computer, nil); !ValidName(got) {
			t.Fatalf("invalid suggestion %q", got)
		}
	}
	for _, name := range []string{"../state", "-foo", "foo-", "x.y", "CAPS", ""} {
		if ValidName(name) {
			t.Fatalf("accepted invalid node name %q", name)
		}
	}
}

type fakeNode struct {
	current        State
	starts, closes int
}

func (n *fakeNode) Start() error                                { n.starts++; return nil }
func (n *fakeNode) Close() error                                { n.closes++; return nil }
func (n *fakeNode) state(context.Context) (State, error)        { return n.current, nil }
func (n *fakeNode) Listen(string, string) (net.Listener, error) { return nil, errors.New("unused") }
func (n *fakeNode) ListenPacket(string, string) (net.PacketConn, error) {
	return nil, errors.New("unused")
}
func (n *fakeNode) do(*http.Request) (*http.Response, error) { return nil, errors.New("unused") }

func TestAuthorizationBeforeAddressAndPersistentNodeReuse(t *testing.T) {
	dir := t.TempDir()
	n := New(dir)
	fake := &fakeNode{current: State{Status: "NeedsLogin", LoginURL: "https://login.tailscale.com/a/test"}}
	n.factory = func(path, name string) backend {
		if path != filepath.Join(dir, name) {
			t.Fatal("node state escaped its private directory")
		}
		return fake
	}
	if _, err := n.Prepare(context.Background(), "../other"); err == nil {
		t.Fatal("accepted path traversal")
	}
	if _, err := n.Prepare(context.Background(), "rementor-marcus"); err == nil {
		t.Fatal("served without authorization")
	}
	if state := n.State(context.Background()); state.LoginURL == "" {
		t.Fatal("setup omitted the authorization link")
	}
	fake.current = State{Status: "Running", Addresses: []string{"100.64.0.20"}}
	for range 2 {
		addresses, err := n.Prepare(context.Background(), "rementor-marcus")
		if err != nil || len(addresses) != 1 || addresses[0] != "100.64.0.20" {
			t.Fatal(addresses, err)
		}
	}
	if fake.starts != 1 {
		t.Fatal("replaced the identity instead of reusing it")
	}
	n.ExpectedTailnet = func(context.Context) (string, error) { return "expected.ts.net", nil }
	fake.current.Tailnet = "another.ts.net"
	if _, err := n.Prepare(context.Background(), "rementor-marcus"); err == nil {
		t.Fatal("published into a different tailnet")
	}
	fake.current.Tailnet = "expected.ts.net"
	if _, err := n.Prepare(context.Background(), "rementor-marcus"); err != nil {
		t.Fatal(err)
	}
	if _, err := n.Prepare(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if fake.closes != 1 {
		t.Fatal("did not disconnect when switching back to the computer")
	}
}
