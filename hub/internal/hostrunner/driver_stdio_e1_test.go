// Tests for the claude-M2 event-vocabulary wedge (companion
// vision-parity E1): `thought` from thinking blocks, `context_window`
// on usage events, and the `by_model` map projection.
//
// The parity test (profile_parity_test.go) covers legacy-vs-profile
// agreement over the corpus; these cover the SHAPES themselves, which
// parity alone cannot — two translators can agree perfectly on a shape
// that is wrong for the client reading it.
package hostrunner

import (
	"context"
	"testing"

	"github.com/termipod/hub/internal/agentfamilies"
)

func eventsOf(t *testing.T, frame map[string]any) []EmittedEvent {
	t.Helper()
	cap := &capturingPoster{}
	d := newE1Driver(cap)
	d.legacyTranslate(context.Background(), frame)
	return cap.events
}

// newE1Driver builds the minimal driver these tests need, matching the
// sibling convention in translator_modes_test.go (captureLog, not a
// bare slog default — a test that logs to stderr is a test that hides
// its own warnings).
func newE1Driver(cap *capturingPoster) *StdioDriver {
	log, _ := captureLog()
	return &StdioDriver{AgentID: "e1-test", Poster: cap, Log: log}
}

func firstOfKind(events []EmittedEvent, kind string) (EmittedEvent, bool) {
	for _, e := range events {
		if e.Kind == kind {
			return e, true
		}
	}
	return EmittedEvent{}, false
}

// ── (a) thinking → thought ───────────────────────────────────────────

// The shape claude-code 2.1.x actually produces: the block is signed
// for API-side verification and its text is withheld. Before E1 this
// fell to the `default:` arm and shipped as kind=raw — a JSON dump in
// the transcript where the M4 tap, reading the same session's on-disk
// log, showed a "Thinking…" chip.
func TestStdio_ThinkingSignedBecomesMarkerThought(t *testing.T) {
	events := eventsOf(t, map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"id":      "msg_1",
			"content": []any{map[string]any{"type": "thinking", "thinking": "", "signature": "EqQBCkY"}},
		},
	})
	ev, ok := firstOfKind(events, "thought")
	if !ok {
		t.Fatalf("no thought event; got kinds %v", kindsOf(events))
	}
	// Matches drivers/local_log_tail/claude_code/mapper.go's M4 shape —
	// the two rungs of one engine must not describe the same block
	// differently.
	if ev.Payload["text"] != "Thinking…" {
		t.Errorf("text = %v; want the marker label", ev.Payload["text"])
	}
	if ev.Payload["marker_only"] != true {
		t.Errorf("marker_only = %v; want true", ev.Payload["marker_only"])
	}
	if ev.Payload["signature_present"] != true {
		t.Errorf("signature_present = %v; want true", ev.Payload["signature_present"])
	}
	if ev.Payload["message_id"] != "msg_1" {
		t.Errorf("message_id = %v; want msg_1 (transcript pairing)", ev.Payload["message_id"])
	}
	if ev.Producer != "agent" {
		t.Errorf("producer = %q; want agent", ev.Producer)
	}
}

// A build that DOES populate the field must have its reasoning
// forwarded, not replaced by the marker. The M4 mapper discards it
// unconditionally; M2 has the content in hand, so throwing it away
// would be a choice, not a limitation.
func TestStdio_ThinkingWithTextForwardsIt(t *testing.T) {
	events := eventsOf(t, map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"id":      "msg_2",
			"content": []any{map[string]any{"type": "thinking", "thinking": "Check the file first."}},
		},
	})
	ev, ok := firstOfKind(events, "thought")
	if !ok {
		t.Fatalf("no thought event; got kinds %v", kindsOf(events))
	}
	if ev.Payload["text"] != "Check the file first." {
		t.Errorf("text = %v; want the reasoning verbatim", ev.Payload["text"])
	}
	if ev.Payload["marker_only"] != false {
		t.Errorf("marker_only = %v; want false — there IS content", ev.Payload["marker_only"])
	}
	if ev.Payload["signature_present"] != false {
		t.Errorf("signature_present = %v; want false", ev.Payload["signature_present"])
	}
}

