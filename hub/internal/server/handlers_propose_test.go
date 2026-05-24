package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// ADR-030 W4 — exercises mcpPropose end-to-end through the registry +
// policy + storage stack. Each test resets the propose-kind registry
// in a Cleanup hook so sibling tests can't pollute it. Inserts are
// asserted by re-reading the row's new ADR-030 columns; the existing
// shape of the table is left to the migration test (W1).

func registerKind(t *testing.T, k ProposeKind) {
	t.Helper()
	t.Cleanup(resetProposeKindsForTest)
	resetProposeKindsForTest()
	RegisterProposeKind(k)
}

// seedAgentWithKind extends the existing seedAgent helper with an
// explicit kind so the cross-project scope check can be exercised on
// both worker and steward callers.
func seedAgentWithKind(t *testing.T, s *Server, team, handle, kind, projectID string) string {
	t.Helper()
	id := NewID()
	if projectID == "" {
		if _, err := s.db.Exec(`
			INSERT INTO agents (id, team_id, handle, kind, status, created_at)
			VALUES (?, ?, ?, ?, 'running', ?)`,
			id, team, handle, kind, NowUTC()); err != nil {
			t.Fatalf("seed agent %q: %v", handle, err)
		}
		return id
	}
	if _, err := s.db.Exec(`
		INSERT INTO agents (id, team_id, handle, kind, status,
		                   project_id, created_at)
		VALUES (?, ?, ?, ?, 'running', ?, ?)`,
		id, team, handle, kind, projectID, NowUTC()); err != nil {
		t.Fatalf("seed agent %q: %v", handle, err)
	}
	return id
}

// readProposeRow fetches the ADR-030 columns of a row by id. Returned
// fields are the canonical surface mcpPropose populates.
type proposeRowDump struct {
	Kind         string
	ChangeKind   string
	AssignedTier string
	ChangeSpec   string
	TargetRef    string
	Assignees    string
	ProjectID    string
	SessionID    string
	Summary      string
}

func readProposeRow(t *testing.T, s *Server, id string) proposeRowDump {
	t.Helper()
	var d proposeRowDump
	if err := s.db.QueryRow(`
		SELECT kind,
		       COALESCE(change_kind, ''),
		       COALESCE(assigned_tier, ''),
		       COALESCE(change_spec_json, ''),
		       COALESCE(target_ref_json, ''),
		       current_assignees_json,
		       COALESCE(project_id, ''),
		       COALESCE(session_id, ''),
		       summary
		  FROM attention_items WHERE id = ?`, id,
	).Scan(&d.Kind, &d.ChangeKind, &d.AssignedTier, &d.ChangeSpec,
		&d.TargetRef, &d.Assignees, &d.ProjectID, &d.SessionID, &d.Summary); err != nil {
		t.Fatalf("read propose row %s: %v", id, err)
	}
	return d
}

// 1. Happy path. Worker proposes against own project; row lands with
// every ADR-030 column populated; assignees default to ["@principal"]
// (the kind has no DefaultTier so the permissive fall-through wins).
func TestMcpPropose_HappyPath_RowShape(t *testing.T) {
	s, _ := newTestServer(t)
	registerKind(t, ProposeKind{Kind: "task.set_status"})
	proj := seedProject(t, s, defaultTeamID)
	agentID := seedAgentWithKind(t, s, defaultTeamID, "worker-1", "claude-code", proj)

	args, _ := json.Marshal(map[string]any{
		"kind":        "task.set_status",
		"target_ref":  map[string]any{"project_id": proj, "task_id": "t-1"},
		"change_spec": map[string]any{"status": "done", "result_summary": "shipped"},
		"reason":      "ready for review",
	})
	out, jerr := s.mcpPropose(context.Background(), defaultTeamID, agentID, args)
	if jerr != nil {
		t.Fatalf("mcpPropose: %v", jerr)
	}
	res := out.(map[string]any)["content"] // mcpResultJSON wraps under "content"
	// mcpResultJSON returns {content: [{type:text, text:<json>}]}; extract.
	contents := res.([]any)
	first := contents[0].(map[string]any)
	var payload map[string]any
	if err := json.Unmarshal([]byte(first["text"].(string)), &payload); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	requestID, _ := payload["request_id"].(string)
	if requestID == "" {
		t.Fatal("expected request_id in result")
	}
	if payload["status"] != "awaiting_response" {
		t.Errorf("status = %v; want awaiting_response", payload["status"])
	}
	if payload["change_kind"] != "task.set_status" {
		t.Errorf("change_kind = %v; want task.set_status", payload["change_kind"])
	}

	row := readProposeRow(t, s, requestID)
	if row.Kind != "propose" {
		t.Errorf("row.kind = %q; want propose", row.Kind)
	}
	if row.ChangeKind != "task.set_status" {
		t.Errorf("change_kind = %q; want task.set_status", row.ChangeKind)
	}
	if row.AssignedTier != GovTierPrincipal {
		t.Errorf("assigned_tier = %q; want principal (no policy → permissive)", row.AssignedTier)
	}
	if !strings.Contains(row.ChangeSpec, `"status":"done"`) {
		t.Errorf("change_spec_json = %q; want done", row.ChangeSpec)
	}
	if !strings.Contains(row.TargetRef, `"task_id":"t-1"`) {
		t.Errorf("target_ref_json = %q; want task_id", row.TargetRef)
	}
	if row.ProjectID != proj {
		t.Errorf("project_id = %q; want %q", row.ProjectID, proj)
	}
	if !strings.Contains(row.Assignees, "@principal") {
		t.Errorf("current_assignees_json = %q; want @principal", row.Assignees)
	}
	if !strings.Contains(row.Summary, "ready for review") {
		t.Errorf("summary missing reason: %q", row.Summary)
	}
}

