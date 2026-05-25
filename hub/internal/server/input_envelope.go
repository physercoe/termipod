package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/termipod/hub/internal/envelope"
)

// The L3 orchestration message contract (ADR-032).
//
// Every message crossing an agent boundary — principal→agent, agent→agent
// (A2A), agent→principal, system→agent — is composed by hub-server as a
// MessageEnvelope and marshaled as the agent_events payload *itself*: the
// six envelope fields sit at the payload top level, not nested under
// payload["body"] / payload["envelope"]. Envelope-aware consumers read
// payload["text"]; extra per-input fields (images, pdfs, …) ride alongside
// the envelope fields on the same flat map.
//
// composeMessage is the single authoring point (ADR-032 D-6) — agents
// author no envelope fields; the hub stamps the whole structure.

// Envelope kinds — the closed four-value enum (ADR-032 D-2). Illocutionary
// force: two openers, one closer, one neutral.
const (
	KindDirective    = "directive"    // opens a loop
	KindQuestion     = "question"     // opens a blocking sub-loop
	KindReport       = "report"       // advances or closes a loop
	KindNotification = "notification" // loop-neutral
)

// Endpoint roles — source-of-message semantics, orthogonal to L1's
// turn-position user/assistant (ADR-032 D-1).
const (
	RolePrincipal   = "principal"
	RolePeerSteward = "peer_steward"
	RolePeerWorker  = "peer_worker"
	RoleSystem      = "system"
)

// Transport kinds for thread correlation (ADR-032 D-4).
const (
	TransportSession   = "session"
	TransportA2A       = "a2a"
	TransportAttention = "attention"
)

// MessageEndpoint identifies a sender or receiver. role is always set;
// handle / agent_id are set when the endpoint is a concrete agent.
type MessageEndpoint struct {
	Role    string `json:"role"`
	Handle  string `json:"handle,omitempty"`
	AgentID string `json:"agent_id,omitempty"`
}

// MessageThread is transport correlation — which conversational channel a
// message rides. Orthogonal to cause (which carries all lineage).
type MessageThread struct {
	Transport string `json:"transport"`
	ID        string `json:"id"`
}

// MessageEnvelope is the L3 message contract (ADR-032 D-1). Cause is the
// lineage reference (the ULID of the directive/task the message serves);
// "" means the message is not tied to a tracked directive.
type MessageEnvelope struct {
	From   MessageEndpoint `json:"from"`
	To     MessageEndpoint `json:"to"`
	Kind   string          `json:"kind"`
	Text   string          `json:"text"`
	Cause  string          `json:"cause,omitempty"`
	Thread MessageThread   `json:"thread"`
}

// validEnvelopeKind reports whether k is one of the closed four envelope kinds.
func validEnvelopeKind(k string) bool {
	switch k {
	case KindDirective, KindQuestion, KindReport, KindNotification:
		return true
	}
	return false
}

// validEnvelopeRole reports whether r is one of the four endpoint roles.
func validEnvelopeRole(r string) bool {
	switch r {
	case RolePrincipal, RolePeerSteward, RolePeerWorker, RoleSystem:
		return true
	}
	return false
}

// validEnvelopeTransport reports whether t is one of the three transports.
func validEnvelopeTransport(t string) bool {
	switch t {
	case TransportSession, TransportA2A, TransportAttention:
		return true
	}
	return false
}

// composeMessage is the single envelope-authoring point (ADR-032 D-6).
// hub-server callers build the endpoints, choose the kind, and pass the
// agent's text; composeMessage stamps the envelope. An unknown kind falls
// back to notification — never directive (ADR-032 D-2) — so a caller bug
// can never silently mint new loops.
func composeMessage(from, to MessageEndpoint, kind, text, cause string, thread MessageThread) MessageEnvelope {
	if !validEnvelopeKind(kind) {
		kind = KindNotification
	}
	return MessageEnvelope{
		From:   from,
		To:     to,
		Kind:   kind,
		Text:   text,
		Cause:  cause,
		Thread: thread,
	}
}

// PayloadMap renders the envelope as the flat agent_events payload map.
// Callers extend the returned map with per-input fields (images, pdfs, …)
// before marshaling — the envelope fields and the extras share one flat
// JSON object. cause is omitted when untied.
func (e MessageEnvelope) PayloadMap() map[string]any {
	m := map[string]any{
		"from":   e.From,
		"to":     e.To,
		"kind":   e.Kind,
		"text":   e.Text,
		"thread": e.Thread,
	}
	if e.Cause != "" {
		m["cause"] = e.Cause
	}
	return m
}

