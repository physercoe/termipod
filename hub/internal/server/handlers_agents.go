package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/termipod/hub/internal/auth"
)

type agentIn struct {
	Handle       string          `json:"handle"`
	Kind         string          `json:"kind"`
	Backend      json.RawMessage `json:"backend,omitempty"`
	Capabilities json.RawMessage `json:"capabilities,omitempty"`
	ParentID     string          `json:"parent_agent_id,omitempty"`
	HostID       string          `json:"host_id,omitempty"`
	BudgetCents  *int            `json:"budget_cents,omitempty"`
	WorktreePath string          `json:"worktree_path,omitempty"`
	JournalPath  string          `json:"journal_path,omitempty"`
}

type agentOut struct {
	ID           string          `json:"id"`
	TeamID       string          `json:"team_id"`
	Handle       string          `json:"handle"`
	Kind         string          `json:"kind"`
	Backend      json.RawMessage `json:"backend"`
	Capabilities json.RawMessage `json:"capabilities"`
	ParentID     string          `json:"parent_agent_id,omitempty"`
	HostID       string          `json:"host_id,omitempty"`
	Status       string          `json:"status"`
	PaneID       string          `json:"pane_id,omitempty"`
	WorktreePath string          `json:"worktree_path,omitempty"`
	JournalPath  string          `json:"journal_path,omitempty"`
	BudgetCents  *int            `json:"budget_cents,omitempty"`
	SpentCents   int             `json:"spent_cents"`
	PauseState   string          `json:"pause_state"`
	IdleSince    *string         `json:"idle_since,omitempty"`
	CreatedAt    string          `json:"created_at"`
	TerminatedAt *string         `json:"terminated_at,omitempty"`
	ArchivedAt   *string         `json:"archived_at,omitempty"`
	// LastEventAt is the ts of the most recent agent_events row for this
	// agent, or nil if no events have been recorded yet. Mobile uses this
	// to classify a `running` agent as healthy / stale / stuck — a wedged
	// claude process keeps `status='running'` but stops emitting events,
	// so `(status, age(LastEventAt))` is the load-bearing liveness signal.
	LastEventAt *string `json:"last_event_at,omitempty"`
	// Mode is the resolved driving mode (M1|M2|M4). Empty for legacy
	// rows predating the resolver; host-runner interprets empty as M4.
	Mode string `json:"mode,omitempty"`
	// Populated on the single-agent GET by joining agent_spawns; omitted
	// from list-agents to keep that payload small.
	SpawnSpecYaml   string          `json:"spawn_spec_yaml,omitempty"`
	SpawnAuthority  json.RawMessage `json:"spawn_authority,omitempty"`
}

