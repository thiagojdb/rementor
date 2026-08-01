package rpc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"connectrpc.com/connect"
	"github.com/thiagojdb/rementor/internal/gen/rementor/v1/rementorv1connect"
)

const CSRFHeader = "X-Rementor-CSRF"

var mutatingProcedures = map[string]struct{}{
	rementorv1connect.ControlPlaneServiceCreateWorkspaceProcedure:      {},
	rementorv1connect.ControlPlaneServiceUpdateWorkspaceProcedure:      {},
	rementorv1connect.ControlPlaneServiceDeleteWorkspaceProcedure:      {},
	rementorv1connect.ControlPlaneServiceUpsertApplicationProcedure:    {},
	rementorv1connect.ControlPlaneServiceDeleteApplicationProcedure:    {},
	rementorv1connect.ControlPlaneServiceToggleApplicationProcedure:    {},
	rementorv1connect.ControlPlaneServiceToggleAllToRemoteProcedure:    {},
	rementorv1connect.ControlPlaneServiceToggleAllToLocalProcedure:     {},
	rementorv1connect.ControlPlaneServiceSyncWorkspaceRoutingProcedure: {},
	rementorv1connect.ControlPlaneServiceUpdateRoutePatternProcedure:   {},
	rementorv1connect.ControlPlaneServiceApplyRouteProcedure:           {},
	rementorv1connect.ControlPlaneServiceSyncRouteProcedure:            {},
}

type CSRFGuard struct {
	token string
}

func NewCSRFToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func NewCSRFGuard(token string) *CSRFGuard {
	return &CSRFGuard{token: token}
}

func (g *CSRFGuard) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if err := g.validate(req.Spec().Procedure, req.Header()); err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

func (g *CSRFGuard) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (g *CSRFGuard) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if err := g.validate(conn.Spec().Procedure, conn.RequestHeader()); err != nil {
			return err
		}
		return next(ctx, conn)
	}
}

func (g *CSRFGuard) validate(procedure string, header http.Header) error {
	if err := validateBrowserOrigin(header); err != nil {
		return newRPCError(connect.CodePermissionDenied, err)
	}
	if _, ok := mutatingProcedures[procedure]; !ok {
		return nil
	}
	if !hasBrowserCSRFHeaders(header) {
		return nil
	}
	if g.token == "" || header.Get(CSRFHeader) != g.token {
		return newRPCError(connect.CodePermissionDenied, errors.New("invalid CSRF token"))
	}
	return nil
}

func hasBrowserCSRFHeaders(header http.Header) bool {
	return header.Get("Origin") != "" || header.Get("Referer") != "" || header.Get("Sec-Fetch-Site") != ""
}

func validateBrowserOrigin(header http.Header) error {
	switch strings.ToLower(header.Get("Sec-Fetch-Site")) {
	case "", "same-origin", "same-site", "none":
	case "cross-site":
		return errors.New("cross-site RPC requests are not allowed")
	default:
		return errors.New("unrecognized browser fetch metadata")
	}

	for _, raw := range []string{header.Get("Origin"), header.Get("Referer")} {
		if raw == "" {
			continue
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Hostname() == "" {
			return errors.New("invalid browser origin")
		}
		if !isAllowedBrowserHost(parsed.Hostname()) {
			return errors.New("browser origin is not allowed")
		}
	}

	return nil
}

func isAllowedBrowserHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == "localhost" ||
		host == "127.0.0.1" ||
		host == "::1" ||
		strings.HasSuffix(host, ".localhost")
}
