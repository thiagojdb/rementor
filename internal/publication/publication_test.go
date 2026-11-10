package publication

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thiagojdb/rementor/internal/access"
	"golang.org/x/net/dns/dnsmessage"
)

type memoryStore struct{ c Configuration }

func (s *memoryStore) Load() (Configuration, error) { return s.c.Clone(), nil }
func (s *memoryStore) Save(c Configuration, expected uint64) (Configuration, error) {
	if expected != s.c.Revision {
		return c, ErrConflict
	}
	s.c = c.Clone()
	return s.c, nil
}

const owner = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func configFixture() Configuration {
	return Configuration{Enabled: true, Suffix: "fleet.example.test", CertificateMode: "private", ListenAddress: "100.64.0.1", Entries: []Entry{{Computer: owner, Name: "marcus", WorkspaceID: "dev", IncludeSessions: true}}}
}

type dedicatedFixture struct {
	ready     bool
	listeners []string
}

func (n *dedicatedFixture) Prepare(_ context.Context, name string) ([]string, error) {
	if name != "rementor-marcus" || !n.ready {
		return nil, errors.New("authorization required")
	}
	return []string{"100.64.0.20"}, nil
}
func (n *dedicatedFixture) Listen(network, address string) (net.Listener, error) {
	n.listeners = append(n.listeners, address)
	return net.Listen(network, "127.0.0.1:0")
}
func (n *dedicatedFixture) ListenPacket(network, address string) (net.PacketConn, error) {
	n.listeners = append(n.listeners, address)
	return net.ListenPacket(network, "127.0.0.1:0")
}
func (*dedicatedFixture) Close() error { return nil }

