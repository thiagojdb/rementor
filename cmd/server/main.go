package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/thiagojdb/rementor/internal/access"
	"github.com/thiagojdb/rementor/internal/config"
	"github.com/thiagojdb/rementor/internal/gen/rementor/v1/rementorv1connect"
	"github.com/thiagojdb/rementor/internal/ingress"
	"github.com/thiagojdb/rementor/internal/nginx"
	"github.com/thiagojdb/rementor/internal/peers"
	"github.com/thiagojdb/rementor/internal/rpc"
	"github.com/thiagojdb/rementor/internal/services"
	"github.com/thiagojdb/rementor/internal/shared"
	"github.com/thiagojdb/rementor/internal/systemd"
	"github.com/thiagojdb/rementor/internal/tailshare"
)

const (
	frontendDistEnv     = "REMENTOR_FRONTEND_DIST"
	defaultFrontendDist = "cmd/server/dist"
)

func frontendDistPath() string {
	if path := strings.TrimSpace(os.Getenv(frontendDistEnv)); path != "" {
		return path
	}
	return defaultFrontendDist
}

func loadFrontendAssets(distPath, csrfToken string) (fs.FS, []byte, error) {
	assetFS := os.DirFS(distPath)
	indexData, err := fs.ReadFile(assetFS, "index.html")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read frontend assets from %q: %w (run `make frontend` or set %s)", distPath, err, frontendDistEnv)
	}

	indexData = []byte(strings.ReplaceAll(string(indexData), "%REMENTOR_CSRF_TOKEN%", csrfToken))
	return assetFS, indexData, nil
}

// InitStep represents a single initialization step
type InitStep struct {
	Name string
	Func func(ctx context.Context) error
}

