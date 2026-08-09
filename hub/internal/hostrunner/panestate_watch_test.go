package hostrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
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
	attn     *recordingAttention
	screen   string
	titles   map[string]string
	activity map[string]int64
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
		poster:   &recordingPoster{},
		attn:     &recordingAttention{},
		titles:   map[string]string{},
		activity: map[string]int64{},
		now:      time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC),
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
		meta: func(context.Context) (map[string]paneMeta, error) {
			m := map[string]paneMeta{}
			for id, title := range tw.titles {
				m[id] = paneMeta{title: title, activity: tw.activity[id]}
			}
			for id, act := range tw.activity {
				if _, ok := m[id]; !ok {
					m[id] = paneMeta{activity: act}
				}
			}
			return m, nil
		},
	}
	return tw
}

func (tw *testWatch) tickAgents(agents []Agent2, authority func(string) bool) {
	tw.paneStateWatch.tick(context.Background(), tw.poster, tw.attn, agents, authority)
}

// recordingAttention stands in for the hub's /attention surface. It hands out
// increasing ids so a test can tell "the same row" from "a second row".
type recordingAttention struct {
	raised   []AttentionIn
	resolved []string
	postErr  error
	next     int
}

func (r *recordingAttention) PostAttention(_ context.Context, in AttentionIn) (AttentionOut, error) {
	if r.postErr != nil {
		return AttentionOut{}, r.postErr
	}
	r.raised = append(r.raised, in)
	r.next++
	return AttentionOut{ID: fmt.Sprintf("att-%d", r.next)}, nil
}

