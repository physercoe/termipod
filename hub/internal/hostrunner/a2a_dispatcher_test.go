package hostrunner

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/termipod/hub/internal/hostrunner/a2a"
)

type fakeInputPoster struct {
	calls []struct {
		agentID string
		fields  map[string]any
	}
	err error
}

func (f *fakeInputPoster) PostAgentInput(ctx context.Context, agentID string, fields map[string]any) error {
	f.calls = append(f.calls, struct {
		agentID string
		fields  map[string]any
	}{agentID, fields})
	return f.err
}

func TestA2AHubDispatcher_PostsTextInput(t *testing.T) {
	p := &fakeInputPoster{}
	d := newA2AHubDispatcher(p)
	msg := a2a.Message{
		MessageID: "m-1",
		Role:      "user",
		Parts:     json.RawMessage(`[{"kind":"text","text":"hello"},{"kind":"text","text":"world"}]`),
	}
	if err := d.Dispatch(context.Background(), "agent-x", msg, "task-1", a2a.NewTaskStore()); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(p.calls) != 1 {
		t.Fatalf("posts = %d, want 1", len(p.calls))
	}
	if p.calls[0].agentID != "agent-x" {
		t.Errorf("agent = %q, want agent-x", p.calls[0].agentID)
	}
	if p.calls[0].fields["kind"] != "text" {
		t.Errorf("kind = %v, want text", p.calls[0].fields["kind"])
	}
	if p.calls[0].fields["body"] != "hello\nworld" {
		t.Errorf("body = %v, want concat of parts", p.calls[0].fields["body"])
	}
}

func TestA2AHubDispatcher_EmptyPartsReturnsErrDispatch(t *testing.T) {
	p := &fakeInputPoster{}
	d := newA2AHubDispatcher(p)
	msg := a2a.Message{
		MessageID: "m-2",
		Role:      "user",
		Parts:     json.RawMessage(`[{"kind":"file","uri":"blob://x"}]`),
	}
	err := d.Dispatch(context.Background(), "agent-x", msg, "task-2", a2a.NewTaskStore())
	if err == nil {
		t.Fatal("err = nil, want ErrDispatch")
	}
	if !errors.Is(err, a2a.ErrDispatch) {
		t.Errorf("err = %v, want ErrDispatch wrap", err)
	}
	if len(p.calls) != 0 {
		t.Errorf("posted %d times, want 0 for empty text", len(p.calls))
	}
}

func TestA2AHubDispatcher_PostErrorWrappedAsErrDispatch(t *testing.T) {
	p := &fakeInputPoster{err: errors.New("hub down")}
	d := newA2AHubDispatcher(p)
	msg := a2a.Message{
		MessageID: "m-3",
		Role:      "user",
		Parts:     json.RawMessage(`[{"kind":"text","text":"hi"}]`),
	}
	err := d.Dispatch(context.Background(), "agent-x", msg, "task-3", a2a.NewTaskStore())
	if err == nil || !errors.Is(err, a2a.ErrDispatch) {
		t.Errorf("err = %v, want wrapped ErrDispatch", err)
	}
}
