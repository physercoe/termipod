package hostrunner

import (
	"context"
	"strings"
)

// paneInfo is what `tmux list-panes -a` tells us about one pane. cmd is the
// foreground process name as tmux sees it (pane_current_command). dead is
// true when tmux keeps the pane around after its command exited (requires
// `set -g remain-on-exit on`); by default tmux removes the pane entirely so
// "missing from the map" is the common crash signal.
type paneInfo struct {
	cmd  string
	dead bool
}

// listTmuxPanes returns a map keyed by pane_id (e.g. "%17") of the current
// fg-command and dead state for every pane the host-runner's tmux server
// knows about. Empty map on error so callers can still treat "no info" as
// "don't transition".
func listTmuxPanes(ctx context.Context) (map[string]paneInfo, error) {
	out, err := runTmux(ctx, "list-panes", "-a", "-F",
		"#{pane_id} #{pane_current_command} #{pane_dead}")
	if err != nil {
		return nil, err
	}
	m := map[string]paneInfo{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		pid := parts[0]
		cmd := parts[1]
		dead := false
		if len(parts) >= 3 && parts[2] == "1" {
			dead = true
		}
		m[pid] = paneInfo{cmd: cmd, dead: dead}
	}
	return m, nil
}

// loginShells are the fg-commands that indicate "no backend CLI is running
// in this pane right now" — either the pane is still booting, or the CLI
// exited and the shell came back to the foreground (only possible with
// remain-on-exit off + a shell wrapper, or when the pane was launched via
// `tmux new-window 'cmd'` that under the hood went through `sh -c`).
var loginShells = map[string]bool{
	"bash": true, "sh": true, "zsh": true,
	"fish": true, "dash": true, "ash": true,
	"ksh": true, "tcsh": true, "csh": true,
}

func isLoginShell(cmd string) bool {
	return loginShells[cmd]
}

// tickReconcile reads tmux ground truth once and PATCHes each non-terminated
// agent on this host to the status that matches its pane:
//
//   - pane_id empty       → skip (spawn hasn't run yet; launchOne owns it)
//   - pane missing / dead → crashed
//   - fg is a shell       → pending (CLI not running yet, or just exited)
//   - fg is anything else → running
//
// Called every poll tick. Per-agent errors are logged and skipped so one
// bad agent doesn't block the others.
func (a *Runner) tickReconcile(ctx context.Context) {
	agents, err := a.Client.ListHostAgents(ctx, a.HostID)
	if err != nil {
		a.Log.Debug("list host agents failed", "err", err)
		return
	}
	panes, err := listTmuxPanes(ctx)
	if err != nil {
		a.Log.Debug("list-panes failed", "err", err)
		return
	}
	for _, ag := range agents {
		if ag.Status == "terminated" || ag.Status == "failed" || ag.Status == "crashed" {
			continue
		}
		if ag.PaneID == "" {
			continue
		}
		info, ok := panes[ag.PaneID]
		var want string
		switch {
		case !ok || info.dead:
			want = "crashed"
		case isLoginShell(info.cmd):
			want = "pending"
		default:
			want = "running"
		}
		if want == ag.Status {
			continue
		}
		patch := AgentPatch{Status: &want}
		if err := a.Client.PatchAgent(ctx, ag.ID, patch); err != nil {
			a.Log.Warn("reconcile patch failed", "agent", ag.ID, "err", err)
			continue
		}
		a.Log.Info("agent reconciled",
			"agent", ag.ID, "handle", ag.Handle,
			"pane", ag.PaneID, "fg", info.cmd, "from", ag.Status, "to", want)
	}
}
