package server

// SOTA orchestrator-worker primitives — the steward calls these via
// MCP to drive the project-layer multi-agent pattern documented in
// docs/multi-agent-sota-gap.md §5. Three tools:
//
//   - agents.fanout — atomically spawn N workers (each with auto_open_session,
//     each tagged with the same correlation_id), and post their first input
//     so they start working without an extra round-trip.
//   - agents.gather — long-poll until all workers in a correlation either
//     post a worker_report or hit a terminal status.
//   - reports.post — workers call this on completion to write a typed
//     worker_report agent_event with frontmatter that the steward can parse
//     programmatically.
//
// The fanout/gather/report shape mirrors Anthropic's research-system
// orchestrator-worker pattern (`docs/multi-agent-sota-gap.md` §1.1) and
// CrewAI's hierarchical process. The deliberate choice: workers don't
// peer-talk; results flow only through reports.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// gatherTimeout caps how long agents.gather will hold the long-poll
// open. Picked so a slow worker has room to think but a hung steward
// doesn't burn its context window forever.
const gatherTimeout = 10 * time.Minute

// ---------------------------------------------------------------------
// agents.fanout
// ---------------------------------------------------------------------

type fanoutWorker struct {
	Handle         string `json:"handle"`
	Kind           string `json:"kind"`
	HostID         string `json:"host_id,omitempty"`
	SpawnSpec      string `json:"spawn_spec_yaml"`
	PersonaSeed    string `json:"persona_seed,omitempty"`
	PermissionMode string `json:"permission_mode,omitempty"`
	Task           string `json:"task"`
}

type fanoutArgs struct {
	CorrelationID string         `json:"correlation_id"`
	Workers       []fanoutWorker `json:"workers"`
}

func (s *Server) mcpAgentsFanout(ctx context.Context, team string, raw json.RawMessage) (any, *jrpcError) {
	var a fanoutArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &jrpcError{Code: -32602, Message: "invalid args: " + err.Error()}
	}
	if a.CorrelationID == "" {
		return nil, &jrpcError{Code: -32602, Message: "correlation_id required"}
	}
	if len(a.Workers) == 0 {
		return nil, &jrpcError{Code: -32602, Message: "at least one worker required"}
	}

	// Sequential spawn rather than one-tx because DoSpawn already opens
	// its own tx (and validates per-spawn). If one worker's spawn fails,
	// the prior ones survive — the caller sees a partial-success result
	// and decides. SOTA frameworks tolerate partial fanout (Anthropic's
	// research system explicitly handles "subagent failed to start").
	results := make([]map[string]any, 0, len(a.Workers))
	for _, w := range a.Workers {
		if w.Handle == "" || w.Kind == "" || w.SpawnSpec == "" || w.Task == "" {
			results = append(results, map[string]any{
				"handle": w.Handle,
				"error":  "handle, kind, spawn_spec_yaml, task required per worker",
			})
			continue
		}
		// Persona seed gets the correlation_id appended so a worker
		// inspecting its own context can tell which fanout it belongs
		// to. Non-load-bearing — just helps debugging when a worker
		// goes off-rails.
		seed := w.PersonaSeed
		if seed != "" && !strings.Contains(seed, a.CorrelationID) {
			seed += "\n\nFANOUT_ID: " + a.CorrelationID
		}
		spawn, code, err := s.DoSpawn(ctx, team, spawnIn{
			ChildHandle:     w.Handle,
			Kind:            w.Kind,
			HostID:          w.HostID,
			SpawnSpec:       w.SpawnSpec,
			PersonaSeed:     seed,
			PermissionMode:  w.PermissionMode,
			AutoOpenSession: true,
		})
		if err != nil {
			results = append(results, map[string]any{
				"handle":      w.Handle,
				"http_status": code,
				"error":       err.Error(),
			})
			continue
		}

		// Stamp the agent's session with the correlation_id so gather
		// can find them. session_id was just opened by DoSpawn's
		// auto-open path; locate it via lookupSessionForAgent.
		sessionID := s.lookupSessionForAgent(ctx, spawn.AgentID)
		if sessionID != "" {
			_, _ = s.writeDB.ExecContext(ctx, `
				UPDATE sessions SET correlation_id = ?
				 WHERE team_id = ? AND id = ?`,
				a.CorrelationID, team, sessionID)
		}

		// Post the task as the worker's first input so it starts
		// processing without waiting for an external nudge. Same
		// /agents/{id}/input wire-shape the mobile + A2A use.
		if perr := s.postSyntheticUserInput(ctx, spawn.AgentID, w.Task); perr != nil {
			// Spawn succeeded but input failed — return both so the
			// steward knows the worker is alive but hasn't received
			// its task. They can resend.
			results = append(results, map[string]any{
				"handle":     w.Handle,
				"agent_id":   spawn.AgentID,
				"session_id": sessionID,
				"error":      "spawn ok but input post failed: " + perr.Error(),
			})
			continue
		}
		results = append(results, map[string]any{
			"handle":     w.Handle,
			"agent_id":   spawn.AgentID,
			"session_id": sessionID,
			"status":     "ok",
		})
	}

	s.recordAudit(ctx, team, "agents.fanout", "agents", a.CorrelationID,
		fmt.Sprintf("fanout %d workers under %s", len(a.Workers), a.CorrelationID),
		map[string]any{"worker_count": len(a.Workers), "correlation_id": a.CorrelationID})

	return mcpResultJSON(map[string]any{
		"correlation_id": a.CorrelationID,
		"workers":        results,
	}), nil
}