// A thinking block must not swallow the blocks around it.
func TestStdio_ThinkingDoesNotDisplaceSiblingBlocks(t *testing.T) {
	events := eventsOf(t, map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"id": "msg_3",
			"content": []any{
				map[string]any{"type": "thinking", "thinking": "", "signature": "s"},
				map[string]any{"type": "text", "text": "Done."},
				map[string]any{"type": "tool_use", "id": "toolu_1", "name": "Read"},
			},
		},
	})
	if got := kindsOf(events); len(got) != 3 ||
		got[0] != "thought" || got[1] != "text" || got[2] != "tool_call" {
		t.Errorf("kinds = %v; want [thought text tool_call] in block order", got)
	}
}

// ── (b) context_window on usage ──────────────────────────────────────

// stream-json's usage block has token counts and no window, so the
// context-fill chip had nothing to divide by and stayed blank on M2.
func TestStdio_UsageGetsContextWindowFromModelTable(t *testing.T) {
	events := eventsOf(t, map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"id":    "msg_4",
			"model": "claude-opus-4-7",
			"usage": map[string]any{"input_tokens": float64(120), "output_tokens": float64(8)},
		},
	})
	ev, ok := firstOfKind(events, "usage")
	if !ok {
		t.Fatalf("no usage event; got kinds %v", kindsOf(events))
	}
	// The value comes from the M4 tap's own table (imported, not copied),
	// so the two rungs agree about the same session.
	if ev.Payload["context_window"] != 1_000_000 {
		t.Errorf("context_window = %v; want 1000000 for opus-4-7", ev.Payload["context_window"])
	}
}

// "Blank > wrong" (D-4): an unrecognised model yields NO field rather
// than a zero, because a zero denominator renders as a wrong percentage
// while a missing one suppresses the chip.
func TestStdio_UnknownModelOmitsContextWindow(t *testing.T) {
	events := eventsOf(t, map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"id":    "msg_5",
			"model": "some-other-vendor-model",
			"usage": map[string]any{"input_tokens": float64(1)},
		},
	})
	ev, _ := firstOfKind(events, "usage")
	if _, has := ev.Payload["context_window"]; has {
		t.Errorf("unknown model stamped context_window = %v; want the field absent", ev.Payload["context_window"])
	}
}

// The engine's own number outranks our table, and it arrives at the END
// of a turn — so it must be remembered for the turns that follow.
func TestStdio_TurnResultTeachesTheContextWindow(t *testing.T) {
	cap := &capturingPoster{}
	d := newE1Driver(cap)
	ctx := context.Background()

	d.legacyTranslate(ctx, map[string]any{
		"type":    "result",
		"subtype": "success",
		"modelUsage": map[string]any{
			// Deliberately NOT what the static table says for this name —
			// the point is that the engine's report wins.
			"claude-opus-4-7": map[string]any{"contextWindow": float64(750_000)},
		},
	})
	d.legacyTranslate(ctx, map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"id":    "msg_6",
			"model": "claude-opus-4-7",
			"usage": map[string]any{"input_tokens": float64(5)},
		},
	})

	ev, ok := firstOfKind(cap.events, "usage")
	if !ok {
		t.Fatalf("no usage event; got kinds %v", kindsOf(cap.events))
	}
	if ev.Payload["context_window"] != 750_000 {
		t.Errorf("context_window = %v; want 750000 learned from turn.result, not the table's 1000000",
			ev.Payload["context_window"])
	}
}

// A window already on the wire is never overwritten — the engine is
// always more authoritative than either of our fallbacks.
func TestStdio_ExistingContextWindowSurvives(t *testing.T) {
	cap := &capturingPoster{}
	d := newE1Driver(cap)
	payload := map[string]any{"model": "claude-opus-4-7", "context_window": 12345}
	d.stampContextWindow(payload)
	if payload["context_window"] != 12345 {
		t.Errorf("context_window = %v; want the frame's own 12345", payload["context_window"])
	}
}

