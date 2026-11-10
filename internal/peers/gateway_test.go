package peers

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGatewayRejectsRemoteAccessAndInvalidEndpointHeaders(t *testing.T) {
	client, _, _, endpoint, _, _ := forwardingFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("invalid gateway request reached upstream") }))
	raw, _ := json.Marshal(endpoint)
	valid := base64.RawURLEncoding.EncodeToString(raw)
	for _, test := range []struct {
		name    string
		remote  string
		headers []string
		status  int
	}{
		{"private-network", "192.168.1.50:1234", []string{valid}, 403},
		{"missing-address", "", []string{valid}, 403},
		{"missing-endpoint", "127.0.0.1:1234", nil, 400},
		{"duplicate-endpoint", "127.0.0.1:1234", []string{valid, valid}, 400},
		{"malformed-endpoint", "127.0.0.1:1234", []string{"broken"}, 400},
		{"unknown-fields", "127.0.0.1:1234", []string{base64.RawURLEncoding.EncodeToString([]byte(`{"url":"http://127.0.0.1:9300"}`))}, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://localhost/rpc/rementor.v1.ControlPlaneService/ListWorkspaces", nil)
			request.RemoteAddr = test.remote
			for _, header := range test.headers {
				request.Header.Add(endpointHeader, header)
			}
			response := httptest.NewRecorder()
			client.Gateway().ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d, want %d", response.Code, test.status)
			}
		})
	}
	unknown := endpoint
	unknown.OwnerFingerprint = client.State().Fingerprint
	raw, _ = json.Marshal(unknown)
	request := httptest.NewRequest(http.MethodGet, "http://localhost/orders", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set(endpointHeader, base64.RawURLEncoding.EncodeToString(raw))
	response := httptest.NewRecorder()
	client.Gateway().ServeHTTP(response, request)
	if response.Code != 403 || response.Header().Get("X-Rementor-Endpoint-Owner") != unknown.OwnerFingerprint {
		t.Fatal("unpaired owner was accepted or lost intended endpoint proof")
	}
}
