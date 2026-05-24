package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// ADR-030 W10 — permission_prompt re-addressing to parent steward.
//
// The strict same-project predicate in permissionPromptAddressee
// requires all three clauses:
//   1. worker.parent_agent_id IS NOT NULL
//   2. parent.kind LIKE 'steward.%'
//   3. parent.project_id IS NOT NULL AND = worker.project_id
//
// These tests cover each clause as the failing one (rows stay
// team-wide-addressed), then the happy path (row stamped with
// project-steward tier + the parent's id in current_assignees_json).

// seedParentedAgent inserts a worker with explicit parent_agent_id +
// project_id. Mirrors handlers_agents_project_id_test.go::seedAgent
// but adds the parent column.
func seedParentedAgent(t *testing.T, s *Server, team, handle, parentID, projectID string) string {
	t.Helper()
	id := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO agents (id, team_id, handle, kind, status,
		                   parent_agent_id, project_id, created_at)
		VALUES (?, ?, ?, 'claude-code', 'running', NULLIF(?,''), NULLIF(?,''), ?)`,
		id, team, handle, parentID, projectID, NowUTC()); err != nil {
		t.Fatalf("seed parented agent: %v", err)
	}
	return id
}

// callPermPrompt runs mcpPermissionPrompt with a tier-significant tool
// (Task — claude-code's sub-agent spawn, registered as TierSignificant
// in tiers.go) so the auto-allow gate doesn't short-circuit before
// the INSERT. Uses a 100ms ctx so the call returns ctx-cancelled
// shortly after the INSERT lands (the call would otherwise block up
// to permissionPromptTimeout=10m waiting for /decide).
func callPermPrompt(t *testing.T, s *Server, workerID string) string {
	t.Helper()
	args, _ := json.Marshal(map[string]any{
		"tool_name": "Task",
		"input":     map[string]any{"description": "do thing"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _ = s.mcpPermissionPrompt(ctx, defaultTeamID, workerID, args)

	// Read the most recent permission_prompt row for this worker.
	var id string
	if err := s.db.QueryRow(`
		SELECT id FROM attention_items
		 WHERE kind = 'permission_prompt'
		   AND actor_handle = (SELECT handle FROM agents WHERE id = ?)
		 ORDER BY created_at DESC LIMIT 1`, workerID).Scan(&id); err != nil {
		t.Fatalf("read permission_prompt row: %v", err)
	}
	return id
}

func readAttentionAddressing(t *testing.T, s *Server, id string) (assigneesJSON, assignedTier string) {
	t.Helper()
	if err := s.db.QueryRow(`
		SELECT current_assignees_json, COALESCE(assigned_tier, '')
		  FROM attention_items WHERE id = ?`, id).
		Scan(&assigneesJSON, &assignedTier); err != nil {
		t.Fatalf("read row: %v", err)
	}
	return
}

// 1. Happy path: worker has same-project steward parent → row
// addressed to the parent.
func TestPermPromptAddressing_SameProjectStewardParent_AddressesRow(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProject(t, s, defaultTeamID)
	stewardID := seedAgentWithKind(t, s, defaultTeamID, "steward.proj-A",
		"steward.v1", proj)
	workerID := seedParentedAgent(t, s, defaultTeamID, "w-A", stewardID, proj)

	attID := callPermPrompt(t, s, workerID)
	assignees, tier := readAttentionAddressing(t, s, attID)

	if tier != GovTierProjectSteward {
		t.Errorf("assigned_tier = %q; want %q", tier, GovTierProjectSteward)
	}
	if !strings.Contains(assignees, stewardID) {
		t.Errorf("assignees should contain steward id %q; got %q", stewardID, assignees)
	}
}

// 2. Cross-project steward parent (binding drift): parent IS a steward
// but project_id differs → row stays team-wide. Catches the v1.0.605
// class of bug where the parent-id pointer survives but the project
// binding has drifted.
func TestPermPromptAddressing_CrossProjectStewardParent_StaysTeamWide(t *testing.T) {
	s, _ := newTestServer(t)
	projA := seedProject(t, s, defaultTeamID)
	projB := seedProject(t, s, defaultTeamID)
	stewardID := seedAgentWithKind(t, s, defaultTeamID, "steward.proj-A",
		"steward.v1", projA)
	// Worker on projB but parent points at projA's steward.
	workerID := seedParentedAgent(t, s, defaultTeamID, "w-drift",
		stewardID, projB)

	attID := callPermPrompt(t, s, workerID)
	assignees, tier := readAttentionAddressing(t, s, attID)

	if tier != "" {
		t.Errorf("assigned_tier = %q; want empty (binding drift should not address)", tier)
	}
	if assignees != "[]" {
		t.Errorf("assignees = %q; want '[]' (team-wide)", assignees)
	}
}

// 3. Non-steward parent → row stays team-wide.
func TestPermPromptAddressing_NonStewardParent_StaysTeamWide(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProject(t, s, defaultTeamID)
	// Parent is a regular worker, not a steward.
	parentID := seedAgentWithKind(t, s, defaultTeamID, "parent-worker",
		"claude-code", proj)
	workerID := seedParentedAgent(t, s, defaultTeamID, "w-non-steward",
		parentID, proj)

	attID := callPermPrompt(t, s, workerID)
	assignees, tier := readAttentionAddressing(t, s, attID)

	if tier != "" {
		t.Errorf("assigned_tier = %q; want empty (non-steward parent)", tier)
	}
	if assignees != "[]" {
		t.Errorf("assignees = %q; want '[]'", assignees)
	}
}

// 4. Orphan worker (no parent_agent_id) → row stays team-wide.
func TestPermPromptAddressing_OrphanWorker_StaysTeamWide(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProject(t, s, defaultTeamID)
	workerID := seedParentedAgent(t, s, defaultTeamID, "w-orphan",
		"", proj) // empty parentID → NULL

	attID := callPermPrompt(t, s, workerID)
	assignees, tier := readAttentionAddressing(t, s, attID)

	if tier != "" {
		t.Errorf("assigned_tier = %q; want empty (orphan)", tier)
	}
	if assignees != "[]" {
		t.Errorf("assignees = %q; want '[]'", assignees)
	}
}

// 5. Both worker and parent have NULL project_id — the
// project_id IS NOT NULL guard prevents NULL = NULL from
// accidentally accepting them as "matching".
func TestPermPromptAddressing_NullProjectIDsBothSides_StaysTeamWide(t *testing.T) {
	s, _ := newTestServer(t)
	stewardID := seedAgentWithKind(t, s, defaultTeamID, "steward.unbound",
		"steward.v1", "") // no project
	workerID := seedParentedAgent(t, s, defaultTeamID, "w-null",
		stewardID, "") // no project

	attID := callPermPrompt(t, s, workerID)
	assignees, tier := readAttentionAddressing(t, s, attID)

	if tier != "" {
		t.Errorf("assigned_tier = %q; want empty (NULL=NULL guard)", tier)
	}
	if assignees != "[]" {
		t.Errorf("assignees = %q; want '[]'", assignees)
	}
}

// 6. Direct helper test — bypasses the full mcpPermissionPrompt
// path so the JOIN predicate is testable in isolation.
func TestPermissionPromptAddressee_HelperDirect(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProject(t, s, defaultTeamID)
	stewardID := seedAgentWithKind(t, s, defaultTeamID, "s", "steward.v1", proj)
	workerID := seedParentedAgent(t, s, defaultTeamID, "w", stewardID, proj)

	got := s.permissionPromptAddressee(context.Background(),
		defaultTeamID, workerID)
	if got != stewardID {
		t.Errorf("permissionPromptAddressee = %q; want %q", got, stewardID)
	}

	// Worker that doesn't exist → "".
	if got := s.permissionPromptAddressee(context.Background(),
		defaultTeamID, "ghost"); got != "" {
		t.Errorf("ghost worker = %q; want \"\"", got)
	}
}