// ── (c) by_model map projection ──────────────────────────────────────

// The profile path passed modelUsage through verbatim, shipping
// claude's camelCase where the typed vocabulary promises ours. Nothing
// errored — the per-model numbers simply read as zero everywhere
// by_model is treated as authoritative (digest fold, insights).
func TestProfile_ByModelIsProjectedToTheTypedShape(t *testing.T) {
	f, ok := agentfamilies.ByName("claude-code")
	if !ok || f.FrameProfile == nil {
		t.Fatal("claude-code frame_profile not embedded")
	}
	events := ApplyProfile(map[string]any{
		"type":    "result",
		"subtype": "success",
		"modelUsage": map[string]any{
			"claude-opus-4-7": map[string]any{
				"inputTokens":              float64(1200),
				"outputTokens":             float64(340),
				"cacheReadInputTokens":     float64(9000),
				"cacheCreationInputTokens": float64(120),
				"costUSD":                  0.42,
				"contextWindow":            float64(1_000_000),
				"maxOutputTokens":          float64(64_000),
			},
		},
	}, f.FrameProfile)

	ev, ok := firstOfKind(events, "turn.result")
	if !ok {
		t.Fatalf("no turn.result; got kinds %v", kindsOf(events))
	}
	byModel, ok := ev.Payload["by_model"].(map[string]any)
	if !ok {
		t.Fatalf("by_model = %#v; want a map", ev.Payload["by_model"])
	}
	// Keys are DATA (model ids) and must survive verbatim; only the
	// values are re-shaped.
	inner, ok := byModel["claude-opus-4-7"].(map[string]any)
	if !ok {
		t.Fatalf("by_model keys = %v; want the model id preserved", byModel)
	}
	for field, want := range map[string]any{
		"input":             float64(1200),
		"output":            float64(340),
		"cache_read":        float64(9000),
		"cache_create":      float64(120),
		"cost_usd":          0.42,
		"context_window":    float64(1_000_000),
		"max_output_tokens": float64(64_000),
	} {
		if inner[field] != want {
			t.Errorf("by_model[model].%s = %v; want %v", field, inner[field], want)
		}
	}
	if _, leaked := inner["inputTokens"]; leaked {
		t.Errorf("engine-native key leaked into the typed payload: %v", inner)
	}
}

// Absent source → the field is omitted, not an empty object. "No
// per-model data" and "per-model data for zero models" are different
// claims, and the hand-written translator omits it too.
func TestProfile_ByModelAbsentWhenSourceMissing(t *testing.T) {
	f, _ := agentfamilies.ByName("claude-code")
	events := ApplyProfile(map[string]any{"type": "result", "subtype": "success"}, f.FrameProfile)
	ev, ok := firstOfKind(events, "turn.result")
	if !ok {
		t.Fatalf("no turn.result; got kinds %v", kindsOf(events))
	}
	if _, has := ev.Payload["by_model"]; has {
		t.Errorf("by_model present with no modelUsage: %v", ev.Payload["by_model"])
	}
}

// One malformed entry must not void the whole map.
func TestProfile_ByModelSkipsNonObjectValues(t *testing.T) {
	f, _ := agentfamilies.ByName("claude-code")
	events := ApplyProfile(map[string]any{
		"type":    "result",
		"subtype": "success",
		"modelUsage": map[string]any{
			"good": map[string]any{"inputTokens": float64(1)},
			"bad":  "not an object",
		},
	}, f.FrameProfile)
	ev, _ := firstOfKind(events, "turn.result")
	byModel, ok := ev.Payload["by_model"].(map[string]any)
	if !ok {
		t.Fatalf("by_model = %#v; want a map", ev.Payload["by_model"])
	}
	if _, has := byModel["bad"]; has {
		t.Errorf("non-object value projected: %v", byModel)
	}
	if _, has := byModel["good"]; !has {
		t.Errorf("good entry dropped alongside the bad one: %v", byModel)
	}
}
