package nginx

import (
	"net"
	"os"
	"strings"
)

// Use the same DNS servers as this computer, including systemd-resolved or
// Tailscale stubs. Never send private upstream names to a public fallback.
func systemResolvers() []string {
	data, _ := os.ReadFile("/etc/resolv.conf")
	return resolversFromConfig(string(data))
}

func resolversFromConfig(config string) []string {
	var resolvers []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(config, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		ip := net.ParseIP(fields[1])
		if ip == nil {
			continue
		}
		address := ip.String()
		if ip.To4() == nil {
			address = "[" + address + "]"
		}
		if !seen[address] {
			resolvers = append(resolvers, address)
			seen[address] = true
		}
	}
	return resolvers
}
