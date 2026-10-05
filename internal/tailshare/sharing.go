// Package tailshare owns named Tailscale publications, independently of route
// selection. Desired state and ownership survive failed setup and restarts.
package tailshare

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thiagojdb/rementor/internal/access"
)

var ErrConflict = errors.New("Tailscale sharing changed; reload and try again")
var labelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

type Origin struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	URL    string `json:"url"`
	Target string `json:"target"`
}
type Configuration struct {
	Version   uint64   `json:"-"`
	Revision  uint64   `json:"revision"`
	Owner     string   `json:"owner"`
	GatewayID string   `json:"gatewayId"`
	Enabled   bool     `json:"enabled"`
	Origins   []Origin `json:"origins"`
	Owned     []Origin `json:"owned"`
}

func (c Configuration) Clone() Configuration {
	c.Origins = slices.Clone(c.Origins)
	c.Owned = slices.Clone(c.Owned)
	return c
}
func (c Configuration) Validate() error {
	if len(c.Owner) != 32 || len(c.Origins) > 64 || len(c.Owned) > 128 {
		return errors.New("invalid Tailscale publication state")
	}
	if _, err := hex.DecodeString(c.Owner); err != nil {
		return errors.New("invalid publication owner")
	}
	if len(c.Owned) > 0 && (c.GatewayID == "" || len(c.GatewayID) > 128) {
		return errors.New("owned publications require their original gateway identity")
	}
	for _, entries := range [][]Origin{c.Origins, c.Owned} {
		seen := map[string]bool{}
		for _, o := range entries {
			u, err := url.Parse(o.URL)
			t, targetErr := url.Parse(o.Target)
			canonical, canonicalErr := access.CanonicalOrigin(o.URL)
			port := 0
			if targetErr == nil {
				port, _ = strconv.Atoi(t.Port())
			}
			if !labelPattern.MatchString(o.Name) || seen[o.Name] || err != nil || canonicalErr != nil || canonical != o.URL || u.Scheme != "https" || u.Host != u.Hostname() || !strings.HasPrefix(u.Host, o.Name+".") || !strings.HasSuffix(u.Host, ".ts.net") || u.Path != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || targetErr != nil || t.Scheme != "http" || t.Hostname() != "127.0.0.1" || port < 1 || port > 65535 || t.Path != "" || t.User != nil || t.RawQuery != "" || t.Fragment != "" {
				return errors.New("invalid named Tailscale origin")
			}
			seen[o.Name] = true
		}
	}
	return nil
}

type Store interface {
	Load() (Configuration, error)
	Save(Configuration, uint64) (Configuration, error)
}
type Gateway struct {
	NodeID    string
	DNSSuffix string
	Name      string
	Setup     string
	CanCreate bool
}
type Provider interface {
	Inspect(context.Context) (Gateway, error)
	Ensure(context.Context, Origin, Ownership, func() error) error
	Remove(context.Context, Origin) error
	Verify(context.Context, Origin, access.Configuration) error
}
type Ownership struct {
	Publication  string
	Installation string
	Claimed      bool
}
type Publication struct {
	Origin  Origin
	Status  string
	Message string
}
type State struct {
	Revision     uint64
	Enabled      bool
	Gateway      Gateway
	Publications []Publication
	Error        string
}
type Manager struct {
	opMu       sync.Mutex
	mu         sync.Mutex
	config     Configuration
	state      State
	store      Store
	provider   Provider
	policy     func() access.Configuration
	wake       chan struct{}
	workCancel context.CancelFunc
	nextOrigin int
	// SharedOwner returns a stable shared-ingress ID only for an enrolled
	// gateway. Set before Run, so equivalent gateways use the same marker.
	SharedOwner func() string
	Target      func() string
}

