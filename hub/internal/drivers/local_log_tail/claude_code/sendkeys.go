package claudecode

import (
	"context"
	"fmt"
	"strings"
)

// dispatchInput implements Adapter.HandleInput. Plan §6.1 vocabulary
// table verbatim — text / cancel / escape / mode_cycle / action_bar /
// pick_option are the engine-level kinds claude-code accepts via
// tmux send-keys.
//
// Approvals (kind="approval") are explicitly rejected: the approval
// channel (--permission-prompt-tool + parked hooks) owns
// tool_permission / plan_approval / compaction; routing those via
// send-keys would race the MCP path and corrupt the TUI.
//
// PaneID must be set before HandleInput fires for any kind other than
// hard_cancel (which uses ClaudePID). The W7 launch glue resolves it
// via ResolvePane and writes a.PaneID before announcing the agent as
// running.
func (a *Adapter) dispatchInput(ctx context.Context, kind string, payload map[string]any) error {
	switch kind {
	case "text", "slash_command":
		return a.inputText(ctx, payload)
	case "cancel":
		return a.inputCancel(ctx)
	case "hard_cancel":
		return a.inputHardCancel(ctx)
	case "escape":
		return a.inputSendKey(ctx, "Escape")
	case "mode_cycle":
		return a.inputSendKey(ctx, "S-Tab")
	case "action_bar":
		name, _ := payload["name"].(string)
		if name == "" {
			return fmt.Errorf("claude-code adapter: action_bar input requires `name`")
		}
		return a.inputSendKey(ctx, name)
	case "pick_option":
		// Plan §6.1 + §5.B.1: arrow-key navigate to option index
		// then Enter. Multi-question payloads need W2i's picker
		// state machine to know which question is current; W2h's
		// pick_option assumes the caller specifies the index for
		// the current TUI prompt.
		idx, _ := payload["index"].(float64) // JSON numbers decode to float64
		if idx < 0 || idx > 16 {
			return fmt.Errorf("claude-code adapter: pick_option index %g out of range [0,16]", idx)
		}
		return a.inputPickOption(ctx, int(idx))
	case "approval":
		return fmt.Errorf("claude-code adapter: approval inputs are routed via the permission_prompt MCP path, not send-keys (ADR-027 §5.A)")
	default:
		return fmt.Errorf("claude-code adapter: unsupported input kind %q", kind)
	}
}

func (a *Adapter) requirePane() error {
	if a.PaneID == "" {
		return fmt.Errorf("claude-code adapter: pane id not resolved yet")
	}
	return nil
}

// inputText sends a free-text body followed by Enter. Short bodies
// (no embedded newlines, ≤ 512 chars) go via send-keys -l. Longer or
// multi-line bodies go via load-buffer + paste-buffer so the TUI
// receives the whole block atomically rather than streaming one
// character per send-keys round-trip.
func (a *Adapter) inputText(ctx context.Context, p map[string]any) error {
	body, _ := p["body"].(string)
	if body == "" {
		return fmt.Errorf("claude-code adapter: text input requires `body`")
	}
	if err := a.requirePane(); err != nil {
		return err
	}
	runner := a.cmdRunner()
	if len(body) <= 512 && !strings.ContainsAny(body, "\n\r") {
		if _, err := runner.Run(ctx, "tmux", "send-keys", "-t", a.PaneID, "-l", body); err != nil {
			return err
		}
	} else {
		// load-buffer reads from stdin; we can't easily plumb stdin
		// through CmdRunner. As an MVP fallback, fold newlines into
		// `tmux send-keys -l <line>; tmux send-keys Enter` per line.
		// Pasting-via-buffer becomes a W2-plus tightening once the
		// CmdRunner interface grows a Stdin field.
		for _, line := range strings.Split(body, "\n") {
			if _, err := runner.Run(ctx, "tmux", "send-keys", "-t", a.PaneID, "-l", line); err != nil {
				return err
			}
			if _, err := runner.Run(ctx, "tmux", "send-keys", "-t", a.PaneID, "Enter"); err != nil {
				return err
			}
		}
		return nil
	}
	_, err := runner.Run(ctx, "tmux", "send-keys", "-t", a.PaneID, "Enter")
	return err
}

