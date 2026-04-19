package hostagent

import (
	"context"
	"log/slog"
	"time"
)

// Agent is the long-running host-agent loop. It registers the host with the
// hub (first boot), heartbeats every HeartbeatInterval, and polls every
// PollInterval for pending spawns on this host. For each spawn it calls
// Launcher.Launch, then PATCHes the child agent to status=running with the
// tmux pane id.
//
// The hub is the source of truth: host-agent carries no local state across
// restarts, it just re-derives the world from /v1/teams/.../agents/spawns.
type Agent struct {
	Client   *Client
	HostName string
	HostID   string // filled in on Start() if empty
	Launcher Launcher
	Log      *slog.Logger

	HeartbeatInterval time.Duration
	PollInterval      time.Duration
}

func (a *Agent) defaults() {
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
}

// Start registers the host (if HostID is empty) and runs until ctx is done.
func (a *Agent) Start(ctx context.Context) error {
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
		}
	}
}

func (a *Agent) tickPoll(ctx context.Context) {
	spawns, err := a.Client.ListPendingSpawns(ctx, a.HostID)
	if err != nil {
		a.Log.Warn("list pending failed", "err", err)
		return
	}
	for _, sp := range spawns {
		a.launchOne(ctx, sp)
	}
}

func (a *Agent) launchOne(ctx context.Context, sp Spawn) {
	pane, err := a.Launcher.Launch(ctx, sp)
	if err != nil {
		a.Log.Error("launch failed", "handle", sp.Handle, "err", err)
		status := "failed"
		_ = a.Client.PatchAgent(ctx, sp.ChildID, AgentPatch{Status: &status})
		return
	}
	status := "running"
	if err := a.Client.PatchAgent(ctx, sp.ChildID, AgentPatch{
		Status: &status,
		PaneID: &pane,
	}); err != nil {
		a.Log.Error("patch agent failed", "handle", sp.Handle, "err", err)
		return
	}
	a.Log.Info("agent running", "handle", sp.Handle, "pane", pane)
}
