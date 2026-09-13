package nginx

import (
	"fmt"
	"github.com/thiagojdb/rementor/internal/models"
	"strings"
	"testing"
)

func largeRoutingFixture() []*models.Workspace {
	var workspaces []*models.Workspace
	for w := 0; w < 30; w++ {
		ws := &models.Workspace{WorkspaceID: fmt.Sprintf("env-%d", w), Type: "routing", RoutingConfig: &models.RoutingConfig{LocalDomain: fmt.Sprintf("env-%d.localhost", w), DefaultRemoteBaseURL: "http://127.0.0.1:19999"}}
		for a := 0; a < 37; a++ {
			id := fmt.Sprintf("app-%d", a)
			ws.Applications = append(ws.Applications, &models.Application{ID: id, AppID: id, Path: "/" + id, Context: "/" + id, Port: 10000 + a})
		}
		ws.Applications[0].Domain = fmt.Sprintf("front-%d.localhost", w)
		workspaces = append(workspaces, ws)
	}
	return workspaces
}

func TestLargeConfigSharesResponseHeaders(t *testing.T) {
	rendered, err := RenderConfig(largeRoutingFixture(), "rementor.localhost")
	if err != nil {
		t.Fatal(err)
	}
	locations := strings.Count(rendered, "location ")
	if len(rendered) > 1_000_000 {
		t.Fatalf("configuration repeats too much per route: %d bytes for %d locations", len(rendered), locations)
	}
}

func BenchmarkRenderLargeConfig(b *testing.B) {
	ws := largeRoutingFixture()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := RenderConfig(ws, "rementor.localhost"); err != nil {
			b.Fatal(err)
		}
	}
}

func TestRenderCacheTracksRoutingInputs(t *testing.T) {
	setTestRementorDomain(t)
	provider := &RoutingProvider{}
	ws := largeRoutingFixture()[:2]
	first, err := provider.render(ws)
	if err != nil {
		t.Fatal(err)
	}
	second, err := provider.render(ws)
	if err != nil || first != second {
		t.Fatal("unchanged configuration did not render identically", err)
	}
	ws[0].Applications[1].Active = true
	ws[0].Route = models.RouteState{RouteVersion: 2, OperationID: "op-2"}
	changed, err := provider.render(ws)
	if err != nil || changed == first {
		t.Fatal("toggle did not invalidate cached workspace", err)
	}
	fresh, err := (&RoutingProvider{}).render(ws)
	if err != nil || fresh != changed {
		t.Fatal("cached render differs from fresh render", err)
	}
	ws[0].Applications[1].PublicPath = "/public"
	ws[0].Applications[1].Path = "/public"
	ws[0].Applications[1].Context = "/backend"
	ws[0].Applications[1].UpstreamContext = "/backend"
	ws[0].Applications[1].LegacyPublicPath = true
	legacy, err := provider.render(ws)
	if err != nil {
		t.Fatal(err)
	}
	ws[0].Applications[1].LegacyPublicPath = false
	explicit, err := provider.render(ws)
	if err != nil || explicit == legacy {
		t.Fatal("legacy routing semantics did not invalidate cache", err)
	}
}
