package locallogtail

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type recordingPoster struct {
	mu     sync.Mutex
	events []postedEvent
}

type postedEvent struct {
	kind, producer string
	payload        any
}

func (p *recordingPoster) PostAgentEvent(_ context.Context, _, kind, producer string, payload any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, postedEvent{kind, producer, payload})
	return nil
}

func (p *recordingPoster) snapshot() []postedEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]postedEvent, len(p.events))
	copy(out, p.events)
	return out
}

type fakeAdapter struct {
	startCalls int
	stopCalls  int
	startErr   error
	inputs     []string
}

func (a *fakeAdapter) Start(_ context.Context) error { a.startCalls++; return a.startErr }
func (a *fakeAdapter) Stop()                         { a.stopCalls++ }
func (a *fakeAdapter) HandleInput(_ context.Context, kind string, _ map[string]any) error {
	a.inputs = append(a.inputs, kind)
	return nil
}

func TestDriverStartStop(t *testing.T) {
	p := &recordingPoster{}
	a := &fakeAdapter{}
	d := &Driver{
		Config:  Config{AgentID: "agent-1", PaneID: "pane-1", Poster: p},
		Adapter: a,
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if a.startCalls != 1 {
		t.Errorf("adapter.Start called %d times, want 1", a.startCalls)
	}
	ev := p.snapshot()
	if len(ev) != 1 || ev[0].kind != "lifecycle" {
		t.Errorf("first event = %+v, want one lifecycle event", ev)
	}
	d.Stop()
	if a.stopCalls != 1 {
		t.Errorf("adapter.Stop called %d times, want 1", a.stopCalls)
	}
	ev = p.snapshot()
	if len(ev) != 2 {
		t.Errorf("events after Stop = %d, want 2 (started+stopped)", len(ev))
	}
	d.Stop()
	if a.stopCalls != 1 {
		t.Errorf("adapter.Stop called %d times after second Stop, want still 1", a.stopCalls)
	}
}

func TestDriverStartIsIdempotent(t *testing.T) {
	p := &recordingPoster{}
	a := &fakeAdapter{}
	d := &Driver{
		Config:  Config{AgentID: "agent-1", Poster: p},
		Adapter: a,
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if a.startCalls != 1 {
		t.Errorf("adapter.Start called %d times, want 1", a.startCalls)
	}
}

func TestDriverInputDelegates(t *testing.T) {
	p := &recordingPoster{}
	a := &fakeAdapter{}
	d := &Driver{
		Config:  Config{AgentID: "agent-1", Poster: p},
		Adapter: a,
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := d.Input(context.Background(), "text", map[string]any{"body": "hi"}); err != nil {
		t.Fatalf("Input: %v", err)
	}
	if len(a.inputs) != 1 || a.inputs[0] != "text" {
		t.Errorf("inputs = %v; want [text]", a.inputs)
	}
}

func TestDriverStartAdapterFailureEmitsStopped(t *testing.T) {
	p := &recordingPoster{}
	a := &fakeAdapter{startErr: errors.New("boom")}
	d := &Driver{
		Config:  Config{AgentID: "agent-1", Poster: p},
		Adapter: a,
	}
	if err := d.Start(context.Background()); err == nil {
		t.Fatal("expected error on adapter Start failure")
	}
	ev := p.snapshot()
	if len(ev) != 2 {
		t.Fatalf("want 2 events (started+stopped), got %d: %+v", len(ev), ev)
	}
	got, ok := ev[1].payload.(map[string]any)
	if !ok {
		t.Fatalf("second event payload = %T, want map", ev[1].payload)
	}
	if got["phase"] != "stopped" {
		t.Errorf("second event phase = %v, want stopped", got["phase"])
	}
	if got["err"] != "boom" {
		t.Errorf("second event err = %v, want boom", got["err"])
	}
}

func TestDriverStartRejectsMissingDeps(t *testing.T) {
	t.Run("no adapter", func(t *testing.T) {
		p := &recordingPoster{}
		d := &Driver{Config: Config{AgentID: "a", Poster: p}}
		if err := d.Start(context.Background()); err == nil {
			t.Error("want error on nil Adapter, got nil")
		}
	})
	t.Run("no poster", func(t *testing.T) {
		d := &Driver{Config: Config{AgentID: "a"}, Adapter: &fakeAdapter{}}
		if err := d.Start(context.Background()); err == nil {
			t.Error("want error on nil Poster, got nil")
		}
	})
	t.Run("no agent id", func(t *testing.T) {
		p := &recordingPoster{}
		d := &Driver{Config: Config{Poster: p}, Adapter: &fakeAdapter{}}
		if err := d.Start(context.Background()); err == nil {
			t.Error("want error on empty AgentID, got nil")
		}
	})
}