func main() {
	// Parse command line arguments
	var host string
	var port int
	flag.StringVar(&host, "host", "127.0.0.1", "host/interface to bind")
	flag.IntVar(&port, "port", 9300, "")
	flag.Parse()
	_ = os.Setenv("REMENTOR_CONTROL_PORT", strconv.Itoa(port))
	if !isLoopbackHost(host) {
		log.Fatalf("refusing to bind unauthenticated control plane to non-loopback host %q", host)
	}
	log.Println(strings.Repeat("=", 58))
	log.Println("Starting Rementor application...")
	log.Println(strings.Repeat("=", 58))
	log.Printf("Using address: %s:%d\n", host, port)

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Shutting down...")

		// Save state
		if registry := services.GetRegistry(); registry != nil {
			if err := registry.SaveState(); err != nil {
				log.Printf("Error saving state: %v", err)
			}
		}

		cancel()
	}()

	// These will be set by initialization steps and used later
	var e *echo.Echo
	var registry *services.Registry

	// Define initialization steps - easy to add, remove, or reorder
	initSteps := []InitStep{
		{
			Name: "Load configuration",
			Func: func(ctx context.Context) error {
				return config.Load()
			},
		},

		{
			Name: "Load registry",
			Func: func(ctx context.Context) error {
				registry = services.GetRegistry()
				nginxProvider := nginx.NewRoutingProvider(config.Config.NginxConfDir, config.Config.NginxBinary)
				if !nginxProvider.IsAvailable() {
					log.Println("  Warning: nginx is not available or config validation failed")
				} else {
					log.Println("  nginx executable and current configuration are valid")
				}
				registry.SetRoutingProvider(nginxProvider)
				return registry.Load()
			},
		},
		{
			Name: "Configure routing",
			Func: func(ctx context.Context) error {
				log.Println("  Using .localhost domains (no DNS setup required)")
				log.Println("  API domain: api.localhost (auto-resolves to 127.0.0.1)")
				log.Printf("  Rementor UI (direct): http://localhost:%d", port)
				log.Printf("  Rementor UI (with nginx routing): http://%s/", config.Config.RementorDomain)
				log.Println("  Path-based routing enabled")
				return nil
			},
		},
		{
			Name: "Start health checker",
			Func: func(ctx context.Context) error {
				registry.StartHealthChecker(ctx)
				return nil
			},
		},
		{
			Name: "Register HTTP routes",
			Func: func(ctx context.Context) error {
				e = echo.New()
				e.HideBanner = true

				// Middleware
				e.Use(rpc.CorrelationMiddleware)
				e.Use(middleware.Logger())
				e.Use(middleware.Recover())

				csrfToken, err := rpc.NewCSRFToken()
				if err != nil {
					return fmt.Errorf("failed to generate CSRF token: %w", err)
				}

				hostname := os.Getenv("REMENTOR_COMPUTER_NAME")
				if hostname == "" {
					hostname, _ = os.Hostname()
				}
				peerManager, err := peers.Open(filepath.Join(config.GetDataDir(), "peers"), hostname)
				if err != nil {
					return fmt.Errorf("initialize peer identity: %w", err)
				}
				peerManager.SetWorkspaceSource(registry)
				sharedStore, err := config.NewSharedIngressStore()
				if err != nil {
					return fmt.Errorf("open shared ingress store: %w", err)
				}
				sharedAuthority, err := shared.Open(peerManager.State().Fingerprint, sharedStore)
				if err != nil {
					return fmt.Errorf("restore shared ingress ownership: %w", err)
				}
				peerManager.SetSharedAuthority(sharedAuthority)
				if err := registry.SetPeerWorkspaceClient(peerManager.State().Fingerprint, peerManager); err != nil {
					peerManager.Close()
					return fmt.Errorf("restore peer inheritance: %w", err)
				}
				peerManager.SetManagementPort(port)
				gatewayPort, err := peers.GatewayPort(port, os.Getenv("REMENTOR_PEER_GATEWAY_PORT"))
				if err != nil {
					peerManager.Close()
					return fmt.Errorf("peer gateway port: %w", err)
				}
				protectedPorts := []int{gatewayPort, 80, 443}
				for _, raw := range strings.Split(os.Getenv("REMENTOR_NGINX_LISTEN_PORTS"), ",") {
					if nginxPort, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
						protectedPorts = append(protectedPorts, nginxPort)
					}
				}
				peerManager.ProtectLocalPorts(protectedPorts...)
				peerManager.Restore()
				gatewayListener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(gatewayPort)))
				if err != nil {
					peerManager.Close()
					return fmt.Errorf("listen on peer gateway: %w", err)
				}
				gateway := &http.Server{Handler: peerManager.Gateway(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
				go func() {
					if err := gateway.Serve(gatewayListener); err != nil && err != http.ErrServerClosed {
						log.Printf("Peer gateway stopped: %v", err)
						cancel()
					}
				}()
				go func() { <-ctx.Done(); peerManager.Close(); _ = gateway.Close() }()
				go registry.RunPeerSynchronization(ctx, 5*time.Second)
				controlPlane := rpc.NewControlPlaneService(registry)
				controlPlane.SetPeerManager(peerManager)
				controlPlane.SetSharedIngress(sharedAuthority)
				publicPort, publicPortError := ingress.PublicPort(port, os.Getenv("REMENTOR_PUBLIC_INGRESS_PORT"))
				publicListen := net.JoinHostPort("127.0.0.1", strconv.Itoa(publicPort))
				if publicPortError != nil || publicPort == gatewayPort {
					publicListen = "" // Publication remains unavailable; local routing still works.
				}
				protectedPorts = append(protectedPorts, publicPort)
				peerManager.ProtectLocalPorts(protectedPorts...)
				nginxAddress := os.Getenv("REMENTOR_PUBLIC_NGINX_ADDRESS")
				if nginxAddress == "" {
					nginxPorts := strings.Split(os.Getenv("REMENTOR_NGINX_LISTEN_PORTS"), ",")
					if len(nginxPorts) == 1 {
						nginxPort := strings.TrimSpace(nginxPorts[0])
						if nginxPort == "" {
							nginxPort = "80"
						}
						nginxAddress = "http://127.0.0.1:" + nginxPort
					}
				}
				if parsed, err := url.Parse(nginxAddress); err == nil {
					nginxPort, _ := strconv.Atoi(parsed.Port())
					protectedPorts = append(protectedPorts, nginxPort)
					peerManager.ProtectLocalPorts(protectedPorts...)
					if nginxPort == port || nginxPort == gatewayPort || nginxPort == publicPort {
						nginxAddress = ""
					}
				}
				gatewaySocket := os.Getenv("REMENTOR_TAILSCALE_GATEWAY_SOCKET")
				if gatewaySocket == "" {
					gatewaySocket = os.Getenv("REMENTOR_TAILSCALE_SOCKET")
				}
				identity := ingress.NewTailscaleIdentityResolver(gatewaySocket)
				deviceCatalog := ingress.NewTailscaleIdentityResolver(os.Getenv("REMENTOR_TAILSCALE_SOCKET"))
				publicRuntime := ingress.NewRuntime(registry, identity, publicListen, nginxAddress, peerManager.IsProtectedLocalPort)
				publicRuntime.ConfigureShared(sharedAuthority, peerManager)
				if err := publicRuntime.Start(); err != nil {
					log.Printf("Public data plane unavailable: %v", err)
				}
				controlPlane.SetPublicIngress(publicRuntime, deviceCatalog)
				publisherProvider := tailshare.NewTailscale(gatewaySocket, os.Getenv("REMENTOR_TAILSCALE_API_TOKEN"))
				publisherProvider.ApplicationPath = func(manifest access.Manifest, origin tailshare.Origin) string {
					ws := registry.GetWorkspaceView(manifest.WorkspaceID)
					if ws == nil {
						return "/"
					}
					host := manifest.RoutingHosts[origin.ID]
					path := ""
					for _, route := range services.NormalizedRoutes(ws) {
						if route.PublicHost == host && route.CanonicalAppID != "" {
							candidate := strings.TrimSuffix(route.Pattern, "/*")
							if candidate == "" {
								candidate = "/"
							}
							if path == "" || len(candidate) < len(path) {
								path = candidate
							}
						}
					}
					if path == "" {
						path = "/"
					}
					return path
				}
				publisher, err := tailshare.New(config.TailscaleSharingStore{}, publisherProvider, func() access.Configuration {
					state := sharedAuthority.State()
					if state.Record != nil && slices.Contains(state.Record.Gateways, peerManager.State().Fingerprint) {
						return access.Configuration{Revision: state.Record.Version, Document: state.Record.Policy.Clone()}
					}
					if state.Record != nil {
						return access.Configuration{Document: access.Empty()}
					}
					return registry.PublicAccessConfiguration()
				})
				if err != nil {
					return fmt.Errorf("restore Tailscale sharing: %w", err)
				}
				controlPlane.SetTailscalePublisher(publisher)
				publisher.Target = func() string { return "http://" + publicRuntime.State().ListenAddress }
				publisher.SharedOwner = func() string {
					state := sharedAuthority.State()
					if state.Record != nil && slices.Contains(state.Record.Gateways, peerManager.State().Fingerprint) {
						return state.Record.GroupID
					}
					return ""
				}
				go publisher.Run(ctx)
				go func() {
					<-ctx.Done()
					publicRuntime.Close()
					identity.Close()
					deviceCatalog.Close()
					publisherProvider.Close()
				}()

				// Typed Connect RPC API generated from proto/rementor/v1/rementor.proto.
				rpcPath, rpcHandler := rementorv1connect.NewControlPlaneServiceHandler(
					controlPlane,
					connect.WithInterceptors(rpc.NewCSRFGuard(csrfToken)),
				)
				e.Any("/rpc"+rpcPath+"*", echo.WrapHandler(http.StripPrefix("/rpc", rpcHandler)))

				e.GET("/healthz", func(c echo.Context) error {
					return c.NoContent(http.StatusOK)
				})
				e.GET(rpc.TracePath, rpc.TraceHandler)
				// Keep the shorter spelling available for manually managed nginx
				// instances while generated routes use /__rementor/trace.
				e.GET("/_rementor/trace", rpc.TraceHandler)

				// Serve the generated SPA assets from the build output directory.
				// The frontend bundle is intentionally not committed to source control.
				frontendFS, indexData, err := loadFrontendAssets(frontendDistPath(), csrfToken)
				if err != nil {
					return err
				}
				assetFS := http.FS(frontendFS)

				// Serve /assets/* directly from the generated frontend FS.
				e.GET("/assets/*", echo.WrapHandler(http.StripPrefix("/", http.FileServer(assetFS))))

				// SPA catch-all: serve index.html for any unmatched route.
				e.GET("/*", func(c echo.Context) error {
					return c.HTMLBlob(http.StatusOK, indexData)
				})

				log.Printf("  Registered %d routes", len(e.Routes()))
				return nil
			},
		},
		{
			Name: "Start HTTP server",
			Func: func(ctx context.Context) error {
				addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
				listener, err := net.Listen("tcp", addr)
				if err != nil {
					return fmt.Errorf("listen on %s: %w", addr, err)
				}

				go func() {
					if err := e.Server.Serve(listener); err != nil && err != http.ErrServerClosed {
						log.Fatalf("Server error: %v", err)
					}
				}()

				if err := systemd.NotifyReady(); err != nil {
					log.Printf("Warning: failed to notify systemd readiness: %v", err)
				}

				watchdogInterval, enabled, err := systemd.WatchdogInterval()
				if err != nil {
					log.Printf("Warning: failed to parse systemd watchdog interval: %v", err)
					return nil
				}

				if enabled {
					// Ping systemd more frequently than required so it can restart us if the process wedges.
					ticker := time.NewTicker(watchdogInterval / 2)
					go func() {
						defer ticker.Stop()
						for {
							select {
							case <-ctx.Done():
								return
							case <-ticker.C:
								if err := systemd.NotifyWatchdog(); err != nil {
									log.Printf("Warning: failed to notify systemd watchdog: %v", err)
								}
							}
						}
					}()
				}

				return nil
			},
		},
	}

	// Execute initialization steps
	totalSteps := len(initSteps)
	for i, step := range initSteps {
		stepNum := i + 1
		prefix := fmt.Sprintf("[%d/%d]", stepNum, totalSteps)

		log.Printf("%s %s...", prefix, step.Name)

		if err := step.Func(ctx); err != nil {
			log.Fatalf("%s Failed to %s: %v", prefix, step.Name, err)
		}

		log.Printf("%s %s complete", prefix, step.Name)
	}

	log.Println("=" + strings.Repeat("=", 58))
	log.Println("Application initialization complete!")
	log.Println("=" + strings.Repeat("=", 58))

	// Wait for shutdown signal
	<-ctx.Done()

	if err := systemd.NotifyStopping(); err != nil {
		log.Printf("Warning: failed to notify systemd shutdown: %v", err)
	}

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := e.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}

	log.Println("Server stopped")
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
