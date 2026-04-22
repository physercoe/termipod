package hostrunner

import (
	"context"
	"fmt"
	"log/slog"
)

// Launcher is the backend-specific strategy for turning a Spawn into a
// running tmux pane. Real implementations wrap tmux new-session + send-keys
// into the chosen CLI (claude-code, codex, …). StubLauncher is a no-op
// placeholder used in tests and bootstrap demos: it just returns a synthetic
// pane id so the spawn loop can complete end-to-end without a TTY.
type Launcher interface {
	// Launch returns the tmux pane target (e.g. "hub-agents:@worker-1.0") where
	// the backend process is now running. The host-runner PATCHes this back to
	// the hub so the mobile client's `↗ pane` link works.
	Launch(ctx context.Context, sp Spawn) (paneID string, err error)

	// LaunchCmd launches an arbitrary command in a pane instead of the
	// launcher's default backend. Used by structured modes (M2/M1) to
	// attach a read-only `tail -f <log>` pane while host-runner owns the
	// real agent process outside tmux.
	LaunchCmd(ctx context.Context, sp Spawn, cmd string) (paneID string, err error)
}

type StubLauncher struct {
	Log *slog.Logger
}

func (s StubLauncher) Launch(_ context.Context, sp Spawn) (string, error) {
	pane := fmt.Sprintf("hub-agents:%s.0", sp.Handle)
	if s.Log != nil {
		s.Log.Info("stub-launch", "handle", sp.Handle, "kind", sp.Kind, "pane", pane)
	}
	return pane, nil
}

func (s StubLauncher) LaunchCmd(_ context.Context, sp Spawn, cmd string) (string, error) {
	pane := fmt.Sprintf("hub-agents:%s.0", sp.Handle)
	if s.Log != nil {
		s.Log.Info("stub-launch-cmd", "handle", sp.Handle, "cmd", cmd, "pane", pane)
	}
	return pane, nil
}
