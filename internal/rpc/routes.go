package rpc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/models"
	"github.com/thiagojdb/rementor/internal/services"
)

func (s *ControlPlaneService) GetRoute(ctx context.Context, req *connect.Request[rementorv1.GetRouteRequest]) (*connect.Response[rementorv1.GetRouteResponse], error) {
	wsID := req.Msg.GetWorkspaceId()
	routes, version, warnings, conflicts, err := s.registry.GetRoutes(wsID)
	if err != nil {
		return nil, newRPCError(classifyRegistryError(err), err)
	}
	ws := s.registry.FindWorkspace(wsID)
	response := &rementorv1.GetRouteResponse{
		WorkspaceId:  wsID,
		Environment:  wsID,
		RouteVersion: &rementorv1.RouteVersion{Value: version},
		Routes:       routeListToProto(routes),
		Warnings:     routeWarningsToProto(warnings),
		Conflicts:    routeConflictsToProto(conflicts),
	}
	if ws != nil {
		response.Environment = ws.WorkspaceID
	}
	return connect.NewResponse(response), nil
}

func (s *ControlPlaneService) GetRouteConflicts(ctx context.Context, req *connect.Request[rementorv1.GetRouteConflictsRequest]) (*connect.Response[rementorv1.GetRouteConflictsResponse], error) {
	wsID := req.Msg.GetWorkspaceId()
	conflicts, version, warnings, err := s.registry.GetRouteConflicts(wsID)
	if err != nil {
		return nil, newRPCError(classifyRegistryError(err), err)
	}
	ws := s.registry.FindWorkspace(wsID)
	environment := wsID
	if ws != nil {
		environment = ws.WorkspaceID
	}
	return connect.NewResponse(&rementorv1.GetRouteConflictsResponse{
		WorkspaceId:  wsID,
		Environment:  environment,
		RouteVersion: &rementorv1.RouteVersion{Value: version},
		Conflicts:    routeConflictsToProto(conflicts),
		Warnings:     routeWarningsToProto(warnings),
	}), nil
}

func (s *ControlPlaneService) ResolveRoute(ctx context.Context, req *connect.Request[rementorv1.ResolveRouteRequest]) (*connect.Response[rementorv1.ResolveRouteResponse], error) {
	resolution, err := s.registry.ResolveRoute(req.Msg.GetWorkspaceId(), req.Msg.GetHost(), req.Msg.GetPath())
	if err != nil {
		return nil, newRPCError(classifyRegistryError(err), err)
	}
	return connect.NewResponse(&rementorv1.ResolveRouteResponse{Resolution: routeResolutionToProto(resolution)}), nil
}

func (s *ControlPlaneService) PlanRoute(ctx context.Context, req *connect.Request[rementorv1.PlanRouteRequest]) (*connect.Response[rementorv1.PlanRouteResponse], error) {
	mode, err := routeModeString(req.Msg.GetDesiredMode())
	if err != nil {
		return nil, newRPCError(connect.CodeInvalidArgument, err)
	}
	var pattern *string
	if req.Msg.RoutePattern != nil {
		value := req.Msg.GetRoutePattern()
		pattern = &value
	}
	plan, err := s.registry.PlanRoute(req.Msg.GetWorkspaceId(), req.Msg.GetApplicationRef(), mode, pattern)
	if err != nil {
		return nil, newRPCError(classifyRegistryError(err), err)
	}
	var expected uint64
	expectedProvided := req.Msg.GetExpectedRouteVersion() != nil
	if expectedProvided {
		expected = req.Msg.GetExpectedRouteVersion().GetValue()
	} else {
		expected = req.Msg.GetExpectedVersion()
	}
	if (expectedProvided || expected != 0) && expected != plan.BaseRouteVersion {
		return nil, newRPCErrorWithStructured(connect.CodeFailedPrecondition, &services.RouteVersionConflictError{WorkspaceID: plan.WorkspaceID, Expected: expected, Actual: plan.BaseRouteVersion}, rementorv1.ErrorCode_ERROR_CODE_CONFLICT)
	}
	return connect.NewResponse(&rementorv1.PlanRouteResponse{Plan: routePlanToProto(plan)}), nil
}

