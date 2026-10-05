package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
)

type Device struct {
	NodeID    string
	UserID    uint64
	Name      string
	DNSName   string
	Addresses []string
	Online    bool
	Tagged    bool
	Self      bool
}

func (resolver *TailscaleIdentityResolver) Devices(ctx context.Context) ([]Device, error) {
	request, err := http.NewRequestWithContext(ctx, "GET", "http://local-tailscaled.sock/localapi/v0/status", nil)
	if err != nil {
		return nil, err
	}
	response, err := resolver.client.Do(request)
	if err != nil {
		return nil, errors.New("Tailscale is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, errors.New("Tailscale device catalog is unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 8<<20 {
		return nil, errors.New("Tailscale device catalog is too large")
	}
	type node struct {
		ID           string
		UserID       uint64
		HostName     string
		DNSName      string
		TailscaleIPs []string
		Online       bool
		Tags         []string
	}
	var catalog struct {
		BackendState string
		Self         *node
		Peer         map[string]*node
	}
	if err := json.Unmarshal(raw, &catalog); err != nil || catalog.BackendState != "Running" || catalog.Self == nil {
		return nil, errors.New("Tailscale must be connected")
	}
	convert := func(n *node, self bool) Device {
		name := n.HostName
		if n.DNSName != "" {
			name = strings.Split(n.DNSName, ".")[0]
		}
		return Device{NodeID: n.ID, UserID: n.UserID, Name: name, DNSName: strings.TrimSuffix(n.DNSName, "."), Addresses: n.TailscaleIPs, Online: n.Online, Tagged: len(n.Tags) > 0, Self: self}
	}
	result := []Device{convert(catalog.Self, true)}
	for _, n := range catalog.Peer {
		if n != nil && n.ID != "" {
			result = append(result, convert(n, false))
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Self != result[j].Self {
			return result[i].Self
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}
func (resolver *TailscaleIdentityResolver) Close() { resolver.client.CloseIdleConnections() }
