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

// W3: session_id rotation. /clear within a running claude process
// mints a new session_id + a new JSONL file. Pre-W3, the adapter
// kept tailing the old JSONL until respawn (latent /clear-blindness
// bug). statusLine carries both fields, so we can re-point the
// tailer in-band and re-emit session.init so mobile renders the
// post-/clear conversation correctly.
//
// Setup: adapter starts on sess-1; we append a usage event so the
// initial session.init lands. Then OnStatusLine fires with a NEW
// session_id + transcript_path pointing at sess-2. The adapter
// should: stop the sess-1 tailer, re-point at sess-2, reset the
// session-init guard, then process sess-2's content as the new
// session. We verify by appending to sess-2 and seeing the second
// session.init carry the new session_id.
func TestAdapter_OnStatusLine_RotatesOnSessionIDChange(t *testing.T) {
	cwd := "/home/test/proj"
	homeDir, projectDir := makeFakeHome(t, cwd)
	jsonl1 := filepath.Join(projectDir, "sess-rot-1.jsonl")
	jsonl2 := filepath.Join(projectDir, "sess-rot-2.jsonl")
	writeJSONL(t, jsonl1)

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
	defer a.Stop()

	// First usage event on sess-1 → triggers initial session.init.
	appendJSONL(t, jsonl1, `{"type":"assistant","message":{"model":"claude-opus-4-7","usage":{"input_tokens":10,"output_tokens":5}}}`)
	first := waitForN(t, poster, 2, 3*time.Second)
	var firstInit *capturedEvent
	for i := range first {
		if first[i].kind == "session.init" {
			firstInit = &first[i]
			break
		}
	}
	if firstInit == nil {
		t.Fatalf("no initial session.init in %v", first)
	}
	if sid, _ := firstInit.payload["session_id"].(string); sid != "sess-rot-1" {
		t.Fatalf("initial session_id = %q, want sess-rot-1", sid)
	}

	// Mint the new (post-/clear) JSONL file and fire statusLine with
	// the new id + path. The handler should Stop the sess-1 tailer
	// and re-point at sess-2.
	writeJSONL(t, jsonl2)
	a.OnStatusLine(ctx, map[string]any{
		"session_id":      "sess-rot-2",
		"transcript_path": jsonl2,
	})

	// Append a usage event to sess-2. The post-rotation runLoop
	// should pick it up and emit a SECOND session.init with the new
	// session_id (the rotation reset the sessionInitSent guard).
	// Allow up to ~2s — the rotation involves a tailer Stop + new
	// tailer Start + appender poll.
	appendJSONL(t, jsonl2, `{"type":"assistant","message":{"model":"claude-opus-4-7","usage":{"input_tokens":20,"output_tokens":7}}}`)

	deadline := time.Now().Add(5 * time.Second)
	var secondInit *capturedEvent
	for time.Now().Before(deadline) {
		snap := poster.snapshot()
		for i := range snap {
			if snap[i].kind != "session.init" {
				continue
			}
			if sid, _ := snap[i].payload["session_id"].(string); sid == "sess-rot-2" {
				secondInit = &snap[i]
				break
			}
		}
		if secondInit != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if secondInit == nil {
		t.Fatalf("no post-rotation session.init carrying sess-rot-2; events = %d", len(poster.snapshot()))
	}
}

// Rotation is a NO-OP when the statusLine session_id matches the
// current engineSessionID (steady-state status-line refreshes during
// the same conversation). Guards against accidentally restarting the
// tailer every ~10s.
func TestAdapter_OnStatusLine_NoRotationOnSameSessionID(t *testing.T) {
	cwd := "/home/test/proj"
	homeDir, projectDir := makeFakeHome(t, cwd)
	jsonl := filepath.Join(projectDir, "sess-no-rot.jsonl")
	writeJSONL(t, jsonl)

	poster := &capturingPoster{}
	a, _ := NewAdapter(Config{AgentID: "a", Workdir: cwd, Poster: poster})
	a.HomeDir = homeDir
	a.SessionCutoff = time.Time{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	// Land the initial usage + session.init.
	appendJSONL(t, jsonl, `{"type":"assistant","message":{"model":"claude-opus-4-7","usage":{"input_tokens":10,"output_tokens":5}}}`)
	waitForN(t, poster, 2, 3*time.Second)
	before := len(poster.snapshot())

	// Fire statusLine with the SAME session_id. No rotation.
	a.OnStatusLine(ctx, map[string]any{
		"session_id":      "sess-no-rot",
		"transcript_path": jsonl,
	})
	// Brief wait — if rotation HAD fired, we'd see a tailer churn
	// log entry. Sample the event count and confirm no new event
	// rows landed beyond what the source file produces.
	time.Sleep(200 * time.Millisecond)
	after := len(poster.snapshot())
	if after != before {
		t.Errorf("same-session statusLine triggered extra events: before=%d after=%d", before, after)
	}
	// pendingTranscriptPath must remain empty.
	a.pendingMu.Lock()
	pending := a.pendingTranscriptPath
	a.pendingMu.Unlock()
	if pending != "" {
		t.Errorf("pendingTranscriptPath set on same-session frame: %q", pending)
	}
}

// Rotation MUST NOT fire before the adapter has resolved its initial
// session (engineSessionID still ""). statusLine frames that arrive
// during the launch race are ignored for rotation purposes — they
// still update the latestStatusLine cache for W2's field overrides.
func TestAdapter_OnStatusLine_NoRotationBeforeFirstResolution(t *testing.T) {
	a, _ := NewAdapter(Config{AgentID: "a", Workdir: "/tmp/p", Poster: &stubPoster{}})
	// engineSessionID stays "" because we haven't called Start.
	a.OnStatusLine(context.Background(), map[string]any{
		"session_id":      "premature-sess",
		"transcript_path": "/path/to/premature.jsonl",
	})
	a.pendingMu.Lock()
	pending := a.pendingTranscriptPath
	a.pendingMu.Unlock()
	if pending != "" {
		t.Errorf("rotation fired before first resolution: pending = %q", pending)
	}
	// But the cache MUST still have been updated (W2 path).
	if got := a.statusLineVersion(); got != "" {
		t.Errorf("version cached unexpectedly: %q", got)
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
