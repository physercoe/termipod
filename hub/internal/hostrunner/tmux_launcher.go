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
// Backend choice is resolved by the caller: Runner.launchOne reads the
// per-spawn backend.cmd (from spawn_spec_yaml) and the per-kind template
// default, and calls LaunchCmd with whichever wins. DefaultCmd is only
// reached via Launch(), which the runner uses when neither the spec nor
// the template declares a command — a legitimate bootstrap / stub state.
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
		// W8: harden the default placeholder. Pre-bundle the default
		// ended with `exec bash`, leaving an interactive shell that
		// PaneDriver subsequently keystroke-pumped the task prompt
		// into — see the v1.0.619 incident in
		// docs/discussions/validate-at-every-boundary.md §1. The
		// upstream W7 hostrunner refusal should prevent this path
		// from being reached for malformed spawns, but if it ever IS
		// reached (legitimate placeholder use, future code path,
		// regression), the pane exits immediately so the reconciler
		// observes a dead pane instead of a bash prompt that looks
		// alive but isn't.
		defaultCmd = `bash -c 'echo "[host-runner] FATAL: launcher reached without backend.cmd. This is a bug; refusing to start interactive shell. See logs."; exit 1'`
	}
	return &TmuxLauncher{Session: session, DefaultCmd: defaultCmd, Log: log}
}

func (t *TmuxLauncher) Launch(ctx context.Context, sp Spawn) (string, error) {
	return t.LaunchCmd(ctx, sp, t.DefaultCmd)
}

func (t *TmuxLauncher) LaunchCmd(ctx context.Context, sp Spawn, cmd string) (string, error) {
	if err := t.ensureSession(ctx); err != nil {
		return "", err
	}
	if cmd == "" {
		cmd = t.DefaultCmd
	}
	window := sanitizeWindowName(sp.Handle)
	// -d: don't switch the client, -n: window name, -P + -F: print pane_id.
	// We deliberately return `#{pane_id}` (the `%N` form) and not the
	// session:window.pane target spec: pane_id is the canonical key used
	// by `list-panes` output, so reconcile.go's lookup hits. tmux accepts
	// %N anywhere a target is expected (capture-pane, send-keys, kill-pane).
	target, err := runTmux(ctx,
		"new-window", "-d", "-t", t.Session+":",
		"-n", window,
		"-P", "-F", "#{pane_id}",
		cmd,
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
