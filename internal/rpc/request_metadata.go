package rpc

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/labstack/echo/v4"
)

const (
	TracePath         = "/__rementor/trace"
	CorrelationHeader = "X-Rementor-Correlation-ID"
	RequestIDHeader   = "X-Rementor-Request-ID"
)

var correlationPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)
var traceparentPattern = regexp.MustCompile(`^[0-9a-fA-F]{2}-[0-9a-fA-F]{32}-[0-9a-fA-F]{16}-[0-9a-fA-F]{2}$`)

// CorrelationMiddleware establishes one validated request identity for every
// direct control-plane request. It is installed before Echo's logger and the
// Connect handler, so both logs and operation metadata use the same value.
func CorrelationMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		request := c.Request()
		correlation := correlationID("", request.Header)
		request.Header.Set("X-Correlation-ID", correlation)
		request.Header.Set("X-Request-ID", correlation)
		c.Set(CorrelationHeader, correlation)
		c.Response().Header().Set(CorrelationHeader, correlation)
		c.Response().Header().Set(RequestIDHeader, correlation)
		c.Response().Header().Set("X-Correlation-ID", correlation)
		c.Response().Header().Set("X-Request-ID", correlation)

		err := next(c)
		log.Printf("control-plane request correlation_id=%s method=%s path=%s status=%d", correlation, request.Method, request.URL.Path, c.Response().Status)
		return err
	}
}

// TraceHandler provides a request inspection path for debugging a route when
// the response body or browser tooling hides headers. The generated nginx
// config exposes this path on each workspace host and forwards it to the
// control plane, preserving the correlation headers.
func TraceHandler(c echo.Context) error {
	correlation := strings.TrimSpace(c.Request().Header.Get("X-Correlation-ID"))
	if !validCorrelationID(correlation) {
		correlation = strings.TrimSpace(c.Response().Header().Get(CorrelationHeader))
	}
	if !validCorrelationID(correlation) {
		correlation = generatedCorrelationID()
		c.Response().Header().Set(CorrelationHeader, correlation)
		c.Response().Header().Set(RequestIDHeader, correlation)
		c.Response().Header().Set("X-Correlation-ID", correlation)
		c.Response().Header().Set("X-Request-ID", correlation)
	}
	proofHeaders := make(map[string]string)
	for _, name := range []string{
		"X-Rementor-App-ID",
		"X-Rementor-Service-ID",
		"X-Rementor-Workspace",
		"X-Rementor-Environment",
		"X-Rementor-Effective-Mode",
		"X-Rementor-Route-Version",
		"X-Rementor-Operation-ID",
		CorrelationHeader,
		RequestIDHeader,
	} {
		if value := strings.TrimSpace(c.Request().Header.Get(name)); value != "" {
			proofHeaders[name] = value
		}
	}
	return c.JSON(http.StatusOK, map[string]any{
		"correlationId": correlation,
		"requestId":     correlation,
		"method":        c.Request().Method,
		"host":          c.Request().Host,
		"path":          c.Request().URL.Path,
		"proofHeaders":  proofHeaders,
	})
}

func validCorrelationID(value string) bool {
	value = strings.TrimSpace(value)
	return correlationPattern.MatchString(value) || traceparentPattern.MatchString(value)
}

func generatedCorrelationID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return "req-" + hex.EncodeToString(raw[:])
	}
	// crypto/rand failures are exceptionally rare; keep the fallback safe and
	// non-empty rather than allowing an untrusted value into a response header.
	return fmt.Sprintf("req-%x", raw[:])
}

func candidateCorrelationID(value string, traceparent bool) string {
	value = strings.TrimSpace(value)
	if traceparent {
		if traceparentPattern.MatchString(value) {
			return value
		}
		return ""
	}
	if correlationPattern.MatchString(value) {
		return value
	}
	return ""
}
