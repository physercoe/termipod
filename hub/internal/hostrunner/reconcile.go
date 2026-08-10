package hostrunner

import (
	"context"
	"strconv"
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

// paneMeta is what the pane-state watcher needs to know about a pane besides
// its screen: the OSC title the app set, and when output last reached it.
//
// activity is `#{window_activity}` — epoch SECONDS, and per WINDOW, not per
// pane. tmux 3.4 has no per-pane activity stamp (the `pane_*` format list has
// none; verified against the 3.4 man page and source), so a window holding two
// agent panes reports one timestamp for both. That errs towards capturing when
// nothing changed, which is the harmless direction. 0 means "unknown" — an
// unparseable or absent value must never look like "nothing happened".
type paneMeta struct {
	title    string
	activity int64
}

// listTmuxPaneMeta returns pane_id → paneMeta for every pane on the server in
// ONE round-trip (pane-state plan D-4 for the title, B5 for the activity
// stamp; P2 left a note that P3's gating folds into this same call).
//
// Kept separate from listTmuxPanes rather than folded into it: a title is
// free-form text that can contain spaces, so it has to be LAST in the format
// string and parsed positionally, and widening the reconcile format to carry
// it would put a field that can contain anything ahead of nothing but still
// change a parser three transitions depend on.
func listTmuxPaneMeta(ctx context.Context) (map[string]paneMeta, error) {
	out, err := runTmux(ctx, "list-panes", "-a", "-F",
		"#{pane_id} #{window_activity} #{pane_title}")
	if err != nil {
		return nil, err
	}
	return parsePaneMeta(out), nil
}

// parsePaneMeta splits `<pane_id> <window_activity> <title...>` lines. Cut on
// the first two spaces only: neither a pane id (`%17`) nor an epoch stamp ever
// contains one, and a title routinely does.
//
// A tmux too old to know `#{window_activity}` expands it to the empty string,
// which lands here as an unparseable field and resolves to activity 0 — the
// gate then never skips. Same for a title-only line from any other shape we
// have not anticipated: the pane id is the only field this function insists on.
func parsePaneMeta(out string) map[string]paneMeta {
	m := map[string]paneMeta{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		id, rest, _ := strings.Cut(line, " ")
		if id == "" {
			continue
		}
		activity, title, _ := strings.Cut(rest, " ")
		secs, err := strconv.ParseInt(activity, 10, 64)
		if err != nil || secs < 0 {
			secs = 0
		}
		m[id] = paneMeta{title: title, activity: secs}
	}
	return m
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

// isM2TailCmd matches the `tail -f <log>` wrapper that M2's tmux
// launcher runs in the display pane. tmux's pane_current_command shows
// the basename, so plain "tail" is the steady state. We accept the
// `gtail` GNU variant on macOS because Homebrew installs it that way.
func isM2TailCmd(cmd string) bool {
	return cmd == "tail" || cmd == "gtail"
}

// tickReconcile reads tmux ground truth once and PATCHes each non-terminated
// agent on this host to the status that matches its pane:
//
//   - pane_id empty       → skip (spawn hasn't run yet; launchOne owns it)
//   - pane missing / dead → crashed
//   - pane is `tail` w/o
//     a Go driver         → crashed (M2 zombie — see "Why tail without a
//     driver = crashed" below)
//   - fg is a shell       → pending (CLI not running yet, or just exited)
//   - fg is anything else → running
//
// Called every poll tick. Per-agent errors are logged and skipped so one
// bad agent doesn't block the others.
//
// Why "tail without a driver = crashed":
//
// M2 spawns own the agent process directly (host-runner is the parent),
// and the tmux pane runs `tail -f <log>` to mirror the driver's stdout.
// When host-runner exits (crash, upgrade, systemd restart), the agent
// process dies with it (parent pipes close → SIGPIPE), but the tail
// pane keeps running because tmux is independent. The reconcile loop
// used to see `cmd=tail` and conclude "running" — leaving a zombie
// agent row pointing at a dead process. Subsequent input from the
// mobile got delivered to nowhere; new sessions opened against the
// same agent saw no response.
//
// Treating a `tail` pane without a corresponding in-process driver as
// crashed is correct because the only way to get a tail pane is via
// M2 launch, and the only way for it to be live is if the current
// host-runner instance launched it (and therefore has the driver).
// Empty `a.drivers` after a restart means none of the prior M2 panes
// are alive, regardless of what tmux says.
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
		hasDriver := a.hasDriver(ag.ID)
		var want string
		switch {
		case !ok || info.dead:
			want = "crashed"
		case isM2TailCmd(info.cmd) && !hasDriver && ag.Status == "running":
			// M2 zombie: the tail pane outlived the agent process.
			// Status filter ensures we don't trip on the brief window
			// during a fresh launchM2 where the pane exists but the
			// driver hasn't been registered yet (agent is still
			// `pending` in that window). See top-of-function comment.
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

	// Driver teardown lives here (not in tickIdle) so a freshly-spawned M2
	// agent in the brief pending → running window doesn't get its process
	// killed by the running-set diff. Drivers persist through pending,
	// running, and paused; they're torn down only when the hub considers
	// the agent gone (terminal status or no longer assigned to this host).
	hostStatus := make(map[string]string, len(agents))
	for _, ag := range agents {
		hostStatus[ag.ID] = ag.Status
	}
	for _, id := range a.driverIDsSnapshot() {
		st, present := hostStatus[id]
		if !present || st == "terminated" || st == "failed" || st == "crashed" || st == "stale" {
			a.stopDriver(id)
		}
	}
}