// 2. Unknown kind → -32602 with the kind name + the registered set
// echoed so the agent can re-propose.
func TestMcpPropose_UnknownKind_Rejects(t *testing.T) {
	s, _ := newTestServer(t)
	t.Cleanup(resetProposeKindsForTest)
	resetProposeKindsForTest()
	RegisterProposeKind(ProposeKind{Kind: "task.set_status"})

	proj := seedProject(t, s, defaultTeamID)
	agentID := seedAgentWithKind(t, s, defaultTeamID, "w", "claude-code", proj)
	args, _ := json.Marshal(map[string]any{
		"kind":        "phase.advance", // not registered
		"target_ref":  map[string]any{"project_id": proj},
		"change_spec": map[string]any{},
	})
	_, jerr := s.mcpPropose(context.Background(), defaultTeamID, agentID, args)
	if jerr == nil {
		t.Fatal("expected error on unknown kind")
	}
	if !strings.Contains(jerr.Message, "phase.advance") {
		t.Errorf("error should name the unknown kind; got %q", jerr.Message)
	}
	if !strings.Contains(jerr.Message, "task.set_status") {
		t.Errorf("error should echo the registered set; got %q", jerr.Message)
	}
}

// 3. dry_run=true. Returns the kind's preview without inserting a row;
//
//	the awaiting_response status is replaced by "dry_run".
func TestMcpPropose_DryRun_ReturnsPreviewNoInsert(t *testing.T) {
	s, _ := newTestServer(t)
	registerKind(t, ProposeKind{
		Kind: "task.set_status",
		DryRun: func(ctx context.Context, _ *Server, target, spec json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"from":"todo","to":"done"}`), nil
		},
	})
	proj := seedProject(t, s, defaultTeamID)
	agentID := seedAgentWithKind(t, s, defaultTeamID, "w", "claude-code", proj)

	before := countAttentionRows(t, s)
	args, _ := json.Marshal(map[string]any{
		"kind":        "task.set_status",
		"target_ref":  map[string]any{"project_id": proj, "task_id": "t-1"},
		"change_spec": map[string]any{"status": "done"},
		"dry_run":     true,
	})
	out, jerr := s.mcpPropose(context.Background(), defaultTeamID, agentID, args)
	if jerr != nil {
		t.Fatalf("mcpPropose: %v", jerr)
	}
	after := countAttentionRows(t, s)
	if after != before {
		t.Errorf("dry_run inserted a row (rows %d → %d)", before, after)
	}

	payload := unwrapMcpResult(t, out)
	if payload["status"] != "dry_run" {
		t.Errorf("status = %v; want dry_run", payload["status"])
	}
	preview, ok := payload["preview"].(map[string]any)
	if !ok {
		t.Fatalf("missing preview; got %v", payload)
	}
	if preview["from"] != "todo" || preview["to"] != "done" {
		t.Errorf("preview mismatch: %v", preview)
	}
}

// 4. Worker targets ANOTHER project → 403 out_of_scope.
func TestMcpPropose_WorkerCrossProject_OutOfScope(t *testing.T) {
	s, _ := newTestServer(t)
	registerKind(t, ProposeKind{Kind: "task.set_status"})

	projA := seedProject(t, s, defaultTeamID)
	projB := seedProject(t, s, defaultTeamID)
	agentID := seedAgentWithKind(t, s, defaultTeamID, "w", "claude-code", projA)

	args, _ := json.Marshal(map[string]any{
		"kind":        "task.set_status",
		"target_ref":  map[string]any{"project_id": projB, "task_id": "t-1"},
		"change_spec": map[string]any{"status": "done"},
	})
	_, jerr := s.mcpPropose(context.Background(), defaultTeamID, agentID, args)
	if jerr == nil {
		t.Fatal("expected out_of_scope rejection")
	}
	if !strings.Contains(jerr.Message, "out_of_scope") {
		t.Errorf("error should mention out_of_scope; got %q", jerr.Message)
	}
}

// 5. Steward targets ANOTHER project → allowed.
func TestMcpPropose_StewardCrossProject_Allowed(t *testing.T) {
	s, _ := newTestServer(t)
	registerKind(t, ProposeKind{Kind: "task.set_status"})

	projA := seedProject(t, s, defaultTeamID)
	projB := seedProject(t, s, defaultTeamID)
	stewardID := seedAgentWithKind(t, s, defaultTeamID, "steward-x", "steward.v1", projA)

	args, _ := json.Marshal(map[string]any{
		"kind":        "task.set_status",
		"target_ref":  map[string]any{"project_id": projB, "task_id": "t-1"},
		"change_spec": map[string]any{"status": "done"},
	})
	out, jerr := s.mcpPropose(context.Background(), defaultTeamID, stewardID, args)
	if jerr != nil {
		t.Fatalf("steward cross-project should be allowed; got %v", jerr)
	}
	payload := unwrapMcpResult(t, out)
	if payload["status"] != "awaiting_response" {
		t.Errorf("status = %v; want awaiting_response", payload["status"])
	}
}

// 6. target_ref with no project_id → scope check skipped (e.g.
// future template.install kind targets above-project scope).
func TestMcpPropose_NoProjectIDInTargetRef_SkipsScopeCheck(t *testing.T) {
	s, _ := newTestServer(t)
	registerKind(t, ProposeKind{Kind: "template.install"})

	projA := seedProject(t, s, defaultTeamID)
	agentID := seedAgentWithKind(t, s, defaultTeamID, "w", "claude-code", projA)

	args, _ := json.Marshal(map[string]any{
		"kind":        "template.install",
		"target_ref":  map[string]any{}, // no project_id
		"change_spec": map[string]any{"category": "prompt", "name": "foo"},
	})
	_, jerr := s.mcpPropose(context.Background(), defaultTeamID, agentID, args)
	if jerr != nil {
		t.Fatalf("above-project scope should allow worker; got %v", jerr)
	}
}

// 7. Caller's addressee_tier hint overrides the policy default. We
// don't write a policy file here; KindFor returns the permissive
// default (principal). Caller asks for project-steward; mcpPropose
// must honour that.
func TestMcpPropose_AddresseeTierHint_Overrides(t *testing.T) {
	s, _ := newTestServer(t)
	registerKind(t, ProposeKind{Kind: "task.set_status"})

	proj := seedProject(t, s, defaultTeamID)
	agentID := seedAgentWithKind(t, s, defaultTeamID, "w", "claude-code", proj)
	args, _ := json.Marshal(map[string]any{
		"kind":           "task.set_status",
		"target_ref":     map[string]any{"project_id": proj, "task_id": "t"},
		"change_spec":    map[string]any{"status": "done"},
		"addressee_tier": GovTierProjectSteward,
	})
	out, jerr := s.mcpPropose(context.Background(), defaultTeamID, agentID, args)
	if jerr != nil {
		t.Fatalf("mcpPropose: %v", jerr)
	}
	payload := unwrapMcpResult(t, out)
	if payload["assigned_tier"] != GovTierProjectSteward {
		t.Errorf("assigned_tier = %v; want project-steward", payload["assigned_tier"])
	}
}

// 8. Policy file `default_tier` chooses the tier when caller omits
// the hint.
func TestMcpPropose_PolicyDefaultTier_WhenNoCallerHint(t *testing.T) {
	s, dir := newTestServer(t)
	registerKind(t, ProposeKind{Kind: "task.set_status"})
	writePolicyFile(t, dir, `
kinds:
  task.set_status:
    default_tier: project-steward
`)
	s.policy.reload()

	proj := seedProject(t, s, defaultTeamID)
	agentID := seedAgentWithKind(t, s, defaultTeamID, "w", "claude-code", proj)
	args, _ := json.Marshal(map[string]any{
		"kind":        "task.set_status",
		"target_ref":  map[string]any{"project_id": proj, "task_id": "t"},
		"change_spec": map[string]any{"status": "done"},
	})
	out, jerr := s.mcpPropose(context.Background(), defaultTeamID, agentID, args)
	if jerr != nil {
		t.Fatalf("mcpPropose: %v", jerr)
	}
	payload := unwrapMcpResult(t, out)
	if payload["assigned_tier"] != GovTierProjectSteward {
		t.Errorf("assigned_tier = %v; want project-steward (from policy default)", payload["assigned_tier"])
	}
}

// 9. Invalid addressee_tier → -32602.
func TestMcpPropose_InvalidTier_Rejects(t *testing.T) {
	s, _ := newTestServer(t)
	registerKind(t, ProposeKind{Kind: "task.set_status"})
	proj := seedProject(t, s, defaultTeamID)
	agentID := seedAgentWithKind(t, s, defaultTeamID, "w", "claude-code", proj)
	args, _ := json.Marshal(map[string]any{
		"kind":           "task.set_status",
		"target_ref":     map[string]any{"project_id": proj},
		"change_spec":    map[string]any{"status": "done"},
		"addressee_tier": "manager", // not a governance tier
	})
	_, jerr := s.mcpPropose(context.Background(), defaultTeamID, agentID, args)
	if jerr == nil {
		t.Fatal("expected error on invalid tier")
	}
	if !strings.Contains(jerr.Message, "manager") {
		t.Errorf("error should name the bad tier; got %q", jerr.Message)
	}
}

// 10. Validate hook fires and rejects malformed change_spec.
func TestMcpPropose_ValidateHookRejects(t *testing.T) {
	s, _ := newTestServer(t)
	registerKind(t, ProposeKind{
		Kind: "task.set_status",
		Validate: func(_ context.Context, _ *Server, _, spec json.RawMessage) error {
			if !strings.Contains(string(spec), `"status"`) {
				return errStub("missing status")
			}
			return nil
		},
	})
	proj := seedProject(t, s, defaultTeamID)
	agentID := seedAgentWithKind(t, s, defaultTeamID, "w", "claude-code", proj)
	args, _ := json.Marshal(map[string]any{
		"kind":        "task.set_status",
		"target_ref":  map[string]any{"project_id": proj},
		"change_spec": map[string]any{"foo": "bar"},
	})
	_, jerr := s.mcpPropose(context.Background(), defaultTeamID, agentID, args)
	if jerr == nil {
		t.Fatal("expected validate rejection")
	}
	if !strings.Contains(jerr.Message, "missing status") {
		t.Errorf("error should bubble validate err; got %q", jerr.Message)
	}
}

// 11. Tier=project-steward + live project steward → assignees holds
//
//	the steward's actual handle, not the symbolic placeholder.
func TestMcpPropose_AssigneesIncludeLiveProjectSteward(t *testing.T) {
	s, _ := newTestServer(t)
	registerKind(t, ProposeKind{Kind: "task.set_status"})
	proj := seedProject(t, s, defaultTeamID)

	// One live project steward + one worker, both on the same project.
	stewardID := seedAgentWithKind(t, s, defaultTeamID, "steward.proj-1", "steward.v1", proj)
	_ = stewardID
	workerID := seedAgentWithKind(t, s, defaultTeamID, "w", "claude-code", proj)

	args, _ := json.Marshal(map[string]any{
		"kind":           "task.set_status",
		"target_ref":     map[string]any{"project_id": proj, "task_id": "t"},
		"change_spec":    map[string]any{"status": "done"},
		"addressee_tier": GovTierProjectSteward,
	})
	out, jerr := s.mcpPropose(context.Background(), defaultTeamID, workerID, args)
	if jerr != nil {
		t.Fatalf("mcpPropose: %v", jerr)
	}
	payload := unwrapMcpResult(t, out)
	row := readProposeRow(t, s, payload["request_id"].(string))
	if !strings.Contains(row.Assignees, "steward.proj-1") {
		t.Errorf("assignees should include live steward handle; got %q", row.Assignees)
	}
}

// --- helpers ---

func countAttentionRows(t *testing.T, s *Server) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM attention_items`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func unwrapMcpResult(t *testing.T, out any) map[string]any {
	t.Helper()
	res := out.(map[string]any)
	contents := res["content"].([]any)
	first := contents[0].(map[string]any)
	var payload map[string]any
	if err := json.Unmarshal([]byte(first["text"].(string)), &payload); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return payload
}

type errStub string

func (e errStub) Error() string { return string(e) }
