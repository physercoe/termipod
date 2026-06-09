package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestNormalizeAgentHandle is the unit-level guard on the bare-handle
// invariant. The string contract is fixed by the glossary entry and
// load-bearing across every spawn/lookup path; if this helper drifts,
// agents downstream will start failing in the @@steward.xxx mode.
func TestNormalizeAgentHandle(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"coder", "coder"},
		{"@coder", "coder"},
		{"  @coder  ", "coder"},
		{"steward.01KRB586", "steward.01KRB586"},
		{"@steward.01KRB586", "steward.01KRB586"},
		// Defensive: only strip ONE leading @ so the function stays
		// idempotent against post-strip data. The a2a.invoke lookup
		// separately collapses pathological `@@…` from a stochastic
		// LLM before card lookup.
		{"@@coder", "@coder"},
		{"", ""},
	}
	for _, tc := range cases {
		got := normalizeAgentHandle(tc.in)
		if got != tc.want {
			t.Errorf("normalizeAgentHandle(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

// TestDoSpawn_StripsAtFromChildHandle exercises the storage-side
// boundary: a spawn passing `child_handle="@coder"` (the legacy
// template form) must land as `coder` in the agents row, with the
// matching change rippling through audit + a2a_cards.
func TestDoSpawn_StripsAtFromChildHandle(t *testing.T) {
	s, _ := newTestServer(t)
	seedTestHost(t, s, defaultTeamID, "host-x", "h1")
	out, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "@coder",
		Kind:        "claude-code",
		HostID:      "host-x",
		SpawnSpec:   "backend:\n  cmd: echo test\n",
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v (status=%d)", err, status)
	}
	var stored string
	if err := s.db.QueryRow(
		`SELECT handle FROM agents WHERE id = ?`, out.AgentID,
	).Scan(&stored); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if stored != "coder" {
		t.Errorf("stored handle = %q; want %q (bare, no @-prefix)", stored, "coder")
	}
}

// TestDoSpawn_PersistsBackendKind guards the #67/#68 fix: DoSpawn must
// write the engine family into backend_json so mobile can resolve
// agent['backend']['kind']. Two sources, in priority order: the rendered
// spec's backend.kind (template/steward spawns where in.Kind is a
// template id), then in.Kind for mobile direct-engine spawns. A stored
// '{}' is exactly the regression these issues reported.
func TestDoSpawn_PersistsBackendKind(t *testing.T) {
	s, _ := newTestServer(t)
	seedTestHost(t, s, defaultTeamID, "host-x", "h1")

	cases := []struct {
		name      string
		handle    string
		kind      string
		spawnSpec string
		want      string
	}{
		{
			name:      "spec backend.kind wins",
			handle:    "coder-a",
			kind:      "steward.general.v1",
			spawnSpec: "backend:\n  kind: claude-code\n  cmd: echo test\n",
			want:      "claude-code",
		},
		{
			name:      "falls back to in.Kind",
			handle:    "coder-b",
			kind:      "codex",
			spawnSpec: "backend:\n  cmd: echo test\n",
			want:      "codex",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
				ChildHandle: tc.handle,
				Kind:        tc.kind,
				HostID:      "host-x",
				SpawnSpec:   tc.spawnSpec,
			})
			if err != nil {
				t.Fatalf("DoSpawn: %v (status=%d)", err, status)
			}
			var stored string
			if err := s.db.QueryRow(
				`SELECT backend_json FROM agents WHERE id = ?`, out.AgentID,
			).Scan(&stored); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if stored == "{}" || stored == "" {
				t.Fatalf("backend_json = %q; want a {\"kind\":...} object (#67/#68 regression)", stored)
			}
			var got struct {
				Kind string `json:"kind"`
			}
			if err := json.Unmarshal([]byte(stored), &got); err != nil {
				t.Fatalf("backend_json %q not valid JSON: %v", stored, err)
			}
			if got.Kind != tc.want {
				t.Errorf("backend.kind = %q; want %q", got.Kind, tc.want)
			}
		})
	}
}

