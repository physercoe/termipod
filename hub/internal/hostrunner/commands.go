package hostrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// runCommand executes a host-directed command and reports the outcome back
// to the hub. Supported kinds: pause, resume, capture, terminate. Each maps
// to a tmux/POSIX primitive; unknown kinds fail fast with a clear error so
// the operator sees why.
func (a *Runner) runCommand(ctx context.Context, cmd HostCommand) {
	var (
		result map[string]any
		err    error
	)
	switch cmd.Kind {
	case "pause":
		err = a.signalPane(ctx, cmd, syscall.SIGSTOP)
	case "resume":
		err = a.signalPane(ctx, cmd, syscall.SIGCONT)
	case "capture":
		result, err = a.capturePane(ctx, cmd)
	case "terminate":
		err = a.terminatePane(ctx, cmd)
	default:
		err = fmt.Errorf("unknown command kind: %s", cmd.Kind)
	}

	patch := CommandPatch{Status: "done"}
	if err != nil {
		patch.Status = "failed"
		patch.Error = err.Error()
		a.Log.Warn("host command failed", "id", cmd.ID, "kind", cmd.Kind, "err", err)
	}
	if result != nil {
		b, _ := json.Marshal(result)
		patch.Result = b
	}
	if perr := a.Client.PatchCommand(ctx, cmd.ID, patch); perr != nil {
		a.Log.Warn("patch command failed", "id", cmd.ID, "err", perr)
	}
}

// paneTarget reads pane_id from args. Falls back to an empty string; the
// caller decides whether that's fatal.
func paneTarget(args json.RawMessage) string {
	var m map[string]any
	_ = json.Unmarshal(args, &m)
	if v, ok := m["pane_id"].(string); ok {
		return v
	}
	return ""
}

func (a *Runner) signalPane(ctx context.Context, cmd HostCommand, sig syscall.Signal) error {
	pane := paneTarget(cmd.Args)
	if pane == "" {
		return fmt.Errorf("pause/resume: pane_id required")
	}
	pidStr, err := runTmux(ctx, "display-message", "-p", "-t", pane, "#{pane_pid}")
	if err != nil {
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(pidStr))
	if err != nil {
		return fmt.Errorf("parse pane_pid %q: %w", pidStr, err)
	}
	// Signal the process group so the whole subtree pauses, not just the shell.
	if err := syscall.Kill(-pid, sig); err != nil {
		// Fall back to the single pid if the pgid path is denied.
		return syscall.Kill(pid, sig)
	}
	return nil
}

func (a *Runner) capturePane(ctx context.Context, cmd HostCommand) (map[string]any, error) {
	pane := paneTarget(cmd.Args)
	if pane == "" {
		return nil, fmt.Errorf("capture: pane_id required")
	}
	// -p: print to stdout, -J: join wrapped lines, -e: keep colour escapes off
	// so the cached text is grep-able.
	text, err := runTmux(ctx, "capture-pane", "-p", "-J", "-t", pane)
	if err != nil {
		return nil, err
	}
	return map[string]any{"text": text}, nil
}

func (a *Runner) terminatePane(ctx context.Context, cmd HostCommand) error {
	pane := paneTarget(cmd.Args)
	if pane == "" && cmd.AgentID == "" {
		return fmt.Errorf("terminate: pane_id or agent_id required")
	}

	// Always tear the driver down first when one is registered, even
	// when a pane is attached. M1/M2 land here with BOTH a pane (the
	// cosmetic tail-F display) AND a registered driver (the long-lived
	// engine subprocess). Killing the pane alone left the driver
	// leaking — no lifecycle.stopped, no Closer firing — and the
	// "[host-runner] M1 stopped at ..." notice the Closer writes never
	// reached the cosmetic log because it was never written. Driver
	// teardown also closes the engine's stdio cleanly so a follow-up
	// kill-pane is purely cosmetic at that point.
	if cmd.AgentID != "" {
		if _, ok := a.drivers[cmd.AgentID]; ok {
			a.stopDriver(cmd.AgentID)
			// Brief flush window — the Closer just wrote a final
			// "stopped at ..." line into the cosmetic log; the pane's
			// tail -F needs ~tens of milliseconds to pick it up via
			// inotify (or its polling fallback) and tmux needs to
			// render the new line before kill-pane destroys the pane
			// buffer. Without this sleep the pane is killed faster
			// than tail can read, and the operator sees the pane
			// vanish with no farewell.
			if pane != "" {
				time.Sleep(150 * time.Millisecond)
			}
		} else if pane == "" {
			// Paneless and no driver — common when the agent was on
			// another host or already torn down. Don't fail the
			// command; the hub-side terminate semantics ("ensure not
			// running") are already satisfied.
			return nil
		}
	}

	if pane != "" {
		if _, err := runTmux(ctx, "kill-pane", "-t", pane); err != nil {
			// "can't find pane: %N" means the pane already exited
			// (e.g. the agent crashed before terminate fired). The
			// semantic goal of terminate — "ensure not running" —
			// is satisfied, so don't bubble the error and fail the
			// command. Other tmux errors (server gone, permission)
			// still propagate.
			if !strings.Contains(err.Error(), "can't find pane") {
				return err
			}
		}
	}
	// Best-effort worktree cleanup. A dirty tree is preserved and flagged
	// on the hub so a human can inspect before discarding — never silently
	// destroy uncommitted work.
	if cmd.AgentID != "" {
		if wt, ok := a.worktrees[cmd.AgentID]; ok {
			dirty, werr := RemoveWorktree(ctx, wt)
			if werr != nil {
				a.Log.Warn("worktree remove failed",
					"agent", cmd.AgentID, "path", wt.Path, "err", werr)
			} else if dirty {
				a.Log.Info("worktree left in place (dirty)",
					"agent", cmd.AgentID, "path", wt.Path)
			}
			delete(a.worktrees, cmd.AgentID)
		}
	}
	return nil
}

