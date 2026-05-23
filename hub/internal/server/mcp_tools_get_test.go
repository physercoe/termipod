package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// tools.get resolves a name across the *composed* catalog — the
// authority registry plus the native registry (ADR-033 §0.1). The
// known case deliberately probes one authority tool and one native
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

// MCP spec requires tool names to match `[A-Za-z0-9_-]+` — no dots.
// Our catalog keeps deprecated dot-named aliases (`documents.list`,
// `plans.steps.create`) for backwards-compat with agents calling the
// legacy names; tools/call still resolves them. But strict clients
// (agy 1.0.1 confirmed) reject the WHOLE tools/list payload with
// `invalid request` if any entry violates the regex, so we filter
// dot-named entries off the wire. The post-v1.0.653 smoke caught this
// — agy's `call_mcp_tool projects_list` failed with `server name
// termipod failed to load: failed to get tools: calling "tools/list":
// invalid request` even though the bridge + token were now fine.
//
// Every dropped name has a snake_case sibling in the wire output, so
// no functionality is lost; the dispatcher continues to accept both
// spellings on tools/call.
func TestMCP_ToolListDefs_FiltersDotNamedAliases(t *testing.T) {
	defs := mcpToolListDefs()
	names := map[string]bool{}
	for _, def := range defs {
		n, _ := def["name"].(string)
		names[n] = true
	}
	// Sample dot-named names that DO exist in the full catalog but
	// MUST be filtered from the wire.
	mustNotShip := []string{
		"documents.list", "documents.get",
		"projects.list", "projects.get",
		"plans.steps.create", "plans.steps.list",
		"agents.list", "agents.spawn",
	}
	for _, n := range mustNotShip {
		if names[n] {
			t.Errorf("tools/list ships %q — MCP spec forbids `.` in tool names; agy 1.0.1 rejects the whole batch", n)
		}
	}
	// Sample snake_case canonical siblings — these MUST ship.
	mustShip := []string{
		"documents_list", "documents_get",
		"projects_list", "projects_get",
		"plan_steps_create", "plan_steps_list",
		"agents_list", "agents_spawn",
	}
	for _, n := range mustShip {
		if !names[n] {
			t.Errorf("tools/list missing canonical %q (was supposed to replace dot-named alias)", n)
		}
	}
}

func TestIsMCPCompliantToolName(t *testing.T) {
	cases := map[string]bool{
		"documents_list":     true,
		"plan-steps-create":  true,
		"AgentsList":         true,
		"x":                  true,
		"abc123":             true,
		"":                   false,
		"documents.list":     false,
		"plans.steps.create": false,
		"foo bar":            false,
		"name$":              false,
		"with/slash":         false,
	}
	for name, want := range cases {
		if got := isMCPCompliantToolName(name); got != want {
			t.Errorf("isMCPCompliantToolName(%q) = %v; want %v", name, got, want)
		}
	}
}

// W2.a — tools/list serves the slim `short` projection: every entry
// carries a non-empty `short`, its `description` equals that `short`
// (the long body is dropped from the wire). tools.get, by contrast,
// still carries the long body — so the description text it returns is
// the long one, not the short.
//
// v1.0.654: tools/list also drops MCP-spec-noncompliant names (dots),
// so the wire is shorter than the full catalog — that's intentional.
// Every dropped entry has a snake_case sibling that stays in the wire,
// so no functionality is lost.
func TestMCP_ToolListDefs_ServesShort(t *testing.T) {
	defs := mcpToolListDefs()
	full := mcpToolDefs()
	if len(defs) > len(full) {
		t.Fatalf("tools/list has %d entries, full catalog %d — projection inflated", len(defs), len(full))
	}
	if len(defs) == len(full) {
		// Should have dropped at least the documents.list / documents.get etc.
		t.Errorf("tools/list (%d) equals full catalog (%d) — expected dot-named aliases to be filtered for MCP-spec compliance", len(defs), len(full))
	}

	listDescLen := 0
	for _, def := range defs {
		name, _ := def["name"].(string)
		short, _ := def["short"].(string)
		desc, _ := def["description"].(string)
		if short == "" {
			t.Errorf("tool %q: empty short in tools/list", name)
		}
		if desc != short {
			t.Errorf("tool %q: tools/list description must equal short, got %q vs %q", name, desc, short)
		}
		if def["inputSchema"] == nil {
			t.Errorf("tool %q: missing inputSchema in tools/list", name)
		}
		listDescLen += len(desc)
	}

	// The long catalog still carries the full body — the projection is
	// what shrinks the wire, so the sum of long descriptions must
	// dominate the sum of shorts. Pick the tool with the longest body
	// to probe tools.get below.
	fullDescLen, widest := 0, ""
	widestLen := 0
	for _, def := range full {
		d, _ := def["description"].(string)
		fullDescLen += len(d)
		if n, _ := def["name"].(string); len(d) > widestLen {
			widestLen, widest = len(d), n
		}
	}
	if listDescLen >= fullDescLen {
		t.Errorf("tools/list descriptions (%d B) not smaller than the full catalog (%d B) — projection did nothing",
			listDescLen, fullDescLen)
	}
	t.Logf("description bytes: tools/list %d, full catalog %d", listDescLen, fullDescLen)

	// tools.get still resolves the full long body, distinct from short.
	s, _ := newTestServer(t)
	out, jerr := s.mcpToolsGet(json.RawMessage(`{"tool_name":"` + widest + `"}`))
	if jerr != nil {
		t.Fatalf("tools.get(%q): %+v", widest, jerr)
	}
	var def map[string]any
	if err := json.Unmarshal([]byte(mcpResultTextBody(t, out)), &def); err != nil {
		t.Fatalf("tools.get body not JSON: %v", err)
	}
	long, _ := def["description"].(string)
	short, _ := def["short"].(string)
	if long == "" || short == "" {
		t.Fatalf("tools.get(%q): short=%q long=%q", widest, short, long)
	}
	if len(long) <= len(short) {
		t.Errorf("tools.get(%q): description (%d B) should be longer than short (%d B) — tools.get must serve the long body",
			widest, len(long), len(short))
	}
}
