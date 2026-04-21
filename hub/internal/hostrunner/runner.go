package hostrunner

import (
	"context"
	"log/slog"
	"time"
)

// Runner is the long-running host-runner loop. It registers the host with the
// hub (first boot), heartbeats every HeartbeatInterval, and polls every
// PollInterval for pending spawns on this host. For each spawn it calls
// Launcher.Launch, then PATCHes the child agent to status=running with the
// tmux pane id.
//
// The hub is the source of truth: host-runner carries no local state across
// restarts, it just re-derives the world from /v1/teams/.../agents/spawns.
type Runner struct {
	Client   *Client
	HostName string
	HostID   string // filled in on Start() if empty
	Launcher Launcher
	Log      *slog.Logger

	HeartbeatInterval time.Duration
	PollInterval      time.Duration
	IdleThreshold     time.Duration // 0 = use default from NewIdleDetector

	idle      *IdleDetector
	panes     map[string]paneState    // keyed by agent id
	tailers   map[string]*Tailer      // keyed by agent id
	worktrees map[string]WorktreeSpec // keyed by agent id
}

func (a *Runner) defaults() {
	if a.HeartbeatInterval == 0 {
		a.HeartbeatInterval = 10 * time.Second
	}
	if a.PollInterval == 0 {
		a.PollInterval = 3 * time.Second
	}
	if a.Log == nil {
		a.Log = slog.Default()
	}
	if a.Launcher == nil {
		a.Launcher = StubLauncher{Log: a.Log}
	}
	if a.idle == nil {
		a.idle = NewIdleDetector(a.IdleThreshold)
	}
	if a.panes == nil {
		a.panes = map[string]paneState{}
	}
	if a.tailers == nil {
		a.tailers = map[string]*Tailer{}
	}
	if a.worktrees == nil {
		a.worktrees = map[string]WorktreeSpec{}
	}
}

// Start registers the host (if HostID is empty) and runs until ctx is done.
func (a *Runner) Start(ctx context.Context) error {
	a.defaults()

	if a.HostID == "" {
		id, err := a.Client.RegisterHost(ctx, a.HostName, nil)
		if err != nil {
			return err
		}
		a.HostID = id
		a.Log.Info("host registered", "host_id", id, "name", a.HostName)
	}

	hb := time.NewTicker(a.HeartbeatInterval)
	defer hb.Stop()
	poll := time.NewTicker(a.PollInterval)
	defer poll.Stop()

	// Kick off an immediate poll so bootstrap isn't delayed by the first tick.
	a.tickPoll(ctx)
	a.tickCommands(ctx)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-hb.C:
			if err := a.Client.Heartbeat(ctx, a.HostID); err != nil {
				a.Log.Warn("heartbeat failed", "err", err)
			}
		case <-poll.C:
			a.tickPoll(ctx)
			a.tickCommands(ctx)
			a.tickReconcile(ctx)
			a.tickIdle(ctx)
		}
	}
}

// tickIdle captures each running pane, runs the idle detector, and raises
// an attention item per stuck pane (deduped by hash inside Inspect).
// Errors on a single pane are logged and skipped — one bad pane shouldn't
// prevent us from watching the others.
func (a *Runner) tickIdle(ctx context.Context) {
	agents, err := a.Client.ListRunningAgents(ctx, a.HostID)
	if err != nil {
		return
	}
	now := time.Now()
	seen := map[string]struct{}{}
	for _, ag := range agents {
		seen[ag.ID] = struct{}{}
		if ag.PaneID == "" || ag.PauseState == "paused" {
			continue
		}
		text, err := runTmux(ctx, "capture-pane", "-p", "-J", "-t", ag.PaneID)
		if err != nil {
			a.Log.Debug("capture for idle failed", "agent", ag.ID, "err", err)
			continue
		}
		prev := a.panes[ag.ID]
		next, raise := a.idle.Inspect(text, prev, now)
		a.panes[ag.ID] = next
		if raise {
			tail := tailLines(text, 5)
			_ = a.Client.PostAttention(ctx, AttentionIn{
				ScopeKind: "team",
				Kind:      "idle",
				Summary:   "agent idle at prompt: " + firstLine(tail),
				Severity:  "minor",
			})
			a.Log.Info("idle attention raised", "agent", ag.ID, "handle", ag.Handle)
		}
	}
	// Drop state for panes that no longer report running — on respawn the hash
	// would spuriously match the stale entry and suppress a legit idle alert.
	for id := range a.panes {
		if _, ok := seen[id]; !ok {
			delete(a.panes, id)
		}
	}
	// Same for tailers: once an agent is no longer in the running set, its
	// FIFO + pipe-pane should be torn down so we don't leak fds / tmp files.
	for id := range a.tailers {
		if _, ok := seen[id]; !ok {
			a.stopTailer(id)
		}
	}
	// Drop worktree bookkeeping for agents the hub no longer considers
	// running. Anything the terminate handler missed (e.g. host-runner
	// killed before cleanup) will be re-synced by `git worktree prune`
	// on the next terminate for that repo.
	for id := range a.worktrees {
		if _, ok := seen[id]; !ok {
			delete(a.worktrees, id)
		}
	}
}

