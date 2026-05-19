package server

import (
	"context"
	"fmt"
)

// The message-admission pipeline (ADR-032 D-7).
//
// Every composed envelope passes this deterministic pipeline at the
// hub-server compose boundary, before its agent_events row is written:
//
//	validateEnvelope → routing-legality → context
//
// The pipeline is fail-safe — error handling is *safe*, not *correct*;
// deny outranks allow. A malformed envelope from a hub writer site is a
// programming error (fail fast, 500, logged loudly). A bad envelope
// whose kind/cause an agent declared (the A2A path) is recoverable —
// rejected with an ADR-031 hint so the agent can retry. Never crash,
// never silently drop.

// admissionError is a rejected-envelope outcome. AgentRecoverable marks
// an envelope an agent can fix and retry; otherwise it is a hub
// programming error.
type admissionError struct {
	Stage            string
	Reason           string
	Hint             Hint
	AgentRecoverable bool
}

func (e *admissionError) Error() string {
	return fmt.Sprintf("admission[%s]: %s", e.Stage, e.Reason)
}

// admitEnvelope runs the admission pipeline. agentOrigin marks an
// envelope whose kind/cause an agent declared (producer=a2a) — its
// rejections are recoverable. Returns nil when the envelope is admitted.
func (s *Server) admitEnvelope(ctx context.Context, env MessageEnvelope, agentOrigin bool) *admissionError {
	// Stage 1 — schema validity.
	if ae := validateEnvelopeSchema(env); ae != nil {
		ae.AgentRecoverable = agentOrigin
		return ae
	}
	// Stage 1 (cont.) — a non-null cause must resolve to a live entity.
	if env.Cause != "" && !s.causeResolves(ctx, env.Cause) {
		return &admissionError{
			Stage:  "validate",
			Reason: "cause does not resolve to a live task or attention item",
			Hint: Hint{HintText: "pass a cause that is an existing task/directive id, " +
				"or omit it for an untied message"},
			AgentRecoverable: agentOrigin,
		}
	}
	// Stage 2 — routing legality.
	if ae := checkRoutingLegality(env); ae != nil {
		ae.AgentRecoverable = agentOrigin
		return ae
	}
	// Stage 3 — context. Phase A: a report/question carrying a cause is
	// schema- and resolution-checked above. The assignee-scoped check
	// ("a report must reference an entity assigned to the sender")
	// arrives with the loop-entity model (ADR-034 / B-phase).
	return nil
}

// validateEnvelopeSchema checks the envelope is structurally well-formed
// — every closed-enum field is in range and the recipient is an agent.
func validateEnvelopeSchema(env MessageEnvelope) *admissionError {
	if !validEnvelopeKind(env.Kind) {
		return &admissionError{Stage: "validate",
			Reason: "unknown kind " + env.Kind,
			Hint:   Hint{HintText: "kind must be directive|question|report|notification"}}
	}
	if !validEnvelopeRole(env.From.Role) {
		return &admissionError{Stage: "validate",
			Reason: "unknown from.role " + env.From.Role,
			Hint:   Hint{HintText: "from.role must be principal|peer_steward|peer_worker|system"}}
	}
	if !validEnvelopeRole(env.To.Role) {
		return &admissionError{Stage: "validate",
			Reason: "unknown to.role " + env.To.Role,
			Hint:   Hint{HintText: "to.role must be principal|peer_steward|peer_worker|system"}}
	}
	if env.To.Role != RolePeerSteward && env.To.Role != RolePeerWorker {
		return &admissionError{Stage: "validate",
			Reason: "to must be an agent (peer_steward|peer_worker)",
			Hint:   Hint{HintText: "address the message to an agent handle, not the principal or system"}}
	}
	if !validEnvelopeTransport(env.Thread.Transport) {
		return &admissionError{Stage: "validate",
			Reason: "unknown thread.transport " + env.Thread.Transport,
			Hint:   Hint{HintText: "thread.transport must be session|a2a|attention"}}
	}
	return nil
}

// checkRoutingLegality enforces who may address whom with which kind —
// deny rules in the spirit of ADR-016's scope manifest.
func checkRoutingLegality(env MessageEnvelope) *admissionError {
	// A worker may report a result or ask a question; it may not open a
	// directive — directives flow down from the principal / a steward.
	if env.From.Role == RolePeerWorker && env.Kind == KindDirective {
		return &admissionError{Stage: "routing",
			Reason: "a worker may not send a directive",
			Hint:   Hint{HintText: "use kind=report to return a result, or kind=question to ask"}}
	}
	// notification is system-only (ADR-032 D-6).
	if env.Kind == KindNotification && env.From.Role != RoleSystem {
		return &admissionError{Stage: "routing",
			Reason: "notification is system-only",
			Hint:   Hint{HintText: "an agent sends directive|question|report"}}
	}
	return nil
}

// causeResolves reports whether a cause ULID points at a live tasks or
// attention_items row — the two tables a loop-entity is drawn over
// (ADR-034 D-8).
func (s *Server) causeResolves(ctx context.Context, cause string) bool {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM tasks WHERE id = ?)
		     + (SELECT COUNT(*) FROM attention_items WHERE id = ?)`,
		cause, cause).Scan(&n)
	return err == nil && n > 0
}
