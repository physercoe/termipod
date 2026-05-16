package hostrunner

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/termipod/hub/internal/hostrunner/a2a"
)

// shutdownExit and shutdownExitDelay are package-level seams so tests
// can drive the host.shutdown path without terminating the test process
// or waiting 200ms per assertion.
var (
	shutdownExit      = os.Exit
	shutdownExitDelay = 200 * time.Millisecond
)

// handleHostVerb is the host-runner's control-plane dispatcher
// (ADR-028 D-1). The tunnel loop in a2a/tunnel.go routes any envelope
// whose Kind starts with "host." through this method.
//
// Returning nil tells the loop to emit the typed unknown_verb response.
// Verb-specific cases are added one wedge at a time (W2 host.shutdown,
// W8 host.update, W11 host.restart, …).
func (r *Runner) handleHostVerb(ctx context.Context, env *a2a.TunnelEnvelope) *a2a.TunnelResponseEnvelope {
	verb := strings.TrimPrefix(env.Kind, "host.")
	switch verb {
	case "shutdown":
		return r.handleHostShutdown(ctx, env)
	default:
		// Unknown verb. Returning nil makes the tunnel loop emit the
		// canonical unknown_verb envelope with host_version stamped.
		return nil
	}
}

// hostShutdownPayload is the verb-args schema for host.shutdown
// (ADR-028 D-1 / D-2). MVP keeps the payload minimal — the operator's
// intent ("update" vs "manual stop") is recorded in reason, and
// force_kill is informational at the verb level: hub-side W2.5 already
// propagated SIGKILL through stopSessionInternal before this verb
// arrives. We log it here for journald correlation.
type hostShutdownPayload struct {
	Reason    string `json:"reason,omitempty"`
	ForceKill bool   `json:"force_kill,omitempty"`
}

// handleHostShutdown runs the per-ADR-028 host.shutdown verb: log the
// reason, tear down any drivers still registered (defensive — hub-side
// session stops already fired before this verb landed), ack via the
// tunnel response, and exit 0 so systemd's Restart=on-failure leaves
// us DOWN.
//
// The os.Exit fires on a delayed goroutine so the response envelope
// has a chance to post back to the hub before the process disappears.
func (r *Runner) handleHostShutdown(ctx context.Context, env *a2a.TunnelEnvelope) *a2a.TunnelResponseEnvelope {
	_ = ctx
	var p hostShutdownPayload
	if len(env.Payload) > 0 {
		_ = json.Unmarshal(env.Payload, &p)
	}
	r.Log.Info("host.shutdown received",
		"reason", p.Reason, "force_kill", p.ForceKill)

	// Cleanup pass over any drivers still registered. Hub-side W3
	// terminates each agent's driver through the existing host-command
	// path before firing this verb, so in steady state this loop is a
	// no-op — but if a stop command was racing or a driver outlived its
	// agent record, this catches the stragglers.
	agentIDs := make([]string, 0, len(r.drivers))
	for id := range r.drivers {
		agentIDs = append(agentIDs, id)
	}
	for _, id := range agentIDs {
		r.stopDriver(id)
	}
	if len(agentIDs) > 0 {
		r.Log.Info("host.shutdown cleanup pass",
			"stragglers_stopped", len(agentIDs))
	}

	// Schedule the exit so the response posts first. 200ms is comfortably
	// longer than the local tunnel round-trip (~ms) but short enough that
	// the operator sees the host go down promptly.
	go func() {
		time.Sleep(shutdownExitDelay)
		r.Log.Info("host.shutdown exiting",
			"code", 0, "reason", p.Reason)
		shutdownExit(0)
	}()

	body, _ := json.Marshal(map[string]any{
		"acked":              true,
		"stragglers_stopped": len(agentIDs),
		"reason":             p.Reason,
	})
	return &a2a.TunnelResponseEnvelope{
		ReqID:   env.ReqID,
		Status:  http.StatusOK,
		Headers: map[string]string{"Content-Type": "application/json"},
		BodyB64: base64.StdEncoding.EncodeToString(body),
	}
}
