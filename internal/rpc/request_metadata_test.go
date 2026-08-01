package rpc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestCorrelationIDPreservesOnlyValidatedValues(t *testing.T) {
	headers := make(http.Header)
	headers.Set("X-Correlation-ID", "caller-42")
	if got := correlationID("", headers); got != "caller-42" {
		t.Fatalf("correlationID() = %q, want caller-42", got)
	}

	headers.Set("X-Correlation-ID", "bad\r\nX-Spoof: yes")
	got := correlationID("", headers)
	if got == "bad\r\nX-Spoof: yes" || !validCorrelationID(got) {
		t.Fatalf("invalid correlation ID was preserved: %q", got)
	}
	if !strings.HasPrefix(got, "req-") {
		t.Fatalf("generated correlation ID = %q, want req- prefix", got)
	}
}

func TestTraceHandlerReturnsRequestInspectionPayload(t *testing.T) {
	e := echo.New()
	e.Use(CorrelationMiddleware)
	e.GET(TracePath, TraceHandler)

	req := httptest.NewRequest(http.MethodGet, TracePath, nil)
	req.Header.Set("X-Request-ID", "trace-7")
	res := httptest.NewRecorder()
	e.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("trace status = %d, want 200", res.Code)
	}
	if got := res.Header().Get(CorrelationHeader); got != "trace-7" {
		t.Fatalf("response correlation header = %q, want trace-7", got)
	}
	if !strings.Contains(res.Body.String(), `"correlationId":"trace-7"`) {
		t.Fatalf("trace payload did not include correlation ID: %s", res.Body.String())
	}
}
