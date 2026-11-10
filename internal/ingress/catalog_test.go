package ingress

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestServeDiscoveryDoesNotModifyExistingPublications(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		ports        []string
		blocked      bool
	}{
		{"free", `{}`, []string{}, false},
		{"other ports", `{"TCP":{"8443":{"HTTPS":true}}}`, []string{"8443"}, false},
		{"background", `{"TCP":{"443":{"HTTPS":true},"8443":{"HTTPS":true}}}`, []string{"443", "8443"}, true},
		{"foreground", `{"Foreground":{"session":{"TCP":{"443":{"TCPForward":"127.0.0.1:9000"}}}}}`, []string{"443"}, true},
		{"service address", `{"Services":{"svc:example":{"TCP":{"443":{"HTTPS":true}}}}}`, []string{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/localapi/v0/serve-config" {
					t.Error("modified an existing publication")
				}
				fmt.Fprint(w, tc.config)
			}))
			defer s.Close()
			resolver := NewTailscaleClientIdentityResolver(&http.Client{Transport: rewriteTransport{s.URL, http.DefaultTransport}})
			ports, err := resolver.ServePorts(context.Background())
			if err != nil || !reflect.DeepEqual(ports, tc.ports) {
				t.Fatal(ports, err)
			}
			if err := resolver.CheckDirectHTTPS(context.Background()); (err != nil) != tc.blocked {
				t.Fatal("wrong port ownership", err)
			}
		})
	}
}

type rewriteTransport struct {
	base      string
	transport http.RoundTripper
}

func (t rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	u, err := http.NewRequest(r.Method, t.base+r.URL.RequestURI(), nil)
	if err != nil {
		return nil, err
	}
	r.URL = u.URL
	return t.transport.RoundTrip(r)
}
