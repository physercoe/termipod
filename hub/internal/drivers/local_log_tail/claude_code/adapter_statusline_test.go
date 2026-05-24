package claudecode

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

// OnStatusLine should be a no-op when payload is nil (defensive
// against an empty fire) — the cached snapshot stays whatever it was
// before. Today's gateway never invokes the sink with nil but the
// contract should tolerate it.
func TestAdapter_OnStatusLine_NilPayloadNoop(t *testing.T) {
	a, _ := NewAdapter(Config{AgentID: "a", Workdir: "/tmp/p", Poster: &stubPoster{}})
	a.OnStatusLine(context.Background(), map[string]any{"version": "2.1.150"})
	if got := a.statusLineVersion(); got != "2.1.150" {
		t.Fatalf("setup version = %q, want 2.1.150", got)
	}
	a.OnStatusLine(context.Background(), nil)
	if got := a.statusLineVersion(); got != "2.1.150" {
		t.Errorf("nil OnStatusLine clobbered prior snapshot; version now = %q", got)
	}
}

// statusLineVersion + statusLineContextWindow return zero values
// before any frame has been received — ensures the override paths
// don't accidentally inject empty/zero values when nothing's cached.
func TestAdapter_StatusLineHelpers_ZeroBeforeFrame(t *testing.T) {
	a, _ := NewAdapter(Config{AgentID: "a", Workdir: "/tmp/p", Poster: &stubPoster{}})
	if got := a.statusLineVersion(); got != "" {
		t.Errorf("version before frame = %q, want empty", got)
	}
	if got := a.statusLineContextWindow(); got != 0 {
		t.Errorf("contextWindow before frame = %d, want 0", got)
	}
}

// statusLineContextWindow must accept the three numeric shapes JSON
// decoding can land on map[string]any: float64 (the default), int
// (when the test or sink constructed the map manually), and
// json.Number (when a decoder was configured with UseNumber). All
// three should yield the same int value — locks the helper against
// silent zero-returns from a future re-marshalling regression.
func TestAdapter_StatusLineContextWindow_NumericShapes(t *testing.T) {
	cases := map[string]any{
		"float64":     float64(1_000_000),
		"int":         1_000_000,
		"json.Number": json.Number("1000000"),
	}
	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			a, _ := NewAdapter(Config{AgentID: "a", Workdir: "/tmp/p", Poster: &stubPoster{}})
			a.OnStatusLine(context.Background(), map[string]any{
				"context_window": map[string]any{"context_window_size": v},
			})
			if got := a.statusLineContextWindow(); got != 1_000_000 {
				t.Errorf("%s: got %d, want 1_000_000", name, got)
			}
		})
	}
}

// Integration: after OnStatusLine fires with a `version`, the next
// usage event triggers maybeEmitSessionInit which posts session.init
// carrying the statusLine version instead of the literal
// "claude-code". This is the v1.0.696 → 697 user-visible win — chip
// strip stops lying about the binary version.
func TestAdapter_SessionInit_UsesStatusLineVersion(t *testing.T) {
	cwd := "/home/test/proj"
	homeDir, projectDir := makeFakeHome(t, cwd)
	jsonl := filepath.Join(projectDir, "sess-v-1.jsonl")
	writeJSONL(t, jsonl) // empty for now

	poster := &capturingPoster{}
	a, _ := NewAdapter(Config{
		AgentID: "a", Workdir: cwd, Poster: poster,
	})
	a.HomeDir = homeDir
	a.SessionCutoff = time.Time{} // no cutoff
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Inject a statusLine frame BEFORE the first usage event lands.
	a.OnStatusLine(ctx, map[string]any{
		"version":    "2.1.150",
		"session_id": "sess-v-1",
	})

	// Append a single assistant message carrying a usage block so the
	// adapter emits both `usage` and the synthetic `session.init`.
	appendJSONL(t, jsonl, `{"type":"assistant","message":{"model":"claude-opus-4-7","usage":{"input_tokens":10,"output_tokens":5}}}`)

	events := waitForN(t, poster, 2, 3*time.Second)
	var gotInit *capturedEvent
	for i := range events {
		if events[i].kind == "session.init" {
			gotInit = &events[i]
			break
		}
	}
	if gotInit == nil {
		t.Fatalf("no session.init in %v", events)
	}
	if v, _ := gotInit.payload["version"].(string); v != "2.1.150" {
		t.Errorf("session.init.version = %q, want 2.1.150 (statusLine override missing)", v)
	}
}

