package server

import (
	"context"
	"strings"
)

// The loop-entity (ADR-034 D-8).
//
// The loop-closure runtime tracks a *role* — the loop-entity — that two
// existing tables satisfy: a directive / task is a `tasks` row, a
// question is an `attention_items` row. There is no `loop_entities`
// table; the open-set is a UNION over the two. LoopEntity is the runtime
// projection both rows are read into, discriminated by Source.

// Loop-entity sources.
const (
	LoopSourceTask     = "task"     // a tasks row — a directive or a task
	LoopSourceQuestion = "question" // an attention_items row — a question
)

// Terminal reasons — the close classification (ADR-034 D-6). Additive to
// the human-facing status; set when a loop-entity closes.
const (
	TerminalCompleted  = "completed"
	TerminalFailed     = "failed"
	TerminalKilled     = "killed"
	TerminalTimedOut   = "timed_out"
	TerminalSuperseded = "superseded"
)

// Escalation states — advanced one level per breached deadline by the
// loop sweep (ADR-034 D-3); idempotent, never re-fires a level.
const (
	EscalationNone      = "none"
	EscalationSteward   = "escalated_steward"
	EscalationPrincipal = "escalated_principal"
)

// questionAttentionKinds is the set of attention_items.kind values that
// are loop-bearing *questions* — an agent asking, awaiting an answer.
// Other attention kinds (spawn approvals, template installs) are
// governance items, not loop-entities.
var questionAttentionKinds = []string{
	"help_request", "select", "approval_request", "elicit", "permission_prompt",
}

// LoopEntity is the runtime projection of an open directive/task/question
// — the role both `tasks` and `attention_items` rows satisfy. Source
// discriminates the backing table; ParentID is the lineage parent
// (tasks.parent_task_id, or attention_items.cause for a question).
type LoopEntity struct {
	ID                 string
	Source             string
	ProjectID          string
	ParentID           string
	AssigneeID         string
	State              string
	InactivityDeadline string
	LastProgressAt     string
	OpenedAt           string
	AbsoluteCap        string
	EscalationState    string
}

// openLoopEntities returns every open loop-entity — the UNION of open
// `tasks` (status not done/cancelled) and open question-kind
// `attention_items` (ADR-034 D-1). This is the set the loop sweep
// scans each tick.
func (s *Server) openLoopEntities(ctx context.Context) ([]LoopEntity, error) {
	ph := make([]string, len(questionAttentionKinds))
	args := make([]any, len(questionAttentionKinds))
	for i, k := range questionAttentionKinds {
		ph[i] = "?"
		args[i] = k
	}
	q := `
		SELECT id, 'task' AS source, project_id,
		       COALESCE(parent_task_id, ''), COALESCE(assignee_id, ''), status,
		       COALESCE(inactivity_deadline, ''), COALESCE(last_progress_at, ''),
		       COALESCE(opened_at, ''), COALESCE(absolute_cap, ''), escalation_state
		  FROM tasks
		 WHERE status NOT IN ('done', 'cancelled')
		UNION ALL
		SELECT id, 'question' AS source, COALESCE(project_id, ''),
		       COALESCE(cause, ''), '', status,
		       COALESCE(inactivity_deadline, ''), COALESCE(last_progress_at, ''),
		       COALESCE(opened_at, ''), COALESCE(absolute_cap, ''), escalation_state
		  FROM attention_items
		 WHERE status = 'open' AND kind IN (` + strings.Join(ph, ", ") + `)`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LoopEntity
	for rows.Next() {
		var e LoopEntity
		if err := rows.Scan(&e.ID, &e.Source, &e.ProjectID, &e.ParentID,
			&e.AssigneeID, &e.State, &e.InactivityDeadline, &e.LastProgressAt,
			&e.OpenedAt, &e.AbsoluteCap, &e.EscalationState); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
