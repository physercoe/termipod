package server

import (
	"context"
	"testing"
)

// insertEventAtTS writes one agent_events row with an explicit ts so tests can
// pin the MIN(ts) ordering sessionAgentIDs depends on.
func insertEventAtTS(t *testing.T, s *Server, agentID, sesID string, seq int, ts string) {
	t.Helper()
	if _, err := evWForTeam(t, s, defaultTeamID).Exec(
		`INSERT INTO agent_events
		   (id, agent_id, seq, ts, kind, producer, payload_json, session_id)
		 VALUES (?, ?, ?, ?, 'text', 'agent', '{}', ?)`,
		"evt-"+sesID+"-"+agentID+"-"+itoaInt(seq),
		agentID, seq, ts, sesID,
	); err != nil {
		t.Fatalf("insert event: %v", err)
	}
}

func seedAgentRowWithID(t *testing.T, s *Server, team, agentID string) {
	t.Helper()
	if _, err := s.db.Exec(
		`INSERT INTO agents (id, team_id, handle, kind, created_at)
		 VALUES (?, ?, ?, 'claude-code', ?)`,
		agentID, team, "h-"+agentID, NowUTC()); err != nil {
		t.Fatalf("insert agent: %v", err)
	}
}

// TestSessionAgentIDs_CachesOnlyWhenArchived verifies the #118 §1
// denormalization: the agent set is scanned (authoritatively) for a live
// session and served from sessions.agent_ids_json for an archived one.
func TestSessionAgentIDs_CachesOnlyWhenArchived(t *testing.T) {
	s, _ := newA2ATestServer(t)
	ctx := context.Background()
	const sesID = "ses-cache"
	const a1, a2, a3 = "agent-1", "agent-2", "agent-3"

	seedAgentRowWithID(t, s, defaultTeamID, a1)
	seedAgentRowWithID(t, s, defaultTeamID, a2)
	seedAgentRowWithID(t, s, defaultTeamID, a3)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions
		   (id, team_id, title, scope_kind, current_agent_id,
		    status, opened_at, last_active_at)
		 VALUES (?, ?, 'cache test', 'team', ?, 'active', ?, ?)`,
		sesID, defaultTeamID, a1, NowUTC(), NowUTC()); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	// a1 active first, then a2 (resume). Ordered by MIN(ts).
	insertEventAtTS(t, s, a1, sesID, 1, "2026-06-27T00:00:01Z")
	insertEventAtTS(t, s, a2, sesID, 1, "2026-06-27T00:00:02Z")

	// While active: scan, do not cache.
	got, err := s.sessionAgentIDs(ctx, defaultTeamID, sesID)
	if err != nil {
		t.Fatalf("sessionAgentIDs (active): %v", err)
	}
	if len(got) != 2 || got[0] != a1 || got[1] != a2 {
		t.Fatalf("active scan = %v, want [%s %s]", got, a1, a2)
	}
	var cached *string
	if err := s.db.QueryRowContext(ctx,
		`SELECT agent_ids_json FROM sessions WHERE id = ?`, sesID).Scan(&cached); err != nil {
		t.Fatalf("read cache col: %v", err)
	}
	if cached != nil {
		t.Fatalf("agent_ids_json materialized for a live session: %q", *cached)
	}

	// Archive the session — now the set is immutable and may be cached.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET status = 'archived' WHERE id = ?`, sesID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	got, err = s.sessionAgentIDs(ctx, defaultTeamID, sesID)
	if err != nil {
		t.Fatalf("sessionAgentIDs (archived, first): %v", err)
	}
	if len(got) != 2 || got[0] != a1 || got[1] != a2 {
		t.Fatalf("archived scan = %v, want [%s %s]", got, a1, a2)
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT agent_ids_json FROM sessions WHERE id = ?`, sesID).Scan(&cached); err != nil {
		t.Fatalf("read cache col after archive: %v", err)
	}
	if cached == nil {
		t.Fatal("agent_ids_json not materialized after archived read")
	}

	// Prove the cache is now served O(1): add a third agent's events to the
	// session's shard, then read again — the result must still be the cached
	// (sealed) set, NOT a fresh scan that would include a3.
	insertEventAtTS(t, s, a3, sesID, 1, "2026-06-27T00:00:03Z")
	got, err = s.sessionAgentIDs(ctx, defaultTeamID, sesID)
	if err != nil {
		t.Fatalf("sessionAgentIDs (archived, cached): %v", err)
	}
	if len(got) != 2 || got[0] != a1 || got[1] != a2 {
		t.Fatalf("cached read = %v, want sealed [%s %s] (not a re-scan)", got, a1, a2)
	}
}
