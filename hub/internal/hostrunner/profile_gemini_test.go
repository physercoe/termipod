package hostrunner

import (
	"path/filepath"
	"testing"

	"github.com/termipod/hub/internal/agentfamilies"
)

// TestProfile_Gemini_TranslatesStreamJSON drives the gemini-cli frame
// profile (ADR-013) through a representative corpus of stream-json
// events and asserts the emitted agent_event kinds. Like codex, there
// is no legacy translator to parity-check against — for gemini the
// profile is authoritative — so this test is expectation-based.
//
// Operator workflow for extending coverage:
//
//  1. Run a real `gemini -p <text> --output-format stream-json` session
//     and append captured events to
//     hub/internal/hostrunner/testdata/profiles/gemini/corpus.jsonl.
//  2. Add the expected (type, role, delta) → kind to wantKinds below,
//     or — for unprofiled types that should fall through to kind=raw —
//     leave it out of wantKinds (the test will accept the raw default).
//  3. Re-run: go test ./internal/hostrunner/ -run Gemini_Translates -v
func TestProfile_Gemini_TranslatesStreamJSON(t *testing.T) {
	corpusPath := filepath.Join(
		"testdata", "profiles", "gemini", "corpus.jsonl")
	corpus := readCorpus(t, corpusPath)
	if len(corpus) == 0 {
		t.Fatalf("corpus %q is empty", corpusPath)
	}

	f, ok := agentfamilies.ByName("gemini-cli")
	if !ok || f.FrameProfile == nil {
		t.Fatal("gemini-cli frame_profile not embedded — slice 2 should have shipped it")
	}
	profile := f.FrameProfile

	// (type, role, delta) → expected emitted kind.
	// role and delta are "" / nil for events that don't carry them.
	type matcher struct {
		eventType string
		role      string
		delta     any // bool or nil
	}
	wantKinds := map[matcher]string{
		{"init", "", nil}:                 "session.init",
		{"message", "user", false}:        "raw", // user echoes fall through
		// Assistant delta frames are handled by ExecResumeDriver's
		// streamFrames accumulator, not by the profile — the evaluator
		// is stateless and gemini's chunks are incremental, not
		// cumulative. Both delta=true and delta=false fall through to
		// kind=raw at the profile layer; the driver's special-case
		// produces the user-visible cumulative text events.
		{"message", "assistant", true}:    "raw",
		{"message", "assistant", false}:   "raw",
		{"tool_use", "", nil}:             "tool_call",
		{"tool_result", "", nil}:          "tool_result",
		{"error", "", nil}:                "error",
		{"result", "", nil}:               "turn.result",
	}

	for i, frame := range corpus {
		eventType, _ := frame["type"].(string)
		role, _ := frame["role"].(string)
		var delta any
		if v, ok := frame["delta"]; ok {
			delta = v
		}
		want, ok := wantKinds[matcher{eventType, role, delta}]
		if !ok {
			t.Errorf("frame %d: no expectation for type=%q role=%q delta=%v — extend wantKinds",
				i, eventType, role, delta)
			continue
		}
		got := ApplyProfile(frame, profile)
		if len(got) != 1 {
			t.Errorf("frame %d (type=%q): want 1 emit, got %d", i, eventType, len(got))
			continue
		}
		if got[0].Kind != want {
			t.Errorf("frame %d (type=%q role=%q delta=%v): kind = %q; want %q",
				i, eventType, role, delta, got[0].Kind, want)
		}
	}
}

