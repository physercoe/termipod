package hostrunner

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/termipod/hub/internal/agentfamilies"
	"github.com/termipod/hub/internal/panestate"
)

// --- fixtures -------------------------------------------------------------

// Real codex screens, lifted from the P1 corpus
// (internal/panestate/testdata/screen_corpus.json), which lifted them from
// herdr's own manifest tests. Using real screens here rather than synthetic
// ones is what makes these wiring tests say something: a screen invented to
// satisfy the rules under test proves only that I read the rules.
const (
	codexBlockedScreen = "• Working (4s • esc to interrupt)\n" +
		"› 1. Yes, proceed\n" +
		"Press enter to confirm or esc to cancel\n"
	codexWorkingScreen = "• Working (4s • esc to interrupt)\n"
	// The transcript viewer is codex's `skip_state_update` rule: the user is
	// reading scrollback, so whatever the screen says about the agent is
	// stale and the published state must freeze.
	codexTranscriptScreen = "• Working (4s • esc to interrupt)\n" +
		"› transcript\n" +
		"↑/↓ to scroll · pgup/pgdn to move · home/end to jump · q to quit · esc to edit prev\n"
)

// testWatch builds a watcher over the REAL embedded registry with every tmux
// seam replaced. Nothing in this file may reach a tmux server.
type testWatch struct {
	*paneStateWatch
	poster   *recordingPoster
	screen   string
	titles   map[string]string
	captured []string
	now      time.Time
	capErr   error
}

func newTestWatch(t *testing.T) *testWatch {
	t.Helper()
	reg, err := panestate.Load()
	if err != nil {
		t.Fatalf("load embedded manifests: %v", err)
	}
	tw := &testWatch{
		poster: &recordingPoster{},
		titles: map[string]string{},
		now:    time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC),
	}
	tw.paneStateWatch = &paneStateWatch{
		reg:     reg,
		log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		entries: map[string]*paneStateEntry{},
		now:     func() time.Time { return tw.now },
		capture: func(_ context.Context, paneID string) (string, error) {
			tw.captured = append(tw.captured, paneID)
			if tw.capErr != nil {
				return "", tw.capErr
			}
			return tw.screen, nil
		},
		titles: func(context.Context) (map[string]string, error) { return tw.titles, nil },
	}
	return tw
}

func (tw *testWatch) tickAgents(agents []Agent2, authority func(string) bool) {
	tw.paneStateWatch.tick(context.Background(), tw.poster, agents, authority)
}

func codexAgent() Agent2 {
	return Agent2{ID: "ag-1", Handle: "cx", Kind: "codex", Status: "running", PaneID: "%7"}
}

// settle runs the identification tick and steps past the startup grace, so a
// caller can assert on classification without the baseline in the way.
func (tw *testWatch) settle(agents []Agent2, authority func(string) bool) {
	tw.tickAgents(agents, authority)
	tw.now = tw.now.Add(paneStateStartupGrace + time.Second)
	tw.poster.reset()
	tw.captured = nil
}

// --- D-5 state machine ----------------------------------------------------

func publishState(s panestate.State) paneStatePublish { return paneStatePublish{state: s} }

// Mirrors herdr's own `pending_idle_holds_working_to_plain_idle_until_confirmed`
// (src/pane/agent_detection.rs) at their 100 ms cadence: the first plain-idle
// observation starts the hold with ZERO confirmations, so the release lands on
// the fourth observation, not the third.
func TestPendingIdleHoldMatchesUpstreamLadder(t *testing.T) {
	now := time.Now()
	prev := publishState(panestate.StateWorking)
	next := publishState(panestate.StateIdle)
	var p pendingIdleHold

	const recheck = 100 * time.Millisecond
	for i, at := range []time.Duration{0, recheck, recheck * 2} {
		if !p.hold(prev, next, false, false, now.Add(at)) {
			t.Fatalf("observation %d: want hold, got release", i)
		}
	}
	if p.hold(prev, next, false, false, now.Add(recheck*3)) {
		t.Fatal("fourth observation: want release after 3 confirmations, got hold")
	}
	if p.active() {
		t.Fatal("hold should be cleared after release")
	}
}

