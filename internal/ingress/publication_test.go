package ingress_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/thiagojdb/rementor/internal/access"
	"github.com/thiagojdb/rementor/internal/config"
	"github.com/thiagojdb/rementor/internal/ingress"
	"github.com/thiagojdb/rementor/internal/models"
	"github.com/thiagojdb/rementor/internal/publication"
	"github.com/thiagojdb/rementor/internal/services"
)

func TestPublicationManifestPinsWorkspaceAndPreservesRuntimeOrigins(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	previous := config.Config
	config.Config.RementorDomain = config.DefaultRementorDomain
	t.Cleanup(func() { config.Config = previous })
	for _, id := range []string{"dev", "qua"} {
		if err := config.AppendWorkspace(models.WorkspaceConfig{ID: id, Name: id, Type: "routing", Routing: models.RoutingConfig{LocalDomain: id + ".giss.localhost"}, Applications: []models.ApplicationConfig{{ID: "frontend", PublicPath: "/", Path: "/", Domain: id + ".giss.localhost", RemoteBaseUrl: "https://example.test"}}}); err != nil {
			t.Fatal(err)
		}
	}
	registry := &services.Registry{}
	registry.SetRoutingProvider(noProxy{})
	if err := registry.Load(); err != nil {
		t.Fatal(err)
	}
	handler, err := ingress.New(registry, identityStub{}, "http://127.0.0.1:18080", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	// The same phone selects the hostname's environment, never a device default.
	for _, id := range []string{"dev", "qua"} {
		host := id + ".giss.marcus.fleet.example.test"
		metadata := publication.Request{WorkspaceID: id, SourceAddress: "100.110.60.50", Links: []publication.Link{{LocalHost: id + ".giss.localhost", URL: "https://" + host}}}
		r := httptest.NewRequest(http.MethodGet, "https://"+host+ingress.ManifestPath, nil)
		response := httptest.NewRecorder()
		handler.ServePublishedHTTP(response, r, metadata)
		if response.Code != 200 {
			t.Fatal(response.Code, response.Body.String())
		}
		var manifest access.Manifest
		if err := json.Unmarshal(response.Body.Bytes(), &manifest); err != nil {
			t.Fatal(err)
		}
		if manifest.WorkspaceID != id || manifest.SelectionSource != "hostname" || manifest.Origins["api"] != "https://"+host || manifest.Origins["frontend"] != "https://"+host || manifest.WebSocketOrigins["api"] != "wss://"+host {
			t.Fatalf("runtime origins changed: %+v", manifest)
		}
		metadata.Links[0].LocalHost = config.DefaultRementorDomain
		response = httptest.NewRecorder()
		handler.ServePublishedHTTP(response, r, metadata)
		if response.Code == 200 {
			t.Fatal("published management host")
		}
	}
}
