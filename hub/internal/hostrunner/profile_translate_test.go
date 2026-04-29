package hostrunner

import (
	"reflect"
	"testing"

	"github.com/termipod/hub/internal/agentfamilies"
)

// TestApplyProfile_NilProfile — without a profile, every frame falls
// through to kind=raw verbatim. This is the steady state for engines
// we haven't profiled yet (codex / gemini-cli / aider in v1).
func TestApplyProfile_NilProfile(t *testing.T) {
	frame := map[string]any{"type": "anything", "x": 1}
	got := ApplyProfile(frame, nil)
	if len(got) != 1 {
		t.Fatalf("got %d events; want 1 raw fallback", len(got))
	}
	if got[0].Kind != "raw" || got[0].Producer != "agent" {
		t.Errorf("fallback shape = %+v; want kind=raw producer=agent", got[0])
	}
	if !reflect.DeepEqual(got[0].Payload, frame) {
		t.Errorf("fallback payload should be the frame verbatim; got %v", got[0].Payload)
	}
}

// TestApplyProfile_NoMatch — profile present but no rule fires →
// raw fallback (D5). Critical: the user keeps the bytes for any
// SDK frame type we haven't written a rule for yet.
func TestApplyProfile_NoMatch(t *testing.T) {
	profile := &agentfamilies.FrameProfile{
		ProfileVersion: 1,
		Rules: []agentfamilies.Rule{
			{
				Match: map[string]any{"type": "assistant"},
				Emit:  agentfamilies.Emit{Kind: "text", Producer: "agent"},
			},
		},
	}
	frame := map[string]any{"type": "rate_limit_event"}
	got := ApplyProfile(frame, profile)
	if len(got) != 1 || got[0].Kind != "raw" {
		t.Errorf("unmatched frame should fall through to raw; got %+v", got)
	}
}

// TestApplyProfile_SimpleMatch — one rule matches via top-level
// type, payload expressions resolve from inner scope. The
// rate_limit_info nested-field dig (the bug v1.0.328 fixed in Go)
// expressed as a profile rule for symmetry with what we'll author
// in Phase 1.4.
func TestApplyProfile_SimpleMatch(t *testing.T) {
	profile := &agentfamilies.FrameProfile{
		ProfileVersion: 1,
		Rules: []agentfamilies.Rule{
			{
				Match: map[string]any{"type": "rate_limit_event"},
				Emit: agentfamilies.Emit{
					Kind:     "rate_limit",
					Producer: "agent",
					Payload: map[string]string{
						"window":    "$.rate_limit_info.rateLimitType || $.rateLimitType",
						"status":    "$.rate_limit_info.status || $.status",
						"resets_at": "$.rate_limit_info.resetsAt || $.resetsAt",
					},
				},
			},
		},
	}
	frame := map[string]any{
		"type": "rate_limit_event",
		"rate_limit_info": map[string]any{
			"rateLimitType": "five_hour",
			"status":        "allowed",
			"resetsAt":      float64(1777443000),
		},
	}
	got := ApplyProfile(frame, profile)
	if len(got) != 1 {
		t.Fatalf("got %d events; want 1", len(got))
	}
	e := got[0]
	if e.Kind != "rate_limit" || e.Producer != "agent" {
		t.Errorf("kind/producer = %q/%q; want rate_limit/agent", e.Kind, e.Producer)
	}
	wantPayload := map[string]any{
		"window":    "five_hour",
		"status":    "allowed",
		"resets_at": float64(1777443000),
	}
	if !reflect.DeepEqual(e.Payload, wantPayload) {
		t.Errorf("payload = %v; want %v", e.Payload, wantPayload)
	}

	// Same rule, but the frame uses the legacy flat shape — coalesce
	// must still resolve. This is exactly the parity surface that
	// motivated ADR-010.
	flat := map[string]any{
		"type":          "rate_limit_event",
		"rateLimitType": "five_hour",
		"status":        "allowed",
		"resetsAt":      float64(1777443000),
	}
	gotFlat := ApplyProfile(flat, profile)
	if !reflect.DeepEqual(gotFlat[0].Payload, wantPayload) {
		t.Errorf("flat-shape payload = %v; want same as nested %v",
			gotFlat[0].Payload, wantPayload)
	}
}