// At the host-runner's real cadence the CAP is what releases the hold, not the
// confirmation count — one tick of delay, not three. This is the constant that
// would silently change meaning if PollInterval ever dropped below ~233 ms.
func TestPendingIdleHoldCapReleasesAtRunnerCadence(t *testing.T) {
	now := time.Now()
	prev := publishState(panestate.StateWorking)
	next := publishState(panestate.StateIdle)
	var p pendingIdleHold

	if !p.hold(prev, next, false, false, now) {
		t.Fatal("first observation should hold")
	}
	if !p.hold(prev, next, false, false, now.Add(paneStateIdleCap-time.Millisecond)) {
		t.Fatal("just inside the cap should still hold")
	}
	// Reset and take a real 3 s tick: the cap fires on the very next one.
	p.clear()
	if !p.hold(prev, next, false, false, now) {
		t.Fatal("first observation should hold")
	}
	if p.hold(prev, next, false, false, now.Add(3*time.Second)) {
		t.Fatal("a 3s tick is past the 700ms cap; want release")
	}
}

func TestPendingIdleHoldBypassedByVisibleChrome(t *testing.T) {
	now := time.Now()
	prev := publishState(panestate.StateWorking)
	var p pendingIdleHold

	visibleIdle := paneStatePublish{state: panestate.StateIdle, visibleIdle: true}
	if p.hold(prev, visibleIdle, false, false, now) {
		t.Fatal("a positively-drawn idle prompt must not be held")
	}
	// A blocked screen is the other never-hold case.
	blocked := paneStatePublish{state: panestate.StateBlocked, visibleBlocker: true}
	if p.hold(prev, blocked, false, false, now) {
		t.Fatal("blocked must publish immediately")
	}
}

func TestPendingIdleHoldClearedByNonIdleObservation(t *testing.T) {
	now := time.Now()
	prev := publishState(panestate.StateWorking)
	var p pendingIdleHold

	if !p.hold(prev, publishState(panestate.StateIdle), false, false, now) {
		t.Fatal("first plain idle should hold")
	}
	if p.hold(prev, publishState(panestate.StateWorking), false, false, now.Add(time.Millisecond)) {
		t.Fatal("working observation should not hold")
	}
	if p.active() {
		t.Fatal("a non-idle observation must clear the hold, not leave it armed")
	}
}

// A `skip_state_update` observation is discarded before the state machine sees
// it, so a transcript viewer opened mid-hold neither cancels the hold nor
// counts as a confirmation towards it.
func TestPaneStateEntryFreezesOnSkipStateUpdate(t *testing.T) {
	now := time.Now()
	e := &paneStateEntry{published: publishState(panestate.StateWorking)}

	if _, publish := e.step(panestate.Explain{State: panestate.StateIdle}, now); publish {
		t.Fatal("first plain idle should be held, not published")
	}
	before := e.pending

	frozen := panestate.Explain{State: panestate.StateUnknown, SkipStateUpdate: true}
	if _, publish := e.step(frozen, now.Add(time.Millisecond)); publish {
		t.Fatal("a frozen observation must not publish")
	}
	if e.pending != before {
		t.Fatalf("frozen observation moved the hold: %+v -> %+v", before, e.pending)
	}
	if e.published.state != panestate.StateWorking {
		t.Fatalf("frozen observation changed the published state to %q", e.published.state)
	}
}

// Upstream publishes on a change to the state OR any visible_* hint. "blocked,
// dialog on screen" is a different claim from "blocked, inferred".
func TestPaneStateEntryPublishesWhenOnlyVisibleHintChanges(t *testing.T) {
	now := time.Now()
	e := &paneStateEntry{published: paneStatePublish{state: panestate.StateBlocked}}

	ex := panestate.Explain{
		State:          panestate.StateBlocked,
		VisibleBlocker: true,
		MatchedRule:    &panestate.MatchedRule{ID: "live_strong_blocker"},
	}
	next, publish := e.step(ex, now)
	if !publish {
		t.Fatal("visible_blocker turning on is a transition")
	}
	if !next.visibleBlocker {
		t.Fatal("published tuple should carry the hint")
	}
	if _, again := e.step(ex, now.Add(time.Second)); again {
		t.Fatal("an unchanged observation must not re-publish")
	}
}

// --- eligibility (D-2 / D-3) ---------------------------------------------

// D-3: an unmapped family is never evaluated — and never even captured, so
// the silence costs nothing. kimi-code-ts is deliberately unmapped (the
// overlay records why).
func TestPaneStateUnmappedFamilyIsNeverCaptured(t *testing.T) {
	tw := newTestWatch(t)
	agents := []Agent2{{ID: "ag-k", Kind: "kimi-code-ts", PaneID: "%3", Status: "running"}}

	tw.tickAgents(agents, func(string) bool { return false })
	tw.now = tw.now.Add(10 * time.Second)
	tw.tickAgents(agents, func(string) bool { return false })

	if len(tw.captured) != 0 {
		t.Fatalf("unmapped family was captured: %v", tw.captured)
	}
	if got := tw.poster.all(); len(got) != 0 {
		t.Fatalf("unmapped family produced events: %+v", got)
	}
}