func (r *recordingAttention) ResolveAttention(_ context.Context, id string) error {
	r.resolved = append(r.resolved, id)
	return nil
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

// --- attention (P3) -------------------------------------------------------

// blockedWatch settles a codex agent and leaves it showing the approval
// dialog, which is the starting point for most of the attention tests.
func blockedWatch(t *testing.T) (*testWatch, []Agent2, func(string) bool) {
	t.Helper()
	tw := newTestWatch(t)
	agents := []Agent2{codexAgent()}
	none := func(string) bool { return false }
	tw.titles["%7"] = "project"
	tw.screen = codexWorkingScreen
	tw.settle(agents, none)
	tw.tickAgents(agents, none) // publish working

	tw.screen = codexBlockedScreen
	tw.now = tw.now.Add(3 * time.Second)
	return tw, agents, none
}

// The plan's headline acceptance clause: a real codex approval screen becomes
// an attention item naming the rule that matched.
func TestBlockedScreenRaisesAttentionWithRuleID(t *testing.T) {
	tw, agents, none := blockedWatch(t)
	tw.tickAgents(agents, none)

	if len(tw.attn.raised) != 1 {
		t.Fatalf("raised %d attention items, want 1", len(tw.attn.raised))
	}
	got := tw.attn.raised[0]
	if got.Kind != paneStateAttentionKind {
		t.Errorf("kind = %q, want %q", got.Kind, paneStateAttentionKind)
	}
	if got.ActorHandle != "cx" {
		t.Errorf("actor_handle = %q, want the agent handle", got.ActorHandle)
	}
	if !strings.Contains(got.Summary, "blocked") || !strings.Contains(got.Summary, "cx") {
		t.Errorf("summary = %q, want it to name the state and the agent", got.Summary)
	}

	var payload map[string]any
	if err := json.Unmarshal(got.PendingPayload, &payload); err != nil {
		t.Fatalf("pending_payload is not json: %v", err)
	}
	if payload["rule_id"] != "live_strong_blocker" {
		t.Errorf("rule_id = %v, want live_strong_blocker", payload["rule_id"])
	}
	if payload["agent_id"] != "ag-1" || payload["pane"] != "%7" {
		t.Errorf("payload lost the agent/pane pointer: %+v", payload)
	}
	// Same rule as the event: evidence is a rule id, never pane text. An
	// attention row fans out further than the transcript does.
	for k, v := range payload {
		if s, ok := v.(string); ok && strings.Contains(s, "Yes, proceed") {
			t.Fatalf("payload key %q leaked the screen", k)
		}
	}
	if strings.Contains(got.Summary, "Yes, proceed") {
		t.Fatalf("summary leaked the screen: %q", got.Summary)
	}
	// The transition event points at the row it raised.
	last, ok := tw.poster.last()
	if !ok || last.Payload["attention_id"] != "att-1" {
		t.Errorf("pane_state event should name the attention row: %+v", last.Payload)
	}
}

// Once per streak, not once per tick. The hub's attention model owns
// re-delivery — that is why D-5's 800 ms visible-blocker re-publish was
// deliberately not ported.
func TestBlockedStreakRaisesExactlyOnce(t *testing.T) {
	tw, agents, none := blockedWatch(t)
	for i := 0; i < 5; i++ {
		tw.tickAgents(agents, none)
		tw.now = tw.now.Add(3 * time.Second)
	}
	if len(tw.attn.raised) != 1 {
		t.Fatalf("raised %d rows across one blocked streak, want 1", len(tw.attn.raised))
	}
	if len(tw.attn.resolved) != 0 {
		t.Fatalf("resolved %v while still blocked", tw.attn.resolved)
	}
}

// The human answered the dialog in the terminal. Nothing tells the hub, so the
// row would sit open forever; the classification leaving blocked is the signal.
func TestAttentionRetractsWhenClassificationLeavesBlocked(t *testing.T) {
	tw, agents, none := blockedWatch(t)
	tw.tickAgents(agents, none)

	tw.screen = codexWorkingScreen
	tw.now = tw.now.Add(3 * time.Second)
	tw.tickAgents(agents, none)

	if len(tw.attn.resolved) != 1 || tw.attn.resolved[0] != "att-1" {
		t.Fatalf("resolved = %v, want [att-1]", tw.attn.resolved)
	}
	// And a NEW streak gets its own row rather than reviving the closed one.
	tw.screen = codexBlockedScreen
	tw.now = tw.now.Add(3 * time.Second)
	tw.tickAgents(agents, none)
	if len(tw.attn.raised) != 2 {
		t.Fatalf("raised %d rows across two streaks, want 2", len(tw.attn.raised))
	}
}

// An agent that stopped running takes its row with it: the row asks someone to
// answer a dialog on a pane that no longer exists.
func TestAttentionRetractsWhenAgentLeavesRunningSet(t *testing.T) {
	tw, agents, none := blockedWatch(t)
	tw.tickAgents(agents, none)

	tw.now = tw.now.Add(3 * time.Second)
	tw.tickAgents(nil, none)

	if len(tw.attn.resolved) != 1 || tw.attn.resolved[0] != "att-1" {
		t.Fatalf("resolved = %v, want [att-1]", tw.attn.resolved)
	}
}

// A hub that was down when the streak began must not cost the whole streak its
// row. The retry works only because tick() decides attention on every
// classified pass, not just on a transition — deleting that property here
// leaves this test as the one that fails.
func TestAttentionRaiseRetriesAfterAFailure(t *testing.T) {
	tw, agents, none := blockedWatch(t)
	tw.attn.postErr = errors.New("hub down")
	tw.tickAgents(agents, none)
	if len(tw.attn.raised) != 0 {
		t.Fatalf("recorded a raise that failed: %+v", tw.attn.raised)
	}

	tw.attn.postErr = nil
	tw.now = tw.now.Add(3 * time.Second)
	tw.tickAgents(agents, none) // same screen: no transition to publish

	if len(tw.attn.raised) != 1 {
		t.Fatalf("raised %d rows after the hub recovered, want 1", len(tw.attn.raised))
	}
}

// `blocked` without `visible_blocker` is an inference — an OSC title, a
// missing spinner. Worth recording as state, not worth waking someone for.
func TestAttentionNeedsAVisibleBlocker(t *testing.T) {
	var e paneStateEntry
	inferred := paneStatePublish{state: panestate.StateBlocked}
	if got := e.attentionFor(inferred); got != attentionNone {
		t.Errorf("inferred blocked: action = %v, want none", got)
	}
	seen := paneStatePublish{state: panestate.StateBlocked, visibleBlocker: true}
	if got := e.attentionFor(seen); got != attentionRaise {
		t.Errorf("visible blocker: action = %v, want raise", got)
	}
	// The dialog scrolling out of the matched region does not end the streak —
	// only leaving `blocked` does.
	e.attentionID = "att-1"
	if got := e.attentionFor(inferred); got != attentionNone {
		t.Errorf("still blocked: action = %v, want none", got)
	}
	if got := e.attentionFor(paneStatePublish{state: panestate.StateIdle}); got != attentionRetract {
		t.Errorf("left blocked: action = %v, want retract", got)
	}
}

// The plan's second acceptance clause: the idle-shell false-positive class
// that the legacy regex detector exists to catch must be UNABLE to raise here.
// A bare `$` prompt is not a blocked agent, and no vendored rule says it is.
func TestBareShellPromptCannotRaiseAttention(t *testing.T) {
	tw := newTestWatch(t)
	agents := []Agent2{codexAgent()}
	none := func(string) bool { return false }
	tw.screen = codexWorkingScreen
	tw.settle(agents, none)
	tw.tickAgents(agents, none) // publish working, so idle is a transition

	// Two ticks: working → plain idle is the one transition D-5 holds, and at
	// our 3 s cadence the cap releases it on the second observation.
	tw.screen = "$ \n"
	for i := 0; i < 2; i++ {
		tw.now = tw.now.Add(3 * time.Second)
		tw.tickAgents(agents, none)
	}

	if len(tw.attn.raised) != 0 {
		t.Fatalf("a bare shell prompt raised %+v", tw.attn.raised)
	}
	last, ok := tw.poster.last()
	if !ok || last.Payload["state"] != string(panestate.StateIdle) {
		t.Fatalf("bare prompt should classify idle, got %+v", last.Payload)
	}
	if last.Payload["fallback_reason"] != panestate.FallbackKnownAgentIdle {
		t.Errorf("want the known-agent idle fallback, got %+v", last.Payload)
	}
}

// --- capture-cost gating (B5) ---------------------------------------------

func TestSkipCaptureOnlyForAnIdleUnchangedPane(t *testing.T) {
	base := func() *paneStateEntry {
		e := &paneStateEntry{published: paneStatePublish{state: panestate.StateIdle}}
		e.scanActivity = 1770000000
		return e
	}
	if !base().skipCapture(1770000000) {
		t.Error("idle pane, unmoved activity: want skip")
	}
	if base().skipCapture(1770000001) {
		t.Error("activity moved: must re-read")
	}
	if base().skipCapture(0) {
		t.Error("unknown stamp: must re-read")
	}

	// A blocked or working pane is re-read every tick no matter what the stamp
	// says. That asymmetry is what keeps a stale stamp from ever freezing the
	// state this lane exists to report.
	for _, s := range []panestate.State{panestate.StateBlocked, panestate.StateWorking, panestate.StateUnknown} {
		e := base()
		e.published.state = s
		if e.skipCapture(1770000000) {
			t.Errorf("state %s: must never be skipped", s)
		}
	}
	// Nor mid-hysteresis: the hold needs its next observation to resolve.
	held := base()
	held.pending.startedAt = time.Now()
	if held.skipCapture(1770000000) {
		t.Error("a pending idle hold must not be skipped")
	}
	// Never scanned: nothing to compare against.
	fresh := &paneStateEntry{published: paneStatePublish{state: panestate.StateIdle}}
	if fresh.skipCapture(0) || fresh.skipCapture(1770000000) {
		t.Error("an unarmed gate must not skip")
	}
}

// `#{window_activity}` has one-second resolution, so a stamp read DURING the
// second it names cannot be compared for equality later — output arriving
// later in that same second would produce an identical stamp, and for an idle
// pane that skip would repeat forever.
func TestNoteScanRefusesAStampFromTheCurrentSecond(t *testing.T) {
	at := time.Unix(1770000000, 0)
	var e paneStateEntry

	e.noteScan(1770000000, at) // captured inside the second the stamp names
	if e.scanActivity != 0 {
		t.Errorf("armed the gate on a same-second stamp: %d", e.scanActivity)
	}
	e.noteScan(1770000000, at.Add(1500*time.Millisecond)) // that second has passed
	if e.scanActivity != 1770000000 {
		t.Errorf("scanActivity = %d, want the stamp", e.scanActivity)
	}
	e.noteScan(0, at.Add(time.Hour)) // unknown stamp disarms
	if e.scanActivity != 0 {
		t.Errorf("an unknown stamp must disarm the gate, got %d", e.scanActivity)
	}
}

// End to end: an idle pane whose window produced no output is not captured at
// all — no subprocess, no evaluation.
func TestCaptureGateSkipsAnUnchangedIdlePane(t *testing.T) {
	tw := newTestWatch(t)
	agents := []Agent2{codexAgent()}
	none := func(string) bool { return false }
	// A stamp from well before the settle clock, so noteScan arms.
	tw.activity["%7"] = tw.now.Add(-time.Minute).Unix()
	tw.screen = "$ \n"
	tw.settle(agents, none)

	tw.tickAgents(agents, none) // first read arms the gate
	if len(tw.captured) != 1 {
		t.Fatalf("first pass captured %d times, want 1", len(tw.captured))
	}
	tw.now = tw.now.Add(3 * time.Second)
	tw.tickAgents(agents, none)
	if len(tw.captured) != 1 {
		t.Fatalf("unchanged idle pane was captured again: %v", tw.captured)
	}

	// Output arrives: the stamp moves and the pane is read again, this time
	// showing a dialog that must still reach attention.
	tw.activity["%7"] = tw.now.Unix()
	tw.screen = codexBlockedScreen
	tw.titles["%7"] = "project"
	tw.now = tw.now.Add(3 * time.Second)
	tw.tickAgents(agents, none)
	if len(tw.captured) != 2 {
		t.Fatalf("moved activity should force a re-read: %v", tw.captured)
	}
	if len(tw.attn.raised) != 1 {
		t.Fatalf("the dialog behind the gate raised %d rows, want 1", len(tw.attn.raised))
	}
}

// A pane the gate has never armed on — because the metadata read failed, or
// tmux has no `#{window_activity}` — is captured every tick, as before.
func TestCaptureGateDefaultsToCapturing(t *testing.T) {
	tw := newTestWatch(t)
	agents := []Agent2{codexAgent()}
	none := func(string) bool { return false }
	tw.screen = "$ \n"
	tw.settle(agents, none)

	for i := 0; i < 3; i++ {
		tw.tickAgents(agents, none)
		tw.now = tw.now.Add(3 * time.Second)
	}
	if len(tw.captured) != 3 {
		t.Fatalf("captured %d times without an activity stamp, want 3", len(tw.captured))
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
	w.tick(context.Background(), &recordingPoster{}, &recordingAttention{},
		[]Agent2{codexAgent()}, func(string) bool { return false })
	if w.covers("codex") {
		t.Error("a disabled watcher covers nothing — else the legacy detector stays off too")
	}
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

// P3's IdleDetector retirement, asserted rather than assumed: for every family
// the evaluator covers, the legacy regex detector must stay quiet — otherwise
// two detectors scrape the same pane and disagree about it.
//
// This holds even for a raw PaneDriver, which is the whole point: that is the
// agent the evaluator is FOR, and it is also the only one the legacy detector
// would have taken.
func TestIdleDetectorSkipsEveryMappedFamily(t *testing.T) {
	a := &Runner{
		drivers:    map[string]Driver{"ag-raw": &PaneDriver{AgentID: "ag-raw"}},
		paneStates: newPaneStateWatch(slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
	if a.paneStates == nil {
		t.Fatal("embedded manifests failed to load")
	}
	families := a.paneStates.reg.Families()
	if len(families) == 0 {
		t.Fatal("no families mapped; the overlay lost its engines block")
	}
	for _, family := range families {
		if !a.hasAnyStateAuthority(Agent2{ID: "ag-raw", Kind: family}) {
			t.Errorf("family %q is evaluated by the manifests but the legacy "+
				"idle detector would also scrape its pane", family)
		}
	}
}

// The retirement must not widen the legacy detector's reach. `kimi-code-ts` is
// a registered family the overlay deliberately does NOT map, and an instance
// that fell back to a raw pane has no structured driver either — the third
// clause of hasAnyStateAuthority is the only thing keeping the regex detector
// off it, and off the TUI-prompt false positive the W11 smoke found.
func TestUnmappedRegisteredFamilyStaysOutOfTheLegacyDetector(t *testing.T) {
	a := &Runner{
		drivers:    map[string]Driver{"ag-raw": &PaneDriver{AgentID: "ag-raw"}},
		paneStates: newPaneStateWatch(slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
	const family = "kimi-code-ts"
	if _, mapped := a.paneStates.reg.ManifestForFamily(family); mapped {
		t.Skipf("%s is mapped now; this test's premise moved", family)
	}
	if _, ok := agentfamilies.ByName(family); !ok {
		t.Fatalf("%s is no longer a registered agent family", family)
	}
	if !a.hasAnyStateAuthority(Agent2{ID: "ag-raw", Kind: family}) {
		t.Error("an unmapped registered family must stay out of the legacy detector")
	}

	// An unregistered, unmapped kind is exactly what the legacy detector is
	// still for, and it must still get it.
	if a.hasAnyStateAuthority(Agent2{ID: "ag-raw", Kind: "some-legacy-script"}) {
		t.Error("the legacy detector lost the agents it exists for")
	}
}

// --- tmux pane metadata parsing -------------------------------------------

func TestParsePaneMetaKeepsSpacesAndEmpties(t *testing.T) {
	out := "%1 1770000000 codex — my project\n" + // both fields
		"%2\n" + // pane id alone
		"%3 1770000001 \n" + // trailing space, empty title
		"\n" + // blank line
		"%4  llm-proxy\n" + // tmux too old for #{window_activity}
		"%5 not-a-number title\n" // garbage where the stamp should be

	got := parsePaneMeta(out)

	want := map[string]paneMeta{
		"%1": {title: "codex — my project", activity: 1770000000},
		"%2": {},
		"%3": {activity: 1770000001},
		// An absent or unparseable stamp must read as UNKNOWN (0), never as a
		// timestamp — 0 disables the capture gate, a wrong number would make it
		// skip a pane forever.
		"%4": {title: "llm-proxy"},
		"%5": {title: "title"},
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d panes, want %d: %+v", len(got), len(want), got)
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s = %+v, want %+v", id, got[id], w)
		}
	}
}
