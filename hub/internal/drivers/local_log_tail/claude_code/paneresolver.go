package claudecode

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// CmdRunner is the seam between this package and exec.Command. The
// default real implementation just runs the binary; tests pass a
// fake that returns canned stdout for known commands. Keeps
// paneresolver_test from depending on a live tmux + ps + pgrep on
// the host (CI runners often don't have tmux).
type CmdRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// realRunner is the default CmdRunner; runs each command and
// returns its combined stdout (stderr surfaces in the error).
type realRunner struct{}

func (realRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		// exec.ExitError already carries stderr; wrap so callers can see what command failed.
		return nil, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out, nil
}

// ResolvePane finds the tmux pane id the claude-code process is
// running in. Algorithm per plan §12.5:
//
//  1. Find claude's parent shell PID via `ps -o ppid= -p <pid>`.
//     That parent is the immediate process tmux sees as the pane's
//     pid (`#{pane_pid}` is the shell, not the user's exec'd
//     binary).
//  2. Run `tmux list-panes -aF '#{pane_pid} #{pane_id} #{pane_active}
//     #{session_activity}'` to enumerate every pane on the host.
//  3. Filter to rows whose pane_pid matches the parent shell. If
//     exactly one match, return its pane_id; if multiple (rare,
//     shouldn't happen with distinct shells), prefer pane_active=1
//     then most-recent session_activity.
//
// runner is the CmdRunner that executes the ps / tmux commands;
// nil → realRunner.
func ResolvePane(ctx context.Context, claudePID int, runner CmdRunner) (string, error) {
	if claudePID <= 0 {
		return "", fmt.Errorf("claude-code paneresolver: invalid pid %d", claudePID)
	}
	if runner == nil {
		runner = realRunner{}
	}

	// Step 1: claude's parent shell PID.
	parentPID, err := parentPIDOf(ctx, runner, claudePID)
	if err != nil {
		return "", fmt.Errorf("resolve parent of pid %d: %w", claudePID, err)
	}
	if parentPID <= 0 {
		return "", fmt.Errorf("claude-code paneresolver: parent of pid %d is 0 (process gone?)", claudePID)
	}

	// Step 2 + 3: list every pane on the host, match on pane_pid.
	out, err := runner.Run(ctx, "tmux", "list-panes", "-aF",
		"#{pane_pid} #{pane_id} #{pane_active} #{session_activity}")
	if err != nil {
		return "", fmt.Errorf("tmux list-panes: %w", err)
	}

	type cand struct {
		paneID   string
		active   bool
		activity int64
	}
	var matches []cand
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		panePID, err := strconv.Atoi(fields[0])
		if err != nil || panePID != parentPID {
			continue
		}
		active := fields[2] == "1"
		activity, _ := strconv.ParseInt(fields[3], 10, 64)
		matches = append(matches, cand{paneID: fields[1], active: active, activity: activity})
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("claude-code paneresolver: no tmux pane has pid %d (parent of claude %d)", parentPID, claudePID)
	}
	// Disambiguate: prefer active, then newest activity.
	best := matches[0]
	for _, m := range matches[1:] {
		if (m.active && !best.active) ||
			(m.active == best.active && m.activity > best.activity) {
			best = m
		}
	}
	return best.paneID, nil
}

// parentPIDOf returns the parent PID of `pid` via `ps -o ppid= -p <pid>`.
// `-o ppid=` suppresses the header so the output is just the integer.
func parentPIDOf(ctx context.Context, runner CmdRunner, pid int) (int, error) {
	out, err := runner.Run(ctx, "ps", "-o", "ppid=", "-p", strconv.Itoa(pid))
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return 0, fmt.Errorf("ps returned empty parent pid for %d", pid)
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("parse parent pid %q: %w", s, err)
	}
	return n, nil
}