// D-2, the acceptance line: an agent whose adapter authors state is never
// evaluated. Asserting on the CAPTURE (not just the event) is the stronger
// claim — a driver-owned pane is not even read.
func TestPaneStateStructuredAuthorityIsNeverCaptured(t *testing.T) {
	tw := newTestWatch(t)
	tw.screen = codexBlockedScreen
	agents := []Agent2{codexAgent()}

	authority := func(string) bool { return true }
	tw.tickAgents(agents, authority)
	tw.now = tw.now.Add(10 * time.Second)
	tw.tickAgents(agents, authority)

	if len(tw.captured) != 0 {
		t.Fatalf("a pane with a state authority was captured: %v", tw.captured)
	}
	if got := tw.poster.all(); len(got) != 0 {
		t.Fatalf("a pane with a state authority produced events: %+v", got)
	}
}

func TestPaneStatePausedAgentIsNeverCaptured(t *testing.T) {
	tw := newTestWatch(t)
	tw.screen = codexBlockedScreen
	ag := codexAgent()
	ag.PauseState = "paused"

	tw.tickAgents([]Agent2{ag}, func(string) bool { return false })
	tw.now = tw.now.Add(10 * time.Second)
	tw.tickAgents([]Agent2{ag}, func(string) bool { return false })

	if len(tw.captured) != 0 {
		t.Fatalf("paused agent was captured: %v", tw.captured)
	}
}

// --- startup grace --------------------------------------------------------

func TestPaneStateStartupGraceSuppressesCapture(t *testing.T) {
	tw := newTestWatch(t)
	tw.screen = codexWorkingScreen
	agents := []Agent2{codexAgent()}
	none := func(string) bool { return false }

	// Identification tick: one baseline event, no capture.
	tw.tickAgents(agents, none)
	if len(tw.captured) != 0 {
		t.Fatalf("identification tick captured the pane: %v", tw.captured)
	}
	base := tw.poster.all()
	if len(base) != 1 {
		t.Fatalf("want one baseline event, got %d", len(base))
	}
	if got := base[0].Payload["state"]; got != string(panestate.StateIdle) {
		t.Fatalf("baseline state = %v, want idle", got)
	}
	if base[0].Payload["baseline"] != true {
		t.Fatal("baseline event should say so rather than leave it inferred from an absent rule_id")
	}

	// Inside the grace window: still no capture. This is the braille-splash
	// trap — a startup banner animates and animation reads as working.
	tw.now = tw.now.Add(paneStateStartupGrace - time.Millisecond)
	tw.tickAgents(agents, none)
	if len(tw.captured) != 0 {
		t.Fatalf("captured inside the startup grace: %v", tw.captured)
	}
	if n := len(tw.poster.all()); n != 1 {
		t.Fatalf("grace window published %d events, want only the baseline", n)
	}

	// Past it, classification resumes.
	tw.now = tw.now.Add(2 * time.Millisecond)
	tw.tickAgents(agents, none)
	if len(tw.captured) != 1 {
		t.Fatalf("want one capture after the grace expired, got %v", tw.captured)
	}
	last, _ := tw.poster.last()
	if got := last.Payload["state"]; got != string(panestate.StateWorking) {
		t.Fatalf("state after grace = %v, want working", got)
	}
}

// --- end to end, real manifests ------------------------------------------

