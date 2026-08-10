package hostrunner

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/termipod/hub/internal/buildinfo"
	"github.com/termipod/hub/internal/hostrunner/a2a"
	"github.com/termipod/hub/internal/selfupdate"
)

// verbExit and verbExitDelay are package-level seams so tests can drive
// the verb handlers that terminate the process (host.shutdown,
// host.update) without killing the test process or waiting 200ms per
// assertion.
var (
	verbExit      = os.Exit
	verbExitDelay = 200 * time.Millisecond
)

// runSelfUpdate is a seam over selfupdate.Run so the host.update verb
// is testable without reaching GitHub.
var runSelfUpdate = selfupdate.Run

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
		return r.handleHostExit(env, 0, "host.shutdown")
	case "restart":
		return r.handleHostExit(env, 75, "host.restart")
	case "update":
		return r.handleHostUpdate(ctx, env)
	case "ping":
		return r.handleHostPing(env)
	case "token_rotate":
		return r.handleHostTokenRotate(env)
	case "dataset_digest":
		return r.handleHostDatasetDigest(env)
	case "dataset_episodes":
		return r.handleHostDatasetEpisodes(env)
	case "dataset_series":
		return r.handleHostDatasetSeries(env)
	case "pane_explain":
		return r.handleHostPaneExplain(ctx, env)
	default:
		// Unknown verb. Returning nil makes the tunnel loop emit the
		// canonical unknown_verb envelope with host_version stamped.
		return nil
	}
}

// hostExitPayload is the verb-args schema shared by host.shutdown and
// host.restart (ADR-028 D-1 / D-2). MVP keeps it minimal — the
// operator's intent is recorded in reason, and force_kill is
// informational at the verb level: hub-side W2.5 already propagated
// SIGKILL through stopSessionInternal before the verb arrives. We log
// it here for journald correlation.
type hostExitPayload struct {
	Reason    string `json:"reason,omitempty"`
	ForceKill bool   `json:"force_kill,omitempty"`
}