func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	var in agentIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Handle == "" || in.Kind == "" {
		writeErr(w, http.StatusBadRequest, "handle and kind required")
		return
	}
	backend := defaultRawObject(in.Backend)
	caps := defaultRawArray(in.Capabilities)
	id := NewID()
	now := NowUTC()
	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO agents (
			id, team_id, handle, kind, backend_json, capabilities_json,
			parent_agent_id, host_id, budget_cents, worktree_path, journal_path,
			status, pause_state, created_at
		) VALUES (?, ?, ?, ?, ?, ?,
		          NULLIF(?, ''), NULLIF(?, ''), ?, NULLIF(?, ''), NULLIF(?, ''),
		          'pending', 'running', ?)`,
		id, team, in.Handle, in.Kind, string(backend), string(caps),
		in.ParentID, in.HostID, nullableInt(in.BudgetCents),
		in.WorktreePath, in.JournalPath, now)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, agentOut{
		ID: id, TeamID: team, Handle: in.Handle, Kind: in.Kind,
		Backend: json.RawMessage(backend), Capabilities: json.RawMessage(caps),
		ParentID: in.ParentID, HostID: in.HostID,
		Status: "pending", PauseState: "running",
		WorktreePath: in.WorktreePath, JournalPath: in.JournalPath,
		BudgetCents: in.BudgetCents, CreatedAt: now,
	})
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	// last_event_at: correlated MAX(ts) over agent_events. The
	// (agent_id, ts) index already exists, so this stays cheap on rows
	// with hundreds of events; we accept the per-row scalar subquery
	// rather than a GROUP BY join because the agents list is bounded
	// (one steward + a few children) at MVP scale.
	q := `
		SELECT id, team_id, handle, kind, backend_json, capabilities_json,
		       COALESCE(parent_agent_id, ''), COALESCE(host_id, ''),
		       status, COALESCE(pane_id, ''),
		       COALESCE(worktree_path, ''), COALESCE(journal_path, ''),
		       budget_cents, spent_cents, pause_state, idle_since,
		       created_at, terminated_at, COALESCE(driving_mode, ''),
		       archived_at,
		       (SELECT MAX(ts) FROM agent_events WHERE agent_id = agents.id)
		FROM agents WHERE team_id = ?`
	args := []any{team}
	if host := r.URL.Query().Get("host_id"); host != "" {
		q += " AND host_id = ?"
		args = append(args, host)
	}
	if st := r.URL.Query().Get("status"); st != "" {
		q += " AND status = ?"
		args = append(args, st)
	}
	// Archived rows stay in the DB for audit/history resolution but drop
	// out of the default list. Pass ?include_archived=1 to see them
	// (mobile uses this when the operator taps "Show archived").
	inc := r.URL.Query().Get("include_archived")
	if inc != "1" && inc != "true" {
		q += " AND archived_at IS NULL"
	}
	q += " ORDER BY created_at"
	rows, err := s.db.QueryContext(r.Context(), q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []agentOut{}
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, a)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "agent")
	row := s.db.QueryRowContext(r.Context(), `
		SELECT id, team_id, handle, kind, backend_json, capabilities_json,
		       COALESCE(parent_agent_id, ''), COALESCE(host_id, ''),
		       status, COALESCE(pane_id, ''),
		       COALESCE(worktree_path, ''), COALESCE(journal_path, ''),
		       budget_cents, spent_cents, pause_state, idle_since,
		       created_at, terminated_at, COALESCE(driving_mode, ''),
		       archived_at,
		       (SELECT MAX(ts) FROM agent_events WHERE agent_id = agents.id)
		FROM agents WHERE team_id = ? AND id = ?`, team, id)
	a, err := scanAgent(row)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "agent not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Pull the original spawn spec so the UI can show and reuse it. Agents
	// created outside the spawn flow (e.g. via handleCreateAgent) have no
	// row here — in that case we just leave the fields empty.
	var (
		specYaml  sql.NullString
		authority sql.NullString
	)
	if qerr := s.db.QueryRowContext(r.Context(), `
		SELECT spawn_spec_yaml, spawn_authority_json
		  FROM agent_spawns
		 WHERE child_agent_id = ?
		 ORDER BY spawned_at DESC LIMIT 1`, id).
		Scan(&specYaml, &authority); qerr == nil {
		if specYaml.Valid {
			a.SpawnSpecYaml = specYaml.String
		}
		if authority.Valid && authority.String != "" {
			a.SpawnAuthority = json.RawMessage(authority.String)
		}
	}
	writeJSON(w, http.StatusOK, a)
}

type agentPatchIn struct {
	Status     *string `json:"status,omitempty"`
	PauseState *string `json:"pause_state,omitempty"`
	PaneID     *string `json:"pane_id,omitempty"`
}

// handlePatchAgent lets host-runners / steward update lifecycle fields
// as the backing process moves through its states.
func (s *Server) handlePatchAgent(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "agent")
	var in agentPatchIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	sets, args := []string{}, []any{}
	if in.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, *in.Status)
		// Stamp terminated_at for any terminal state so the row's
		// lifecycle is self-describing without reading history.
		if *in.Status == "terminated" || *in.Status == "crashed" || *in.Status == "failed" {
			sets = append(sets, "terminated_at = ?")
			args = append(args, NowUTC())
		}
	}
	if in.PauseState != nil {
		sets = append(sets, "pause_state = ?")
		args = append(args, *in.PauseState)
	}
	if in.PaneID != nil {
		sets = append(sets, "pane_id = NULLIF(?, '')")
		args = append(args, *in.PaneID)
	}
	if len(sets) == 0 {
		writeErr(w, http.StatusBadRequest, "no fields to update")
		return
	}
	args = append(args, team, id)
	q := "UPDATE agents SET " + joinComma(sets) + " WHERE team_id = ? AND id = ?"
	res, err := s.db.ExecContext(r.Context(), q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "agent not found")
		return
	}
	// A status=terminated PATCH from the UI marks the row, but without
	// also enqueuing a host-side kill the pane stays alive. Mirror the
	// MCP shutdown_self path so both entrypoints converge on the same
	// host-runner cleanup.
	if in.Status != nil && *in.Status == "terminated" {
		var hostID, paneID sql.NullString
		var handle string
		qerr := s.db.QueryRowContext(r.Context(),
			`SELECT host_id, pane_id, handle FROM agents WHERE team_id = ? AND id = ?`,
			team, id).Scan(&hostID, &paneID, &handle)
		if qerr == nil && hostID.Valid && hostID.String != "" {
			_, _ = s.enqueueHostCommand(r.Context(), hostID.String, id,
				"terminate", map[string]any{"pane_id": paneID.String})
		}
		s.recordAudit(r.Context(), team, "agent.terminate", "agent", id,
			"terminate "+handle, map[string]any{"handle": handle})
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleArchiveAgent soft-deletes a terminated/failed/crashed agent so
// it drops off the live list. The row stays in the DB to keep audit
// events and spawn history resolvable. Refuses to archive a live agent —
// operators must terminate it first.
func (s *Server) handleArchiveAgent(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "agent")
	var status, handle string
	var archived sql.NullString
	err := s.db.QueryRowContext(r.Context(),
		`SELECT status, handle, archived_at FROM agents WHERE team_id = ? AND id = ?`,
		team, id).Scan(&status, &handle, &archived)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "agent not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if archived.Valid {
		writeErr(w, http.StatusConflict, "already archived")
		return
	}
	if status != "terminated" && status != "failed" && status != "crashed" {
		writeErr(w, http.StatusConflict, "terminate the agent before archiving")
		return
	}
	now := NowUTC()
	if _, err := s.db.ExecContext(r.Context(),
		`UPDATE agents SET archived_at = ? WHERE team_id = ? AND id = ?`,
		now, team, id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAudit(r.Context(), team, "agent.archive", "agent", id,
		"archive "+handle,
		map[string]any{"handle": handle, "status": status},
	)
	w.WriteHeader(http.StatusNoContent)
}

type spawnIn struct {
	ParentID     string          `json:"parent_agent_id,omitempty"`
	ChildHandle  string          `json:"child_handle"`
	Kind         string          `json:"kind"`
	HostID       string          `json:"host_id,omitempty"`
	SpawnSpec    string          `json:"spawn_spec_yaml"`
	Authority    json.RawMessage `json:"spawn_authority,omitempty"`
	Task         json.RawMessage `json:"task,omitempty"`
	WorktreePath string          `json:"worktree_path,omitempty"`
	BudgetCents  *int            `json:"budget_cents,omitempty"`
	// Mode is an optional override of the template's driving_mode.
	// When set it's strict — the resolver tries only this candidate,
	// no fallback. Empty means "use template + fallbacks".
	Mode string `json:"mode,omitempty"`
	// PersonaSeed is a free-form addendum the user types into the
	// mobile bootstrap sheet. The hub appends it to the rendered
	// CLAUDE.md as a "Persona override" section so the agent sees
	// both the template body and the user's customization on first
	// turn. Empty means no override.
	PersonaSeed string `json:"persona_seed,omitempty"`
	// PermissionMode controls how claude (or any backend that templates
	// against {{permission_flag}}) handles tool-call approval. Recognised
	// values:
	//   - "skip"   → expand to `--dangerously-skip-permissions` (auto-allow,
	//                matches a local `claude` session on a PC).
	//   - "prompt" → expand to `--permission-prompt-tool mcp__termipod__
	//                permission_prompt` (route every tool call through the
	//                hub MCP gateway → attention_items). Only useful once
	//                that MCP tool is registered.
	//   - ""       → empty expansion, falls through to whatever default
	//                claude does in stream-json --print mode.
	// The mobile bootstrap sheet defaults to "skip" so the demo flow
	// works without W2 plumbing.
	PermissionMode string `json:"permission_mode,omitempty"`
}

