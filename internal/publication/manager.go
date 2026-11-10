package publication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type State struct {
	Configuration Configuration
	Scopes        []PublishedScope
	DNSActive     bool
	HTTPSActive   bool
	DNSError      string
	HTTPSError    string
	Certificate   string
	ListenAddress string
}
type DedicatedNetwork interface {
	Prepare(context.Context, string) ([]string, error)
	Listen(string, string) (net.Listener, error)
	ListenPacket(string, string) (net.PacketConn, error)
	Close() error
}
type Options struct {
	SetupError      string
	Catalog         Catalog
	Serve           func(http.ResponseWriter, *http.Request, Request)
	Addresses       func(context.Context) ([]string, error)
	CADirectory     string
	CertificateFile string
	KeyFile         string
	// Listener injection keeps acceptance tests off privileged host ports.
	Listen       func(string, string) (net.Listener, error)
	ListenPacket func(string, string) (net.PacketConn, error)
	Network      DedicatedNetwork
}
type Manager struct {
	op           sync.Mutex
	mu           sync.RWMutex
	store        Store
	options      Options
	state        State
	catalog      map[string][]Scope
	generation   context.Context
	cancel       context.CancelFunc
	server       *http.Server
	dnsUDP       net.PacketConn
	dnsTCP       net.Listener
	certificates *certificates
	wake         chan struct{}
}

