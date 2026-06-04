package hostrunner

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRenderInboundEnvelope_PrincipalDirective(t *testing.T) {
	raw := json.RawMessage(`{
		"from":{"role":"principal"},
		"to":{"role":"peer_steward","handle":"research-steward","agent_id":"agt_s"},
		"kind":"directive","text":"survey the citation graph",
		"thread":{"transport":"session","id":"ses_1"}}`)
	got, selfEcho := renderInboundEnvelope(raw)
	if selfEcho {
		t.Fatal("principal directive must not be a self-echo")
	}
	if !strings.Contains(got, "directive from the principal") {
		t.Errorf("header missing sender/kind: %q", got)
	}
	if !strings.Contains(got, "survey the citation graph") {
		t.Errorf("rendered turn lost the text: %q", got)
	}
	if !strings.Contains(got, "Reply in this chat") {
		t.Errorf("principal/session should reply via chat: %q", got)
	}
}

func TestRenderInboundEnvelope_A2ADirective(t *testing.T) {
	raw := json.RawMessage(`{
		"from":{"role":"peer_steward","handle":"research-steward"},
		"to":{"role":"peer_worker","handle":"worker.ml","agent_id":"agt_w"},
		"kind":"directive","text":"run the ablation",
		"thread":{"transport":"a2a","id":"req_1"}}`)
	got, _ := renderInboundEnvelope(raw)
	if !strings.Contains(got, "@research-steward (a peer steward)") {
		t.Errorf("header missing A2A sender: %q", got)
	}
	if !strings.Contains(got, `a2a_invoke(handle="research-steward"`) {
		t.Errorf("A2A message should reply via a2a_invoke: %q", got)
	}
}

func TestRenderInboundEnvelope_SystemNotification(t *testing.T) {
	raw := json.RawMessage(`{
		"from":{"role":"system"},
		"to":{"role":"peer_steward","handle":"s","agent_id":"agt_s"},
		"kind":"notification","text":"Task 'X' completed.",
		"thread":{"transport":"session","id":"ses_1"}}`)
	got, _ := renderInboundEnvelope(raw)
	if !strings.Contains(got, "notification from the system") {
		t.Errorf("header missing: %q", got)
	}
	if !strings.Contains(got, "Informational — no reply is routed") {
		t.Errorf("notification should render the act-not-reply contract: %q", got)
	}
}

func TestRenderInboundEnvelope_SelfEcho(t *testing.T) {
	raw := json.RawMessage(`{
		"from":{"role":"peer_worker","handle":"worker.ml"},
		"to":{"role":"peer_worker","handle":"worker.ml","agent_id":"agt_w"},
		"kind":"report","text":"echo","thread":{"transport":"a2a","id":"r"}}`)
	got, selfEcho := renderInboundEnvelope(raw)
	if !selfEcho {
		t.Error("from.handle == to.handle must be detected as a self-echo")
	}
	if got != "" {
		t.Errorf("a self-echo renders no turn; got %q", got)
	}
}

func TestRenderInboundEnvelope_NoEnvelopeFallback(t *testing.T) {
	// A legacy / malformed row with no envelope falls back to plain text.
	raw := json.RawMessage(`{"text":"bare text"}`)
	got, selfEcho := renderInboundEnvelope(raw)
	if selfEcho {
		t.Error("a no-envelope payload is not a self-echo")
	}
	if got != "bare text" {
		t.Errorf("fallback should return the text verbatim; got %q", got)
	}
}

func TestDeriveReplyVia(t *testing.T) {
	cases := []struct {
		kind, transport, want string
	}{
		{"notification", "session", "none"},
		{"notification", "a2a", "none"},
		{"directive", "a2a", "a2a"},
		{"report", "a2a", "a2a"},
		{"question", "attention", "attention_reply"},
		{"directive", "session", "chat"},
	}
	for _, c := range cases {
		if got := deriveReplyVia(c.kind, c.transport); got != c.want {
			t.Errorf("deriveReplyVia(%q,%q) = %q, want %q",
				c.kind, c.transport, got, c.want)
		}
	}
}

// TestInputRouter_SkipsSelfEcho: an input.text envelope whose sender ==
// recipient is dropped by the router and never reaches the driver; a
// normal event in the same batch still dispatches.
func TestInputRouter_SkipsSelfEcho(t *testing.T) {
	lister := &fakeInputLister{
		first: []AgentEvent{
			ev(1, "a2a", "input.text", `{"from":{"role":"peer_worker","handle":"worker.ml"},"to":{"role":"peer_worker","handle":"worker.ml","agent_id":"agt_w"},"kind":"report","text":"echo","thread":{"transport":"a2a","id":"r"}}`),
			ev(2, "user", "input.cancel", `{"reason":"stop"}`),
		},
	}
	drv := &capturingInputter{}
	r := NewInputRouter(lister, silentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Attach(ctx, "agent-1", drv, 0)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(drv.snapshot()) >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.Detach("agent-1")

	for _, c := range drv.snapshot() {
		if c.kind == "text" {
			t.Errorf("self-echo input.text should not dispatch; got %+v", c)
		}
	}
}