func (a *Runner) tickCommands(ctx context.Context) {
	cmds, err := a.Client.ListPendingCommands(ctx, a.HostID)
	if err != nil {
		a.Log.Warn("list commands failed", "err", err)
		return
	}
	for _, c := range cmds {
		a.runCommand(ctx, c)
	}
}

func (a *Runner) tickPoll(ctx context.Context) {
	spawns, err := a.Client.ListPendingSpawns(ctx, a.HostID)
	if err != nil {
		a.Log.Warn("list pending failed", "err", err)
		return
	}
	for _, sp := range spawns {
		a.launchOne(ctx, sp)
	}
}

func (a *Runner) launchOne(ctx context.Context, sp Spawn) {
	// Parse the spec up front; ChannelID binding (tailer) and Worktree are
	// both optional and we want to reason about them together.
	spec, err := ParseSpec(sp.SpawnSpec)
	if err != nil {
		a.Log.Warn("parse spawn spec failed", "handle", sp.Handle, "err", err)
		// Fall through — a bad YAML shouldn't block the pane itself; the
		// spec-derived features (markers, worktree) just won't be wired.
	}

	// Ensure the worktree exists before we launch — the backend process
	// will almost certainly cd into it, so having it ready avoids a race
	// where the first command runs in a non-existent dir.
	if sp.WorktreePath != "" && spec.Worktree.Repo != "" {
		wt := WorktreeSpec{
			Repo:   spec.Worktree.Repo,
			Path:   sp.WorktreePath,
			Branch: spec.Worktree.Branch,
			Base:   spec.Worktree.Base,
		}
		created, werr := EnsureWorktree(ctx, wt)
		if werr != nil {
			a.Log.Warn("worktree ensure failed",
				"handle", sp.Handle, "path", wt.Path, "err", werr)
			// Continue: the pane still launches; the operator can retry
			// by hand. Aborting here would leave the agent in pending
			// forever, which is worse than "missing worktree".
		} else if created {
			a.Log.Info("worktree created",
				"handle", sp.Handle, "path", wt.Path, "branch", wt.Branch)
		}
		a.worktrees[sp.ChildID] = wt
	}

	pane, err := a.Launcher.Launch(ctx, sp)
	if err != nil {
		a.Log.Error("launch failed", "handle", sp.Handle, "err", err)
		status := "failed"
		_ = a.Client.PatchAgent(ctx, sp.ChildID, AgentPatch{Status: &status})
		return
	}
	// Record the pane id but leave status='pending'. The reconcile tick
	// owns the pending → running transition: it flips only once tmux
	// reports a non-shell foreground command, so a pane whose CLI failed
	// to start is correctly distinguished from a working one.
	if err := a.Client.PatchAgent(ctx, sp.ChildID, AgentPatch{
		PaneID: &pane,
	}); err != nil {
		a.Log.Error("patch agent failed", "handle", sp.Handle, "err", err)
		return
	}
	a.Log.Info("agent pane created", "handle", sp.Handle, "pane", pane)

	// Best-effort: bring up a marker tailer if the spawn spec bound the
	// agent to a project/channel. Missing IDs leave the pane visible in
	// tmux without event forwarding — the backend can still speak MCP
	// directly if that's configured elsewhere. Spec was already parsed at
	// the top of launchOne; reuse it.
	if spec.ChannelID == "" || spec.ProjectID == "" {
		return
	}
	t := &Tailer{
		AgentID:   sp.ChildID,
		PaneID:    pane,
		ProjectID: spec.ProjectID,
		ChannelID: spec.ChannelID,
		Client:    a.Client,
		Log:       a.Log,
	}
	if err := t.Start(ctx); err != nil {
		a.Log.Warn("tailer start failed", "agent", sp.ChildID, "err", err)
		return
	}
	a.tailers[sp.ChildID] = t
}

// stopTailer tears down marker forwarding for an agent that has left the
// running set (terminated, stale, or reassigned to another host). Callers
// must hold no lock — Stop is idempotent.
func (a *Runner) stopTailer(agentID string) {
	t, ok := a.tailers[agentID]
	if !ok {
		return
	}
	t.Stop()
	delete(a.tailers, agentID)
}
