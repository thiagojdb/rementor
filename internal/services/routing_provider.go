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
