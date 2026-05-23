package hostrunner

import (
	"encoding/json"
	"strings"
)

// Driver-side rendering of the ADR-032 message envelope.
//
// The hub composes every input.text event's payload as a flat message
// envelope {from,to,kind,text,cause,thread}. The host-runner only reads
// it: renderInboundEnvelope turns the envelope into an unambiguous,
// engine-facing text turn — naming the sender, the message kind, and the
// reply mechanism — and drops a self-echo (an agent's own message routed
// back to itself). The rendered text replaces payload["body"], the field
// every driver's `case "text"` branch already reads, so the drivers
// themselves need no envelope knowledge.

// inboundEnvelope mirrors the hub-composed MessageEnvelope as it arrives
// on an input.text payload. Read-only on the host-runner side.
type inboundEnvelope struct {
	From   envEndpoint `json:"from"`
	To     envEndpoint `json:"to"`
	Kind   string      `json:"kind"`
	Text   string      `json:"text"`
	Cause  string      `json:"cause"`
	Thread envThread   `json:"thread"`
}

type envEndpoint struct {
	Role    string `json:"role"`
	Handle  string `json:"handle"`
	AgentID string `json:"agent_id"`
}

type envThread struct {
	Transport string `json:"transport"`
	ID        string `json:"id"`
}

// renderInboundEnvelope parses the message envelope from a raw input.text
// payload and returns the engine-facing text turn plus a self-echo flag.
// A payload carrying no envelope (from.role empty — a legacy/malformed
// row) falls back to its plain `text` field so the engine still receives
// something rather than nothing.
func renderInboundEnvelope(raw json.RawMessage) (text string, selfEcho bool) {
	var e inboundEnvelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return "", false
	}
	if e.From.Role == "" {
		return e.Text, false
	}
	if isSelfEcho(e) {
		return "", true
	}
	return renderEnvelopeTurn(e), false
}

// isSelfEcho reports whether the envelope's sender and recipient are the
// same agent — a message an agent addressed to itself. Matched by handle
// (the A2A from-endpoint carries no agent_id) and by agent_id as a
// fallback.
func isSelfEcho(e inboundEnvelope) bool {
	if e.From.Handle != "" && e.From.Handle == e.To.Handle {
		return true
	}
	if e.From.AgentID != "" && e.From.AgentID == e.To.AgentID {
		return true
	}
	return false
}

// deriveReplyVia computes the reply channel from the envelope (ADR-032
// D-5) — it is not a stored field. A notification routes no reply; an
// A2A message is answered over A2A; an attention message via its reply;
// everything else replies in the agent's own chat.
func deriveReplyVia(kind, transport string) string {
	if kind == "notification" {
		return "none"
	}
	switch transport {
	case "a2a":
		return "a2a"
	case "attention":
		return "attention_reply"
	default:
		return "chat"
	}
}

// senderDescription names the envelope sender for the rendered header.
func senderDescription(from envEndpoint) string {
	switch from.Role {
	case "principal":
		return "the principal"
	case "system":
		return "the system"
	case "peer_steward":
		return atHandle(from.Handle) + " (a peer steward)"
	case "peer_worker":
		return atHandle(from.Handle) + " (a peer worker)"
	default:
		return atHandle(from.Handle)
	}
}

func atHandle(h string) string {
	if h == "" {
		return "an agent"
	}
	return "@" + strings.TrimPrefix(h, "@")
}

// replyInstruction renders the contract for how — and whether — to reply,
// per the derived reply_via. A notification states the kind's contract
// (act, do not reply), never a bald "no reply expected".
func replyInstruction(e inboundEnvelope, replyVia string) string {
	switch replyVia {
	case "a2a":
		return "To respond, send an A2A message back: a2a.invoke(handle=\"" +
			strings.TrimPrefix(e.From.Handle, "@") + "\", kind=\"report\")."
	case "attention_reply":
		return "To respond, resolve the originating request."
	case "none":
		return "Informational — no reply is routed. Act on it if it concerns work you own."
	default: // chat
		// Softened from the v1.0.648-and-earlier "Reply in this chat
		// when you have a result." That wording implied every message
		// required a substantive result and pushed agents (especially
		// agy with --dangerously-skip-permissions) into deep
		// investigation on casual greetings. The match-the-ask
		// guidance now lives in the prompt; the envelope just says
		// "reply when ready" without prescribing a "result".
		return "Reply in this chat. Match the response to the ask — " +
			"a brief acknowledgement is fine when the directive isn't a task."
	}
}

// renderEnvelopeTurn formats the full engine-facing turn: a header naming
// the sender and kind, the message text, and the reply instruction.
func renderEnvelopeTurn(e inboundEnvelope) string {
	kind := e.Kind
	if kind == "" {
		kind = "message"
	}
	replyVia := deriveReplyVia(e.Kind, e.Thread.Transport)

	var b strings.Builder
	b.WriteString("[")
	b.WriteString(kind)
	b.WriteString(" from ")
	b.WriteString(senderDescription(e.From))
	b.WriteString("]\n")
	b.WriteString(e.Text)
	b.WriteString("\n\n")
	b.WriteString(replyInstruction(e, replyVia))
	return b.String()
}