func (s *ControlPlaneService) ApplyRoute(ctx context.Context, req *connect.Request[rementorv1.ApplyRouteRequest]) (*connect.Response[rementorv1.ApplyRouteResponse], error) {
	wsID := req.Msg.GetWorkspaceId()
	var plan services.RoutePlan
	var err error
	if req.Msg.GetPlan() != nil {
		plan, err = routePlanFromProto(req.Msg.GetPlan())
		if err != nil {
			return nil, newRPCError(connect.CodeInvalidArgument, err)
		}
	} else {
		mode, modeErr := routeModeString(req.Msg.GetDesiredMode())
		if modeErr != nil {
			return nil, newRPCError(connect.CodeInvalidArgument, modeErr)
		}
		var pattern *string
		if req.Msg.RoutePattern != nil {
			value := req.Msg.GetRoutePattern()
			pattern = &value
		}
		plan, err = s.registry.PlanRoute(wsID, req.Msg.GetApplicationRef(), mode, pattern)
		if err != nil {
			return nil, newRPCError(classifyRegistryError(err), err)
		}
	}
	var expected uint64
	expectedRouteVersionProvided := req.Msg.GetExpectedRouteVersion() != nil
	if expectedRouteVersionProvided {
		expected = req.Msg.GetExpectedRouteVersion().GetValue()
	} else {
		expected = req.Msg.GetExpectedVersion()
	}
	// Zero is a valid initial route version when the optional wrapper is
	// explicitly present. Preserve that compare-and-swap value instead of
	// letting ApplyRoutePlan infer the freshly generated plan's current version.
	if expectedRouteVersionProvided && expected == 0 {
		plan.BaseRouteVersion = 0
	}
	result, err := s.registry.ApplyRoutePlan(wsID, plan, expected, req.Msg.GetIdempotencyKey(), correlationID(req.Msg.GetCorrelationId(), req.Header()))
	if err != nil {
		code := classifyRegistryError(err)
		if errors.Is(err, services.ErrRouteVersionConflict) || errors.Is(err, services.ErrRouteIdempotencyConflict) || strings.Contains(strings.ToLower(err.Error()), "route conflict") {
			code = connect.CodeFailedPrecondition
		}
		errorCode := structuredErrorCode(code)
		if errors.Is(err, services.ErrRouteVersionConflict) || errors.Is(err, services.ErrRouteIdempotencyConflict) {
			errorCode = rementorv1.ErrorCode_ERROR_CODE_CONFLICT
		}
		return nil, newRPCErrorWithStructured(code, err, errorCode)
	}
	return connect.NewResponse(&rementorv1.ApplyRouteResponse{
		Changed:            result.Changed,
		Plan:               routePlanToProto(result.Plan),
		Routes:             routeListToProto(result.Routes),
		Operation:          toProtoOperation(result.Operation),
		Verified:           result.Verified,
		VerificationStatus: result.Verification,
		Status:             result.Status,
		Degraded:           result.Degraded,
		RollbackStatus:     result.Rollback,
	}), nil
}

func (s *ControlPlaneService) SyncRoute(ctx context.Context, req *connect.Request[rementorv1.SyncRouteRequest]) (*connect.Response[rementorv1.SyncRouteResponse], error) {
	// Sync is a repair operation by default. An explicit repair=false performs
	// a read-only drift check.
	repair := true
	if req.Msg.Repair != nil {
		repair = req.Msg.GetRepair()
	}
	result, err := s.registry.SyncRoute(req.Msg.GetWorkspaceId(), correlationID(req.Msg.GetCorrelationId(), req.Header()), repair)
	if err != nil {
		var transactionErr *services.RouteTransactionError
		if errors.As(err, &transactionErr) {
			return nil, newRPCErrorWithStructured(connect.CodeInternal, err, rementorv1.ErrorCode_ERROR_CODE_INTERNAL)
		}
		return nil, newRPCError(classifyRegistryError(err), err)
	}
	return connect.NewResponse(&rementorv1.SyncRouteResponse{
		WorkspaceId:           result.WorkspaceID,
		Changed:               result.Changed,
		Verified:              result.Verified,
		Status:                result.Status,
		DesiredRouteVersion:   &rementorv1.RouteVersion{Value: result.DesiredRouteVersion},
		EffectiveRouteVersion: &rementorv1.RouteVersion{Value: result.EffectiveRouteVersion},
		Routes:                routeListToProto(result.Routes),
		Warnings:              routeWarningsToProto(result.Warnings),
		Operation:             toProtoOperation(result.Operation),
		Degraded:              result.Degraded,
		RollbackStatus:        result.Rollback,
	}), nil
}