func TestPaneStateCodexApprovalScreenPublishesBlocked(t *testing.T) {
	tw := newTestWatch(t)
	agents := []Agent2{codexAgent()}
	none := func(string) bool { return false }
	tw.titles["%7"] = "project"
	tw.screen = codexWorkingScreen
	tw.settle(agents, none)

	// working first...
	tw.tickAgents(agents, none)
	last, ok := tw.poster.last()
	if !ok || last.Payload["state"] != string(panestate.StateWorking) {
		t.Fatalf("want working, got %+v", last.Payload)
	}

	// ...then the approval dialog.
	tw.screen = codexBlockedScreen
	tw.now = tw.now.Add(3 * time.Second)
	tw.tickAgents(agents, none)

	last, ok = tw.poster.last()
	if !ok {
		t.Fatal("no event for the approval screen")
	}
	if last.Kind != PaneStateEventKind {
		t.Fatalf("kind = %q, want %q", last.Kind, PaneStateEventKind)
	}
	// The producer column is a closed enum (agent|user|system); a `panestate`
	// producer would be rejected by the hub with a 400.
	if last.Producer != "system" {
		t.Fatalf("producer = %q, want system", last.Producer)
	}
	if last.AgentID != "ag-1" {
		t.Fatalf("agent = %q", last.AgentID)
	}
	// The acceptance triple.
	if got := last.Payload["state"]; got != string(panestate.StateBlocked) {
		t.Fatalf("state = %v, want blocked", got)
	}
	if got := last.Payload["rule_id"]; got != "live_strong_blocker" {
		t.Fatalf("rule_id = %v, want live_strong_blocker", got)
	}
	if got, ok := last.Payload["manifest_version"].(string); !ok || got == "" {
		t.Fatalf("manifest_version missing: %+v", last.Payload)
	}
	if got := last.Payload["previous_state"]; got != string(panestate.StateWorking) {
		t.Fatalf("previous_state = %v, want working", got)
	}
	if last.Payload["visible_blocker"] != true {
		t.Fatalf("want visible_blocker on a drawn approval dialog: %+v", last.Payload)
	}
	// P4 owns screen previews; a transition event must not carry pane text.
	for k, v := range last.Payload {
		if s, ok := v.(string); ok && s == codexBlockedScreen {
			t.Fatalf("payload key %q leaked the screen", k)
		}
	}
}

// The transcript viewer freezes the published state end-to-end: the user is
// reading scrollback and the working spinner behind it is stale.
func TestPaneStateTranscriptViewerFreezesPublishedState(t *testing.T) {
	tw := newTestWatch(t)
	agents := []Agent2{codexAgent()}
	none := func(string) bool { return false }
	tw.titles["%7"] = "project"
	tw.screen = codexBlockedScreen
	tw.settle(agents, none)

	tw.tickAgents(agents, none)
	if last, _ := tw.poster.last(); last.Payload["state"] != string(panestate.StateBlocked) {
		t.Fatalf("setup: want blocked, got %+v", last.Payload)
	}
	before := len(tw.poster.all())

	tw.screen = codexTranscriptScreen
	tw.now = tw.now.Add(3 * time.Second)
	tw.tickAgents(agents, none)
	if n := len(tw.poster.all()); n != before {
		t.Fatalf("transcript viewer published %d new events, want 0", n-before)
	}
}

func TestPaneStateNoMatchFallsBackToIdleWithReason(t *testing.T) {
	tw := newTestWatch(t)
	agents := []Agent2{codexAgent()}
	none := func(string) bool { return false }
	tw.screen = "nothing here resembles a codex screen\n"
	tw.settle(agents, none)

	tw.tickAgents(agents, none)
	// The baseline already said idle, so a fallback-idle is not a transition
	// and nothing is published — which is itself the assertion worth making.
	if got := tw.poster.all(); len(got) != 0 {
		t.Fatalf("fallback idle re-published over an idle baseline: %+v", got)
	}
	// Drive it through working so the fallback becomes a real transition and
	// the reason is visible on the wire.
	tw.screen = codexWorkingScreen
	tw.now = tw.now.Add(3 * time.Second)
	tw.tickAgents(agents, none)
	tw.screen = "nothing here resembles a codex screen\n"
	// Two ticks: the first arms the working->plain-idle hold, the second is
	// past the 700ms cap and releases it.
	tw.now = tw.now.Add(3 * time.Second)
	tw.tickAgents(agents, none)
	if last, _ := tw.poster.last(); last.Payload["state"] != string(panestate.StateWorking) {
		t.Fatalf("hold should still be suppressing idle, got %+v", last.Payload)
	}
	tw.now = tw.now.Add(3 * time.Second)
	tw.tickAgents(agents, none)

	last, _ := tw.poster.last()
	if got := last.Payload["state"]; got != string(panestate.StateIdle) {
		t.Fatalf("state = %v, want idle", got)
	}
	if got := last.Payload["fallback_reason"]; got != panestate.FallbackKnownAgentIdle {
		t.Fatalf("fallback_reason = %v, want %q", got, panestate.FallbackKnownAgentIdle)
	}
	if _, ok := last.Payload["rule_id"]; ok {
		t.Fatal("a fallback classification must not name a rule it did not match")
	}
}

