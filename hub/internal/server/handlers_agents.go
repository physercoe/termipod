package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
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
	q := `
		SELECT id, team_id, handle, kind, backend_json, capabilities_json,
		       COALESCE(parent_agent_id, ''), COALESCE(host_id, ''),
		       status, COALESCE(pane_id, ''),
		       COALESCE(worktree_path, ''), COALESCE(journal_path, ''),
		       budget_cents, spent_cents, pause_state, idle_since,
		       created_at, terminated_at
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
		       created_at, terminated_at
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
	writeJSON(w, http.StatusOK, a)
}

type agentPatchIn struct {
	Status     *string `json:"status,omitempty"`
	PauseState *string `json:"pause_state,omitempty"`
	PaneID     *string `json:"pane_id,omitempty"`
}

// handlePatchAgent lets host-agents / steward update lifecycle fields
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
		if *in.Status == "terminated" {
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
}

type spawnOut struct {
	SpawnID     string `json:"spawn_id,omitempty"`
	AgentID     string `json:"agent_id,omitempty"`
	SpawnedAt   string `json:"spawned_at,omitempty"`
	Status      string `json:"status,omitempty"`          // "spawned" | "pending_approval"
	AttentionID string `json:"attention_id,omitempty"`    // set when gated on approval
	Tier        string `json:"tier,omitempty"`
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
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, tier,
			current_assignees_json, pending_payload_json,
			status, created_at
		) VALUES (?, NULL, 'team', ?, 'approval_request',
		          ?, ?, ?,
		          ?, ?,
		          'open', ?)`,
		id, team, summary, severity, tier,
		string(assignees), string(payload), NowUTC())
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
			status, pause_state, created_at
		) VALUES (?, ?, ?, ?, '{}', '[]',
		          NULLIF(?, ''), NULLIF(?, ''), ?, NULLIF(?, ''),
		          'pending', 'running', ?)`,
		agentID, team, in.ChildHandle, in.Kind,
		in.ParentID, in.HostID, nullableInt(in.BudgetCents),
		in.WorktreePath, now); err != nil {
		return spawnOut{}, http.StatusConflict, err
	}

	authority := defaultRawObject(in.Authority)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_spawns (
			id, parent_agent_id, child_agent_id, spawn_spec_yaml,
			spawn_authority_json, task_json, spawned_at, worktree_path
		) VALUES (?, NULLIF(?, ''), ?, ?, ?, ?, ?, NULLIF(?, ''))`,
		spawnID, in.ParentID, agentID, in.SpawnSpec,
		string(authority), nullBytes(in.Task), now, in.WorktreePath); err != nil {
		return spawnOut{}, http.StatusInternalServerError, err
	}

	if err := tx.Commit(); err != nil {
		return spawnOut{}, http.StatusInternalServerError, err
	}
	return spawnOut{SpawnID: spawnID, AgentID: agentID, SpawnedAt: now}, http.StatusCreated, nil
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
}

// handleListSpawns returns agent_spawns rows, filtered by host and/or status.
// Primary caller is the host-agent polling for agents pending launch on its box.
func (s *Server) handleListSpawns(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	host := r.URL.Query().Get("host_id")
	status := r.URL.Query().Get("status") // e.g. "pending"

	q := `
		SELECT sp.id, COALESCE(sp.parent_agent_id, ''), sp.child_agent_id,
		       a.handle, a.kind, COALESCE(a.host_id, ''), a.status,
		       sp.spawn_spec_yaml, sp.spawn_authority_json, sp.task_json,
		       COALESCE(sp.worktree_path, ''), sp.spawned_at
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
	out := []spawnListOut{}
	for rows.Next() {
		var sp spawnListOut
		var authority string
		var task sql.NullString
		if err := rows.Scan(&sp.SpawnID, &sp.ParentID, &sp.ChildID,
			&sp.Handle, &sp.Kind, &sp.HostID, &sp.Status,
			&sp.SpawnSpec, &authority, &task,
			&sp.WorktreePath, &sp.SpawnedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		sp.Authority = json.RawMessage(authority)
		if task.Valid {
			sp.Task = json.RawMessage(task.String)
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
		a                             agentOut
		backend, caps                 string
		budget                        sql.NullInt64
		idleSince, termAt             sql.NullString
	)
	if err := r.Scan(&a.ID, &a.TeamID, &a.Handle, &a.Kind, &backend, &caps,
		&a.ParentID, &a.HostID, &a.Status, &a.PaneID,
		&a.WorktreePath, &a.JournalPath,
		&budget, &a.SpentCents, &a.PauseState, &idleSince,
		&a.CreatedAt, &termAt); err != nil {
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
