// Package peers owns enrollment and the authenticated peer-only listener.
// Management RPCs are never mounted on this listener.
package peers

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thiagojdb/rementor/internal/fleet"
	"github.com/thiagojdb/rementor/internal/shared"
)

const invitationTTL = 5 * time.Minute
const maxDocument = 8192

type Peer struct {
	Fingerprint string `json:"fingerprint"`
	Name        string `json:"name"`
	URL         string `json:"url"`
}
type diskState struct {
	Name        string          `json:"name"`
	Certificate []byte          `json:"certificate"`
	Key         []byte          `json:"key"`
	Listen      string          `json:"listen"`
	URL         string          `json:"url"`
	Peers       map[string]Peer `json:"peers"`
}
type State struct {
	Name        string
	Fingerprint string
	Listen      string
	URL         string
	Active      bool
	Error       string
	Peers       []Peer
	Addresses   []string
}
type Invitation struct {
	Version     int       `json:"version"`
	Name        string    `json:"name"`
	URL         string    `json:"url"`
	Fingerprint string    `json:"fingerprint"`
	Token       string    `json:"token"`
	ExpiresAt   time.Time `json:"expiresAt"`
}
type enrollment struct {
	Token string `json:"token"`
	Name  string `json:"name"`
	URL   string `json:"url"`
}
type Manager struct {
	mu                 sync.Mutex
	path               string
	state              diskState
	identity           tls.Certificate
	server             *http.Server
	inviteHash         [32]byte
	inviteExpires      time.Time
	listenerError      string
	generation         uint64
	workspaceSource    fleet.WorkspaceSource
	sharedAuthority    *shared.Authority
	sharedIngress      SharedIngress
	publicationIngress PublicationIngress
	forwards           map[uint64]activeForward
	forwardSequence    uint64
	managementPort     int
	protectedPorts     map[int]bool
	now                func() time.Time
	autoAddresses      func() []string
}

func Open(directory, name string) (*Manager, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return nil, err
	}
	m := &Manager{path: filepath.Join(directory, "identity.json"), now: time.Now, managementPort: 9300, autoAddresses: invitationAddresses}
	raw, err := os.ReadFile(m.path)
	if err == nil {
		if err = json.Unmarshal(raw, &m.state); err != nil {
			return nil, fmt.Errorf("read peer identity: %w", err)
		}
		if err = os.Chmod(m.path, 0600); err != nil {
			return nil, err
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if strings.TrimSpace(name) == "" {
			name = "this-computer"
		}
		if len(name) > 80 {
			return nil, errors.New("computer name is too long")
		}
		pub, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if err != nil {
			return nil, err
		}
		template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		cert, err := x509.CreateCertificate(rand.Reader, template, template, pub, key)
		if err != nil {
			return nil, err
		}
		keyDER, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return nil, err
		}
		m.state = diskState{Name: name, Certificate: cert, Key: keyDER, Peers: map[string]Peer{}}
		if err = m.persist(m.state); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	if m.state.Peers == nil {
		m.state.Peers = map[string]Peer{}
	}
	m.identity, err = tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: m.state.Certificate}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: m.state.Key}))
	if err != nil {
		return nil, fmt.Errorf("load peer key: %w", err)
	}
	return m, nil
}

func fingerprint(cert []byte) string {
	digest := sha256.Sum256(cert)
	return hex.EncodeToString(digest[:])
}
func validFingerprint(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32 && value == strings.ToLower(value)
}
func (m *Manager) persist(next diskState) error {
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(m.path), ".identity-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), m.path)
}
func cloneState(s diskState) diskState {
	result := s
	result.Peers = make(map[string]Peer, len(s.Peers))
	for id, p := range s.Peers {
		result.Peers[id] = p
	}
	return result
}
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := State{Name: m.state.Name, Fingerprint: fingerprint(m.state.Certificate), Listen: m.state.Listen, URL: m.state.URL, Active: m.server != nil, Error: m.listenerError, Addresses: PrivateAddresses()}
	for _, p := range m.state.Peers {
		s.Peers = append(s.Peers, p)
	}
	sort.Slice(s.Peers, func(i, j int) bool { return s.Peers[i].Name < s.Peers[j].Name })
	return s
}
func PrivateAddresses() []string {
	result := []string{"127.0.0.1"}
	addresses, _ := net.InterfaceAddrs()
	for _, addr := range addresses {
		ip, _, err := net.ParseCIDR(addr.String())
		if err != nil || ip.IsLoopback() {
			continue
		}
		if privateIP(ip) {
			result = append(result, ip.String())
		}
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := strings.HasPrefix(result[i], "100."), strings.HasPrefix(result[j], "100.")
		if a != b {
			return a
		}
		return result[i] < result[j]
	})
	return result
}
func privateIP(ip net.IP) bool {
	_, tailnet, _ := net.ParseCIDR("100.64.0.0/10")
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || tailnet.Contains(ip))
}
func validURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || strings.HasSuffix(raw, "#") {
		return errors.New("use an HTTPS address without credentials, path, query or fragment")
	}
	return nil
}