func routeModeString(mode rementorv1.RouteMode) (string, error) {
	switch mode {
	case rementorv1.RouteMode_ROUTE_MODE_LOCAL:
		return "local", nil
	case rementorv1.RouteMode_ROUTE_MODE_REMOTE:
		return "remote", nil
	default:
		return "", fmt.Errorf("desired route mode must be local or remote")
	}
}

func routeModeProto(mode string) rementorv1.RouteMode {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "local":
		return rementorv1.RouteMode_ROUTE_MODE_LOCAL
	case "remote":
		return rementorv1.RouteMode_ROUTE_MODE_REMOTE
	case "fallback":
		return rementorv1.RouteMode_ROUTE_MODE_FALLBACK
	default:
		return rementorv1.RouteMode_ROUTE_MODE_UNSPECIFIED
	}
}

func routeToProto(route services.Route) *rementorv1.NormalizedRoute {
	return &rementorv1.NormalizedRoute{
		WorkspaceId:         route.WorkspaceID,
		Environment:         route.Environment,
		PublicHost:          route.PublicHost,
		Pattern:             route.Pattern,
		CanonicalAppId:      route.CanonicalAppID,
		ServiceId:           route.ServiceID,
		Repository:          route.Repository,
		DesiredMode:         routeModeProto(route.DesiredMode),
		EffectiveMode:       routeModeProto(route.EffectiveMode),
		Target:              route.Target,
		LocalTarget:         route.LocalTarget,
		RemoteTarget:        route.RemoteTarget,
		RemoteFallback:      route.RemoteFallback,
		UpstreamContext:     route.UpstreamContext,
		Precedence:          models.ClampInt32(route.Precedence),
		PrecedenceReason:    route.PrecedenceReason,
		Exact:               route.Exact,
		IntentionalOverride: route.IntentionalOverride,
	}
}

func routeListToProto(routes []services.Route) []*rementorv1.NormalizedRoute {
	result := make([]*rementorv1.NormalizedRoute, 0, len(routes))
	for _, route := range routes {
		result = append(result, routeToProto(route))
	}
	return result
}

func routeWarningToProto(warning services.RouteWarning) *rementorv1.RouteWarning {
	return &rementorv1.RouteWarning{Code: warning.Code, Message: warning.Message}
}

func routeWarningsToProto(warnings []services.RouteWarning) []*rementorv1.RouteWarning {
	result := make([]*rementorv1.RouteWarning, 0, len(warnings))
	for _, warning := range warnings {
		result = append(result, routeWarningToProto(warning))
	}
	return result
}

func routeConflictToProto(conflict services.RouteConflict) *rementorv1.RouteConflict {
	result := &rementorv1.RouteConflict{
		WorkspaceId: conflict.WorkspaceID, Environment: conflict.Environment, PublicHost: conflict.PublicHost,
		Pattern: conflict.Pattern, AppId: conflict.AppID, ConflictingAppId: conflict.ConflictingAppID,
		WinningAppId: conflict.WinningAppID, Reason: conflict.Reason,
		AppServiceId: conflict.AppServiceID, ConflictingServiceId: conflict.ConflictingServiceID,
		WinningServiceId: conflict.WinningServiceID, ShadowedAppId: conflict.ShadowedAppID,
		ShadowedServiceId: conflict.ShadowedServiceID, WinningPattern: conflict.WinningPattern,
		ShadowedPattern: conflict.ShadowedPattern, WinningPrecedence: models.ClampInt32(conflict.WinningPrecedence),
		ShadowedPrecedence: models.ClampInt32(conflict.ShadowedPrecedence), WinningPrecedenceReason: conflict.WinningPrecedenceReason,
		ShadowedPrecedenceReason: conflict.ShadowedPrecedenceReason, PrecedenceReason: conflict.PrecedenceReason, Intentional: conflict.Intentional,
	}
	if conflict.WinningRoute != nil {
		result.WinningRoute = routeToProto(*conflict.WinningRoute)
	}
	if conflict.ShadowedRoute != nil {
		result.ShadowedRoute = routeToProto(*conflict.ShadowedRoute)
	}
	return result
}

func routeConflictsToProto(conflicts []services.RouteConflict) []*rementorv1.RouteConflict {
	result := make([]*rementorv1.RouteConflict, 0, len(conflicts))
	for _, conflict := range conflicts {
		result = append(result, routeConflictToProto(conflict))
	}
	return result
}

