package antigravity

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type capturedEvent struct {
	Kind     string
	Producer string
	Payload  map[string]any
}

type fakePoster struct {
	mu     sync.Mutex
	events []capturedEvent
}

func (p *fakePoster) PostAgentEvent(_ context.Context, _, kind, producer string, payload any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	pm, _ := payload.(map[string]any)
	p.events = append(p.events, capturedEvent{Kind: kind, Producer: producer, Payload: pm})
	return nil
}

func (p *fakePoster) snapshot() []capturedEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]capturedEvent(nil), p.events...)
}

// Start must resolve the conversation id from agy's workspace→id cache,
// wait for the transcript, and post mapped events for each step — the
// end-to-end Adapter path against a faked agy store.
func TestAdapter_StartResolvesAndPosts(t *testing.T) {
	home := t.TempDir()
	workdir := "/home/ubuntu/agytest"
	convID := "conv-abc"

	// Seed the workspace→id cache.
	cacheDir := filepath.Join(StoreDir(home), "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cacheBody, _ := json.Marshal(map[string]string{workdir: convID})
	writeFile(t, filepath.Join(cacheDir, "last_conversations.json"), string(cacheBody))

	// Seed the transcript.
	tdir := filepath.Dir(TranscriptPath(home, convID))
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, TranscriptPath(home, convID),
		`{"step_index":0,"type":"USER_INPUT","status":"DONE","content":"hi"}`+"\n"+
			`{"step_index":1,"type":"PLANNER_RESPONSE","status":"DONE","content":"hello back"}`+"\n")

	poster := &fakePoster{}
	a, err := NewAdapter(Config{
		AgentID: "ag1",
		Workdir: workdir,
		HomeDir: home,
		Poster:  poster,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	if a.ConversationID != convID {
		t.Fatalf("ConversationID = %q; want %q (resume cursor)", a.ConversationID, convID)
	}

	// Expect: a session.init cursor event (W8) + one text event from the
	// PLANNER_RESPONSE (USER_INPUT drops). Poll until both are present.
	deadline := time.After(2 * time.Second)
	for {
		got := poster.snapshot()
		var sawInit, sawText bool
		for _, e := range got {
			if e.Kind == "session.init" && e.Payload["session_id"] == convID {
				sawInit = true
			}
			if e.Kind == "text" && e.Payload["text"] == "hello back" {
				sawText = true
			}
		}
		if sawInit && sawText {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("want session.init(%s)+text; got %+v", convID, got)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestAdapter_HandleInput_RequiresPane(t *testing.T) {
	a, err := NewAdapter(Config{AgentID: "ag1", Workdir: "/x", Poster: &fakePoster{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.HandleInput(context.Background(), "text", map[string]any{"body": "hi"}); err == nil {
		t.Fatal("want error when PaneID unset; got nil")
	}
}

// pick_option arrow-navigates Down×index then Enter (agy's menu UX).
func TestAdapter_PickOption_ArrowNav(t *testing.T) {
	fr := &fakeRunner{}
	a, err := NewAdapter(Config{AgentID: "ag1", Workdir: "/x", Poster: &fakePoster{}})
	if err != nil {
		t.Fatal(err)
	}
	a.PaneID = "%3"
	a.CmdRunner = fr
	if err := a.HandleInput(context.Background(), "pick_option", map[string]any{"index": float64(2)}); err != nil {
		t.Fatal(err)
	}
	want := []string{"Down", "Down", "Enter"}
	if len(fr.keys) != len(want) {
		t.Fatalf("keys = %v; want %v", fr.keys, want)
	}
	for i := range want {
		if fr.keys[i] != want[i] {
			t.Fatalf("keys[%d] = %q; want %q (%v)", i, fr.keys[i], want[i], fr.keys)
		}
	}
}

// fakeRunner records the final tmux send-keys argument (the key/literal).
type fakeRunner struct{ keys []string }

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name == "tmux" && len(args) >= 1 && args[0] == "send-keys" {
		f.keys = append(f.keys, args[len(args)-1])
	}
	return nil, nil
}
