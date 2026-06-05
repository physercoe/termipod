package server

import (
	"testing"
)

// /resolve is the no-decision dismiss path for informational rows. These
// tests pin the guard that keeps it safe: it clears FYI kinds (notice,
// budget_exceeded) but refuses any kind that owes a waiting agent a
// turn-based reply — those must go through /decide so the agent wakes.

func seedOpenAttention(t *testing.T, c *e2eCtx, kind string) string {
	t.Helper()
	id := NewID()
	now := NowUTC()
	if _, err := c.s.db.Exec(`
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json, status, created_at,
			actor_kind, actor_handle
		) VALUES (?, NULL, 'team', NULL, ?,
		          'x', 'minor', '[]', 'open', ?,
		          'agent', 'someone')`, id, kind, now,
	); err != nil {
		t.Fatalf("seed %s: %v", kind, err)
	}
	return id
}

func TestResolveAttention_DismissesNoticeButNotDecidable(t *testing.T) {
	c := newE2E(t)

	// A notice (FYI, no waiting agent) dismisses cleanly → 204.
	noticeID := seedOpenAttention(t, c, "notice")
	if status, body := c.call("POST",
		"/v1/teams/"+c.teamID+"/attention/"+noticeID+"/resolve", nil); status != 204 {
		t.Fatalf("resolve notice = %d (%v); want 204", status, body)
	}
	var st string
	if err := c.s.db.QueryRow(
		`SELECT status FROM attention_items WHERE id = ?`, noticeID).Scan(&st); err != nil {
		t.Fatalf("status lookup: %v", err)
	}
	if st != "resolved" {
		t.Fatalf("notice status = %q after dismiss; want resolved", st)
	}

	// budget_exceeded (system FYI) is dismissable too.
	budgetID := seedOpenAttention(t, c, "budget_exceeded")
	if status, _ := c.call("POST",
		"/v1/teams/"+c.teamID+"/attention/"+budgetID+"/resolve", nil); status != 204 {
		t.Fatalf("resolve budget_exceeded = %d; want 204", status)
	}

	// An approval_request owes a waiting agent a reply → 409, must use /decide.
	apprID := seedOpenAttention(t, c, "approval_request")
	if status, _ := c.call("POST",
		"/v1/teams/"+c.teamID+"/attention/"+apprID+"/resolve", nil); status != 409 {
		t.Fatalf("resolve approval_request = %d; want 409 (awaits agent reply)", status)
	}
	// It must still be open — the guard fired before the UPDATE.
	if err := c.s.db.QueryRow(
		`SELECT status FROM attention_items WHERE id = ?`, apprID).Scan(&st); err != nil {
		t.Fatalf("status lookup: %v", err)
	}
	if st != "open" {
		t.Fatalf("approval_request status = %q; the guard must leave it open for /decide", st)
	}

	// Dismissing an already-resolved row is a 409, not a silent no-op.
	if status, _ := c.call("POST",
		"/v1/teams/"+c.teamID+"/attention/"+noticeID+"/resolve", nil); status != 409 {
		t.Fatalf("re-resolve = %d; want 409", status)
	}
}
