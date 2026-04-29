// Profile-driven frame translation (ADR-010, plan Phase 1.3).
//
// `ApplyProfile` is the pure-data sibling of driver_stdio.go's
// translate(): same input (one decoded stream-json frame), same
// conceptual output (zero-or-more agent_event rows), but driven by a
// declarative *.FrameProfile instead of hand-written field paths.
//
// The function is exported and side-effect-free so the parity test
// (Phase 1.5) can run both translators against a recorded corpus and
// diff their outputs row-by-row. The driver wires the result into
// PostAgentEvent calls; ApplyProfile itself doesn't touch I/O.
package hostrunner

import (
	"github.com/termipod/hub/internal/agentfamilies"
	"github.com/termipod/hub/internal/hostrunner/profile_eval"
)

// EmittedEvent is the agent_event a profile rule produced. Mirrors
// the (kind, producer, payload) tuple PostAgentEvent takes; we don't
// build the full DB row here because timestamps + seq are the
// driver's job.
type EmittedEvent struct {
	Kind     string
	Producer string
	Payload  map[string]any
}

// ApplyProfile evaluates `profile` against `frame` and returns the
// emitted events.
//
// Dispatch semantics: **most-specific match wins**. Among all rules
// whose match-predicate is satisfied, only those tied for the largest
// match-keyset size fire (in declaration order). This lets a profile:
//
//   - Stack rules with progressively more keys for hierarchical
//     dispatch — `{type: system, subtype: init}` beats
//     `{type: system}` beats `{}` for an init frame.
//   - Run multiple rules with the *same* match — an assistant frame
//     fires both its `for_each` over content blocks and its
//     `when_present`-gated usage rule, both with `{type: assistant}`.
//
// Nil profile or zero matching rules → `kind=raw, producer=agent,
// payload=verbatim` fallback (D5). A rule that matches but produces
// no events (e.g. when_present gated to nil) is *not* the fallback
// case — the author chose to skip; respect it.
func ApplyProfile(frame map[string]any, profile *agentfamilies.FrameProfile) []EmittedEvent {
	if profile == nil || len(profile.Rules) == 0 {
		return []EmittedEvent{rawFallback(frame)}
	}
	var winners []agentfamilies.Rule
	bestSize := -1
	for _, rule := range profile.Rules {
		if !matchesAll(rule.Match, frame) {
			continue
		}
		size := len(rule.Match)
		if size > bestSize {
			bestSize = size
			winners = []agentfamilies.Rule{rule}
		} else if size == bestSize {
			winners = append(winners, rule)
		}
	}
	if bestSize < 0 {
		return []EmittedEvent{rawFallback(frame)}
	}
	var out []EmittedEvent
	for _, rule := range winners {
		out = append(out, applyRule(rule, frame, nil)...)
	}
	return out
}

// applyRule fires one rule against the given (inner, outer) scope.
// Three shapes:
//
//   - WhenPresent set + non-nil  → fall through to plain emit
//   - WhenPresent set + nil      → no-op (the rule is gated)
//   - ForEach set                → iterate the resolved array,
//     evaluating sub_rules per element with the array element as
//     inner and the current frame as outer. Sub-rules respect their
//     own match predicates so a single for_each can dispatch across
//     {text, tool_use, …} block types.
//   - neither                    → single emit at the rule's level
//
// Returns the events the rule produced (possibly empty).
func applyRule(rule agentfamilies.Rule, inner, outer map[string]any) []EmittedEvent {
	if rule.WhenPresent != "" {
		if profile_eval.Eval(rule.WhenPresent, inner, outer) == nil {
			return nil
		}
	}
	if rule.ForEach != "" {
		raw := profile_eval.Eval(rule.ForEach, inner, outer)
		arr, ok := raw.([]any)
		if !ok {
			return nil
		}
		var out []EmittedEvent
		// `inner` becomes the new outer scope for each iteration; the
		// per-element value is the new inner. Sub-rules see $.foo
		// against the element and $$.bar against the parent frame.
		for _, item := range arr {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if len(rule.SubRules) == 0 {
				// No sub-rules → the for_each rule's own emit fires
				// per element. (Less common but supported.)
				if rule.Emit.Kind != "" {
					out = append(out, buildEmit(rule.Emit, itemMap, inner))
				}
				continue
			}
			for _, sub := range rule.SubRules {
				if !matchesAll(sub.Match, itemMap) {
					continue
				}
				out = append(out, applyRule(sub, itemMap, inner)...)
				break // first sub-rule match wins per element
			}
		}
		return out
	}
	if rule.Emit.Kind == "" {
		return nil
	}
	return []EmittedEvent{buildEmit(rule.Emit, inner, outer)}
}

// matchesAll returns true when every key in `match` literal-equals
// the corresponding top-level field of `frame`. Empty match matches
// any frame. Type-mismatches (e.g. match expects "assistant" but
// frame[type] is a number) return false rather than panicking.
//
// Only top-level fields are checked — nested matches aren't part of
// the v1 grammar. Rules that need to dispatch on nested shape do so
// via for_each + sub_rules.
func matchesAll(match map[string]any, frame map[string]any) bool {
	if len(match) == 0 {
		return true
	}
	for k, want := range match {
		got, present := frame[k]
		if !present {
			return false
		}
		if got != want {
			return false
		}
	}
	return true
}

// buildEmit resolves a rule's emit declaration into a concrete
// EmittedEvent by evaluating each payload expression. Producer
// defaults to "agent" — the kind that an agent-output frame has been
// historically attributed to. Profiles can override per rule (e.g.
// system lifecycle frames mark themselves producer=system).
func buildEmit(emit agentfamilies.Emit, inner, outer map[string]any) EmittedEvent {
	producer := emit.Producer
	if producer == "" {
		producer = "agent"
	}
	payload := make(map[string]any, len(emit.Payload))
	for k, expr := range emit.Payload {
		payload[k] = profile_eval.Eval(expr, inner, outer)
	}
	return EmittedEvent{
		Kind:     emit.Kind,
		Producer: producer,
		Payload:  payload,
	}
}

// rawFallback is the no-rule-matched event. Identical to
// driver_stdio.go's default branch — kind=raw, producer=agent, the
// frame as-is. ADR-010 D5: profiles aren't required to declare a
// catch-all because operators want forward-compatibility with
// unprofiled SDK frame types.
func rawFallback(frame map[string]any) EmittedEvent {
	return EmittedEvent{
		Kind:     "raw",
		Producer: "agent",
		Payload:  frame,
	}
}
