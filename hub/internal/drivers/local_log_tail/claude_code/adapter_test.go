package claudecode

import (
	"context"
	"testing"

	locallogtail "github.com/termipod/hub/internal/drivers/local_log_tail"
)

type stubPoster struct{ posts int }

func (s *stubPoster) PostAgentEvent(_ context.Context, _, _, _ string, _ any) error {
	s.posts++
	return nil
}

func TestNewAdapter_ValidatesConfig(t *testing.T) {
	good := Config{AgentID: "a", Workdir: "/tmp/proj", Poster: &stubPoster{}}
	if _, err := NewAdapter(good); err != nil {
		t.Errorf("NewAdapter with good config: %v", err)
	}
	tests := map[string]Config{
		"missing agent id": {Workdir: "/tmp", Poster: &stubPoster{}},
		"missing workdir":  {AgentID: "a", Poster: &stubPoster{}},
		"missing poster":   {AgentID: "a", Workdir: "/tmp"},
	}
	for name, cfg := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := NewAdapter(cfg); err == nil {
				t.Errorf("NewAdapter(%v) = nil; want error", name)
			}
		})
	}
}

func TestKnobs_DefaultsApplied(t *testing.T) {
	a, err := NewAdapter(Config{AgentID: "a", Workdir: "/tmp/p", Poster: &stubPoster{}})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	if a.Knobs.IdleThresholdMs != 2000 {
		t.Errorf("idle = %d, want 2000", a.Knobs.IdleThresholdMs)
	}
	if a.Knobs.HookParkDefaultMs != 60_000 {
		t.Errorf("hook_park = %d, want 60000", a.Knobs.HookParkDefaultMs)
	}
	if a.Knobs.CancelHardAfterMs != 2000 {
		t.Errorf("cancel_hard = %d, want 2000", a.Knobs.CancelHardAfterMs)
	}
	if a.Knobs.ReplayTurnsOnAttach != 5 {
		t.Errorf("replay = %d, want 5", a.Knobs.ReplayTurnsOnAttach)
	}
}

func TestKnobs_CustomValuesPreserved(t *testing.T) {
	a, err := NewAdapter(Config{
		AgentID: "a", Workdir: "/tmp/p", Poster: &stubPoster{},
		Knobs: Knobs{IdleThresholdMs: 500, HookParkDefaultMs: 30_000},
	})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	if a.Knobs.IdleThresholdMs != 500 {
		t.Errorf("idle = %d, want 500", a.Knobs.IdleThresholdMs)
	}
	if a.Knobs.HookParkDefaultMs != 30_000 {
		t.Errorf("hook_park = %d, want 30000", a.Knobs.HookParkDefaultMs)
	}
	// Unset fields default
	if a.Knobs.CancelHardAfterMs != 2000 {
		t.Errorf("cancel_hard = %d, want 2000 (default)", a.Knobs.CancelHardAfterMs)
	}
}

func TestAdapter_StartStopIdempotent(t *testing.T) {
	a, _ := NewAdapter(Config{AgentID: "a", Workdir: "/tmp/p", Poster: &stubPoster{}})
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	a.Stop()
	a.Stop() // second Stop is a no-op
}

func TestAdapter_OnHookStubReturnsEmpty(t *testing.T) {
	a, _ := NewAdapter(Config{AgentID: "a", Workdir: "/tmp/p", Poster: &stubPoster{}})
	resp, err := a.OnHook(context.Background(), "Stop", map[string]any{})
	if err != nil {
		t.Fatalf("OnHook: %v", err)
	}
	if resp == nil {
		t.Errorf("OnHook stub returned nil; want empty map")
	}
}

func TestAdapter_HandleInputStubRejects(t *testing.T) {
	a, _ := NewAdapter(Config{AgentID: "a", Workdir: "/tmp/p", Poster: &stubPoster{}})
	if err := a.HandleInput(context.Background(), "text", map[string]any{"body": "hi"}); err == nil {
		t.Error("HandleInput stub returned nil; want not-yet-wired error")
	}
}

// Compile-time check is in adapter.go; this is a runtime guard that
// the type assertion compiles (and would fail loudly if the interface
// drifted).
func TestAdapter_SatisfiesLocalLogTailAdapter(t *testing.T) {
	var _ locallogtail.Adapter = &Adapter{}
}
