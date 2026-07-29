package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

// Tests for the structural-issues fold (digest_issues.go, transcript P5 §2).
//
// The expectations here are hand-built, NOT derived from the fold: an
// incremental-vs-brute equivalence check can only prove the two paths agree,
// never that either is right — a rule both share is invisible to it. So each
// run below states independently what a reader of that event sequence should
// see, and the equivalence test comes after.

// issueRun is one hand-built event sequence plus what the rules should find in
// it, written out by hand from the sequence.
type issueRun struct {
	name   string
	events []foldEvent
	// terminal folds the run as an agent that has already stopped (the seal).
	terminal bool
	// issues is class → expected count. A class absent from the map must NOT
	// appear at all, so a rule that over-fires fails here.
	issues map[string]int64
	// anchors is class → the seq each sample should point at, in order.
	anchors map[string][]int64
	// labels is class → the sample headline, in order.
	labels map[string][]string
	// errors pins the reported-error taxonomy alongside, so a new issue rule
	// that leaks into Errors (double-counting the same failure) is caught.
	errors map[string]int64
	// turnIssues is the per-turn issue_count, by turn index.
	turnIssues []int64
}

func ev(seq int64, kind string, payload map[string]any) foldEvent {
	if payload == nil {
		payload = map[string]any{}
	}
	// A fixed ts per seq keeps durations deterministic without the tests caring.
	return foldEvent{Seq: seq, Ordinal: seq, Kind: kind, TS: "2026-07-29T10:00:0" + string(rune('0'+seq%10)) + "Z", Payload: payload}
}

