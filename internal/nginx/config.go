package nginx

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/thiagojdb/rementor/internal/models"
	"github.com/thiagojdb/rementor/internal/services"
	"github.com/thiagojdb/rementor/internal/validation"
)

type Config struct {
	Servers []Server
}

type Server struct {
	Name      string
	Listen    []string
	Comments  []string
	Locations []Location
}

type Location struct {
	Modifier      string
	Pattern       string
	Rewrite       string
	Proxy         Proxy
	Redirect      string
	AddCORS       bool
	PassHeaders   bool
	StripOrigin   bool
	ProxyRequests bool
}

type Proxy struct {
	Scheme         string
	Host           string
	Port           int
	PassURI        string
	HeaderHost     string
	ForwardedHost  string
	ForwardedProto string
	SSLServerName  bool
}

func RenderConfig(workspaces []*models.Workspace, rementorDomain string) (string, error) {
	if err := validation.LocalHostname(rementorDomain); err != nil {
		return "", fmt.Errorf("rementor domain: %w", err)
	}
	for _, ws := range workspaces {
		apps := make([]models.ApplicationConfig, 0, len(ws.Applications))
		for _, app := range ws.Applications {
			apps = append(apps, models.ApplicationConfig{
				ID: app.ID, Name: app.Name, Path: app.Path, Domain: app.Domain,
				RemoteBaseUrl: app.RemoteBaseUrl, Port: app.Port, Health: app.Health,
				Active: app.Active, RoutePattern: app.RoutePattern, RouteOverride: app.RouteOverride, Context: app.Context,
				StripOrigin: app.StripOrigin,
			})
		}
		localDomain := ""
		if ws.RoutingConfig != nil {
			localDomain = ws.RoutingConfig.LocalDomain
		}
		if err := validation.Workspace(ws.GetType(), localDomain, ws.GetDefaultRemoteBaseURL(), apps); err != nil {
			return "", fmt.Errorf("workspace %q: %w", ws.WorkspaceID, err)
		}
		details := make([]string, 0)
		for _, conflict := range services.RouteConflicts(ws) {
			if conflict.Intentional {
				continue
			}
			details = append(details, fmt.Sprintf("%q and %q on %s %s (%s)", conflict.AppID, conflict.ConflictingAppID, conflict.PublicHost, conflict.Pattern, conflict.Reason))
		}
		if len(details) > 0 {
			return "", fmt.Errorf("workspace %q: %d route conflict(s): %s; set routeOverride on both owners or change their paths", ws.WorkspaceID, len(details), strings.Join(details, "; "))
		}
	}
	cfg := buildConfig(workspaces, rementorDomain)
	var buf bytes.Buffer
	if err := nginxTemplate.Execute(&buf, cfg); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func BaseConfig(confDir string) string {
	return fmt.Sprintf(`events {}

http {
    underscores_in_headers on;
    include %s/*.conf;
}
`, filepath.Clean(confDir))
}

func buildConfig(workspaces []*models.Workspace, rementorDomain string) Config {
	listen := listenDirectives()
	servers := []Server{{
		Name:   rementorDomain,
		Listen: listen,
		Locations: []Location{{
			Pattern:     "/",
			Proxy:       localProxy(models.DefaultRementorPort, false, ""),
			AddCORS:     true,
			PassHeaders: true,
		}},
	}}

	for _, ws := range workspaces {
		if ws.IsLocalApps() {
			for _, app := range ws.Applications {
				if app.Domain == "" || app.Port == 0 {
					continue
				}
				servers = append(servers, Server{
					Name:   app.Domain,
					Listen: listen,
					Locations: []Location{{
						Pattern:     "/",
						Proxy:       localProxy(app.Port, false, ""),
						AddCORS:     true,
						PassHeaders: true,
					}},
				})
			}
			continue
		}

		servers = append(servers, routingServer(ws.GetLocalDomain(), ws, nil, listen))
		for _, app := range ws.Applications {
			if app.Domain == "" {
				continue
			}
			servers = append(servers, routingServer(app.Domain, ws, app, listen))
		}
	}

	return Config{Servers: servers}
}

func routingServer(serverName string, ws *models.Workspace, domainApp *models.Application, listen []string) Server {
	locations := make([]Location, 0)
	defaultRemoteBaseUrl := ws.GetDefaultRemoteBaseURL()

	if domainApp == nil {
		for _, app := range ws.Applications {
			if app.Domain != "" {
				continue
			}
			if app.Active && app.Port > 0 {
				locations = append(locations, localAppLocations(ws, app)...)
			}
		}
		for _, app := range ws.Applications {
			if app.Domain != "" || app.Active {
				continue
			}
			remoteBase := app.RemoteBaseUrl
			if remoteBase == "" {
				remoteBase = defaultRemoteBaseUrl
			}
			if remoteBase == "" {
				continue
			}
			locations = append(locations, remoteAppLocations(ws, appWithRemoteBase(app, remoteBase))...)
		}
		locations = append(locations, fallbackLocation(ws, nil))
	} else {
		for _, app := range ws.Applications {
			if app.Domain != "" || app.ID == domainApp.ID {
				continue
			}
			if app.Active && app.Port > 0 {
				locations = append(locations, localAppLocations(ws, app)...)
				continue
			}
			remoteBase := app.RemoteBaseUrl
			if remoteBase == "" {
				remoteBase = defaultRemoteBaseUrl
			}
			if remoteBase == "" {
				continue
			}
			locations = append(locations, remoteAppLocations(ws, appWithRemoteBase(app, remoteBase))...)
		}
		if domainApp.Active && domainApp.Port > 0 {
			locations = append(locations, localAppLocations(ws, domainApp)...)
		} else if domainApp.RemoteBaseUrl != "" {
			locations = append(locations, remoteAppLocations(ws, domainApp)...)
		}
		locations = append(locations, fallbackLocation(ws, domainApp))
	}

	return Server{Name: serverName, Listen: listen, Locations: dedupeLocations(locations)}
}

func listenDirectives() []string {
	host := strings.TrimSpace(os.Getenv("REMENTOR_NGINX_LISTEN_HOST"))

	ports := []int{models.DefaultHTTPPort}
	if raw := strings.TrimSpace(os.Getenv("REMENTOR_NGINX_LISTEN_PORTS")); raw != "" {
		ports = nil
		for _, part := range strings.Split(raw, ",") {
			port, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil || port < 1 || port > 65535 {
				continue
			}
			ports = append(ports, port)
		}
		if len(ports) == 0 {
			ports = []int{models.DefaultHTTPPort}
		}
	}

	hosts := []string{"127.0.0.1", "[::1]"}
	if host != "" {
		hosts = []string{host}
	}

	listen := make([]string, 0, len(ports)*len(hosts))
	for _, port := range ports {
		for _, host := range hosts {
			if host == "*" || host == "0.0.0.0" {
				listen = append(listen, strconv.Itoa(port))
				continue
			}
			listen = append(listen, fmt.Sprintf("%s:%d", host, port))
		}
	}
	return listen
}

func appWithRemoteBase(app *models.Application, remoteBase string) *models.Application {
	return &models.Application{
		ID:            app.ID,
		AppID:         app.AppID,
		ServiceID:     app.ServiceID,
		Repository:    app.Repository,
		Aliases:       append([]string(nil), app.Aliases...),
		Name:          app.Name,
		Path:          app.Path,
		Domain:        app.Domain,
		RemoteBaseUrl: remoteBase,
		Context:       app.Context,
		Health:        app.Health,
		Port:          app.Port,
		Active:        app.Active,
		RoutePattern:  app.RoutePattern,
		RouteOverride: app.RouteOverride,
		StripOrigin:   app.StripOrigin,
	}
}

func localAppLocations(ws *models.Workspace, app *models.Application) []Location {
	if app.Path == "/" && app.Context != "" && app.Context != "/" {
		context := cleanPath(app.Context)
		return []Location{
			{Modifier: "=", Pattern: "/", Proxy: localProxy(app.Port, false, ""), AddCORS: true, PassHeaders: true, StripOrigin: app.StripOrigin},
			{Modifier: "=", Pattern: context, Redirect: context + "/"},
			{Pattern: context + "/", Proxy: localProxy(app.Port, true, ""), AddCORS: true, PassHeaders: true, StripOrigin: app.StripOrigin},
		}
	}
	return locationsForPatterns(routePathPatterns(ws, app), localProxy(app.Port, false, ""), app.StripOrigin)
}

func remoteAppLocations(ws *models.Workspace, app *models.Application) []Location {
	if app.Path == "/" && app.Context != "" && app.Context != "/" {
		context := cleanPath(app.Context)
		proxy := remoteProxy(app.RemoteBaseUrl)
		return []Location{
			{Modifier: "=", Pattern: "/", Rewrite: context + "/home", Proxy: proxy, AddCORS: true, PassHeaders: true},
			{Modifier: "=", Pattern: context, Proxy: proxy, AddCORS: true, PassHeaders: true},
			{Pattern: context + "/", Proxy: proxy, AddCORS: true, PassHeaders: true},
		}
	}
	return locationsForPatterns(routePathPatterns(ws, app), remoteProxy(app.RemoteBaseUrl), false)
}

func locationsForPatterns(patterns []string, proxy Proxy, stripOrigin bool) []Location {
	locations := make([]Location, 0, len(patterns))
	for _, pattern := range patterns {
		modifier, nginxPattern := nginxLocationPattern(pattern)
		locations = append(locations, Location{
			Modifier:    modifier,
			Pattern:     nginxPattern,
			Proxy:       proxy,
			AddCORS:     true,
			PassHeaders: true,
			StripOrigin: stripOrigin,
		})
	}
	return locations
}

func fallbackLocation(ws *models.Workspace, domainApp *models.Application) Location {
	rootApp := domainApp
	if rootApp == nil {
		for _, app := range ws.Applications {
			if app.Domain == "" && app.Path == "/" && app.RemoteBaseUrl != "" {
				rootApp = app
				break
			}
		}
	}

	if rootApp != nil && rootApp.Active && rootApp.Port > 0 {
		return Location{
			Pattern:     "/",
			Proxy:       localProxy(rootApp.Port, false, ""),
			AddCORS:     true,
			PassHeaders: true,
			StripOrigin: rootApp.StripOrigin,
		}
	}

	remoteBase := ws.GetDefaultRemoteBaseURL()
	var rewrite string
	if rootApp != nil && rootApp.RemoteBaseUrl != "" {
		remoteBase = rootApp.RemoteBaseUrl
		if rootApp.Context != "" && rootApp.Context != "/" {
			rewrite = fmt.Sprintf("%s$uri", cleanPath(rootApp.Context))
		}
	}
	return Location{
		Pattern:     "/",
		Rewrite:     rewrite,
		Proxy:       remoteProxy(remoteBase),
		AddCORS:     true,
		PassHeaders: true,
	}
}

func localProxy(port int, stripPrefix bool, passURI string) Proxy {
	if stripPrefix {
		passURI = "/"
	}
	return Proxy{
		Scheme:        "http",
		Host:          "localhost",
		Port:          port,
		PassURI:       passURI,
		HeaderHost:    "localhost",
		ForwardedHost: "localhost",
	}
}

func remoteProxy(rawURL string) Proxy {
	parsed, err := url.Parse(rawURL)
	host := strings.TrimPrefix(strings.TrimPrefix(rawURL, "https://"), "http://")
	scheme := "https"
	port := 443
	if err == nil && parsed.Host != "" {
		host = parsed.Host
		if parsed.Scheme != "" {
			scheme = parsed.Scheme
		}
		if parsed.Port() != "" {
			host = parsed.Hostname()
			fmt.Sscanf(parsed.Port(), "%d", &port)
		} else if scheme == "http" {
			port = 80
		}
	}
	host = strings.TrimRight(host, "/")
	return Proxy{
		Scheme:         scheme,
		Host:           host,
		Port:           port,
		HeaderHost:     host,
		ForwardedHost:  host,
		ForwardedProto: scheme,
		SSLServerName:  scheme == "https",
	}
}

func routePathPatterns(ws *models.Workspace, app *models.Application) []string {
	if patterns := services.RoutePatterns(ws, app); len(patterns) > 0 {
		return patterns
	}
	// Keep a defensive fallback for partially initialized workspaces. Normal
	// registry/config paths always provide the shared normalized projection.
	if app.RoutePattern != nil && *app.RoutePattern != "" {
		return []string{*app.RoutePattern}
	}
	if app.Path == "/" {
		return []string{"/*"}
	}
	path := app.Context
	if path == "" {
		path = app.Path
	}
	path = cleanPath(path)
	return []string{path, fmt.Sprintf("%s/*", path)}
}

func nginxLocationPattern(pattern string) (string, string) {
	pattern = cleanPath(pattern)
	if pattern == "/*" || pattern == "/" {
		return "", "/"
	}
	if strings.HasSuffix(pattern, "/*") {
		return "", strings.TrimSuffix(pattern, "*")
	}
	return "=", pattern
}

func cleanPath(path string) string {
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}
	return path
}

