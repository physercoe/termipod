package hostrunner

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
)

// capHandler records every emitted record's level+message so a test can
// assert what reached the log. It honors the handler level so Debug records
// are dropped exactly as the default WARN/INFO stream would drop them.
type capHandler struct {
	mu      sync.Mutex
	level   slog.Level
	records []slog.Record
}

func (h *capHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }
func (h *capHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	h.records = append(h.records, r)
	h.mu.Unlock()
	return nil
}
func (h *capHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capHandler) WithGroup(string) slog.Handler       { return h }

func (h *capHandler) messages() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.records))
	for i, r := range h.records {
		out[i] = r.Level.String() + " " + r.Message
	}
	return out
}

// TestNotePollOutcome_EdgeTriggered pins the dedup contract the tester's
// SQLITE_BUSY spam exposed: a persistently failing poll must log ONCE on the
// falling edge and ONCE on recovery, never identically every tick.
func TestNotePollOutcome_EdgeTriggered(t *testing.T) {
	h := &capHandler{level: slog.LevelInfo} // default stream: Debug suppressed
	r := &Runner{Log: slog.New(h)}
	boom := errors.New("database is locked (5) (SQLITE_BUSY)")

	// Five consecutive failing ticks for the same run.
	for i := 0; i < 5; i++ {
		r.notePollOutcome("trackio", "run-1", "trackio://p/r", boom)
	}
	// Then it recovers, and stays healthy.
	r.notePollOutcome("trackio", "run-1", "trackio://p/r", nil)
	r.notePollOutcome("trackio", "run-1", "trackio://p/r", nil)

	got := h.messages()
	want := []string{
		"WARN metric poll failed",  // falling edge only
		"INFO metric poll recovered", // rising edge only
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d records, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("record %d = %q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}
}

// TestNotePollOutcome_PerRunIndependent confirms the edge state is keyed per
// (scheme, run): one run failing must not mute another run's first failure.
func TestNotePollOutcome_PerRunIndependent(t *testing.T) {
	h := &capHandler{level: slog.LevelInfo}
	r := &Runner{Log: slog.New(h)}
	boom := errors.New("boom")

	r.notePollOutcome("trackio", "run-1", "u1", boom)
	r.notePollOutcome("trackio", "run-2", "u2", boom) // different run → must log
	r.notePollOutcome("wandb", "run-1", "u3", boom)   // same id, different scheme → must log

	if n := len(h.messages()); n != 3 {
		t.Fatalf("expected 3 distinct falling-edge WARNs, got %d: %v", n, h.messages())
	}
}