func routeChangeToProto(change services.RouteChange) *rementorv1.RouteChange {
	result := &rementorv1.RouteChange{ApplicationId: change.ApplicationID}
	if change.Before != nil {
		result.Before = routeToProto(*change.Before)
	}
	if change.After != nil {
		result.After = routeToProto(*change.After)
	}
	return result
}

func routePlanToProto(plan services.RoutePlan) *rementorv1.RoutePlan {
	result := &rementorv1.RoutePlan{
		WorkspaceId:      plan.WorkspaceID,
		Environment:      plan.Environment,
		BaseRouteVersion: &rementorv1.RouteVersion{Value: plan.BaseRouteVersion},
		BaseVersion:      plan.BaseRouteVersion,
		ApplicationId:    plan.ApplicationID,
		DesiredMode:      routeModeProto(plan.DesiredMode),
		BeforeRoutes:     routeListToProto(plan.Before),
		AfterRoutes:      routeListToProto(plan.After),
		Fingerprint:      plan.Fingerprint,
	}
	if plan.RoutePattern != nil {
		value := *plan.RoutePattern
		result.RoutePattern = &value
	}
	for _, change := range plan.Changes {
		result.Changes = append(result.Changes, routeChangeToProto(change))
	}
	result.Warnings = routeWarningsToProto(plan.Warnings)
	result.Conflicts = routeConflictsToProto(plan.Conflicts)
	return result
}

func normalizedRouteFromProto(route *rementorv1.NormalizedRoute) services.Route {
	if route == nil {
		return services.Route{}
	}
	return services.Route{WorkspaceID: route.GetWorkspaceId(), Environment: route.GetEnvironment(), PublicHost: route.GetPublicHost(), Pattern: route.GetPattern(), CanonicalAppID: route.GetCanonicalAppId(), ServiceID: route.GetServiceId(), Repository: route.GetRepository(), DesiredMode: routeModeStringUnsafe(route.GetDesiredMode()), EffectiveMode: routeModeStringUnsafe(route.GetEffectiveMode()), Target: route.GetTarget(), LocalTarget: route.GetLocalTarget(), RemoteTarget: route.GetRemoteTarget(), RemoteFallback: route.GetRemoteFallback(), UpstreamContext: route.GetUpstreamContext(), Precedence: int(route.GetPrecedence()), PrecedenceReason: route.GetPrecedenceReason(), Exact: route.GetExact(), IntentionalOverride: route.GetIntentionalOverride()}
}

func routeModeStringUnsafe(mode rementorv1.RouteMode) string {
	switch mode {
	case rementorv1.RouteMode_ROUTE_MODE_LOCAL:
		return "local"
	case rementorv1.RouteMode_ROUTE_MODE_REMOTE:
		return "remote"
	case rementorv1.RouteMode_ROUTE_MODE_FALLBACK:
		return "fallback"
	default:
		return ""
	}
}

func routePlanFromProto(plan *rementorv1.RoutePlan) (services.RoutePlan, error) {
	if plan == nil {
		return services.RoutePlan{}, fmt.Errorf("route plan is required")
	}
	mode, err := routeModeString(plan.GetDesiredMode())
	if err != nil {
		return services.RoutePlan{}, err
	}
	result := services.RoutePlan{WorkspaceID: plan.GetWorkspaceId(), Environment: plan.GetEnvironment(), ApplicationID: plan.GetApplicationId(), DesiredMode: mode, Fingerprint: plan.GetFingerprint()}
	if plan.GetBaseRouteVersion() != nil {
		result.BaseRouteVersion = plan.GetBaseRouteVersion().GetValue()
	} else {
		result.BaseRouteVersion = plan.GetBaseVersion()
	}
	if plan.RoutePattern != nil {
		value := plan.GetRoutePattern()
		result.RoutePattern = &value
	}
	for _, route := range plan.GetBeforeRoutes() {
		result.Before = append(result.Before, normalizedRouteFromProto(route))
	}
	for _, route := range plan.GetAfterRoutes() {
		result.After = append(result.After, normalizedRouteFromProto(route))
	}
	for _, warning := range plan.GetWarnings() {
		result.Warnings = append(result.Warnings, services.RouteWarning{Code: warning.GetCode(), Message: warning.GetMessage()})
	}
	for _, conflict := range plan.GetConflicts() {
		result.Conflicts = append(result.Conflicts, routeConflictFromProto(conflict))
	}
	return result, nil
}

