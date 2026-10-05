package peers

import "errors"

// WithTrustedPeer serializes a short local commit with revocation/disable.
// Network fetching happens before this guard; the callback must not call back
// into Manager or perform peer network requests.
func (m *Manager) WithTrustedPeer(id string, commit func() error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.state.Peers[id]; !ok || m.server == nil {
		return errors.New("peer connection is disabled or computer is not paired")
	}
	return commit()
}
