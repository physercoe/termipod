// mcp_authority_spawn.go — ADR-025 W9 gate for `agents.spawn`.
//
// Two enforcement axes that the role manifest can't express alone:
//
//   1. The general steward (`kind = steward.general.v1`) is blocked
//      from `agents.spawn` entirely. Its role is "concierge that
//      delegates"; it raises a `project_steward_request` attention
//      item (W4) instead of spawning workers directly.
//
//   2. When the spawn request carries `project_id` (per ADR-025 D5),
//      the caller's `agent_id` must match `projects.steward_agent_id`
//      for that project. Otherwise the project's accountability chain
//      forks: a stranger steward could create workers attributed to
//      somebody else's project.
//
// Non-MCP callers (REST clients with a user/principal bearer) bypass
// this gate by design — the user IS the principal and can spawn into
// any project they own. Mobile UI rerouting (W10/W11) handles that
// surface separately.

package server

import (
	"encoding/json"
	"strings"

	"gopkg.in/yaml.v3"
)

// authorizeAgentsSpawn enforces the W9 rules. agentID is the caller's
// resolved agent_id (from scope_json); empty means principal/bootstrap
// token — that path bypasses (return nil).
//
// Returns nil to allow; a jrpcError to deny.
func (s *Server) authorizeAgentsSpawn(agentID string, raw json.RawMessage) *jrpcError {
	if agentID == "" {
		return nil // principal token bypass.
	}

	// Look up the caller's kind so we can apply the general-steward
	// outright block. Missing row → deny (the row should always exist
	// if the bearer resolved; not finding it points to a stale token).
	var callerKind string
	if err := s.db.QueryRow(
		`SELECT kind FROM agents WHERE id = ?`, agentID,
	).Scan(&callerKind); err != nil {
		return &jrpcError{
			Code:    -32601,
			Message: "agents.spawn: caller agent not found",
		}
	}
	if callerKind == generalStewardKind {
		return &jrpcError{
			Code: -32601,
			Message: "agents.spawn: general steward must delegate via " +
				"request_project_steward (ADR-025 D2)",
		}
	}

	// Resolve project_id from the request: top-level body field wins
	// for the unusual MCP-direct case, otherwise parse it out of the
	// spawn_spec_yaml (canonical site every template + mobile sheet
	// writes to per ADR-025 W2).
	projectID := pickProjectIDFromArgs(raw)
	if projectID == "" {
		return nil // not a project-bound spawn; pre-ADR path stays open.
	}

	// Project-bound spawn — the caller must be that project's steward.
	var stewardID string
	if err := s.db.QueryRow(
		`SELECT COALESCE(steward_agent_id, '') FROM projects WHERE id = ?`,
		projectID,
	).Scan(&stewardID); err != nil {
		return &jrpcError{
			Code:    -32601,
			Message: "agents.spawn: project " + projectID + " not found",
		}
	}
	if stewardID == "" {
		// Project exists but has no live steward yet. The director must
		// materialize one through the W3 ensure endpoint before any
		// project-bound spawn is allowed.
		return &jrpcError{
			Code: -32601,
			Message: "agents.spawn: project has no steward yet; " +
				"call POST /steward/ensure first (ADR-025 W3)",
		}
	}
	if stewardID != agentID {
		return &jrpcError{
			Code: -32601,
			Message: "agents.spawn: caller is not the steward for project " +
				projectID + " (ADR-025 D3)",
		}
	}
	return nil
}

// pickProjectIDFromArgs returns the spawn's project binding from the
// raw MCP args. Mirrors the precedence DoSpawn applies: YAML wins
// over body. Returns "" when neither carries a binding.
//
// Args shape:
//
//	{
//	  "project_id":      "<optional body fallback>",
//	  "spawn_spec_yaml": "project_id: <canonical site>\n..."
//	  ...
//	}
func pickProjectIDFromArgs(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var top struct {
		ProjectID     string `json:"project_id"`
		SpawnSpecYaml string `json:"spawn_spec_yaml"`
	}
	if err := json.Unmarshal(raw, &top); err != nil {
		return ""
	}
	// YAML first — same precedence as DoSpawn.
	if y := strings.TrimSpace(top.SpawnSpecYaml); y != "" {
		var spec struct {
			ProjectID string `yaml:"project_id"`
		}
		_ = yaml.Unmarshal([]byte(top.SpawnSpecYaml), &spec)
		if spec.ProjectID != "" {
			return spec.ProjectID
		}
	}
	return top.ProjectID
}