// handleHostExit is the shared body of host.shutdown and host.restart:
// log the reason, tear down any drivers still registered (defensive —
// hub-side session stops already fired before the verb landed), ack
// via the tunnel response, and schedule a process exit on a delayed
// goroutine so the response posts first.
//
// The exit code is the only difference between the two verbs, and it
// encodes operator intent to systemd (ADR-028 D-2): 0 = true shutdown
// (Restart=on-failure does NOT respawn), 75 = bounce (systemd respawns
// with whatever binary is at the install path).
func (r *Runner) handleHostExit(env *a2a.TunnelEnvelope, exitCode int, verb string) *a2a.TunnelResponseEnvelope {
	var p hostExitPayload
	if len(env.Payload) > 0 {
		_ = json.Unmarshal(env.Payload, &p)
	}
	r.Log.Info(verb+" received", "reason", p.Reason, "force_kill", p.ForceKill)

	// Cleanup pass over any drivers still registered. Hub-side
	// orchestration terminates each agent's driver through the existing
	// host-command path before firing this verb, so in steady state
	// this loop is a no-op — but if a stop command was racing or a
	// driver outlived its agent record, this catches the stragglers.
	agentIDs := r.driverIDsSnapshot()
	for _, id := range agentIDs {
		r.stopDriver(id)
	}
	if len(agentIDs) > 0 {
		r.Log.Info(verb+" cleanup pass", "stragglers_stopped", len(agentIDs))
	}

	// Schedule the exit so the response posts first. 200ms is
	// comfortably longer than the local tunnel round-trip (~ms) but
	// short enough that the operator sees the host react promptly.
	go func() {
		time.Sleep(verbExitDelay)
		r.Log.Info(verb+" exiting", "code", exitCode, "reason", p.Reason)
		verbExit(exitCode)
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

// handleHostPing runs the host.ping verb (ADR-028 plan W14/W15): a
// read-side liveness + version probe. It writes nothing and never
// exits — it just reflects this host-runner's build identity back
// through the tunnel so `hub-server version --remote` and
// `hub-server hosts ls/ping` can report what each host is running.
func (r *Runner) handleHostPing(env *a2a.TunnelEnvelope) *a2a.TunnelResponseEnvelope {
	body, _ := json.Marshal(map[string]any{
		"ok":         true,
		"version":    buildinfo.Version,
		"commit":     buildinfo.Commit,
		"build_time": buildinfo.BuildTime,
		"modified":   buildinfo.Modified,
		"ts":         time.Now().UTC().Format(time.RFC3339),
	})
	return &a2a.TunnelResponseEnvelope{
		ReqID:   env.ReqID,
		Status:  http.StatusOK,
		Headers: map[string]string{"Content-Type": "application/json"},
		BodyB64: base64.StdEncoding.EncodeToString(body),
	}
}

// hostTokenRotatePayload is the verb-args schema for host.token_rotate
// (ADR-028 plan W20). Token is the new hub bearer the host should
// adopt.
type hostTokenRotatePayload struct {
	Token  string `json:"token"`
	Reason string `json:"reason,omitempty"`
}

// handleHostTokenRotate runs the host.token_rotate verb: persist the
// new bearer to the state dir, then swap it into the live Client so the
// next hub call uses it — no restart needed.
//
// Order is load-bearing for brick-safety (ADR-028 W20):
//
//  1. If there is no state dir the token cannot be persisted, so a
//     restart would re-auth with the old (about-to-be-revoked) token.
//     Refuse with 500 — the hub gets no success ack and won't revoke.
//  2. Persist BEFORE swapping. A failed write leaves the host on the
//     old token, still authenticated; the hub (no ack) won't revoke it.
//  3. Swap the live bearer, THEN ack. The ack POST itself then travels
//     on the new token, so by the time the hub revokes the old one the
//     host has demonstrably adopted the new one.
func (r *Runner) handleHostTokenRotate(env *a2a.TunnelEnvelope) *a2a.TunnelResponseEnvelope {
	var p hostTokenRotatePayload
	if len(env.Payload) > 0 {
		_ = json.Unmarshal(env.Payload, &p)
	}
	fail := func(status int, msg string) *a2a.TunnelResponseEnvelope {
		r.Log.Error("host.token_rotate refused; staying on the current token", "err", msg)
		body, _ := json.Marshal(map[string]any{"acked": true, "ok": false, "error": msg})
		return &a2a.TunnelResponseEnvelope{
			ReqID:   env.ReqID,
			Status:  status,
			Headers: map[string]string{"Content-Type": "application/json"},
			BodyB64: base64.StdEncoding.EncodeToString(body),
		}
	}
	if p.Token == "" {
		return fail(http.StatusBadRequest, "empty token")
	}
	if r.StateDir == "" {
		return fail(http.StatusInternalServerError,
			"no state dir configured — a rotated token could not survive a restart")
	}
	r.Log.Info("host.token_rotate received", "reason", p.Reason)
	if err := saveStateToken(r.StateDir, r.Client.BaseURL, r.Client.Team,
		r.HostName, p.Token); err != nil {
		return fail(http.StatusInternalServerError, "persist token: "+err.Error())
	}
	r.Client.SetToken(p.Token)
	r.Log.Info("host.token_rotate: token swapped and persisted")

	body, _ := json.Marshal(map[string]any{"acked": true, "ok": true})
	return &a2a.TunnelResponseEnvelope{
		ReqID:   env.ReqID,
		Status:  http.StatusOK,
		Headers: map[string]string{"Content-Type": "application/json"},
		BodyB64: base64.StdEncoding.EncodeToString(body),
	}
}

// hostUpdatePayload is the verb-args schema for host.update (ADR-028
// D-2 / plan W8). Every field is optional: an empty Version falls back
// to Channel, an empty Channel resolves to the selfupdate default
// (stable — note the hub's admin endpoints normalize their own empty
// channel to alpha before the verb fires, so only a hand-crafted verb
// ever lands here channel-less), an empty UpstreamRepo resolves to
// selfupdate.DefaultRepo.
type hostUpdatePayload struct {
	Version      string `json:"version,omitempty"`
	Channel      string `json:"channel,omitempty"`
	UpstreamRepo string `json:"upstream_repo,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

// handleHostUpdate runs the host.update verb in two stages (ADR-028
// D-2 / plan W8, amended for the progress endpoint):
//
//  1. Resolve — synchronous, network-light (a dry-run self-update: two
//     small JSON GETs, no download). The ack therefore still carries
//     the ops-relevant outcome the original synchronous design wanted:
//     the release exists, it has this host's asset, and the from/to
//     versions. A resolve failure returns 500 and the host stays up.
//  2. Download + verify + install — background goroutine with progress
//     posts to the hub's per-host progress slot. The tarball download
//     has no whole-body timeout (slow links are an explicit design
//     case), so it can legitimately outlive any ack window; making the
//     verb block for it only produced false "verb: context deadline
//     exceeded" failures hub-side while the host quietly succeeded.
//
// On install success the goroutine exits 75 so systemd respawns with
// the new binary; on failure it posts an error sample and the host
// keeps running the OLD binary (the bytes on disk are untouched).
func (r *Runner) handleHostUpdate(ctx context.Context, env *a2a.TunnelEnvelope) *a2a.TunnelResponseEnvelope {
	var p hostUpdatePayload
	if len(env.Payload) > 0 {
		_ = json.Unmarshal(env.Payload, &p)
	}
	r.Log.Info("host.update received",
		"version", p.Version, "channel", p.Channel,
		"upstream_repo", p.UpstreamRepo, "reason", p.Reason)

	res, err := runSelfUpdate(ctx, selfupdate.Options{
		Binary:  "host-runner",
		Repo:    p.UpstreamRepo,
		Channel: p.Channel,
		Version: p.Version,
		DryRun:  true, // resolve-only: the real run happens in finishHostUpdate
		Log:     r.Log,
	})
	if err != nil {
		r.Log.Error("host.update resolve failed; staying on the current binary", "err", err)
		body, _ := json.Marshal(map[string]any{
			"acked": true,
			"ok":    false,
			"error": err.Error(),
		})
		return &a2a.TunnelResponseEnvelope{
			ReqID:   env.ReqID,
			Status:  http.StatusInternalServerError,
			Headers: map[string]string{"Content-Type": "application/json"},
			BodyB64: base64.StdEncoding.EncodeToString(body),
		}
	}

	// Resolved: ack now, download in the background with progress.
	go r.finishHostUpdate(p, res)

	body, _ := json.Marshal(map[string]any{
		"acked":        true,
		"ok":           true,
		"from_version": res.FromVersion,
		"to_version":   res.ToVersion,
		"asset":        res.Asset,
		"started":      true,
	})
	return &a2a.TunnelResponseEnvelope{
		ReqID:   env.ReqID,
		Status:  http.StatusOK,
		Headers: map[string]string{"Content-Type": "application/json"},
		BodyB64: base64.StdEncoding.EncodeToString(body),
	}
}

// finishHostUpdate is stage 2 of host.update: download + SHA256-verify
// + install the resolved release, posting progress samples the hub
// relays to the operator's admin pane. Runs detached from the verb
// handler's ctx — that ctx dies when the ack posts, but the download
// must not — with its own generous cap for a genuinely wedged link.
func (r *Runner) finishHostUpdate(p hostUpdatePayload, res *selfupdate.Result) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	report := r.updateProgressReporter(ctx, res.ToVersion)
	_, err := runSelfUpdate(ctx, selfupdate.Options{
		Binary:     "host-runner",
		Repo:       p.UpstreamRepo,
		Channel:    p.Channel,
		Version:    p.Version,
		OnProgress: report,
		Log:        r.Log,
	})
	if err != nil {
		r.Log.Error("host.update failed; staying on the current binary", "err", err)
		r.postUpdateProgress(ctx, UpdateProgressIn{
			Phase: "error", ToVersion: res.ToVersion, Error: err.Error(),
		})
		return
	}
	r.postUpdateProgress(ctx, UpdateProgressIn{Phase: "done", ToVersion: res.ToVersion})

	// The new binary is on disk. Brief delay so the done sample posts
	// first, then systemd respawns with the freshly written binary.
	time.Sleep(verbExitDelay)
	r.Log.Info("host.update exiting for respawn", "code", 75, "to", res.ToVersion)
	verbExit(75)
}

// updateProgressReporter adapts selfupdate's OnProgress to throttled
// hub posts: byte samples at most once a second (a fast link would
// otherwise POST per 256KiB step), phase transitions always.
func (r *Runner) updateProgressReporter(ctx context.Context, toVersion string) func(selfupdate.Progress) {
	var mu sync.Mutex
	lastPost := time.Time{}
	lastPhase := ""
	return func(pr selfupdate.Progress) {
		mu.Lock()
		now := time.Now()
		if pr.Phase == lastPhase && now.Sub(lastPost) < time.Second {
			mu.Unlock()
			return
		}
		lastPhase, lastPost = pr.Phase, now
		mu.Unlock()
		r.postUpdateProgress(ctx, UpdateProgressIn{
			Phase: pr.Phase, Done: pr.Done, Total: pr.Total, ToVersion: toVersion,
		})
	}
}

// postUpdateProgress ships one sample to the hub, best-effort: a host
// without a hub client (unit tests) or a hub that is briefly unreachable
// must not fail the update over a progress bar.
func (r *Runner) postUpdateProgress(ctx context.Context, in UpdateProgressIn) {
	if r.Client == nil || r.HostID == "" {
		return
	}
	if err := r.Client.PostUpdateProgress(ctx, r.HostID, in); err != nil {
		r.Log.Warn("host.update progress post failed (continuing)", "err", err)
	}
}