func TestPaneStateCaptureFailureKeepsEntry(t *testing.T) {
	tw := newTestWatch(t)
	agents := []Agent2{codexAgent()}
	none := func(string) bool { return false }
	tw.screen = codexWorkingScreen
	tw.settle(agents, none)

	tw.tickAgents(agents, none)
	if n := len(tw.poster.all()); n != 1 {
		t.Fatalf("setup: want 1 event, got %d", n)
	}
	tw.capErr = context.DeadlineExceeded
	tw.now = tw.now.Add(3 * time.Second)
	tw.tickAgents(agents, none)
	if n := len(tw.poster.all()); n != 1 {
		t.Fatalf("a failed capture published an event")
	}
	// The entry survived, so the next good capture resumes from `working`
	// rather than re-identifying and replaying a baseline.
	tw.capErr = nil
	tw.screen = codexBlockedScreen
	tw.titles["%7"] = "project"
	tw.now = tw.now.Add(3 * time.Second)
	tw.tickAgents(agents, none)
	last, _ := tw.poster.last()
	if got := last.Payload["previous_state"]; got != string(panestate.StateWorking) {
		t.Fatalf("previous_state = %v, want working (entry was dropped?)", got)
	}
}

func TestPaneStatePrunesEntriesForVanishedAgents(t *testing.T) {
	tw := newTestWatch(t)
	agents := []Agent2{codexAgent()}
	none := func(string) bool { return false }
	tw.screen = codexWorkingScreen
	tw.settle(agents, none)
	tw.tickAgents(agents, none)
	if len(tw.entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(tw.entries))
	}

	tw.now = tw.now.Add(3 * time.Second)
	tw.tickAgents(nil, none)
	if len(tw.entries) != 0 {
		t.Fatalf("entry survived the agent leaving the running set: %+v", tw.entries)
	}
}

// --- invariants -----------------------------------------------------------

// The legacy IdleDetector and this watcher must never scrape the same pane.
// They are disjoint because tickIdle skips any agent whose kind is a
// registered family, and every family the overlay maps is one. That is an
// invariant of the OVERLAY, not of this file, so it has to be asserted here:
// mapping a family that is not registered would quietly enable both detectors
// on the same pane, to disagree with each other.
func TestPaneStateFamiliesAreRegisteredAgentFamilies(t *testing.T) {
	reg, err := panestate.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	families := reg.Families()
	if len(families) == 0 {
		t.Fatal("no families mapped; the overlay lost its engines block")
	}
	for _, family := range families {
		if _, ok := agentfamilies.ByName(family); !ok {
			t.Errorf("overlay maps %q, which is not a registered agent family: "+
				"hasStructuredDriver() returns false for it, so the legacy idle "+
				"detector would scrape the same pane this watcher classifies", family)
		}
	}
}

// A nil watcher is the disabled state (embedded manifests failed to load).
// It must be inert, not a nil-pointer panic in the poll loop — losing pane
// classification is a degraded host-runner, panicking is a dead one.
func TestPaneStateNilWatchIsInert(t *testing.T) {
	var w *paneStateWatch
	w.tick(context.Background(), &recordingPoster{}, []Agent2{codexAgent()}, func(string) bool { return false })
}

// --- runner-level authority ----------------------------------------------

type noopDriver struct{}

func (noopDriver) Start(context.Context) error { return nil }
func (noopDriver) Stop()                       {}

func TestPaneStateAuthorityDistinguishesRawPaneDriver(t *testing.T) {
	a := &Runner{drivers: map[string]Driver{
		"structured": noopDriver{},
		"raw":        &PaneDriver{AgentID: "raw"},
	}}

	if !a.paneStateAuthority("structured") {
		t.Error("a driver that authors state must count as an authority")
	}
	if a.paneStateAuthority("raw") {
		t.Error("a raw PaneDriver authors no state — it is a text scraper")
	}
	// No driver in this process: the pane outlived a host-runner restart, so
	// nothing is reporting state and the screen is the only signal.
	if a.paneStateAuthority("unknown") {
		t.Error("an agent with no live driver has no state authority")
	}
}

// --- tmux title parsing ---------------------------------------------------

func TestParsePaneTitlesKeepsSpacesAndEmpties(t *testing.T) {
	out := "%1 codex — my project\n%2\n%3 \n\n%4 llm-proxy\n"
	got := parsePaneTitles(out)

	want := map[string]string{
		"%1": "codex — my project",
		"%2": "",
		"%3": "",
		"%4": "llm-proxy",
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d panes, want %d: %+v", len(got), len(want), got)
	}
	for id, title := range want {
		if got[id] != title {
			t.Errorf("%s title = %q, want %q", id, got[id], title)
		}
	}
}
