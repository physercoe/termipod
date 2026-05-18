package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// tools.get resolves a name across the *composed* catalog — base +
// extra + orchestration + authority (ADR-031 rollout plan §0.1). The
// known case deliberately probes one authority tool and one base
// tool to prove both halves are reachable through the single
// meta-tool, not just one registry's slice.
func TestMCP_ToolsGet_Known(t *testing.T) {
	s, _ := newTestServer(t)

	for _, name := range []string{"documents.get", "get_feed"} {
		out, jerr := s.mcpToolsGet(json.RawMessage(`{"tool_name":"` + name + `"}`))
		if jerr != nil {
			t.Fatalf("tools.get(%q): unexpected jrpcError %+v", name, jerr)
		}
		if m, ok := out.(map[string]any); ok && m["isError"] == true {
			t.Fatalf("tools.get(%q): unexpected isError result", name)
		}
		body := mcpResultTextBody(t, out)
		var def map[string]any
		if err := json.Unmarshal([]byte(body), &def); err != nil {
			t.Fatalf("tools.get(%q): body not JSON: %v — %s", name, err, body)
		}
		if def["name"] != name {
			t.Errorf("tools.get(%q): result name = %v, want %q", name, def["name"], name)
		}
		if d, _ := def["description"].(string); d == "" {
			t.Errorf("tools.get(%q): empty description", name)
		}
		if def["inputSchema"] == nil {
			t.Errorf("tools.get(%q): missing inputSchema", name)
		}
	}
}

// An unknown name is a recoverable failure: tools.get returns an
// isError content block (not a *jrpcError), so the agent sees the
// message inline and can retry against tools/list. A missing
// tool_name, by contrast, is a protocol fault → *jrpcError.
func TestMCP_ToolsGet_Unknown(t *testing.T) {
	s, _ := newTestServer(t)

	out, jerr := s.mcpToolsGet(json.RawMessage(`{"tool_name":"nope.does_not_exist"}`))
	if jerr != nil {
		t.Fatalf("tools.get(unknown): want isError result, got jrpcError %+v", jerr)
	}
	m, ok := out.(map[string]any)
	if !ok || m["isError"] != true {
		t.Fatalf("tools.get(unknown): result not flagged isError: %+v", out)
	}
	body := mcpResultTextBody(t, out)
	if !strings.Contains(body, "unknown tool") || !strings.Contains(body, "tools/list") {
		t.Errorf("tools.get(unknown): message missing recovery guidance: %s", body)
	}

	if _, jerr := s.mcpToolsGet(json.RawMessage(`{}`)); jerr == nil {
		t.Error("tools.get(no args): want jrpcError for missing tool_name, got nil")
	}
}
