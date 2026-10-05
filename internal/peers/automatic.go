package peers

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// Automatic invitations prefer Tailscale, then a private network interface.
// Loopback and container bridges cannot make an invitation usable by another
// computer, so they remain available only through explicit connection settings.
func invitationAddresses() []string {
	interfaces, _ := net.Interfaces()
	var addresses []string
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || strings.HasPrefix(iface.Name, "docker") || strings.HasPrefix(iface.Name, "br-") || strings.HasPrefix(iface.Name, "veth") || strings.HasPrefix(iface.Name, "virbr") {
			continue
		}
		values, _ := iface.Addrs()
		for _, value := range values {
			ip, _, err := net.ParseCIDR(value.String())
			if err == nil && privateIP(ip) && !ip.IsLoopback() {
				addresses = append(addresses, ip.String())
			}
		}
	}
	sort.Slice(addresses, func(i, j int) bool {
		a, b := addressPriority(addresses[i]), addressPriority(addresses[j])
		if a != b {
			return a < b
		}
		return addresses[i] < addresses[j]
	})
	return addresses
}

func addressPriority(address string) int {
	ip := net.ParseIP(address)
	_, tailnet, _ := net.ParseCIDR("100.64.0.0/10")
	if ip != nil && tailnet.Contains(ip) {
		return 0
	}
	if ip != nil && ip.To4() != nil {
		return 1
	}
	return 2
}

// Callers already hold mu. Starting the listener and issuing the invitation
// must serialize with disable/revoke and other simultaneous invitation calls.
func (m *Manager) ensureConnectionLocked() error {
	if m.server != nil {
		return nil
	}
	if m.state.Listen != "" {
		return m.configureLocked(m.state.Listen, m.state.URL)
	}
	for _, ip := range m.autoAddresses() {
		if !privateIP(net.ParseIP(ip)) {
			continue
		}
		for port := 9443; port < 9459; port++ {
			listen := net.JoinHostPort(ip, strconv.Itoa(port))
			err := m.configureLocked(listen, "https://"+listen)
			if err == nil {
				return nil
			}
			if !errors.Is(err, syscall.EADDRINUSE) {
				return fmt.Errorf("enable private connection: %w", err)
			}
		}
	}
	return errors.New("no private address or free pairing port available; connect to your private network or choose an address in Connection settings")
}
