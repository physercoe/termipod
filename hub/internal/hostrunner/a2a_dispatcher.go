package hostrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
// POSTs them to the hub's /input endpoint for that agent. The hub then
// writes an agent_events row which the local InputRouter picks up and
// delivers to the driver via Inputter.Input.
//
// Going through the hub (rather than calling the driver directly) keeps
// peer input on the same audit path as phone/web input and means no new
// cross-cutting state is introduced for A2A. Producer attribution is
// currently "user" — a follow-up wedge adds a producer='a2a' distinction
// when the input endpoint grows a source field.
//
// The task is left in "submitted" on success; the driver's downstream
// output won't (yet) flow back into the A2A task history. Closing that
// loop is tracked alongside the A2A stream-response work.
type a2aHubDispatcher struct {
	poster a2aInputPoster
}

func newA2AHubDispatcher(c a2aInputPoster) *a2aHubDispatcher {
	return &a2aHubDispatcher{poster: c}
}

// Dispatch extracts text from msg.Parts and posts it as kind=text input
// to the hub. A message with no text parts yields an error so the
// message/send handler can mark the task failed rather than silently
// dropping the submission.
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
		"kind": "text",
		"body": text,
	}); err != nil {
		return fmt.Errorf("%w: post input: %v", a2a.ErrDispatch, err)
	}
	// Leave the task in "submitted" — the hub's agent_events stream is
	// the canonical progress channel; A2A tasks/get is best-effort.
	return nil
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
