package rpc

import (
	"github.com/thiagojdb/rementor/internal/models"
	"testing"
	"time"
)

func TestHealthUpdateDoesNotInventUncheckedEndpointTimestamp(t *testing.T) {
	response := toProtoHealthUpdate(models.HealthUpdate{LocalChecked: time.Now()}, nil)
	if response.LocalCheckedAt == nil || response.LocalChecked == "" {
		t.Fatal("checked endpoint timestamp was lost")
	}
	if response.RemoteCheckedAt != nil || response.RemoteChecked != "" {
		t.Fatal("unchecked remote endpoint was represented as checked")
	}
}
