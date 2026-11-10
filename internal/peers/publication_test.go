package peers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thiagojdb/rementor/internal/publication"
)

type publicationReceiver struct {
	t     *testing.T
	owner string
}

func (p publicationReceiver) ServePublishedHTTP(w http.ResponseWriter, r *http.Request, metadata publication.Request) {
	if metadata.Computer != p.owner || metadata.WorkspaceID != "xml" || metadata.SourceAddress != "100.64.0.2" {
		p.t.Errorf("publication target changed: %+v", metadata)
	}
	if r.RequestURI != "/v1/publication-forward/service/a%2Fb?q=two" || r.URL.Path != "/service/a/b" || r.URL.RawPath != "/service/a%2Fb" || r.Host != "xml.dev.marcus.example.test" {
		p.t.Errorf("request path/host changed: %+v", r.URL)
	}
	if r.Header.Get("X-ReMentor-Forged") != "" || r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Tailscale-User-Login") != "" {
		p.t.Error("spoofed forwarding identity survived")
	}
	if len(metadata.Links) != 1 || metadata.Links[0].LocalHost != "xml.dev.localhost" {
		p.t.Error("routing hosts changed")
	}
	w.WriteHeader(201)
	_, _ = io.WriteString(w, "owner")
}
func TestPublicationForwardPinsComputerAndRevokesPairing(t *testing.T) {
	client, owner := testManager(t, "client"), testManager(t, "owner")
	code, invitation := invite(t, owner)
	if _, err := client.Pair(context.Background(), code, invitation.Fingerprint); err != nil {
		t.Fatal(err)
	}
	owner.SetPublicationIngress(publicationReceiver{t, owner.State().Fingerprint})
	metadata := publication.Request{Computer: owner.State().Fingerprint, WorkspaceID: "xml", SourceAddress: "100.64.0.2", Links: []publication.Link{{LocalHost: "xml.dev.localhost", URL: "https://xml.dev.marcus.example.test"}}}
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "https://xml.dev.marcus.example.test/service/a%2Fb?q=two", nil)
		r.Header.Set("X-ReMentor-Forged", "true")
		r.Header.Set("X-Forwarded-For", "spoof")
		r.Header.Set("Tailscale-User-Login", "spoof")
		return r
	}
	response := httptest.NewRecorder()
	client.ForwardPublication(response, request(), metadata)
	if response.Code != 201 || response.Body.String() != "owner" {
		t.Fatalf("forward failed: %d %s", response.Code, response.Body.String())
	}
	if err := client.Revoke(metadata.Computer); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	client.ForwardPublication(response, request(), metadata)
	if response.Code != 403 || !strings.Contains(response.Body.String(), "not paired") {
		t.Fatal("unpaired publication still routed", response.Code)
	}
}
