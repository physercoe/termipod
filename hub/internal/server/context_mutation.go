package server

import "strings"

// contextMutation describes a slash command that mutates engine-side
// conversation state but emits no observable frame back to the hub.
// ADR-014 OQ-4: the hub transcript is the operation log — when the
// user triggers `/compact`, `/clear`, `/rewind` (claude) or
// `/compress` (gemini), the engine's view of the conversation
// silently diverges from `agent_events`. We can't observe the
// mutation in the engine's stream, but we *can* observe the user's
// command on the input route, so we emit a typed marker event
// (`producer=system`, `kind=context.<verb>`) right after the input
// row. Mobile renders these as inline chips so the operator sees
// "this is where the engine context truncated, even though the
// transcript continues."
//
// The marker is best-effort and pre-engine: the hub records the
// user's *intent*, not the engine's confirmation. If the engine
// doesn't recognise the command (e.g. `/rewind` against an older
// claude build) the marker is still emitted — that's correct,
// because the user typed it and the operator should see what was
// attempted, not just what succeeded.
type contextMutation struct {
	// Kind is the typed agent_event kind to emit
	// (`context.compacted` etc.). Stable wire vocabulary — mobile
	// clients render off this.
	Kind string
	// Verb is the human-readable noun for the operation
	// (`compact`, `clear`, `rewind`, `compress`). Used in the
	// marker payload so the renderer can label the chip without
	// having to map kind → verb itself.
	Verb string
}

// detectContextMutation inspects an input.text body for a leading
// slash command that mutates engine context. Returns the mutation
// descriptor + true when one matches, or zero + false when the body
// is regular text. agentKind selects the per-engine command set —
// claude's `/compact` and gemini's `/compress` are different verbs
// for the same operation, so they map to different `kind` values.
//
// Match rules:
//   - body is trimmed before inspection.
//   - the slash command must be the leading token (followed by
//     whitespace or end-of-body); discussing `/compact` mid-sentence
//     does not trigger the marker.
//   - matching is case-sensitive; claude's REPL is case-sensitive,
//     so we follow.
//   - unknown engines fall through to the empty match — no
//     speculative markers for kinds we haven't audited.
func detectContextMutation(agentKind, body string) (contextMutation, bool) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" || trimmed[0] != '/' {
		return contextMutation{}, false
	}
	// Extract the leading slash-token; everything up to the first
	// whitespace.
	end := len(trimmed)
	for i := 0; i < len(trimmed); i++ {
		c := trimmed[i]
		if c == ' ' || c == '\t' || c == '\n' {
			end = i
			break
		}
	}
	token := trimmed[:end]

	switch agentKind {
	case "claude-code":
		switch token {
		case "/compact":
			return contextMutation{Kind: "context.compacted", Verb: "compact"}, true
		case "/clear":
			return contextMutation{Kind: "context.cleared", Verb: "clear"}, true
		case "/rewind":
			return contextMutation{Kind: "context.rewound", Verb: "rewind"}, true
		}
	case "gemini-cli":
		switch token {
		case "/compress":
			return contextMutation{Kind: "context.compacted", Verb: "compress"}, true
		case "/clear":
			return contextMutation{Kind: "context.cleared", Verb: "clear"}, true
		}
	}
	return contextMutation{}, false
}
