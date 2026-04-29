package hostrunner

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/termipod/hub/internal/agentfamilies"
)

// loadClaudeProfile loads the embedded claude-code FrameProfile for
// the mode-dispatch tests below. Sourced from agent_families.yaml so
// these tests exercise the canonical rules rather than a synthetic
// fixture — if the YAML profile becomes stale, the tests catch it.
func loadClaudeProfile(t *testing.T) *agentfamilies.FrameProfile {
	t.Helper()
	f, ok := agentfamilies.ByName("claude-code")
	if !ok || f.FrameProfile == nil {
		t.Fatal("claude-code frame_profile not embedded")
	}
	return f.FrameProfile
}

// captureLog returns a *slog.Logger writing to the returned buffer
// so tests can assert on the divergence-warning content.
func captureLog() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	h := slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), buf
}

// frame is a minimal text-emitting assistant frame; covers the
// per-block dispatch + when_present-gated usage path.
func frame(t *testing.T) map[string]any {
	t.Helper()
	return map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"id":      "msg_modes",
			"model":   "claude-opus-4-7",
			"content": []any{map[string]any{"type": "text", "text": "hi"}},
			"usage": map[string]any{
				"input_tokens":  float64(10),
				"output_tokens": float64(2),
			},
		},
	}
}

// TestTranslatorMode_LegacyDefault — empty FrameTranslator means
// "legacy", same as today's behavior. Profile is not invoked even
// when a profile is loaded; emits are exactly the legacy shape.
func TestTranslatorMode_LegacyDefault(t *testing.T) {
	cap := &capturingPoster{}
	log, _ := captureLog()
	drv := &StdioDriver{
		AgentID: "agent-legacy",
		Poster:  cap,
		Log:     log,
		// FrameTranslator deliberately empty.
		FrameProfile: loadClaudeProfile(t),
	}
	drv.translate(context.Background(), frame(t))
	if len(cap.events) != 2 {
		t.Fatalf("legacy emit count = %d; want 2 (text + usage)", len(cap.events))
	}
	for _, e := range cap.events {
		if e.Kind != "text" && e.Kind != "usage" {
			t.Errorf("unexpected event kind in legacy mode: %s", e.Kind)
		}
	}
}

// TestTranslatorMode_ProfileOnly — the profile is authoritative;
// legacy is never invoked. ApplyProfile + claude-code's embedded
// rules drive the emitted events.
func TestTranslatorMode_ProfileOnly(t *testing.T) {
	cap := &capturingPoster{}
	log, _ := captureLog()
	drv := &StdioDriver{
		AgentID:         "agent-profile",
		Poster:          cap,
		Log:             log,
		FrameTranslator: "profile",
		FrameProfile:    loadClaudeProfile(t),
	}
	drv.translate(context.Background(), frame(t))
	if len(cap.events) != 2 {
		t.Fatalf("profile emit count = %d; want 2 (text + usage)", len(cap.events))
	}
	// Profile should produce the same kinds as legacy for this frame
	// (per the parity test corpus). Spot-check the message_id lift.
	var got string
	for _, e := range cap.events {
		if e.Kind == "text" {
			got, _ = e.Payload["message_id"].(string)
		}
	}
	if got != "msg_modes" {
		t.Errorf("text.message_id = %q; want msg_modes (lifted via $$ in profile)", got)
	}
}

// TestTranslatorMode_BothMatchingFrames — when legacy and profile
// agree, no divergence warning should fire. Confirms the "both"
// canary stays quiet for parity-clean frames.
func TestTranslatorMode_BothMatchingFrames(t *testing.T) {
	cap := &capturingPoster{}
	log, logBuf := captureLog()
	drv := &StdioDriver{
		AgentID:         "agent-both",
		Poster:          cap,
		Log:             log,
		FrameTranslator: "both",
		FrameProfile:    loadClaudeProfile(t),
	}
	drv.translate(context.Background(), frame(t))
	// Profile is authoritative → cap should hold profile output (2 events).
	if len(cap.events) != 2 {
		t.Fatalf("both-mode emit count = %d; want 2", len(cap.events))
	}
	// No divergence: log shouldn't contain the warning.
	if strings.Contains(logBuf.String(), "frame_translator divergence") {
		t.Errorf("unexpected divergence log for parity-clean frame:\n%s", logBuf.String())
	}
}

// TestTranslatorMode_BothLogsDivergence — a synthetic profile that
// disagrees with legacy on field names should trigger the divergence
// warning. Profile is still authoritative (its events go to the
// poster); legacy runs in shadow only.
func TestTranslatorMode_BothLogsDivergence(t *testing.T) {
	// Hand-rolled profile that emits a different `text` kind shape
	// than legacy: `body` instead of `text`. Legacy emits {text: "hi"};
	// profile emits {body: "hi"}. Diff should fire.
	mismatched := &agentfamilies.FrameProfile{
		ProfileVersion: 1,
		Rules: []agentfamilies.Rule{
			{
				Match:   map[string]any{"type": "assistant"},
				ForEach: "$.message.content",
				SubRules: []agentfamilies.Rule{
					{
						Match: map[string]any{"type": "text"},
						Emit: agentfamilies.Emit{
							Kind:    "text",
							Payload: map[string]string{"body": "$.text"},
						},
					},
				},
			},
		},
	}
	cap := &capturingPoster{}
	log, logBuf := captureLog()
	drv := &StdioDriver{
		AgentID:         "agent-both-diff",
		Poster:          cap,
		Log:             log,
		FrameTranslator: "both",
		FrameProfile:    mismatched,
	}
	drv.translate(context.Background(), frame(t))
	// Profile is authoritative — cap holds 1 event (text, body=hi).
	if len(cap.events) != 1 {
		t.Fatalf("both-mode emit count = %d; want 1 (profile only)", len(cap.events))
	}
	if cap.events[0].Payload["body"] != "hi" {
		t.Errorf("authoritative payload should be profile's body field, got %+v",
			cap.events[0].Payload)
	}
	// Divergence warning should have fired with the count-mismatch
	// detail (legacy emits text+usage; mismatched profile emits only
	// text, since the test's hand-rolled rules don't cover usage).
	logged := logBuf.String()
	if !strings.Contains(logged, "frame_translator divergence") {
		t.Errorf("expected divergence warning in log; got:\n%s", logged)
	}
	if !strings.Contains(logged, "count differs") {
		t.Errorf("expected divergence diff to call out count mismatch; got:\n%s",
			logged)
	}
}

// TestTranslatorMode_ProfileMissingFallsToLegacy — operator misconfig
// (FrameTranslator=profile but no profile loaded) shouldn't lose
// events. Driver falls back to legacy with a one-time warning.
func TestTranslatorMode_ProfileMissingFallsToLegacy(t *testing.T) {
	cap := &capturingPoster{}
	log, logBuf := captureLog()
	drv := &StdioDriver{
		AgentID:         "agent-misconfigured",
		Poster:          cap,
		Log:             log,
		FrameTranslator: "profile",
		FrameProfile:    nil, // misconfig
	}
	drv.translate(context.Background(), frame(t))
	// Should still emit the legacy events (text + usage).
	if len(cap.events) != 2 {
		t.Fatalf("expected legacy fallback to emit 2 events; got %d", len(cap.events))
	}
	if !strings.Contains(logBuf.String(), "no profile loaded") {
		t.Errorf("expected fallback warning in log; got:\n%s", logBuf.String())
	}
}
