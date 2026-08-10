// Declarative pane-state classification, wired into the host-runner's poll
// tick (docs/plans/pane-state-manifests.md lane P, wedges P2 + P3).
//
// `internal/panestate` (P1) is a pure library: screen in, classification out.
// This file is everything around it — which panes are eligible, where the
// screen and the OSC title come from, the debounce/hysteresis/startup-grace
// state machine, the agent event a transition becomes, and (P3) the attention
// row a blocked streak opens and later withdraws.
//
// Where it runs, and why not in PaneDriver
// ----------------------------------------
// The plan's P2 line says "feed the evaluator from `PaneDriver`'s tick". It
// lives in the runner's pane tick instead, because two of the plan's own
// decisions cannot be satisfied from inside a driver:
//
//   - D-3 needs the agent's family. PaneDriver only knows an agent id and a
//     pane id; the family lives on the hub's agent row.
//   - D-4 wants ONE `list-panes -F` round-trip covering all panes for the
//     OSC title (and, since P3, the activity stamp). A per-driver call is one
//     round-trip per agent.
//
// The runner tick already walks every running pane for the stall detector, so
// this adds no new enumeration — and the two are disjoint by construction
// (see paneStateWatch.tick and Runner.hasAnyStateAuthority).
//
// D-2's structured-authority exception is NOT here, and P3 records why: it is
// not a port (upstream short-circuits on `lifecycle_authority_active` before
// reading the screen), and whether it would complement or duplicate claude's
// hook-raised permission_prompt rows cannot be settled without watching a real
// pane. See the plan's D-2 correction.
package hostrunner

import (
	"context"
	"encoding/json"
	"errors"
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

// paneStateAttentionKind is the attention kind a blocked classification
// raises (P3). D-6 left the choice open — "no new attention kind unless P3
// review finds `idle` semantically wrong for 'blocked on approval'" — and the
// review's answer is: reuse `idle`, because the KIND on this surface is a
// routing-and-affordance token, not the classification.
//
// `idle` is the only kind both clients already route correctly for a row a
// human can acknowledge but not answer:
//
//   - mobile buckets it under Agents and renders a single Dismiss
//     (`me_screen.dart` _filterForAttention, `inline_actions.dart`
//     _isInformational). Its kind test runs BEFORE the pending_payload test,
//     so attaching evidence below does not flip it into Requests.
//   - the hub keeps it out of `attentionAwaitsAgentReply`, so /resolve accepts
//     it — which is what makes the retract leg legal at all.
//
// A newly minted kind would have inherited the unknown-kind default instead:
// on mobile, a row carrying a pending_payload falls into Requests and draws
// **Approve / Reject** for a state report nothing can approve. That is the
// same unknown-kind hazard P2 found in the event feed, in a second registry —
// the affordance defaults are per-surface, and neither defaults to silence.
//
// The cost is the term collision this lane spent P1 avoiding: `idle` and
// `blocked` are contrasting states in `internal/panestate`, and the row we
// raise for `blocked` is kind `idle`. It is contained to the wire name — the
// summary, the payload, and the pane_state event all say blocked — and it is
// the smaller of the two prices.
const paneStateAttentionKind = "idle"

// paneStateAttentionSeverity matches the stall detector's. A blocked pane is
// worth surfacing, not worth escalating: nothing is broken, someone just has
// to answer a question the agent asked its terminal instead of the hub.
const paneStateAttentionSeverity = "minor"

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

	// attentionID is the open row for the CURRENT blocked streak, "" when
	// none. One row per streak, not per tick: the hub's attention model owns
	// re-delivery, which is why D-5's 800 ms visible-blocker re-publish was
	// deliberately not ported.
	attentionID string

	// scanActivity is the `#{window_activity}` stamp the last capture is
	// known-good for, or 0 when this pane is not skippable. See noteScan.
	scanActivity int64
}

// attentionAction is what a freshly published classification owes the
// attention surface. Separated from the doing so the decision is testable
// without a hub.
type attentionAction int

const (
	attentionNone attentionAction = iota
	attentionRaise
	attentionRetract
)