// TestRoleDenialMessage_NoDoubleAtPattern confirms the new escalation
// guidance: no literal `@<parent_handle>` template the LLM would
// double-prefix against an already-`@`-prefixed handle, and names the
// real tool/arg pair (a2a_invoke + handle) instead of the wrong
// pre-fix `request_help(target=...)` advice. Worth keeping as a
// guard so a future copy-edit doesn't reintroduce the failure mode.
func TestRoleDenialMessage_NoDoubleAtPattern(t *testing.T) {
	err := roleDeniedErr("worker", "agents_spawn")
	msg := err.Message
	if strings.Contains(msg, "@<") {
		t.Errorf("role-denial reintroduced literal @<...> template: %s", msg)
	}
	if strings.Contains(msg, "target=") {
		t.Errorf("role-denial mentions invalid `target` argument: %s", msg)
	}
	for _, want := range []string{"a2a_invoke", "handle=", "parent.handle", "request_help"} {
		if !strings.Contains(msg, want) {
			t.Errorf("role-denial missing expected phrase %q: %s", want, msg)
		}
	}
}

// TestWorkerCanCallTasksComplete confirms the WorkerEligible flag flip:
// before this commit, a worker calling tasks.complete (the explicit
// close-out verb the CLAUDE.md task footer instructs them to use)
// got -32601 from the role gate. The fix is a single boolean in
// toolspec.go; this test pins it so it can't silently regress.
func TestWorkerCanCallTasksComplete(t *testing.T) {
	if jerr := (&Server{}).authorizeMCPCall(nil, "", "worker", "tasks_complete"); jerr != nil {
		t.Fatalf("principal bypass (agentID='') should not deny: %s", jerr.Message)
	}
	// Authority registry lookup is the path that fires here; we
	// reach into it via lookupToolSpec to assert the flag without
	// spinning up the full DB plumbing.
	spec, ok, _ := lookupToolSpec("tasks_complete")
	if !ok {
		t.Fatal("tasks_complete missing from registry")
	}
	if !spec.WorkerEligible {
		t.Errorf("tasks_complete.WorkerEligible = false; workers can't close out their assigned tasks")
	}
	// The dotted alias was retired in WS1.1 — it must no longer resolve, so a
	// worker is steered to the canonical tasks_complete (which the close-out
	// footer now emits).
	if _, ok, _ := lookupToolSpec("tasks.complete"); ok {
		t.Error("tasks.complete still resolves — WS1.1 retired the dotted alias")
	}
}

// TestHandleSpawn_RestStripsAt covers the REST entry (not the direct
// DoSpawn): a POST /v1/teams/{team}/agents/spawn with `child_handle=
// "@worker"` lands as `worker` in the agents row. The boundary is
// the same normalizeAgentHandle call; this test pins the wiring.
func TestHandleSpawn_RestStripsAt(t *testing.T) {
	s, token := newA2ATestServer(t)
	seedTestHost(t, s, defaultTeamID, "host-y", "h2")
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/spawn",
		map[string]any{
			"child_handle":    "@worker-1",
			"kind":            "claude-code",
			"host_id":         "host-y",
			"spawn_spec_yaml": "kind: claude-code\nbackend:\n  cmd: echo test\n",
		})
	if status != http.StatusCreated {
		t.Fatalf("spawn: status=%d body=%s", status, body)
	}
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM agents WHERE handle = 'worker-1'`,
	).Scan(&n); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if n != 1 {
		t.Errorf("expected one 'worker-1' (bare) row; got count=%d", n)
	}
	// And no row stored with the leading @.
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM agents WHERE handle = '@worker-1'`,
	).Scan(&n); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if n != 0 {
		t.Errorf("did not expect any '@worker-1' rows; got count=%d", n)
	}
}
