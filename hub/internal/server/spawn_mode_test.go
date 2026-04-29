package server

import (
	"context"
	"strings"
	"testing"
)

// seedHostCaps inserts a host row with the given capabilities_json, so
// tests can exercise resolver behaviour against a realistic capability
// surface without running a real probe.
func seedHostCaps(t *testing.T, s *Server, capsJSON string) string {
	t.Helper()
	hostID := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO hosts (id, team_id, name, status, capabilities_json, created_at)
		VALUES (?, ?, ?, 'connected', ?, ?)`,
		hostID, defaultTeamID, "test-host-"+hostID[:6], capsJSON, NowUTC()); err != nil {
		t.Fatalf("seed host: %v", err)
	}
	return hostID
}

func TestDoSpawn_ModeFromYAML_PersistsResolvedMode(t *testing.T) {
	s, _ := newTestServer(t)
	hostID := seedHostCaps(t, s, `{
		"agents": {
			"claude-code": {"installed": true, "supports": ["M1","M2","M4"]}
		}
	}`)

	out, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "w1",
		Kind:        "claude-code",
		HostID:      hostID,
		SpawnSpec:   "driving_mode: M2\nfallback_modes: [M4]\n",
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v (status=%d)", err, status)
	}
	if out.Mode != "M2" {
		t.Fatalf("out.Mode = %q; want M2", out.Mode)
	}

	// The column must be persisted so handleListSpawns can return it.
	var persisted string
	if err := s.db.QueryRow(
		`SELECT COALESCE(driving_mode,'') FROM agents WHERE id = ?`, out.AgentID,
	).Scan(&persisted); err != nil {
		t.Fatalf("query driving_mode: %v", err)
	}
	if persisted != "M2" {
		t.Fatalf("persisted driving_mode = %q; want M2", persisted)
	}
}

func TestDoSpawn_ModeOverride_Strict(t *testing.T) {
	s, _ := newTestServer(t)
	// Host doesn't support M1 for codex — resolver should reject the
	// override (and not silently fall back to M4 from fallback_modes).
	hostID := seedHostCaps(t, s, `{
		"agents": {
			"codex": {"installed": true, "supports": ["M2","M4"]}
		}
	}`)

	_, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "w2",
		Kind:        "codex",
		HostID:      hostID,
		SpawnSpec:   "fallback_modes: [M4]\n",
		Mode:        "M1",
	})
	if err == nil {
		t.Fatal("want error for unsupported override; got nil")
	}
	if status != 400 {
		t.Fatalf("status = %d; want 400", status)
	}
	if !strings.Contains(err.Error(), "M1") {
		t.Fatalf("error should mention M1; got %v", err)
	}
}

func TestDoSpawn_BillingConflict_ClaudeCodeM1Subscription(t *testing.T) {
	s, _ := newTestServer(t)
	// Host declares subscription billing for claude-code; M1 requires
	// api_key, but the fallback list includes M2 which must win.
	hostID := seedHostCaps(t, s, `{
		"agents": {
			"claude-code": {"installed": true, "supports": ["M1","M2","M4"]}
		},
		"billing_declarations": {"claude-code": "subscription"}
	}`)

	out, _, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "w3",
		Kind:        "claude-code",
		HostID:      hostID,
		SpawnSpec:   "driving_mode: M1\nfallback_modes: [M2]\n",
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v", err)
	}
	if out.Mode != "M2" {
		t.Fatalf("out.Mode = %q; want M2 (billing-driven fallback)", out.Mode)
	}
}

func TestDoSpawn_NoModeDeclared_LeavesEmpty(t *testing.T) {
	s, _ := newTestServer(t)
	// No mode anywhere → resolveSpawnMode short-circuits to "" and the
	// row lands with NULL driving_mode. Host-runner defaults to M4.
	out, _, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "w4",
		Kind:        "claude-code",
		SpawnSpec:   "project_id: p1\n",
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v", err)
	}
	if out.Mode != "" {
		t.Fatalf("out.Mode = %q; want empty (opt-in)", out.Mode)
	}
	var persisted any
	if err := s.db.QueryRow(
		`SELECT driving_mode FROM agents WHERE id = ?`, out.AgentID,
	).Scan(&persisted); err != nil {
		t.Fatalf("query: %v", err)
	}
	if persisted != nil {
		t.Fatalf("persisted driving_mode = %v; want NULL", persisted)
	}
}

func TestDoSpawn_UnprobedHost_PermissiveFallback(t *testing.T) {
	s, _ := newTestServer(t)
	// Host exists but capabilities_json is empty — resolver should
	// trust the declared mode so the spawn isn't blocked before the
	// first probe lands.
	hostID := seedHostCaps(t, s, `{}`)

	out, _, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "w5",
		Kind:        "claude-code",
		HostID:      hostID,
		SpawnSpec:   "driving_mode: M2\n",
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v", err)
	}
	if out.Mode != "M2" {
		t.Fatalf("out.Mode = %q; want M2", out.Mode)
	}
}