func New(store Store, options Options) (*Manager, error) {
	c, err := store.Load()
	if err != nil {
		return nil, err
	}
	if c.Suffix == "" {
		c.Suffix = "rementor.internal"
		c.CertificateMode = "private"
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if options.Listen == nil {
		options.Listen = net.Listen
	}
	if options.ListenPacket == nil {
		options.ListenPacket = net.ListenPacket
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{store: store, options: options, state: State{Configuration: c}, catalog: map[string][]Scope{}, generation: ctx, cancel: cancel, wake: make(chan struct{}, 1)}, nil
}
func (m *Manager) State() State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.state
	s.Configuration = s.Configuration.Clone()
	s.Scopes = append([]PublishedScope(nil), s.Scopes...)
	for i := range s.Scopes {
		s.Scopes[i].Links = append([]Link(nil), s.Scopes[i].Links...)
		s.Scopes[i].Scope.Hosts = append([]string(nil), s.Scopes[i].Scope.Hosts...)
	}
	return s
}
func (m *Manager) Save(c Configuration, expected uint64) (State, error) {
	if err := c.Validate(); err != nil {
		return State{}, err
	}
	m.op.Lock()
	defer m.op.Unlock()
	if m.State().Configuration.Revision != expected {
		return State{}, ErrConflict
	}
	c.Revision = expected + 1
	saved, err := m.store.Save(c, expected)
	if err != nil {
		return State{}, err
	}
	m.stop()
	m.mu.Lock()
	m.state.Configuration = saved
	m.state.Scopes = nil
	m.catalog = map[string][]Scope{}
	m.cancel()
	m.generation, m.cancel = context.WithCancel(context.Background())
	m.mu.Unlock()
	select {
	case m.wake <- struct{}{}:
	default:
	}
	return m.State(), nil
}
func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	defer m.Close()
	for {
		m.Reconcile(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-m.wake:
		}
	}
}
func (m *Manager) Reconcile(ctx context.Context) {
	m.op.Lock()
	defer m.op.Unlock()
	c := m.State().Configuration
	owners := map[string]bool{}
	for _, e := range c.Entries {
		owners[e.Computer] = true
	}
	catalogs := map[string][]Scope{}
	failures := map[string]string{}
	// Each source is queried once. An unavailable source retains its last names
	// but never its ability to serve application traffic.
	for owner := range owners {
		check, cancel := context.WithTimeout(ctx, 4*time.Second)
		scopes, err := m.options.Catalog(check, owner)
		cancel()
		if err != nil {
			failures[owner] = err.Error()
			catalogs[owner] = m.catalog[owner]
		} else {
			catalogs[owner] = scopes
			m.catalog[owner] = scopes
		}
	}
	var scopes []PublishedScope
	hosts := map[string]int{}
	for _, e := range c.Entries {
		found := false
		for _, s := range catalogs[e.Computer] {
			if s.WorkspaceID != e.WorkspaceID && !(e.IncludeSessions && s.SessionID != "" && s.EnvironmentID == e.WorkspaceID) {
				continue
			}
			if s.WorkspaceID == e.WorkspaceID {
				found = true
			}
			p := PublishedScope{Entry: e, Scope: s, Error: failures[e.Computer]}
			if len(s.Hosts) > 32 {
				p.Error = "This environment has more than 32 application hostnames"
			}
			for _, host := range s.Hosts {
				name, err := Hostname(host, e.Name, c.Suffix)
				if err != nil {
					p.Error = err.Error()
					continue
				}
				if index, exists := hosts[name]; exists {
					p.Error = "Hostname is already published"
					scopes[index].Error = p.Error
				} else {
					hosts[name] = len(scopes)
				}
				p.Links = append(p.Links, Link{LocalHost: host, URL: "https://" + name})
			}
			scopes = append(scopes, p)
		}
		if !found {
			problem := failures[e.Computer]
			if problem == "" {
				problem = "Selected environment is unavailable"
			}
			scopes = append(scopes, PublishedScope{Entry: e, Scope: Scope{WorkspaceID: e.WorkspaceID, Name: e.WorkspaceID}, Error: problem})
		}
	}
	sort.Slice(scopes, func(i, j int) bool {
		a, b := scopes[i], scopes[j]
		return a.Entry.Computer+a.Scope.WorkspaceID < b.Entry.Computer+b.Scope.WorkspaceID
	})
	m.mu.Lock()
	before, _ := json.Marshal(m.state.Scopes)
	after, _ := json.Marshal(scopes)
	if string(before) != string(after) {
		m.cancel()
		m.generation, m.cancel = context.WithCancel(context.Background())
	}
	m.state.Scopes = scopes
	m.mu.Unlock()
	var addresses []string
	var err error
	if m.options.Network != nil {
		addresses, err = m.options.Network.Prepare(ctx, c.NodeName)
	} else if c.NodeName != "" {
		err = errors.New("dedicated Tailscale publishing is unavailable")
	}
	if !c.Enabled {
		m.stop()
		return
	}
	if m.options.SetupError != "" {
		m.fail("", m.options.SetupError)
		return
	}
	address := c.ListenAddress
	listen, listenPacket := m.options.Listen, m.options.ListenPacket
	if c.NodeName == "" {
		addresses, err = m.options.Addresses(ctx)
	} else {
		if err == nil && len(addresses) > 0 {
			address = addresses[0]
		}
		if m.options.Network != nil {
			listen, listenPacket = m.options.Network.Listen, m.options.Network.ListenPacket
		}
	}
	if err == nil {
		found := false
		for _, a := range addresses {
			if a == address && TailnetIP(net.ParseIP(a)) {
				found = true
			}
		}
		if !found {
			err = errors.New("select this computer's connected Tailscale address")
		}
	}
	if err != nil {
		m.stop()
		m.fail(err.Error(), err.Error())
		return
	}
	if current := m.State(); current.ListenAddress != "" && current.ListenAddress != address {
		m.stop()
	}
	certs := m.certificates
	if certs == nil || c.CertificateMode == "external" || m.server == nil {
		certs, err = newCertificates(m.options.CADirectory, c.CertificateMode, m.options.CertificateFile, m.options.KeyFile)
	}
	if err != nil {
		m.stop()
		m.fail("", err.Error())
		return
	}
	for _, scope := range scopes {
		for _, link := range scope.Links {
			if _, err := certs.get(strings.TrimPrefix(link.URL, "https://")); err != nil {
				m.stop()
				m.fail("", "HTTPS certificate: "+err.Error())
				return
			}
		}
	}
	if current := m.State(); m.server != nil && m.dnsUDP != nil && m.dnsTCP != nil && current.DNSActive && current.HTTPSActive {
		if c.CertificateMode == "external" {
			m.certificates.mu.Lock()
			m.certificates.external = certs.external
			m.certificates.mu.Unlock()
		}
		return
	}
	m.stop()
	m.certificates = certs
	m.mu.Lock()
	m.state.Certificate = certs.publicPEM
	m.state.ListenAddress = address
	m.mu.Unlock()
	tcp, err := listen("tcp", net.JoinHostPort(address, "443"))
	if err != nil {
		m.fail("", fmt.Sprintf("HTTPS on 443: %v", err))
		return
	}
	dnsTCP, err := listen("tcp", net.JoinHostPort(address, "53"))
	if err != nil {
		tcp.Close()
		m.fail(fmt.Sprintf("DNS on TCP 53: %v", err), "")
		return
	}
	udp, err := listenPacket("udp", net.JoinHostPort(address, "53"))
	if err != nil {
		tcp.Close()
		dnsTCP.Close()
		m.fail(fmt.Sprintf("DNS on UDP 53: %v", err), "")
		return
	}
	server := &http.Server{Handler: m, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384, TLSConfig: m.tlsConfig()}
	m.server, m.dnsTCP, m.dnsUDP = server, dnsTCP, udp
	m.mu.Lock()
	m.state.HTTPSActive = true
	m.state.DNSActive = true
	m.state.DNSError = ""
	m.state.HTTPSError = ""
	m.mu.Unlock()
	go func() {
		if err := server.ServeTLS(tcp, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			m.mu.Lock()
			m.state.HTTPSActive = false
			m.state.HTTPSError = err.Error()
			m.mu.Unlock()
		}
	}()
	go m.serveDNSUDP(udp)
	go m.serveDNSTCP(dnsTCP)
}
func (m *Manager) fail(dns, https string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.DNSError = dns
	m.state.HTTPSError = https
}
func (m *Manager) stop() {
	if m.server != nil {
		_ = m.server.Close()
		m.server = nil
	}
	if m.dnsUDP != nil {
		_ = m.dnsUDP.Close()
		m.dnsUDP = nil
	}
	if m.dnsTCP != nil {
		_ = m.dnsTCP.Close()
		m.dnsTCP = nil
	}
	m.mu.Lock()
	m.state.DNSActive = false
	m.state.HTTPSActive = false
	m.state.DNSError = ""
	m.state.HTTPSError = ""
	m.cancel()
	m.generation, m.cancel = context.WithCancel(context.Background())
	m.mu.Unlock()
}
func (m *Manager) Close() {
	m.op.Lock()
	defer m.op.Unlock()
	m.stop()
	if m.options.Network != nil {
		_ = m.options.Network.Close()
	}
}
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !TailnetIP(net.ParseIP(remote)) {
		http.Error(w, "Tailscale connection required", http.StatusForbidden)
		return
	}
	host := r.Host
	if parsed, port, err := net.SplitHostPort(host); err == nil {
		if port != "443" {
			http.Error(w, "HTTPS on port 443 required", 400)
			return
		}
		host = parsed
	}
	if r.TLS == nil || r.TLS.ServerName != host {
		http.Error(w, "HTTPS hostname does not match this request", 421)
		return
	}
	m.mu.RLock()
	c := m.state.Configuration
	var selected *PublishedScope
	for _, s := range m.state.Scopes {
		for _, l := range s.Links {
			if l.URL == "https://"+host {
				copy := s
				selected = &copy
			}
		}
	}
	generation := m.generation
	m.mu.RUnlock()
	if !c.Enabled || selected == nil {
		http.NotFound(w, r)
		return
	}
	if selected.Error != "" {
		http.Error(w, "Selected computer or environment unavailable: "+selected.Error, 503)
		return
	}
	request, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(generation, cancel)
	defer stop()
	m.options.Serve(w, r.WithContext(request), Request{Computer: selected.Entry.Computer, WorkspaceID: selected.Scope.WorkspaceID, SourceAddress: remote, Links: append([]Link(nil), selected.Links...)})
}
