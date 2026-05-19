package server

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"strings"

	"gopkg.in/yaml.v3"
)

// Orchestration lifecycle hooks (ADR-034 D-5).
//
// The loop-closure runtime runs two hub-side hooks at orchestration
// events. Per CLAUDE.md "behaviour is data" they are configured by
// bundled YAML (loop_hooks_defaults.yaml), not Go constants — a new
// closure policy is a change to that file. The hooks are loop-lifecycle
// only; message-level admission is ADR-032 D-7's deterministic pipeline,
// not a hook.

//go:embed loop_hooks_defaults.yaml
var loopHooksDefaultYAML []byte

type loopHookConfig struct {
	PreAgentIdle struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"pre_agent_idle"`
	PostDirectiveOutcome struct {
		Enabled           bool `yaml:"enabled"`
		MinSynthesisChars int  `yaml:"min_synthesis_chars"`
	} `yaml:"post_directive_outcome"`
}

// loopHooks is the parsed hook configuration, loaded once from the
// bundled YAML. A parse failure leaves both hooks disabled (fail-safe —
// a broken config never silently changes orchestration behaviour).
var loopHooks = loadLoopHooks()

func loadLoopHooks() loopHookConfig {
	var c loopHookConfig
	if err := yaml.Unmarshal(loopHooksDefaultYAML, &c); err != nil {
		return loopHookConfig{}
	}
	return c
}

// lifecycleIsIdle reports whether a lifecycle event's payload carries an
// idle / stopped phase — the trigger for the PreAgentIdle hook.
func lifecycleIsIdle(payloadJSON string) bool {
	var p struct {
		Phase string `json:"phase"`
	}
	if json.Unmarshal([]byte(payloadJSON), &p) != nil {
		return false
	}
	return p.Phase == "idle" || p.Phase == "stopped"
}

// onPreAgentIdle fires when an agent goes idle. If it still owns open
// loop-entities, the hook re-wakes it with the open set — it cannot rest
// while it owns unclosed work (ADR-034 D-5). Best-effort.
func (s *Server) onPreAgentIdle(ctx context.Context, agentID string) {
	if !loopHooks.PreAgentIdle.Enabled || agentID == "" {
		return
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT title FROM tasks
		 WHERE assignee_id = ? AND status NOT IN ('done', 'cancelled')`, agentID)
	if err != nil {
		return
	}
	var open []string
	for rows.Next() {
		var title string
		if rows.Scan(&title) == nil {
			open = append(open, title)
		}
	}
	rows.Close()
	if len(open) == 0 {
		return
	}
	text := "You went idle but still own open work: " + strings.Join(open, "; ") +
		". Continue it, or close each item with a terminal report — do not " +
		"go idle while you hold an open directive."
	s.emitSystemNotification(ctx, agentID, text, "")
}

// onPostDirectiveOutcome fires when a report closes a task. For a root
// task — a principal directive — it flags a closing report that is a
// bare relay rather than a genuine synthesis (ADR-034 D-5; "synthesis is
// not relay"). Best-effort; the flag is an audit row, not a block.
func (s *Server) onPostDirectiveOutcome(ctx context.Context, taskID, resultSummary string) {
	if !loopHooks.PostDirectiveOutcome.Enabled || taskID == "" {
		return
	}
	var parent sql.NullString
	var projectID string
	if err := s.db.QueryRowContext(ctx,
		`SELECT parent_task_id, project_id FROM tasks WHERE id = ?`, taskID).
		Scan(&parent, &projectID); err != nil {
		return
	}
	if parent.Valid && parent.String != "" {
		return // not a root directive — a child task's outcome isn't gated.
	}
	if len(strings.TrimSpace(resultSummary)) >= loopHooks.PostDirectiveOutcome.MinSynthesisChars {
		return // looks synthesised.
	}
	var team string
	_ = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(team_id, '') FROM projects WHERE id = ?`, projectID).Scan(&team)
	s.recordAudit(ctx, team, "loop.relay_not_synthesis", "task", taskID,
		"directive closed without a synthesis report",
		map[string]any{"project_id": projectID, "summary_len": len(resultSummary)})
}

// emitSystemNotification delivers a system-from notification envelope
// into an agent's session as an input.text event (ADR-032). Shared by
// the lifecycle hooks; best-effort.
func (s *Server) emitSystemNotification(ctx context.Context, agentID, text, cause string) {
	sessionID := s.lookupSessionForAgent(ctx, agentID)
	env := composeMessage(systemEndpoint(), s.endpointForAgent(ctx, agentID),
		KindNotification, text, cause,
		MessageThread{Transport: TransportSession, ID: sessionID})
	if ae := s.admitEnvelope(ctx, env, false); ae != nil {
		s.log.Warn("system notification rejected",
			"stage", ae.Stage, "reason", ae.Reason)
		return
	}
	payload, _ := json.Marshal(env.PayloadMap())
	id := NewID()
	ts := NowUTC()
	var seq int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json, session_id)
		SELECT ?, ?, COALESCE(MAX(seq), 0) + 1, ?, 'input.text', 'system', ?, NULLIF(?, '')
		  FROM agent_events WHERE agent_id = ?
		RETURNING seq`,
		id, agentID, ts, string(payload), sessionID, agentID).Scan(&seq)
	if err != nil {
		s.log.Warn("system notification insert failed", "agent", agentID, "err", err)
		return
	}
	s.touchSession(ctx, sessionID)
	s.bus.Publish(agentBusKey(agentID), map[string]any{
		"id": id, "agent_id": agentID, "seq": seq, "ts": ts,
		"kind": "input.text", "producer": "system",
		"payload": json.RawMessage(payload), "session_id": sessionID,
	})
}
