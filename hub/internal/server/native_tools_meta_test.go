package server

import (
	"encoding/json"
	"testing"
)

// Every native tool must have a nativeToolMeta row (ADR-031 W2.b) —
// same fail-closed guard as the authority side.
func TestNativeToolMeta_EveryToolCovered(t *testing.T) {
	for _, tl := range buildNativeTools() {
		if _, ok := nativeToolMeta[tl.Name]; !ok {
			t.Errorf("native tool %q has no nativeToolMeta row — add one (W2.b overlay)", tl.Name)
		}
	}
	known := map[string]bool{}
	for _, tl := range buildNativeTools() {
		known[tl.Name] = true
	}
	for name := range nativeToolMeta {
		if !known[name] {
			t.Errorf("nativeToolMeta has a row for %q, which is not a native tool", name)
		}
	}
}

// tools_get returns the ADR-031 D-1 structured payload: the
// operational flags (fail-closed pair) and the see_also discovery
// list, on both registries.
func TestMCP_ToolsGet_StructuredPayload(t *testing.T) {
	s, _ := newTestServer(t)

	cases := []struct {
		tool          string
		wantReadOnly  bool
		seeAlsoMember string
	}{
		{"documents_get", true, "get_project_doc"},   // authority, read
		{"documents_create", false, "documents_list"}, // authority, write
		{"get_project_doc", true, "documents_get"},    // native, read
		{"agents_fanout", false, "agents_gather"},     // native, write
	}
	for _, c := range cases {
		out, jerr := s.mcpToolsGet(json.RawMessage(`{"tool_name":"` + c.tool + `"}`))
		if jerr != nil {
			t.Fatalf("tools_get(%q): %+v", c.tool, jerr)
		}
		var def map[string]any
		if err := json.Unmarshal([]byte(mcpResultTextBody(t, out)), &def); err != nil {
			t.Fatalf("tools_get(%q): body not JSON: %v", c.tool, err)
		}
		cs, _ := def["concurrency_safe"].(bool)
		se, _ := def["side_effecting"].(bool)
		if cs != c.wantReadOnly {
			t.Errorf("%s: concurrency_safe = %v, want %v", c.tool, cs, c.wantReadOnly)
		}
		if se == c.wantReadOnly {
			t.Errorf("%s: side_effecting = %v, must be the inverse of concurrency_safe", c.tool, se)
		}
		see, _ := def["see_also"].([]any)
		found := false
		for _, v := range see {
			if v == c.seeAlsoMember {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: see_also %v missing expected member %q", c.tool, see, c.seeAlsoMember)
		}
	}
}

// The slim tools/list projection must NOT carry the D-1 structured
// fields — they ride only in tools_get (W2.a keeps the catalog small).
func TestMCP_ToolListDefs_OmitsStructuredPayload(t *testing.T) {
	for _, def := range mcpToolListDefs() {
		for _, k := range []string{"see_also", "examples", "failure_modes"} {
			if _, present := def[k]; present {
				name, _ := def["name"].(string)
				t.Errorf("tools/list entry %q carries %q — structured payload belongs in tools_get only", name, k)
			}
		}
	}
}