func issueRuns() []issueRun {
	return []issueRun{
		{
			name: "a tool call whose result never arrives",
			events: []foldEvent{
				ev(1, "turn.start", map[string]any{"turn_id": "t1"}),
				ev(2, "tool_call", map[string]any{"id": "c1", "name": "Bash"}),
				ev(3, "tool_call", map[string]any{"id": "c2", "name": "Read"}),
				ev(4, "tool_result", map[string]any{"tool_use_id": "c2"}),
				ev(5, "turn.result", map[string]any{"turn_id": "t1", "status": "success"}),
			},
			issues: map[string]int64{issueMissingToolResult: 1},
			// Anchored on the CALL (seq 2), not on the turn.result that noticed.
			anchors:    map[string][]int64{issueMissingToolResult: {2}},
			labels:     map[string][]string{issueMissingToolResult: {"Bash"}},
			turnIssues: []int64{1},
		},
		{
			name: "a result matching no call",
			events: []foldEvent{
				ev(1, "turn.start", map[string]any{"turn_id": "t1"}),
				ev(2, "tool_result", map[string]any{"tool_use_id": "ghost", "name": "Edit"}),
				ev(3, "turn.result", map[string]any{"turn_id": "t1", "status": "success"}),
			},
			issues:     map[string]int64{issueOrphanToolResult: 1},
			anchors:    map[string][]int64{issueOrphanToolResult: {2}},
			labels:     map[string][]string{issueOrphanToolResult: {"Edit"}},
			turnIssues: []int64{1},
		},
		{
			name: "an unanswered permission gate is not a plumbing fault",
			events: []foldEvent{
				ev(1, "turn.start", map[string]any{"turn_id": "t1"}),
				ev(2, "tool_call", map[string]any{"id": "p1", "name": "mcp__termipod__permission_prompt"}),
				ev(3, "turn.result", map[string]any{"turn_id": "t1", "status": "success"}),
			},
			issues:     map[string]int64{issueUnansweredPermission: 1},
			anchors:    map[string][]int64{issueUnansweredPermission: {2}},
			turnIssues: []int64{1},
		},
		{
			name: "an answered gate raises nothing",
			events: []foldEvent{
				ev(1, "turn.start", map[string]any{"turn_id": "t1"}),
				ev(2, "tool_call", map[string]any{"id": "p1", "name": "permission_prompt"}),
				ev(3, "tool_result", map[string]any{"tool_use_id": "p1"}),
				ev(4, "turn.result", map[string]any{"turn_id": "t1", "status": "success"}),
			},
			issues:     map[string]int64{},
			turnIssues: []int64{0},
		},
		{
			name: "a run that stops mid-turn, sealed",
			events: []foldEvent{
				ev(1, "turn.start", map[string]any{"turn_id": "t1"}),
				ev(2, "tool_call", map[string]any{"id": "c1", "name": "Bash"}),
			},
			terminal: true,
			issues: map[string]int64{
				issueIncompleteTurn:    1,
				issueMissingToolResult: 1,
			},
			anchors: map[string][]int64{
				issueIncompleteTurn:    {1},
				issueMissingToolResult: {2},
			},
			turnIssues: []int64{2},
		},
		{
			name: "the same run still live raises nothing",
			events: []foldEvent{
				ev(1, "turn.start", map[string]any{"turn_id": "t1"}),
				ev(2, "tool_call", map[string]any{"id": "c1", "name": "Bash"}),
			},
			issues:     map[string]int64{},
			turnIssues: []int64{0},
		},
		{
			name: "a turn stopped for an abnormal reason claude reports no status for",
			events: []foldEvent{
				ev(1, "turn.start", map[string]any{"turn_id": "t1"}),
				ev(2, "turn.result", map[string]any{"turn_id": "t1", "terminal_reason": "error_max_turns"}),
			},
			issues: map[string]int64{issueAbnormalStop: 1},
			// The turn's first event, not the end marker (issue #64 convention).
			anchors: map[string][]int64{issueAbnormalStop: {1}},
			labels:  map[string][]string{issueAbnormalStop: {"error_max_turns"}},
			// Nothing reported it: turnResultStatus reads an absent status as
			// success, which is exactly why the issues layer has to catch it.
			errors:     map[string]int64{},
			turnIssues: []int64{1},
		},
		{
			name: "a normal end raises nothing",
			events: []foldEvent{
				ev(1, "turn.start", map[string]any{"turn_id": "t1"}),
				ev(2, "turn.result", map[string]any{"turn_id": "t1", "stop_reason": "end_turn"}),
			},
			issues:     map[string]int64{},
			turnIssues: []int64{0},
		},
		{
			name: "a director-cancelled turn is an intentional end, not a finding",
			events: []foldEvent{
				ev(1, "turn.start", map[string]any{"turn_id": "t1"}),
				ev(2, "turn.result", map[string]any{"turn_id": "t1", "stop_reason": "cancelled"}),
			},
			issues:     map[string]int64{},
			turnIssues: []int64{0},
		},
		{
			name: "an already-reported failure is not double-counted as an issue",
			events: []foldEvent{
				ev(1, "turn.start", map[string]any{"turn_id": "t1"}),
				ev(2, "turn.result", map[string]any{"turn_id": "t1", "status": "failed", "stop_reason": "refusal"}),
			},
			issues:     map[string]int64{},
			errors:     map[string]int64{"failed_turn": 1},
			turnIssues: []int64{1}, // error_count's turn tally, not issue_count
		},
		{
			name: "one kind carrying its tool id under two spellings",
			events: []foldEvent{
				ev(1, "tool_call", map[string]any{"id": "c1", "name": "A"}),
				ev(2, "tool_result", map[string]any{"tool_use_id": "c1"}),
				ev(3, "tool_call", map[string]any{"toolCallId": "c2", "name": "B"}),
				ev(4, "tool_result", map[string]any{"tool_use_id": "c2"}),
			},
			issues:  map[string]int64{issueMixedIDShape: 1},
			anchors: map[string][]int64{issueMixedIDShape: {3}},
			labels:  map[string][]string{issueMixedIDShape: {"tool_call.toolCallId"}},
			// The turn is synthetic (no turn.start) and never closes, so the
			// call/result pairing is complete but the turn stays open — unsealed,
			// that is not a finding.
			turnIssues: []int64{1},
		},
		{
			name: "the normal cross-kind spread is not a mixed shape",
			events: []foldEvent{
				ev(1, "tool_call", map[string]any{"id": "c1", "name": "A"}),
				ev(2, "tool_result", map[string]any{"tool_use_id": "c1"}),
				ev(3, "tool_call", map[string]any{"id": "c2", "name": "B"}),
				ev(4, "tool_call_update", map[string]any{"toolCallId": "c2", "status": "completed"}),
			},
			issues:     map[string]int64{},
			turnIssues: []int64{0},
		},
		{
			name: "an in-flight update does not resolve the call, a terminal one does",
			events: []foldEvent{
				ev(1, "turn.start", map[string]any{"turn_id": "t1"}),
				ev(2, "tool_call", map[string]any{"id": "c1", "name": "Bash"}),
				ev(3, "tool_call_update", map[string]any{"toolCallId": "c1", "status": "in_progress"}),
				ev(4, "tool_call_update", map[string]any{"toolCallId": "c1", "status": "completed"}),
				ev(5, "turn.result", map[string]any{"turn_id": "t1", "status": "success"}),
			},
			issues:     map[string]int64{},
			turnIssues: []int64{0},
		},
		{
			name: "a trailing update after the result is not an orphan",
			events: []foldEvent{
				ev(1, "turn.start", map[string]any{"turn_id": "t1"}),
				ev(2, "tool_call", map[string]any{"id": "c1", "name": "Bash"}),
				ev(3, "tool_result", map[string]any{"tool_use_id": "c1"}),
				ev(4, "tool_call_update", map[string]any{"toolCallId": "c1", "status": "completed"}),
				ev(5, "turn.result", map[string]any{"turn_id": "t1", "status": "success"}),
			},
			issues:     map[string]int64{},
			turnIssues: []int64{0},
		},
		{
			name: "a partial update carrying no status leaves the call open",
			events: []foldEvent{
				ev(1, "turn.start", map[string]any{"turn_id": "t1"}),
				ev(2, "tool_call", map[string]any{"id": "c1", "name": "Bash"}),
				ev(3, "tool_call_update", map[string]any{"toolCallId": "c1"}),
				ev(4, "turn.result", map[string]any{"turn_id": "t1", "status": "success"}),
			},
			issues:     map[string]int64{issueMissingToolResult: 1},
			anchors:    map[string][]int64{issueMissingToolResult: {2}},
			turnIssues: []int64{1},
		},
		{
			name: "findings sweep in call order across several open calls",
			events: []foldEvent{
				ev(1, "turn.start", map[string]any{"turn_id": "t1"}),
				ev(2, "tool_call", map[string]any{"id": "c1", "name": "First"}),
				ev(3, "tool_call", map[string]any{"id": "c2", "name": "Second"}),
				ev(4, "tool_call", map[string]any{"id": "c3", "name": "Third"}),
				ev(5, "turn.result", map[string]any{"turn_id": "t1", "status": "success"}),
			},
			issues:     map[string]int64{issueMissingToolResult: 3},
			anchors:    map[string][]int64{issueMissingToolResult: {2, 3, 4}},
			labels:     map[string][]string{issueMissingToolResult: {"First", "Second", "Third"}},
			turnIssues: []int64{3},
		},
	}
}

