package hostagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"syscall"
)

// runCommand executes a host-directed command and reports the outcome back
// to the hub. Supported kinds: pause, resume, capture, terminate. Each maps
// to a tmux/POSIX primitive; unknown kinds fail fast with a clear error so
// the operator sees why.
func (a *Agent) runCommand(ctx context.Context, cmd HostCommand) {
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

func (a *Agent) signalPane(ctx context.Context, cmd HostCommand, sig syscall.Signal) error {
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

func (a *Agent) capturePane(ctx context.Context, cmd HostCommand) (map[string]any, error) {
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

func (a *Agent) terminatePane(ctx context.Context, cmd HostCommand) error {
	pane := paneTarget(cmd.Args)
	if pane == "" {
		return fmt.Errorf("terminate: pane_id required")
	}
	_, err := runTmux(ctx, "kill-pane", "-t", pane)
	return err
}

