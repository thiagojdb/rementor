package peers

import (
	"context"
	"net/http"
	"testing"
)

func TestPeerEndpointHealthUsesDeclaredOwnerAndRejectsStaleOrRevokedReferences(t *testing.T) {
	client, owner, source, endpoint, _, upstream := forwardingFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/orders/health" || r.Header.Get(endpointHeader) != "" || r.Host != "localhost" {
			t.Error("health probe leaked metadata or changed path")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	if healthy, err := client.ProbeEndpoint(context.Background(), endpoint, "/orders/health"); err != nil || !healthy {
		t.Fatalf("healthy endpoint rejected: %v", err)
	}
	if healthy, err := client.ProbeEndpoint(context.Background(), endpoint, "http://127.0.0.1:9300"); err == nil || healthy {
		t.Fatal("health accepted a caller URL")
	}
	source.mu.Lock()
	source.version++
	source.mu.Unlock()
	if healthy, err := client.ProbeEndpoint(context.Background(), endpoint, "/orders/health"); err == nil || healthy {
		t.Fatal("stale snapshot was reported healthy")
	}
	source.mu.Lock()
	source.version--
	source.mu.Unlock()
	upstream.Close()
	if healthy, err := client.ProbeEndpoint(context.Background(), endpoint, "/orders/health"); err == nil || healthy {
		t.Fatal("stopped process was reported healthy")
	}
	if err := owner.Revoke(client.State().Fingerprint); err != nil {
		t.Fatal(err)
	}
	if healthy, err := client.ProbeEndpoint(context.Background(), endpoint, "/orders/health"); err == nil || healthy {
		t.Fatal("revoked connection was reported healthy")
	}
}
