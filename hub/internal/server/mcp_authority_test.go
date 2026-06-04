package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/termipod/hub/internal/auth"
)

// TestMCP_NotificationsGetNoResponse pins the JSON-RPC 2.0 §4.1
// contract: the server MUST NOT reply to a notification (a request
// without `id`). Pre-v1.0.656 the in-process /mcp/<token> handler
// wrote an error frame for every unknown method including
// notifications. agy 1.0.1 sends `notifications/roots/list_changed`
// to every connected MCP server (host-verified in the agytest
// stderr log); the unsolicited error frame our hub returned looked
// to agy like a protocol violation, agy closed its MCP client, and
// subsequent tools/call surfaced as `connection closed: client is
// closing: invalid request`.
func TestMCP_NotificationsGetNoResponse(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/hub.db"
	token, err := Init(dir, dbPath)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	s, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	srv := httptest.NewServer(s.router)
	t.Cleanup(srv.Close)

	// Each of these is a notification: no `id` field. Per JSON-RPC
	// the response body MUST be empty.
	cases := []string{
		`{"jsonrpc":"2.0","method":"notifications/roots/list_changed","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"abc"}}`,
		`{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":"x","progress":0.5}}`,
		// An unknown notification method must also produce no body —
		// the default-case must not fire on notifications.
		`{"jsonrpc":"2.0","method":"this/does/not/exist","params":{}}`,
	}
	for _, line := range cases {
		req, _ := http.NewRequestWithContext(context.Background(), "POST",
			srv.URL+"/mcp/"+token, bytes.NewReader([]byte(line)))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("notification POST: %v (line=%s)", err, line)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Errorf("notification %q: status=%d want 204 (body=%s)", line, resp.StatusCode, body)
		}
		if len(bytes.TrimSpace(body)) != 0 {
			t.Errorf("notification %q: returned body %q — JSON-RPC notifications must produce no response", line, body)
		}
	}
}

