package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Host commands are the hub→host-runner work queue (see migration 0002).
// The host-runner pulls pending commands on its poll tick, applies them
// locally (SIGSTOP on a pane, tmux capture-pane, etc.), and PATCHes the
// result back. Keeping it pull-only means host-runners behind NAT work
// without any hub-initiated connection.

type commandOut struct {
	ID      string          `json:"id"`
	HostID  string          `json:"host_id"`
	AgentID string          `json:"agent_id,omitempty"`
	Kind    string          `json:"kind"`
	Args    json.RawMessage `json:"args"`
	Status  string          `json:"status"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   string          `json:"error,omitempty"`
	// Progress is a detached job's coarse {phase, done, total}, and ProgressAt
	// the hub's receipt time for it (ADR-058 §3). Both nil for every inline
	// kind, which never reports progress.
	Progress    json.RawMessage `json:"progress,omitempty"`
	ProgressAt  *string         `json:"progress_at,omitempty"`
	CreatedAt   string          `json:"created_at"`
	DeliveredAt *string         `json:"delivered_at,omitempty"`
	CompletedAt *string         `json:"completed_at,omitempty"`
}

// handleListHostCommands returns pending commands for a host and atomically
// flips them to 'delivered'. host-runner calls this on each poll tick.
func (s *Server) handleListHostCommands(w http.ResponseWriter, r *http.Request) {
	hostID := chi.URLParam(r, "host")
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "pending"
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, host_id, COALESCE(agent_id, ''), kind, args_json,
		       status, COALESCE(result_json, ''), COALESCE(error, ''),
		       COALESCE(progress_json, ''), progress_at,
		       created_at, delivered_at, completed_at
		FROM host_commands
		WHERE host_id = ? AND status = ?
		ORDER BY created_at
		LIMIT 50`, hostID, status)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	defer rows.Close()

	out := []commandOut{}
	ids := []any{}
	for rows.Next() {
		var c commandOut
		var args, result, progress string
		var progressAt, delivered, completed sql.NullString
		if err := rows.Scan(&c.ID, &c.HostID, &c.AgentID, &c.Kind, &args,
			&c.Status, &result, &c.Error,
			&progress, &progressAt,
			&c.CreatedAt, &delivered, &completed); err != nil {
			s.writeDBErr(w, err)
			return
		}
		c.Args = json.RawMessage(args)
		if result != "" {
			c.Result = json.RawMessage(result)
		}
		if progress != "" {
			c.Progress = json.RawMessage(progress)
		}
		if progressAt.Valid {
			c.ProgressAt = &progressAt.String
		}
		if delivered.Valid {
			c.DeliveredAt = &delivered.String
		}
		if completed.Valid {
			c.CompletedAt = &completed.String
		}
		out = append(out, c)
		ids = append(ids, c.ID)
	}
	if err := rows.Err(); err != nil {
		s.writeDBErr(w, err)
		return
	}

	if status == "pending" && len(ids) > 0 {
		now := NowUTC()
		q := "UPDATE host_commands SET status = 'delivered', delivered_at = ? WHERE id IN (?" +
			strings_repeat(",?", len(ids)-1) + ")"
		args := append([]any{now}, ids...)
		if _, err := s.writeDB.ExecContext(r.Context(), q, args...); err != nil {
			// Non-fatal: worst case host-runner re-reads the same command next tick,
			// and its PATCH is idempotent.
			s.log.Warn("mark delivered failed", "err", err)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type commandPatchIn struct {
	Status string          `json:"status"` // 'done' | 'failed' | "" (progress-only)
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	// Progress carries a detached job's coarse {phase, done, total}. Sent with
	// no status it is a heartbeat (ADR-058 §3). Sent *alongside* a status it is
	// ignored: the terminal report is authoritative, and progress describes a
	// job that is still running.
	Progress json.RawMessage `json:"progress,omitempty"`
}

// handlePatchHostCommand lets the host-runner report completion / failure.
// On a successful 'capture' we also cache the pane content on the agent row
// so API callers can read it without queuing another capture command.
//
// A patch carrying progress and no status is a running job's heartbeat: it
// updates progress_json + progress_at and nothing else. Keeping it on this same
// endpoint is what lets the statuses stay as they are — `delivered` already
// means "running" for a job kind, so no consumer of the lifecycle had to learn
// a new state.
func (s *Server) handlePatchHostCommand(w http.ResponseWriter, r *http.Request) {
	cmdID := chi.URLParam(r, "cmd")
	var in commandPatchIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Status == "" && len(in.Progress) > 0 {
		s.patchCommandProgress(w, r, cmdID, in.Progress)
		return
	}
	if in.Status != "done" && in.Status != "failed" {
		writeErr(w, http.StatusBadRequest, "status must be done|failed (omit it to report progress only)")
		return
	}

	// Load kind + agent_id before update so we can do the capture cache write.
	var kind, agentID string
	err := s.db.QueryRowContext(r.Context(),
		`SELECT kind, COALESCE(agent_id, '') FROM host_commands WHERE id = ?`, cmdID).
		Scan(&kind, &agentID)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "command not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}

	now := NowUTC()
	result := string(in.Result)
	if result == "" {
		result = "{}"
	}
	// The command-completion UPDATE and the agent-state syncs it triggers
	// (last_capture on a capture, pause_state on pause/resume) must commit
	// together: a command marked `done` while the agent's pause_state stays
	// stale is the inconsistency #76 flagged, and those agent UPDATE errors
	// were previously discarded. One transaction makes the pair atomic.
	tx, err := s.writeDB.BeginTx(r.Context(), nil)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	defer tx.Rollback() // no-op once Commit succeeds
	if _, err := tx.ExecContext(r.Context(), `
		UPDATE host_commands SET
			status = ?, result_json = ?, error = NULLIF(?, ''), completed_at = ?
		WHERE id = ?`,
		in.Status, result, in.Error, now, cmdID); err != nil {
		s.writeDBErr(w, err)
		return
	}

	if in.Status == "done" && kind == "capture" && agentID != "" {
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(in.Result, &payload); err == nil && payload.Text != "" {
			if _, err := tx.ExecContext(r.Context(),
				`UPDATE agents SET last_capture = ?, last_capture_at = ? WHERE id = ?`,
				payload.Text, now, agentID); err != nil {
				s.writeDBErr(w, err)
				return
			}
		}
	}

	// Synchronise agent.pause_state with pause/resume outcomes.
	if in.Status == "done" && agentID != "" {
		switch kind {
		case "pause":
			if _, err := tx.ExecContext(r.Context(),
				`UPDATE agents SET pause_state = 'paused' WHERE id = ?`, agentID); err != nil {
				s.writeDBErr(w, err)
				return
			}
		case "resume":
			if _, err := tx.ExecContext(r.Context(),
				`UPDATE agents SET pause_state = 'running' WHERE id = ?`, agentID); err != nil {
				s.writeDBErr(w, err)
				return
			}
		}
	}
	if err := tx.Commit(); err != nil {
		s.writeDBErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// maxCommandProgressBytes caps one progress payload. Progress is a coarse
// {phase, done, total} that arrives every ~30s for the life of a job; without a
// bound a host could stream arbitrary volume into a column nothing truncates.
const maxCommandProgressBytes = 4 << 10

// patchCommandProgress records a running job's heartbeat.
//
// progress_at is stamped here, from the hub's clock, and never read out of the
// payload: the stale-job sweep's verdict must not depend on a remote host's
// clock being right.
func (s *Server) patchCommandProgress(w http.ResponseWriter, r *http.Request, cmdID string, progress json.RawMessage) {
	if len(progress) > maxCommandProgressBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "progress payload too large")
		return
	}
	var probe map[string]any
	if err := json.Unmarshal(progress, &probe); err != nil {
		writeErr(w, http.StatusBadRequest, "progress must be a json object")
		return
	}

	// `AND status = 'delivered'` is the whole point of the guard: a heartbeat
	// must never revive a row that has already reached a terminal state. If the
	// stale sweep gave up on this job, a bare "still here" is not evidence
	// enough to undo that — only a real terminal patch, which carries a result,
	// speaks for the work.
	res, err := s.writeDB.ExecContext(r.Context(), `
		UPDATE host_commands SET progress_json = ?, progress_at = ?
		WHERE id = ? AND status = 'delivered'`,
		string(progress), NowUTC(), cmdID)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Either no such command, or it is no longer running. Both mean the
		// same thing to a job that is still reporting: stop.
		writeErr(w, http.StatusConflict, "command is not running")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// enqueueHostCommand is the internal helper other handlers use to push work
// to a host. agentID is optional (some commands target the host itself).
func (s *Server) enqueueHostCommand(ctx context.Context, hostID, agentID, kind string, args any) (string, error) {
	argsJSON := []byte("{}")
	if args != nil {
		b, err := json.Marshal(args)
		if err != nil {
			return "", err
		}
		argsJSON = b
	}
	id := NewID()
	_, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO host_commands (id, host_id, agent_id, kind, args_json, status, created_at)
		VALUES (?, ?, NULLIF(?, ''), ?, ?, 'pending', ?)`,
		id, hostID, agentID, kind, string(argsJSON), NowUTC())
	return id, err
}

// strings_repeat avoids a strings import just for a comma splatter.
func strings_repeat(s string, n int) string {
	if n <= 0 {
		return ""
	}
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