// attentionFor decides raise / retract / nothing for a classification that is
// about to be published.
//
// Raising needs BOTH `blocked` and `visible_blocker` (plan P3). The strictness
// is the point: `blocked` alone can come from a rule that inferred the state
// from an OSC title or a spinner's absence, and a guess is not worth waking
// someone for. `visible_blocker` means the manifest matched a dialog that is
// on the screen right now — evidence a human can go look at.
//
// The streak is keyed on the STATE only, so blocked→blocked with the dialog
// scrolling out of the matched region keeps the row (still blocked, still
// waiting); it retracts when the classification leaves blocked entirely.
func (e *paneStateEntry) attentionFor(next paneStatePublish) attentionAction {
	if next.state == panestate.StateBlocked {
		if e.attentionID == "" && next.visibleBlocker {
			return attentionRaise
		}
		return attentionNone
	}
	if e.attentionID != "" {
		return attentionRetract
	}
	return attentionNone
}

// skipCapture is B5's capture-cost gate: don't even read the screen of a pane
// that is idle and has produced no output since we last read it.
//
// Ported from upstream's `should_skip_idle_screen_scan` (agent_detection.rs:91)
// with its conditions intact — skip ONLY when the published state is idle and
// no idle hold is in flight. Upstream's other two guards, `agent_changed` and
// `process_exited`, are the same structurally-false pair documented on
// pendingIdleHold.hold, and its `agent.is_none()` guard is unreachable here
// because eligibility already established the agent.
//
// The asymmetry is what makes a wrong skip survivable: a blocked or working
// pane is re-read every tick no matter what the stamp says, so the gate can
// never freeze the state this lane exists to report. The worst a stale stamp
// can do is delay noticing that an IDLE pane became something else.
func (e *paneStateEntry) skipCapture(activity int64) bool {
	if e.scanActivity == 0 || activity != e.scanActivity {
		return false
	}
	return e.published.state == panestate.StateIdle && !e.pending.active()
}

// noteScan records the activity stamp the capture just taken is valid for.
//
// It stores the stamp only when the SECOND it names had already elapsed when
// we captured. `#{window_activity}` has one-second resolution, so output that
// lands later in the same second as the stamp we read produces an IDENTICAL
// stamp — equality would then read as "nothing happened" when something did,
// and for an idle pane that skip would repeat forever. Requiring
// `now > activity` means any later output must fall in a strictly greater
// second, which makes the equality test sound rather than probabilistic.
//
// A 0 stamp (unknown / unparseable / a tmux without the format) stores 0 and
// the pane is simply never skipped.
func (e *paneStateEntry) noteScan(activity int64, now time.Time) {
	if activity > 0 && now.Unix() > activity {
		e.scanActivity = activity
		return
	}
	e.scanActivity = 0
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
	meta    func(ctx context.Context) (map[string]paneMeta, error)
	now     func() time.Time
	entries map[string]*paneStateEntry
}

// paneStateAttention is the attention surface the watcher needs: raise a row,
// and withdraw the one it raised. *Client satisfies it; tests stub it.
type paneStateAttention interface {
	AttentionPoster
	AttentionResolver
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
		meta:    listTmuxPaneMeta,
		now:     time.Now,
		entries: map[string]*paneStateEntry{},
	}
}

// covers reports whether the declarative evaluator has rules for an agent
// family — i.e. whether this watcher is a state authority for it (plan D-3).
//
// Nil-safe on both receiver and registry so the caller's guard reads the same
// whether or not the embedded manifests loaded.
func (w *paneStateWatch) covers(kind string) bool {
	if w == nil || w.reg == nil || kind == "" {
		return false
	}
	_, ok := w.reg.ManifestForFamily(kind)
	return ok
}

// explain captures one pane and returns the full evaluation record (plan P4).
//
// Deliberately NOT a read of the tick's cached state: the tick stores only the
// published tuple, and a human asking "why does it think that" needs the rules
// that did not match as much as the one that did. It also needs the answer for
// the screen as it is NOW, not as of up to three seconds ago — a rule debugger
// that lags the pane it describes is worse than none.
//
// It ignores the capture gate and the startup grace for the same reason: both
// exist to save work the tick did not need, and this call is the case where
// the work was explicitly asked for.
func (w *paneStateWatch) explain(ctx context.Context, agentID, paneID, family string) (panestate.ExplainResult, error) {
	if w == nil || w.reg == nil {
		return panestate.ExplainResult{}, errPaneStateDisabled
	}
	manifestID, mapped := w.reg.ManifestForFamily(family)
	if !mapped {
		// D-3 again: an unmapped family is a definite answer ("nothing
		// classifies this engine"), not a failure to compute one. The caller
		// turns it into a 422 naming the family.
		return panestate.ExplainResult{}, &UnmappedFamilyError{Family: family}
	}
	screen, err := w.capture(ctx, paneID)
	if err != nil {
		return panestate.ExplainResult{}, err
	}
	title := ""
	if meta, merr := w.meta(ctx); merr == nil {
		title = meta[paneID].title
	} else {
		// Same degradation as the tick: classify on screen text alone rather
		// than fail. The record's empty osc_title is itself the evidence that
		// the `osc_title` rules could not have fired.
		w.log.Debug("pane metadata read failed during explain", "pane", paneID, "err", merr)
	}
	in := panestate.Input{Screen: screen, OSCTitle: title}
	ex, err := w.reg.EvaluateManifest(manifestID, in)
	if err != nil {
		return panestate.ExplainResult{}, err
	}
	res := panestate.NewExplainResult("live", family, in, ex)
	res.AgentID = agentID
	res.PaneID = paneID
	return res, nil
}