// TestMCPAuthority_RoundTrip verifies the consolidation: a spawned-agent
// MCP client posting tools/list to /mcp/<token> sees the rich-authority
// catalog (e.g. projects.list, agents.spawn, schedules.create) advertised
// alongside the in-process catalog. The hub used to require a second MCP
// daemon (hub-mcp-server) to expose those names — v1.0.297 wires them
// in-process via a chi-router transport, so one bridge entry in
// .mcp.json now reaches the union of both surfaces.
func TestMCPAuthority_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/hub.db"
	token, err := Init(dir, dbPath)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	s, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	srv := httptest.NewServer(s.router)
	t.Cleanup(srv.Close)

	// tools/list: a sample of authority names must appear.
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	req, _ := http.NewRequestWithContext(context.Background(), "POST",
		srv.URL+"/mcp/"+token, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var listOut struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &listOut); err != nil {
		t.Fatalf("decode tools/list: %v (%s)", err, raw)
	}
	have := make(map[string]bool, len(listOut.Result.Tools))
	for _, tt := range listOut.Result.Tools {
		have[tt.Name] = true
	}
	// v1.0.654: tools/list filters MCP-spec-noncompliant dot-named
	// aliases off the wire (`documents.list`, `projects.list`, …) so
	// strict clients like agy 1.0.1 don't reject the whole batch.
	// Check the snake_case canonical names — they're the only ones
	// that ship now. Both spellings still resolve on tools/call (see
	// the second probe below).
	for _, want := range []string{
		"projects_list", "plans_create", "agents_spawn",
		"schedules_run", "audit_read", "a2a_invoke",
	} {
		if !have[want] {
			t.Errorf("authority tool %q missing from tools/list", want)
		}
	}
	// And the dot-named legacy aliases MUST be filtered out.
	for _, mustNotShip := range []string{
		"projects.list", "plans.create", "agents.spawn",
	} {
		if have[mustNotShip] {
			t.Errorf("dot-named alias %q still on the wire — agy 1.0.1 will reject", mustNotShip)
		}
	}

	// tools/call projects_list — exercises the chi-router transport
	// hitting GET /v1/teams/{team}/projects in-process. Only the canonical
	// snake_case name resolves (WS1.1 retired the dotted spelling).
	body, _ = json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name":      "projects_list",
			"arguments": map[string]any{},
		},
	})
	req, _ = http.NewRequestWithContext(context.Background(), "POST",
		srv.URL+"/mcp/"+token, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	raw, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	var callOut struct {
		Result map[string]any `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &callOut); err != nil {
		t.Fatalf("decode tools/call: %v (%s)", err, raw)
	}
	if callOut.Error != nil {
		t.Fatalf("tools/call returned error: %+v (raw=%s)", callOut.Error, raw)
	}
	if callOut.Result == nil {
		t.Fatalf("tools/call missing result: %s", raw)
	}
}

// TestRoles_ManifestMatching exercises the manifest matcher in
// isolation — pure unit, no DB. Verifies the embedded default
// roles.yaml's coverage of the steward / worker boundary.
func TestRoles_ManifestMatching(t *testing.T) {
	if err := initRoles(""); err != nil { // empty dataRoot → embed only
		t.Fatalf("initRoles: %v", err)
	}
	r := activeRoles()
	if r == nil {
		t.Fatal("activeRoles nil after init")
	}

	// Kind → role derivation
	cases := []struct {
		kind, want string
	}{
		{"steward.general.v1", "steward"},
		{"steward.research.v1", "steward"},
		{"steward.infra.v1", "steward"},
		{"lit-reviewer.v1", "worker"},
		{"coder.v1", "worker"},
		{"ml-worker.v1", "worker"},
		{"paper-writer.v1", "worker"},
		{"unknown.kind", "worker"}, // default
		{"", "worker"},
	}
	for _, c := range cases {
		if got := r.RoleFor(c.kind); got != c.want {
			t.Errorf("RoleFor(%q) = %q; want %q", c.kind, got, c.want)
		}
	}

	// RoleForSpec — when the caller (mobile, historically) sends the
	// engine kind as `kind` for a steward template, the spawn_spec_yaml
	// still carries `default_role: team.*` and that must escalate the
	// role correctly. Otherwise stewards spawned from mobile land with
	// worker role and can't invoke `agents.spawn` themselves.
	specCases := []struct {
		name, kind, spec, want string
	}{
		{
			"steward spec with engine kind",
			"claude-code",
			"template: agents.steward.research\nversion: 1\ndefault_role: team.coordinator\n",
			"steward",
		},
		{
			"steward spec via concierge",
			"claude-code",
			"default_role: team.concierge\n",
			"steward",
		},
		{
			"worker spec keeps worker role",
			"claude-code",
			"default_role: worker.ml\n",
			"worker",
		},
		{
			"kind already matches prefix_steward — spec ignored",
			"steward.research.v1",
			"default_role: worker.foo\n",
			"steward",
		},
		{
			"empty spec falls through to kind",
			"claude-code",
			"",
			"worker",
		},
		{
			"nested default_role under another key is NOT picked up",
			"claude-code",
			"backend:\n  default_role: team.coordinator\n",
			"worker",
		},
		{
			"comment after value is stripped",
			"claude-code",
			"default_role: team.coordinator # canonical steward signal\n",
			"steward",
		},
		{
			"quoted value works",
			"claude-code",
			"default_role: \"team.coordinator\"\n",
			"steward",
		},
	}
	for _, c := range specCases {
		if got := r.RoleForSpec(c.kind, c.spec); got != c.want {
			t.Errorf("%s: RoleForSpec(%q, …) = %q; want %q",
				c.name, c.kind, got, c.want)
		}
	}

	// Steward allows everything.
	for _, tool := range []string{"agents.spawn", "documents.create", "schedules.create", "anything"} {
		if !r.Allows("steward", tool) {
			t.Errorf("steward should allow %q", tool)
		}
	}

	// Worker is denied steward-only tools.
	for _, tool := range []string{
		"agents.spawn", "agents.archive", "agents.terminate",
		"templates_agent_create", "schedules.create", "projects.update",
		"hosts.update_ssh_hint",
	} {
		if r.Allows("worker", tool) {
			t.Errorf("worker should NOT allow %q", tool)
		}
	}

	// Worker is allowed its surface.
	for _, tool := range []string{
		"documents.create", "documents.list", "documents.get",
		"reviews.create", "reviews.list",
		"runs.register", "runs.complete", "run.metrics.read",
		"channels.post_event", "post_message",
		"attention.create", "request_help", "request_select",
		"a2a.invoke", // role gate passes; target is enforced separately by authorizeA2ATarget (D4)
		"tasks.create", "tasks.update",
		"agents.list",  // *.list pattern
		"agents.get",   // *.get pattern
		"projects.get", // *.get pattern
		"permission_prompt",
		"reports.post", "agents.gather",
	} {
		if !r.Allows("worker", tool) {
			t.Errorf("worker should allow %q", tool)
		}
	}

	// Unknown role → deny.
	if r.Allows("auditor", "documents.list") {
		t.Error("unknown role should deny by default")
	}
}

// TestMCPAuthority_WorkerDeniedStewardTool verifies the middleware
// rejects a worker-role MCP token calling a steward-only tool. Mints
// a fresh agent + auth_token row directly so we don't need a real
// spawn flow; the role-stamping at spawn time has its own coverage in
// TestSpawnRole_StampedFromKind below.
func TestMCPAuthority_WorkerDeniedStewardTool(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/hub.db"
	if _, err := Init(dir, dbPath); err != nil {
		t.Fatalf("Init: %v", err)
	}
	s, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	srv := httptest.NewServer(s.router)
	t.Cleanup(srv.Close)

	// Mint a worker-role token bound to a fake agent. Role is the
	// load-bearing field; agent_id need only resolve out of the table.
	now := time.Now().UTC().Format(time.RFC3339)
	agentID := "test-worker-1"
	if _, err := s.db.Exec(`INSERT INTO agents
		(id, team_id, handle, kind, capabilities_json,
		 status, pause_state, created_at)
		VALUES (?, ?, ?, ?, '[]', 'running', 'running', ?)`,
		agentID, defaultTeamID, "@worker-test", "lit-reviewer.v1", now); err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	tok := auth.NewToken()
	scopeJSON, _ := json.Marshal(map[string]any{
		"team":     defaultTeamID,
		"role":     "worker",
		"agent_id": agentID,
		"handle":   "@worker-test",
	})
	if _, err := s.db.Exec(`INSERT INTO auth_tokens
		(id, kind, token_hash, scope_json, created_at)
		VALUES (?, 'agent', ?, ?, ?)`,
		NewID(), auth.HashToken(tok), string(scopeJSON), now); err != nil {
		t.Fatalf("insert auth_token: %v", err)
	}

	// Worker calling agents.spawn — should be denied.
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "agents_spawn",
			"arguments": map[string]any{"child_handle": "@x", "kind": "ml-worker.v1"},
		},
	})
	req, _ := http.NewRequestWithContext(context.Background(), "POST",
		srv.URL+"/mcp/"+tok, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var out struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	if out.Error == nil {
		t.Fatalf("expected error, got: %s", raw)
	}
	if !bytes.Contains(raw, []byte("not permitted for role")) {
		t.Errorf("expected role-denial message, got: %s", raw)
	}

	// Worker calling a tool in its allow set (search) — should NOT
	// be denied by the role gate. (May still error for other reasons,
	// but not the role-denial message.)
	body2, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name":      "search",
			"arguments": map[string]any{"q": "anything"},
		},
	})
	req2, _ := http.NewRequestWithContext(context.Background(), "POST",
		srv.URL+"/mcp/"+tok, bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("tools/call (search): %v", err)
	}
	raw2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if bytes.Contains(raw2, []byte("not permitted for role")) {
		t.Errorf("worker should be allowed search; got role denial: %s", raw2)
	}
}

// TestMCPAuthority_A2ATargetRestriction verifies ADR-016 D4: workers
// may invoke a2a.invoke only against their parent steward. Builds a
// minimal three-agent fixture (parent steward + child worker +
// unrelated steward) and exercises both allow and deny paths.
//
// We don't expect the underlying A2A handler to succeed (no cards are
// published in the test fixture), so we discriminate allow vs deny by
// inspecting the error message: the role/target gate emits a specific
// "a2a target not permitted" string; downstream errors don't.
func TestMCPAuthority_A2ATargetRestriction(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/hub.db"
	if _, err := Init(dir, dbPath); err != nil {
		t.Fatalf("Init: %v", err)
	}
	s, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	srv := httptest.NewServer(s.router)
	t.Cleanup(srv.Close)

	now := time.Now().UTC().Format(time.RFC3339)

	// Parent steward, child worker, unrelated steward.
	parentID := "test-parent-steward"
	workerID := "test-child-worker"
	otherID := "test-other-steward"
	for _, a := range []struct{ id, handle, kind string }{
		{parentID, "@parent", "steward.research.v1"},
		{workerID, "@worker", "lit-reviewer.v1"},
		{otherID, "@other", "steward.infra.v1"},
	} {
		if _, err := s.db.Exec(`INSERT INTO agents
			(id, team_id, handle, kind, capabilities_json,
			 status, pause_state, created_at)
			VALUES (?, ?, ?, ?, '[]', 'running', 'running', ?)`,
			a.id, defaultTeamID, a.handle, a.kind, now); err != nil {
			t.Fatalf("insert agent %s: %v", a.id, err)
		}
	}

	// Spawn record linking worker to parent.
	if _, err := s.db.Exec(`INSERT INTO agent_spawns
		(id, parent_agent_id, child_agent_id, spawn_spec_yaml,
		 spawn_authority_json, task_json, spawned_at)
		VALUES (?, ?, ?, '', '{}', '{}', ?)`,
		NewID(), parentID, workerID, now); err != nil {
		t.Fatalf("insert spawn: %v", err)
	}

	// Worker's MCP token (role=worker).
	tok := auth.NewToken()
	scopeJSON, _ := json.Marshal(map[string]any{
		"team":     defaultTeamID,
		"role":     "worker",
		"agent_id": workerID,
	})
	if _, err := s.db.Exec(`INSERT INTO auth_tokens
		(id, kind, token_hash, scope_json, created_at)
		VALUES (?, 'agent', ?, ?, ?)`,
		NewID(), auth.HashToken(tok), string(scopeJSON), now); err != nil {
		t.Fatalf("insert token: %v", err)
	}

	call := func(handle string) []byte {
		body, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{
				"name":      "a2a_invoke",
				"arguments": map[string]any{"handle": handle, "text": "hi"},
			},
		})
		req, _ := http.NewRequestWithContext(context.Background(), "POST",
			srv.URL+"/mcp/"+tok, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("http: %v", err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return raw
	}

	// Worker → unrelated steward: gate denies.
	if got := call("@other"); !bytes.Contains(got, []byte("a2a target not permitted")) {
		t.Errorf("worker → @other should be denied; got: %s", got)
	}

	// Worker → parent: gate allows. Tool implementation will likely
	// fail because no A2A card is published in this fixture, but the
	// failure must not be the gate's deny message.
	if got := call("@parent"); bytes.Contains(got, []byte("a2a target not permitted")) {
		t.Errorf("worker → parent should pass gate; got gate denial: %s", got)
	}

	// Worker → self: also denied (self is not parent).
	if got := call("@worker"); !bytes.Contains(got, []byte("a2a target not permitted")) {
		t.Errorf("worker → @worker should be denied; got: %s", got)
	}

	// Steward token bypasses target restriction entirely. Mint one
	// for the parent and verify any handle gets through the gate.
	stewardTok := auth.NewToken()
	stewardScope, _ := json.Marshal(map[string]any{
		"team":     defaultTeamID,
		"role":     "steward",
		"agent_id": parentID,
	})
	if _, err := s.db.Exec(`INSERT INTO auth_tokens
		(id, kind, token_hash, scope_json, created_at)
		VALUES (?, 'agent', ?, ?, ?)`,
		NewID(), auth.HashToken(stewardTok), string(stewardScope), now); err != nil {
		t.Fatalf("insert steward token: %v", err)
	}
	bodyS, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "a2a_invoke",
			"arguments": map[string]any{"handle": "@other", "text": "hi"},
		},
	})
	reqS, _ := http.NewRequestWithContext(context.Background(), "POST",
		srv.URL+"/mcp/"+stewardTok, bytes.NewReader(bodyS))
	reqS.Header.Set("Content-Type", "application/json")
	respS, err := http.DefaultClient.Do(reqS)
	if err != nil {
		t.Fatalf("steward http: %v", err)
	}
	rawS, _ := io.ReadAll(respS.Body)
	respS.Body.Close()
	if bytes.Contains(rawS, []byte("a2a target not permitted")) {
		t.Errorf("steward → any should bypass target restriction; got: %s", rawS)
	}
}

// TestMCPAuthority_LegacyAgentRoleFallback verifies that a token
// minted with the legacy role="agent" still gates correctly via the
// agents-table fallback in resolveAgentRole.
func TestMCPAuthority_LegacyAgentRoleFallback(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/hub.db"
	if _, err := Init(dir, dbPath); err != nil {
		t.Fatalf("Init: %v", err)
	}
	s, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	srv := httptest.NewServer(s.router)
	t.Cleanup(srv.Close)

	// Steward kind, but legacy role="agent" stamping. Fallback should
	// re-derive role=steward from agent_kind.
	now := time.Now().UTC().Format(time.RFC3339)
	agentID := "test-steward-legacy"
	if _, err := s.db.Exec(`INSERT INTO agents
		(id, team_id, handle, kind, capabilities_json,
		 status, pause_state, created_at)
		VALUES (?, ?, ?, ?, '[]', 'running', 'running', ?)`,
		agentID, defaultTeamID, "@steward-test", "steward.research.v1", now); err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	tok := auth.NewToken()
	scopeJSON, _ := json.Marshal(map[string]any{
		"team":     defaultTeamID,
		"role":     "agent", // legacy stamp
		"agent_id": agentID,
	})
	if _, err := s.db.Exec(`INSERT INTO auth_tokens
		(id, kind, token_hash, scope_json, created_at)
		VALUES (?, 'agent', ?, ?, ?)`,
		NewID(), auth.HashToken(tok), string(scopeJSON), now); err != nil {
		t.Fatalf("insert auth_token: %v", err)
	}

	// Steward kind calling agents.spawn — should pass the role gate
	// (even though stamp is "agent", fallback derives steward from kind).
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "agents_spawn",
			"arguments": map[string]any{"child_handle": "@w", "kind": "ml-worker.v1"},
		},
	})
	req, _ := http.NewRequestWithContext(context.Background(), "POST",
		srv.URL+"/mcp/"+tok, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if bytes.Contains(raw, []byte("not permitted for role")) {
		t.Errorf("legacy steward token should pass role gate via kind fallback; got: %s", raw)
	}
}

// TestTemplateAuthoring_W2 covers the W2 deliverables: steward
// creates / lists / gets / updates / deletes a template via MCP;
// worker is denied write attempts via the role gate; steward attempts
// to edit its own kind's template and is denied by the self-
// modification guard (ADR-016 D7).
func TestTemplateAuthoring_W2(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/hub.db"
	if _, err := Init(dir, dbPath); err != nil {
		t.Fatalf("Init: %v", err)
	}
	s, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	srv := httptest.NewServer(s.router)
	t.Cleanup(srv.Close)

	now := time.Now().UTC().Format(time.RFC3339)

	// Mint a steward token (role=steward) for steward.research.v1.
	stewardID := "test-steward"
	if _, err := s.db.Exec(`INSERT INTO agents
		(id, team_id, handle, kind, capabilities_json,
		 status, pause_state, created_at)
		VALUES (?, ?, ?, ?, '[]', 'running', 'running', ?)`,
		stewardID, defaultTeamID, "@steward", "steward.research.v1", now); err != nil {
		t.Fatalf("insert steward: %v", err)
	}
	stewardTok := auth.NewToken()
	stewardScope, _ := json.Marshal(map[string]any{
		"team": defaultTeamID, "role": "steward", "agent_id": stewardID,
	})
	if _, err := s.db.Exec(`INSERT INTO auth_tokens
		(id, kind, token_hash, scope_json, created_at)
		VALUES (?, 'agent', ?, ?, ?)`,
		NewID(), auth.HashToken(stewardTok), string(stewardScope), now); err != nil {
		t.Fatalf("insert steward token: %v", err)
	}

	// Mint a worker token.
	workerID := "test-worker"
	if _, err := s.db.Exec(`INSERT INTO agents
		(id, team_id, handle, kind, capabilities_json,
		 status, pause_state, created_at)
		VALUES (?, ?, ?, ?, '[]', 'running', 'running', ?)`,
		workerID, defaultTeamID, "@w", "lit-reviewer.v1", now); err != nil {
		t.Fatalf("insert worker: %v", err)
	}
	workerTok := auth.NewToken()
	workerScope, _ := json.Marshal(map[string]any{
		"team": defaultTeamID, "role": "worker", "agent_id": workerID,
	})
	if _, err := s.db.Exec(`INSERT INTO auth_tokens
		(id, kind, token_hash, scope_json, created_at)
		VALUES (?, 'agent', ?, ?, ?)`,
		NewID(), auth.HashToken(workerTok), string(workerScope), now); err != nil {
		t.Fatalf("insert worker token: %v", err)
	}

	mcpCall := func(tok, name string, args map[string]any) []byte {
		body, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": name, "arguments": args},
		})
		req, _ := http.NewRequestWithContext(context.Background(), "POST",
			srv.URL+"/mcp/"+tok, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("http: %v", err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return raw
	}

	// 1. Steward creates a worker template.
	yaml := "family: test-worker\nbin: echo\nsupports: [M4]\n"
	got := mcpCall(stewardTok, "templates_agent_create", map[string]any{
		"name": "test-worker.v1.yaml", "content": yaml,
	})
	if bytes.Contains(got, []byte("\"error\"")) {
		t.Fatalf("steward create failed: %s", got)
	}

	// 2. Steward lists agent templates — should include the new one.
	got = mcpCall(stewardTok, "templates_agent_list", map[string]any{})
	if !bytes.Contains(got, []byte("test-worker.v1.yaml")) {
		t.Errorf("list missing new template: %s", got)
	}

	// 3. Steward gets back the content.
	got = mcpCall(stewardTok, "templates_agent_get", map[string]any{
		"name": "test-worker.v1.yaml",
	})
	if !bytes.Contains(got, []byte("test-worker")) {
		t.Errorf("get missing content: %s", got)
	}

	// 4. Steward updates content.
	yaml2 := yaml + "frame_translator: legacy\n"
	got = mcpCall(stewardTok, "templates_agent_update", map[string]any{
		"name": "test-worker.v1.yaml", "content": yaml2,
	})
	if bytes.Contains(got, []byte("\"error\"")) {
		t.Fatalf("steward update failed: %s", got)
	}

	// 5. Worker calling create — denied by role gate.
	got = mcpCall(workerTok, "templates_agent_create", map[string]any{
		"name": "evil.v1.yaml", "content": "x",
	})
	if !bytes.Contains(got, []byte("not permitted for role")) {
		t.Errorf("worker create should be denied; got: %s", got)
	}
	got = mcpCall(workerTok, "templates_agent_delete", map[string]any{
		"name": "test-worker.v1.yaml",
	})
	if !bytes.Contains(got, []byte("not permitted for role")) {
		t.Errorf("worker delete should be denied; got: %s", got)
	}

	// 6. Worker reads — allowed (via *.list / *.get).
	got = mcpCall(workerTok, "templates_agent_list", map[string]any{})
	if bytes.Contains(got, []byte("not permitted for role")) {
		t.Errorf("worker list should be allowed; got role denial: %s", got)
	}
	got = mcpCall(workerTok, "templates_agent_get", map[string]any{
		"name": "test-worker.v1.yaml",
	})
	if bytes.Contains(got, []byte("not permitted for role")) {
		t.Errorf("worker get should be allowed; got role denial: %s", got)
	}

	// 7. Self-modification guard: steward.research.v1 trying to edit
	//    its own kind's template — denied.
	got = mcpCall(stewardTok, "templates_agent_update", map[string]any{
		"name":    "steward.research.v1.yaml",
		"content": "subverted: true\n",
	})
	if !bytes.Contains(got, []byte("self-modification guard")) {
		t.Errorf("self-mod guard should fire on agents/steward.research.v1.yaml; got: %s", got)
	}
	got = mcpCall(stewardTok, "templates_prompt_update", map[string]any{
		"name":    "steward.research.v1.md",
		"content": "subverted",
	})
	if !bytes.Contains(got, []byte("self-modification guard")) {
		t.Errorf("self-mod guard should fire on prompts/steward.research.v1.md; got: %s", got)
	}

	// 8. Steward deletes the template.
	got = mcpCall(stewardTok, "templates_agent_delete", map[string]any{
		"name": "test-worker.v1.yaml",
	})
	if bytes.Contains(got, []byte("\"error\"")) {
		t.Errorf("steward delete failed: %s", got)
	}
}