// TestApplyProfile_ForEachWithSubRules — claude's
// assistant.message.content[] dispatches per block type. Each text
// block becomes a kind=text event; each tool_use becomes a
// kind=tool_call. Outer scope (the assistant message) is reachable
// via $$ for message_id propagation.
func TestApplyProfile_ForEachWithSubRules(t *testing.T) {
	profile := &agentfamilies.FrameProfile{
		ProfileVersion: 1,
		Rules: []agentfamilies.Rule{
			{
				Match:   map[string]any{"type": "assistant"},
				ForEach: "$.message.content",
				SubRules: []agentfamilies.Rule{
					{
						Match: map[string]any{"type": "text"},
						Emit: agentfamilies.Emit{
							Kind:     "text",
							Producer: "agent",
							Payload: map[string]string{
								"text":       "$.text",
								"message_id": "$$.message.id",
							},
						},
					},
					{
						Match: map[string]any{"type": "tool_use"},
						Emit: agentfamilies.Emit{
							Kind:     "tool_call",
							Producer: "agent",
							Payload: map[string]string{
								"id":    "$.id",
								"name":  "$.name",
								"input": "$.input",
							},
						},
					},
				},
			},
		},
	}
	frame := map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"id": "msg_42",
			"content": []any{
				map[string]any{"type": "text", "text": "hello"},
				map[string]any{
					"type": "tool_use",
					"id":   "toolu_1",
					"name": "Read",
					"input": map[string]any{
						"file_path": "/etc/hosts",
					},
				},
				// An unknown block type → no sub-rule matches → silently
				// dropped. The legacy translator emits raw; future ADR
				// could harden this but per D5 silent-drop only applies
				// to the *outer* fallback, not inside for_each. For v1
				// we keep parity-friendly behavior: dropped blocks need
				// their own sub-rule.
				map[string]any{"type": "thinking", "text": "..."},
			},
		},
	}
	got := ApplyProfile(frame, profile)
	if len(got) != 2 {
		t.Fatalf("got %d events; want 2 (text + tool_call)", len(got))
	}
	if got[0].Kind != "text" || got[0].Payload["text"] != "hello" {
		t.Errorf("text event wrong: %+v", got[0])
	}
	if got[0].Payload["message_id"] != "msg_42" {
		t.Errorf("text event message_id = %v; want msg_42 (outer scope)",
			got[0].Payload["message_id"])
	}
	if got[1].Kind != "tool_call" || got[1].Payload["id"] != "toolu_1" {
		t.Errorf("tool_call event wrong: %+v", got[1])
	}
}

// TestApplyProfile_FirstMatchWins — when two top-level rules could
// both match, only the first fires. This is how profiles dispatch
// system.subtype=init vs system.subtype=rate_limit_event vs the
// generic system fallback: order them most-specific-first.
//
// Match here uses two keys ANDed (type + subtype) to disambiguate.
func TestApplyProfile_FirstMatchWins(t *testing.T) {
	profile := &agentfamilies.FrameProfile{
		ProfileVersion: 1,
		Rules: []agentfamilies.Rule{
			{
				Match: map[string]any{"type": "system", "subtype": "init"},
				Emit:  agentfamilies.Emit{Kind: "session.init"},
			},
			{
				Match: map[string]any{"type": "system", "subtype": "rate_limit_event"},
				Emit:  agentfamilies.Emit{Kind: "rate_limit"},
			},
			{
				Match: map[string]any{"type": "system"},
				Emit:  agentfamilies.Emit{Kind: "system"},
			},
		},
	}
	cases := []struct {
		name     string
		frame    map[string]any
		wantKind string
	}{
		{
			name:     "init",
			frame:    map[string]any{"type": "system", "subtype": "init"},
			wantKind: "session.init",
		},
		{
			name:     "rate_limit_event_under_system",
			frame:    map[string]any{"type": "system", "subtype": "rate_limit_event"},
			wantKind: "rate_limit",
		},
		{
			name:     "other_system_subtype_falls_to_generic",
			frame:    map[string]any{"type": "system", "subtype": "task_started"},
			wantKind: "system",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ApplyProfile(c.frame, profile)
			if len(got) != 1 || got[0].Kind != c.wantKind {
				t.Errorf("got %+v; want kind=%q", got, c.wantKind)
			}
		})
	}
}