// Integration: after OnStatusLine fires with an authoritative
// context_window_size, the next usage event's context_window comes
// from statusLine, not the prefix-family heuristic. Use a model name
// the heuristic would map to 1M (claude-opus-4-7) but inject a
// 750_000 statusLine value to prove the override actually fires.
func TestAdapter_Usage_UsesStatusLineContextWindow(t *testing.T) {
	cwd := "/home/test/proj"
	homeDir, projectDir := makeFakeHome(t, cwd)
	jsonl := filepath.Join(projectDir, "sess-cw-1.jsonl")
	writeJSONL(t, jsonl)

	poster := &capturingPoster{}
	a, _ := NewAdapter(Config{
		AgentID: "a", Workdir: cwd, Poster: poster,
	})
	a.HomeDir = homeDir
	a.SessionCutoff = time.Time{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	a.OnStatusLine(ctx, map[string]any{
		"context_window": map[string]any{
			"context_window_size": float64(750_000),
		},
	})

	appendJSONL(t, jsonl, `{"type":"assistant","message":{"model":"claude-opus-4-7","usage":{"input_tokens":10,"output_tokens":5}}}`)

	events := waitForN(t, poster, 2, 3*time.Second)
	var gotUsage *capturedEvent
	for i := range events {
		if events[i].kind == "usage" {
			gotUsage = &events[i]
			break
		}
	}
	if gotUsage == nil {
		t.Fatalf("no usage event in %v", events)
	}
	cw, _ := gotUsage.payload["context_window"].(int)
	if cw != 750_000 {
		t.Errorf("usage.context_window = %v, want 750_000 (statusLine override missing)", gotUsage.payload["context_window"])
	}
}

// Falls back to the heuristic when no statusLine frame has fired
// (cold-open race). Locks the "blank > wrong" relegation contract:
// the W2 wedge MUST NOT regress chip behaviour for older claude
// versions that never ship a statusLine. claude-opus-4-7 → 1M via
// the prefix family in claudeModelContextWindow.
func TestAdapter_Usage_FallsBackToHeuristicWithoutStatusLine(t *testing.T) {
	cwd := "/home/test/proj"
	homeDir, projectDir := makeFakeHome(t, cwd)
	jsonl := filepath.Join(projectDir, "sess-fb-1.jsonl")
	writeJSONL(t, jsonl)

	poster := &capturingPoster{}
	a, _ := NewAdapter(Config{
		AgentID: "a", Workdir: cwd, Poster: poster,
	})
	a.HomeDir = homeDir
	a.SessionCutoff = time.Time{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// NO OnStatusLine call.
	appendJSONL(t, jsonl, `{"type":"assistant","message":{"model":"claude-opus-4-7","usage":{"input_tokens":10,"output_tokens":5}}}`)

	events := waitForN(t, poster, 2, 3*time.Second)
	var gotUsage *capturedEvent
	for i := range events {
		if events[i].kind == "usage" {
			gotUsage = &events[i]
			break
		}
	}
	if gotUsage == nil {
		t.Fatalf("no usage event in %v", events)
	}
	cw, _ := gotUsage.payload["context_window"].(int)
	if cw != 1_000_000 {
		t.Errorf("fallback usage.context_window = %v, want 1_000_000 (heuristic relegation regressed)", gotUsage.payload["context_window"])
	}
}
