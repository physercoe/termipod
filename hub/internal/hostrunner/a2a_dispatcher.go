package hostrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/termipod/hub/internal/hostrunner/a2a"
)

// a2aInputPoster is the narrow contract a2aHubDispatcher needs from the
// hub client. The real *Client satisfies this; tests can fake it without
// standing up a hub.
type a2aInputPoster interface {
	PostAgentInput(ctx context.Context, agentID string, fields map[string]any) error
}

// a2aHubDispatcher is the concrete a2a.Dispatcher the host-runner wires
// in when its A2A server is enabled. Incoming message/send calls arrive
// already routed to an agent_id; the dispatcher extracts text parts and
// POSTs them to the hub's /input endpoint for that agent, stamped
// producer="a2a" so peer-originated input is distinguishable in the
// audit trail from phone/web input.
//
// Going through the hub (rather than calling the driver directly) keeps
// peer input on the same audit path as phone/web input and means no new
// cross-cutting state is introduced for A2A.
//
// Response harvesting: Dispatch registers a correlation
// (agentID, taskID, store). When a driver emits producer="agent" output
// events, a2aHubDispatcher.OnAgentEvent routes the text into the
// correlated task's history and advances state submitted → working. On
// a lifecycle "stopped" phase the task flips to completed. Only one
// live task per agent is tracked — a second message/send arriving
// before the first finishes marks the prior one canceled (terminal)
// and supersedes the correlation, mirroring the single-turn shape of
// today's drivers.
type a2aHubDispatcher struct {
	poster a2aInputPoster

	mu sync.Mutex
	// open holds at most one live task per agent. Tasks move out of
	// this map as soon as they transition to a terminal state (either
	// via driver output reaching the completed flip, or being
	// superseded by a fresh message/send).
	open map[string]*a2aOpenTask
}

// a2aOpenTask is the minimum correlation state needed to route driver
// output back into a2a task history. Treated as immutable once
// registered — mutations (state/history) go through the TaskStore,
// which is lock-protected.
type a2aOpenTask struct {
	taskID string
	store  *a2a.TaskStore
}

func newA2AHubDispatcher(c a2aInputPoster) *a2aHubDispatcher {
	return &a2aHubDispatcher{
		poster: c,
		open:   map[string]*a2aOpenTask{},
	}
}

// Dispatch extracts text from msg.Parts and posts it as kind=text input
// to the hub with producer="a2a". A message with no text parts yields
// an error so the message/send handler can mark the task failed rather
// than silently dropping the submission.
//
// On success the dispatcher records (agentID, taskID, store) so driver
// output for this agent can be appended back to the task history. A
// pre-existing open task for the same agent is marked canceled before
// the new one takes its slot.
func (d *a2aHubDispatcher) Dispatch(ctx context.Context, agentID string, msg a2a.Message,
	taskID string, store *a2a.TaskStore) error {
	text, err := extractTextParts(msg.Parts)
	if err != nil {
		return fmt.Errorf("%w: %v", a2a.ErrDispatch, err)
	}
	if text == "" {
		return fmt.Errorf("%w: message has no text parts", a2a.ErrDispatch)
	}
	if err := d.poster.PostAgentInput(ctx, agentID, map[string]any{
		"kind":     "text",
		"body":     text,
		"producer": "a2a",
	}); err != nil {
		return fmt.Errorf("%w: post input: %v", a2a.ErrDispatch, err)
	}
	d.registerTask(agentID, taskID, store)
	return nil
}

// registerTask claims the agent's single open-task slot for taskID.
// Any pre-existing open task is moved to the canceled terminal state so
// late output can't leak into the wrong history.
func (d *a2aHubDispatcher) registerTask(agentID, taskID string, store *a2a.TaskStore) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if prev, ok := d.open[agentID]; ok && prev.store != nil {
		prev.store.Update(agentID, prev.taskID,
			a2a.TaskStatus{State: a2a.TaskStateCanceled}, nil)
	}
	d.open[agentID] = &a2aOpenTask{taskID: taskID, store: store}
}

