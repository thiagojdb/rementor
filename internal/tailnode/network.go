// Package tailnode supplies an independent, persistent Tailscale node for
// hostname publishing. It never edits the computer's tailscaled configuration.
package tailnode

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"tailscale.com/tsnet"
)

type State struct {
	Name, DNSName, Tailnet, LoginURL, Status, Error string
	Addresses                                       []string
}

type backend interface {
	Start() error
	Close() error
	state(context.Context) (State, error)
	Listen(string, string) (net.Listener, error)
	ListenPacket(string, string) (net.PacketConn, error)
	do(*http.Request) (*http.Response, error)
}
type node struct{ *tsnet.Server }

func (n node) state(ctx context.Context) (State, error) {
	c, err := n.LocalClient()
	if err != nil {
		return State{}, err
	}
	s, err := c.StatusWithoutPeers(ctx)
	if err != nil {
		return State{}, err
	}
	state := State{Status: s.BackendState, LoginURL: s.AuthURL}
	if s.Self != nil {
		state.Name = s.Self.HostName
		state.DNSName = strings.TrimSuffix(s.Self.DNSName, ".")
	}
	if s.CurrentTailnet != nil {
		state.Tailnet = s.CurrentTailnet.MagicDNSSuffix
	}
	for _, ip := range s.TailscaleIPs {
		state.Addresses = append(state.Addresses, ip.String())
	}
	return state, nil
}
func (n node) do(r *http.Request) (*http.Response, error) {
	c, err := n.LocalClient()
	if err != nil {
		return nil, err
	}
	return c.DoLocalRequest(r)
}

type Network struct {
	mu              sync.Mutex
	dir, name       string
	server          backend
	factory         func(string, string) backend
	lastError       string
	ExpectedTailnet func(context.Context) (string, error)
}

func New(dir string) *Network {
	return &Network{dir: dir, factory: func(dir, name string) backend {
		// Auth URLs belong only in the local setup UI, never service logs.
		return node{&tsnet.Server{Dir: dir, Hostname: name, UserLogf: func(string, ...any) {}}}
	}}
}
func ValidName(name string) bool {
	if len(name) < 1 || len(name) > 63 || name[0] == '-' || name[len(name)-1] == '-' {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// SuggestName reserves all known node names, including offline nodes. An IP
// cannot be predicted: Tailscale assigns it after the user authorizes the node.
func SuggestName(computer string, used []string) string {
	var label strings.Builder
	for _, c := range strings.ToLower(computer) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			label.WriteRune(c)
		} else {
			label.WriteByte('-')
		}
	}
	base := "rementor-" + strings.Trim(label.String(), "-")
	base = strings.TrimRight(base[:min(len(base), 56)], "-")
	occupied := map[string]bool{}
	for _, name := range used {
		occupied[strings.ToLower(strings.Split(name, ".")[0])] = true
	}
	for i := 1; ; i++ {
		candidate := base
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d", base, i)
		}
		if !occupied[candidate] {
			return candidate
		}
	}
}

// Prepare is called under the publisher's operation lock, after old listeners
// have been closed whenever the configuration changes.
func (n *Network) Prepare(ctx context.Context, name string) ([]string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.lastError = ""
	if name != "" && !ValidName(name) {
		return nil, errors.New("enter a Tailscale device name using letters, numbers and hyphens")
	}
	if n.name != name {
		if n.server != nil {
			_ = n.server.Close()
			n.server = nil
		}
		n.name = name
	}
	if name == "" {
		return nil, nil
	}
	if n.server == nil {
		dir := filepath.Join(n.dir, name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		n.server = n.factory(dir, name)
		if err := n.server.Start(); err != nil {
			_ = n.server.Close()
			n.server = nil
			n.lastError = "cannot start the publishing Tailscale node"
			return nil, errors.New(n.lastError)
		}
	}
	s, err := n.server.state(ctx)
	if err != nil {
		return nil, errors.New("cannot read the publishing Tailscale connection")
	}
	if s.Status != "Running" {
		return nil, errors.New("authorize the publishing address in DNS & HTTPS")
	}
	if n.ExpectedTailnet != nil {
		expected, err := n.ExpectedTailnet(ctx)
		if err != nil {
			n.lastError = err.Error()
			return nil, err
		}
		if expected == "" || s.Tailnet != expected {
			n.lastError = "authorize the publishing device in this computer's tailnet"
			return nil, errors.New(n.lastError)
		}
	}
	if len(s.Addresses) == 0 {
		return nil, errors.New("Tailscale has not assigned a publishing address yet")
	}
	return s.Addresses, nil
}
func (n *Network) State(ctx context.Context) State {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.server == nil {
		return State{Name: n.name, Error: n.lastError}
	}
	s, err := n.server.state(ctx)
	if err != nil {
		return State{Name: n.name, Error: "cannot read the publishing Tailscale connection"}
	}
	s.Error = n.lastError
	return s
}
func (n *Network) Listen(network, address string) (net.Listener, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.server == nil {
		return nil, errors.New("publishing Tailscale node is disconnected")
	}
	return n.server.Listen(network, address)
}
func (n *Network) ListenPacket(network, address string) (net.PacketConn, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.server == nil {
		return nil, errors.New("publishing Tailscale node is disconnected")
	}
	return n.server.ListenPacket(network, address)
}
func (n *Network) RoundTrip(r *http.Request) (*http.Response, error) {
	n.mu.Lock()
	server := n.server
	n.mu.Unlock()
	if server == nil {
		return nil, errors.New("publishing Tailscale node is disconnected")
	}
	return server.do(r)
}
func (n *Network) Close() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.server == nil {
		return nil
	}
	err := n.server.Close()
	n.server = nil
	return err
}