// inputSendKey sends a single named key. The caller passes the tmux
// key-name (Enter, Escape, C-c, S-Tab, Up, Down, Tab, F1, …); tmux
// expands these without -l. Validated against a small allowlist so a
// rogue payload can't slip an arbitrary `; rm -rf /` into argv.
func (a *Adapter) inputSendKey(ctx context.Context, key string) error {
	if !isAllowedTmuxKey(key) {
		return fmt.Errorf("claude-code adapter: send_key %q not in allowlist", key)
	}
	if err := a.requirePane(); err != nil {
		return err
	}
	_, err := a.cmdRunner().Run(ctx, "tmux", "send-keys", "-t", a.PaneID, key)
	return err
}

// inputCancel sends C-c to the pane. The hard-fallback (kill -INT)
// is a separate kind (hard_cancel) so mobile can decide when to
// escalate; W2h does not auto-escalate on a timer.
func (a *Adapter) inputCancel(ctx context.Context) error {
	if err := a.requirePane(); err != nil {
		return err
	}
	_, err := a.cmdRunner().Run(ctx, "tmux", "send-keys", "-t", a.PaneID, "C-c")
	return err
}

// inputHardCancel SIGINTs the claude process directly. Used when
// soft cancel didn't catch (claude was stuck in a tool call, etc.).
// Requires ClaudePID; if unset the call errors. We use SIGINT not
// SIGKILL so claude has a chance to write its session JSONL footer
// before exiting.
func (a *Adapter) inputHardCancel(ctx context.Context) error {
	if a.ClaudePID <= 0 {
		return fmt.Errorf("claude-code adapter: hard_cancel requires ClaudePID (not set)")
	}
	_, err := a.cmdRunner().Run(ctx, "kill", "-INT", fmt.Sprintf("%d", a.ClaudePID))
	return err
}

// inputPickOption navigates the AskUserQuestion picker to the
// requested option index (0-based) and presses Enter. Plan §6.1
// "AskUserQuestion: pick option i" row: `Down × i; Enter`.
//
// Index 0 is the default-highlighted option, so no Down keystrokes
// are needed before Enter.
func (a *Adapter) inputPickOption(ctx context.Context, idx int) error {
	if err := a.requirePane(); err != nil {
		return err
	}
	runner := a.cmdRunner()
	for i := 0; i < idx; i++ {
		if _, err := runner.Run(ctx, "tmux", "send-keys", "-t", a.PaneID, "Down"); err != nil {
			return err
		}
	}
	_, err := runner.Run(ctx, "tmux", "send-keys", "-t", a.PaneID, "Enter")
	return err
}

// HandleInput is the locallogtail.Adapter entry; routes to
// dispatchInput. The W2a stub that always errored is replaced by
// the real implementation in W2h.
func (a *Adapter) HandleInput(ctx context.Context, kind string, payload map[string]any) error {
	return a.dispatchInput(ctx, kind, payload)
}

// allowedTmuxKeys is the closed set of tmux key-names HandleInput
// accepts for "escape" / "mode_cycle" / "action_bar" inputs. Anything
// outside this list returns an error so a malformed mobile payload
// can't smuggle shell metacharacters through argv. Add new keys here
// when the mobile action-bar grows new buttons.
var allowedTmuxKeys = map[string]struct{}{
	"Enter": {}, "Escape": {}, "S-Tab": {}, "Tab": {},
	"Up": {}, "Down": {}, "Left": {}, "Right": {},
	"PageUp": {}, "PageDown": {}, "Home": {}, "End": {},
	"C-c": {}, "C-d": {}, "C-r": {}, "C-l": {},
	"F1": {}, "F2": {}, "F3": {}, "F4": {}, "F5": {},
	"F6": {}, "F7": {}, "F8": {}, "F9": {}, "F10": {},
	"F11": {}, "F12": {},
	"Space": {}, "BSpace": {},
}

func isAllowedTmuxKey(key string) bool {
	_, ok := allowedTmuxKeys[key]
	return ok
}

// cmdRunner returns the adapter's CmdRunner if non-nil, otherwise
// the real exec-backed runner. Lazily looked up so tests can swap it
// in via the CmdRunner field on the adapter.
func (a *Adapter) cmdRunner() CmdRunner {
	if a.CmdRunner != nil {
		return a.CmdRunner
	}
	return realRunner{}
}
