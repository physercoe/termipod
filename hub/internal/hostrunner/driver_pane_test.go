package hostrunner

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakePoster records every PostAgentEvent call for assertions.
type fakePoster struct {
	mu     sync.Mutex
	events []postedEvent
}

type postedEvent struct {
	AgentID  string
	Kind     string
	Producer string
	Payload  map[string]any
}

func (f *fakePoster) PostAgentEvent(_ context.Context, agentID, kind, producer string, payload any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, _ := payload.(map[string]any)
	f.events = append(f.events, postedEvent{agentID, kind, producer, m})
	return nil
}

func (f *fakePoster) snapshot() []postedEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]postedEvent, len(f.events))
	copy(out, f.events)
	return out
}

func (f *fakePoster) wait(t *testing.T, want int, timeout time.Duration) []postedEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if evs := f.snapshot(); len(evs) >= want {
			return evs
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d events; got %d", want, len(f.snapshot()))
	return nil
}

func TestDiffAppend(t *testing.T) {
	cases := []struct {
		name       string
		prev, next string
		want       string
	}{
		{"empty prev", "", "hello\n", "hello\n"},
		{"strict append", "hello\n", "hello\nworld\n", "world\n"},
		{"no change", "hello\n", "hello\n", ""},
		{"scrollback trim", "A\nB\nC\n", "B\nC\nD\n", "D\n"},
		{"full redraw", "abc", "xyz", "xyz"},
		{"suffix overlap", "foobar", "barbaz", "baz"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := diffAppend(c.prev, c.next); got != c.want {
				t.Fatalf("diffAppend(%q,%q) = %q; want %q", c.prev, c.next, got, c.want)
			}
		})
	}
}

func TestPaneDriver_EmitsLifecycleAndTextDelta(t *testing.T) {
	// Canned captures: tick 1 yields "hello\n", tick 2 appends "world\n",
	// tick 3 unchanged. Expect lifecycle.started + two text events + a
	// lifecycle.stopped on Stop.
	captures := []string{"hello\n", "hello\nworld\n", "hello\nworld\n"}
	i := 0
	poster := &fakePoster{}
	drv := &PaneDriver{
		AgentID:  "agent-1",
		PaneID:   "pane-1",
		Poster:   poster,
		Interval: 10 * time.Millisecond,
		Capture: func(_ context.Context, _ string) (string, error) {
			if i >= len(captures) {
				return captures[len(captures)-1], nil
			}
			out := captures[i]
			i++
			return out, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := drv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Expect at least: lifecycle.started + 2 text events.
	poster.wait(t, 3, time.Second)
	drv.Stop()

	evs := poster.snapshot()
	if evs[0].Kind != "lifecycle" || evs[0].Producer != "system" ||
		evs[0].Payload["phase"] != "started" {
		t.Fatalf("first event want lifecycle.started/system; got %+v", evs[0])
	}
	// Find the two text events in order.
	var texts []string
	for _, e := range evs {
		if e.Kind == "text" {
			if e.Producer != "agent" {
				t.Fatalf("text event producer = %q; want agent", e.Producer)
			}
			texts = append(texts, e.Payload["text"].(string))
		}
	}
	if len(texts) < 2 {
		t.Fatalf("want >=2 text events; got %d (%v)", len(texts), texts)
	}
	if texts[0] != "hello\n" {
		t.Fatalf("text[0] = %q; want %q", texts[0], "hello\n")
	}
	if texts[1] != "world\n" {
		t.Fatalf("text[1] = %q; want %q", texts[1], "world\n")
	}
	// Final event must be lifecycle.stopped (posted during Stop on a
	// fresh context).
	last := evs[len(evs)-1]
	if last.Kind == "lifecycle" && last.Payload["phase"] == "stopped" {
		return // ok
	}
	// Stop posts asynchronously via a 3s-budget ctx; the snapshot may
	// have been taken before it landed. Poll briefly.
	poster.wait(t, len(evs)+1, 500*time.Millisecond)
	evs = poster.snapshot()
	last = evs[len(evs)-1]
	if last.Kind != "lifecycle" || last.Payload["phase"] != "stopped" {
		t.Fatalf("last event want lifecycle.stopped; got %+v", last)
	}
}

// recordedSend captures every (text, literal) pair the driver sent through
// the SendKeys seam so tests can assert on the exact wire shape.
type recordedSend struct {
	text    string
	literal bool
}

func TestPaneDriver_InputTranslations(t *testing.T) {
	cases := []struct {
		name    string
		kind    string
		payload map[string]any
		want    []recordedSend
		wantErr bool
	}{
		{
			name:    "text",
			kind:    "text",
			payload: map[string]any{"body": "ls -la"},
			want:    []recordedSend{{"ls -la", true}, {"Enter", false}},
		},
		{
			name:    "cancel",
			kind:    "cancel",
			payload: map[string]any{"reason": "stop"},
			want:    []recordedSend{{"C-c", false}},
		},
		{
			name:    "approval with note",
			kind:    "approval",
			payload: map[string]any{"decision": "allow", "note": "ok"},
			want:    []recordedSend{{"allow: ok", true}, {"Enter", false}},
		},
		{
			name:    "approval no note",
			kind:    "approval",
			payload: map[string]any{"decision": "deny"},
			want:    []recordedSend{{"deny", true}, {"Enter", false}},
		},
		{
			name:    "attach",
			kind:    "attach",
			payload: map[string]any{"document_id": "doc-1"},
			want: []recordedSend{
				{"# attach requested: document_id=doc-1", true},
				{"Enter", false},
			},
		},
		{
			name:    "text missing body",
			kind:    "text",
			payload: map[string]any{},
			wantErr: true,
		},
		{
			name:    "approval missing decision",
			kind:    "approval",
			payload: map[string]any{},
			wantErr: true,
		},
		{
			name:    "attach missing doc",
			kind:    "attach",
			payload: map[string]any{},
			wantErr: true,
		},
		{
			name:    "unknown kind",
			kind:    "bogus",
			payload: map[string]any{},
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var mu sync.Mutex
			var got []recordedSend
			drv := &PaneDriver{
				AgentID: "agent-input",
				PaneID:  "pane-xyz",
				Poster:  &fakePoster{},
				SendKeys: func(_ context.Context, pane, text string, literal bool) error {
					if pane != "pane-xyz" {
						t.Errorf("pane target = %q; want pane-xyz", pane)
					}
					mu.Lock()
					got = append(got, recordedSend{text, literal})
					mu.Unlock()
					return nil
				},
			}
			err := drv.Input(context.Background(), c.kind, c.payload)
			if c.wantErr {
				if err == nil {
					t.Fatalf("want error; got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Input: %v", err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("len(sends) = %d; want %d (%+v)", len(got), len(c.want), got)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("send[%d] = %+v; want %+v", i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestPaneDriver_InputRejectsMissingPane(t *testing.T) {
	drv := &PaneDriver{
		AgentID: "agent-nopane",
		Poster:  &fakePoster{},
	}
	if err := drv.Input(context.Background(), "text", map[string]any{"body": "x"}); err == nil {
		t.Fatal("expected error when PaneID is empty")
	}
}

func TestPaneDriver_StopIsIdempotent(t *testing.T) {
	poster := &fakePoster{}
	drv := &PaneDriver{
		AgentID:  "agent-2",
		PaneID:   "pane-2",
		Poster:   poster,
		Interval: 10 * time.Millisecond,
		Capture:  func(context.Context, string) (string, error) { return "", nil },
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	drv.Stop()
	drv.Stop() // second Stop must not panic or re-emit
}
