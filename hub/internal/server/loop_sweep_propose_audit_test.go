package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// ADR-030 W11.5 — sweep emits attention.escalation_advanced audit
// rows on each escalation_state transition for propose-kind
// attention rows.
//
// The sweep already emits `loop.stall_escalated` for every
// loop-entity stall; W11.5 adds a SECOND audit row scoped to
// propose semantics (carrying change_kind + assigned_tier +
// preview) so the activity feed can render "stalled propose"
// without re-fetching the row.

// seedProposeAttentionStale inserts a propose attention row whose
// inactivity_deadline is already in the past, so the next sweep
// tick will escalate it.
func seedProposeAttentionStale(t *testing.T, s *Server, projectID, changeKind string) string {
	t.Helper()
	id := NewID()
	now := time.Now().UTC()
	past := now.Add(-2 * time.Hour)
	cap := now.Add(7 * 24 * time.Hour) // generous absolute cap so we don't time out
	if _, err := s.db.Exec(`
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json,
			pending_payload_json, status, created_at,
			actor_kind, actor_handle,
			change_kind, assigned_tier,
			change_spec_json, target_ref_json,
			opened_at, last_progress_at, inactivity_deadline,
			absolute_cap, escalation_state
		) VALUES (?, ?, 'team', NULL, 'propose',
		          'Propose ` + changeKind + `', 'minor', '[]',
		          '{}', 'open', ?,
		          'agent', 'w1',
		          ?, 'project-steward',
		          '{"status":"done","result_summary":"shipped"}', '{}',
		          ?, ?, ?, ?, 'none')`,
		id, projectID, loopTS(now),
		changeKind,
		loopTS(now), loopTS(now), loopTS(past),
		loopTS(cap)); err != nil {
		t.Fatalf("seed propose attention: %v", err)
	}
	return id
}