func TestDedicatedPublishingUsesAssignedAddressWithoutTakingOverHostPorts(t *testing.T) {
	c := configFixture()
	c.NodeName = "rementor-marcus"
	c.ListenAddress = "" // An IP does not exist until Tailscale assigns it.
	network := &dedicatedFixture{ready: true}
	manager, err := New(&memoryStore{c: c}, Options{
		Network: network, CADirectory: t.TempDir(),
		Catalog: func(context.Context, string) ([]Scope, error) {
			return []Scope{{WorkspaceID: "dev", Hosts: []string{"dev.giss.localhost"}}}, nil
		},
		Addresses: func(context.Context) ([]string, error) {
			t.Error("checked the host's occupied address")
			return nil, errors.New("host 443 belongs to T3 Code")
		},
		Listen: func(string, string) (net.Listener, error) {
			t.Error("bound a host port")
			return nil, errors.New("host listener used")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	manager.Reconcile(context.Background())
	state := manager.State()
	if !state.DNSActive || !state.HTTPSActive || state.ListenAddress != "100.64.0.20" {
		t.Fatalf("dedicated address not serving: %+v", state)
	}
	if strings.Join(network.listeners, ",") != "100.64.0.20:443,100.64.0.20:53,100.64.0.20:53" {
		t.Fatal(network.listeners)
	}
	answer := dnsQuery(t, manager, "dev.giss.marcus.fleet.example.test", dnsmessage.TypeA)
	if len(answer.Answers) != 1 || answer.Answers[0].Body.(*dnsmessage.AResource).A != [4]byte{100, 64, 0, 20} {
		t.Fatalf("DNS returned the host address: %+v", answer)
	}
	network.ready = false
	manager.Reconcile(context.Background())
	state = manager.State()
	if state.DNSActive || state.HTTPSActive || state.HTTPSError == "" {
		t.Fatal("fell back to the host after losing the dedicated node")
	}
}
func TestHostnamesPreserveAliasesAndSessionHierarchy(t *testing.T) {
	for _, host := range []string{"dev.giss.localhost", "desenvolvimento.giss.localhost", "feature-xml.dev.giss.localhost"} {
		name, err := Hostname(host, "marcus", "fleet.example.test")
		if err != nil || name != strings.TrimSuffix(host, ".localhost")+".marcus.fleet.example.test" {
			t.Fatalf("hostname %s: %s %v", host, name, err)
		}
	}
	for _, host := range []string{"localhost", "example.com", "a.localhost.attacker.test"} {
		if _, err := Hostname(host, "marcus", "fleet.example.test"); err == nil {
			t.Fatalf("accepted undeclared namespace %s", host)
		}
	}
	c := configFixture()
	c.Entries = append(c.Entries, Entry{Computer: strings.Repeat("b", 64), Name: "marcus", WorkspaceID: "qua"})
	if c.Validate() == nil {
		t.Fatal("different computers claimed the same URL namespace")
	}
	for _, suffix := range []string{"foo:443", "https://foo.test", "x.localhost", "x.test/path", "x.test.", "x.test?x=y"} {
		c = configFixture()
		c.Suffix = suffix
		if c.Validate() == nil {
			t.Fatalf("invalid suffix accepted: %s", suffix)
		}
	}
}
func TestPublishedRequestPinsTaggedAndUserDevicesToHostTarget(t *testing.T) {
	request := Request{WorkspaceID: "xml", SourceAddress: "100.64.0.2", Links: []Link{{LocalHost: "xml.dev.localhost", URL: "https://xml.dev.marcus.example.test"}}}
	for _, identity := range []access.Identity{{NodeID: "phone", UserID: 99}, {NodeID: "server", Tagged: true}} {
		doc, err := request.Document(identity)
		if err != nil {
			t.Fatal(err)
		}
		selection, err := doc.Select(identity)
		if err != nil || selection.WorkspaceID != "xml" {
			t.Fatal(selection, err)
		}
		if _, err := doc.Select(access.Identity{NodeID: "someone-else", Tagged: true}); err == nil {
			t.Fatal("unverified tagged identity authorized")
		}
	}
	request.Links[0].URL = "https://xml.dev.example.test:8443"
	if _, err := request.Document(access.Identity{NodeID: "phone", UserID: 99}); err == nil {
		t.Fatal("published non-443 origin")
	}
}
func dnsQuery(t *testing.T, m *Manager, host string, kind dnsmessage.Type) dnsmessage.Message {
	t.Helper()
	name, err := dnsmessage.NewName(host + ".")
	if err != nil {
		t.Fatal(err)
	}
	query := dnsmessage.Message{Header: dnsmessage.Header{ID: 15, RecursionDesired: true}, Questions: []dnsmessage.Question{{Name: name, Type: kind, Class: dnsmessage.ClassINET}}}
	raw, err := query.Pack()
	if err != nil {
		t.Fatal(err)
	}
	var response dnsmessage.Message
	if err := response.Unpack(m.answerDNS(raw)); err != nil {
		t.Fatal(err)
	}
	return response
}

type sourceConn struct{ net.Conn }

func (sourceConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("100.64.0.2"), Port: 12345}
}

type sourceListener struct{ net.Listener }

func (l sourceListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return sourceConn{c}, nil
}
func TestLiveSessionsDNSHTTPSFailureAndRevocation(t *testing.T) {
	store := &memoryStore{c: configFixture()}
	var scopes = []Scope{{WorkspaceID: "dev", EnvironmentID: "dev", Name: "desenvolvimento", Hosts: []string{"dev.giss.localhost", "desenvolvimento.giss.localhost"}}}
	var unavailable bool
	var mu sync.Mutex
	addresses := map[string]string{}
	started := make(chan struct{}, 1)
	canceled := make(chan struct{}, 1)
	manager, err := New(store, Options{CADirectory: t.TempDir(), Catalog: func(context.Context, string) ([]Scope, error) {
		if unavailable {
			return nil, errors.New("marcus is offline")
		}
		return scopes, nil
	}, Addresses: func(context.Context) ([]string, error) { return []string{"100.64.0.1"}, nil }, Listen: func(network, address string) (net.Listener, error) {
		l, err := net.Listen(network, "127.0.0.1:0")
		if err == nil {
			mu.Lock()
			addresses[address] = l.Addr().String()
			mu.Unlock()
		}
		return sourceListener{l}, err
	}, ListenPacket: func(network, address string) (net.PacketConn, error) { return net.ListenPacket(network, "127.0.0.1:0") }, Serve: func(w http.ResponseWriter, r *http.Request, metadata Request) {
		if metadata.Computer != owner {
			t.Error("wrong instance")
		}
		if r.URL.Path == "/stream" {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			started <- struct{}{}
			<-r.Context().Done()
			canceled <- struct{}{}
			return
		}
		_, _ = io.WriteString(w, metadata.WorkspaceID)
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	manager.Reconcile(context.Background())
	state := manager.State()
	if !state.DNSActive || !state.HTTPSActive || state.Certificate == "" {
		t.Fatalf("listeners not started: %+v", state)
	}
	root := x509.NewCertPool()
	if !root.AppendCertsFromPEM([]byte(state.Certificate)) {
		t.Fatal("CA missing")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: root}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		mu.Lock()
		target := addresses["100.64.0.1:443"]
		mu.Unlock()
		return (&net.Dialer{}).DialContext(ctx, network, target)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	get := func(host string) string {
		t.Helper()
		res, err := client.Get("https://" + host + "/")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 {
			t.Fatalf("HTTP %d: %s", res.StatusCode, raw)
		}
		return string(raw)
	}
	host := "dev.giss.marcus.fleet.example.test"
	if get(host) != "dev" {
		t.Fatal("wrong environment")
	}
	if response := dnsQuery(t, manager, host, dnsmessage.TypeA); len(response.Answers) != 1 || response.Header.RecursionAvailable {
		t.Fatal("incorrect authoritative DNS", response)
	}
	if response := dnsQuery(t, manager, "not-published.marcus.fleet.example.test", dnsmessage.TypeA); response.Header.RCode != dnsmessage.RCodeNameError {
		t.Fatal("wildcard exposed unpublished host")
	}
	if response := dnsQuery(t, manager, "example.com", dnsmessage.TypeA); response.Header.RCode != dnsmessage.RCodeRefused {
		t.Fatal("DNS became a recursive resolver")
	}
	if _, err := client.Get("https://unknown.marcus.fleet.example.test/"); err == nil {
		t.Fatal("issued a certificate for an unpublished hostname")
	}
	scopes = append(scopes, Scope{WorkspaceID: "xml-id", EnvironmentID: "dev", SessionID: "xml-id", Name: "xml", Hosts: []string{"xml.dev.giss.localhost"}})
	manager.Reconcile(context.Background())
	if get("xml.dev.giss.marcus.fleet.example.test") != "xml-id" {
		t.Fatal("new session not live")
	}
	unavailable = true
	manager.Reconcile(context.Background())
	response, err := client.Get("https://" + host + "/")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 503 {
		t.Fatal("offline source silently served", response.StatusCode)
	}
	unavailable = false
	manager.Reconcile(context.Background())
	response, err = client.Get("https://" + host + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	<-started
	c := manager.State().Configuration
	c.Enabled = false
	if _, err := manager.Save(c, c.Revision); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("publication removal did not cancel stream")
	}
	if _, err := manager.Save(c, 0); !errors.Is(err, ErrConflict) {
		t.Fatal("stale save overwrote publication")
	}
	if response := dnsQuery(t, manager, host, dnsmessage.TypeA); response.Header.RCode != dnsmessage.RCodeRefused {
		t.Fatal("disabled DNS still answered")
	}
}
func TestPrivateCARestartsAndListenerCollisionAreVisible(t *testing.T) {
	dir := t.TempDir()
	first, err := newCertificates(dir, "private", "", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := newCertificates(dir, "private", "", "")
	if err != nil || first.publicPEM != second.publicPEM {
		t.Fatal("restart changed trust root", err)
	}
	cert, err := first.get("xml.dev.marcus.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := cert.Leaf.VerifyHostname("xml.dev.marcus.example.test"); err != nil {
		t.Fatal(err)
	}
	manager, err := New(&memoryStore{c: configFixture()}, Options{CADirectory: dir, Catalog: func(context.Context, string) ([]Scope, error) { return nil, nil }, Addresses: func(context.Context) ([]string, error) { return []string{"100.64.0.1"}, nil }, Listen: func(string, string) (net.Listener, error) { return nil, errors.New("port already in use") }})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	manager.Reconcile(context.Background())
	if state := manager.State(); state.HTTPSActive || !strings.Contains(state.HTTPSError, "port already in use") {
		t.Fatalf("port collision hidden: %+v", state)
	}
}
