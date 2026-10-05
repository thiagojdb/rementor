package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/thiagojdb/rementor/internal/access"
)

type IdentityResolver interface {
	Resolve(context.Context, string) (access.Identity, error)
}
type TailscaleIdentityResolver struct{ client *http.Client }

func NewTailscaleIdentityResolver(socket string) *TailscaleIdentityResolver {
	if socket == "" {
		socket = "/var/run/tailscale/tailscaled.sock"
	}
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", socket)
	}, ResponseHeaderTimeout: 5 * time.Second}
	return &TailscaleIdentityResolver{client: &http.Client{Transport: transport, Timeout: 5 * time.Second}}
}
func (resolver *TailscaleIdentityResolver) Resolve(ctx context.Context, address string) (access.Identity, error) {
	ip := net.ParseIP(address)
	if ip == nil || !isTailscaleAddress(ip) {
		return access.Identity{}, errors.New("request source is not a Tailscale address")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://local-tailscaled.sock/localapi/v0/whois?addr="+url.QueryEscape(ip.String()), nil)
	if err != nil {
		return access.Identity{}, err
	}
	res, err := resolver.client.Do(req)
	if err != nil {
		return access.Identity{}, errors.New("Tailscale device identity is unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return access.Identity{}, errors.New("Tailscale does not recognize this device")
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil {
		return access.Identity{}, err
	}
	if len(raw) > 1<<20 {
		return access.Identity{}, errors.New("Tailscale identity response is too large")
	}
	var identity struct {
		Node *struct {
			StableID          string
			User              uint64
			Tags              []string
			MachineAuthorized *bool
			Expired           bool
			KeyExpiry         time.Time
			Addresses         []string
		}
	}
	if err := json.Unmarshal(raw, &identity); err != nil || identity.Node == nil || identity.Node.StableID == "" {
		return access.Identity{}, errors.New("Tailscale device is not authorized")
	}
	node := identity.Node
	// WhoIs resolves a member of tailscaled's current network map. Peer nodes
	// can omit MachineAuthorized even when connected and permitted by Serve;
	// self nodes include it. Reject an explicit denial, rather than interpreting
	// missing peer metadata as denial. Unknown/removed nodes fail WhoIs above.
	if node.MachineAuthorized != nil && !*node.MachineAuthorized {
		return access.Identity{}, errors.New("Tailscale device is not authorized")
	}
	if node.Expired || (!node.KeyExpiry.IsZero() && !node.KeyExpiry.After(time.Now())) {
		return access.Identity{}, errors.New("Tailscale device identity has expired")
	}
	found := false
	for _, address := range node.Addresses {
		value, _, err := net.ParseCIDR(address)
		if err == nil && value.Equal(ip) {
			found = true
			break
		}
	}
	if !found {
		return access.Identity{}, errors.New("Tailscale identity does not own the source address")
	}
	return access.Identity{NodeID: node.StableID, UserID: node.User, Tagged: len(node.Tags) > 0}, nil
}
func isTailscaleAddress(ip net.IP) bool {
	_, v4, _ := net.ParseCIDR("100.64.0.0/10")
	_, v6, _ := net.ParseCIDR("fd7a:115c:a1e0::/48")
	return v4.Contains(ip) || v6.Contains(ip)
}

// SourceAddress accepts the single address Serve generated in the provider
// proof. It intentionally rejects forwarded chains and user identity headers.
func SourceAddress(request *http.Request) (string, error) {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return "", errors.New("application ingress requires the loopback Serve boundary")
	}
	values := request.Header.Values("X-Forwarded-For")
	if len(values) != 1 || strings.TrimSpace(values[0]) != values[0] || strings.Contains(values[0], ",") {
		return "", errors.New("missing or ambiguous Serve source address")
	}
	ip := net.ParseIP(values[0])
	if ip == nil || !isTailscaleAddress(ip) {
		return "", errors.New("invalid Serve source address")
	}
	protocols := request.Header.Values("X-Forwarded-Proto")
	if len(protocols) != 1 || protocols[0] != "https" {
		return "", errors.New("application ingress requires HTTPS through Serve")
	}
	return ip.String(), nil
}