// TestApplyProfile_WhenPresent — gates an emit on a non-nil
// expression. Used so an `assistant` frame that lacks
// message.usage doesn't emit a usage event with all-nil fields.
func TestApplyProfile_WhenPresent(t *testing.T) {
	profile := &agentfamilies.FrameProfile{
		ProfileVersion: 1,
		Rules: []agentfamilies.Rule{
			{
				Match:       map[string]any{"type": "assistant"},
				WhenPresent: "$.message.usage",
				Emit: agentfamilies.Emit{
					Kind: "usage",
					Payload: map[string]string{
						"input_tokens":  "$.message.usage.input_tokens",
						"output_tokens": "$.message.usage.output_tokens",
					},
				},
			},
		},
	}
	with := map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"usage": map[string]any{
				"input_tokens":  float64(120),
				"output_tokens": float64(40),
			},
		},
	}
	got := ApplyProfile(with, profile)
	if len(got) != 1 || got[0].Kind != "usage" {
		t.Fatalf("with-usage frame: got %+v", got)
	}

	without := map[string]any{
		"type":    "assistant",
		"message": map[string]any{"id": "msg_no_usage"},
	}
	got = ApplyProfile(without, profile)
	// Without usage, the rule matched at top-level (type=assistant)
	// but the when_present gate fired — the author opted out, so we
	// emit nothing rather than falling back to raw. Raw fallback is
	// reserved for "no rule's match-predicate was satisfied at all"
	// (D5 of ADR-010, interpreted strictly).
	if len(got) != 0 {
		t.Errorf("when_present gate should suppress emit; got %+v", got)
	}
}

// TestApplyProfile_MultipleRulesWithSameMatchAllFire — claude's
// assistant frame needs both a `for_each content` rule (per-block
// emits) and a `when_present message.usage` rule (telemetry). They
// share `match: {type: assistant}` so first-match-wins would block
// the second; most-specific-tie semantics fire both in declaration
// order.
func TestApplyProfile_MultipleRulesWithSameMatchAllFire(t *testing.T) {
	profile := &agentfamilies.FrameProfile{
		ProfileVersion: 1,
		Rules: []agentfamilies.Rule{
			{
				Match:   map[string]any{"type": "assistant"},
				ForEach: "$.message.content",
				SubRules: []agentfamilies.Rule{
					{
						Match: map[string]any{"type": "text"},
						Emit:  agentfamilies.Emit{Kind: "text", Payload: map[string]string{"text": "$.text"}},
					},
				},
			},
			{
				Match:       map[string]any{"type": "assistant"},
				WhenPresent: "$.message.usage",
				Emit: agentfamilies.Emit{
					Kind: "usage",
					Payload: map[string]string{
						"input_tokens": "$.message.usage.input_tokens",
					},
				},
			},
		},
	}
	frame := map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"id": "msg_1",
			"content": []any{
				map[string]any{"type": "text", "text": "hi"},
			},
			"usage": map[string]any{"input_tokens": float64(10)},
		},
	}
	got := ApplyProfile(frame, profile)
	if len(got) != 2 {
		t.Fatalf("got %d events; want 2 (text + usage)", len(got))
	}
	if got[0].Kind != "text" {
		t.Errorf("event[0].kind = %q; want text", got[0].Kind)
	}
	if got[1].Kind != "usage" {
		t.Errorf("event[1].kind = %q; want usage", got[1].Kind)
	}
}