// parseEnvelope unmarshals an agent_events payload back into a
// MessageEnvelope. Extra flat fields (images, …) are ignored. Reused by
// the driver-side render (A3) and the admission pipeline (A4).
func parseEnvelope(payload []byte) (MessageEnvelope, error) {
	var e MessageEnvelope
	err := json.Unmarshal(payload, &e)
	return e, err
}

// roleForKind maps an agent's `kind` to its envelope endpoint role.
// Stewards (kind `steward.*`) are peer_steward; every other engine is a
// peer_worker. Mirrors the steward-detection-by-kind rule used elsewhere.
func roleForKind(kind string) string {
	if strings.HasPrefix(kind, "steward.") {
		return RolePeerSteward
	}
	return RolePeerWorker
}

// systemEndpoint is the envelope endpoint for hub-originated messages.
func systemEndpoint() MessageEndpoint {
	return MessageEndpoint{Role: RoleSystem}
}

// endpointForAgent resolves an agent id to a MessageEndpoint, looking up
// its handle and role. Best-effort: an unknown agent yields a peer_worker
// endpoint carrying just the id.
func (s *Server) endpointForAgent(ctx context.Context, agentID string) MessageEndpoint {
	ep := MessageEndpoint{Role: RolePeerWorker, AgentID: agentID}
	var kind, handle sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(kind,''), COALESCE(handle,'') FROM agents WHERE id = ?`,
		agentID).Scan(&kind, &handle); err != nil {
		return ep
	}
	ep.Role = roleForKind(kind.String)
	ep.Handle = strings.TrimPrefix(handle.String, "@")
	return ep
}

// composeTextInputEnvelope builds the envelope for an input.text event
// landing via handlePostAgentInput. producer "a2a" → a peer-originated
// message whose from-role/kind/cause were stamped by the A2A relay and
// carried across the tunnel; anything else → a principal directive.
func (s *Server) composeTextInputEnvelope(ctx context.Context, in *agentInputIn, producer, agentID, sessionID string) MessageEnvelope {
	to := s.endpointForAgent(ctx, agentID)
	if producer == "a2a" {
		from := MessageEndpoint{Role: in.FromRole, Handle: in.FromHandle}
		if !validEnvelopeRole(from.Role) {
			from.Role = RolePeerWorker
		}
		kind := in.A2AKind
		if kind == "" {
			kind = KindDirective // a bare A2A dispatch defaults to a directive
		}
		return composeMessage(from, to, kind, in.Body, in.Cause,
			MessageThread{Transport: TransportA2A, ID: sessionID})
	}
	// producer "user" / "" — principal direct input.
	return composeMessage(
		MessageEndpoint{Role: RolePrincipal}, to,
		KindDirective, in.Body, in.Cause,
		MessageThread{Transport: TransportSession, ID: sessionID})
}

// renderEnvelopeForDriver produces the engine-facing prose for a
// MessageEnvelope using the operator-editable templates (ADR-032 D-10).
// The result is stamped onto payload["rendered_text"] alongside the
// structured envelope fields so the host-runner can prefer the
// pre-rendered string without re-doing the work + re-parsing the
// templates on its own filesystem (which it doesn't have — see
// blueprint §3.2 on the hub / host-runner process split).
//
// Empty string when the loader is nil (test paths that haven't wired
// it) or when the envelope's `from.role` is empty (legacy / malformed
// row; host-runner's no-envelope fallback handles those).
func (s *Server) renderEnvelopeForDriver(env MessageEnvelope) string {
	if s.envelope == nil {
		return ""
	}
	if env.From.Role == "" {
		return ""
	}
	tpl := s.envelope.Resolve()
	return tpl.Render(envelope.Message{
		Kind:       env.Kind,
		FromRole:   env.From.Role,
		FromHandle: env.From.Handle,
		Transport:  env.Thread.Transport,
		Text:       env.Text,
	})
}

// renderEnvelopeSenderLabel returns the operator-template-resolved
// human-readable sender description (e.g. "the principal",
// "@worker-a (a peer worker)") for an envelope. Stamped onto
// payload["from_label"] so the mobile feed's
// `[from: <label>]` row reflects YAML edits without round-tripping
// the rendering logic through a parallel hardcoded Dart map. The
// mobile side falls back to its own static mapping when this field
// is absent (legacy events, hot-reload-unaware tests, A2A relay
// paths that don't pass through this hub handler).
//
// Empty string when the loader is nil or the envelope's role is
// empty — same fall-through invariants as renderEnvelopeForDriver.
func (s *Server) renderEnvelopeSenderLabel(env MessageEnvelope) string {
	if s.envelope == nil {
		return ""
	}
	if env.From.Role == "" {
		return ""
	}
	return s.envelope.Resolve().RenderSender(env.From.Role, env.From.Handle)
}