type spawnOut struct {
	SpawnID     string `json:"spawn_id,omitempty"`
	AgentID     string `json:"agent_id,omitempty"`
	SpawnedAt   string `json:"spawned_at,omitempty"`
	Status      string `json:"status,omitempty"`          // "spawned" | "pending_approval"
	AttentionID string `json:"attention_id,omitempty"`    // set when gated on approval
	Tier        string `json:"tier,omitempty"`
	// Mode is the concrete driving mode the resolver picked (M1|M2|M4).
	// Empty when no mode info was declared anywhere — host-runner stays
	// on its default M4 in that case.
	Mode string `json:"mode,omitempty"`
}

// handleSpawn creates a pending agent + an agent_spawns audit row, unless
// policy gates the action behind an approval_request attention. In the
// gated case we return 202 + attention_id and the actual spawn happens on
// approve.
func (s *Server) handleSpawn(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	var in spawnIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if s.policy != nil {
		tier := s.policy.Decide("spawn")
		if tier != "" && tier != TierAuto {
			approvers := s.policy.ApproversFor(tier)
			if len(approvers) > 0 {
				attID, err := s.createSpawnApproval(r.Context(), team, tier, approvers, in)
				if err != nil {
					writeErr(w, http.StatusInternalServerError, err.Error())
					return
				}
				writeJSON(w, http.StatusAccepted, spawnOut{
					Status:      "pending_approval",
					AttentionID: attID,
					Tier:        tier,
				})
				return
			}
		}
	}
	out, status, err := s.DoSpawn(r.Context(), team, in)
	if err != nil {
		writeErr(w, status, err.Error())
		return
	}
	out.Status = "spawned"
	s.recordAudit(r.Context(), team, "agent.spawn", "agent", out.AgentID,
		"spawn "+in.ChildHandle+" ("+in.Kind+")",
		map[string]any{"handle": in.ChildHandle, "kind": in.Kind, "host_id": in.HostID},
	)
	writeJSON(w, status, out)
}

