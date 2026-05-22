package antigravity

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// CmdRunner is the seam between this package and exec.Command, so tests
// don't need a live tmux on PATH. Mirrors the claude_code adapter's
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

// HandleInput translates a mobile input into an agy-side action via tmux
// send-keys (ADR-035 D5). agy is a TUI like claude-code, so the
// vocabulary is the same — text, cancel, escape, the arrow-nav picker —
// minus claude-code's MCP-routed approval channel (agy has no
// permission-prompt-tool, so an approval here IS the arrow-nav picker;
// see D6 / Phase 2 W9).
func (a *Adapter) HandleInput(ctx context.Context, kind string, payload map[string]any) error {
	switch kind {
	case "text", "slash_command":
		return a.inputText(ctx, payload)
	case "cancel":
		return a.inputSendKey(ctx, "C-c")
	case "escape":
		return a.inputSendKey(ctx, "Escape")
	case "pick_option":
		// agy permission/choice menus navigate with Up/Down + Enter
		// (host-verified). index is the 0-based target row.
		idx, _ := payload["index"].(float64) // JSON numbers decode to float64
		if idx < 0 || idx > 16 {
			return fmt.Errorf("antigravity adapter: pick_option index %g out of range [0,16]", idx)
		}
		return a.inputPickOption(ctx, int(idx))
	case "action_bar":
		name, _ := payload["name"].(string)
		if !isAllowedTmuxKey(name) {
			return fmt.Errorf("antigravity adapter: action_bar key %q not in allowlist", name)
		}
		return a.inputSendKey(ctx, name)
	default:
		return fmt.Errorf("antigravity adapter: unsupported input kind %q", kind)
	}
}

func (a *Adapter) requirePane() error {
	if a.PaneID == "" {
		return fmt.Errorf("antigravity adapter: pane id not resolved yet")
	}
	return nil
}

// inputText sends a free-text body then Enter. Multi-line bodies are sent
// line-by-line (send-keys -l then Enter) so the TUI receives each line;
// short single-line bodies go in one -l call. Mirrors the claude-code
// adapter's MVP approach (buffer-paste is a later tightening).
func (a *Adapter) inputText(ctx context.Context, p map[string]any) error {
	body, _ := p["body"].(string)
	if body == "" {
		return fmt.Errorf("antigravity adapter: text input requires `body`")
	}
	if err := a.requirePane(); err != nil {
		return err
	}
	runner := a.cmdRunner()
	if len(body) <= 512 && !strings.ContainsAny(body, "\n\r") {
		if _, err := runner.Run(ctx, "tmux", "send-keys", "-t", a.PaneID, "-l", body); err != nil {
			return err
		}
		_, err := runner.Run(ctx, "tmux", "send-keys", "-t", a.PaneID, "Enter")
		return err
	}
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

// inputSendKey sends one named tmux key from the allowlist (so a rogue
// payload can't smuggle shell metacharacters through argv).
func (a *Adapter) inputSendKey(ctx context.Context, key string) error {
	if !isAllowedTmuxKey(key) {
		return fmt.Errorf("antigravity adapter: send_key %q not in allowlist", key)
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

// allowedTmuxKeys is the closed set HandleInput accepts for named-key
// inputs. Add keys here as the mobile action-bar grows.
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
