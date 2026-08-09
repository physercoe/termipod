// Declarative pane-state classification, wired into the host-runner's poll
// tick (docs/plans/pane-state-manifests.md lane P, wedge P2).
//
// `internal/panestate` (P1) is a pure library: screen in, classification out.
// This file is everything around it — which panes are eligible, where the
// screen and the OSC title come from, the debounce/hysteresis/startup-grace
// state machine, and the agent event a transition becomes.
//
// Where it runs, and why not in PaneDriver
// ----------------------------------------
// The plan's P2 line says "feed the evaluator from `PaneDriver`'s tick". It
// lives in the runner's pane tick instead, because three of the plan's own
// decisions cannot be satisfied from inside a driver:
//
//   - D-3 needs the agent's family. PaneDriver only knows an agent id and a
//     pane id; the family lives on the hub's agent row.
//   - D-4 wants ONE `list-panes -F` round-trip covering all panes for the
//     OSC title. A per-driver call is one round-trip per agent.
//   - D-2's ported exception (a visible blocker on a pane whose adapter DOES
//     author state may still raise attention, P3) is about panes PaneDriver
//     does not own and cannot see.
//
// The runner tick already walks every running pane for the IdleDetector
// that P3 retires, so this adds no new enumeration — and the two are
// disjoint by construction (see paneStateWatch.tick).
package hostrunner

import (
	"context"
	"log/slog"
	"time"

	"github.com/termipod/hub/internal/panestate"
)

// PaneStateEventKind is the agent-event kind carrying one classification.
//
// The plan (D-6) calls `panestate` a *producer*. It cannot be one: the event
// ingest endpoint accepts a closed set and rejects anything else outright
// (`internal/server/handlers_agent_events.go:95`, "producer must be
// agent|user|system"). The producer axis answers "whose bytes are these", and
// these bytes are host-runner's own — the same reason PaneDriver stamps its
// lifecycle events producer=system (driver_pane.go:10-15). So the
// classification is a KIND, emitted by the system producer.
//
// (The `agent_events.producer` column can also hold `a2a`, but only via the
// separate agent-INPUT endpoint, which has its own closed vocabulary —
// `user|a2a`, handlers_agent_input.go:438-445. Neither route has an
// extension point for a per-subsystem producer name.)
const PaneStateEventKind = "pane_state"

const paneStateEventProducer = "system"

// D-5 constants, read from herdr `src/pane/agent_detection.rs` at the same
// commit the manifests are vendored from (6f311498):
//
//	AGENT_PENDING_IDLE_RECHECK       100ms  — their poll cadence
//	AGENT_PENDING_IDLE_CONFIRMATIONS 3
//	AGENT_PENDING_IDLE_CAP           700ms
//	AGENT_STARTUP_GRACE_WINDOW       3s
//	STABLE_VISIBLE_SIGNAL_REFRESH    800ms  — deliberately NOT ported
//
// The cap and the confirmation count are two ends of the same hold, and which
// one fires depends on the caller's cadence. Upstream polls at 100 ms, so the
// three confirmations land first (~300 ms) and the cap is the safety net. We
// poll at Runner.PollInterval (3 s), so the CAP always fires first: the hold
// costs exactly one tick. Both are ported anyway — the constants are the
// contract, and a future faster tick must not silently change the semantics.
//
// The 800 ms re-publish is D-5's one explicit non-port: it exists so a
// blocked pane keeps nudging upstream's own UI, and the hub's attention model
// already owns re-delivery. We publish a blocked classification once per
// streak (P3 raises attention from it).
const (
	paneStateIdleConfirmations = 3
	paneStateIdleCap           = 700 * time.Millisecond
	paneStateStartupGrace      = 3 * time.Second
)

// paneStatePublish is the tuple whose change constitutes a transition.
//
// Upstream publishes on a change to the state OR to any of the three
// `visible_*` hints (`should_publish_detection_update`), not on the state
// alone: "blocked, and the dialog is on screen" is a different claim from
// "blocked, inferred" even though both say blocked. Comparable on purpose —
// `next != prev` IS the four-field diff.
type paneStatePublish struct {
	state          panestate.State
	visibleIdle    bool
	visibleBlocker bool
	visibleWorking bool
}

