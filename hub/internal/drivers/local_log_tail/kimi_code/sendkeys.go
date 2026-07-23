package kimi_code

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// CmdRunner is the seam between this package and exec.Command, so tests
// don't need a live tmux on PATH. Mirrors the antigravity adapter's
// CmdRunner; the default real implementation just runs the binary.
type CmdRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type realRunner struct{}

func (realRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out, nil
}

func (a *Adapter) cmdRunner() CmdRunner {
	if a.CmdRunner != nil {
		return a.CmdRunner
	}
	return realRunner{}
}

// HandleInput translates a mobile input into a kimi-side action via
// tmux send-keys. kimi's interactive UI is a TUI like claude-code /
// agy, so the vocabulary mirrors the antigravity adapter's: text,
// cancel, escape, the arrow-nav picker — minus claude-code's MCP-routed
// approval channel (kimi has no host-runner hook surface; a permission
// prompt inside the pane is answered with the arrow-nav picker, driven
// by pick_option / action_bar).
func (a *Adapter) HandleInput(ctx context.Context, kind string, payload map[string]any) error {
	switch kind {
	case "text", "slash_command":
		return a.inputText(ctx, payload)
	case "attention_reply":
		// The hub fans out the principal's /decide on an attention as
		// `input.attention_reply` to the owning agent. Other drivers
		// re-render it as a fresh user turn; for a TUI engine the same
		// approach works — render the structured payload into a
		// humanised line and send-keys it as normal text input.
		// Mirrors the antigravity adapter's attention_reply arm.
		body := formatAttentionReplyText(payload)
		if body == "" {
			return fmt.Errorf("kimi-code adapter: attention_reply produced no text")
		}
		return a.inputText(ctx, map[string]any{"body": body})
	case "cancel":
		return a.inputSendKey(ctx, "C-c")
	case "escape":
		return a.inputSendKey(ctx, "Escape")
	case "pick_option":
		// kimi's permission/choice menus navigate with Up/Down + Enter.
		// index is the 0-based target row.
		idx, _ := payload["index"].(float64) // JSON numbers decode to float64
		if idx < 0 || idx > 16 {
			return fmt.Errorf("kimi-code adapter: pick_option index %g out of range [0,16]", idx)
		}
		return a.inputPickOption(ctx, int(idx))
	case "action_bar":
		name, _ := payload["name"].(string)
		if !isAllowedTmuxKey(name) {
			return fmt.Errorf("kimi-code adapter: action_bar key %q not in allowlist", name)
		}
		return a.inputSendKey(ctx, name)
	default:
		return fmt.Errorf("kimi-code adapter: unsupported input kind %q", kind)
	}
}

func (a *Adapter) requirePane() error {
	if a.PaneID == "" {
		return fmt.Errorf("kimi-code adapter: pane id not resolved yet")
	}
	return nil
}

// inputText sends a free-text body as ONE atomic submission, then
// Enter. Multi-line bodies go via tmux's named-buffer paste path —
// identical to the antigravity adapter's inputText (see its comment
// for the full `-r` rationale: without it tmux translates internal LF
// into CR keystrokes and each line arrives as a separate submission).
func (a *Adapter) inputText(ctx context.Context, p map[string]any) error {
	body, _ := p["body"].(string)
	if body == "" {
		return fmt.Errorf("kimi-code adapter: text input requires `body`")
	}
	if err := a.requirePane(); err != nil {
		return err
	}
	runner := a.cmdRunner()

	// Single-line, short → cheap path: send-keys -l + Enter.
	if len(body) <= 512 && !strings.ContainsAny(body, "\n\r") {
		if _, err := runner.Run(ctx, "tmux", "send-keys", "-t", a.PaneID, "-l", body); err != nil {
			return err
		}
		_, err := runner.Run(ctx, "tmux", "send-keys", "-t", a.PaneID, "Enter")
		return err
	}

	// Multi-line / long → atomic paste-buffer.
	bufName := "kimiinput_" + strings.TrimPrefix(a.PaneID, "%")
	if _, err := runner.Run(ctx, "tmux", "set-buffer", "-b", bufName, body); err != nil {
		return fmt.Errorf("set-buffer: %w", err)
	}
	if _, err := runner.Run(ctx, "tmux", "paste-buffer", "-b", bufName, "-d", "-r", "-t", a.PaneID); err != nil {
		_, _ = runner.Run(ctx, "tmux", "delete-buffer", "-b", bufName)
		return fmt.Errorf("paste-buffer: %w", err)
	}
	if _, err := runner.Run(ctx, "tmux", "send-keys", "-t", a.PaneID, "Enter"); err != nil {
		return err
	}
	return nil
}

