package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// ADR-030 W8 — apply_agent_spawn.go unit tests. The end-to-end
// dispatch path (decide-handler → registry) is covered in
// handlers_propose_dispatch_test.go.

func TestAgentSpawn_RegisteredAtInit(t *testing.T) {
	pk, ok := LookupProposeKind("agent.spawn")
	if !ok {
		t.Fatal("agent.spawn not registered at init()")
	}
	if pk.Validate == nil || pk.DryRun == nil || pk.Apply == nil {
		t.Errorf("missing functions: validate=%v dry=%v apply=%v",
			pk.Validate != nil, pk.DryRun != nil, pk.Apply != nil)
	}
}

func TestAgentSpawn_Validate(t *testing.T) {
	pk, _ := LookupProposeKind("agent.spawn")
	cases := []struct {
		name   string
		spec   string
		wantOK bool
		wantIn string
	}{
		{"happy", `{"child_handle":"w","kind":"claude-code","spawn_spec_yaml":"backend:\n  cmd: x\n"}`, true, ""},
		{"missing change_spec", ``, false, "change_spec required"},
		{"missing child_handle", `{"kind":"claude-code"}`, false, "child_handle"},
		{"missing kind", `{"child_handle":"w"}`, false, "kind required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := pk.Validate(context.Background(), nil, nil, json.RawMessage(tc.spec))
			if tc.wantOK {
				if err != nil {
					t.Errorf("validate: %v; want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("want error; got nil")
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("err %q should contain %q", err.Error(), tc.wantIn)
			}
		})
	}
}

func TestAgentSpawn_DryRun_Preview(t *testing.T) {
	pk, _ := LookupProposeKind("agent.spawn")
	spec, _ := json.Marshal(map[string]any{
		"child_handle":    "w1",
		"kind":            "claude-code",
		"host_id":         "h1",
		"project_id":      "p1",
		"parent_agent_id": "a-parent",
		"spawn_spec_yaml": "backend:\n  cmd: x\n",
	})
	raw, err := pk.DryRun(context.Background(), nil, nil, spec)
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	var preview map[string]any
	_ = json.Unmarshal(raw, &preview)
	if preview["child_handle"] != "w1" {
		t.Errorf("child_handle = %v; want w1", preview["child_handle"])
	}
	if preview["engine_kind"] != "claude-code" {
		t.Errorf("engine_kind = %v; want claude-code", preview["engine_kind"])
	}
	if preview["host_id"] != "h1" {
		t.Errorf("host_id = %v; want h1", preview["host_id"])
	}
	if preview["parent_agent_id"] != "a-parent" {
		t.Errorf("parent_agent_id = %v; want a-parent", preview["parent_agent_id"])
	}
}

// Apply happy path: spawns through DoSpawn and emits agent.spawn
// audit row with the propose lineage on meta.
func TestAgentSpawn_Apply_HappyPath(t *testing.T) {
	s, _ := newTestServer(t)
	hostID := seedHostCaps(t, s, `{
		"agents": {"claude-code": {"installed": true, "supports": ["M1","M2","M4"]}}
	}`)
	pk, _ := LookupProposeKind("agent.spawn")
	spec, _ := json.Marshal(map[string]any{
		"child_handle":    "w-via-propose",
		"kind":            "claude-code",
		"host_id":         hostID,
		"spawn_spec_yaml": "kind: claude-code\nbackend:\n  cmd: echo test\n",
	})
	ac := ProposeApplyContext{
		AttentionID: "att-spawn-1", Team: defaultTeamID,
		AssignedTier: GovTierPrincipal, DeciderHandle: "@principal", Via: "propose",
	}
	executedRaw, err := pk.Apply(context.Background(), s, ac, nil, spec)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var executed map[string]any
	_ = json.Unmarshal(executedRaw, &executed)
	if executed["kind"] != "spawn" {
		t.Errorf("executed.kind = %v; want spawn", executed["kind"])
	}
	if executed["agent_id"] == nil || executed["agent_id"] == "" {
		t.Error("executed missing agent_id")
	}
	agentID := executed["agent_id"].(string)

	// Audit row carries via=propose + propose_id.
	var meta string
	if err := s.db.QueryRow(`
		SELECT meta_json FROM audit_events
		 WHERE action = 'agent.spawn' AND target_id = ?
		 ORDER BY ts DESC LIMIT 1`, agentID).Scan(&meta); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	for _, want := range []string{
		`"via":"propose"`,
		`"propose_id":"att-spawn-1"`,
		`"by_tier":"principal"`,
		`"handle":"w-via-propose"`,
	} {
		if !strings.Contains(meta, want) {
			t.Errorf("audit meta missing %s: %q", want, meta)
		}
	}
}

// Apply via legacy alias path stamps via=alias_legacy on the audit.
func TestAgentSpawn_Apply_AliasLegacyViaTag(t *testing.T) {
	s, _ := newTestServer(t)
	hostID := seedHostCaps(t, s, `{
		"agents": {"claude-code": {"installed": true, "supports": ["M1","M2","M4"]}}
	}`)
	pk, _ := LookupProposeKind("agent.spawn")
	spec, _ := json.Marshal(map[string]any{
		"child_handle":    "w-legacy",
		"kind":            "claude-code",
		"host_id":         hostID,
		"spawn_spec_yaml": "kind: claude-code\nbackend:\n  cmd: echo test\n",
	})
	ac := ProposeApplyContext{
		AttentionID: "att-legacy", Team: defaultTeamID,
		Via: "alias_legacy",
	}
	executedRaw, err := pk.Apply(context.Background(), s, ac, nil, spec)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var executed map[string]any
	_ = json.Unmarshal(executedRaw, &executed)
	agentID := executed["agent_id"].(string)
	var meta string
	_ = s.db.QueryRow(`
		SELECT meta_json FROM audit_events
		 WHERE action = 'agent.spawn' AND target_id = ?
		 ORDER BY ts DESC LIMIT 1`, agentID).Scan(&meta)
	if !strings.Contains(meta, `"via":"alias_legacy"`) {
		t.Errorf("audit meta should carry via=alias_legacy; got %q", meta)
	}
}

// Apply with missing team in apply context → fails cleanly.
func TestAgentSpawn_Apply_MissingTeam(t *testing.T) {
	s, _ := newTestServer(t)
	pk, _ := LookupProposeKind("agent.spawn")
	spec, _ := json.Marshal(map[string]any{
		"child_handle":    "w",
		"kind":            "claude-code",
		"spawn_spec_yaml": "backend:\n  cmd: x\n",
	})
	_, err := pk.Apply(context.Background(), s, ProposeApplyContext{}, nil, spec)
	if err == nil {
		t.Fatal("expected error on missing team")
	}
	if !strings.Contains(err.Error(), "team") {
		t.Errorf("err %q should mention team", err.Error())
	}
}
