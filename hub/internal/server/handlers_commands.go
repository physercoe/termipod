package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Host commands are the hub→host-agent work queue (see migration 0002).
// The host-agent pulls pending commands on its poll tick, applies them
// locally (SIGSTOP on a pane, tmux capture-pane, etc.), and PATCHes the
// result back. Keeping it pull-only means host-agents behind NAT work
// without any hub-initiated connection.

type commandOut struct {
	ID          string          `json:"id"`
	HostID      string          `json:"host_id"`
	AgentID     string          `json:"agent_id,omitempty"`
	Kind        string          `json:"kind"`
	Args        json.RawMessage `json:"args"`
	Status      string          `json:"status"`
	Result      json.RawMessage `json:"result,omitempty"`
	Error       string          `json:"error,omitempty"`
	CreatedAt   string          `json:"created_at"`
	DeliveredAt *string         `json:"delivered_at,omitempty"`
	CompletedAt *string         `json:"completed_at,omitempty"`
}

// handleListHostCommands returns pending commands for a host and atomically
// flips them to 'delivered'. host-agent calls this on each poll tick.
func (s *Server) handleListHostCommands(w http.ResponseWriter, r *http.Request) {
	hostID := chi.URLParam(r, "host")
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "pending"
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, host_id, COALESCE(agent_id, ''), kind, args_json,
		       status, COALESCE(result_json, ''), COALESCE(error, ''),
		       created_at, delivered_at, completed_at
		FROM host_commands
		WHERE host_id = ? AND status = ?
		ORDER BY created_at
		LIMIT 50`, hostID, status)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	out := []commandOut{}
	ids := []any{}
	for rows.Next() {
		var c commandOut
		var args, result string
		var delivered, completed sql.NullString
		if err := rows.Scan(&c.ID, &c.HostID, &c.AgentID, &c.Kind, &args,
			&c.Status, &result, &c.Error,
			&c.CreatedAt, &delivered, &completed); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		c.Args = json.RawMessage(args)
		if result != "" {
			c.Result = json.RawMessage(result)
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

	if status == "pending" && len(ids) > 0 {
		now := NowUTC()
		q := "UPDATE host_commands SET status = 'delivered', delivered_at = ? WHERE id IN (?" +
			strings_repeat(",?", len(ids)-1) + ")"
		args := append([]any{now}, ids...)
		if _, err := s.db.ExecContext(r.Context(), q, args...); err != nil {
			// Non-fatal: worst case host-agent re-reads the same command next tick,
			// and its PATCH is idempotent.
			s.log.Warn("mark delivered failed", "err", err)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type commandPatchIn struct {
	Status string          `json:"status"` // 'done' | 'failed'
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// handlePatchHostCommand lets the host-agent report completion / failure.
// On a successful 'capture' we also cache the pane content on the agent row
// so API callers can read it without queuing another capture command.
func (s *Server) handlePatchHostCommand(w http.ResponseWriter, r *http.Request) {
	cmdID := chi.URLParam(r, "cmd")
	var in commandPatchIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Status != "done" && in.Status != "failed" {
		writeErr(w, http.StatusBadRequest, "status must be done|failed")
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
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	now := NowUTC()
	result := string(in.Result)
	if result == "" {
		result = "{}"
	}
	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE host_commands SET
			status = ?, result_json = ?, error = NULLIF(?, ''), completed_at = ?
		WHERE id = ?`,
		in.Status, result, in.Error, now, cmdID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	if in.Status == "done" && kind == "capture" && agentID != "" {
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(in.Result, &payload); err == nil && payload.Text != "" {
			_, _ = s.db.ExecContext(r.Context(),
				`UPDATE agents SET last_capture = ?, last_capture_at = ? WHERE id = ?`,
				payload.Text, now, agentID)
		}
	}

	// Synchronise agent.pause_state with pause/resume outcomes.
	if in.Status == "done" && agentID != "" {
		switch kind {
		case "pause":
			_, _ = s.db.ExecContext(r.Context(),
				`UPDATE agents SET pause_state = 'paused' WHERE id = ?`, agentID)
		case "resume":
			_, _ = s.db.ExecContext(r.Context(),
				`UPDATE agents SET pause_state = 'running' WHERE id = ?`, agentID)
		}
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
	_, err := s.db.ExecContext(ctx, `
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