// Configure opts in only the chosen private interface. The listener accepts
// client certificates, but authorization is checked anew on every request.
func (m *Manager) Configure(listen, advertised string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.configureLocked(listen, advertised)
}

func (m *Manager) configureLocked(listen, advertised string) error {
	next := cloneState(m.state)
	if listen == "" {
		next.Listen = ""
		next.URL = ""
		if err := m.persist(next); err != nil {
			return err
		}
		if m.server != nil {
			_ = m.server.Close()
			m.server = nil
		}
		m.state = next
		m.cancelForwardsLocked("")
		m.generation++
		m.inviteExpires = time.Time{}
		m.listenerError = ""
		return nil
	}
	host, port, err := net.SplitHostPort(listen)
	portNumber, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || portNumber < 1 || portNumber > 65535 || !privateIP(net.ParseIP(host)) {
		return errors.New("choose a private or loopback IP address and a nonzero port")
	}
	if err = validURL(advertised); err != nil {
		return err
	}
	next.Listen = listen
	next.URL = advertised
	if m.server != nil && listen == m.state.Listen {
		if err = m.persist(next); err != nil {
			return err
		}
		m.state = next
		m.cancelForwardsLocked("")
		m.generation++
		m.inviteExpires = time.Time{}
		return nil
	}
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("start peer listener: %w", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(m.handle), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32768}
	secure := tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{m.identity}, MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAnyClientCert})
	if err = m.persist(next); err != nil {
		_ = listener.Close()
		return err
	}
	if m.server != nil {
		_ = m.server.Close()
	}
	m.server = server
	m.state = next
	m.cancelForwardsLocked("")
	m.generation++
	m.listenerError = ""
	m.inviteExpires = time.Time{}
	go func() { _ = server.Serve(secure) }()
	return nil
}
func (m *Manager) Restore() {
	m.mu.Lock()
	listen, advertised := m.state.Listen, m.state.URL
	m.mu.Unlock()
	if listen == "" {
		return
	}
	if err := m.Configure(listen, advertised); err != nil {
		m.mu.Lock()
		m.listenerError = err.Error()
		m.mu.Unlock()
	}
}
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server != nil {
		_ = m.server.Close()
		m.server = nil
	}
	m.inviteExpires = time.Time{}
	m.cancelForwardsLocked("")
}
func (m *Manager) CreateInvitation() (string, Invitation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureConnectionLocked(); err != nil {
		return "", Invitation{}, err
	}
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", Invitation{}, err
	}
	i := Invitation{Version: 1, Name: m.state.Name, URL: m.state.URL, Fingerprint: fingerprint(m.state.Certificate), Token: hex.EncodeToString(token[:]), ExpiresAt: m.now().Add(invitationTTL)}
	m.inviteHash = sha256.Sum256([]byte(i.Token))
	m.inviteExpires = i.ExpiresAt
	raw, _ := json.Marshal(i)
	return "rementor:" + base64.RawURLEncoding.EncodeToString(raw), i, nil
}
func (m *Manager) CancelInvitation() { m.mu.Lock(); defer m.mu.Unlock(); m.inviteExpires = time.Time{} }
func ParseInvitation(code string, now time.Time) (Invitation, error) {
	var i Invitation
	code = strings.TrimSpace(code)
	if len(code) > maxDocument || !strings.HasPrefix(code, "rementor:") {
		return i, errors.New("paste the complete Rementor invitation from the other computer")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(code, "rementor:"))
	if err != nil {
		return i, errors.New("invalid invitation")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&i); err != nil {
		return i, errors.New("invalid invitation")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return i, errors.New("invalid invitation")
	}
	token, err := hex.DecodeString(i.Token)
	if err != nil || len(token) != 32 || i.Version != 1 || !validFingerprint(i.Fingerprint) || len(i.Name) > 80 || strings.TrimSpace(i.Name) == "" {
		return i, errors.New("invalid invitation")
	}
	if err = validURL(i.URL); err != nil {
		return i, err
	}
	if !now.Before(i.ExpiresAt) || i.ExpiresAt.After(now.Add(invitationTTL+time.Minute)) {
		return i, errors.New("invitation expired or the computers’ clocks disagree; create another invitation")
	}
	return i, nil
}
func (m *Manager) client(pin string) *http.Client {
	transport := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 5 * time.Second, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, TLSClientConfig: &tls.Config{Certificates: []tls.Certificate{m.identity}, MinVersion: tls.VersionTLS13,
		// Enrollment uses the certificate pin carried over the out-of-band
		// invitation instead of a public CA or the destination hostname.
		InsecureSkipVerify: true, VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) != 1 || fingerprint(state.PeerCertificates[0].Raw) != pin {
				return errors.New("peer identity does not match the invitation")
			}
			cert := state.PeerCertificates[0]
			now := m.now()
			if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
				return errors.New("peer certificate expired or is not valid yet")
			}
			return nil
		}}}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("peer redirects are not allowed") }}
}
func (m *Manager) Pair(ctx context.Context, code, confirmedFingerprint string) (Peer, error) {
	i, err := ParseInvitation(code, m.now())
	if err != nil {
		return Peer{}, err
	}
	if i.Fingerprint != confirmedFingerprint {
		return Peer{}, errors.New("confirm the fingerprint shown on the other computer")
	}
	m.mu.Lock()
	if fingerprint(m.state.Certificate) == i.Fingerprint {
		m.mu.Unlock()
		return Peer{}, errors.New("this invitation belongs to this computer")
	}
	if len(m.state.Peers) >= 64 {
		if _, paired := m.state.Peers[i.Fingerprint]; !paired {
			m.mu.Unlock()
			return Peer{}, errors.New("disconnect a computer before adding another; maximum 64")
		}
	}
	if err := m.ensureConnectionLocked(); err != nil {
		m.mu.Unlock()
		return Peer{}, err
	}
	own := Peer{Name: m.state.Name, URL: m.state.URL, Fingerprint: fingerprint(m.state.Certificate)}
	generation := m.generation
	m.mu.Unlock()
	body, _ := json.Marshal(enrollment{Token: i.Token, Name: own.Name, URL: own.URL})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, i.URL+"/v1/pair", bytes.NewReader(body))
	if err != nil {
		return Peer{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := m.client(i.Fingerprint)
	defer client.CloseIdleConnections()
	response, err := client.Do(req)
	if err != nil {
		return Peer{}, fmt.Errorf("connect to %s: %w", i.Name, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Peer{}, errors.New("pairing was rejected; the invitation may be expired, cancelled or already used")
	}
	var remote Peer
	if err = json.NewDecoder(io.LimitReader(response.Body, maxDocument)).Decode(&remote); err != nil {
		return Peer{}, err
	}
	if remote.Fingerprint != i.Fingerprint || remote.URL != i.URL || remote.Name != i.Name {
		return Peer{}, errors.New("peer identity changed during pairing")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// If local revocation/disable happened during the request, do not establish
	// local trust. The remote may have accepted us; surface the partial result.
	if m.server == nil || m.state.URL != own.URL || m.generation != generation {
		return Peer{}, errors.New("connection settings changed during pairing; remove this computer on the other side and retry")
	}
	next := cloneState(m.state)
	if _, exists := next.Peers[remote.Fingerprint]; !exists && len(next.Peers) >= 64 {
		return Peer{}, errors.New("other computer accepted pairing, but this computer’s 64-peer limit was reached; disconnect it there and retry")
	}
	next.Peers[remote.Fingerprint] = remote
	if err = m.persist(next); err != nil {
		return Peer{}, fmt.Errorf("other computer accepted pairing, but local trust could not be saved; remove this computer there and retry: %w", err)
	}
	m.state = next
	m.generation++
	m.cancelForwardsLocked(remote.Fingerprint)
	return remote, nil
}
func (m *Manager) Revoke(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	next := cloneState(m.state)
	delete(next.Peers, id)
	if err := m.persist(next); err != nil {
		return err
	}
	m.state = next
	m.cancelForwardsLocked(id)
	m.generation++
	m.inviteExpires = time.Time{}
	return nil
}
func (m *Manager) Check(ctx context.Context, id string) error {
	m.mu.Lock()
	p, ok := m.state.Peers[id]
	m.mu.Unlock()
	if !ok {
		return errors.New("computer is not paired")
	}
	client := m.client(p.Fingerprint)
	defer client.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.URL+"/v1/status", nil)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("peer no longer trusts this computer; pair again")
	}
	var remote Peer
	if err = json.NewDecoder(io.LimitReader(resp.Body, maxDocument)).Decode(&remote); err != nil {
		return err
	}
	if remote.Fingerprint != id {
		return errors.New("peer identity mismatch")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok = m.state.Peers[id]; !ok {
		return errors.New("computer was unpaired during the check")
	}
	return nil
}
func (m *Manager) matchesInvitation(token string) bool {
	digest := sha256.Sum256([]byte(token))
	return len(token) == 64 && subtle.ConstantTimeCompare(digest[:], m.inviteHash[:]) == 1
}

func (m *Manager) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.TLS == nil || len(r.TLS.PeerCertificates) != 1 {
		http.Error(w, "client certificate required", http.StatusUnauthorized)
		return
	}
	cert := r.TLS.PeerCertificates[0]
	now := m.now()
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		http.Error(w, "invalid client certificate", http.StatusUnauthorized)
		return
	}
	id := fingerprint(cert.Raw)
	if r.Method == http.MethodPost && r.URL.Path == "/v1/pair" {
		var request enrollment
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDocument))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || decoder.Decode(new(any)) != io.EOF || len(request.Name) > 80 || strings.TrimSpace(request.Name) == "" || validURL(request.URL) != nil {
			http.Error(w, "invalid enrollment", http.StatusBadRequest)
			return
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.server == nil || !m.now().Before(m.inviteExpires) || !m.matchesInvitation(request.Token) {
			http.Error(w, "invalid invitation", http.StatusForbidden)
			return
		}
		if id == fingerprint(m.state.Certificate) {
			http.Error(w, "cannot pair with self", http.StatusBadRequest)
			return
		}
		next := cloneState(m.state)
		if _, exists := next.Peers[id]; !exists && len(next.Peers) >= 64 {
			http.Error(w, "paired computer limit reached", http.StatusConflict)
			return
		}
		next.Peers[id] = Peer{Fingerprint: id, Name: request.Name, URL: request.URL}
		if err := m.persist(next); err != nil {
			http.Error(w, "cannot save pairing", http.StatusInternalServerError)
			return
		}
		m.state = next
		m.cancelForwardsLocked(id)
		m.generation++
		m.inviteExpires = time.Time{}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Peer{Fingerprint: fingerprint(m.state.Certificate), Name: m.state.Name, URL: m.state.URL})
		return
	}
	m.mu.Lock()
	_, trusted := m.state.Peers[id]
	own := Peer{Fingerprint: fingerprint(m.state.Certificate), Name: m.state.Name, URL: m.state.URL}
	active := m.server != nil
	source := m.workspaceSource
	m.mu.Unlock()
	if !trusted || !active {
		http.Error(w, "computer is not paired", http.StatusForbidden)
		return
	}
	if r.URL.Path == publicWorkspaceCatalogPath {
		m.handlePublicWorkspaceCatalog(w, r, id, own.Fingerprint)
		return
	}
	if strings.HasPrefix(r.URL.Path, publicationForwardPrefix+"/") {
		m.handlePublicationForward(w, r, id)
		return
	}
	if strings.HasPrefix(r.URL.Path, sharedForwardPrefix+"/") {
		m.handleSharedForward(w, r, id)
		return
	}
	if r.URL.Path == sharedPrefix || strings.HasPrefix(r.URL.Path, sharedPrefix+"/") {
		m.handleShared(w, r, id)
		return
	}
	if strings.HasPrefix(r.URL.Path, forwardPrefix+"/") {
		m.handleForward(w, r, id, own.Fingerprint, source)
		return
	}
	if r.URL.Path == "/v1/configuration/edit" {
		m.handleConfigurationEdit(w, r, id, source)
		return
	}
	if r.URL.Path == "/v1/configuration" {
		m.handleConfiguration(w, r, id, source)
		return
	}
	if r.Method == http.MethodGet && (r.URL.Path == "/v1/workspaces" || strings.HasPrefix(r.URL.Path, "/v1/workspaces/")) {
		m.handleWorkspace(w, r, id, own.Fingerprint, source)
		return
	}
	if r.Method != http.MethodGet || r.URL.Path != "/v1/status" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(own)
}