func New(store Store, provider Provider, policy func() access.Configuration) (*Manager, error) {
	c, err := store.Load()
	if err != nil {
		return nil, err
	}
	if c.Owner == "" && c.Version == 0 {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return nil, err
		}
		c.Owner = hex.EncodeToString(id[:])
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	m := &Manager{store: store, provider: provider, policy: policy, config: c, wake: make(chan struct{}, 1)}
	m.state = State{Revision: c.Revision, Enabled: c.Enabled}
	return m, nil
}
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.state
	s.Publications = slices.Clone(s.Publications)
	return s
}
func (m *Manager) Notify() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}
func (m *Manager) Set(enabled bool, origins []Origin, expected uint64) (State, error) {
	m.mu.Lock()
	if m.workCancel != nil {
		m.workCancel()
	}
	m.mu.Unlock()
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if expected != m.config.Revision {
		return m.State(), ErrConflict
	}
	next := m.config.Clone()
	next.Enabled = enabled
	next.Revision++
	if enabled {
		if len(origins) == 0 {
			return m.State(), errors.New("choose an application to share")
		}
		next.Origins = slices.Clone(origins)
	}
	if err := next.Validate(); err != nil {
		return m.State(), err
	}
	saved, err := m.store.Save(next, m.config.Version)
	if err != nil {
		return m.State(), err
	}
	m.config = saved
	m.mu.Lock()
	m.state.Revision = saved.Revision
	m.state.Enabled = enabled
	m.state.Error = ""
	m.state.Publications = nil
	for _, origin := range saved.Origins {
		status := "setting_up"
		if !enabled {
			status = "removing"
		}
		m.state.Publications = append(m.state.Publications, Publication{Origin: origin, Status: status})
	}
	m.mu.Unlock()
	m.Notify()
	return m.State(), nil
}

// Origins accepts only service names in this gateway's actual tailnet. It
// deduplicates aliases without collapsing different application origins.
func Origins(doc access.Document, suffix, target string) ([]Origin, error) {
	if suffix == "" {
		return nil, errors.New("connect a Tailscale gateway before sharing")
	}
	var result []Origin
	seen := map[string]bool{}
	for _, entry := range doc.Origins {
		canonical, err := access.CanonicalOrigin(entry.URL)
		if err != nil {
			return nil, err
		}
		u, _ := url.Parse(canonical)
		name := strings.TrimSuffix(u.Hostname(), "."+suffix)
		if u.Scheme != "https" || u.Port() != "" || u.Hostname() != name+"."+suffix || !labelPattern.MatchString(name) {
			return nil, fmt.Errorf("%s must use a named HTTPS service in %s, without a port", entry.ID, suffix)
		}
		if !seen[name] {
			result = append(result, Origin{ID: entry.ID, Name: name, URL: canonical, Target: target})
			seen[name] = true
		}
	}
	return result, nil
}
func SuggestedName(host string) string {
	name := strings.TrimSuffix(strings.ToLower(host), ".localhost")
	var b strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	name = strings.Trim(b.String(), "-")
	if len(name) > 63 {
		name = strings.TrimRight(name[:54], "-") + "-" + shortHash(host)
	}
	if name == "" {
		name = "rementor"
	}
	return name
}