// OnAgentEvent is the tap point for driver output. It is safe to call
// for any (agentID, kind, producer, payload) tuple — unrelated events
// are silently ignored. Invoke this from a wrapper around
// AgentEventPoster.PostAgentEvent so every driver mode feeds the same
// correlator.
//
// State handling:
//   - producer="agent" with a "text" field on the payload: first such
//     event flips state submitted → working and appends the text as a
//     role="agent" history message. Subsequent text events append more
//     messages but stay in working (we have no reliable turn-complete
//     signal across all drivers).
//   - producer="system" + kind="lifecycle" + phase="stopped": flip to
//     completed and release the slot. Without a driver-side idle
//     signal this is the only unambiguous terminal event we can watch.
//   - everything else is a no-op.
func (d *a2aHubDispatcher) OnAgentEvent(agentID, kind, producer string, payload any) {
	d.mu.Lock()
	t, ok := d.open[agentID]
	d.mu.Unlock()
	if !ok {
		return
	}

	switch {
	case producer == "agent" && hasText(payload):
		text := extractPayloadText(payload)
		if text == "" {
			return
		}
		parts, err := json.Marshal([]map[string]any{{"kind": "text", "text": text}})
		if err != nil {
			return
		}
		msg := &a2a.Message{
			MessageID: t.taskID + ".agent." + nextAgentMsgSuffix(),
			Role:      "agent",
			Parts:     parts,
		}
		// submitted → working on first reply; working stays working on
		// subsequent chunks. No completed flip here: most drivers do
		// not emit a turn-complete signal, so we defer that to the
		// lifecycle.stopped path below.
		t.store.Update(agentID, t.taskID,
			a2a.TaskStatus{State: a2a.TaskStateWorking}, msg)
	case producer == "system" && kind == "lifecycle":
		phase := extractLifecyclePhase(payload)
		if phase != "stopped" {
			return
		}
		t.store.Update(agentID, t.taskID,
			a2a.TaskStatus{State: a2a.TaskStateCompleted}, nil)
		d.mu.Lock()
		// Only release the slot if it's still ours — a concurrent
		// Dispatch may have already superseded us.
		if cur, ok := d.open[agentID]; ok && cur == t {
			delete(d.open, agentID)
		}
		d.mu.Unlock()
	}
}

// nextAgentMsgSuffix returns a process-unique discriminator for the
// next agent message id. TaskStore.Update never enforces uniqueness
// on MessageID; we just need something distinct within a task's
// history so tools consuming tasks/get don't collapse duplicates.
var (
	agentMsgCounterMu sync.Mutex
	agentMsgCounter   uint64
)

func nextAgentMsgSuffix() string {
	agentMsgCounterMu.Lock()
	agentMsgCounter++
	n := agentMsgCounter
	agentMsgCounterMu.Unlock()
	return fmt.Sprintf("%d", n)
}

// hasText reports whether payload is a map carrying a non-empty "text"
// field. Kept separate so extractPayloadText can be called
// unconditionally without repeating the type switch.
func hasText(payload any) bool {
	m, ok := payload.(map[string]any)
	if !ok {
		return false
	}
	s, _ := m["text"].(string)
	return s != ""
}

func extractPayloadText(payload any) string {
	m, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	s, _ := m["text"].(string)
	return s
}

func extractLifecyclePhase(payload any) string {
	m, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	s, _ := m["phase"].(string)
	return s
}

// extractTextParts folds A2A Parts into a single string. A2A v0.3 allows
// a few part kinds (text/file/data); for the MVP we only consume text
// and concat with newlines so multi-part messages arrive at the agent
// intact. Unknown kinds are skipped (not errors) so peers can ship
// richer messages without breaking simple agents.
func extractTextParts(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var parts []struct {
		Kind string `json:"kind"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Kind != "text" || p.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(p.Text)
	}
	return b.String(), nil
}

// a2aPosterTap wraps an AgentEventPoster so every driver-emitted event
// is both forwarded to the hub (the existing contract) and mirrored to
// the A2A correlator. The correlator fans out to open tasks by
// agentID; agents with no live task see zero overhead beyond a mutex
// miss.
type a2aPosterTap struct {
	inner AgentEventPoster
	disp  *a2aHubDispatcher
}

func newA2APosterTap(inner AgentEventPoster, disp *a2aHubDispatcher) *a2aPosterTap {
	return &a2aPosterTap{inner: inner, disp: disp}
}

func (p *a2aPosterTap) PostAgentEvent(ctx context.Context, agentID, kind, producer string, payload any) error {
	if p.disp != nil {
		p.disp.OnAgentEvent(agentID, kind, producer, payload)
	}
	return p.inner.PostAgentEvent(ctx, agentID, kind, producer, payload)
}