// inputSendKey sends one named tmux key from the allowlist (so a rogue
// payload can't smuggle shell metacharacters through argv).
func (a *Adapter) inputSendKey(ctx context.Context, key string) error {
	if !isAllowedTmuxKey(key) {
		return fmt.Errorf("kimi-code adapter: send_key %q not in allowlist", key)
	}
	if err := a.requirePane(); err != nil {
		return err
	}
	_, err := a.cmdRunner().Run(ctx, "tmux", "send-keys", "-t", a.PaneID, key)
	return err
}

// inputPickOption navigates the arrow-nav menu down `idx` rows then
// Enter. Index 0 is the default-highlighted row (Enter only).
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

// formatAttentionReplyText renders an `input.attention_reply` payload
// into the humanised line we feed back into kimi's chat as a normal
// turn. Mirrors `hostrunner.formatAttentionReplyText` (driver_stdio.go)
// — same prefix scheme + per-kind format — so the agent reads the same
// shape regardless of which engine raised the original request_*
// attention. Duplicated rather than shared because the hostrunner
// package already imports this one (would be a cycle), matching the
// antigravity adapter's note.
func formatAttentionReplyText(payload map[string]any) string {
	kind, _ := payload["kind"].(string)
	reqID, _ := payload["request_id"].(string)
	decision, _ := payload["decision"].(string)
	body, _ := payload["body"].(string)
	option, _ := payload["option_id"].(string)
	reason, _ := payload["reason"].(string)

	prefix := ""
	if reqID != "" {
		short := reqID
		if len(short) > 8 {
			short = short[:8]
		}
		prefix = "[reply to " + kind + " " + short + "] "
	}

	switch kind {
	case "approval_request":
		switch decision {
		case "approve":
			if reason != "" {
				return prefix + "Approved. Reason: " + reason
			}
			return prefix + "Approved."
		case "reject":
			if reason != "" {
				return prefix + "Rejected. Reason: " + reason
			}
			return prefix + "Rejected."
		}
		return prefix + decision
	case "select":
		if decision == "reject" {
			if reason != "" {
				return prefix + "No option chosen. Reason: " + reason
			}
			return prefix + "No option chosen."
		}
		if option != "" {
			return prefix + "Selected: " + option
		}
		return prefix + "Selected."
	case "help_request":
		if decision == "reject" {
			if reason != "" {
				return prefix + "Dismissed without reply. Reason: " + reason
			}
			return prefix + "Dismissed without reply."
		}
		if body != "" {
			return prefix + body
		}
		return prefix + "(empty reply)"
	}
	if body != "" {
		return prefix + body
	}
	return prefix + decision
}

// allowedTmuxKeys is the closed set HandleInput accepts for named-key
// inputs. Mirrors the antigravity adapter's allowlist.
var allowedTmuxKeys = map[string]struct{}{
	"Enter": {}, "Escape": {}, "Tab": {}, "S-Tab": {},
	"Up": {}, "Down": {}, "Left": {}, "Right": {},
	"PageUp": {}, "PageDown": {}, "Home": {}, "End": {},
	"C-c": {}, "C-d": {}, "C-r": {}, "C-l": {},
	"Space": {}, "BSpace": {},
}

func isAllowedTmuxKey(key string) bool {
	_, ok := allowedTmuxKeys[key]
	return ok
}
