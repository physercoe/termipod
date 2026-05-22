package server

import (
	"context"
	"errors"
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
		SpawnSpec:   "driving_mode: M2\nfallback_modes: [M4]\nbackend:\n  cmd: echo test\n",
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
		SpawnSpec:   "fallback_modes: [M4]\nbackend:\n  cmd: echo test\n",
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
		SpawnSpec:   "driving_mode: M1\nfallback_modes: [M2]\nbackend:\n  cmd: echo test\n",
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
		SpawnSpec:   "name: w4\nbackend:\n  cmd: echo test\n",
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
		SpawnSpec:   "driving_mode: M2\nbackend:\n  cmd: echo test\n",
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v", err)
	}
	if out.Mode != "M2" {
		t.Fatalf("out.Mode = %q; want M2", out.Mode)
	}
}

// Singleton handlers (the persistent general steward, future similar
// kinds) pass a *template id* in spawnIn.Kind because that's what the
// agents.kind row needs to be for findRunningGeneralSteward to recognise
// the singleton. The mode resolver must still look the host's caps up by
// the *family* declared in the template's backend.kind — without that
// the lookup misses on every probed host (caps.Agents only carries family
// keys: claude-code/codex/gemini-cli) and resolution fails with
// "no compatible mode" the moment any probe has run.
// ADR-035 W2: antigravity declares supports:[M4] at the family level
// (no ACP / no --output-format on agy 1.0.1). An explicit M1/M2 request
// must fail fast with a 422 + Hint EVEN on an unprobed host, where the
// host-caps fallback is permissive ([M1,M2,M4]) and would otherwise let
// the spawn coerce through and hang at launch. The family floor is the
// boundary that catches it.
func TestDoSpawn_FamilyModeFloor_AntigravityRejectsM1(t *testing.T) {
	s, _ := newTestServer(t)
	hostID := seedHostCaps(t, s, `{}`) // unprobed: permissive host fallback

	_, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "agy1",
		Kind:        "antigravity",
		HostID:      hostID,
		SpawnSpec:   "driving_mode: M1\nbackend:\n  cmd: agy\n",
	})
	if err == nil {
		t.Fatal("want error for M1 against an M4-only engine; got nil")
	}
	if status != 422 {
		t.Fatalf("status = %d; want 422", status)
	}
	var me *ModeUnsupportedError
	if !errors.As(err, &me) {
		t.Fatalf("want *ModeUnsupportedError; got %T (%v)", err, err)
	}
	if me.Family != "antigravity" || me.Mode != "M1" {
		t.Fatalf("error fields = {%q,%q}; want {antigravity,M1}", me.Family, me.Mode)
	}
	if h := me.Hint(); !strings.Contains(h.HintText, "M4") {
		t.Fatalf("hint should name the supported mode M4; got %q", h.HintText)
	}
}

// The floor must NOT block a supported mode: an explicit M4 antigravity
// spawn resolves normally even on an unprobed host.
func TestDoSpawn_FamilyModeFloor_AntigravityAcceptsM4(t *testing.T) {
	s, _ := newTestServer(t)
	hostID := seedHostCaps(t, s, `{}`)

	out, _, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "agy2",
		Kind:        "antigravity",
		HostID:      hostID,
		SpawnSpec:   "driving_mode: M4\nbackend:\n  cmd: agy\n",
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v", err)
	}
	if out.Mode != "M4" {
		t.Fatalf("out.Mode = %q; want M4", out.Mode)
	}
}

func TestDoSpawn_ModeFromBackendKind_TemplateIdAsKind(t *testing.T) {
	s, _ := newTestServer(t)
	hostID := seedHostCaps(t, s, `{
		"agents": {
			"claude-code": {"installed": true, "supports": ["M1","M2","M4"]}
		}
	}`)

	out, _, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "@steward",
		Kind:        "steward.general.v1",
		HostID:      hostID,
		SpawnSpec: "driving_mode: M2\n" +
			"fallback_modes: [M4]\n" +
			"backend:\n  kind: claude-code\n  cmd: echo test\n",
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v", err)
	}
	if out.Mode != "M2" {
		t.Fatalf("out.Mode = %q; want M2 (resolved via backend.kind)", out.Mode)
	}
}
