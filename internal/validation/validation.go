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

// MetadataValidationOptions controls how route metadata warnings are handled.
// Unknown frontend roots are intentionally warnings by default because a
// repository may not expose enough information to prove its browser base.
// Strict mode promotes those selected warnings to validation errors.
type MetadataValidationOptions struct {
	Strict bool
}

// MetadataWarning is the structured diagnostic shared by validation and the
// route lifecycle. Code and Field are stable machine-readable identifiers;
// Severity is either "warning" or "error" and Remediation tells callers how
// to make the metadata actionable.
type MetadataWarning struct {
	Code        string `json:"code"`
	Field       string `json:"field"`
	Severity    string `json:"severity"`
	Message     string `json:"message"`
	Remediation string `json:"remediation"`
}

const (
	WarningFrontendRootUnknown  = "FRONTEND_ROOT_UNKNOWN"
	WarningFrontendRootMismatch = "FRONTEND_ROOT_MISMATCH"
	WarningLegacyRouteMetadata  = "LEGACY_ROUTE_METADATA"
)

// MetadataValidationError identifies a warning promoted to an error by strict
// mode or an explicitly contradictory metadata declaration.
type MetadataValidationError struct {
	Warning MetadataWarning
}

func (e *MetadataValidationError) Error() string {
	if e == nil {
		return "invalid route metadata"
	}
	if e.Warning.Field == "" {
		return e.Warning.Message
	}
	return fmt.Sprintf("%s: %s", e.Warning.Field, e.Warning.Message)
}

// NormalizeRoutePath canonicalizes a public or upstream path. Trailing
// slashes are harmless and removed; query strings, fragments, wildcards,
// dot-segments, duplicate separators, and control/injection characters are
// rejected instead of being guessed at during rendering.
func NormalizeRoutePath(label, value string, optional bool) (string, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		if optional {
			return "", nil
		}
		return "", fmt.Errorf("%s must start with /", label)
	}
	if !strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("%s must start with /", label)
	}
	if containsUnsafe(raw) || strings.ContainsAny(raw, "?#") || strings.IndexFunc(raw, unicode.IsSpace) >= 0 {
		return "", fmt.Errorf("%s contains unsafe characters", label)
	}
	if len(raw) > 1 {
		raw = strings.TrimRight(raw, "/")
	}
	if strings.Contains(raw, "*") || strings.Contains(raw, "//") {
		return "", fmt.Errorf("%s contains malformed path separators or wildcard", label)
	}
	segments := strings.Split(raw, "/")
	for _, segment := range segments {
		if segment == "." || segment == ".." {
			return "", fmt.Errorf("%s contains dot segments", label)
		}
	}
	if raw == "" {
		raw = "/"
	}
	return raw, nil
}

// ValidateRouteMetadata validates the explicit public/upstream/frontend
// metadata for one application. It accepts legacy path/context fields and
// reports a migration warning when those are the only values supplied.
func ValidateRouteMetadata(app models.ApplicationConfig, strict bool) ([]MetadataWarning, error) {
	return ValidateApplicationMetadata(models.WorkspaceTypeRouting, app, MetadataValidationOptions{Strict: strict})
}

