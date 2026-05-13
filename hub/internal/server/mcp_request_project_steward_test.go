// ADR-025 W4 — mcpRequestProjectSteward coverage.
//
// The general steward calls request_project_steward when the principal
// asks it to operate in a project that has no live steward. The tool
// creates a `project_steward_request` attention item carrying the
// suggestion payload that the mobile host-picker sheet (W7) reads to
// prefill its fields.

package server

import (
	"context"
	"encoding/json"
	"testing"
)

func TestMcpRequestProjectSteward_CreatesAttention(t *testing.T) {
	s, _ := newTestServer(t)
	hostID := seedHostCaps(t, s, `{
		"agents": {"claude-code": {"installed": true, "supports": ["M2"]}}
	}`)
	out, _, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "general-steward",
		Kind:        "claude-code",
		HostID:      hostID,
		SpawnSpec:   "driving_mode: M2\n",
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v", err)
	}
	proj := seedProjectInTeam(t, s, "demo-project")

	args, _ := json.Marshal(map[string]any{
		"project_id":        proj,
		"reason":            "Director asked me to draft a coder for the auth refactor.",
		"suggested_host_id": hostID,
	})
	res, jerr := s.mcpRequestProjectSteward(
		context.Background(), defaultTeamID, out.AgentID, args)
	if jerr != nil {
		t.Fatalf("mcpRequestProjectSteward: %s", jerr.Message)
	}
	if got := mcpToolBodyField(res, "kind"); got != "project_steward_request" {
		t.Errorf("returned kind=%q; want project_steward_request", got)
	}
	if got := mcpToolBodyField(res, "status"); got != "awaiting_response" {
		t.Errorf("returned status=%q; want awaiting_response", got)
	}

	// Persisted attention row carries the kind + scope + payload.
	var (
		kind, projectID, scopeKind, scopeID, severity, payload string
	)
	row := s.db.QueryRow(`
		SELECT kind, COALESCE(project_id, ''), scope_kind, COALESCE(scope_id, ''),
		       severity, COALESCE(pending_payload_json, '')
		  FROM attention_items
		 WHERE actor_handle = 'general-steward'
		 ORDER BY created_at DESC LIMIT 1`)
	if err := row.Scan(&kind, &projectID, &scopeKind, &scopeID, &severity, &payload); err != nil {
		t.Fatalf("attention lookup: %v", err)
	}
	if kind != "project_steward_request" {
		t.Errorf("kind=%q; want project_steward_request", kind)
	}
	if projectID != proj {
		t.Errorf("project_id=%q; want %q", projectID, proj)
	}
	if scopeKind != "project" || scopeID != proj {
		t.Errorf("scope=(%q,%q); want (project,%q)", scopeKind, scopeID, proj)
	}
	if severity != "major" {
		t.Errorf("severity=%q; want major", severity)
	}

	var p map[string]any
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if p["project_id"] != proj {
		t.Errorf("payload.project_id=%v; want %s", p["project_id"], proj)
	}
	if p["suggested_host_id"] != hostID {
		t.Errorf("payload.suggested_host_id=%v; want %s",
			p["suggested_host_id"], hostID)
	}
	if p["reason"] == "" {
		t.Error("payload.reason is empty")
	}
	if p["requested_by"] != out.AgentID {
		t.Errorf("payload.requested_by=%v; want %s",
			p["requested_by"], out.AgentID)
	}
}

func TestMcpRequestProjectSteward_RequiresProjectAndReason(t *testing.T) {
	s, _ := newTestServer(t)

	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"missing project_id", map[string]any{"reason": "because"}},
		{"missing reason", map[string]any{"project_id": "p"}},
		{"both missing", map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(tc.args)
			_, jerr := s.mcpRequestProjectSteward(
				context.Background(), defaultTeamID, "agent-id", raw)
			if jerr == nil {
				t.Fatalf("missing required field accepted: %v", tc.args)
			}
		})
	}
}

func TestMcpRequestProjectSteward_RejectsUnknownProject(t *testing.T) {
	s, _ := newTestServer(t)

	args, _ := json.Marshal(map[string]any{
		"project_id": "does-not-exist",
		"reason":     "draft worker",
	})
	_, jerr := s.mcpRequestProjectSteward(
		context.Background(), defaultTeamID, "agent-id", args)
	if jerr == nil {
		t.Fatal("unknown project accepted; want -32602")
	}
	if jerr.Code != -32602 {
		t.Errorf("code=%d; want -32602", jerr.Code)
	}
}

func TestMcpRequestProjectSteward_CrossTeamRejected(t *testing.T) {
	s, _ := newTestServer(t)
	// Foreign team + project that the default-team caller must not be
	// able to raise an attention against.
	if _, err := s.db.Exec(
		`INSERT INTO teams (id, name, created_at) VALUES ('other-team', 'other', ?)`,
		NowUTC()); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	foreignProj := seedProjectInTeamID(t, s, "other-team", "foreign-proj")

	args, _ := json.Marshal(map[string]any{
		"project_id": foreignProj,
		"reason":     "should be denied",
	})
	_, jerr := s.mcpRequestProjectSteward(
		context.Background(), defaultTeamID, "agent-id", args)
	if jerr == nil {
		t.Fatal("cross-team project accepted; want -32602")
	}
}