// TestIssueRulesOnHandBuiltRuns is the independent-expectation half: each run's
// findings are written out by hand from the event sequence.
func TestIssueRulesOnHandBuiltRuns(t *testing.T) {
	for _, r := range issueRuns() {
		t.Run(r.name, func(t *testing.T) {
			d, turns := computeAgentDigestTerminal("a1", defaultTeamID, r.events, r.terminal)
			assertIssues(t, d, r)
			if r.turnIssues != nil {
				if len(turns) != len(r.turnIssues) {
					t.Fatalf("turn rows = %d, want %d", len(turns), len(r.turnIssues))
				}
				for i, want := range r.turnIssues {
					got := turns[i].IssueCount
					if r.errors != nil && len(r.errors) > 0 {
						// This run's turn tally is exercised by error_count.
						got = turns[i].ErrorCount
					}
					if got != want {
						t.Errorf("turn[%d] per-turn count = %d, want %d", i, got, want)
					}
				}
			}
		})
	}
}

func assertIssues(t *testing.T, d *agentDigest, r issueRun) {
	t.Helper()
	var total int64
	for class, want := range r.issues {
		got := d.Issues[class]
		if got == nil {
			t.Errorf("issues[%q] missing, want count %d", class, want)
			continue
		}
		if got.Count != want {
			t.Errorf("issues[%q].count = %d, want %d", class, got.Count, want)
		}
		if got.Severity != issueSeverityOf(class) {
			t.Errorf("issues[%q].severity = %q, want %q", class, got.Severity, issueSeverityOf(class))
		}
		total += want
	}
	for class, agg := range d.Issues {
		if _, ok := r.issues[class]; !ok {
			t.Errorf("unexpected issue class %q (count %d) — a rule over-fired", class, agg.Count)
		}
	}
	if d.IssueCount != total {
		t.Errorf("issue_count = %d, want %d", d.IssueCount, total)
	}
	for class, want := range r.anchors {
		got := d.Issues[class]
		if got == nil {
			continue
		}
		if len(got.SampleSeqs) != len(want) {
			t.Errorf("issues[%q] samples = %v, want anchors %v", class, got.SampleSeqs, want)
			continue
		}
		for i, w := range want {
			if got.SampleSeqs[i] != w {
				t.Errorf("issues[%q].sample_seqs[%d] = %d, want %d", class, i, got.SampleSeqs[i], w)
			}
			// The three aligned slices must never drift apart.
			if got.SampleOrdinals[i] != w {
				t.Errorf("issues[%q].sample_ordinals[%d] = %d, want %d (aligned with seq)",
					class, i, got.SampleOrdinals[i], w)
			}
			if got.SampleTSs[i] == "" {
				t.Errorf("issues[%q].sample_ts[%d] empty", class, i)
			}
		}
	}
	for class, want := range r.labels {
		got := d.Issues[class]
		if got == nil {
			continue
		}
		for i, w := range want {
			if i < len(got.SampleLabels) && got.SampleLabels[i] != w {
				t.Errorf("issues[%q].sample_labels[%d] = %q, want %q", class, i, got.SampleLabels[i], w)
			}
		}
	}
	if r.errors != nil {
		for class, want := range r.errors {
			got := d.Errors[class]
			if got == nil || got.Count != want {
				t.Errorf("errors[%q] = %v, want count %d", class, got, want)
			}
		}
		for class, agg := range d.Errors {
			if _, ok := r.errors[class]; !ok {
				t.Errorf("issue rule leaked into the error taxonomy: errors[%q] = %d", class, agg.Count)
			}
		}
	}
}

