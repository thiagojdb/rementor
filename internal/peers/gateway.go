package peers

import (
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/thiagojdb/rementor/internal/fleet"
)

type gatewayHeadersKey struct{}

// Gateway accepts only application forwarding. It has no management or
// enrollment routes and must be served on a loopback listener.
func (m *Manager) Gateway() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || !net.ParseIP(host).IsLoopback() {
			http.Error(w, "local gateway requires loopback access", http.StatusForbidden)
			return
		}
		values := r.Header.Values(endpointHeader)
		if len(values) != 1 || len(values[0]) > 4096 {
			http.Error(w, "invalid application endpoint", http.StatusBadRequest)
			return
		}
		raw, err := base64.RawURLEncoding.DecodeString(values[0])
		var endpoint fleet.EndpointRef
		if err != nil || readSnapshotJSON(strings.NewReader(string(raw)), &endpoint) != nil || fleet.ValidateEndpointRef(endpoint) != nil {
			http.Error(w, "invalid application endpoint", http.StatusBadRequest)
			return
		}
		endpointProof(w.Header(), endpoint)
		m.Forward(w, r.WithContext(context.WithValue(r.Context(), gatewayHeadersKey{}, true)), endpoint)
	})
}

// GatewayPort reserves a distinct loopback listener for each management
// instance. Explicit overrides let a deployment avoid an occupied port.
func GatewayPort(managementPort int, override string) (int, error) {
	port := managementPort + 1
	if override != "" {
		var err error
		port, err = strconv.Atoi(override)
		if err != nil {
			return 0, err
		}
	}
	if port < 1 || port > 65535 || port == managementPort {
		return 0, strconv.ErrRange
	}
	return port, nil
}
