package access

import (
	"strings"
	"testing"
)

func TestRoutingWorkspaceCatalogRejectsForeignIdentityAndAmbiguousEntries(t *testing.T) {
	owner := strings.Repeat("a", 64)
	base := func() WorkspaceCatalog {
		return WorkspaceCatalog{SchemaVersion: 1, OwnerFingerprint: owner, Workspaces: []WorkspaceHosts{{WorkspaceID: "dev", Name: "Routing development", LocalHosts: []string{"dev.localhost"}}}}
	}
	if err := base().Validate(owner); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*WorkspaceCatalog){
		"foreign owner":       func(c *WorkspaceCatalog) { c.OwnerFingerprint = strings.Repeat("b", 64) },
		"foreign schema":      func(c *WorkspaceCatalog) { c.SchemaVersion = 2 },
		"duplicate workspace": func(c *WorkspaceCatalog) { c.Workspaces = append(c.Workspaces, c.Workspaces[0]) },
		"duplicate host": func(c *WorkspaceCatalog) {
			c.Workspaces[0].LocalHosts = append(c.Workspaces[0].LocalHosts, "dev.localhost")
		},
		"remote URL":        func(c *WorkspaceCatalog) { c.Workspaces[0].LocalHosts = []string{"https://dev.example.test"} },
		"invalid workspace": func(c *WorkspaceCatalog) { c.Workspaces[0].WorkspaceID = "../dev" },
		"missing hosts":     func(c *WorkspaceCatalog) { c.Workspaces[0].LocalHosts = nil },
	} {
		t.Run(name, func(t *testing.T) {
			c := base()
			mutate(&c)
			if c.Validate(owner) == nil {
				t.Fatal("accepted invalid catalog")
			}
		})
	}
}
