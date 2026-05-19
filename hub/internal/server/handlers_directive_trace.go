package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
)

// The directive trace (ADR-034 D-7).
//
// GET /v1/teams/{team}/directives/{task}/trace reconstructs a
// directive's timeline — "principal issued → steward received → task
// dispatched → … → [STALL] → …" — by walking the parent/cause chain over
// the existing tables. It is a *query*: no new event stream. ADR-032's
// `cause` and `tasks.parent_task_id` make the timeline a join.

// traceEvent is one hop on a directive's timeline.
type traceEvent struct {
	TS       string `json:"ts"`
	Kind     string `json:"kind"`
	EntityID string `json:"entity_id"`
	Summary  string `json:"summary"`
	Stall    bool   `json:"stall,omitempty"`
}

func (s *Server) handleDirectiveTrace(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	rootID := chi.URLParam(r, "task")

	// The directive must exist and belong to the team (projects join).
	var title string
	err := s.db.QueryRowContext(r.Context(), `
		SELECT t.title FROM tasks t
		  JOIN projects p ON p.id = t.project_id
		 WHERE t.id = ? AND p.team_id = ?`, rootID, team).Scan(&title)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "directive not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	ctx := r.Context()
	subtree, err := s.directiveSubtree(ctx, rootID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	events := []traceEvent{}
	taskEvents, err := s.traceTaskEvents(ctx, subtree)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	events = append(events, taskEvents...)

	questionIDs, questionEvents, err := s.traceQuestionEvents(ctx, subtree)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	events = append(events, questionEvents...)

	auditEvents, err := s.traceLoopAuditEvents(ctx, append(subtree, questionIDs...))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	events = append(events, auditEvents...)

	sort.SliceStable(events, func(i, j int) bool { return events[i].TS < events[j].TS })
	writeJSON(w, http.StatusOK, map[string]any{
		"directive": map[string]any{"id": rootID, "title": title},
		"trace":     events,
	})
}

// directiveSubtree returns the directive and every descendant task id,
// walking tasks.parent_task_id.
func (s *Server) directiveSubtree(ctx context.Context, rootID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH RECURSIVE subtree(id) AS (
		  SELECT id FROM tasks WHERE id = ?
		  UNION ALL
		  SELECT t.id FROM tasks t JOIN subtree s ON t.parent_task_id = s.id
		)
		SELECT id FROM subtree`, rootID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// inClause builds a "?,?,?" placeholder list and its args for an IN query.
func inClause(ids []string) (string, []any) {
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args[i] = id
	}
	return strings.Join(ph, ","), args
}

// traceTaskEvents emits an opened event for every subtree task and a
// closed event for every terminal one.
func (s *Server) traceTaskEvents(ctx context.Context, ids []string) ([]traceEvent, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph, args := inClause(ids)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, title, status, created_at,
		       COALESCE(completed_at, updated_at), COALESCE(terminal_reason, '')
		  FROM tasks WHERE id IN (`+ph+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []traceEvent
	for rows.Next() {
		var id, title, status, createdAt, closedAt, reason string
		if err := rows.Scan(&id, &title, &status, &createdAt, &closedAt, &reason); err != nil {
			return nil, err
		}
		out = append(out, traceEvent{
			TS: createdAt, Kind: "task_opened", EntityID: id,
			Summary: "task opened — " + title,
		})
		if status == "done" || status == "cancelled" {
			sum := "task " + status + " — " + title
			if reason != "" {
				sum += " (" + reason + ")"
			}
			out = append(out, traceEvent{
				TS: closedAt, Kind: "task_closed", EntityID: id, Summary: sum,
			})
		}
	}
	return out, rows.Err()
}

// traceQuestionEvents emits raised/resolved events for every question
// whose cause is in the subtree, and returns those question ids.
func (s *Server) traceQuestionEvents(ctx context.Context, taskIDs []string) ([]string, []traceEvent, error) {
	if len(taskIDs) == 0 {
		return nil, nil, nil
	}
	ph, args := inClause(taskIDs)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, summary, status, created_at, COALESCE(resolved_at, '')
		  FROM attention_items WHERE cause IN (`+ph+`)`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var ids []string
	var out []traceEvent
	for rows.Next() {
		var id, summary, status, createdAt, resolvedAt string
		if err := rows.Scan(&id, &summary, &status, &createdAt, &resolvedAt); err != nil {
			return nil, nil, err
		}
		ids = append(ids, id)
		out = append(out, traceEvent{
			TS: createdAt, Kind: "question_raised", EntityID: id,
			Summary: "question raised — " + summary,
		})
		if status != "open" && resolvedAt != "" {
			out = append(out, traceEvent{
				TS: resolvedAt, Kind: "question_resolved", EntityID: id,
				Summary: "question resolved — " + summary,
			})
		}
	}
	return ids, out, rows.Err()
}

// traceLoopAuditEvents emits the loop.* audit rows targeting any entity
// in the trace — escalations carry the [STALL] marker.
func (s *Server) traceLoopAuditEvents(ctx context.Context, entityIDs []string) ([]traceEvent, error) {
	if len(entityIDs) == 0 {
		return nil, nil
	}
	ph, args := inClause(entityIDs)
	rows, err := s.db.QueryContext(ctx, `
		SELECT ts, action, target_id, summary
		  FROM audit_events
		 WHERE action LIKE 'loop.%' AND target_id IN (`+ph+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []traceEvent
	for rows.Next() {
		var ts, action, targetID, summary string
		if err := rows.Scan(&ts, &action, &targetID, &summary); err != nil {
			return nil, err
		}
		stall := action == "loop.stall_escalated"
		if stall {
			summary = "[STALL] " + summary
		}
		out = append(out, traceEvent{
			TS: ts, Kind: action, EntityID: targetID, Summary: summary, Stall: stall,
		})
	}
	return out, rows.Err()
}