// pendingIdleHold is the asymmetric hysteresis: working → *plain* idle waits
// for confirmation, everything else publishes immediately.
//
// The asymmetry is the point. A working spinner that blinks off for one frame
// would otherwise flap the state; but a screen that positively shows idle
// chrome (a drawn prompt box) is evidence, not an absence of evidence, so it
// bypasses the hold. Blocked never waits — that is the one state a human
// needs to see immediately.
type pendingIdleHold struct {
	startedAt     time.Time
	confirmations int
}

func (p *pendingIdleHold) clear() { *p = pendingIdleHold{} }

func (p *pendingIdleHold) active() bool { return !p.startedAt.IsZero() }

// hold ports `PendingIdleConfirmation::should_hold_working_to_idle`.
//
// Note the shape: the FIRST plain-idle observation starts the timer and holds
// with zero confirmations, so the transition needs three further observations
// (upstream's own test asserts holds at t, +100ms, +200ms and a release at
// +300ms). The cap RELEASES the hold rather than extending it — past 700 ms
// the transition goes through however few confirmations arrived.
//
// `agentChanged` and `processExited` are upstream's two bypasses. Both are
// structurally false here and are passed as such by the only caller: the
// agent identity of a pane is fixed by the hub's agent row for its lifetime
// (a respawn mints a new agent id, not a new identity for this one), and
// process exit is tickReconcile's job, not this tick's. They stay in the
// signature so a re-vendor can diff this function against upstream's.
func (p *pendingIdleHold) hold(prev, next paneStatePublish, agentChanged, processExited bool, now time.Time) bool {
	plainIdle := prev.state == panestate.StateWorking &&
		next.state == panestate.StateIdle &&
		!next.visibleIdle &&
		!next.visibleBlocker &&
		!agentChanged &&
		!processExited
	if !plainIdle {
		p.clear()
		return false
	}
	if !p.active() {
		p.startedAt = now
		p.confirmations = 0
		return true
	}
	if now.Sub(p.startedAt) >= paneStateIdleCap {
		p.clear()
		return false
	}
	p.confirmations++
	if p.confirmations >= paneStateIdleConfirmations {
		p.clear()
		return false
	}
	return true
}

// paneStateEntry is one agent's classification state across ticks.
type paneStateEntry struct {
	manifestID string
	published  paneStatePublish
	graceUntil time.Time
	pending    pendingIdleHold
}

// identify runs on the first tick an agent becomes eligible. Upstream
// publishes an immediate idle baseline and then suppresses detection for
// AGENT_STARTUP_GRACE_WINDOW — the braille-splash trap: an engine's startup
// banner animates, and animation reads as "working" before the engine has
// even finished booting.
//
// One deviation: upstream stores `last_visible_idle = true` here while
// publishing `visible_idle: false`. We store what we published. Storing a
// hint we did not publish means the first real classification differs from
// the stored baseline on a field nobody was ever told about, and emits a
// redundant idle event for it.
func (e *paneStateEntry) identify(now time.Time) paneStatePublish {
	e.graceUntil = now.Add(paneStateStartupGrace)
	e.pending.clear()
	e.published = paneStatePublish{state: panestate.StateIdle}
	return e.published
}

func (e *paneStateEntry) inGrace(now time.Time) bool {
	return !e.graceUntil.IsZero() && now.Before(e.graceUntil)
}

// step advances one agent by one observation, reporting what to publish.
//
// A `skip_state_update` rule freezes: upstream drops the observation before
// the state machine ever sees it (`detection_update_for_publish_with_osc`
// returns None), so the hold timer neither advances nor resets. A transcript
// viewer opened mid-hold must not cancel the hold OR count towards it — the
// underlying state was not observed at all.
func (e *paneStateEntry) step(ex panestate.Explain, now time.Time) (paneStatePublish, bool) {
	if ex.SkipStateUpdate {
		return e.published, false
	}
	next := paneStatePublish{
		state:          ex.State,
		visibleIdle:    ex.VisibleIdle,
		visibleBlocker: ex.VisibleBlocker,
		visibleWorking: ex.VisibleWorking,
	}
	if e.pending.hold(e.published, next, false, false, now) {
		return e.published, false
	}
	if next == e.published {
		return e.published, false
	}
	e.published = next
	return next, true
}