// TestIssuesIncrementalMatchesBrute drives every hand-built run through the
// real POST fold path and asserts the persisted issues equal a brute-force
// scan — the ADR-038 invariant, extended to the new aggregation. The pending
// set and the id-shape memory survive the JSON round-trip through
// fold_state_json, so this also pins that persistence.
func TestIssuesIncrementalMatchesBrute(t *testing.T) {
	for i, r := range issueRuns() {
		if r.terminal {
			continue // the seal is exercised by TestDigestSealIsIdempotent
		}
		t.Run(r.name, func(t *testing.T) {
			s, _ := newTestServer(t)
			ctx := context.Background()
			agentID := "agent-issues-" + string(rune('a'+i))
			if _, err := s.db.Exec(
				`INSERT INTO agents (id, team_id, handle, kind, status, created_at)
				 VALUES (?,?,?,?,?,?)`,
				agentID, defaultTeamID, agentID, "claude-code", "running", NowUTC(),
			); err != nil {
				t.Fatalf("seed agent: %v", err)
			}
			for _, e := range r.events {
				payload, _ := json.Marshal(e.Payload)
				if _, err := evWForTeam(t, s, defaultTeamID).Exec(
					`INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json, session_ordinal)
					 VALUES (?,?,?,?,?,?,?,?)`,
					NewID(), agentID, e.Seq, e.TS, e.Kind, "agent", string(payload), e.Ordinal,
				); err != nil {
					t.Fatalf("insert event seq %d: %v", e.Seq, err)
				}
				s.foldEventIntoDigest(ctx, defaultTeamID, agentID, e.Seq, e.Ordinal, e.Kind, e.TS, "agent", string(payload))
			}

			got, ok, err := loadAgentDigest(ctx, dgRForTeam(t, s, defaultTeamID), agentID)
			if err != nil || !ok {
				t.Fatalf("load digest: ok=%v err=%v", ok, err)
			}
			want, _ := computeAgentDigest(agentID, defaultTeamID, r.events)

			if got.IssueCount != want.IssueCount {
				t.Errorf("issue_count incr=%d brute=%d", got.IssueCount, want.IssueCount)
			}
			if len(got.Issues) != len(want.Issues) {
				t.Errorf("issue classes incr=%v brute=%v", classNames(got.Issues), classNames(want.Issues))
			}
			for class, w := range want.Issues {
				g := got.Issues[class]
				if g == nil {
					t.Errorf("issues[%q] missing from the incremental fold", class)
					continue
				}
				if g.Count != w.Count || g.Severity != w.Severity {
					t.Errorf("issues[%q] incr=(%d,%s) brute=(%d,%s)", class, g.Count, g.Severity, w.Count, w.Severity)
				}
				// Order matters: an unsorted sweep would agree on counts and
				// differ here.
				if len(g.SampleSeqs) != len(w.SampleSeqs) {
					t.Errorf("issues[%q] samples incr=%v brute=%v", class, g.SampleSeqs, w.SampleSeqs)
					continue
				}
				for i, ws := range w.SampleSeqs {
					if g.SampleSeqs[i] != ws {
						t.Errorf("issues[%q].sample_seqs incr=%v brute=%v", class, g.SampleSeqs, w.SampleSeqs)
						break
					}
				}
			}

			turns, err := loadAllTurns(ctx, dgRForTeam(t, s, defaultTeamID), agentID)
			if err != nil {
				t.Fatalf("load turns: %v", err)
			}
			_, wantTurns := computeAgentDigest(agentID, defaultTeamID, r.events)
			if len(turns) != len(wantTurns) {
				t.Fatalf("turn rows incr=%d brute=%d", len(turns), len(wantTurns))
			}
			for i := range wantTurns {
				if turns[i].IssueCount != wantTurns[i].IssueCount {
					t.Errorf("turn[%d].issue_count incr=%d brute=%d", i, turns[i].IssueCount, wantTurns[i].IssueCount)
				}
			}
		})
	}
}