// postSyntheticUserInput writes a producer='user' input.text event so
// the worker's InputRouter delivers it as the next user message on its
// next tick — same wire-shape mobile + A2A use, but injected by the
// hub itself so a spawn can start working without an external nudge.
// Shared by agents.fanout (one input per worker in the burst) and
// ADR-029 spawn-with-task (the steward's task body becomes the first
// turn for the new worker). Goes directly to SQL to stay in-process.
func (s *Server) postSyntheticUserInput(ctx context.Context, agentID, body string) error {
	if body == "" {
		return errors.New("empty task body")
	}
	sessionID := s.lookupSessionForAgent(ctx, agentID)
	// ADR-032: deliver the synthetic turn as a message envelope — a
	// hub-injected directive (the steward's task body) to the worker.
	env := composeMessage(systemEndpoint(), s.endpointForAgent(ctx, agentID),
		KindDirective, body, "",
		MessageThread{Transport: TransportSession, ID: sessionID})
	if ae := s.admitEnvelope(ctx, env, false); ae != nil {
		return fmt.Errorf("synthetic input envelope rejected: %s", ae.Error())
	}
	payload, _ := json.Marshal(env.PayloadMap())
	id, _, _, ts, err := insertAgentEvent(ctx, s.writeDB, agentEventInsert{
		AgentID:     agentID,
		SessionID:   sessionID,
		Kind:        "input.text",
		Producer:    "user",
		PayloadJSON: string(payload),
	})
	if err != nil {
		return err
	}
	s.touchSession(ctx, sessionID)
	s.bus.Publish(agentBusKey(agentID), map[string]any{
		"id":         id,
		"agent_id":   agentID,
		"ts":         ts,
		"kind":       "input.text",
		"producer":   "user",
		"payload":    json.RawMessage(payload),
		"session_id": sessionID,
	})
	return nil
}

// ---------------------------------------------------------------------
// agents.gather
// ---------------------------------------------------------------------

type gatherArgs struct {
	CorrelationID string `json:"correlation_id"`
	TimeoutS      int    `json:"timeout_s,omitempty"`
}

func (s *Server) mcpAgentsGather(ctx context.Context, team string, raw json.RawMessage) (any, *jrpcError) {
	var a gatherArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &jrpcError{Code: -32602, Message: "invalid args: " + err.Error()}
	}
	if a.CorrelationID == "" {
		return nil, &jrpcError{Code: -32602, Message: "correlation_id required"}
	}
	timeout := gatherTimeout
	if a.TimeoutS > 0 && time.Duration(a.TimeoutS)*time.Second < timeout {
		timeout = time.Duration(a.TimeoutS) * time.Second
	}
	deadline := time.Now().Add(timeout)
	delay := 200 * time.Millisecond
	const maxDelay = 3 * time.Second

	for {
		results, allDone, err := s.fanoutResults(ctx, team, a.CorrelationID)
		if err != nil {
			return nil, &jrpcError{Code: -32000, Message: err.Error()}
		}
		if allDone || time.Now().After(deadline) {
			return mcpResultJSON(map[string]any{
				"correlation_id": a.CorrelationID,
				"timed_out":      !allDone,
				"workers":        results,
			}), nil
		}
		select {
		case <-ctx.Done():
			return nil, &jrpcError{Code: -32000, Message: ctx.Err().Error()}
		case <-time.After(delay):
		}
		if delay < maxDelay {
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}
}

