package hostrunner

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/termipod/hub/internal/agentfamilies"
)

// Vision-parity E2 — the two supplements AppServerDriver applies on
// top of what the codex frame profile produced.
//
// These run at `translateNotification`, not through the pipes: the
// contract under test is "what shape does a codex notification finally
// have", and that answer is the profile's output plus the driver's
// stateful edits. Testing either half alone would pass while the pair
// disagreed.

func e2Driver(t *testing.T) (*AppServerDriver, *fakePoster) {
	t.Helper()
	f, ok := agentfamilies.ByName("codex")
	if !ok || f.FrameProfile == nil {
		t.Fatal("codex frame_profile not embedded")
	}
	poster := &fakePoster{}
	return &AppServerDriver{
		AgentID:      "agent-e2",
		Engine:       "codex",
		FrameProfile: f.FrameProfile,
		Poster:       poster,
	}, poster
}

func e2Notify(t *testing.T, d *AppServerDriver, method string, params map[string]any) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
	if err != nil {
		t.Fatalf("marshal %s: %v", method, err)
	}
	d.translateNotification(context.Background(), raw)
}

func planNotification(turnID string, steps ...map[string]any) map[string]any {
	plan := make([]any, 0, len(steps))
	for _, s := range steps {
		plan = append(plan, s)
	}
	return map[string]any{"threadId": "thr_1", "turnId": turnID, "plan": plan}
}

