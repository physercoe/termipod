package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/termipod/hub/internal/mcpver"
	"github.com/termipod/hub/internal/mcpwire"
)

// Lane U acceptance, driven through the real /mcp/{token} route rather
// than the handlers: every claim here is about what a client sees on the
// wire, and the transport (headers, batch framing, status codes) is half
// of what changed.

// doMCP posts one JSON-RPC frame to /mcp/{token} and returns the
// recorder, so a caller can assert on response headers as well as body.
func doMCP(t *testing.T, s *Server, token string, frame any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(frame); err != nil {
		t.Fatalf("encode frame: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp/"+token, &buf)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	s.router.ServeHTTP(rr, req)
	return rr
}

// mcpResultOf decodes a JSON-RPC response body's `result` object.
func mcpResultOf(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var resp struct {
		Result map[string]any `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, rr.Body.String())
	}
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	return resp.Result
}

func mcpErrorCodeOf(t *testing.T, rr *httptest.ResponseRecorder) int {
	t.Helper()
	var resp struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, rr.Body.String())
	}
	if resp.Error == nil {
		t.Fatalf("expected a JSON-RPC error, got %s", rr.Body.String())
	}
	return resp.Error.Code
}

// assertServerIdentity checks the 2026-07-28 `_meta` identity every
// result carries (SEP-2575 moved it out of the initialize response).
func assertServerIdentity(t *testing.T, result map[string]any) {
	t.Helper()
	meta, ok := result["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("result carries no _meta: %v", result)
	}
	info, ok := meta[mcpwire.MetaServerInfo].(map[string]any)
	if !ok {
		t.Fatalf("_meta carries no %s: %v", mcpwire.MetaServerInfo, meta)
	}
	if info["name"] != mcpServerName {
		t.Errorf("serverInfo.name = %v, want %q", info["name"], mcpServerName)
	}
}

func TestMCP_InitializeEchoesAKnownRevisionAndStampsTheHeader(t *testing.T) {
	s, token := newA2ATestServer(t)
	// 2026-07-28 is the revision this lane exists to answer; echoing it
	// is the whole acceptance criterion.
	rr := doMCP(t, s, token, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2026-07-28"},
	}, nil)
	result := mcpResultOf(t, rr)
	if result["protocolVersion"] != "2026-07-28" {
		t.Errorf("protocolVersion = %v, want the client's own ask echoed", result["protocolVersion"])
	}
	if got := rr.Header().Get(mcpwire.HeaderProtocolVersion); got != "2026-07-28" {
		t.Errorf("%s header = %q, want the negotiated revision", mcpwire.HeaderProtocolVersion, got)
	}
	if result["resultType"] != mcpwire.ResultTypeComplete {
		t.Errorf("resultType = %v, want %q", result["resultType"], mcpwire.ResultTypeComplete)
	}
	assertServerIdentity(t, result)
}

// The v1.0.649 regression, at the transport level: agy 1.0.1 tears the
// connection down if its 2025-11-25 is answered with anything else.
func TestMCP_InitializeEchoesEveryRevisionInTheSet(t *testing.T) {
	s, token := newA2ATestServer(t)
	for _, v := range mcpver.Supported() {
		rr := doMCP(t, s, token, map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "initialize",
			"params": map[string]any{"protocolVersion": v},
		}, nil)
		if got := mcpResultOf(t, rr)["protocolVersion"]; got != v {
			t.Errorf("initialize(%q) answered %v; a known revision must round-trip", v, got)
		}
	}
}

func TestMCP_InitializeAnswersTheFloorForAnUnknownRevision(t *testing.T) {
	s, token := newA2ATestServer(t)
	rr := doMCP(t, s, token, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2027-01-01"},
	}, nil)
	result := mcpResultOf(t, rr)
	if result["protocolVersion"] != mcpver.Floor {
		t.Errorf("protocolVersion = %v, want the floor %q — never a blind echo",
			result["protocolVersion"], mcpver.Floor)
	}
	if got := rr.Header().Get(mcpwire.HeaderProtocolVersion); got != mcpver.Floor {
		t.Errorf("header = %q, want it to agree with the body (%q)", got, mcpver.Floor)
	}
}

func TestMCP_ServerDiscoverAdvertisesTheWholeSet(t *testing.T) {
	s, token := newA2ATestServer(t)
	rr := doMCP(t, s, token, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "server/discover",
	}, nil)
	result := mcpResultOf(t, rr)
	raw, ok := result["protocolVersions"].([]any)
	if !ok {
		t.Fatalf("server/discover carries no protocolVersions: %v", result)
	}
	got := make([]string, 0, len(raw))
	for _, v := range raw {
		got = append(got, v.(string))
	}
	want := mcpver.Supported()
	if len(got) != len(want) {
		t.Fatalf("advertised %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("revision %d: advertised %q, want %q", i, got[i], want[i])
		}
	}
	// A client's response-cache layer reads discover like any list.
	if result["ttlMs"] != float64(mcpwire.ListTTLMillis) {
		t.Errorf("ttlMs = %v, want %d", result["ttlMs"], mcpwire.ListTTLMillis)
	}
	if result["cacheScope"] != mcpwire.CacheScopePrivate {
		t.Errorf("cacheScope = %v, want %q", result["cacheScope"], mcpwire.CacheScopePrivate)
	}
	assertServerIdentity(t, result)
}

func TestMCP_ToolsListIsCacheableAndAnnotated(t *testing.T) {
	s, token := newA2ATestServer(t)
	rr := doMCP(t, s, token, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	}, nil)
	result := mcpResultOf(t, rr)
	if result["ttlMs"] != float64(mcpwire.ListTTLMillis) {
		t.Errorf("ttlMs = %v, want %d", result["ttlMs"], mcpwire.ListTTLMillis)
	}
	// Never "public": this catalog is filtered by the caller's role, so a
	// shared cache serving one agent's list to another leaks surface.
	if result["cacheScope"] != mcpwire.CacheScopePrivate {
		t.Errorf("cacheScope = %v, want %q", result["cacheScope"], mcpwire.CacheScopePrivate)
	}
	tools, ok := result["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("tools/list returned no tools: %v", result)
	}
	// The slim wire projection must carry annotations through — tools/list
	// is the only place most clients ever look, so dropping them there
	// would make the dual-publish invisible in practice.
	sawReadOnly, sawMutating := false, false
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
		ro, ok := ann["readOnlyHint"].(bool)
		if !ok {
			t.Fatalf("tool %q readOnlyHint is not a bool: %v", name, ann["readOnlyHint"])
		}
		if ro {
			sawReadOnly = true
		} else {
			sawMutating = true
		}
		// destructiveHint is deliberately never published — asserting it
		// false is the one direction that can invite a client to
		// auto-approve a mutating call.
		if _, present := ann["destructiveHint"]; present {
			t.Errorf("tool %q publishes destructiveHint; nothing in the registry justifies the claim", name)
		}
	}
	if !sawReadOnly || !sawMutating {
		t.Errorf("readOnlyHint never varied (readOnly=%v mutating=%v) — the annotation is not reading ToolSpec.ReadOnly",
			sawReadOnly, sawMutating)
	}
}

// The catalog's own truth and the spec's annotation must agree — a tool
// that says side_effecting AND readOnlyHint:true would tell our clients
// one thing and a spec-aware client the opposite.
func TestMCP_AnnotationsAgreeWithTheRegistryFlags(t *testing.T) {
	for _, def := range mcpToolDefs() {
		name, _ := def["name"].(string)
		ann, ok := def["annotations"].(map[string]any)
		if !ok {
			t.Fatalf("catalog entry %q has no annotations", name)
		}
		if ann["readOnlyHint"] != def["concurrency_safe"] {
			t.Errorf("%s: readOnlyHint %v != concurrency_safe %v", name, ann["readOnlyHint"], def["concurrency_safe"])
		}
		if ann["readOnlyHint"] == def["side_effecting"] {
			t.Errorf("%s: readOnlyHint %v must be the inverse of side_effecting %v", name, ann["readOnlyHint"], def["side_effecting"])
		}
	}
}

func TestMCP_BatchIsInvalidRequestNotParseError(t *testing.T) {
	s, token := newA2ATestServer(t)
	// A JSON-RPC batch: well-formed JSON, not a request object.
	rr := doMCP(t, s, token, []any{
		map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"},
	}, nil)
	if code := mcpErrorCodeOf(t, rr); code != -32600 {
		t.Errorf("batch answered %d, want -32600 invalid request (a parse error sends the client debugging its transport)", code)
	}
}

func TestMCP_UnparseableBodyIsStillAParseError(t *testing.T) {
	s, token := newA2ATestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/mcp/"+token, bytes.NewReader([]byte("{not json")))
	rr := httptest.NewRecorder()
	s.router.ServeHTTP(rr, req)
	if code := mcpErrorCodeOf(t, rr); code != -32700 {
		t.Errorf("malformed body answered %d, want -32700 parse error", code)
	}
}

// 2026-07-28 is stateless: a request carries its own version, and there
// may never have been an initialize at all.
func TestMCP_MetaOnlyRequestNegotiatesWithoutInitialize(t *testing.T) {
	s, token := newA2ATestServer(t)
	rr := doMCP(t, s, token, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
		"params": map[string]any{
			"_meta": map[string]any{
				mcpwire.MetaProtocolVersion: "2026-07-28",
				mcpwire.MetaClientInfo:      map[string]any{"name": "synthetic", "version": "1.0"},
			},
		},
	}, nil)
	if got := rr.Header().Get(mcpwire.HeaderProtocolVersion); got != "2026-07-28" {
		t.Errorf("header = %q; a _meta-declared revision must be honoured with no handshake", got)
	}
	if _, ok := mcpResultOf(t, rr)["tools"]; !ok {
		t.Error("the request was not served")
	}
}

func TestMCP_ProtocolVersionHeaderIsHonouredAndValidated(t *testing.T) {
	s, token := newA2ATestServer(t)

	// Known revision on the header, nothing in the body: honoured.
	rr := doMCP(t, s, token, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"},
		map[string]string{mcpwire.HeaderProtocolVersion: "2025-11-25"})
	if got := rr.Header().Get(mcpwire.HeaderProtocolVersion); got != "2025-11-25" {
		t.Errorf("header echo = %q, want 2025-11-25", got)
	}

	// Unknown revision on the header: validated against the set, so the
	// answer is the floor — never the client's unlisted string.
	rr = doMCP(t, s, token, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"},
		map[string]string{mcpwire.HeaderProtocolVersion: "2027-01-01"})
	if got := rr.Header().Get(mcpwire.HeaderProtocolVersion); got != mcpver.Floor {
		t.Errorf("header echo = %q, want the floor %q", got, mcpver.Floor)
	}
}

// The honesty rule: this transport is stateless, so a request that
// declares no revision leaves us genuinely not knowing. We stamp nothing
// rather than guess — a wrong version claim on the response is worse
// than none.
func TestMCP_UndeclaredRequestStampsNoVersionHeader(t *testing.T) {
	s, token := newA2ATestServer(t)
	rr := doMCP(t, s, token, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, nil)
	if got := rr.Header().Get(mcpwire.HeaderProtocolVersion); got != "" {
		t.Errorf("header = %q; a client that declared nothing must not be told a version", got)
	}
}

// An initialize with no protocolVersion at all still negotiates — the
// floor — because the handshake's whole job is to answer that question.
func TestMCP_InitializeWithNoAskStillAnswersTheFloor(t *testing.T) {
	s, token := newA2ATestServer(t)
	rr := doMCP(t, s, token, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"}, nil)
	if got := mcpResultOf(t, rr)["protocolVersion"]; got != mcpver.Floor {
		t.Errorf("protocolVersion = %v, want the floor %q", got, mcpver.Floor)
	}
	if got := rr.Header().Get(mcpwire.HeaderProtocolVersion); got != mcpver.Floor {
		t.Errorf("header = %q, want the floor %q", got, mcpver.Floor)
	}
}

// U9: both representations, never one. The text block is what an LLM
// actually reads when a client renders the result into its context.
func TestMCPResultJSON_CarriesTextAndStructuredContent(t *testing.T) {
	r := mcpResultJSON(map[string]any{"id": "abc", "n": 2})
	if r["resultType"] != mcpwire.ResultTypeComplete {
		t.Errorf("resultType = %v, want %q", r["resultType"], mcpwire.ResultTypeComplete)
	}
	sc, ok := r["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("no structuredContent: %v", r)
	}
	if sc["id"] != "abc" {
		t.Errorf("structuredContent lost the payload: %v", sc)
	}
	content, ok := r["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("the text fallback was dropped: %v", r)
	}
	block := content[0].(map[string]any)
	if block["type"] != "text" || block["text"] == "" {
		t.Errorf("text block is not renderable: %v", block)
	}
}

func TestMCPResultError_IsCompleteNotInputRequired(t *testing.T) {
	// An errored tool call HAS finished. "input_required" would tell a
	// 2026-07-28 client to retry with more input, forever.
	r := mcpResultError("nope")
	if r["isError"] != true {
		t.Errorf("isError = %v, want true", r["isError"])
	}
	if r["resultType"] != mcpwire.ResultTypeComplete {
		t.Errorf("resultType = %v, want %q", r["resultType"], mcpwire.ResultTypeComplete)
	}
}