// TestProfile_Gemini_PayloadFields pins the load-bearing payload
// fields the driver (slice 3) and mobile UI depend on. Field-level
// coverage outside this list is left to the operator-extended corpus
// + the wantKinds map above.
func TestProfile_Gemini_PayloadFields(t *testing.T) {
	f, _ := agentfamilies.ByName("gemini-cli")
	profile := f.FrameProfile

	// session.init carries the session_id we persist as the resume
	// cursor under ADR-013 D2.
	initFrame := map[string]any{
		"type":       "init",
		"session_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
		"model":      "gemini-2.5-pro",
		"timestamp":  "2026-04-29T10:00:00Z",
	}
	got := ApplyProfile(initFrame, profile)
	if len(got) != 1 || got[0].Kind != "session.init" {
		t.Fatalf("init: want one session.init, got %+v", got)
	}
	if got[0].Payload["session_id"] != "a1b2c3d4-e5f6-7890-abcd-ef1234567890" {
		t.Errorf("session.init.session_id = %v; want UUID",
			got[0].Payload["session_id"])
	}
	if got[0].Payload["model"] != "gemini-2.5-pro" {
		t.Errorf("session.init.model = %v; want gemini-2.5-pro",
			got[0].Payload["model"])
	}

	// Assistant frames (delta=true OR delta=false) intentionally fall
	// through to kind=raw at the profile layer. The driver's
	// streamFrames accumulator owns turning incremental chunks into a
	// growing cumulative text event with partial:true + a turn-local
	// message_id; the profile evaluator is stateless and can't
	// accumulate. Pin both shapes here so a future profile editor
	// doesn't accidentally re-add a per-chunk text rule and double-
	// emit alongside the driver-side accumulator.
	asstChunk := map[string]any{
		"type":      "message",
		"role":      "assistant",
		"content":   "Hello, world.",
		"delta":     true,
		"timestamp": "2026-04-29T10:00:02Z",
	}
	got = ApplyProfile(asstChunk, profile)
	if len(got) != 1 || got[0].Kind != "raw" {
		t.Errorf("message assistant delta=true: want raw fallback (driver accumulates), got %+v", got)
	}

	asstFinal := map[string]any{
		"type":      "message",
		"role":      "assistant",
		"content":   "Hello, world.",
		"delta":     false,
		"timestamp": "2026-04-29T10:00:03Z",
	}
	got = ApplyProfile(asstFinal, profile)
	if len(got) != 1 || got[0].Kind != "raw" {
		t.Errorf("message assistant delta=false: want raw fallback (gemini@0.41 doesn't emit this), got %+v", got)
	}

	// tool_use → tool_call pair. id pairs with the matching
	// tool_result on completion.
	toolUse := map[string]any{
		"type":       "tool_use",
		"tool_name":  "shell",
		"tool_id":    "call_xyz",
		"parameters": map[string]any{"command": "echo hi"},
		"timestamp":  "2026-04-29T10:00:04Z",
	}
	got = ApplyProfile(toolUse, profile)
	if len(got) != 1 || got[0].Kind != "tool_call" {
		t.Fatalf("tool_use: want one tool_call, got %+v", got)
	}
	if got[0].Payload["id"] != "call_xyz" {
		t.Errorf("tool_call.id = %v; want call_xyz", got[0].Payload["id"])
	}
	if got[0].Payload["name"] != "shell" {
		t.Errorf("tool_call.name = %v; want shell", got[0].Payload["name"])
	}

	toolResult := map[string]any{
		"type":      "tool_result",
		"tool_id":   "call_xyz",
		"status":    "success",
		"output":    "hi\n",
		"timestamp": "2026-04-29T10:00:05Z",
	}
	got = ApplyProfile(toolResult, profile)
	if len(got) != 1 || got[0].Kind != "tool_result" {
		t.Fatalf("tool_result: want one tool_result, got %+v", got)
	}
	if got[0].Payload["tool_use_id"] != "call_xyz" {
		t.Errorf("tool_result.tool_use_id = %v; want call_xyz (must pair with tool_call)",
			got[0].Payload["tool_use_id"])
	}
	if got[0].Payload["content"] != "hi\n" {
		t.Errorf("tool_result.content = %v; want hi\\n", got[0].Payload["content"])
	}
	if got[0].Payload["status"] != "success" {
		t.Errorf("tool_result.status = %v; want success", got[0].Payload["status"])
	}

	// result → turn.result with the canonical hub fields the mobile
	// transcript's token aggregator keys on. We flatten gemini's nested
	// stats into top-level by_model + duration_ms + total/input/output
	// tokens so the same renderer code that lights up for claude/codex
	// fires for gemini. The full raw stats are still under `stats` for
	// future telemetry rules.
	resultFrame := map[string]any{
		"type":   "result",
		"status": "success",
		"stats": map[string]any{
			"input_tokens":  float64(100),
			"output_tokens": float64(50),
			"total_tokens":  float64(150),
			"duration_ms":   float64(2000),
			"tool_calls":    float64(0),
			"models": map[string]any{
				"gemini-3-flash-preview": map[string]any{
					"input_tokens":  float64(80),
					"output_tokens": float64(40),
					"total_tokens":  float64(120),
				},
			},
		},
		"timestamp": "2026-04-29T10:00:09Z",
	}
	got = ApplyProfile(resultFrame, profile)
	if len(got) != 1 || got[0].Kind != "turn.result" {
		t.Fatalf("result: want one turn.result, got %+v", got)
	}
	p := got[0].Payload
	if p["status"] != "success" {
		t.Errorf("turn.result.status = %v; want success", p["status"])
	}
	if p["duration_ms"] != float64(2000) {
		t.Errorf("turn.result.duration_ms = %v; want 2000 (lifted from stats)", p["duration_ms"])
	}
	if p["total_tokens"] != float64(150) {
		t.Errorf("turn.result.total_tokens = %v; want 150 (lifted from stats)", p["total_tokens"])
	}
	if p["by_model"] == nil {
		t.Errorf("turn.result.by_model is nil; want it lifted from stats.models so mobile token-usage tile fires")
	}
	if bm, ok := p["by_model"].(map[string]any); ok {
		if _, has := bm["gemini-3-flash-preview"]; !has {
			t.Errorf("turn.result.by_model missing gemini-3-flash-preview key: %+v", bm)
		}
	}
	if p["stats"] == nil {
		t.Errorf("turn.result.stats = nil; want full stats forwarded for future telemetry rules")
	}
}

// TestFrameProfile_EmbeddedGemini sanity-checks that the gemini-cli
// family entry actually picked up a non-nil FrameProfile from the
// embedded YAML — the real failure mode if the YAML edit is silently
// reverted, not a logic bug. Pairs with the same shape test for codex
// (TestFrameProfile_EmbeddedCodex).
func TestFrameProfile_EmbeddedGemini(t *testing.T) {
	f, ok := agentfamilies.ByName("gemini-cli")
	if !ok {
		t.Fatal("gemini-cli family not embedded")
	}
	if f.FrameProfile == nil {
		t.Fatal("gemini-cli FrameProfile is nil — slice 2 YAML didn't load")
	}
	if f.FrameTranslator != "profile" {
		t.Errorf("frame_translator: want profile (no legacy path for exec-per-turn), got %q",
			f.FrameTranslator)
	}
	if len(f.FrameProfile.Rules) < 5 {
		t.Errorf("expected ≥5 rules (init/message/tool_use/tool_result/result/error), got %d",
			len(f.FrameProfile.Rules))
	}

	// Sanity-check: M2 must be in supports — exec-per-turn wraps the
	// stream-json M2 transport even though the lifecycle differs.
	hasM2 := false
	for _, m := range f.Supports {
		if m == "M2" {
			hasM2 = true
		}
	}
	if !hasM2 {
		t.Errorf("supports: M2 missing — exec-per-turn driver dispatches via M2, got %v", f.Supports)
	}
}