func countAuditsForAttention(t *testing.T, s *Server, attID, action string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(
		`SELECT count(*) FROM audit_events WHERE action = ? AND target_id = ?`,
		action, attID,
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// 1. One sweep tick on a stale propose row emits exactly one
// attention.escalation_advanced audit (alongside the legacy
// loop.stall_escalated row).
func TestLoopSweep_ProposeEscalation_EmitsAuditOnce(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProject(t, s, defaultTeamID)
	attID := seedProposeAttentionStale(t, s, proj, "task.set_status")

	s.sweepLoopOnce(context.Background())

	if n := countAuditsForAttention(t, s, attID, "attention.escalation_advanced"); n != 1 {
		t.Errorf("attention.escalation_advanced count = %d; want 1", n)
	}
	if n := countAuditsForAttention(t, s, attID, "loop.stall_escalated"); n != 1 {
		t.Errorf("loop.stall_escalated count = %d; want 1", n)
	}

	// Row escalated to escalated_steward.
	var st string
	_ = s.db.QueryRow(`SELECT escalation_state FROM attention_items WHERE id = ?`, attID).Scan(&st)
	if st != EscalationSteward {
		t.Errorf("escalation_state = %q; want %q", st, EscalationSteward)
	}
}

// 2. Audit meta carries the W11.5-spec shape: change_kind, from_state,
// to_state, original_assigned_tier, project_id, change_spec_preview.
func TestLoopSweep_ProposeEscalation_AuditMetaShape(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProject(t, s, defaultTeamID)
	attID := seedProposeAttentionStale(t, s, proj, "task.set_status")

	s.sweepLoopOnce(context.Background())

	var meta string
	if err := s.db.QueryRow(`
		SELECT meta_json FROM audit_events
		 WHERE action = 'attention.escalation_advanced' AND target_id = ?
		 ORDER BY ts DESC LIMIT 1`, attID,
	).Scan(&meta); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(meta), &m)

	if m["attention_id"] != attID {
		t.Errorf("meta.attention_id = %v; want %s", m["attention_id"], attID)
	}
	if m["change_kind"] != "task.set_status" {
		t.Errorf("meta.change_kind = %v; want task.set_status", m["change_kind"])
	}
	if m["from_state"] != "none" {
		t.Errorf("meta.from_state = %v; want none", m["from_state"])
	}
	if m["to_state"] != EscalationSteward {
		t.Errorf("meta.to_state = %v; want %q", m["to_state"], EscalationSteward)
	}
	if m["original_assigned_tier"] != "project-steward" {
		t.Errorf("meta.original_assigned_tier = %v; want project-steward", m["original_assigned_tier"])
	}
	if m["project_id"] != proj {
		t.Errorf("meta.project_id = %v; want %s", m["project_id"], proj)
	}
	preview, _ := m["change_spec_preview"].(string)
	if !strings.Contains(preview, "shipped") {
		t.Errorf("meta.change_spec_preview should include change_spec content; got %q", preview)
	}
}

// 3. Two ticks across the same row at the same state emit ONE
// transition, not two. The escalation_state column itself is the
// dedup key — escalateStall only fires when inactivity_deadline is
// past, and the UPDATE bumps the deadline forward so the next tick
// doesn't re-fire until the next budget.
func TestLoopSweep_ProposeEscalation_NoDuplicateOnReTick(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProject(t, s, defaultTeamID)
	attID := seedProposeAttentionStale(t, s, proj, "deliverable.set_state")

	// First tick — escalates none → steward.
	s.sweepLoopOnce(context.Background())
	if n := countAuditsForAttention(t, s, attID, "attention.escalation_advanced"); n != 1 {
		t.Fatalf("first tick: count = %d; want 1", n)
	}
	// Second tick at the same simulated time — deadline was pushed
	// forward by the first escalateStall, so escalateStall won't fire
	// again. Audit count stays at 1.
	s.sweepLoopOnce(context.Background())
	if n := countAuditsForAttention(t, s, attID, "attention.escalation_advanced"); n != 1 {
		t.Errorf("after re-tick: count = %d; want 1 (no duplicate)", n)
	}
}

// 4. Non-propose attention row (legacy approval_request etc.) does
// NOT emit attention.escalation_advanced — only loop.stall_escalated.
// Regression for the W11.5 gate on change_kind != "".
func TestLoopSweep_LegacyAttention_NoProposeAudit(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProject(t, s, defaultTeamID)

	// Legacy approval_request — no change_kind.
	id := NewID()
	now := time.Now().UTC()
	past := now.Add(-2 * time.Hour)
	if _, err := s.db.Exec(`
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json,
			pending_payload_json, status, created_at,
			actor_kind, actor_handle,
			opened_at, last_progress_at, inactivity_deadline,
			absolute_cap, escalation_state
		) VALUES (?, ?, 'team', NULL, 'approval_request',
		          'legacy approve', 'minor', '[]',
		          '{}', 'open', ?,
		          'agent', 'w1',
		          ?, ?, ?, ?, 'none')`,
		id, proj, loopTS(now),
		loopTS(now), loopTS(now), loopTS(past),
		loopTS(now.Add(7*24*time.Hour))); err != nil {
		t.Fatalf("seed legacy attention: %v", err)
	}

	s.sweepLoopOnce(context.Background())

	if n := countAuditsForAttention(t, s, id, "attention.escalation_advanced"); n != 0 {
		t.Errorf("legacy attention emitted %d propose-audit rows; want 0", n)
	}
	if n := countAuditsForAttention(t, s, id, "loop.stall_escalated"); n != 1 {
		t.Errorf("loop.stall_escalated count = %d; want 1", n)
	}
}

// 5. truncateChangeSpecPreview clips at the limit + appends ellipsis.
func TestTruncateChangeSpecPreview(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"shorter", "abc", 10, "abc"},
		{"exact", "abcdefghij", 10, "abcdefghij"},
		{"longer", "abcdefghijklm", 10, "abcdefghij…"},
		{"empty", "", 10, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateChangeSpecPreview(tc.in, tc.n)
			if got != tc.want {
				t.Errorf("got %q; want %q", got, tc.want)
			}
		})
	}
}