func classNames(m map[string]*issueClassAgg) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestDigestSealIsIdempotent pins that the end-of-run findings are folded
// exactly once however many times the terminal hook fires, and that a refold
// reproduces them — without which every schema bump would either drop or
// duplicate them.
func TestDigestSealIsIdempotent(t *testing.T) {
	events := []foldEvent{
		ev(1, "turn.start", map[string]any{"turn_id": "t1"}),
		ev(2, "tool_call", map[string]any{"id": "c1", "name": "Bash"}),
	}
	d, turns := computeAgentDigestTerminal("a1", defaultTeamID, events, true)
	if d.IssueCount != 2 {
		t.Fatalf("sealed issue_count = %d, want 2 (incomplete_turn + missing_tool_result)", d.IssueCount)
	}
	if turns[0].IssueCount != 2 {
		t.Errorf("sealed turn issue_count = %d, want 2", turns[0].IssueCount)
	}

	// Re-sealing the same digest must be a no-op.
	f := newDigestFolder(d)
	f.open = &turns[0]
	if f.seal() {
		t.Error("seal reported work on an already-sealed digest")
	}
	if d.IssueCount != 2 {
		t.Errorf("issue_count after re-seal = %d, want 2", d.IssueCount)
	}

	// A refold of the same log reproduces the seal exactly.
	again, _ := computeAgentDigestTerminal("a1", defaultTeamID, events, true)
	if again.IssueCount != d.IssueCount {
		t.Errorf("refold issue_count = %d, want %d", again.IssueCount, d.IssueCount)
	}
	for class, w := range d.Issues {
		g := again.Issues[class]
		if g == nil || g.Count != w.Count {
			t.Errorf("refold issues[%q] = %v, want count %d", class, g, w.Count)
		}
	}
}

