// handlers_project_steward.go — idempotent ensure-spawn for the
// per-project steward (ADR-025 W3).
//
// ADR-025 D1 tightens ADR-017 D6: every engaged project has *exactly
// one* steward, materialized lazily on first engagement (director
// taps the project's steward overlay, sends a message, or a peer
// delegates a project-scoped intent). Director consents to the spawn
// via the host-picker sheet (W7). This handler is the entry point
// that sheet posts to.
//
// Structurally it mirrors handleEnsureGeneralSteward — first-call
// spawn, subsequent calls coalesce on the running instance. The
// difference is the binding: project stewards are scoped to a
// project_id (W1+W2 column on `agents`) instead of the team-wide
// singleton the general steward represents. projects.steward_agent_id
// is kept in lockstep so the existing field stays authoritative.

package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// projectStewardKindDefault is the bundled `steward.v1` template id
// — the generic Claude Code steward most project flows want by
// default. Mobile's host-picker sheet (W7) may swap this for a
// domain-specific kind (e.g. steward.research.v1) by passing `kind:`
// in the request body.
const projectStewardKindDefault = "steward.v1"

// projectStewardHandle is the canonical handle for a project steward.
// Handles are unique per team-active-set (partial index), so two
// projects can both have a `@steward.<project_id_short>` without
// colliding even before one archives.
//
// We use a per-project handle suffix instead of the team-singleton
// `@steward` (used by the general steward) so the steward list
// reads naturally in the mobile sessions / hub-meta screens.
func projectStewardHandle(projectID string) string {
	if len(projectID) > 8 {
		return "@steward." + projectID[:8]
	}
	return "@steward." + projectID
}

// ensureProjectStewardIn is the request body. host_id is required
// once W7 lands the host-picker sheet — for now we still
// pickFirstHost when empty so demos work without a sheet.
// permission_mode chooses which entry from the template's
// backend.permission_modes map drives the spawn flags (skip /
// prompt; empty = template default).
// kind is an optional override for callers that want a
// domain-specific steward template (e.g. `steward.research.v1`).
type ensureProjectStewardIn struct {
	HostID         string `json:"host_id,omitempty"`
	PermissionMode string `json:"permission_mode,omitempty"`
	Kind           string `json:"kind,omitempty"`
}

// ensureProjectStewardOut matches the general-steward shape so
// mobile can share rendering code between the two surfaces.
type ensureProjectStewardOut struct {
	AgentID    string `json:"agent_id"`
	SpawnID    string `json:"spawn_id,omitempty"`
	Status     string `json:"status"`
	AlreadyRan bool   `json:"already_running"`
	ProjectID  string `json:"project_id"`
}

// handleEnsureProjectSteward returns the project's running steward,
// spawning one if none exists. Director-auth only — the general
// steward delegates here via an attention item (W4) rather than
// calling directly.
func (s *Server) handleEnsureProjectSteward(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	project := chi.URLParam(r, "project")
	ctx := r.Context()

	if err := s.validateProjectInTeam(ctx, team, project); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}

	var body ensureProjectStewardIn
	_ = json.NewDecoder(r.Body).Decode(&body)

	// Fast path: an existing live steward bound to this project.
	if existing, err := s.findRunningProjectSteward(ctx, team, project); err == nil && existing != "" {
		writeJSON(w, http.StatusOK, ensureProjectStewardOut{
			AgentID:    existing,
			Status:     "running",
			AlreadyRan: true,
			ProjectID:  project,
		})
		return
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusInternalServerError, "lookup: "+err.Error())
		return
	}

	// Pick a host: explicit body field wins; otherwise auto-pick.
	var hostID string
	var err error
	if body.HostID != "" {
		if err = s.validateTeamHost(ctx, team, body.HostID); err != nil {
			writeErr(w, http.StatusBadRequest, "host_id: "+err.Error())
			return
		}
		hostID = body.HostID
	} else {
		hostID, err = s.pickFirstHost(ctx, team)
		if err != nil {
			writeErr(w, http.StatusFailedDependency, "no host available: "+err.Error())
			return
		}
	}

	// Resolve the template id (and the matching .yaml on disk).
	kind := body.Kind
	if kind == "" {
		kind = projectStewardKindDefault
	}
	specBody, err := s.loadBuiltinAgentTemplate(kind + ".yaml")
	if err != nil {
		writeErr(w, http.StatusInternalServerError,
			"load "+kind+" template: "+err.Error())
		return
	}

	permission := body.PermissionMode
	if permission == "" {
		permission = "skip" // matches the general-steward bootstrap default
	}

	// Compose the spawn input. ProjectID is fed via the body
	// fallback (W2 precedence: YAML wins, body is the fallback).
	// The bundled steward.v1 template doesn't carry a `project_id:`
	// key, so the body field flows through unchanged.
	in := spawnIn{
		ParentID:        "", // top-level — director consented directly
		ChildHandle:     projectStewardHandle(project),
		Kind:            kind,
		HostID:          hostID,
		ProjectID:       project,
		SpawnSpec:       string(specBody),
		AutoOpenSession: true,
		PermissionMode:  permission,
	}

	out, status, err := s.DoSpawn(ctx, team, in)
	if err != nil {
		// Coalesce on race: a concurrent ensure that beat us to the
		// punch shows up in the lookup now.
		if existing, lookupErr := s.findRunningProjectSteward(ctx, team, project); lookupErr == nil && existing != "" {
			writeJSON(w, http.StatusOK, ensureProjectStewardOut{
				AgentID:    existing,
				Status:     "running",
				AlreadyRan: true,
				ProjectID:  project,
			})
			return
		}
		writeErr(w, status, fmt.Sprintf("spawn: %v", err))
		return
	}

	// Keep projects.steward_agent_id in lockstep with the live
	// steward. The W1 column on `agents` is the authority for the
	// "find this project's steward" lookup, but this field was the
	// pre-ADR pointer; updating it avoids divergence and lets
	// existing readers (steward_state, mobile project overview) keep
	// working without joining through `agents`.
	if _, perr := s.db.ExecContext(ctx,
		`UPDATE projects SET steward_agent_id = ?
		   WHERE team_id = ? AND id = ?`,
		out.AgentID, team, project); perr != nil {
		writeErr(w, http.StatusInternalServerError,
			"bind steward_agent_id: "+perr.Error())
		return
	}

	writeJSON(w, http.StatusCreated, ensureProjectStewardOut{
		AgentID:    out.AgentID,
		SpawnID:    out.SpawnID,
		Status:     out.Status,
		AlreadyRan: false,
		ProjectID:  project,
	})
}

// findRunningProjectSteward returns the agent_id of the project's
// live steward, or "" with sql.ErrNoRows if none exists. "Live"
// excludes archived/terminated rows so re-ensure after an archive
// spawns a fresh one.
func (s *Server) findRunningProjectSteward(ctx context.Context, team, project string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM agents
		 WHERE team_id   = ?
		   AND project_id = ?
		   AND kind LIKE 'steward.%'
		   AND status NOT IN ('terminated','crashed','failed')
		   AND archived_at IS NULL
		 ORDER BY created_at DESC
		 LIMIT 1`,
		team, project).Scan(&id)
	if err != nil {
		return "", err
	}
	return id, nil
}

// validateProjectInTeam confirms the project id exists and belongs
// to the named team. The handler returns a 404 on miss so cross-team
// id guesses fail closed.
func (s *Server) validateProjectInTeam(ctx context.Context, team, project string) error {
	var got string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM projects WHERE id = ? AND team_id = ?`,
		project, team).Scan(&got)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("project %s not found in team %s", project, team)
	}
	return err
}
