package server

import (
	"context"
	"sort"
	"testing"
)

// Regression: migration 0023 must preserve every column the application
// code reads/writes. The first cut of 0023 dropped last_capture and
// last_capture_at when recreating the table — runtime UPDATEs from the
// pane-capture handler then failed silently. Asserting via PRAGMA
// table_info catches column drift on any future agents-table rebuild.
func TestAgentsTable_HasAllRequiredColumns(t *testing.T) {
	s, _ := newTestServer(t)

	rows, err := s.db.Query(`PRAGMA table_info(agents)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer rows.Close()

	got := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[name] = true
	}

	want := []string{
		"id", "team_id", "handle", "kind", "backend_json", "capabilities_json",
		"parent_agent_id", "status", "host_id", "pane_id", "worktree_path",
		"journal_path", "budget_cents", "spent_cents", "idle_since",
		"pause_state", "last_prompt_tail", "created_at", "terminated_at",
		"last_capture", "last_capture_at", "archived_at", "driving_mode",
	}
	var missing []string
	for _, c := range want {
		if !got[c] {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("agents table missing columns: %v", missing)
	}
}

// Regression: respawning an agent under the same handle as a previously
// archived agent must succeed. Before migration 0023 the table-level
// UNIQUE(team_id, handle) blocked this with SQLITE_CONSTRAINT_UNIQUE
// (HTTP 409), even though the archived row was no longer visible to LIST.
// Now the constraint is a partial unique index that excludes archived
// rows, so the second spawn should land cleanly.
func TestDoSpawn_RespawnAfterArchive_SameHandle(t *testing.T) {
	s, _ := newTestServer(t)
	hostID := seedHostCaps(t, s, `{
		"agents": {
			"claude-code": {"installed": true, "supports": ["M1","M2","M4"]}
		}
	}`)

	out1, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "steward",
		Kind:        "claude-code",
		HostID:      hostID,
		SpawnSpec:   "kind: claude-code\n",
	})
	if err != nil {
		t.Fatalf("first spawn: %v (status=%d)", err, status)
	}

	// Simulate the archive endpoint: terminate + soft-delete.
	if _, err := s.db.Exec(
		`UPDATE agents SET status='terminated', archived_at=? WHERE id=?`,
		NowUTC(), out1.AgentID,
	); err != nil {
		t.Fatalf("archive setup: %v", err)
	}

	out2, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "steward",
		Kind:        "claude-code",
		HostID:      hostID,
		SpawnSpec:   "kind: claude-code\n",
	})
	if err != nil {
		t.Fatalf("respawn after archive: %v (status=%d)", err, status)
	}
	if out2.AgentID == out1.AgentID {
		t.Fatalf("respawn reused archived agent id %q; want a new row", out1.AgentID)
	}
}

// Regression: respawning after *terminate-without-archive* must succeed.
// The "Recreate steward" UI calls PATCH status='terminated' but never
// hits the archive endpoint, so archived_at stays NULL. Migration 0023's
// partial index (WHERE archived_at IS NULL) kept the handle reserved on
// the terminated row, re-triggering the original 2067 → 409. Migration
// 0024 widens the predicate to also exclude non-live statuses.
func TestDoSpawn_RespawnAfterTerminate_NotArchived(t *testing.T) {
	s, _ := newTestServer(t)
	hostID := seedHostCaps(t, s, `{
		"agents": {
			"claude-code": {"installed": true, "supports": ["M1","M2","M4"]}
		}
	}`)

	out1, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "steward",
		Kind:        "claude-code",
		HostID:      hostID,
		SpawnSpec:   "kind: claude-code\n",
	})
	if err != nil {
		t.Fatalf("first spawn: %v (status=%d)", err, status)
	}

	// Mirror the UI's "Recreate steward" flow: terminate without archiving.
	// archived_at stays NULL on the dead row.
	if _, err := s.db.Exec(
		`UPDATE agents SET status='terminated' WHERE id=?`,
		out1.AgentID,
	); err != nil {
		t.Fatalf("terminate setup: %v", err)
	}

	out2, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "steward",
		Kind:        "claude-code",
		HostID:      hostID,
		SpawnSpec:   "kind: claude-code\n",
	})
	if err != nil {
		t.Fatalf("respawn after terminate: %v (status=%d)", err, status)
	}
	if out2.AgentID == out1.AgentID {
		t.Fatalf("respawn reused terminated agent id %q; want a new row", out1.AgentID)
	}
}

// Negative: while the first agent is still live (not terminated/archived),
// a second spawn under the same handle must still 409. The partial index
// keeps the "no two live agents share a handle" rule intact.
func TestDoSpawn_DuplicateHandle_LiveAgent_StillRejected(t *testing.T) {
	s, _ := newTestServer(t)
	hostID := seedHostCaps(t, s, `{
		"agents": {
			"claude-code": {"installed": true, "supports": ["M1","M2","M4"]}
		}
	}`)

	if _, _, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "steward",
		Kind:        "claude-code",
		HostID:      hostID,
		SpawnSpec:   "kind: claude-code\n",
	}); err != nil {
		t.Fatalf("first spawn: %v", err)
	}

	_, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "steward",
		Kind:        "claude-code",
		HostID:      hostID,
		SpawnSpec:   "kind: claude-code\n",
	})
	if err == nil {
		t.Fatal("want error on duplicate live handle; got nil")
	}
	if status != 409 {
		t.Fatalf("status = %d; want 409", status)
	}
}