// errPaneStateDisabled is returned when the embedded manifests did not load,
// which nils the watcher. Named so the verb can answer "detection is off on
// this host" rather than a generic failure.
var errPaneStateDisabled = errors.New("pane-state detection is disabled on this host (manifests failed to load)")

// UnmappedFamilyError names a family the overlay does not bind to a manifest.
// A typed error because the answer is a 422 with the family in it, not a 500.
type UnmappedFamilyError struct{ Family string }

func (e *UnmappedFamilyError) Error() string {
	return "no pane-state manifest is mapped for agent family " + e.Family
}

// tick classifies every eligible pane once and posts the transitions.
//
// `hasAuthority` is D-2: it reports whether a live in-process driver authors
// this agent's state. It is a callback because the runner owns the driver
// map. Upstream has the same gate — `lifecycle_authority_active` short-
// circuits its detection loop before the screen is ever read (pane.rs:807).
//
// Disjointness with the stall detector (IdleDetector) is enforced from the other
// too: Runner.hasAnyStateAuthority asks w.covers() before running it, so no
// pane is ever scraped by both. TestPaneStateFamiliesAreRegisteredAgentFamilies
// and TestIdleDetectorSkipsEveryMappedFamily lock the two halves — this is the
// kind of invariant that rots silently.
//
// `attn` may be nil, which turns off the attention leg while leaving state
// events flowing. Nothing wires it that way in production; it keeps the
// classification tests free of an attention stub.
func (w *paneStateWatch) tick(ctx context.Context, poster AgentEventPoster,
	attn paneStateAttention, agents []Agent2, hasAuthority func(agentID string) bool) {
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
			// A manifest swap under a live agent re-identifies it, so the row
			// raised under the old rules is withdrawn first — its rule id
			// names evidence the new manifest may not even have.
			w.retract(ctx, attn, e)
			e = &paneStateEntry{manifestID: manifestID}
			w.entries[ag.ID] = e
			w.post(ctx, poster, ag, e, e.identify(now), paneStatePublish{}, nil, "")
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
	// list does not keep dead entries alive for an extra tick. An agent that
	// left the running set takes its attention row with it: the row asked
	// someone to go answer a dialog on a pane that no longer exists.
	for id, e := range w.entries {
		if _, ok := seen[id]; !ok {
			w.retract(ctx, attn, e)
			delete(w.entries, id)
		}
	}
	if len(due) == 0 {
		return
	}

	// D-4: one round-trip for every pane's OSC title, and B5's activity stamp
	// in the same call. A failure degrades rather than skipping the tick — the
	// screen regions still classify, only the `osc_title` rules go quiet, and
	// an unknown stamp simply disables the capture gate for this pass.
	meta, err := w.meta(ctx)
	if err != nil {
		w.log.Debug("pane metadata read failed; classifying on screen text alone", "err", err)
		meta = nil
	}

	for _, d := range due {
		if d.entry.skipCapture(meta[d.agent.PaneID].activity) {
			continue
		}
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
			OSCTitle: meta[d.agent.PaneID].title,
			// OSCProgress stays empty: tmux does not surface OSC 9;4
			// progress to a client. Three vendored rules reference it and
			// are inert for us (D-4, documented rather than worked around).
		})
		if eerr != nil {
			w.log.Debug("pane state evaluation failed", "agent", d.agent.ID, "err", eerr)
			continue
		}
		// The gate arms only after a capture actually succeeded and evaluated,
		// so a failed read never counts as "we have seen this screen".
		d.entry.noteScan(meta[d.agent.PaneID].activity, now)

		prev := d.entry.published
		next, publish := d.entry.step(ex, now)

		// Attention is decided on every classified tick, NOT only on a
		// transition, and before the event so the event can name the row.
		// Deciding it inside the publish branch would make a failed raise
		// permanent: the streak's transition already happened, so the retry
		// tick has nothing to publish and would never look again.
		raised := ""
		switch d.entry.attentionFor(next) {
		case attentionRaise:
			raised = w.raise(ctx, attn, d.agent, d.entry, ex)
		case attentionRetract:
			w.retract(ctx, attn, d.entry)
		case attentionNone:
		}
		if !publish {
			continue
		}
		w.post(ctx, poster, d.agent, d.entry, next, prev, &ex, raised)
	}
}