// createSpawnApproval inserts an attention_items row tagged with tier and a
// pending_payload_json holding the spawnIn, ready for the approval-decide
// handler to dispatch once quorum is reached.
func (s *Server) createSpawnApproval(ctx context.Context, team, tier string, approvers []string, in spawnIn) (string, error) {
	id := NewID()
	payload, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	assignees, _ := json.Marshal(approvers)
	severity := "major"
	if tier == TierCritical {
		severity = "critical"
	}
	summary := "spawn " + in.ChildHandle + " (" + in.Kind + ")"
	_, actorKind, actorHandle := actorFromContext(ctx)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, tier,
			current_assignees_json, pending_payload_json,
			status, created_at,
			actor_kind, actor_handle
		) VALUES (?, NULL, 'team', ?, 'approval_request',
		          ?, ?, ?,
		          ?, ?,
		          'open', ?,
		          ?, ?)`,
		id, team, summary, severity, tier,
		string(assignees), string(payload), NowUTC(),
		actorKind, nullIfEmpty(actorHandle))
	return id, err
}

// DoSpawn is the reusable core called by both the HTTP handler and the
// scheduler. It creates the agent row + audit row transactionally.
// Returns (out, httpStatus, err); on err the httpStatus is the suggested
// status for HTTP callers, ignored by internal callers.
func (s *Server) DoSpawn(ctx context.Context, team string, in spawnIn) (spawnOut, int, error) {
	if in.ChildHandle == "" || in.Kind == "" || in.SpawnSpec == "" {
		return spawnOut{}, http.StatusBadRequest,
			errors.New("child_handle, kind, spawn_spec_yaml required")
	}

	// Expand template vars ({{handle}}, {{journal}}, {{principal}}, …) before
	// persisting. Render failures are treated as client errors — a malformed
	// placeholder means the spec can never spawn usefully, better to reject.
	principal := "@principal"
	if tok, ok := auth.FromContext(ctx); ok {
		principal = principalFromScope(tok.ScopeJSON)
	}
	rendered, err := s.renderSpawnSpec(ctx, team, in, principal)
	if err != nil {
		return spawnOut{}, http.StatusBadRequest, err
	}
	// Inline the agent's CLAUDE.md (resolved from the template's `prompt:`
	// field). The host-runner launcher writes context_files entries into
	// the workdir before spawn so Claude Code sees the persona on
	// startup. Failures here are a config bug — a template that names a
	// missing prompt should not silently spawn a contextless agent.
	vars, err := s.buildSpawnVars(ctx, team, in, principal)
	if err != nil {
		return spawnOut{}, http.StatusBadRequest, err
	}
	rendered, err = s.resolveContextFiles(rendered, vars, in.PersonaSeed)
	if err != nil {
		return spawnOut{}, http.StatusBadRequest, err
	}
	in.SpawnSpec = rendered

	// Resolve the driving mode before we open the tx so a 400 exits
	// without a rollback. Empty mode is legal (opt-in): DoSpawn stores
	// NULL and host-runner defaults to M4 at launch time.
	mode, err := s.resolveSpawnMode(ctx, in)
	if err != nil {
		return spawnOut{}, http.StatusBadRequest, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return spawnOut{}, http.StatusInternalServerError, err
	}
	defer tx.Rollback()

	agentID := NewID()
	spawnID := NewID()
	now := NowUTC()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agents (
			id, team_id, handle, kind, backend_json, capabilities_json,
			parent_agent_id, host_id, budget_cents, worktree_path,
			driving_mode,
			status, pause_state, created_at
		) VALUES (?, ?, ?, ?, '{}', '[]',
		          NULLIF(?, ''), NULLIF(?, ''), ?, NULLIF(?, ''),
		          NULLIF(?, ''),
		          'pending', 'running', ?)`,
		agentID, team, in.ChildHandle, in.Kind,
		in.ParentID, in.HostID, nullableInt(in.BudgetCents),
		in.WorktreePath, mode, now); err != nil {
		return spawnOut{}, http.StatusConflict, err
	}

	// Mint the agent's MCP bearer token. scope_json carries team + agent_id
	// so the existing /mcp/{token} resolver (resolveMCPToken) routes the
	// session to this agent without further lookup. Plaintext is stashed
	// on agent_spawns; host-runner reads it once at launch to materialize
	// .mcp.json and never asks again. auth_tokens stores the hash only,
	// so the plaintext is unrecoverable via that table alone. We insert
	// inside the spawn tx so a rollback also unwinds the token (no orphan
	// rows if e.g. agent_spawns INSERT fails).
	mcpTokenPlaintext := auth.NewToken()
	mcpTokenID := NewID()
	mcpScopeJSON, _ := json.Marshal(map[string]any{
		"team":     team,
		"role":     "agent",
		"agent_id": agentID,
		"handle":   in.ChildHandle,
	})
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO auth_tokens (id, kind, token_hash, scope_json, created_at)
		VALUES (?, 'agent', ?, ?, ?)`,
		mcpTokenID, auth.HashToken(mcpTokenPlaintext),
		string(mcpScopeJSON), now); err != nil {
		return spawnOut{}, http.StatusInternalServerError, err
	}

	authority := defaultRawObject(in.Authority)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_spawns (
			id, parent_agent_id, child_agent_id, spawn_spec_yaml,
			spawn_authority_json, task_json, spawned_at, worktree_path,
			mcp_token_plaintext
		) VALUES (?, NULLIF(?, ''), ?, ?, ?, ?, ?, NULLIF(?, ''), ?)`,
		spawnID, in.ParentID, agentID, in.SpawnSpec,
		string(authority), nullBytes(in.Task), now, in.WorktreePath,
		mcpTokenPlaintext); err != nil {
		return spawnOut{}, http.StatusInternalServerError, err
	}

	if err := tx.Commit(); err != nil {
		return spawnOut{}, http.StatusInternalServerError, err
	}
	return spawnOut{SpawnID: spawnID, AgentID: agentID, SpawnedAt: now, Mode: mode}, http.StatusCreated, nil
}

