package ingress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/thiagojdb/rementor/internal/peers"
	"github.com/thiagojdb/rementor/internal/shared"
)

type State struct {
	ListenAddress string
	NginxAddress  string
	Active        bool
	Error         string
}

// Runtime owns a loopback data-plane server independently of management.
// A collision disables publication and remains visible through management;
// existing localhost routing and peer forwarding continue to function.
type Runtime struct {
	mu            sync.Mutex
	state         State
	source        Source
	identity      IdentityResolver
	protectedPort func(int) bool
	server        *http.Server
	handler       *Handler
	cancel        context.CancelFunc
	authority     *shared.Authority
	peers         *peers.Manager
}

func PublicPort(management int, override string) (int, error) {
	port := management + 2
	if override != "" {
		value, err := strconv.Atoi(override)
		if err != nil {
			return 0, errors.New("public ingress port must be an integer")
		}
		port = value
	}
	if port < 1 || port > 65535 || port == management {
		return 0, errors.New("public ingress port must be a distinct TCP port between 1 and 65535")
	}
	return port, nil
}
func NewRuntime(source Source, identity IdentityResolver, listen, nginxAddress string, protected func(int) bool) *Runtime {
	return &Runtime{source: source, identity: identity, protectedPort: protected, state: State{ListenAddress: listen, NginxAddress: nginxAddress}}
}

// ConfigureShared must run before Start; local publication remains available
// when no shared group has been explicitly configured.
func (runtime *Runtime) ConfigureShared(authority *shared.Authority, manager *peers.Manager) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.authority, runtime.peers = authority, manager
}
func (runtime *Runtime) State() State {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.state
}
func (runtime *Runtime) Start() error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.server != nil {
		return nil
	}
	host, _, err := net.SplitHostPort(runtime.state.ListenAddress)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return runtime.fail(errors.New("public data plane must listen on loopback"))
	}
	handler, err := New(runtime.source, runtime.identity, runtime.state.NginxAddress, runtime.protectedPort)
	if err != nil {
		return runtime.fail(err)
	}
	listener, err := net.Listen("tcp", runtime.state.ListenAddress)
	if err != nil {
		handler.Close()
		return runtime.fail(fmt.Errorf("listen on public data plane: %w", err))
	}
	if runtime.authority != nil {
		if err := handler.ConfigureShared(runtime.authority, runtime.peers); err != nil {
			listener.Close()
			handler.Close()
			return runtime.fail(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192, BaseContext: func(net.Listener) context.Context { return ctx }}
	runtime.server = server
	runtime.state.ListenAddress = listener.Addr().String()
	runtime.handler = handler
	runtime.cancel = cancel
	runtime.state.Active = true
	runtime.state.Error = ""
	go func() {
		err := server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			runtime.mu.Lock()
			if runtime.server == server {
				runtime.fail(err)
				runtime.server = nil
				cancel()
				handler.Close()
			}
			runtime.mu.Unlock()
		}
	}()
	return nil
}
func (runtime *Runtime) fail(err error) error {
	runtime.state.Active = false
	runtime.state.Error = err.Error()
	return err
}
func (runtime *Runtime) Close() {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.server != nil {
		runtime.cancel()
		_ = runtime.server.Close()
		runtime.handler.Close()
		runtime.server = nil
		runtime.state.Active = false
	}
}
