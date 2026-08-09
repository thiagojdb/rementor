package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

const (
	mcpLegacyProtocolVersion = "2024-11-05"
	mcpModernProtocolVersion = "2026-07-28"
	mcpServerName            = "mcp-rementor"
	mcpServerVersion         = "1.0.0"
)

type mcpProtocolMode string

const (
	mcpProtocolAuto   mcpProtocolMode = "auto"
	mcpProtocolModern mcpProtocolMode = "modern"
	mcpProtocolLegacy mcpProtocolMode = "legacy"
)

type mcpServer struct {
	client    *Client
	serverURL string
	mode      mcpProtocolMode
}

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *mcpError `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type mcpToolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type mcpToolResult struct {
	Content           []mcpTextContent `json:"content"`
	StructuredContent any              `json:"structuredContent,omitempty"`
	IsError           bool             `json:"isError,omitempty"`
}

type mcpTextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpAppRouteResult struct {
	Changed       bool                  `json:"changed"`
	PreviousRoute string                `json:"previousRoute"`
	CurrentRoute  string                `json:"currentRoute"`
	Application   ApplicationDTO        `json:"application"`
	Plan          *RoutePlanDTO         `json:"plan,omitempty"`
	Operation     *OperationMetadataDTO `json:"operation,omitempty"`
}

type mcpToggleResult struct {
	PreviousRoute string         `json:"previousRoute"`
	CurrentRoute  string         `json:"currentRoute"`
	Application   ApplicationDTO `json:"application"`
}

type mcpCompactWorkspace struct {
	ID                   string                     `json:"id"`
	Type                 string                     `json:"type"`
	Name                 string                     `json:"name"`
	LocalDomain          string                     `json:"localDomain,omitempty"`
	DefaultRemoteBaseURL string                     `json:"defaultRemoteBaseUrl,omitempty"`
	ApplicationCount     int                        `json:"applicationCount"`
	LocalCount           int                        `json:"localCount"`
	RemoteCount          int                        `json:"remoteCount"`
	UnhealthyLocalCount  int                        `json:"unhealthyLocalCount"`
	Environment          WorkspaceEnvironmentRefDTO `json:"environment"`
	Route                *RouteStateDTO             `json:"route,omitempty"`
}

type mcpCompactApplication struct {
	ID                 string                     `json:"id"`
	AppID              string                     `json:"appId,omitempty"`
	ServiceID          string                     `json:"serviceId,omitempty"`
	Repository         string                     `json:"repository,omitempty"`
	Aliases            []string                   `json:"aliases,omitempty"`
	Name               string                     `json:"name"`
	Route              string                     `json:"route"`
	RouteState         *RouteStateDTO             `json:"routeState,omitempty"`
	Path               string                     `json:"path"`
	PublicPath         string                     `json:"publicPath,omitempty"`
	UpstreamContext    string                     `json:"upstreamContext,omitempty"`
	FrontendRoot       string                     `json:"frontendRoot,omitempty"`
	FrontendRootSource string                     `json:"frontendRootSource,omitempty"`
	Domain             string                     `json:"domain,omitempty"`
	Port               int                        `json:"port"`
	HealthStatus       string                     `json:"healthStatus"`
	RemoteStatus       string                     `json:"remoteStatus,omitempty"`
	URL                string                     `json:"url,omitempty"`
	Environment        WorkspaceEnvironmentRefDTO `json:"environment"`
	RouteOverride      bool                       `json:"routeOverride,omitempty"`
}

type mcpApplicationUpsertResult struct {
	Status      string                `json:"status"`
	Application ApplicationDTO        `json:"application"`
	Operation   *OperationMetadataDTO `json:"operation,omitempty"`
	Warnings    []RouteWarningDTO     `json:"warnings,omitempty"`
}

type mcpAnnounceResult struct {
	Workspace   string                `json:"workspace"`
	App         string                `json:"app"`
	Status      string                `json:"status"`
	Activated   bool                  `json:"activated"`
	URL         string                `json:"url,omitempty"`
	Application ApplicationDTO        `json:"application"`
	Operation   *OperationMetadataDTO `json:"operation,omitempty"`
}

// MCPCmd runs rementorctl as a dual-era stdio MCP server.
func MCPCmd(client *Client, serverURL string, args []string) {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	protocol := fs.String("protocol", string(mcpProtocolAuto), "MCP protocol mode: auto, modern, or legacy")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "unexpected mcp arguments: %s\n", strings.Join(fs.Args(), " "))
		os.Exit(2)
	}
	mode, err := parseMCPProtocolMode(*protocol)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	server := &mcpServer{client: client, serverURL: serverURL, mode: mode}
	if err := server.serve(os.Stdin, os.Stdout); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintf(os.Stderr, "mcp error: %v\n", err)
		os.Exit(1)
	}
}

func parseMCPProtocolMode(value string) (mcpProtocolMode, error) {
	mode := mcpProtocolMode(strings.ToLower(strings.TrimSpace(value)))
	switch mode {
	case mcpProtocolAuto, mcpProtocolModern, mcpProtocolLegacy:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid MCP protocol mode %q: expected auto, modern, or legacy", value)
	}
}

func (s *mcpServer) serve(input io.Reader, output io.Writer) error {
	reader := bufio.NewReader(input)
	for {
		req, framed, err := readMCPRequest(reader)
		if err != nil {
			return err
		}
		resp := s.handle(req)
		if resp == nil {
			continue
		}
		if err := writeMCPResponse(output, resp, framed); err != nil {
			return err
		}
	}
}

func readMCPRequest(reader *bufio.Reader) (mcpRequest, bool, error) {
	first, err := reader.Peek(1)
	if err != nil {
		return mcpRequest{}, false, err
	}
	if first[0] == '{' {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return mcpRequest{}, false, err
		}
		var req mcpRequest
		if err := json.Unmarshal(bytes.TrimSpace(line), &req); err != nil {
			return mcpRequest{}, false, err
		}
		return req, false, nil
	}

	contentLength := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return mcpRequest{}, true, err
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			break
		}
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), "Content-Length") {
			parsed, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil {
				return mcpRequest{}, true, err
			}
			contentLength = parsed
		}
	}
	if contentLength < 0 {
		return mcpRequest{}, true, fmt.Errorf("missing Content-Length header")
	}

	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return mcpRequest{}, true, err
	}
	var req mcpRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return mcpRequest{}, true, err
	}
	return req, true, nil
}

func writeMCPResponse(output io.Writer, resp *mcpResponse, framed bool) error {
	payload, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	if framed {
		_, err = fmt.Fprintf(output, "Content-Length: %d\r\n\r\n%s", len(payload), payload)
		return err
	}
	_, err = fmt.Fprintf(output, "%s\n", payload)
	return err
}

func (s *mcpServer) handle(req mcpRequest) (resp *mcpResponse) {
	defer func() {
		if recovered := recover(); recovered != nil {
			message := fmt.Sprint(recovered)
			resp = mcpErr(req.ID, -32000, message, map[string]any{"type": classifyMCPError(errors.New(message)), "message": message})
		}
	}()

	if req.JSONRPC != "2.0" {
		return mcpErr(req.ID, -32600, "Invalid Request", nil)
	}
	if req.Method == "notifications/initialized" || req.Method == "notifications/cancelled" {
		return nil
	}

	requestVersion, hasVersion := mcpRequestProtocolVersion(req.Params)
	mode := s.selectProtocolMode(req, hasVersion)
	if mode == mcpProtocolModern {
		if req.Method == "initialize" {
			return mcpErr(req.ID, -32601, "Method not found", map[string]any{"message": "initialize is not used by MCP 2026-07-28; use server/discover or call methods directly"})
		}
		if err := validateModernMCPRequest(req.Params); err != nil {
			return mcpErr(req.ID, -32602, "Invalid params", map[string]any{"message": err.Error()})
		}
		if requestVersion != mcpModernProtocolVersion {
			return unsupportedMCPVersion(req.ID, requestVersion, []string{mcpModernProtocolVersion})
		}
	} else if hasVersion {
		return unsupportedMCPVersion(req.ID, requestVersion, []string{mcpLegacyProtocolVersion})
	}

	var result any
	var err error
	switch req.Method {
	case "initialize":
		result = map[string]any{
			"protocolVersion": mcpLegacyProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      mcpServerInfo(),
		}
	case "server/discover":
		if mode != mcpProtocolModern {
			return mcpErr(req.ID, -32601, "Method not found", nil)
		}
		result = map[string]any{
			"supportedVersions": []string{mcpModernProtocolVersion},
			"capabilities":      map[string]any{"tools": map[string]any{}},
			"instructions":      "Use the Rementor tools to inspect and control local or remote application routing.",
			"ttlMs":             300000,
			"cacheScope":        "public",
		}
	case "ping":
		if mode == mcpProtocolModern {
			return mcpErr(req.ID, -32601, "Method not found", nil)
		}
		result = map[string]any{}
	case "tools/list":
		result = map[string]any{"tools": mcpToolList()}
		if mode == mcpProtocolModern {
			result.(map[string]any)["ttlMs"] = 300000
			result.(map[string]any)["cacheScope"] = "public"
		}
	case "tools/call":
		result, err = s.handleToolCall(req.Params)
	default:
		return mcpErr(req.ID, -32601, "Method not found", map[string]any{"method": req.Method})
	}
	if err != nil {
		data := map[string]any{"type": classifyMCPError(err), "message": err.Error()}
		if apiErr, ok := err.(*APIError); ok {
			data["statusCode"] = apiErr.StatusCode
			if apiErr.Code != "" {
				data["code"] = apiErr.Code
			}
		}
		return mcpErr(req.ID, -32000, err.Error(), data)
	}
	if mode == mcpProtocolModern {
		result = modernMCPResult(result)
	}
	return &mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
}

func (s *mcpServer) selectProtocolMode(req mcpRequest, hasVersion bool) mcpProtocolMode {
	if s.mode != mcpProtocolAuto {
		return s.mode
	}
	if req.Method == "initialize" {
		s.mode = mcpProtocolLegacy
	} else if req.Method == "server/discover" || hasVersion {
		s.mode = mcpProtocolModern
	} else {
		// Preserve the historical behavior for legacy harnesses that call a
		// method before initialize. A conforming modern client always supplies
		// its protocol version in per-request metadata.
		return mcpProtocolLegacy
	}
	return s.mode
}

func mcpRequestProtocolVersion(raw json.RawMessage) (string, bool) {
	var params struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &params) != nil || params.Meta == nil {
		return "", false
	}
	value, ok := params.Meta["io.modelcontextprotocol/protocolVersion"]
	if !ok || json.Unmarshal(value, new(string)) != nil {
		return "", ok
	}
	var version string
	_ = json.Unmarshal(value, &version)
	return version, true
}

func validateModernMCPRequest(raw json.RawMessage) error {
	var params struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if len(raw) == 0 {
		return errors.New("params._meta is required")
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return fmt.Errorf("params must be an object: %w", err)
	}
	if params.Meta == nil {
		return errors.New("params._meta is required")
	}
	version, ok := params.Meta["io.modelcontextprotocol/protocolVersion"]
	if !ok {
		return errors.New("params._meta.io.modelcontextprotocol/protocolVersion is required")
	}
	var versionString string
	if err := json.Unmarshal(version, &versionString); err != nil || versionString == "" {
		return errors.New("params._meta.io.modelcontextprotocol/protocolVersion must be a non-empty string")
	}
	capabilities, ok := params.Meta["io.modelcontextprotocol/clientCapabilities"]
	if !ok {
		return errors.New("params._meta.io.modelcontextprotocol/clientCapabilities is required")
	}
	var capabilitiesObject map[string]any
	if err := json.Unmarshal(capabilities, &capabilitiesObject); err != nil || capabilitiesObject == nil {
		return errors.New("params._meta.io.modelcontextprotocol/clientCapabilities must be an object")
	}
	return nil
}

func unsupportedMCPVersion(id any, requested string, supported []string) *mcpResponse {
	return mcpErr(id, -32022, "Unsupported protocol version", map[string]any{
		"supported": supported,
		"requested": requested,
	})
}

func modernMCPResult(result any) map[string]any {
	var modern map[string]any
	payload, err := json.Marshal(result)
	if err == nil {
		_ = json.Unmarshal(payload, &modern)
	}
	if modern == nil {
		modern = map[string]any{"value": result}
	}
	modern["resultType"] = "complete"
	modern["_meta"] = map[string]any{"io.modelcontextprotocol/serverInfo": mcpServerInfo()}
	return modern
}

func mcpServerInfo() map[string]string {
	return map[string]string{"name": mcpServerName, "version": mcpServerVersion}
}

func (s *mcpServer) handleToolCall(raw json.RawMessage) (any, error) {
	var params mcpToolCallParams
	if len(raw) == 0 {
		return nil, fmt.Errorf("tools/call params must be an object")
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, err
	}
	if params.Arguments == nil {
		params.Arguments = map[string]any{}
	}

	switch params.Name {
	case "rementor.status":
		return s.toolStatus()
	case "rementor.workspaces":
		return s.toolWorkspaces()
	case "rementor.workspace_get":
		return s.toolWorkspaceGet(params.Arguments)
	case "rementor.workspace_create":
		return s.toolWorkspaceCreate(params.Arguments)
	case "rementor.apps":
		return s.toolApps(params.Arguments)
	case "rementor.app_get":
		return s.toolAppGet(params.Arguments)
	case "rementor.app_resolve":
		return s.toolAppResolve(params.Arguments)
	case "rementor.app_alias":
		return s.toolAppAlias(params.Arguments)
	case "rementor.app_register":
		return s.toolAppRegister(params.Arguments)
	case "rementor.app_announce":
		return s.toolAppAnnounce(params.Arguments)
	case "rementor.app_unregister":
		return s.toolAppUnregister(params.Arguments)
	case "rementor.app_set_route":
		return s.toolAppSetRoute(params.Arguments)
	case "rementor.app_toggle":
		return s.toolAppToggle(params.Arguments)
	case "rementor.workspace_set_all":
		return s.toolWorkspaceSetAll(params.Arguments)
	case "rementor.sync_routing":
		return s.toolSyncRouting(params.Arguments)
	case "rementor_route_get":
		return s.toolRouteGet(params.Arguments)
	case "rementor_route_resolve":
		return s.toolRouteResolve(params.Arguments)
	case "rementor_route_plan":
		return s.toolRoutePlan(params.Arguments)
	case "rementor_route_apply":
		return s.toolRouteApply(params.Arguments)
	case "rementor_route_sync":
		return s.toolRouteSync(params.Arguments)
	case "rementor.route_pattern_get":
		return s.toolRoutePatternGet(params.Arguments)
	case "rementor.route_pattern_set":
		return s.toolRoutePatternSet(params.Arguments)
	default:
		return nil, fmt.Errorf("unknown tool: %s", params.Name)
	}
}

func (s *mcpServer) toolStatus() (any, error) {
	resp, err := http.Get(strings.TrimRight(s.serverURL, "/") + "/healthz")
	if err != nil {
		return toolResult("Rementor is not reachable", map[string]any{
			"running":      false,
			"baseUrl":      s.serverURL,
			"dashboardUrl": "http://rementor.localhost/",
		}), nil
	}
	defer resp.Body.Close()
	running := resp.StatusCode >= 200 && resp.StatusCode < 300
	text := "Rementor is not reachable"
	if running {
		text = "Rementor is running"
	}
	return toolResult(text, map[string]any{
		"running":      running,
		"baseUrl":      s.serverURL,
		"dashboardUrl": "http://rementor.localhost/",
	}), nil
}

func (s *mcpServer) toolWorkspaces() (any, error) {
	workspaces, err := s.listWorkspaces()
	if err != nil {
		return nil, err
	}
	summary := map[string]int{"total": len(workspaces), "routing": 0, "localApps": 0}
	compact := make([]mcpCompactWorkspace, 0, len(workspaces))
	for _, ws := range workspaces {
		if ws.Type == "local-apps" {
			summary["localApps"]++
		} else {
			summary["routing"]++
		}
		compact = append(compact, compactWorkspace(ws))
	}
	return toolResult(fmt.Sprintf("Resolved %d Rementor workspace(s)", len(workspaces)), map[string]any{
		"summary":    summary,
		"workspaces": compact,
	}), nil
}

func (s *mcpServer) toolWorkspaceGet(args map[string]any) (any, error) {
	workspace, err := s.getWorkspace(requiredString(args, "workspace"))
	if err != nil {
		return nil, err
	}
	return toolResult(fmt.Sprintf("Workspace %s has %d app(s)", workspace.ID, len(workspace.Applications)), workspace), nil
}

func (s *mcpServer) toolWorkspaceCreate(args map[string]any) (any, error) {
	req := CreateWorkspaceRequest{
		ID:                   requiredString(args, "workspace"),
		Type:                 optionalString(args, "type"),
		Name:                 optionalString(args, "name"),
		Color:                optionalString(args, "color"),
		LocalDomain:          optionalString(args, "local_domain"),
		DefaultRemoteBaseURL: optionalString(args, "default_remote_base_url"),
		StrictMetadata:       optionalBool(args, "strict_metadata"),
	}
	if req.Type == "" {
		req.Type = "routing"
	}
	workspace, err := s.client.CreateWorkspace(context.Background(), req)
	if err != nil {
		return nil, err
	}
	return toolResult(fmt.Sprintf("Workspace %s created", workspace.ID), workspace), nil
}

func (s *mcpServer) toolApps(args map[string]any) (any, error) {
	workspace, err := s.getWorkspace(requiredString(args, "workspace"))
	if err != nil {
		return nil, err
	}
	local := 0
	unhealthyLocal := 0
	apps := make([]mcpCompactApplication, 0, len(workspace.Applications))
	for _, app := range workspace.Applications {
		if app.Active {
			local++
			if app.HealthStatus == "unhealthy" {
				unhealthyLocal++
			}
		}
		apps = append(apps, compactApplication(workspace, app))
	}
	return toolResult(fmt.Sprintf("Resolved %d app(s): %d local, %d remote", len(apps), local, len(apps)-local), map[string]any{
		"summary": map[string]int{
			"total":          len(apps),
			"local":          local,
			"remote":         len(apps) - local,
			"unhealthyLocal": unhealthyLocal,
		},
		"applications": apps,
	}), nil
}

func (s *mcpServer) toolAppGet(args map[string]any) (any, error) {
	app, err := s.getApp(requiredString(args, "workspace"), requiredString(args, "app"))
	if err != nil {
		return nil, err
	}
	return toolResult(fmt.Sprintf("App %s is routed %s", app.ID, routeOf(app)), app), nil
}

func (s *mcpServer) toolAppResolve(args map[string]any) (any, error) {
	workspace := requiredString(args, "workspace")
	ref := requiredString(args, "app")
	app, err := s.client.ResolveApplication(context.Background(), workspace, ref)
	if err != nil {
		return nil, err
	}
	return toolResult(fmt.Sprintf("Resolved %s to app %s", ref, app.ID), app), nil
}

func (s *mcpServer) toolAppAlias(args map[string]any) (any, error) {
	workspace := requiredString(args, "workspace")
	ref := requiredString(args, "app")
	alias := requiredString(args, "alias")
	app, err := s.client.RegisterApplicationAlias(context.Background(), workspace, ref, alias)
	if err != nil {
		return nil, err
	}
	return toolResult(fmt.Sprintf("Alias %s registered for app %s", alias, app.ID), app), nil
}

func (s *mcpServer) toolAppRegister(args map[string]any) (any, error) {
	workspaceID := requiredString(args, "workspace")
	appID := requiredString(args, "app")
	input := appInputFromArgs(appID, args)
	result, err := s.registerApp(workspaceID, input)
	if err != nil {
		return nil, err
	}
	return toolResult(fmt.Sprintf("App %s %s", result.Application.ID, result.Status), result), nil
}

func (s *mcpServer) toolAppAnnounce(args map[string]any) (any, error) {
	workspaceID := requiredString(args, "workspace")
	appID := requiredString(args, "app")
	workspace, err := s.ensureWorkspace(workspaceID, optionalString(args, "type"), optionalString(args, "local_domain"))
	if err != nil {
		return nil, err
	}
	path := optionalString(args, "path")
	domain := optionalString(args, "domain")
	if workspace.Type == "local-apps" {
		if domain == "" {
			domain = appID + ".localhost"
		}
	} else if path == "" {
		path = "/" + appID
	}
	args["path"] = path
	args["domain"] = domain
	registered, err := s.registerApp(workspaceID, appInputFromArgs(appID, args))
	if err != nil {
		return nil, err
	}
	application := registered.Application
	activated := false
	operation := registered.Operation
	if !optionalBool(args, "no_activate") {
		set, err := s.setRoute(workspaceID, appID, "local")
		if err != nil {
			return nil, err
		}
		activated = set.Changed
		application = set.Application
		if set.Operation != nil {
			operation = set.Operation
		}
	}
	result := mcpAnnounceResult{
		Workspace:   workspaceID,
		App:         appID,
		Status:      registered.Status,
		Activated:   activated,
		URL:         buildMCPAppURL(workspace, path, domain),
		Application: application,
		Operation:   operation,
	}
	return toolResult(fmt.Sprintf("App %s %s; route is %s", appID, registered.Status, routeOf(application)), result), nil
}

func (s *mcpServer) toolAppUnregister(args map[string]any) (any, error) {
	workspaceID := requiredString(args, "workspace")
	appID := requiredString(args, "app")
	operation, err := s.client.DeleteApplicationWithMetadata(context.Background(), workspaceID, appID)
	if err != nil {
		return nil, err
	}
	return toolResult(fmt.Sprintf("App %s unregistered from %s", appID, workspaceID), map[string]any{
		"status": "unregistered", "workspace": workspaceID, "app": appID, "operation": operation,
	}), nil
}

func (s *mcpServer) toolAppSetRoute(args map[string]any) (any, error) {
	result, err := s.setRoute(requiredString(args, "workspace"), requiredString(args, "app"), requiredRoute(args))
	if err != nil {
		return nil, err
	}
	suffix := ""
	if !result.Changed {
		suffix = " (already set)"
	}
	return toolResult(fmt.Sprintf("App route is %s%s", result.CurrentRoute, suffix), result), nil
}

func (s *mcpServer) toolAppToggle(args map[string]any) (any, error) {
	result, err := s.toggleApp(requiredString(args, "workspace"), requiredString(args, "app"))
	if err != nil {
		return nil, err
	}
	return toolResult(fmt.Sprintf("App toggled from %s to %s", result.PreviousRoute, result.CurrentRoute), result), nil
}

func (s *mcpServer) toolWorkspaceSetAll(args map[string]any) (any, error) {
	route := requiredRoute(args)
	var result ToggleResultResponse
	var err error
	if route == "local" {
		result, err = s.client.ToggleAllToLocal(context.Background(), requiredString(args, "workspace"))
	} else {
		result, err = s.client.ToggleAllToRemote(context.Background(), requiredString(args, "workspace"))
	}
	if err != nil {
		return nil, err
	}
	return toolResult(fmt.Sprintf("Updated routes: %d succeeded, %d failed", result.SuccessCount, result.FailureCount), result), nil
}

func (s *mcpServer) toolSyncRouting(args map[string]any) (any, error) {
	result, err := s.client.SyncRoute(context.Background(), requiredString(args, "workspace"), true, optionalString(args, "correlation_id"))
	if err != nil {
		return nil, err
	}
	return toolResult("Rementor routing synced", result), nil
}

func (s *mcpServer) toolRouteGet(args map[string]any) (any, error) {
	result, err := s.client.GetRoutes(context.Background(), requiredString(args, "workspace"))
	if err != nil {
		return nil, err
	}
	return toolResult(fmt.Sprintf("Resolved %d route(s)", len(result.Routes)), result), nil
}

func (s *mcpServer) toolRouteResolve(args map[string]any) (any, error) {
	result, err := s.client.ResolveRoute(context.Background(), requiredString(args, "workspace"), optionalString(args, "host"), optionalString(args, "path"))
	if err != nil {
		return nil, err
	}
	return toolResult(fmt.Sprintf("Resolved %s%s to %s", result.Host, result.Path, result.Target), result), nil
}

func (s *mcpServer) toolRoutePlan(args map[string]any) (any, error) {
	mode := requiredString(args, "mode")
	var pattern *string
	if value, ok := args["pattern"].(string); ok {
		pattern = &value
	}
	if optionalBool(args, "clear_pattern") {
		value := ""
		pattern = &value
	}
	result, err := s.client.PlanRoute(context.Background(), PlanRouteRequest{WorkspaceID: requiredString(args, "workspace"), ApplicationRef: requiredString(args, "app"), DesiredMode: mode, RoutePattern: pattern, ExpectedVersion: optionalUint64(args, "expected_version"), CorrelationID: optionalString(args, "correlation_id"), StrictMetadata: optionalBool(args, "strict_metadata")})
	if err != nil {
		return nil, err
	}
	return toolResult(fmt.Sprintf("Planned %d route change(s)", len(result.Changes)), result), nil
}

func (s *mcpServer) toolRouteApply(args map[string]any) (any, error) {
	request := ApplyRouteRequest{WorkspaceID: requiredString(args, "workspace"), ApplicationRef: optionalString(args, "app"), DesiredMode: optionalString(args, "mode"), ExpectedVersion: optionalUint64(args, "expected_version"), IdempotencyKey: optionalString(args, "idempotency_key"), CorrelationID: optionalString(args, "correlation_id"), StrictMetadata: optionalBool(args, "strict_metadata")}
	if value, ok := args["pattern"].(string); ok {
		request.RoutePattern = &value
	}
	if optionalBool(args, "clear_pattern") {
		value := ""
		request.RoutePattern = &value
	}
	if raw, ok := args["plan"]; ok && raw != nil {
		payload, err := json.Marshal(raw)
		if err != nil {
			return nil, err
		}
		var plan RoutePlanDTO
		if err := json.Unmarshal(payload, &plan); err != nil {
			return nil, fmt.Errorf("invalid route plan: %w", err)
		}
		request.Plan = &plan
		if request.ApplicationRef == "" {
			request.ApplicationRef = plan.ApplicationID
		}
	}
	if request.Plan == nil && (request.ApplicationRef == "" || request.DesiredMode == "") {
		return nil, fmt.Errorf("app and mode are required unless plan is supplied")
	}
	result, err := s.client.ApplyRoute(context.Background(), request)
	if err != nil {
		return nil, err
	}
	return toolResult("Route applied", result), nil
}

func (s *mcpServer) toolRouteSync(args map[string]any) (any, error) {
	result, err := s.client.SyncRoute(context.Background(), requiredString(args, "workspace"), true, optionalString(args, "correlation_id"))
	if err != nil {
		return nil, err
	}
	return toolResult("Route synchronization complete", result), nil
}

func (s *mcpServer) toolRoutePatternGet(args map[string]any) (any, error) {
	result, err := s.client.GetRoutePattern(context.Background(), requiredString(args, "workspace"), requiredString(args, "app"))
	if err != nil {
		return nil, err
	}
	text := "No route pattern configured"
	if result.Pattern != nil && *result.Pattern != "" {
		text = "Route pattern: " + *result.Pattern
	}
	return toolResult(text, result), nil
}

func (s *mcpServer) toolRoutePatternSet(args map[string]any) (any, error) {
	req := UpdateRoutePatternRequest{Pattern: optionalString(args, "pattern")}
	app, err := s.client.UpdateRoutePattern(context.Background(), requiredString(args, "workspace"), requiredString(args, "app"), req)
	if err != nil {
		return nil, err
	}
	return toolResult("Route pattern updated", app), nil
}

func (s *mcpServer) listWorkspaces() ([]WorkspaceDTO, error) {
	return s.client.ListWorkspaces(context.Background())
}

func (s *mcpServer) getWorkspace(id string) (WorkspaceDTO, error) {
	return s.client.GetWorkspace(context.Background(), id)
}

func (s *mcpServer) getApp(workspaceID, appID string) (ApplicationDTO, error) {
	return s.client.GetApplication(context.Background(), workspaceID, appID)
}

func (s *mcpServer) ensureWorkspace(workspaceID, wsType, localDomain string) (WorkspaceDTO, error) {
	workspace, err := s.getWorkspace(workspaceID)
	if err == nil {
		return workspace, nil
	}
	if apiErr, ok := err.(*APIError); !ok || apiErr.StatusCode != http.StatusNotFound {
		return WorkspaceDTO{}, err
	}
	if wsType == "" {
		wsType = "routing"
	}
	if wsType == "routing" && localDomain == "" {
		return WorkspaceDTO{}, fmt.Errorf("local_domain is required when creating a routing workspace")
	}
	req := CreateWorkspaceRequest{ID: workspaceID, Type: wsType, LocalDomain: localDomain}
	workspace, err = s.client.CreateWorkspace(context.Background(), req)
	if err != nil {
		return WorkspaceDTO{}, err
	}
	return workspace, nil
}

func (s *mcpServer) registerApp(workspaceID string, input ApplicationConfigInput) (mcpApplicationUpsertResult, error) {
	workspace, err := s.getWorkspace(workspaceID)
	if err != nil {
		return mcpApplicationUpsertResult{}, err
	}
	_, status := upsertApp(workspace.Applications, input)
	if status != "unchanged" {
		upserted, err := s.client.UpsertApplication(context.Background(), workspaceID, input)
		if err != nil {
			return mcpApplicationUpsertResult{}, err
		}
		return mcpApplicationUpsertResult{Status: status, Application: upserted.Application, Operation: upserted.Operation, Warnings: upserted.Warnings}, nil
	}
	app, err := s.getApp(workspaceID, input.ID)
	if err != nil {
		return mcpApplicationUpsertResult{}, err
	}
	return mcpApplicationUpsertResult{Status: status, Application: app}, nil
}

func (s *mcpServer) setRoute(workspaceID, appID, route string) (mcpAppRouteResult, error) {
	app, err := s.getApp(workspaceID, appID)
	if err != nil {
		return mcpAppRouteResult{}, err
	}
	current := routeOf(app)
	if current == route {
		return mcpAppRouteResult{Changed: false, PreviousRoute: current, CurrentRoute: current, Application: app}, nil
	}
	plan, err := s.client.PlanRoute(context.Background(), PlanRouteRequest{WorkspaceID: workspaceID, ApplicationRef: appID, DesiredMode: route})
	if err != nil {
		return mcpAppRouteResult{}, err
	}
	applied, err := s.client.ApplyRoute(context.Background(), ApplyRouteRequest{WorkspaceID: workspaceID, ApplicationRef: appID, DesiredMode: route, Plan: &plan})
	if err != nil {
		return mcpAppRouteResult{}, err
	}
	updated, err := s.getApp(workspaceID, appID)
	if err != nil {
		return mcpAppRouteResult{}, err
	}
	return mcpAppRouteResult{Changed: applied.Changed, PreviousRoute: current, CurrentRoute: routeOf(updated), Application: updated, Plan: &applied.Plan, Operation: applied.Operation}, nil
}

func (s *mcpServer) toggleApp(workspaceID, appID string) (mcpToggleResult, error) {
	before, err := s.getApp(workspaceID, appID)
	if err != nil {
		return mcpToggleResult{}, err
	}
	after, err := s.client.ToggleApplication(context.Background(), workspaceID, appID)
	if err != nil {
		return mcpToggleResult{}, err
	}
	return mcpToggleResult{PreviousRoute: routeOf(before), CurrentRoute: routeOf(after), Application: after}, nil
}

func mcpToolList() []map[string]any {
	return []map[string]any{
		toolSchema("rementor.status", "Check whether the local Rementor server is reachable.", emptySchema()),
		toolSchema("rementor.workspaces", "List Rementor workspaces in compact form with route counts.", emptySchema()),
		toolSchema("rementor.workspace_get", "Get a full workspace, including all applications.", workspaceSchema()),
		toolSchema("rementor.workspace_create", "Create a Rementor workspace.", objectSchema(map[string]any{
			"workspace":               map[string]any{"type": "string"},
			"type":                    map[string]any{"type": "string", "enum": []string{"routing", "local-apps"}, "default": "routing"},
			"name":                    map[string]any{"type": "string"},
			"color":                   map[string]any{"type": "string"},
			"local_domain":            map[string]any{"type": "string"},
			"default_remote_base_url": map[string]any{"type": "string"},
			"strict_metadata":         map[string]any{"type": "boolean"},
		}, []string{"workspace"})),
		toolSchema("rementor.apps", "List applications in a workspace in compact form.", workspaceSchema()),
		toolSchema("rementor.app_get", "Get full application details.", workspaceAppSchema()),
		toolSchema("rementor.app_resolve", "Resolve a canonical application ID or alias in a workspace.", workspaceAppSchema()),
		toolSchema("rementor.app_alias", "Register an unambiguous normalized alias for an application identity.", objectSchema(map[string]any{
			"workspace": map[string]any{"type": "string"}, "app": map[string]any{"type": "string"}, "alias": map[string]any{"type": "string"},
		}, []string{"workspace", "app", "alias"})),
		toolSchema("rementor.app_register", "Upsert application metadata without changing its current local/remote route.", appMetadataSchema(false)),
		toolSchema("rementor.app_announce", "Ensure workspace/app exists and activate local routing unless no_activate is true.", appMetadataSchema(true)),
		toolSchema("rementor.app_unregister", "Remove an application from a workspace.", workspaceAppSchema()),
		toolSchema("rementor.app_set_route", "Ensure an app routes to local or remote. This checks current state and toggles only if needed.", routeSchema()),
		toolSchema("rementor.app_toggle", "Flip an app route between local and remote. Prefer app_set_route when the intended target is known.", workspaceAppSchema()),
		toolSchema("rementor.workspace_set_all", "Route all applications in a workspace to local or remote.", workspaceRouteSchema()),
		toolSchema("rementor.sync_routing", "Force Rementor to regenerate and reload routing for a workspace.", workspaceSchema()),
		toolSchema("rementor_route_get", "Inspect the normalized routes for a workspace.", workspaceSchema()),
		toolSchema("rementor_route_resolve", "Resolve a public host and request path to its winning route.", objectSchema(map[string]any{"workspace": map[string]any{"type": "string"}, "host": map[string]any{"type": "string"}, "path": map[string]any{"type": "string", "default": "/"}}, []string{"workspace"})),
		toolSchema("rementor_route_plan", "Create a deterministic, non-mutating route plan.", objectSchema(map[string]any{"workspace": map[string]any{"type": "string"}, "app": map[string]any{"type": "string"}, "mode": map[string]any{"type": "string", "enum": []string{"local", "remote"}}, "pattern": map[string]any{"type": "string"}, "clear_pattern": map[string]any{"type": "boolean"}, "expected_version": map[string]any{"type": "integer"}, "strict_metadata": map[string]any{"type": "boolean"}}, []string{"workspace", "app", "mode"})),
		toolSchema("rementor_route_apply", "Apply a route plan with version and idempotency checks.", objectSchema(map[string]any{"workspace": map[string]any{"type": "string"}, "app": map[string]any{"type": "string"}, "mode": map[string]any{"type": "string", "enum": []string{"local", "remote"}}, "plan": map[string]any{"type": "object"}, "pattern": map[string]any{"type": "string"}, "clear_pattern": map[string]any{"type": "boolean"}, "expected_version": map[string]any{"type": "integer"}, "idempotency_key": map[string]any{"type": "string"}, "strict_metadata": map[string]any{"type": "boolean"}}, []string{"workspace"})),
		toolSchema("rementor_route_sync", "Reconcile a workspace's desired routes with the loaded proxy configuration.", workspaceSchema()),
		toolSchema("rementor.route_pattern_get", "Get the route pattern for an application.", workspaceAppSchema()),
		toolSchema("rementor.route_pattern_set", "Set or clear the route pattern for an application. Omit pattern or pass empty string to clear.", objectSchema(map[string]any{
			"workspace": map[string]any{"type": "string"}, "app": map[string]any{"type": "string"}, "pattern": map[string]any{"type": "string"},
		}, []string{"workspace", "app"})),
	}
}

func toolSchema(name, description string, inputSchema map[string]any) map[string]any {
	return map[string]any{"name": name, "description": description, "inputSchema": inputSchema}
}

func emptySchema() map[string]any {
	return objectSchema(map[string]any{}, nil)
}

func workspaceSchema() map[string]any {
	return objectSchema(map[string]any{"workspace": map[string]any{"type": "string"}}, []string{"workspace"})
}

func workspaceAppSchema() map[string]any {
	return objectSchema(map[string]any{"workspace": map[string]any{"type": "string"}, "app": map[string]any{"type": "string"}}, []string{"workspace", "app"})
}

func routeSchema() map[string]any {
	return objectSchema(map[string]any{"workspace": map[string]any{"type": "string"}, "app": map[string]any{"type": "string"}, "route": map[string]any{"type": "string", "enum": []string{"local", "remote"}}}, []string{"workspace", "app", "route"})
}

func workspaceRouteSchema() map[string]any {
	return objectSchema(map[string]any{"workspace": map[string]any{"type": "string"}, "route": map[string]any{"type": "string", "enum": []string{"local", "remote"}}}, []string{"workspace", "route"})
}

func appMetadataSchema(announce bool) map[string]any {
	props := map[string]any{
		"workspace":            map[string]any{"type": "string"},
		"app":                  map[string]any{"type": "string"},
		"port":                 map[string]any{"type": "number"},
		"path":                 map[string]any{"type": "string"},
		"domain":               map[string]any{"type": "string"},
		"remote_base_url":      map[string]any{"type": "string"},
		"context":              map[string]any{"type": "string"},
		"public_path":          map[string]any{"type": "string"},
		"upstream_context":     map[string]any{"type": "string"},
		"frontend_root":        map[string]any{"type": "string"},
		"frontend_root_source": map[string]any{"type": "string"},
		"strict_metadata":      map[string]any{"type": "boolean"},
		"name":                 map[string]any{"type": "string"},
		"health":               map[string]any{"type": "string"},
		"app_id":               map[string]any{"type": "string"},
		"service_id":           map[string]any{"type": "string"},
		"repository":           map[string]any{"type": "string"},
		"aliases":              map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"route_override":       map[string]any{"type": "boolean", "description": "mark overlapping route ownership as intentional"},
	}
	if announce {
		props["type"] = map[string]any{"type": "string", "enum": []string{"routing", "local-apps"}, "default": "routing"}
		props["local_domain"] = map[string]any{"type": "string"}
		props["no_activate"] = map[string]any{"type": "boolean", "default": false}
	}
	return objectSchema(props, []string{"workspace", "app", "port"})
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func toolResult(text string, structured any) mcpToolResult {
	return mcpToolResult{Content: []mcpTextContent{{Type: "text", Text: text}}, StructuredContent: structured}
}

func mcpErr(id any, code int, message string, data any) *mcpResponse {
	return &mcpResponse{JSONRPC: "2.0", ID: id, Error: &mcpError{Code: code, Message: message, Data: data}}
}

func classifyMCPError(err error) string {
	if _, ok := err.(*APIError); ok {
		return "REMENTOR_API_ERROR"
	}
	if strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "must be") {
		return "INVALID_PARAMS"
	}
	return "INTERNAL_ERROR"
}

func requiredString(args map[string]any, name string) string {
	value, _ := args[name].(string)
	if strings.TrimSpace(value) == "" {
		panic(fmt.Sprintf("%s is required", name))
	}
	return value
}

func optionalString(args map[string]any, name string) string {
	value, _ := args[name].(string)
	return value
}

func optionalBool(args map[string]any, name string) bool {
	value, _ := args[name].(bool)
	return value
}

func optionalBoolPtr(args map[string]any, name string) *bool {
	value, ok := args[name].(bool)
	if !ok {
		return nil
	}
	return &value
}

func optionalUint64(args map[string]any, name string) uint64 {
	switch value := args[name].(type) {
	case float64:
		if value < 0 {
			return 0
		}
		return uint64(value)
	case int:
		if value < 0 {
			return 0
		}
		return uint64(value)
	case uint64:
		return value
	default:
		return 0
	}
}

func requiredInt(args map[string]any, name string) int {
	switch value := args[name].(type) {
	case float64:
		return int(value)
	case int:
		return value
	default:
		panic(fmt.Sprintf("%s is required", name))
	}
}

func requiredRoute(args map[string]any) string {
	route := requiredString(args, "route")
	if route != "local" && route != "remote" {
		panic("route must be local or remote")
	}
	return route
}

func appInputFromArgs(appID string, args map[string]any) ApplicationConfigInput {
	return ApplicationConfigInput{
		ID:                 appID,
		AppID:              optionalString(args, "app_id"),
		ServiceID:          optionalString(args, "service_id"),
		Repository:         optionalString(args, "repository"),
		Aliases:            optionalStrings(args, "aliases"),
		Name:               optionalString(args, "name"),
		Path:               optionalString(args, "path"),
		Domain:             optionalString(args, "domain"),
		RemoteBaseUrl:      optionalString(args, "remote_base_url"),
		Port:               requiredInt(args, "port"),
		Health:             optionalString(args, "health"),
		Context:            optionalString(args, "context"),
		PublicPath:         optionalString(args, "public_path"),
		UpstreamContext:    optionalString(args, "upstream_context"),
		FrontendRoot:       optionalString(args, "frontend_root"),
		FrontendRootSource: optionalString(args, "frontend_root_source"),
		StrictMetadata:     optionalBool(args, "strict_metadata"),
		RouteOverride:      optionalBoolPtr(args, "route_override"),
	}
}

func optionalStrings(args map[string]any, name string) []string {
	values, _ := args[name].([]any)
	if len(values) == 0 {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			result = append(result, text)
		}
	}
	return result
}

func compactWorkspace(ws WorkspaceDTO) mcpCompactWorkspace {
	local := 0
	unhealthy := 0
	for _, app := range ws.Applications {
		if app.Active {
			local++
			if app.HealthStatus == "unhealthy" {
				unhealthy++
			}
		}
	}
	result := mcpCompactWorkspace{ID: ws.ID, Type: ws.Type, Name: ws.Name, ApplicationCount: len(ws.Applications), LocalCount: local, RemoteCount: len(ws.Applications) - local, UnhealthyLocalCount: unhealthy, Environment: ws.Environment, Route: ws.Route}
	if ws.Routing != nil {
		result.LocalDomain = ws.Routing.LocalDomain
		result.DefaultRemoteBaseURL = ws.Routing.DefaultRemoteBaseURL
	}
	return result
}

func compactApplication(ws WorkspaceDTO, app ApplicationDTO) mcpCompactApplication {
	publicPath := app.PublicPath
	if publicPath == "" {
		publicPath = app.Path
	}
	return mcpCompactApplication{ID: app.ID, AppID: app.AppID, ServiceID: app.ServiceID, Repository: app.Repository, Aliases: append([]string(nil), app.Aliases...), Name: app.Name, Route: routeOf(app), RouteState: app.Route, Path: app.Path, PublicPath: publicPath, UpstreamContext: app.UpstreamContext, FrontendRoot: app.FrontendRoot, FrontendRootSource: app.FrontendRootSource, Domain: app.Domain, Port: app.Port, HealthStatus: app.HealthStatus, RemoteStatus: app.RemoteStatus, URL: buildMCPAppURL(ws, publicPath, app.Domain), Environment: ws.Environment, RouteOverride: app.RouteOverride}
}

func routeOf(app ApplicationDTO) string {
	if app.Route != nil && app.Route.DesiredMode != "" {
		return app.Route.DesiredMode
	}
	if app.Active {
		return "local"
	}
	return "remote"
}

func buildMCPAppURL(ws WorkspaceDTO, appPathValue, domain string) string {
	if ws.Type == "local-apps" {
		if domain == "" {
			return ""
		}
		return "http://" + domain
	}
	if ws.Routing == nil || ws.Routing.LocalDomain == "" {
		return ""
	}
	return "http://" + ws.Routing.LocalDomain + appPathValue
}
