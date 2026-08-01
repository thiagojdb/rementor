package services

import (
	"net/http"
	"time"

	"github.com/thiagojdb/rementor/internal/models"
)

// RoutingProvider defines the interface for routing providers.
// Implementations must support full config replacement via LoadInitialConfig.
// The old granular mutation methods (SwitchToLocal, SwitchToRemote, etc.)
// have been removed because they are redundant with full-reload and create
// multiple paths for staleness.
type RoutingProvider interface {
	IsAvailable() bool
	LoadInitialConfig(workspaces []*models.Workspace) error
	Close() error
}

// RoutingVerifier is an optional extension implemented by providers that can
// inspect the configuration currently loaded by the proxy.  LoadInitialConfig
// remains the compatibility boundary for existing providers; when this hook
// is present the registry uses it to close the apply/verify boundary before
// persisting desired state.
type RoutingVerifier interface {
	VerifyRouting(workspaces []*models.Workspace) error
}

// RoutingInspector is an optional read-only drift check.  Providers that can
// compare their loaded projection with a candidate should implement it so a
// sync operation can report external proxy drift instead of relying only on
// the registry's last successful apply.
type RoutingInspector interface {
	InspectRouting(workspaces []*models.Workspace) (bool, error)
}

// ToggleResult represents the result of a toggle all operation
type ToggleResult struct {
	SuccessCount int
	FailureCount int
}

// CheckHealth checks if a URL is healthy
func CheckHealth(url string) bool {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
