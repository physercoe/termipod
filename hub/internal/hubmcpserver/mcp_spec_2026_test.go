package hubmcpserver

import (
	"encoding/json"
	"testing"

	"github.com/termipod/hub/internal/mcpver"
	"github.com/termipod/hub/internal/mcpwire"
)

// Lane U on the stdio daemon. Everything here is one of the four servers
// answering the same contract — the point of ADR-063 D1 is that they
// cannot drift, so each one is asserted against the same shared package
// rather than against a copied literal.

func daemonCall(t *testing.T, line string) map[string]any {
	t.Helper()
	c := newHubClient("http://127.0.0.1:1", "", "team-alpha")
	raw, ok := handleLine(c, buildTools(), []byte(line+"\n"))
	if !ok {
		t.Fatalf("expected a response for an id'd request")
	}
	var resp struct {
		Result map[string]any `json:"result"`
		Error  *jsonrpcError  `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode: %v (raw %s)", err, raw)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	return resp.Result
}

func TestDaemon_InitializeEchoesEveryKnownRevision(t *testing.T) {
	for _, v := range mcpver.Supported() {
		result := daemonCall(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+v+`"}}`)
		if result["protocolVersion"] != v {
			t.Errorf("initialize(%q) answered %v", v, result["protocolVersion"])
		}
	}
}

func TestDaemon_InitializeAnswersTheFloorForAnUnknownRevision(t *testing.T) {
	result := daemonCall(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2027-01-01"}}`)
	if result["protocolVersion"] != mcpver.Floor {
		t.Errorf("protocolVersion = %v, want the floor %q", result["protocolVersion"], mcpver.Floor)
	}
}

// Stdio has no headers, so `_meta` is the ONLY way a stateless
// 2026-07-28 client can declare its revision here.
func TestDaemon_MetaDeclaredRevisionIsHonoured(t *testing.T) {
	result := daemonCall(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`)
	if result["protocolVersion"] != "2026-07-28" {
		t.Errorf("protocolVersion = %v; a _meta-declared revision must be honoured", result["protocolVersion"])
	}
}

func TestDaemon_ServerDiscoverAdvertisesTheWholeSet(t *testing.T) {
	result := daemonCall(t, `{"jsonrpc":"2.0","id":1,"method":"server/discover"}`)
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
	if result["ttlMs"] != float64(mcpwire.ListTTLMillis) {
		t.Errorf("ttlMs = %v", result["ttlMs"])
	}
}

func TestDaemon_ToolsListIsCacheableAndAnnotated(t *testing.T) {
	result := daemonCall(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if result["resultType"] != mcpwire.ResultTypeComplete {
		t.Errorf("resultType = %v", result["resultType"])
	}
	if result["cacheScope"] != mcpwire.CacheScopePrivate {
		t.Errorf("cacheScope = %v, want %q", result["cacheScope"], mcpwire.CacheScopePrivate)
	}
	tools, _ := result["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("no tools")
	}
	for _, raw := range tools {
		tool := raw.(map[string]any)
		name, _ := tool["name"].(string)
		ann, ok := tool["annotations"].(map[string]any)
		if !ok {
			t.Fatalf("tool %q has no annotations", name)
		}
		if ann["title"] != mcpwire.ToolTitle(name) {
			t.Errorf("tool %q title = %v", name, ann["title"])
		}
	}
}

// Identity moved into every result's `_meta` in 2026-07-28 — a stateless
// client never sees an initialize response to read it from.
func TestDaemon_EveryResultCarriesServerIdentity(t *testing.T) {
	for _, method := range []string{"initialize", "server/discover", "tools/list", "ping"} {
		result := daemonCall(t, `{"jsonrpc":"2.0","id":1,"method":"`+method+`"}`)
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
		if info["name"] != serverName {
			t.Errorf("%s: serverInfo.name = %v, want %q", method, info["name"], serverName)
		}
		if result["resultType"] != mcpwire.ResultTypeComplete {
			t.Errorf("%s: resultType = %v", method, result["resultType"])
		}
	}
}