// SuggestedNames distinguishes hostnames whose flattened labels collide.
func SuggestedNames(hosts []string) map[string]string {
	result := map[string]string{}
	owners := map[string]string{}
	for _, host := range hosts {
		if _, exists := result[host]; exists {
			continue
		}
		name := SuggestedName(host)
		result[host] = name
		if previous, ok := owners[name]; ok && previous != host {
			prefix := name
			if len(prefix) > 54 {
				prefix = prefix[:54]
			}
			prefix = strings.TrimRight(prefix, "-")
			result[previous] = prefix + "-" + shortHash(previous)
			result[host] = prefix + "-" + shortHash(host)
		}
		owners[name] = host
	}
	return result
}
func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:4])
}
func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		m.Reconcile(ctx)
		select {
		case <-ctx.Done():
			return
		case <-m.wake:
		case <-ticker.C:
		}
	}
}
func (m *Manager) Reconcile(parent context.Context) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	m.mu.Lock()
	m.workCancel = cancel
	m.mu.Unlock()
	s := State{Revision: m.config.Revision, Enabled: m.config.Enabled}
	defer func() {
		slices.SortFunc(s.Publications, func(a, b Publication) int { return strings.Compare(a.Origin.Name, b.Origin.Name) })
		m.mu.Lock()
		m.state = s
		m.workCancel = nil
		m.mu.Unlock()
	}()
	gateway, err := m.provider.Inspect(ctx)
	s.Gateway = gateway
	if err != nil {
		s.Error = err.Error()
		for _, origin := range m.config.Origins {
			s.Publications = append(s.Publications, Publication{Origin: origin, Status: "unavailable", Message: s.Error})
		}
		return
	}
	if len(m.config.Owned) > 0 && (gateway.NodeID == "" || gateway.NodeID != m.config.GatewayID) {
		s.Error = "The Tailscale gateway identity changed. Restore the original gateway socket to reconcile its owned entries."
		for _, origin := range m.config.Origins {
			s.Publications = append(s.Publications, Publication{Origin: origin, Status: "needs_attention", Message: s.Error})
		}
		return
	}
	policy := m.policy()
	policyValid := true
	if m.config.Enabled && len(m.config.Origins) > 0 {
		target := m.config.Origins[0].Target
		if m.Target != nil {
			target = m.Target()
		}
		// Access-policy changes are authorized publication changes. Equivalent
		// gateways follow the shared policy, never their own route selection.
		origins, err := Origins(policy.Document, gateway.DNSSuffix, target)
		if err != nil {
			policyValid = false
			s.Error = err.Error()
		}
		if err == nil && len(origins) > 0 && !slices.Equal(origins, m.config.Origins) {
			next := m.config.Clone()
			next.Origins = origins
			if err := m.persist(next); err != nil {
				s.Error = err.Error()
				return
			}
		}
	}
	desired := map[string]Origin{}
	if m.config.Enabled && policyValid {
		for _, origin := range m.config.Origins {
			for _, allowed := range policy.Document.Origins {
				if allowed.URL == origin.URL {
					desired[origin.Name] = origin
					break
				}
			}
		}
	}
	// Retain failed removals durably. Never forget ownership before cleanup,
	// including when the user turns sharing off or removes an access origin.
	failedRemovals := map[string]bool{}
	for _, owned := range slices.Clone(m.config.Owned) {
		wanted, exists := desired[owned.Name]
		if exists && wanted == owned {
			continue
		}
		if err := m.provider.Remove(ctx, owned); err != nil {
			failedRemovals[owned.Name] = true
			s.Publications = append(s.Publications, Publication{Origin: owned, Status: "cleanup_failed", Message: err.Error()})
			continue
		}
		next := m.config.Clone()
		next.Owned = slices.DeleteFunc(next.Owned, func(o Origin) bool { return o.Name == owned.Name })
		if err := m.persist(next); err != nil {
			s.Error = err.Error()
			return
		}
	}
	if !m.config.Enabled {
		for _, origin := range m.config.Origins {
			if !failedRemovals[origin.Name] {
				s.Publications = append(s.Publications, Publication{Origin: origin, Status: "off"})
			}
		}
		return
	}
	start := m.nextOrigin
	exhausted := false
	for offset := 0; offset < len(m.config.Origins); offset++ {
		index := (start + offset) % len(m.config.Origins)
		origin := m.config.Origins[index]
		if failedRemovals[origin.Name] {
			continue
		}
		p := Publication{Origin: origin, Status: "setting_up"}
		if ctx.Err() != nil {
			if !exhausted {
				m.nextOrigin = index
				exhausted = true
			}
			p.Message = "Queued for the next registration check."
			s.Publications = append(s.Publications, p)
			continue
		}
		if _, ok := desired[origin.Name]; !ok {
			p.Status = "policy_changed"
			p.Message = "This URL is no longer in the access policy. Configure sharing again."
			s.Publications = append(s.Publications, p)
			continue
		}
		if gateway.Setup != "" {
			p.Status = "needs_setup"
			p.Message = gateway.Setup
			s.Publications = append(s.Publications, p)
			continue
		}
		owned := slices.Contains(m.config.Owned, origin)
		claim := func() error {
			if owned {
				return nil
			}
			next := m.config.Clone()
			next.GatewayID = gateway.NodeID
			next.Owned = append(next.Owned, origin)
			return m.persist(next)
		}
		owner := m.config.Owner
		if m.SharedOwner != nil {
			if group := m.SharedOwner(); group != "" {
				owner = group
			}
		}
		if err := m.provider.Ensure(ctx, origin, Ownership{Publication: owner, Installation: m.config.Owner, Claimed: owned}, claim); err != nil {
			p.Status = "needs_attention"
			p.Message = err.Error()
		} else if err := m.provider.Verify(ctx, origin, policy); err != nil {
			p.Status = "awaiting_verification"
			p.Message = err.Error()
		} else {
			p.Status = "ready"
		}
		s.Publications = append(s.Publications, p)
		if ctx.Err() != nil {
			m.nextOrigin = (index + 1) % len(m.config.Origins)
		}
	}
}
func (m *Manager) persist(next Configuration) error {
	saved, err := m.store.Save(next, m.config.Version)
	if err == nil {
		m.config = saved
	}
	return err
}