// paneStateWatch owns the per-agent entries and the tmux seams.
//
// capture / titles / now are injected so the tests never reach a real tmux
// server; the zero value of each resolves to the real thing in newPaneStateWatch.
// The poster is NOT a field: Start swaps Runner.agentPoster for the A2A tap
// after defaults() has already run, so a watcher that captured one at
// construction would keep posting into the untapped client for the life of
// the process. It arrives per tick instead.
type paneStateWatch struct {
	reg     *panestate.Registry
	log     *slog.Logger
	capture PaneCaptureFunc
	titles  func(ctx context.Context) (map[string]string, error)
	now     func() time.Time
	entries map[string]*paneStateEntry
}

// newPaneStateWatch builds the watcher, or returns nil when the embedded
// manifests will not load.
//
// nil disables classification rather than failing the host-runner: the
// embedded set is validated by internal/panestate's own tests, so a load
// error here means a corrupt binary, and a host-runner that refuses to
// supervise its agents over a detection feature is a worse outcome than one
// that supervises them without it.
func newPaneStateWatch(log *slog.Logger) *paneStateWatch {
	reg, err := panestate.Load()
	if err != nil {
		log.Error("pane-state manifests failed to load; declarative pane-state detection is off", "err", err)
		return nil
	}
	return &paneStateWatch{
		reg:     reg,
		log:     log,
		capture: tmuxCapturePane,
		titles:  listTmuxPaneTitles,
		now:     time.Now,
		entries: map[string]*paneStateEntry{},
	}
}