// raise opens one attention row for a blocked streak and returns its id.
//
// The evidence is a rule id and a manifest version, never screen text — the
// same rule post() follows, for the same reason: a blocked pane is showing
// whatever the agent was doing, which may be a secret, and an attention row is
// the most widely-fanned surface the hub has. P4's explain verb is where a
// human asks for the region preview, deliberately.
func (w *paneStateWatch) raise(ctx context.Context, attn paneStateAttention,
	ag Agent2, e *paneStateEntry, ex panestate.Explain) string {
	if attn == nil {
		return ""
	}
	who := ag.Handle
	if who == "" {
		who = ag.ID
	}
	rule := ""
	if ex.MatchedRule != nil {
		rule = ex.MatchedRule.ID
	}
	summary := "agent blocked at a prompt: " + who
	if rule != "" {
		summary += " (" + rule + ")"
	}
	payload := map[string]any{
		"detector":    "panestate",
		"state":       string(panestate.StateBlocked),
		"agent_id":    ag.ID,
		"family":      ag.Kind,
		"pane":        ag.PaneID,
		"manifest_id": e.manifestID,
	}
	if rule != "" {
		payload["rule_id"] = rule
	}
	if m, ok := w.reg.Manifest(e.manifestID); ok {
		payload["manifest_version"] = m.Version
		payload["manifest_source"] = m.Source
	}
	pending, err := json.Marshal(payload)
	if err != nil {
		// A payload we cannot marshal must not cost the raise — the summary
		// alone still tells a human which agent to go look at.
		w.log.Debug("pane_state attention payload marshal failed", "agent", ag.ID, "err", err)
		pending = nil
	}
	out, err := attn.PostAttention(ctx, AttentionIn{
		ScopeKind:      "team",
		Kind:           paneStateAttentionKind,
		Summary:        summary,
		Severity:       paneStateAttentionSeverity,
		ActorHandle:    ag.Handle,
		PendingPayload: pending,
	})
	if err != nil {
		// Leaving attentionID empty means the next tick that still classifies
		// blocked retries — the streak owes a row, not this tick. That only
		// works because tick() decides attention on every classified pass; see
		// the note there.
		w.log.Debug("pane_state attention raise failed", "agent", ag.ID, "err", err)
		return ""
	}
	e.attentionID = out.ID
	w.log.Info("pane blocked; attention raised",
		"agent", ag.ID, "handle", ag.Handle, "rule", rule, "attention", out.ID)
	return out.ID
}

// retract closes the row this watcher raised, if any. Tolerant by design: a
// 409 means the director dismissed it first, which is the outcome we wanted
// anyway. Either way the id is dropped so the next blocked streak raises fresh.
func (w *paneStateWatch) retract(ctx context.Context, attn paneStateAttention, e *paneStateEntry) {
	if e == nil || e.attentionID == "" {
		return
	}
	id := e.attentionID
	e.attentionID = ""
	if attn == nil {
		return
	}
	if err := attn.ResolveAttention(ctx, id); err != nil {
		w.log.Debug("pane_state attention resolve failed", "attention", id, "err", err)
	}
}

// post emits one classification as an agent event.
//
// The payload carries no screen text. A region preview is P4's explain verb,
// which a human asks for; putting it in every transition would push pane
// contents into the transcript of an agent that may be showing a secret.
func (w *paneStateWatch) post(ctx context.Context, poster AgentEventPoster, ag Agent2,
	e *paneStateEntry, next, prev paneStatePublish, ex *panestate.Explain, attentionID string) {
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
	if attentionID != "" {
		payload["attention_id"] = attentionID
	}
	if err := poster.PostAgentEvent(ctx, ag.ID, PaneStateEventKind, paneStateEventProducer, payload); err != nil {
		w.log.Debug("post pane_state event failed", "agent", ag.ID, "err", err)
	}
}