func routeConflictFromProto(conflict *rementorv1.RouteConflict) services.RouteConflict {
	if conflict == nil {
		return services.RouteConflict{}
	}
	result := services.RouteConflict{
		WorkspaceID: conflict.GetWorkspaceId(), Environment: conflict.GetEnvironment(), PublicHost: conflict.GetPublicHost(),
		Pattern: conflict.GetPattern(), AppID: conflict.GetAppId(), ConflictingAppID: conflict.GetConflictingAppId(),
		WinningAppID: conflict.GetWinningAppId(), Reason: conflict.GetReason(), AppServiceID: conflict.GetAppServiceId(),
		ConflictingServiceID: conflict.GetConflictingServiceId(), WinningServiceID: conflict.GetWinningServiceId(),
		ShadowedAppID: conflict.GetShadowedAppId(), ShadowedServiceID: conflict.GetShadowedServiceId(),
		WinningPattern: conflict.GetWinningPattern(), ShadowedPattern: conflict.GetShadowedPattern(),
		WinningPrecedence: int(conflict.GetWinningPrecedence()), ShadowedPrecedence: int(conflict.GetShadowedPrecedence()),
		WinningPrecedenceReason: conflict.GetWinningPrecedenceReason(), ShadowedPrecedenceReason: conflict.GetShadowedPrecedenceReason(), PrecedenceReason: conflict.GetPrecedenceReason(),
		Intentional: conflict.GetIntentional(),
	}
	if conflict.GetWinningRoute() != nil {
		value := normalizedRouteFromProto(conflict.GetWinningRoute())
		result.WinningRoute = &value
	}
	if conflict.GetShadowedRoute() != nil {
		value := normalizedRouteFromProto(conflict.GetShadowedRoute())
		result.ShadowedRoute = &value
	}
	return result
}

func routeResolutionToProto(resolution services.RouteResolution) *rementorv1.RouteResolution {
	result := &rementorv1.RouteResolution{WorkspaceId: resolution.WorkspaceID, Environment: resolution.Environment, Host: resolution.Host, Path: resolution.Path, MatchingPattern: resolution.MatchingPattern, CanonicalAppId: resolution.CanonicalAppID, ServiceId: resolution.ServiceID, Target: resolution.Target, Precedence: int32(resolution.Precedence), PrecedenceReason: resolution.PrecedenceReason}
	if resolution.Route != nil {
		result.Route = routeToProto(*resolution.Route)
	}
	return result
}

func newRPCErrorWithStructured(code connect.Code, err error, structuredCode rementorv1.ErrorCode) *connect.Error {
	if err == nil {
		err = fmt.Errorf("request failed")
	}
	rpcErr := connect.NewError(code, err)
	metadata := map[string]string{}
	var versionConflict *services.RouteVersionConflictError
	if errors.As(err, &versionConflict) {
		metadata["workspaceId"] = versionConflict.WorkspaceID
		metadata["expectedVersion"] = fmt.Sprintf("%d", versionConflict.Expected)
		metadata["actualVersion"] = fmt.Sprintf("%d", versionConflict.Actual)
	}
	var bindingErr *services.BrowserURLBindingError
	if errors.As(err, &bindingErr) {
		metadata["workspaceId"] = bindingErr.WorkspaceID
		metadata["applicationId"] = bindingErr.AppID
		metadata["field"] = bindingErr.Field
	}
	var ambiguousErr *models.AmbiguousApplicationError
	if errors.As(err, &ambiguousErr) {
		metadata["reference"] = ambiguousErr.Reference
		metadata["matches"] = strings.Join(ambiguousErr.Matches, ",")
	}
	var transactionErr *services.RouteTransactionError
	if errors.As(err, &transactionErr) {
		if transactionErr.Operation != nil {
			metadata["operationId"] = transactionErr.Operation.OperationID
			metadata["correlationId"] = transactionErr.Operation.CorrelationID
		}
		metadata["transactionStatus"] = transactionErr.Status
		metadata["rollbackStatus"] = transactionErr.Rollback
		metadata["degraded"] = fmt.Sprintf("%t", transactionErr.Degraded)
	}
	detail, detailErr := connect.NewErrorDetail(&rementorv1.StructuredError{Code: structuredCode, Message: err.Error(), Metadata: metadata})
	if detailErr == nil {
		rpcErr.AddDetail(detail)
	}
	return rpcErr
}