func dedupeLocations(locations []Location) []Location {
	seen := make(map[string]Location)
	order := make([]string, 0, len(locations))
	for _, loc := range locations {
		key := loc.Modifier + " " + loc.Pattern
		if _, ok := seen[key]; !ok {
			order = append(order, key)
			seen[key] = loc
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		a := seen[order[i]]
		b := seen[order[j]]
		return locationRank(a) < locationRank(b)
	})
	out := make([]Location, 0, len(order))
	for _, key := range order {
		out = append(out, seen[key])
	}
	return out
}

func locationRank(loc Location) int {
	if loc.Modifier == "=" {
		return 0
	}
	if loc.Pattern != "/" {
		return 1
	}
	return 2
}

var nginxTemplate = template.Must(template.New("nginx").Parse(`# Generated by rementor. Do not edit manually.
{{- range .Servers }}

server {
{{- range .Listen }}
    listen {{ . }};
{{- end }}
    server_name {{ .Name }};

    set $rementor_cors_origin "";
    if ($http_origin ~* "^https?://([a-z0-9-]+\.)*localhost(:[0-9]+)?$") {
        set $rementor_cors_origin $http_origin;
    }
    if ($http_origin ~* "^https?://(127\.0\.0\.1|\[::1\])(:[0-9]+)?$") {
        set $rementor_cors_origin $http_origin;
    }

    add_header Access-Control-Allow-Origin $rementor_cors_origin always;
    add_header Access-Control-Allow-Methods "GET, POST, PUT, DELETE, PATCH, OPTIONS" always;
    add_header Access-Control-Allow-Headers "*" always;
    add_header Access-Control-Max-Age 86400 always;
    add_header Vary "Origin" always;

    if ($request_method = OPTIONS) {
        return 204;
    }
{{- range .Locations }}

    location {{ if .Modifier }}{{ .Modifier }} {{ end }}{{ .Pattern }} {
{{- if .Redirect }}
        return 301 {{ .Redirect }};
{{- else }}
{{- if .Rewrite }}
        rewrite ^ {{ .Rewrite }} break;
{{- end }}
        proxy_pass_request_headers on;
{{- if .StripOrigin }}
        proxy_set_header Origin "";
{{- end }}
        proxy_set_header Host {{ .Proxy.HeaderHost }};
{{- if .Proxy.ForwardedHost }}
        proxy_set_header X-Forwarded-Host {{ .Proxy.ForwardedHost }};
{{- end }}
{{- if .Proxy.ForwardedProto }}
        proxy_set_header X-Forwarded-Proto {{ .Proxy.ForwardedProto }};
{{- end }}
{{- if .Proxy.SSLServerName }}
        proxy_ssl_server_name on;
{{- end }}
{{- if .AddCORS }}
        proxy_hide_header Access-Control-Allow-Origin;
        proxy_hide_header Access-Control-Allow-Methods;
        proxy_hide_header Access-Control-Allow-Headers;
        proxy_hide_header Access-Control-Max-Age;
        add_header Access-Control-Allow-Origin $rementor_cors_origin always;
        add_header Access-Control-Allow-Methods "GET, POST, PUT, DELETE, PATCH, OPTIONS" always;
        add_header Access-Control-Allow-Headers "*" always;
        add_header Access-Control-Max-Age 86400 always;
        add_header Vary "Origin" always;
{{- end }}
        proxy_pass {{ .Proxy.Scheme }}://{{ .Proxy.Host }}:{{ .Proxy.Port }}{{ .Proxy.PassURI }};
{{- end }}
    }
{{- end }}
}
{{- end }}
`))
