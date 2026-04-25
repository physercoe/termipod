package server

import (
	"context"
	"testing"
)

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

// Negative: while the first agent is still live (not archived), a second
// spawn under the same handle must still 409. The partial index keeps
// the original "no two live agents share a handle" rule intact.
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
