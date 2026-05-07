// handlers_general_steward.go — singleton spawn for the team's
// general steward (W4 of the lifecycle wedge plan; ADR-001 D-amend-2).
//
// The general steward is persistent and team-scoped: one per team,
// always-on, archived only by manual director action. This handler
// is the idempotent ensure-spawn entry point. It's distinct from the
// regular /agents/spawn endpoint because:
//
//   - it has no parent (top-level concierge);
//   - it's a singleton — concurrent or repeat calls coalesce on the
//     first running instance rather than producing duplicates;
//   - the director's mobile UI calls it on home-tab open (W3) so
//     the steward exists by the time the director taps the card.
//
// On first call: spawns a fresh `steward.general.v1` agent on the
// team's first known host (operator picks the binding for now;
// multi-host topology is post-MVP). Subsequent calls return the
// existing running instance.

package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"

	hub "github.com/termipod/hub"
)

// generalStewardKind is the canonical agent kind for the persistent
// team-scoped concierge. Matches the bundled template's `template:`
// id once normalised — see ADR-001 D-amend-2 + research-demo-
// lifecycle.md §4 D2.
const generalStewardKind = "steward.general.v1"

// generalStewardHandle is the canonical handle within a team. Per
// ADR-016 and the agent_kind → role mapping in roles.yaml, anything
// starting with "steward." resolves to role=steward; we still pin
// the handle here so cross-team references (e.g. logs, audit) can
// rely on a stable string.
const generalStewardHandle = "@steward"

// ensureGeneralStewardIn is the optional request body. host_id pins the
// spawn to a specific team-host (e.g. when the team has multiple hosts
// and the principal wants to bind the steward to one of them via the
// mobile picker). Empty body keeps the legacy auto-pick semantics —
// the most recently registered team-host wins.
type ensureGeneralStewardIn struct {
	HostID string `json:"host_id,omitempty"`
}

// ensureGeneralStewardOut is the response shape — same envelope as
// /agents/spawn, plus a flag indicating whether we actually spawned
// or found an existing instance. Callers (mobile, tests) use the
// flag to decide whether to greet the agent or just open the feed.
type ensureGeneralStewardOut struct {
	AgentID    string `json:"agent_id"`
	SpawnID    string `json:"spawn_id,omitempty"`
	Status     string `json:"status"`
	AlreadyRan bool   `json:"already_running"`
}

// handleEnsureGeneralSteward returns the team's running general
// steward, spawning one if none exists. The handler is read-side
// for the common case (subsequent calls hit only the lookup) and
// write-side (full spawn path) only on the first interaction with
// a team.
func (s *Server) handleEnsureGeneralSteward(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	ctx := r.Context()

	// Optional body: caller may pin the spawn to a specific host.
	// We tolerate an empty body (legacy callers POST `{}`) — the
	// decoder leaves HostID="" and we fall through to pickFirstHost.
	var body ensureGeneralStewardIn
	_ = json.NewDecoder(r.Body).Decode(&body)

	// Fast path: an existing running instance.
	if existing, err := s.findRunningGeneralSteward(ctx, team); err == nil && existing != "" {
		writeJSON(w, http.StatusOK, ensureGeneralStewardOut{
			AgentID:    existing,
			Status:     "running",
			AlreadyRan: true,
		})
		return
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusInternalServerError, "lookup: "+err.Error())
		return
	}

	// Pick a host for the spawn. If the caller named one, validate it
	// belongs to this team; otherwise auto-pick the most-recently
	// registered host for backwards compatibility.
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

	// Load the bundled steward.general.v1 template body — disk
	// overlay first (in case an operator has customised one), then
	// embedded FS as the source of truth. Note: the manager/IC
	// invariant says the steward.general kind isn't editable by the
	// general steward itself (ADR-016 D7); operator-side edits via
	// the REST surface are still allowed because the director has
	// god-mode in concierge mode (W3 mobile editor).
	specBody, err := s.loadBuiltinAgentTemplate(generalStewardKind + ".yaml")
	if err != nil {
		writeErr(w, http.StatusInternalServerError,
			"load steward.general.v1 template: "+err.Error())
		return
	}

	// Compose the spawn input. No parent (general steward is
	// top-level), no worktree (concierge work happens in
	// ~/hub-work/general per the template's default_workdir),
	// auto-open a session so the director sees a chat surface
	// immediately on home-tab open.
	in := spawnIn{
		ParentID:        "", // top-level
		ChildHandle:     generalStewardHandle,
		Kind:            generalStewardKind,
		HostID:          hostID,
		SpawnSpec:       string(specBody),
		AutoOpenSession: true,
		PermissionMode:  "skip", // matches MVP demo flow
	}

	out, status, err := s.DoSpawn(ctx, team, in)
	if err != nil {
		// Coalesce on race: if a concurrent call already spawned an
		// instance between our findRunningGeneralSteward check and
		// the DoSpawn attempt, the unique-handle constraint trips
		// and we re-do the lookup. The second-attempted request
		// returns the first's instance.
		if existing, lookupErr := s.findRunningGeneralSteward(ctx, team); lookupErr == nil && existing != "" {
			writeJSON(w, http.StatusOK, ensureGeneralStewardOut{
				AgentID:    existing,
				Status:     "running",
				AlreadyRan: true,
			})
			return
		}
		writeErr(w, status, fmt.Sprintf("spawn: %v", err))
		return
	}

	writeJSON(w, http.StatusCreated, ensureGeneralStewardOut{
		AgentID:    out.AgentID,
		SpawnID:    out.SpawnID,
		Status:     out.Status,
		AlreadyRan: false,
	})
}