// fanoutResults collects per-worker state for a correlation_id. A
// worker is considered "done" when it has posted at least one event
// of kind='worker_report' OR its session/agent has reached a terminal
// state (closed/deleted/terminated/failed/crashed).
func (s *Server) fanoutResults(
	ctx context.Context, team, corr string,
) (results []map[string]any, allDone bool, err error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.current_agent_id, s.status,
		       a.handle, a.status AS agent_status
		  FROM sessions s
		  LEFT JOIN agents a ON a.id = s.current_agent_id
		 WHERE s.team_id = ? AND s.correlation_id = ?`,
		team, corr)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	type row struct {
		sessionID, agentID, sessionStatus string
		handle                            sql.NullString
		agentStatus                       sql.NullString
	}
	var entries []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.sessionID, &r.agentID, &r.sessionStatus,
			&r.handle, &r.agentStatus); err != nil {
			return nil, false, err
		}
		entries = append(entries, r)
	}
	if len(entries) == 0 {
		return []map[string]any{}, true, nil
	}

	allDone = true
	for _, r := range entries {
		// Look up the latest worker_report event for this agent (if any).
		var reportPayload sql.NullString
		var reportTS sql.NullString
		_ = s.db.QueryRowContext(ctx, `
			SELECT payload_json, ts FROM agent_events
			 WHERE agent_id = ? AND kind = 'worker_report'
			 ORDER BY seq DESC LIMIT 1`, r.agentID).Scan(&reportPayload, &reportTS)

		hasReport := reportPayload.Valid
		terminal := r.agentStatus.Valid &&
			(r.agentStatus.String == "terminated" ||
				r.agentStatus.String == "crashed" ||
				r.agentStatus.String == "failed")
		done := hasReport || terminal
		if !done {
			allDone = false
		}
		entry := map[string]any{
			"session_id":     r.sessionID,
			"agent_id":       r.agentID,
			"handle":         r.handle.String,
			"session_status": r.sessionStatus,
			"agent_status":   r.agentStatus.String,
			"done":           done,
		}
		if hasReport {
			var rp map[string]any
			_ = json.Unmarshal([]byte(reportPayload.String), &rp)
			entry["report"] = rp
			if reportTS.Valid {
				entry["report_ts"] = reportTS.String
			}
		}
		results = append(results, entry)
	}
	return results, allDone, nil
}

// ---------------------------------------------------------------------
// reports.post
// ---------------------------------------------------------------------

type reportArgs struct {
	Status          string   `json:"status"`
	SummaryMD       string   `json:"summary_md"`
	OutputArtifacts []string `json:"output_artifacts,omitempty"`
	BudgetUsedUSD   float64  `json:"budget_used_usd,omitempty"`
	NextSteps       []string `json:"next_steps,omitempty"`
}

func (s *Server) mcpReportsPost(ctx context.Context, agentID string, raw json.RawMessage) (any, *jrpcError) {
	if agentID == "" {
		return nil, &jrpcError{Code: -32000,
			Message: "reports.post called outside agent scope (no agent_id on token)"}
	}
	var a reportArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &jrpcError{Code: -32602, Message: "invalid args: " + err.Error()}
	}
	if a.Status == "" || a.SummaryMD == "" {
		return nil, &jrpcError{Code: -32602, Message: "status + summary_md required"}
	}
	switch a.Status {
	case "success", "partial", "failed":
		// ok
	default:
		return nil, &jrpcError{Code: -32602,
			Message: "status must be one of: success, partial, failed"}
	}

	payload, _ := json.Marshal(map[string]any{
		"status":           a.Status,
		"summary_md":       a.SummaryMD,
		"output_artifacts": a.OutputArtifacts,
		"budget_used_usd":  a.BudgetUsedUSD,
		"next_steps":       a.NextSteps,
	})
	sessionID := s.lookupSessionForAgent(ctx, agentID)
	id, _, _, ts, err := insertAgentEvent(ctx, s.writeDB, agentEventInsert{
		AgentID:     agentID,
		SessionID:   sessionID,
		Kind:        "worker_report",
		Producer:    "agent",
		PayloadJSON: string(payload),
	})
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	s.touchSession(ctx, sessionID)
	s.bus.Publish(agentBusKey(agentID), map[string]any{
		"id":         id,
		"agent_id":   agentID,
		"ts":         ts,
		"kind":       "worker_report",
		"producer":   "agent",
		"payload":    json.RawMessage(payload),
		"session_id": sessionID,
	})
	return mcpResultJSON(map[string]any{"id": id, "status": a.Status}), nil
}