// TestRefoldPreservesOutcome pins that recomputing a digest keeps the terminal
// stamp. `outcome` is written by the terminal hook, never by the fold, so a
// refold that recomputed from the event log alone would silently clear it — and
// every schema bump refolds every sealed digest, so the loss would be
// fleet-wide and permanent (the hook only runs once, when the session stops).
func TestRefoldPreservesOutcome(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()
	const agentID = "agent-outcome"
	if _, err := s.db.Exec(
		`INSERT INTO agents (id, team_id, handle, kind, status, created_at)
		 VALUES (?,?,?,?,?,?)`,
		agentID, defaultTeamID, agentID, "claude-code", "terminated", NowUTC(),
	); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	events := []foldEvent{
		ev(1, "turn.start", map[string]any{"turn_id": "t1"}),
		ev(2, "turn.result", map[string]any{"turn_id": "t1", "status": "success"}),
	}
	for _, e := range events {
		payload, _ := json.Marshal(e.Payload)
		if _, err := evWForTeam(t, s, defaultTeamID).Exec(
			`INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json)
			 VALUES (?,?,?,?,?,?,?)`,
			NewID(), agentID, e.Seq, e.TS, e.Kind, "agent", string(payload),
		); err != nil {
			t.Fatalf("insert event: %v", err)
		}
		s.foldEventIntoDigest(ctx, defaultTeamID, agentID, e.Seq, e.Ordinal, e.Kind, e.TS, "agent", string(payload))
	}
	s.finalizeDigestOutcome(ctx, defaultTeamID, agentID)

	dr := dgRForTeam(t, s, defaultTeamID)
	before, ok, err := loadAgentDigest(ctx, dr, agentID)
	if err != nil || !ok {
		t.Fatalf("load digest: ok=%v err=%v", ok, err)
	}
	if before.Outcome == "" {
		t.Fatal("outcome not stamped by the terminal hook")
	}

	after, err := s.backfillAgentDigest(ctx, agentID, defaultTeamID)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if after.Outcome != before.Outcome {
		t.Errorf("outcome after refold = %q, want %q (a schema bump would wipe every sealed digest)",
			after.Outcome, before.Outcome)
	}
	reloaded, _, err := loadAgentDigest(ctx, dr, agentID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Outcome != before.Outcome {
		t.Errorf("persisted outcome after refold = %q, want %q", reloaded.Outcome, before.Outcome)
	}
}

// TestEventToolIDKeyPrecedence pins the single id-key table every consumer
// resolves through. The `callToolIdOf` bug shipped three times because each
// reader re-derived this; the table is the thing that must not drift.
func TestEventToolIDKeyPrecedence(t *testing.T) {
	cases := []struct {
		kind    string
		payload map[string]any
		wantID  string
		wantKey string
	}{
		{"tool_call", map[string]any{"id": "a", "toolCallId": "b"}, "a", "id"},
		{"tool_call", map[string]any{"toolCallId": "b"}, "b", "toolCallId"},
		{"tool_result", map[string]any{"tool_use_id": "a", "toolCallId": "b", "id": "c"}, "a", "tool_use_id"},
		{"tool_result", map[string]any{"toolCallId": "b", "id": "c"}, "b", "toolCallId"},
		{"tool_result", map[string]any{"id": "c"}, "c", "id"},
		{"tool_call_update", map[string]any{"toolCallId": "b", "id": "c"}, "b", "toolCallId"},
		{"tool_call_update", map[string]any{"id": "c"}, "c", "id"},
		{"text", map[string]any{"id": "c"}, "", ""},
		{"tool_call", map[string]any{}, "", ""},
	}
	for _, c := range cases {
		id, key := eventToolIDKey(c.kind, c.payload)
		if id != c.wantID || key != c.wantKey {
			t.Errorf("eventToolIDKey(%q, %v) = (%q, %q), want (%q, %q)",
				c.kind, c.payload, id, key, c.wantID, c.wantKey)
		}
		if got := eventToolID(c.kind, c.payload); got != c.wantID {
			t.Errorf("eventToolID(%q, %v) = %q, want %q", c.kind, c.payload, got, c.wantID)
		}
	}
}

func TestIsPermissionGateTool(t *testing.T) {
	gates := []string{"permission_prompt", "mcp__termipod__permission_prompt", "request_approval", "mcp__x__request_approval"}
	for _, n := range gates {
		if !isPermissionGateTool(n) {
			t.Errorf("isPermissionGateTool(%q) = false, want true", n)
		}
	}
	notGates := []string{"", "Bash", "request_help", "permission_prompt_helper", "__permission_prompt", "prompt"}
	for _, n := range notGates {
		if isPermissionGateTool(n) {
			t.Errorf("isPermissionGateTool(%q) = true, want false", n)
		}
	}
}

func TestWorstIssueSeverity(t *testing.T) {
	if got := worstIssueSeverity(nil); got != "" {
		t.Errorf("worstIssueSeverity(nil) = %q, want empty", got)
	}
	m := map[string]*issueClassAgg{
		issueMixedIDShape:   {Count: 3, Severity: issueSeverityInfo},
		issueAbnormalStop:   {Count: 1, Severity: issueSeverityWarning},
		issueIncompleteTurn: {Count: 0, Severity: issueSeverityError}, // zero must not win
	}
	if got := worstIssueSeverity(m); got != issueSeverityWarning {
		t.Errorf("worstIssueSeverity = %q, want %q", got, issueSeverityWarning)
	}
	m[issueMissingToolResult] = &issueClassAgg{Count: 1, Severity: issueSeverityError}
	if got := worstIssueSeverity(m); got != issueSeverityError {
		t.Errorf("worstIssueSeverity = %q, want %q", got, issueSeverityError)
	}
}

// digestStoreDDLv6 is the pinned pre-v7 digest.db shape — a copy, deliberately,
// so the test proves an OLD file is brought forward rather than re-asserting
// whatever the current DDL happens to say.
const digestStoreDDLv6 = `
CREATE TABLE IF NOT EXISTS agent_event_digests (
    agent_id          TEXT PRIMARY KEY,
    team_id           TEXT NOT NULL,
    schema_version    INTEGER NOT NULL DEFAULT 1,
    updated_at        TEXT NOT NULL,
    watermark_seq     INTEGER NOT NULL DEFAULT 0,
    event_count       INTEGER NOT NULL DEFAULT 0,
    turn_count        INTEGER NOT NULL DEFAULT 0,
    first_ts          TEXT NOT NULL DEFAULT '',
    last_ts           TEXT NOT NULL DEFAULT '',
    duration_ms       INTEGER NOT NULL DEFAULT 0,
    cost_usd          REAL NOT NULL DEFAULT 0,
    by_model_json     TEXT NOT NULL DEFAULT '{}',
    error_count       INTEGER NOT NULL DEFAULT 0,
    errors_json       TEXT NOT NULL DEFAULT '{}',
    tool_total        INTEGER NOT NULL DEFAULT 0,
    tool_failed       INTEGER NOT NULL DEFAULT 0,
    tools_json        TEXT NOT NULL DEFAULT '{}',
    latency_hist_json TEXT NOT NULL DEFAULT '{}',
    outcome           TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS agent_turns (
    agent_id      TEXT NOT NULL,
    turn_id       TEXT NOT NULL,
    team_id       TEXT NOT NULL,
    idx           INTEGER NOT NULL,
    start_seq     INTEGER NOT NULL,
    start_ts      TEXT NOT NULL,
    end_seq       INTEGER NOT NULL DEFAULT 0,
    end_ts        TEXT NOT NULL DEFAULT '',
    duration_ms   INTEGER NOT NULL DEFAULT 0,
    status        TEXT NOT NULL DEFAULT '',
    cost_usd      REAL NOT NULL DEFAULT 0,
    in_tokens     INTEGER NOT NULL DEFAULT 0,
    out_tokens    INTEGER NOT NULL DEFAULT 0,
    tool_count    INTEGER NOT NULL DEFAULT 0,
    tool_failed   INTEGER NOT NULL DEFAULT 0,
    error_count   INTEGER NOT NULL DEFAULT 0,
    start_ordinal INTEGER,
    session_id    TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (agent_id, turn_id)
);
`

// TestEnsureDigestSchemaEvolvesAnExistingShard pins the additive-column path.
// The sharded stores sit outside the migration chain and their DDL is
// CREATE TABLE IF NOT EXISTS, so without ensureShardColumns a new column would
// reach fresh installs only and every existing shard would fail on first query.
func TestEnsureDigestSchemaEvolvesAnExistingShard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "digest.db")
	old, err := sql.Open("sqlite", dsnFKOff(path))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := old.Exec(digestStoreDDLv6); err != nil {
		t.Fatalf("v6 schema: %v", err)
	}
	if _, err := old.Exec(`INSERT INTO agent_event_digests
		(agent_id, team_id, schema_version, updated_at) VALUES ('a1','t1',6,'now')`); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	old.Close()

	db, err := ensureDigestStore(path)
	if err != nil {
		t.Fatalf("ensureDigestStore on a v6 file: %v", err)
	}
	defer db.Close()

	for _, c := range digestStoreAddedColumns {
		cols, err := tableColumns(db, c.table)
		if err != nil {
			t.Fatalf("table_info %s: %v", c.table, err)
		}
		if !cols[c.name] {
			t.Errorf("%s.%s missing after ensureDigestSchema", c.table, c.name)
		}
	}
	// The pre-existing row survives and reads back through the current loader.
	ctx := context.Background()
	d, ok, err := loadAgentDigest(ctx, db, "a1")
	if err != nil || !ok {
		t.Fatalf("load pre-existing row: ok=%v err=%v", ok, err)
	}
	if d.SchemaVersion != 6 {
		t.Errorf("schema_version = %d, want 6 (the stale row must still be seen as stale)", d.SchemaVersion)
	}
	if d.Issues == nil || d.State == nil {
		t.Error("loader left Issues/State nil for a row whose columns defaulted")
	}
	// And re-running is a no-op.
	if err := ensureDigestSchema(db); err != nil {
		t.Errorf("second ensureDigestSchema: %v", err)
	}
}

