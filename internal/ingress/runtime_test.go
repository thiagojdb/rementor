package ingress

import (
	"context"
	"net"
	"testing"

	"github.com/thiagojdb/rementor/internal/access"
	"github.com/thiagojdb/rementor/internal/services"
)

type unavailableSource struct{}

func (unavailableSource) BeginPublicRequest(context.Context, access.Identity, string, string, bool) (context.Context, services.AccessRequest, func(), error) {
	return nil, services.AccessRequest{}, nil, access.ErrNotAuthorized
}
func (unavailableSource) PublicAccessConfiguration() access.Configuration {
	return access.Configuration{Document: access.Empty()}
}

type unavailableIdentity struct{}

func (unavailableIdentity) Resolve(context.Context, string) (access.Identity, error) {
	return access.Identity{}, access.ErrNotAuthorized
}
func TestPublicRuntimeReportsCollisionAndCanRetry(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(unavailableSource{}, unavailableIdentity{}, occupied.Addr().String(), "http://127.0.0.1:18080", nil)
	defer runtime.Close()
	if err := runtime.Start(); err == nil || runtime.State().Active || runtime.State().Error == "" {
		t.Fatal("publication ignored port collision")
	}
	occupied.Close()
	if err := runtime.Start(); err != nil || !runtime.State().Active || runtime.State().Error != "" {
		t.Fatal("publication could not retry", err)
	}
	runtime.Close()
	if runtime.State().Active {
		t.Fatal("runtime stayed active")
	}
	runtime = NewRuntime(unavailableSource{}, unavailableIdentity{}, "0.0.0.0:9302", "http://127.0.0.1:18080", nil)
	if runtime.Start() == nil {
		runtime.Close()
		t.Fatal("runtime exposed public backend on network interface")
	}
}