// tick classifies every eligible pane once and posts the transitions.
//
// `hasAuthority` is D-2: it reports whether a live in-process driver authors
// this agent's state. It is a callback because the runner owns the driver
// map. Upstream has the same gate — `lifecycle_authority_active` short-
// circuits its detection loop before the screen is ever read (pane.rs:807).
//
// Disjointness with the IdleDetector that P3 retires is structural, not
// coincidental: every family the overlay maps is a registered agent family,
// so hasStructuredDriver() already makes tickIdle skip it. That invariant is a
// test (TestPaneStateFamiliesAreRegisteredAgentFamilies) because it is the
// kind that rots silently — adding a mapping for an unregistered kind would
// have both detectors scraping the same pane and disagreeing.
func (w *paneStateWatch) tick(ctx context.Context, poster AgentEventPoster,
	agents []Agent2, hasAuthority func(agentID string) bool) {
	if w == nil || w.reg == nil || poster == nil {
		return
	}
	now := w.now()

	type eligible struct {
		agent      Agent2
		manifestID string
		entry      *paneStateEntry
	}
	var due []eligible
	seen := make(map[string]struct{}, len(agents))

	for _, ag := range agents {
		if ag.PaneID == "" || ag.PauseState == "paused" {
			continue
		}
		manifestID, mapped := w.reg.ManifestForFamily(ag.Kind)
		if !mapped {
			// D-3: an unmapped family gets no evaluation, never a guess.
			// Classifying an engine with another engine's rules produces
			// confident, wrong attention.
			continue
		}
		if hasAuthority != nil && hasAuthority(ag.ID) {
			continue
		}
		seen[ag.ID] = struct{}{}

		e := w.entries[ag.ID]
		if e == nil || e.manifestID != manifestID {
			e = &paneStateEntry{manifestID: manifestID}
			w.entries[ag.ID] = e
			w.post(ctx, poster, ag, e, e.identify(now), paneStatePublish{}, nil)
			continue
		}
		if e.inGrace(now) {
			// No capture at all during the grace window — upstream reads no
			// screen either, and a capture we would discard is a subprocess
			// we should not spawn.
			e.pending.clear()
			continue
		}
		e.graceUntil = time.Time{}
		due = append(due, eligible{agent: ag, manifestID: manifestID, entry: e})
	}

	// Prune before the (possibly expensive) evaluation pass so a long agent
	// list does not keep dead entries alive for an extra tick.
	for id := range w.entries {
		if _, ok := seen[id]; !ok {
			delete(w.entries, id)
		}
	}
	if len(due) == 0 {
		return
	}

	// D-4: one round-trip for every pane's OSC title. A failure degrades to
	// empty titles rather than skipping the tick — the screen regions still
	// classify, and only the `osc_title` rules go quiet.
	titles, err := w.titles(ctx)
	if err != nil {
		w.log.Debug("pane title read failed; classifying on screen text alone", "err", err)
		titles = nil
	}

	for _, d := range due {
		screen, cerr := w.capture(ctx, d.agent.PaneID)
		if cerr != nil {
			// Transient tmux failures (pane gone, server restarted) are
			// tickReconcile's business; skip this pane and keep the entry so
			// the next tick resumes from the same published state.
			w.log.Debug("pane capture for state failed", "agent", d.agent.ID, "err", cerr)
			continue
		}
		// No trimming. D-4 describes the geometry as "the bottom-anchored
		// last 24 rows"; upstream's `ghostty_detection_text` actually reads
		// `terminal.rows()` — the whole viewport — and falls back to
		// DEFAULT_DETECTION_ROWS=24 only when the row count is unavailable
		// (terminal.rs:2468-2475). `capture-pane -p -J` already returns
		// exactly the visible screen, so trimming to 24 would CUT rows the
		// rules were written to see on a taller pane.
		ex, eerr := w.reg.EvaluateManifest(d.manifestID, panestate.Input{
			Screen:   screen,
			OSCTitle: titles[d.agent.PaneID],
			// OSCProgress stays empty: tmux does not surface OSC 9;4
			// progress to a client. Three vendored rules reference it and
			// are inert for us (D-4, documented rather than worked around).
		})
		if eerr != nil {
			w.log.Debug("pane state evaluation failed", "agent", d.agent.ID, "err", eerr)
			continue
		}
		prev := d.entry.published
		next, publish := d.entry.step(ex, now)
		if !publish {
			continue
		}
		w.post(ctx, poster, d.agent, d.entry, next, prev, &ex)
	}
}

// post emits one classification as an agent event.
//
// The payload carries no screen text. A region preview is P4's explain verb,
// which a human asks for; putting it in every transition would push pane
// contents into the transcript of an agent that may be showing a secret.
func (w *paneStateWatch) post(ctx context.Context, poster AgentEventPoster, ag Agent2,
	e *paneStateEntry, next, prev paneStatePublish, ex *panestate.Explain) {
	payload := map[string]any{
		"state":          string(next.state),
		"previous_state": string(prev.state),
		"family":         ag.Kind,
		"manifest_id":    e.manifestID,
		"pane":           ag.PaneID,
	}
	if m, ok := w.reg.Manifest(e.manifestID); ok {
		payload["manifest_version"] = m.Version
		payload["manifest_source"] = m.Source
	}
	if ex == nil {
		// The startup baseline: nothing was evaluated, so there is no rule to
		// name. Said explicitly rather than left to be inferred from an
		// absent rule_id.
		payload["baseline"] = true
	} else if ex.MatchedRule != nil {
		payload["rule_id"] = ex.MatchedRule.ID
	} else if ex.FallbackReason != "" {
		payload["fallback_reason"] = ex.FallbackReason
	}
	if next.visibleIdle {
		payload["visible_idle"] = true
	}
	if next.visibleBlocker {
		payload["visible_blocker"] = true
	}
	if next.visibleWorking {
		payload["visible_working"] = true
	}
	if err := poster.PostAgentEvent(ctx, ag.ID, PaneStateEventKind, paneStateEventProducer, payload); err != nil {
		w.log.Debug("post pane_state event failed", "agent", ag.ID, "err", err)
	}
}
