// handlers_project_start.go — ADR-046 / WS4 explicit project Start.
//
// In the inline-spec model a project is created bound to a domain steward
// (projects.on_create_template_id) but the steward is NOT spawned at create.
// The director reviews / edits the materialized project, then taps Start —
// THIS handler — which spawns the bound steward. Separating bind from spawn
// is the whole point: create is cheap and reviewable; Start is the deliberate
// "begin work" gesture that puts a live agent on a host.
//
// Structurally this mirrors handleEnsureProjectSteward (ADR-025 W3) but sources
// the steward kind from the project's binding rather than a request override,
// and treats an already-running steward as a conflict (409) rather than a
// silent coalesce — Start is meant to be pressed once.

package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// startProjectIn is the optional request body. host_id pins the host the
// steward spawns on (the mobile host-picker sheet); empty auto-picks.
// permission_mode chooses the template's permission flags (skip / prompt;
// empty = the bootstrap default "skip").
type startProjectIn struct {
	HostID         string `json:"host_id,omitempty"`
	PermissionMode string `json:"permission_mode,omitempty"`
}

// startProjectOut reports the spawned (or already-running) steward. Shape
// matches ensureProjectStewardOut so mobile can share rendering.
type startProjectOut struct {
	AgentID    string `json:"agent_id"`
	SpawnID    string `json:"spawn_id,omitempty"`
	Status     string `json:"status"`
	AlreadyRan bool   `json:"already_running"`
	ProjectID  string `json:"project_id"`
}

// handleStartProject spawns the project's bound domain steward. Director-auth
// only. Idempotent in the sense that a second call while a steward is already
// running returns 409 with the live agent id rather than spawning a second.
func (s *Server) handleStartProject(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	project := chi.URLParam(r, "project")
	ctx := r.Context()

	// Resolve the bound steward kind + ensure the project exists in-team.
	var boundKind sql.NullString
	var status string
	err := s.db.QueryRowContext(ctx,
		`SELECT on_create_template_id, status FROM projects
		  WHERE id = ? AND team_id = ?`, project, team).Scan(&boundKind, &status)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if status == "archived" {
		writeErr(w, http.StatusConflict, "project is archived")
		return
	}
	boundID := boundKind.String
	if boundID == "" {
		writeErr(w, http.StatusUnprocessableEntity,
			"project has no bound steward (on_create_template_id) to start")
		return
	}
	// The bound id is a template id (e.g. `agents.steward.code-migration`).
	// Derive the agent `kind` the steward row carries — it must start with
	// `steward.` so findRunningProjectSteward (kind LIKE 'steward.%') can find
	// it on the next ensure/Start. Strip the conventional `agents.` template
	// prefix; ids already in `steward.*` form pass through unchanged.
	kind := stewardKindFromTemplateID(boundID)

	// Already running → conflict, with the live steward id so the caller can
	// jump straight to it.
	if existing, lerr := s.findRunningProjectSteward(ctx, team, project); lerr == nil && existing != "" {
		writeJSON(w, http.StatusConflict, startProjectOut{
			AgentID:    existing,
			Status:     "running",
			AlreadyRan: true,
			ProjectID:  project,
		})
		return
	} else if lerr != nil && !errors.Is(lerr, sql.ErrNoRows) {
		writeErr(w, http.StatusInternalServerError, "steward lookup: "+lerr.Error())
		return
	}

	var body startProjectIn
	_ = json.NewDecoder(r.Body).Decode(&body)

	// Resolve a host: explicit body field wins; otherwise auto-pick.
	hostID := body.HostID
	if hostID != "" {
		if verr := s.validateTeamHost(ctx, team, hostID); verr != nil {
			writeErr(w, http.StatusBadRequest, "host_id: "+verr.Error())
			return
		}
	} else {
		var perr error
		hostID, perr = s.pickFirstHost(ctx, team)
		if perr != nil {
			writeErr(w, http.StatusFailedDependency, "no host available: "+perr.Error())
			return
		}
	}

	// Resolve the bound steward template by its id (matches the `template:`
	// field, same indirection the spawn path uses) rather than by filename —
	// on_create_template_id stores the id form, not a file stem.
	specBody, err := s.readAgentTemplate(team, boundID)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity,
			"bound steward template "+boundID+" not found: "+err.Error())
		return
	}
	permission := body.PermissionMode
	if permission == "" {
		permission = "skip"
	}

	in := spawnIn{
		ChildHandle:     projectStewardHandle(project),
		Kind:            kind,
		HostID:          hostID,
		ProjectID:       project,
		SpawnSpec:       specBody,
		AutoOpenSession: true,
		PermissionMode:  permission,
	}
	out, spawnStatus, err := s.DoSpawn(ctx, team, in)
	if err != nil {
		// Coalesce on race: a concurrent Start (or ensure) that beat us shows
		// up in the lookup now.
		if existing, lerr := s.findRunningProjectSteward(ctx, team, project); lerr == nil && existing != "" {
			writeJSON(w, http.StatusConflict, startProjectOut{
				AgentID:    existing,
				Status:     "running",
				AlreadyRan: true,
				ProjectID:  project,
			})
			return
		}
		writeErr(w, spawnStatus, fmt.Sprintf("start spawn: %v", err))
		return
	}

	// Keep projects.steward_agent_id in lockstep with the live steward (same
	// invariant handleEnsureProjectSteward maintains).
	if _, perr := s.writeDB.ExecContext(ctx,
		`UPDATE projects SET steward_agent_id = ? WHERE team_id = ? AND id = ?`,
		out.AgentID, team, project); perr != nil {
		writeErr(w, http.StatusInternalServerError, "bind steward_agent_id: "+perr.Error())
		return
	}

	s.recordAudit(ctx, team, "project.started", "project", project,
		"start project — spawned bound steward "+kind,
		map[string]any{"agent_id": out.AgentID, "steward_kind": kind, "host_id": hostID})

	writeJSON(w, http.StatusCreated, startProjectOut{
		AgentID:    out.AgentID,
		SpawnID:    out.SpawnID,
		Status:     out.Status,
		AlreadyRan: false,
		ProjectID:  project,
	})
}

// stewardKindFromTemplateID maps a bound steward template id to the agent
// `kind` the spawned row should carry. The convention is `agents.steward.<x>`
// for the template id and `steward.<x>` for the agent kind (matching the
// bundled `steward.v1` default and the kind LIKE 'steward.%' predicate
// findRunningProjectSteward uses). Ids already in `steward.*` form pass
// through; an id that is neither shape is returned unchanged (it still
// spawns, just outside the project-steward lookup — an authoring error the
// caller surfaces elsewhere).
func stewardKindFromTemplateID(id string) string {
	if stripped, ok := strings.CutPrefix(id, "agents."); ok {
		return stripped
	}
	return id
}
