package validation

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/thiagojdb/rementor/internal/models"
)

var identifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func Workspace(workspaceType, localDomain, remoteBaseURL string, apps []models.ApplicationConfig) error {
	if workspaceType != models.WorkspaceTypeRouting && workspaceType != models.WorkspaceTypeLocalApps {
		return fmt.Errorf("type must be %q or %q", models.WorkspaceTypeRouting, models.WorkspaceTypeLocalApps)
	}
	if workspaceType == models.WorkspaceTypeRouting {
		if err := LocalHostname(localDomain); err != nil {
			return fmt.Errorf("local domain: %w", err)
		}
		if err := OptionalHTTPURL(remoteBaseURL); err != nil {
			return fmt.Errorf("default remote base URL: %w", err)
		}
	}

	seen := make(map[string]struct{}, len(apps))
	for i, app := range apps {
		if _, ok := seen[app.ID]; ok {
			return fmt.Errorf("application %q is duplicated", app.ID)
		}
		seen[app.ID] = struct{}{}
		if err := Application(workspaceType, app); err != nil {
			return fmt.Errorf("application %d: %w", i+1, err)
		}
	}
	return nil
}

func Application(workspaceType string, app models.ApplicationConfig) error {
	if err := Identifier("application ID", app.ID); err != nil {
		return err
	}
	if app.Port < 0 || app.Port > 65535 {
		return fmt.Errorf("port must be between 0 and 65535")
	}
	if workspaceType == models.WorkspaceTypeLocalApps {
		if app.Port == 0 {
			return fmt.Errorf("port is required for local-apps applications")
		}
		if err := LocalHostname(app.Domain); err != nil {
			return fmt.Errorf("domain: %w", err)
		}
	} else {
		if err := RoutePath("path", app.Path, false); err != nil {
			return err
		}
		if app.Domain != "" {
			if err := LocalHostname(app.Domain); err != nil {
				return fmt.Errorf("domain: %w", err)
			}
		}
		if err := OptionalHTTPURL(app.RemoteBaseUrl); err != nil {
			return fmt.Errorf("remote base URL: %w", err)
		}
	}
	if err := RoutePath("context", app.Context, true); err != nil {
		return err
	}
	if err := RelativePath("health endpoint", app.Health); err != nil {
		return err
	}
	if app.RoutePattern != nil {
		if err := RoutePattern(*app.RoutePattern); err != nil {
			return err
		}
	}
	return nil
}

func Identifier(label, value string) error {
	if !identifierPattern.MatchString(value) {
		return fmt.Errorf("%s must use lowercase letters, numbers, and hyphens", label)
	}
	return nil
}

func LocalHostname(value string) error {
	value = strings.TrimSpace(strings.TrimSuffix(value, "."))
	if value == "" {
		return fmt.Errorf("is required")
	}
	if strings.ContainsAny(value, " \t\r\n;{}$") || net.ParseIP(value) != nil {
		return fmt.Errorf("must be a valid .localhost hostname")
	}
	labels := strings.Split(value, ".")
	if len(labels) < 2 || labels[len(labels)-1] != "localhost" {
		return fmt.Errorf("must end in .localhost")
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || !identifierPattern.MatchString(label) {
			return fmt.Errorf("must be a valid .localhost hostname")
		}
	}
	return nil
}

func OptionalHTTPURL(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" {
		return fmt.Errorf("must be an absolute HTTP or HTTPS URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("scheme must be http or https")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("must not contain credentials or a fragment")
	}
	if containsUnsafe(value) {
		return fmt.Errorf("contains unsafe characters")
	}
	return nil
}

func RoutePath(label, value string, optional bool) error {
	value = strings.TrimSpace(value)
	if value == "" && optional {
		return nil
	}
	if value == "" || !strings.HasPrefix(value, "/") {
		return fmt.Errorf("%s must start with /", label)
	}
	if containsUnsafe(value) || strings.Contains(value, "?") || strings.Contains(value, "#") {
		return fmt.Errorf("%s contains unsafe characters", label)
	}
	return nil
}

func RelativePath(label, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if containsUnsafe(value) || strings.ContainsAny(value, "?#") {
		return fmt.Errorf("%s contains unsafe characters", label)
	}
	return nil
}

func RoutePattern(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if err := RoutePath("route pattern", value, false); err != nil {
		return err
	}
	if strings.Count(value, "*") > 1 || (strings.Contains(value, "*") && !strings.HasSuffix(value, "/*")) {
		return fmt.Errorf("route pattern only supports a trailing /* wildcard")
	}
	return nil
}

func containsUnsafe(value string) bool {
	if strings.ContainsAny(value, ";\r\n{}$`\\") {
		return true
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}
