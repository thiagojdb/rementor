package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestParseMCPProtocolMode(t *testing.T) {
	for _, value := range []string{"auto", "modern", "legacy", " MODERN "} {
		if _, err := parseMCPProtocolMode(value); err != nil {
			t.Fatalf("parseMCPProtocolMode(%q) failed: %v", value, err)
		}
	}
	if _, err := parseMCPProtocolMode("future"); err == nil {
		t.Fatal("expected invalid protocol mode to fail")
	}
}

func TestMCPLegacyInitializeRemainsAvailable(t *testing.T) {
	server := &mcpServer{mode: mcpProtocolAuto}
	response := server.handle(mcpRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params:  json.RawMessage(`{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1"}}`),
	})

	if response == nil || response.Error != nil {
		t.Fatalf("unexpected initialize response: %#v", response)
	}
	result := resultMap(t, response.Result)
	if result["protocolVersion"] != mcpLegacyProtocolVersion {
		t.Fatalf("unexpected legacy version: %#v", result)
	}
	if _, ok := result["resultType"]; ok {
		t.Fatalf("legacy response unexpectedly contains resultType: %#v", result)
	}
	if server.mode != mcpProtocolLegacy {
		t.Fatalf("auto mode did not lock to legacy: %q", server.mode)
	}
}

func TestMCPModernDiscoverSelectsStatelessProtocol(t *testing.T) {
	server := &mcpServer{mode: mcpProtocolAuto}
	response := server.handle(modernMCPRequest(1, "server/discover", map[string]any{}))

	if response == nil || response.Error != nil {
		t.Fatalf("unexpected discover response: %#v", response)
	}
	result := resultMap(t, response.Result)
	if result["resultType"] != "complete" {
		t.Fatalf("modern result has no complete discriminator: %#v", result)
	}
	versions, ok := result["supportedVersions"].([]any)
	if !ok || len(versions) != 1 || versions[0] != mcpModernProtocolVersion {
		t.Fatalf("unexpected supported versions: %#v", result["supportedVersions"])
	}
	meta, ok := result["_meta"].(map[string]any)
	if !ok || meta["io.modelcontextprotocol/serverInfo"] == nil {
		t.Fatalf("modern result has no server info: %#v", result)
	}
	if server.mode != mcpProtocolModern {
		t.Fatalf("auto mode did not lock to modern: %q", server.mode)
	}
}

func TestMCPModernToolsListWorksWithoutInitialize(t *testing.T) {
	server := &mcpServer{mode: mcpProtocolAuto}
	response := server.handle(modernMCPRequest(2, "tools/list", map[string]any{}))

	if response == nil || response.Error != nil {
		t.Fatalf("unexpected tools/list response: %#v", response)
	}
	result := resultMap(t, response.Result)
	if result["resultType"] != "complete" || result["cacheScope"] != "public" {
		t.Fatalf("modern list metadata is missing: %#v", result)
	}
	tools, ok := result["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("tools/list returned no tools: %#v", result["tools"])
	}
}

func TestMCPModernRequestRequiresCapabilities(t *testing.T) {
	server := &mcpServer{mode: mcpProtocolModern}
	params := json.RawMessage(`{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}`)
	response := server.handle(mcpRequest{JSONRPC: "2.0", ID: 1, Method: "tools/list", Params: params})

	if response == nil || response.Error == nil || response.Error.Code != -32602 {
		t.Fatalf("expected invalid params, got %#v", response)
	}
}

func TestMCPModernRequestRejectsUnsupportedVersion(t *testing.T) {
	server := &mcpServer{mode: mcpProtocolAuto}
	request := modernMCPRequest(1, "tools/list", map[string]any{})
	var params map[string]any
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatal(err)
	}
	params["_meta"].(map[string]any)["io.modelcontextprotocol/protocolVersion"] = "2099-01-01"
	request.Params, _ = json.Marshal(params)
	response := server.handle(request)

	if response == nil || response.Error == nil || response.Error.Code != -32022 {
		t.Fatalf("expected unsupported version, got %#v", response)
	}
	data := response.Error.Data.(map[string]any)
	if data["requested"] != "2099-01-01" {
		t.Fatalf("unexpected unsupported-version data: %#v", data)
	}
}

func TestMCPModernRejectsLegacyInitializeWithDiagnostic(t *testing.T) {
	server := &mcpServer{mode: mcpProtocolModern}
	response := server.handle(mcpRequest{JSONRPC: "2.0", ID: 1, Method: "initialize", Params: json.RawMessage(`{}`)})
	if response == nil || response.Error == nil || response.Error.Code != -32601 {
		t.Fatalf("expected method not found, got %#v", response)
	}
}

func TestMCPUnknownMethodUsesJSONRPCMethodNotFound(t *testing.T) {
	server := &mcpServer{mode: mcpProtocolModern}
	response := server.handle(modernMCPRequest(1, "unknown/method", map[string]any{}))
	if response == nil || response.Error == nil || response.Error.Code != -32601 {
		t.Fatalf("expected method not found, got %#v", response)
	}
}

func TestMCPFramingRetainsLegacyCompatibility(t *testing.T) {
	requestJSON := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`

	lineRequest, framed, err := readMCPRequest(bufio.NewReader(strings.NewReader(requestJSON + "\n")))
	if err != nil || framed || lineRequest.Method != "initialize" {
		t.Fatalf("newline framing failed: request=%#v framed=%v err=%v", lineRequest, framed, err)
	}

	framedInput := "Content-Length: " + jsonNumber(len(requestJSON)) + "\r\n\r\n" + requestJSON
	contentLengthRequest, framed, err := readMCPRequest(bufio.NewReader(strings.NewReader(framedInput)))
	if err != nil || !framed || contentLengthRequest.Method != "initialize" {
		t.Fatalf("Content-Length compatibility failed: request=%#v framed=%v err=%v", contentLengthRequest, framed, err)
	}

	var output bytes.Buffer
	if err := writeMCPResponse(&output, &mcpResponse{JSONRPC: "2.0", ID: 1, Result: map[string]any{}}, true); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(output.String(), "Content-Length: ") {
		t.Fatalf("framed response was not retained: %q", output.String())
	}
}

func modernMCPRequest(id any, method string, extra map[string]any) mcpRequest {
	params := map[string]any{
		"_meta": map[string]any{
			"io.modelcontextprotocol/protocolVersion":    mcpModernProtocolVersion,
			"io.modelcontextprotocol/clientCapabilities": map[string]any{},
			"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "test", "version": "1"},
		},
	}
	for key, value := range extra {
		params[key] = value
	}
	payload, _ := json.Marshal(params)
	return mcpRequest{JSONRPC: "2.0", ID: id, Method: method, Params: payload}
}

func resultMap(t *testing.T, result any) map[string]any {
	t.Helper()
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func jsonNumber(value int) string {
	payload, _ := json.Marshal(value)
	return string(payload)
}