type spawnListOut struct {
	SpawnID      string          `json:"spawn_id"`
	ParentID     string          `json:"parent_agent_id,omitempty"`
	ChildID      string          `json:"child_agent_id"`
	Handle       string          `json:"handle"`
	Kind         string          `json:"kind"`
	HostID       string          `json:"host_id,omitempty"`
	Status       string          `json:"status"`
	SpawnSpec    string          `json:"spawn_spec_yaml"`
	Authority    json.RawMessage `json:"spawn_authority"`
	Task         json.RawMessage `json:"task,omitempty"`
	WorktreePath string          `json:"worktree_path,omitempty"`
	SpawnedAt    string          `json:"spawned_at"`
	// Mode is the resolved driving mode; empty if no mode was declared.
	Mode string `json:"mode,omitempty"`
	// McpToken is the plaintext bearer the spawned agent uses to call
	// /mcp/{token} on the hub. Surfaced to host-runner only — this
	// endpoint requires a host-kind auth token. Treated as a per-agent
	// secret: host-runner writes it into the agent's local .mcp.json
	// and never exposes it to mobile/principal clients. Empty for
	// pre-W2.2 spawns that predate the column.
	McpToken string `json:"mcp_token,omitempty"`
}

// handleListSpawns returns agent_spawns rows, filtered by host and/or status.
// Primary caller is the host-runner polling for agents pending launch on its box.
func (s *Server) handleListSpawns(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	host := r.URL.Query().Get("host_id")
	status := r.URL.Query().Get("status") // e.g. "pending"

	q := `
		SELECT sp.id, COALESCE(sp.parent_agent_id, ''), sp.child_agent_id,
		       a.handle, a.kind, COALESCE(a.host_id, ''), a.status,
		       sp.spawn_spec_yaml, sp.spawn_authority_json, sp.task_json,
		       COALESCE(sp.worktree_path, ''), sp.spawned_at,
		       COALESCE(a.driving_mode, ''),
		       COALESCE(sp.mcp_token_plaintext, '')
		FROM agent_spawns sp
		JOIN agents a ON a.id = sp.child_agent_id
		WHERE a.team_id = ?`
	args := []any{team}
	if host != "" {
		q += " AND a.host_id = ?"
		args = append(args, host)
	}
	if status != "" {
		q += " AND a.status = ?"
		args = append(args, status)
	}
	q += " ORDER BY sp.spawned_at DESC LIMIT 200"

	rows, err := s.db.QueryContext(r.Context(), q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	// Only host-kind callers (the host-runner) may see the plaintext MCP
	// bearer; user-kind dashboard callers must not be able to read agent
	// secrets via the spawn-list endpoint.
	revealMcpToken := false
	if tok, ok := auth.FromContext(r.Context()); ok && tok.Kind == "host" {
		revealMcpToken = true
	}
	out := []spawnListOut{}
	for rows.Next() {
		var sp spawnListOut
		var authority string
		var task sql.NullString
		if err := rows.Scan(&sp.SpawnID, &sp.ParentID, &sp.ChildID,
			&sp.Handle, &sp.Kind, &sp.HostID, &sp.Status,
			&sp.SpawnSpec, &authority, &task,
			&sp.WorktreePath, &sp.SpawnedAt, &sp.Mode,
			&sp.McpToken); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		sp.Authority = json.RawMessage(authority)
		if task.Valid {
			sp.Task = json.RawMessage(task.String)
		}
		if !revealMcpToken {
			sp.McpToken = ""
		}
		out = append(out, sp)
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- helpers ----

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAgent(r rowScanner) (agentOut, error) {
	var (
		a                                   agentOut
		backend, caps                       string
		budget                              sql.NullInt64
		idleSince, termAt, archAt, lastEvAt sql.NullString
	)
	if err := r.Scan(&a.ID, &a.TeamID, &a.Handle, &a.Kind, &backend, &caps,
		&a.ParentID, &a.HostID, &a.Status, &a.PaneID,
		&a.WorktreePath, &a.JournalPath,
		&budget, &a.SpentCents, &a.PauseState, &idleSince,
		&a.CreatedAt, &termAt, &a.Mode, &archAt, &lastEvAt); err != nil {
		return a, err
	}
	a.Backend = json.RawMessage(backend)
	a.Capabilities = json.RawMessage(caps)
	if budget.Valid {
		b := int(budget.Int64)
		a.BudgetCents = &b
	}
	if idleSince.Valid {
		a.IdleSince = &idleSince.String
	}
	if termAt.Valid {
		a.TerminatedAt = &termAt.String
	}
	if archAt.Valid {
		a.ArchivedAt = &archAt.String
	}
	if lastEvAt.Valid {
		a.LastEventAt = &lastEvAt.String
	}
	return a, nil
}

func defaultRawObject(b json.RawMessage) []byte {
	if len(b) == 0 {
		return []byte("{}")
	}
	return b
}

func defaultRawArray(b json.RawMessage) []byte {
	if len(b) == 0 {
		return []byte("[]")
	}
	return b
}

func nullableInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func joinComma(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out
}