// ValidateApplicationMetadata is the options-aware metadata validator used by
// workspace validation, registration, rendering, and route planning.
func ValidateApplicationMetadata(workspaceType string, app models.ApplicationConfig, options MetadataValidationOptions) ([]MetadataWarning, error) {
	warnings := make([]MetadataWarning, 0, 2)
	publicPath := app.PublicRoutePath()
	context := app.BackendContextPath()

	// If both old and new names are present, they must describe the same
	// route. Silently preferring one would make persisted and rendered routes
	// diverge.
	if strings.TrimSpace(app.Path) != "" && strings.TrimSpace(app.PublicPath) != "" {
		legacy, legacyErr := NormalizeRoutePath("path", app.Path, workspaceType == models.WorkspaceTypeLocalApps)
		explicit, explicitErr := NormalizeRoutePath("public path", app.PublicPath, workspaceType == models.WorkspaceTypeLocalApps)
		if legacyErr == nil && explicitErr == nil && legacy != explicit {
			warning := MetadataWarning{Code: "PUBLIC_PATH_CONFLICT", Field: "publicPath", Severity: "error", Message: "path and publicPath describe different public routes", Remediation: "Keep one value and remove the legacy path field, or make both values identical."}
			return warnings, &MetadataValidationError{Warning: warning}
		}
	}
	if strings.TrimSpace(app.Context) != "" && strings.TrimSpace(app.UpstreamContext) != "" {
		legacy, legacyErr := NormalizeRoutePath("context", app.Context, true)
		explicit, explicitErr := NormalizeRoutePath("upstream context", app.UpstreamContext, true)
		if legacyErr == nil && explicitErr == nil && legacy != explicit {
			warning := MetadataWarning{Code: "UPSTREAM_CONTEXT_CONFLICT", Field: "upstreamContext", Severity: "error", Message: "context and upstreamContext describe different upstream prefixes", Remediation: "Keep one value and remove the legacy context field, or make both values identical."}
			return warnings, &MetadataValidationError{Warning: warning}
		}
	}

	publicOptional := workspaceType == models.WorkspaceTypeLocalApps
	if _, err := NormalizeRoutePath("public path", publicPath, publicOptional); err != nil {
		return warnings, err
	}
	if _, err := NormalizeRoutePath("upstream context", context, true); err != nil {
		return warnings, err
	}
	frontendRoot, err := NormalizeRoutePath("frontend root", app.FrontendRoot, true)
	if err != nil {
		return warnings, err
	}
	if strings.TrimSpace(app.FrontendRootSource) != "" && frontendRoot == "" {
		warning := MetadataWarning{Code: "FRONTEND_ROOT_SOURCE_WITHOUT_ROOT", Field: "frontendRootSource", Severity: "error", Message: "frontend root source was provided without a frontend root", Remediation: "Provide frontendRoot from the repository manifest or remove frontendRootSource."}
		return warnings, &MetadataValidationError{Warning: warning}
	}

	// A public route needs proof that the frontend was built for that base. An
	// explicitly nested frontend root on a root public route is contradictory in
	// the other direction; both cases are rejected rather than guessed at.
	if workspaceType == models.WorkspaceTypeRouting && publicPath != "" {
		if publicPath == "/" && frontendRoot != "" && frontendRoot != "/" {
			warning := MetadataWarning{Code: WarningFrontendRootMismatch, Field: "frontendRoot", Severity: "error", Message: fmt.Sprintf("frontend root %q does not match root public path", frontendRoot), Remediation: "Use frontendRoot / for a root public route or configure an explicit asset rewrite."}
			return warnings, &MetadataValidationError{Warning: warning}
		}
		if publicPath != "/" {
			switch {
			case frontendRoot == "":
				warnings = append(warnings, MetadataWarning{Code: WarningFrontendRootUnknown, Field: "frontendRoot", Severity: "warning", Message: fmt.Sprintf("frontend root cannot be proven for public path %q", publicPath), Remediation: "Declare frontendRoot in registration metadata or the repository manifest; use strict mode to reject unknown roots."})
			case frontendRoot == "/":
				warning := MetadataWarning{Code: WarningFrontendRootMismatch, Field: "frontendRoot", Severity: "error", Message: fmt.Sprintf("frontend root %q does not match nested public path %q", frontendRoot, publicPath), Remediation: "Build the frontend with the nested base, register a matching frontendRoot, or configure an explicit rewrite."}
				return warnings, &MetadataValidationError{Warning: warning}
			case frontendRoot != publicPath && !strings.HasPrefix(publicPath, frontendRoot+"/"):
				warning := MetadataWarning{Code: WarningFrontendRootMismatch, Field: "frontendRoot", Severity: "error", Message: fmt.Sprintf("frontend root %q is incompatible with public path %q", frontendRoot, publicPath), Remediation: "Set frontendRoot to the public route base or change publicPath to a route under the frontend root."}
				return warnings, &MetadataValidationError{Warning: warning}
			}
		}
	}

	if app.LegacyPublicPath || (strings.TrimSpace(app.PublicPath) == "" && strings.TrimSpace(app.Path) != "") {
		warnings = append(warnings, MetadataWarning{Code: WarningLegacyRouteMetadata, Field: "path", Severity: "warning", Message: "legacy path is being used as publicPath", Remediation: "Persist publicPath explicitly; path remains supported for migration."})
	}
	if app.LegacyUpstreamContext || (strings.TrimSpace(app.UpstreamContext) == "" && strings.TrimSpace(app.Context) != "") {
		warnings = append(warnings, MetadataWarning{Code: WarningLegacyRouteMetadata, Field: "context", Severity: "warning", Message: "legacy context is being used as upstreamContext", Remediation: "Persist upstreamContext explicitly; context remains supported for migration."})
	}

	if options.Strict {
		for _, warning := range warnings {
			if warning.Severity != "warning" || warning.Code != WarningFrontendRootUnknown {
				continue
			}
			promoted := warning
			promoted.Severity = "error"
			return warnings, &MetadataValidationError{Warning: promoted}
		}
	}
	return warnings, nil
}

func Workspace(workspaceType, localDomain, remoteBaseURL string, apps []models.ApplicationConfig) error {
	_, err := WorkspaceWithOptions(workspaceType, localDomain, remoteBaseURL, apps, MetadataValidationOptions{})
	return err
}