func onlyKind(t *testing.T, poster *fakePoster, kind string) []postedEvent {
	t.Helper()
	var out []postedEvent
	for _, e := range poster.snapshot() {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// TestAppServer_PlanStatusIsCanonical — codex spells the middle state
// `inProgress`; the vocabulary (set by ACP, which reached `plan`
// first) spells it `in_progress`. This is the rename that fails
// silently: both clients' renderers treat an unrecognized status as
// "not started", so without it every running step in a codex plan
// showed as unstarted and nothing anywhere reported a problem.
func TestAppServer_PlanStatusIsCanonical(t *testing.T) {
	d, poster := e2Driver(t)
	e2Notify(t, d, "turn/plan/updated", planNotification("turn_1",
		map[string]any{"step": "read auth.go", "status": "completed"},
		map[string]any{"step": "refactor middleware", "status": "inProgress"},
		map[string]any{"step": "run tests", "status": "pending"},
	))

	plans := onlyKind(t, poster, "plan")
	if len(plans) != 1 {
		t.Fatalf("want 1 plan event, got %d (%+v)", len(plans), poster.snapshot())
	}
	entries, _ := plans[0].Payload["entries"].([]any)
	if len(entries) != 3 {
		t.Fatalf("entries = %#v; want 3", plans[0].Payload["entries"])
	}
	want := []string{"completed", "in_progress", "pending"}
	for i, w := range want {
		entry, _ := entries[i].(map[string]any)
		if entry["status"] != w {
			t.Errorf("entries[%d].status = %v; want %q", i, entry["status"], w)
		}
	}
	// The profile's field rename must survive the driver's pass.
	first, _ := entries[0].(map[string]any)
	if first["content"] != "read auth.go" {
		t.Errorf("entries[0].content = %v; want the step text", first["content"])
	}
}

// TestAppServer_PlanStatusPassesUnknownValuesThrough — a status this
// build has never seen is data, not a typo to be repaired. Rewriting
// it would be inventing a claim; leaving it lets the clients apply
// their safe default ("not started").
func TestAppServer_PlanStatusPassesUnknownValuesThrough(t *testing.T) {
	d, poster := e2Driver(t)
	e2Notify(t, d, "turn/plan/updated", planNotification("turn_1",
		map[string]any{"step": "one", "status": "blockedOnHuman"},
	))
	entries, _ := onlyKind(t, poster, "plan")[0].Payload["entries"].([]any)
	entry, _ := entries[0].(map[string]any)
	if entry["status"] != "blockedOnHuman" {
		t.Errorf("status = %v; want the engine's own value untouched", entry["status"])
	}
}

// TestAppServer_PlanChainsPerTurn — every codex plan notification is a
// FULL snapshot, so without a stable chain root the clients render one
// card per snapshot instead of one card that updates. The chain is
// scoped to the turn: a new turn must start a new card, or turn 2's
// plan silently overwrites turn 1's and the transcript loses it.
func TestAppServer_PlanChainsPerTurn(t *testing.T) {
	d, poster := e2Driver(t)
	e2Notify(t, d, "turn/plan/updated", planNotification("turn_1",
		map[string]any{"step": "one", "status": "pending"}))
	e2Notify(t, d, "turn/plan/updated", planNotification("turn_1",
		map[string]any{"step": "one", "status": "inProgress"}))
	e2Notify(t, d, "turn/plan/updated", planNotification("turn_2",
		map[string]any{"step": "two", "status": "pending"}))

	plans := onlyKind(t, poster, "plan")
	if len(plans) != 3 {
		t.Fatalf("want 3 plan events, got %d", len(plans))
	}
	for i, p := range plans {
		if p.Payload["partial"] != true {
			t.Errorf("plans[%d].partial = %v; want true (the fold-in-place marker)", i, p.Payload["partial"])
		}
		if id, _ := p.Payload["message_id"].(string); id == "" {
			t.Errorf("plans[%d] has no message_id — nothing to chain on", i)
		}
	}
	if plans[0].Payload["message_id"] != plans[1].Payload["message_id"] {
		t.Errorf("two snapshots of one turn must share a chain root; got %v and %v",
			plans[0].Payload["message_id"], plans[1].Payload["message_id"])
	}
	if plans[1].Payload["message_id"] == plans[2].Payload["message_id"] {
		t.Errorf("a new turn must start a new card; both got %v", plans[2].Payload["message_id"])
	}
}

// TestAppServer_PlanWithoutTurnIDDoesNotFoldAcrossTurns — codex marks
// turnId required, so this is the can't-happen path. It is pinned
// because the tempting fallback (one chain forever) is the harmful
// one: folding snapshots we can't prove belong together would drop a
// whole turn's plan from the transcript, while N cards merely looks
// repetitive.
func TestAppServer_PlanWithoutTurnIDDoesNotFoldAcrossTurns(t *testing.T) {
	d, poster := e2Driver(t)
	frame := map[string]any{"threadId": "thr_1", "plan": []any{
		map[string]any{"step": "one", "status": "pending"},
	}}
	e2Notify(t, d, "turn/plan/updated", frame)
	e2Notify(t, d, "turn/plan/updated", frame)

	plans := onlyKind(t, poster, "plan")
	if len(plans) != 2 {
		t.Fatalf("want 2 plan events, got %d", len(plans))
	}
	if plans[0].Payload["message_id"] == plans[1].Payload["message_id"] {
		t.Errorf("without a turn id the snapshots must not share a chain root; both got %v",
			plans[0].Payload["message_id"])
	}
}

// TestAppServer_PlanUsesTheTrackedTurnWhenTheFrameOmitsIt — before
// minting a fresh root, fall back to the turn the driver is already
// tracking from turn/started. Same information, second-best source.
func TestAppServer_PlanUsesTheTrackedTurnWhenTheFrameOmitsIt(t *testing.T) {
	d, poster := e2Driver(t)
	e2Notify(t, d, "turn/started", map[string]any{
		"turn": map[string]any{"id": "turn_9", "status": "inProgress"},
	})
	frame := map[string]any{"threadId": "thr_1", "plan": []any{
		map[string]any{"step": "one", "status": "pending"},
	}}
	e2Notify(t, d, "turn/plan/updated", frame)
	e2Notify(t, d, "turn/plan/updated", frame)

	plans := onlyKind(t, poster, "plan")
	if len(plans) != 2 {
		t.Fatalf("want 2 plan events, got %d", len(plans))
	}
	if plans[0].Payload["message_id"] != plans[1].Payload["message_id"] {
		t.Errorf("the tracked turn should chain them; got %v and %v",
			plans[0].Payload["message_id"], plans[1].Payload["message_id"])
	}
}

// TestAppServer_TurnDurationPrefersTheEngineNumber — codex measures
// the turn; the driver measures the notification round trip. When the
// engine reports, its number wins outright.
func TestAppServer_TurnDurationPrefersTheEngineNumber(t *testing.T) {
	d, poster := e2Driver(t)
	e2Notify(t, d, "turn/started", map[string]any{
		"turn": map[string]any{"id": "turn_1", "status": "inProgress"},
	})
	e2Notify(t, d, "turn/completed", map[string]any{
		"turn": map[string]any{"id": "turn_1", "status": "completed", "durationMs": float64(4312)},
	})

	results := onlyKind(t, poster, "turn.result")
	if len(results) != 1 {
		t.Fatalf("want 1 turn.result, got %d", len(results))
	}
	if results[0].Payload["duration_ms"] != float64(4312) {
		t.Errorf("duration_ms = %v; want the engine's 4312", results[0].Payload["duration_ms"])
	}
}

// TestAppServer_TurnDurationFallsBackToTheDriverClock — older codex
// builds have no `durationMs` and current ones may send null ("if
// known"). Without the fallback the turn footer loses its duration on
// exactly those sessions.
func TestAppServer_TurnDurationFallsBackToTheDriverClock(t *testing.T) {
	d, poster := e2Driver(t)
	e2Notify(t, d, "turn/started", map[string]any{
		"turn": map[string]any{"id": "turn_1", "status": "inProgress"},
	})
	time.Sleep(2 * time.Millisecond)
	e2Notify(t, d, "turn/completed", map[string]any{
		"turn": map[string]any{"id": "turn_1", "status": "completed"},
	})

	results := onlyKind(t, poster, "turn.result")
	if len(results) != 1 {
		t.Fatalf("want 1 turn.result, got %d", len(results))
	}
	ms, ok := results[0].Payload["duration_ms"].(int64)
	if !ok {
		t.Fatalf("duration_ms = %#v; want a driver-measured int64", results[0].Payload["duration_ms"])
	}
	if ms < 1 {
		t.Errorf("duration_ms = %d; want at least the 2ms that elapsed", ms)
	}
}

// TestAppServer_TurnDurationAbsentForAnUntimedTurn — the narrow part
// of the fallback. After a host-runner restart mid-turn there is no
// clock for the turn that completes, and a duration measured from
// "when I started watching" is a different quantity wearing the same
// name. Absence is the honest answer (D-4).
func TestAppServer_TurnDurationAbsentForAnUntimedTurn(t *testing.T) {
	d, poster := e2Driver(t)
	e2Notify(t, d, "turn/completed", map[string]any{
		"turn": map[string]any{"id": "turn_1", "status": "completed"},
	})

	results := onlyKind(t, poster, "turn.result")
	if len(results) != 1 {
		t.Fatalf("want 1 turn.result, got %d", len(results))
	}
	if v := results[0].Payload["duration_ms"]; v != nil {
		t.Errorf("duration_ms = %#v; want nil for a turn this driver never saw start", v)
	}
}

// TestAppServer_TurnDurationDoesNotLeakAcrossTurns — the clock is
// keyed by turn id precisely so turn 2's completion can't be measured
// against turn 1's start. Without the key check, a completion for an
// untimed turn would silently pick up the previous turn's clock and
// report a plausible, wrong number.
func TestAppServer_TurnDurationDoesNotLeakAcrossTurns(t *testing.T) {
	d, poster := e2Driver(t)
	e2Notify(t, d, "turn/started", map[string]any{
		"turn": map[string]any{"id": "turn_1", "status": "inProgress"},
	})
	e2Notify(t, d, "turn/completed", map[string]any{
		"turn": map[string]any{"id": "turn_2", "status": "completed"},
	})

	results := onlyKind(t, poster, "turn.result")
	if len(results) != 1 {
		t.Fatalf("want 1 turn.result, got %d", len(results))
	}
	if v := results[0].Payload["duration_ms"]; v != nil {
		t.Errorf("duration_ms = %#v; want nil — turn_1's clock does not measure turn_2", v)
	}
}
