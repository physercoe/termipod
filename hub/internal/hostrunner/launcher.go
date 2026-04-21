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
