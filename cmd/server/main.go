package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/thiagojdb/rementor/internal/config"
	"github.com/thiagojdb/rementor/internal/gen/rementor/v1/rementorv1connect"
	"github.com/thiagojdb/rementor/internal/nginx"
	"github.com/thiagojdb/rementor/internal/rpc"
	"github.com/thiagojdb/rementor/internal/services"
	"github.com/thiagojdb/rementor/internal/systemd"
)

//go:embed all:dist
var distFS embed.FS

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
				e.Use(middleware.Logger())
				e.Use(middleware.Recover())

				csrfToken, err := rpc.NewCSRFToken()
				if err != nil {
					return fmt.Errorf("failed to generate CSRF token: %w", err)
				}

				// Typed Connect RPC API generated from proto/rementor/v1/rementor.proto.
				rpcPath, rpcHandler := rementorv1connect.NewControlPlaneServiceHandler(
					rpc.NewControlPlaneService(registry),
					connect.WithInterceptors(rpc.NewCSRFGuard(csrfToken)),
				)
				e.Any("/rpc"+rpcPath+"*", echo.WrapHandler(http.StripPrefix("/rpc", rpcHandler)))

				e.GET("/healthz", func(c echo.Context) error {
					return c.NoContent(http.StatusOK)
				})

				// Serve embedded SPA static assets
				distSub, err := fs.Sub(distFS, "dist")
				if err != nil {
					return fmt.Errorf("failed to sub dist FS: %w", err)
				}
				assetFS := http.FS(distSub)

				// Serve /assets/* directly from embedded FS
				e.GET("/assets/*", echo.WrapHandler(http.StripPrefix("/", http.FileServer(assetFS))))

				// SPA catch-all: serve index.html for any unmatched route
				indexData, err := distFS.ReadFile("dist/index.html")
				if err != nil {
					return fmt.Errorf("failed to read embedded index.html: %w", err)
				}
				indexData = []byte(strings.ReplaceAll(string(indexData), "%REMENTOR_CSRF_TOKEN%", csrfToken))
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
