package hostrunner

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

// TmuxLauncher places each spawned agent in a dedicated tmux window under
// a shared session (default "hub-agents"). The mobile client's `↗ pane`
// chip renders the returned pane target; the host-runner itself is not in
// the hot path for pane output (viewers SSH to tmux directly, plan §5).
//
// Backend choice: for now every pane runs DefaultCmd. A real launcher would
// parse spawn_spec_yaml.backend.{kind,cmd} and dispatch accordingly — that
// lands with the YAML parser in a later slice.
type TmuxLauncher struct {
	Session    string // tmux session name; created lazily
	DefaultCmd string // e.g. `bash -c 'echo hello; exec bash'`
	Log        *slog.Logger
}

func NewTmuxLauncher(session, defaultCmd string, log *slog.Logger) *TmuxLauncher {
	if session == "" {
		session = "hub-agents"
	}
	if defaultCmd == "" {
		defaultCmd = `bash -c 'echo "[host-runner] pane ready; no backend configured"; exec bash'`
	}
	return &TmuxLauncher{Session: session, DefaultCmd: defaultCmd, Log: log}
}

func (t *TmuxLauncher) Launch(ctx context.Context, sp Spawn) (string, error) {
	if err := t.ensureSession(ctx); err != nil {
		return "", err
	}
	window := sanitizeWindowName(sp.Handle)
	// -d: don't switch the client, -n: window name, -P + -F: print target
	target, err := runTmux(ctx,
		"new-window", "-d", "-t", t.Session+":",
		"-n", window,
		"-P", "-F", "#{session_name}:#{window_index}.#{pane_index}",
		t.DefaultCmd,
	)
	if err != nil {
		return "", fmt.Errorf("new-window: %w", err)
	}
	pane := strings.TrimSpace(target)
	if t.Log != nil {
		t.Log.Info("tmux-launch", "handle", sp.Handle, "pane", pane)
	}
	return pane, nil
}

func (t *TmuxLauncher) ensureSession(ctx context.Context) error {
	// has-session returns non-zero if missing — that's our signal to create.
	if err := exec.CommandContext(ctx, "tmux", "has-session", "-t", t.Session).Run(); err == nil {
		return nil
	}
	if _, err := runTmux(ctx, "new-session", "-d", "-s", t.Session, "-n", "_bootstrap"); err != nil {
		return fmt.Errorf("new-session: %w", err)
	}
	return nil
}

func runTmux(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "tmux", args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, string(out))
	}
	return string(out), nil
}

// sanitizeWindowName strips characters that confuse tmux target parsing.
// tmux allows most printable chars, but `.` and `:` are separators in targets.
func sanitizeWindowName(handle string) string {
	r := strings.NewReplacer(":", "_", ".", "_", " ", "_")
	return r.Replace(handle)
}