// TestDigestJSONExposesIssues pins the wire shape both clients read.
func TestDigestJSONExposesIssues(t *testing.T) {
	events := []foldEvent{
		ev(1, "turn.start", map[string]any{"turn_id": "t1"}),
		ev(2, "tool_call", map[string]any{"id": "c1", "name": "Bash"}),
		ev(3, "turn.result", map[string]any{"turn_id": "t1", "status": "success"}),
	}
	d, _ := computeAgentDigest("a1", defaultTeamID, events)
	out := digestJSON(d)
	if out["issue_count"] != int64(1) {
		t.Errorf("issue_count = %v, want 1", out["issue_count"])
	}
	if out["issue_worst_severity"] != issueSeverityError {
		t.Errorf("issue_worst_severity = %v, want %q", out["issue_worst_severity"], issueSeverityError)
	}
	issues, ok := out["issues"].(map[string]*issueClassAgg)
	if !ok || issues[issueMissingToolResult] == nil {
		t.Fatalf("issues = %v, want a %s entry", out["issues"], issueMissingToolResult)
	}
	// The reported-error taxonomy is untouched by the new rules.
	if out["error_count"] != int64(0) {
		t.Errorf("error_count = %v, want 0 — issues must not leak into errors", out["error_count"])
	}
	// Round-trips as JSON with the severity a client groups by.
	blob, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := back["issues"].(map[string]any)[issueMissingToolResult].(map[string]any)
	if got["severity"] != issueSeverityError {
		t.Errorf("wire severity = %v, want %q", got["severity"], issueSeverityError)
	}
	if got["count"].(float64) != 1 {
		t.Errorf("wire count = %v, want 1", got["count"])
	}
}