// findRunningGeneralSteward returns the agent_id of the team's
// running general steward, or "" with sql.ErrNoRows if none exists.
// "Running" excludes archived/terminated instances — a director who
// archives the steward and reopens the home-tab card respawns a
// fresh one.
func (s *Server) findRunningGeneralSteward(ctx context.Context, team string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM agents
		WHERE team_id = ? AND kind = ?
		  AND status NOT IN ('terminated','crashed','failed')
		  AND terminated_at IS NULL
		ORDER BY created_at DESC
		LIMIT 1`,
		team, generalStewardKind).Scan(&id)
	if err != nil {
		return "", err
	}
	return id, nil
}

// validateTeamHost confirms the host id exists and belongs to the
// named team. Returns sql.ErrNoRows-shaped failure as a 400-ready
// message rather than letting the FK trip later in DoSpawn — the
// principal asked for this host explicitly so a clear error beats a
// generic spawn-failed.
func (s *Server) validateTeamHost(ctx context.Context, team, hostID string) error {
	var got string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM hosts WHERE id = ? AND team_id = ?`,
		hostID, team).Scan(&got)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("host %s not found in team %s", hostID, team)
	}
	return err
}

// pickFirstHost returns the most-recently-registered host for the
// team. MVP single-host bias; multi-host scheduling for the general
// steward is a post-MVP wedge. If no host exists, we surface a
// dependency error — the caller should install a host-runner first.
func (s *Server) pickFirstHost(ctx context.Context, team string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM hosts
		WHERE team_id = ?
		ORDER BY created_at DESC
		LIMIT 1`,
		team).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("no hosts registered for team %s", team)
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

// loadBuiltinAgentTemplate returns the YAML body for an agent
// template by file name (e.g. "steward.general.v1.yaml"). Tries the
// team's overlay path first; falls back to the embedded FS so
// freshly-installed hubs that haven't run writeBuiltinTemplates
// yet still resolve. Same precedence as handleGetTemplate.
func (s *Server) loadBuiltinAgentTemplate(name string) ([]byte, error) {
	if !safeTemplateName(name) {
		return nil, fmt.Errorf("unsafe template name %q", name)
	}
	overlayPath := filepath.Join(s.cfg.DataRoot, "team", "templates", "agents", name)
	if b, err := os.ReadFile(overlayPath); err == nil {
		return b, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	embedded, err := fs.ReadFile(hub.TemplatesFS, "templates/agents/"+name)
	if err != nil {
		return nil, fmt.Errorf("template %q not found on disk or in embed: %w", name, err)
	}
	return embedded, nil
}

// _ keeps imports tidy; the json import is used by the response
// shape encoder writeJSON.
var _ = json.Marshal
