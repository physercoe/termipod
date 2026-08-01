package hostrunner

import (
	"encoding/json"
	"testing"

	"github.com/termipod/hub/internal/mcpver"
	"github.com/termipod/hub/internal/mcpwire"
)

// Lane U on the host-runner UDS gateway — the third of the three Go
// servers that used to carry its own copy of the version set. handleLine
// is driven directly here: the framing is already covered by
// TestGateway_InitializeAndToolsList, and what changed is the shape of
// what comes back.

func gwCall(t *testing.T, line string) map[string]any {
	t.Helper()
	g := &McpGateway{}
	raw := g.handleLine([]byte(line))
	if len(raw) == 0 {
		t.Fatalf("no response for %s", line)
	}
	var resp struct {
		Result map[string]any `json:"result"`
		Error  *gwRespError   `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode: %v (raw %s)", err, raw)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	return resp.Result
}

func TestGateway_InitializeEchoesEveryKnownRevision(t *testing.T) {
	for _, v := range mcpver.Supported() {
		result := gwCall(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+v+`"}}`)
		if result["protocolVersion"] != v {
			t.Errorf("initialize(%q) answered %v", v, result["protocolVersion"])
		}
	}
}

func TestGateway_InitializeAnswersTheFloorForAnUnknownRevision(t *testing.T) {
	result := gwCall(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2027-01-01"}}`)
	if result["protocolVersion"] != mcpver.Floor {
		t.Errorf("protocolVersion = %v, want the floor %q", result["protocolVersion"], mcpver.Floor)
	}
}

func TestGateway_MetaDeclaredRevisionIsHonoured(t *testing.T) {
	result := gwCall(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`)
	if result["protocolVersion"] != "2026-07-28" {
		t.Errorf("protocolVersion = %v; a _meta-declared revision must be honoured", result["protocolVersion"])
	}
}

func TestGateway_ServerDiscoverAdvertisesTheWholeSet(t *testing.T) {
	result := gwCall(t, `{"jsonrpc":"2.0","id":1,"method":"server/discover"}`)
	raw, ok := result["protocolVersions"].([]any)
	if !ok {
		t.Fatalf("no protocolVersions: %v", result)
	}
	want := mcpver.Supported()
	if len(raw) != len(want) {
		t.Fatalf("advertised %v, want %v", raw, want)
	}
	for i, v := range raw {
		if v != want[i] {
			t.Errorf("revision %d: %v, want %q", i, v, want[i])
		}
	}
}

// This catalog is hand-written — there is no ToolSpec.ReadOnly behind it —
// so the fail-closed rule is the thing under test: only the named
// observers claim readOnlyHint, and every other tool here writes to the
// hub.
func TestGateway_ToolsListAnnotationsAreFailClosed(t *testing.T) {
	result := gwCall(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if result["cacheScope"] != mcpwire.CacheScopePrivate {
		t.Errorf("cacheScope = %v, want %q", result["cacheScope"], mcpwire.CacheScopePrivate)
	}
	tools, _ := result["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("no tools")
	}
	sawPing := false
	for _, raw := range tools {
		tool := raw.(map[string]any)
		name, _ := tool["name"].(string)
		ann, ok := tool["annotations"].(map[string]any)
		if !ok {
			t.Fatalf("tool %q has no annotations", name)
		}
		if ann["title"] != mcpwire.ToolTitle(name) {
			t.Errorf("tool %q title = %v, want %q", name, ann["title"], mcpwire.ToolTitle(name))
		}
		wantReadOnly := gwReadOnlyTools[name]
		if ann["readOnlyHint"] != wantReadOnly {
			t.Errorf("tool %q readOnlyHint = %v, want %v", name, ann["readOnlyHint"], wantReadOnly)
		}
		if name == "host.ping" {
			sawPing = true
			if ann["readOnlyHint"] != true {
				t.Error("host.ping observes without mutating; it should be the one read-only tool here")
			}
		}
	}
	if !sawPing {
		t.Error("host.ping missing from the catalog — the allow-list is asserting nothing")
	}
}

func TestGateway_EveryResultCarriesServerIdentity(t *testing.T) {
	for _, method := range []string{"initialize", "server/discover", "tools/list", "ping"} {
		result := gwCall(t, `{"jsonrpc":"2.0","id":1,"method":"`+method+`"}`)
		meta, ok := result["_meta"].(map[string]any)
		if !ok {
			t.Errorf("%s: result carries no _meta", method)
			continue
		}
		info, ok := meta[mcpwire.MetaServerInfo].(map[string]any)
		if !ok {
			t.Errorf("%s: _meta carries no serverInfo", method)
			continue
		}
		if info["name"] != gwServerName {
			t.Errorf("%s: serverInfo.name = %v, want %q", method, info["name"], gwServerName)
		}
		if result["resultType"] != mcpwire.ResultTypeComplete {
			t.Errorf("%s: resultType = %v", method, result["resultType"])
		}
	}
}

// ADR-033 D-5: the gateway's dot-named tools stay. It serves claude M4
// only, where dots are safe, and renaming them is a wire break for zero
// semantic gain — 2026-07-28 legalizing more characters changes nothing.
func TestGateway_DotNamedToolsSurvive(t *testing.T) {
	result := gwCall(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools, _ := result["tools"].([]any)
	found := false
	for _, raw := range tools {
		if raw.(map[string]any)["name"] == "host.ping" {
			found = true
		}
	}
	if !found {
		t.Error("host.ping was filtered off the wire; this gateway must keep its dot-named tools")
	}
}