// TestSessionRollupMergesIssues pins that a resumed session's drawer reads as
// one run's findings — the mobile↔desktop parity surface consumes the rollup.
func TestSessionRollupMergesIssues(t *testing.T) {
	mk := func(agent string, seqBase int64) *agentDigest {
		d, _ := computeAgentDigest(agent, defaultTeamID, []foldEvent{
			ev(seqBase+1, "turn.start", map[string]any{"turn_id": "t"}),
			ev(seqBase+2, "tool_call", map[string]any{"id": "c", "name": "Bash"}),
			ev(seqBase+3, "turn.result", map[string]any{"turn_id": "t", "status": "success"}),
		})
		return d
	}
	rollup := newAgentDigest("", defaultTeamID)
	mergeDigest(rollup, mk("a1", 0))
	mergeDigest(rollup, mk("a2", 3))
	if rollup.IssueCount != 2 {
		t.Errorf("rollup issue_count = %d, want 2", rollup.IssueCount)
	}
	agg := rollup.Issues[issueMissingToolResult]
	if agg == nil || agg.Count != 2 {
		t.Fatalf("rollup issues[%s] = %v, want count 2", issueMissingToolResult, agg)
	}
	if agg.Severity != issueSeverityError {
		t.Errorf("rollup severity = %q, want %q", agg.Severity, issueSeverityError)
	}
	if len(agg.SampleSeqs) != 2 || len(agg.SampleTSs) != 2 || len(agg.SampleLabels) != 2 {
		t.Errorf("rollup samples not aligned 1:1: seqs=%v ts=%v labels=%v",
			agg.SampleSeqs, agg.SampleTSs, agg.SampleLabels)
	}
}
