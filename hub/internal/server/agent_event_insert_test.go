package server

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// P0 of ADR-042 / plans/dense-session-ordinal.md — the centralized event
// insert. These pin the helper's contract and ratchet the duplication out.

// insertAgentEvent assigns a monotonic per-agent seq starting at 1.
func TestInsertAgentEvent_MonotonicPerAgent(t *testing.T) {
	s, _ := newA2ATestServer(t)
	agent := seedAgentRow(t, s, defaultTeamID, "p0-mono", "claude-code")

	var wantSeq int64
	for _, body := range []string{"a", "b", "c"} {
		wantSeq++
		id, seq, ts, err := insertAgentEvent(context.Background(), s.db, agentEventInsert{
			AgentID:     agent,
			SessionID:   "sess-1",
			Kind:        "text",
			Producer:    "agent",
			PayloadJSON: `{"body":"` + body + `"}`,
		})
		if err != nil {
			t.Fatalf("insert %q: %v", body, err)
		}
		if id == "" || ts == "" {
			t.Fatalf("insert %q: empty id/ts (id=%q ts=%q)", body, id, ts)
		}
		if seq != wantSeq {
			t.Fatalf("insert %q: want seq %d, got %d", body, wantSeq, seq)
		}
		// The returned seq must match what's stored.
		var stored int64
		if err := s.db.QueryRow(`SELECT seq FROM agent_events WHERE id = ?`, id).Scan(&stored); err != nil {
			t.Fatalf("read back %q: %v", body, err)
		}
		if stored != seq {
			t.Fatalf("insert %q: returned seq %d != stored %d", body, seq, stored)
		}
	}
}

// The known defect ADR-042 fixes: seq is per-agent, so two agents sharing one
// session both restart at 1 — their seq ranges COLLIDE. This test documents
// the collision (the session-ordinal added in P1 is what makes a session-unique
// coordinate); if it ever stops being true, the foundation assumption changed.
func TestInsertAgentEvent_SeqIsPerAgentNotPerSession(t *testing.T) {
	s, _ := newA2ATestServer(t)
	a := seedAgentRow(t, s, defaultTeamID, "p0-a", "claude-code")
	b := seedAgentRow(t, s, defaultTeamID, "p0-b", "claude-code")
	const session = "shared-session"

	seqA1, seqA2 := insertSeq(t, s, a, session), insertSeq(t, s, a, session)
	seqB1, seqB2 := insertSeq(t, s, b, session), insertSeq(t, s, b, session)

	if seqA1 != 1 || seqA2 != 2 || seqB1 != 1 || seqB2 != 2 {
		t.Fatalf("want per-agent seqs (1,2)/(1,2), got A=(%d,%d) B=(%d,%d)",
			seqA1, seqA2, seqB1, seqB2)
	}
	// Both agents have a seq=1 row inside the SAME session — the collision.
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM agent_events WHERE session_id = ? AND seq = 1`, session,
	).Scan(&n); err != nil {
		t.Fatalf("count seq=1: %v", err)
	}
	if n != 2 {
		t.Fatalf("want 2 distinct rows at seq=1 in one session (the collision), got %d", n)
	}
}

// An empty SessionID is normalized to SQL NULL — an event either belongs to a
// session or it does not; "" is never a session id (and P1 only assigns
// session_ordinal when session_id is non-NULL).
func TestInsertAgentEvent_EmptySessionStoresNull(t *testing.T) {
	s, _ := newA2ATestServer(t)
	agent := seedAgentRow(t, s, defaultTeamID, "p0-null", "claude-code")

	id, _, _, err := insertAgentEvent(context.Background(), s.db, agentEventInsert{
		AgentID:     agent,
		SessionID:   "",
		Kind:        "system",
		Producer:    "system",
		PayloadJSON: `{}`,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	var session sql.NullString
	if err := s.db.QueryRow(`SELECT session_id FROM agent_events WHERE id = ?`, id).Scan(&session); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if session.Valid {
		t.Fatalf("want NULL session_id, got %q", session.String)
	}
}

// Forward-only ratchet: no production code may hand-roll the agent_events
// COALESCE(MAX(seq)) insert — it must go through insertAgentEvent, the single
// assignment site (ADR-042). Test fixtures (seedEventAt's explicit-seq VALUES
// insert) are exempt; this scans non-test source only.
func TestNoInlineAgentEventInsert_OutsideHelper(t *testing.T) {
	// The insert idiom specifically (the +1 increment) — not a bare
	// COALESCE(MAX(seq),0) watermark read, which is a legitimate read.
	const marker = "COALESCE(MAX(seq), 0) + 1"
	var offenders []string
	root := filepath.Join("..", "..", "internal")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "agent_event_insert.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), marker) {
			offenders = append(offenders, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("inline agent_events insert outside insertAgentEvent (route through the helper): %v", offenders)
	}
}

func insertSeq(t *testing.T, s *Server, agentID, session string) int64 {
	t.Helper()
	_, seq, _, err := insertAgentEvent(context.Background(), s.db, agentEventInsert{
		AgentID:     agentID,
		SessionID:   session,
		Kind:        "text",
		Producer:    "agent",
		PayloadJSON: `{}`,
	})
	if err != nil {
		t.Fatalf("insertSeq: %v", err)
	}
	return seq
}