// WorkspaceWithOptions validates a workspace and returns non-fatal route
// metadata diagnostics in application order.
func WorkspaceWithOptions(workspaceType, localDomain, remoteBaseURL string, apps []models.ApplicationConfig, options MetadataValidationOptions) ([]MetadataWarning, error) {
	if workspaceType != models.WorkspaceTypeRouting && workspaceType != models.WorkspaceTypeLocalApps {
		return nil, fmt.Errorf("type must be %q or %q", models.WorkspaceTypeRouting, models.WorkspaceTypeLocalApps)
	}
	if workspaceType == models.WorkspaceTypeRouting {
		if err := LocalHostname(localDomain); err != nil {
			return nil, fmt.Errorf("local domain: %w", err)
		}
		if err := OptionalHTTPURL(remoteBaseURL); err != nil {
			return nil, fmt.Errorf("default remote base URL: %w", err)
		}
	}

	seen := make(map[string]string, len(apps))
	warnings := make([]MetadataWarning, 0)
	for i, app := range apps {
		canonical := models.NormalizeIdentityToken(app.CanonicalAppID())
		if previous, ok := seen[canonical]; ok {
			return warnings, fmt.Errorf("application identity %q is duplicated by %q and %q", canonical, previous, app.CanonicalAppID())
		}
		seen[canonical] = app.CanonicalAppID()
		appWarnings, err := ApplicationWithOptions(workspaceType, app, options)
		if err != nil {
			return warnings, fmt.Errorf("application %d: %w", i+1, err)
		}
		warnings = append(warnings, appWarnings...)
		for _, alias := range app.NormalizedAliases() {
			if previous, ok := seen[alias]; ok {
				return warnings, fmt.Errorf("application alias %q conflicts with %q", alias, previous)
			}
			seen[alias] = app.CanonicalAppID()
		}
	}
	return warnings, nil
}

func Application(workspaceType string, app models.ApplicationConfig) error {
	_, err := ApplicationWithOptions(workspaceType, app, MetadataValidationOptions{})
	return err
}

// ApplicationWithOptions validates an application and returns route metadata
// warnings without mutating the caller's configuration.
func ApplicationWithOptions(workspaceType string, app models.ApplicationConfig, options MetadataValidationOptions) ([]MetadataWarning, error) {
	canonical := models.NormalizeIdentityToken(app.CanonicalAppID())
	if err := IdentityIdentifier("application ID", app.CanonicalAppID()); err != nil {
		return nil, err
	}
	if app.ServiceID != "" {
		if err := IdentityIdentifier("service ID", app.ServiceID); err != nil {
			return nil, err
		}
	}
	if app.Repository != "" {
		if err := IdentityIdentifier("repository", app.Repository); err != nil {
			return nil, err
		}
	}
	for _, alias := range app.Aliases {
		normalizedAlias := models.NormalizeIdentityToken(alias)
		if err := IdentityIdentifier("application alias", alias); err != nil {
			return nil, err
		}
		if normalizedAlias == canonical {
			return nil, fmt.Errorf("application alias %q duplicates its canonical ID", alias)
		}
	}
	if app.Port < 0 || app.Port > 65535 {
		return nil, fmt.Errorf("port must be between 0 and 65535")
	}
	if workspaceType == models.WorkspaceTypeLocalApps {
		if app.Port == 0 {
			return nil, fmt.Errorf("port is required for local-apps applications")
		}
		if err := LocalHostname(app.Domain); err != nil {
			return nil, fmt.Errorf("domain: %w", err)
		}
	} else {
		if _, err := NormalizeRoutePath("public path", app.PublicRoutePath(), false); err != nil {
			return nil, err
		}
		if app.Domain != "" {
			if err := LocalHostname(app.Domain); err != nil {
				return nil, fmt.Errorf("domain: %w", err)
			}
		}
		if err := OptionalHTTPURL(app.RemoteBaseUrl); err != nil {
			return nil, fmt.Errorf("remote base URL: %w", err)
		}
	}
	if _, err := NormalizeRoutePath("upstream context", app.BackendContextPath(), true); err != nil {
		return nil, err
	}
	warnings, err := ValidateApplicationMetadata(workspaceType, app, options)
	if err != nil {
		return warnings, err
	}
	if err := RelativePath("health endpoint", app.Health); err != nil {
		return warnings, err
	}
	if app.RoutePattern != nil {
		if err := RoutePattern(*app.RoutePattern); err != nil {
			return warnings, err
		}
	}
	return warnings, nil
}

func Identifier(label, value string) error {
	if !identifierPattern.MatchString(value) {
		return fmt.Errorf("%s must use lowercase letters, numbers, and hyphens", label)
	}
	return nil
}

// IdentityIdentifier accepts the human-facing separators normalized by the
// identity resolver while rejecting URL/path syntax and other punctuation.
func IdentityIdentifier(label, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s is required", label)
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || unicode.IsSpace(r) {
			continue
		}
		return fmt.Errorf("%s contains unsupported characters", label)
	}
	return Identifier(label, models.NormalizeIdentityToken(value))
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
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if strings.Count(value, "*") > 1 || (strings.Contains(value, "*") && !strings.HasSuffix(value, "/*")) {
		return fmt.Errorf("route pattern only supports a trailing /* wildcard")
	}
	if strings.Contains(value, "//") && value != "/*" {
		return fmt.Errorf("route pattern contains malformed path separators")
	}
	base := value
	if strings.HasSuffix(value, "/*") {
		base = strings.TrimSuffix(value, "/*")
		if base == "" {
			base = "/"
		}
	}
	if _, err := NormalizeRoutePath("route pattern", base, false); err != nil {
		return err
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
