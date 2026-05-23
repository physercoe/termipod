package antigravity

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// drain collects Steps until `want` arrive or the deadline fires.
func drain(t *testing.T, ch <-chan Step, want int, within time.Duration) []Step {
	t.Helper()
	var got []Step
	deadline := time.After(within)
	for len(got) < want {
		select {
		case s, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, s)
		case <-deadline:
			return got
		}
	}
	return got
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// A step seen first as RUNNING then rewritten to DONE in place must emit
// twice (first-sight + RUNNING→DONE), and a step that never changes emits
// once — that is the snapshot/watch-and-diff contract that distinguishes
// this reader from the claude-code append tailer.
func TestReader_RunningThenDone_EmitsTwice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript_full.jsonl")
	writeFile(t, path,
		`{"step_index":0,"type":"USER_INPUT","status":"DONE"}`+"\n"+
			`{"step_index":2,"type":"PLANNER_RESPONSE","status":"RUNNING"}`+"\n")

	r := &Reader{Path: path, PollEvery: 20 * time.Millisecond}
	ch, err := r.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	first := drain(t, ch, 2, time.Second)
	if len(first) != 2 {
		t.Fatalf("first scan: got %d steps, want 2 (%+v)", len(first), first)
	}

	// Rewrite step 2 in place to DONE and append a new step.
	time.Sleep(30 * time.Millisecond) // ensure mtime advances
	writeFile(t, path,
		`{"step_index":0,"type":"USER_INPUT","status":"DONE"}`+"\n"+
			`{"step_index":2,"type":"PLANNER_RESPONSE","status":"DONE"}`+"\n"+
			`{"step_index":3,"type":"MCP_TOOL","status":"DONE"}`+"\n")

	more := drain(t, ch, 2, time.Second)
	if len(more) != 2 {
		t.Fatalf("after rewrite: got %d steps, want 2 (step2 RUNNING→DONE + new step3); (%+v)", len(more), more)
	}
	// step 0 (unchanged DONE) must NOT re-emit.
	for _, s := range more {
		if s.Index == 0 {
			t.Fatalf("unchanged step 0 re-emitted: %+v", s)
		}
	}
}

// Regression lock for the v1.0.646 mobile-feed-replays-old-log bug.
// On resume, the reader must drain the pre-existing transcript into
// `emitted` without sending anything downstream — only steps that
// appear AFTER Start are emitted.
func TestReader_SkipExisting_DoesNotReplayHistory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript_full.jsonl")
	// Seed three "historical" steps before Start.
	writeFile(t, path,
		`{"step_index":0,"type":"USER_INPUT","status":"DONE"}`+"\n"+
			`{"step_index":1,"type":"PLANNER_RESPONSE","status":"DONE"}`+"\n"+
			`{"step_index":2,"type":"MCP_TOOL","status":"DONE"}`+"\n")

	r := &Reader{Path: path, PollEvery: 20 * time.Millisecond, SkipExisting: true}
	ch, err := r.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	// Give the prime + initial scan a beat. Nothing should arrive —
	// every step is already known.
	silent := drain(t, ch, 1, 200*time.Millisecond)
	if len(silent) != 0 {
		t.Fatalf("SkipExisting must not replay history; got %+v", silent)
	}

	// A NEW step appearing post-Start should emit normally.
	time.Sleep(30 * time.Millisecond)
	writeFile(t, path,
		`{"step_index":0,"type":"USER_INPUT","status":"DONE"}`+"\n"+
			`{"step_index":1,"type":"PLANNER_RESPONSE","status":"DONE"}`+"\n"+
			`{"step_index":2,"type":"MCP_TOOL","status":"DONE"}`+"\n"+
			`{"step_index":3,"type":"PLANNER_RESPONSE","status":"DONE"}`+"\n")

	got := drain(t, ch, 1, time.Second)
	if len(got) != 1 || got[0].Index != 3 {
		t.Fatalf("post-Start step 3 should emit; got %+v", got)
	}
}

// Inverse — when SkipExisting is false (the default, fresh-spawn path),
// the reader must emit every pre-existing step on first scan.
func TestReader_DefaultEmitsExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript_full.jsonl")
	writeFile(t, path,
		`{"step_index":0,"type":"USER_INPUT","status":"DONE"}`+"\n"+
			`{"step_index":1,"type":"PLANNER_RESPONSE","status":"DONE"}`+"\n")

	r := &Reader{Path: path, PollEvery: 20 * time.Millisecond /* SkipExisting: false */}
	ch, err := r.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	got := drain(t, ch, 2, time.Second)
	if len(got) != 2 {
		t.Fatalf("fresh spawn should emit both existing steps; got %d (%+v)", len(got), got)
	}
}

func TestReader_TornLineSkippedThenRecovered(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript_full.jsonl")
	// A torn (partial) line plus a whole one.
	writeFile(t, path,
		`{"step_index":0,"type":"USER_INPUT","status":"DONE"}`+"\n"+
			`{"step_index":1,"type":"PLANNER`+"\n")

	r := &Reader{Path: path, PollEvery: 20 * time.Millisecond}
	ch, err := r.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	got := drain(t, ch, 1, 500*time.Millisecond)
	if len(got) != 1 || got[0].Index != 0 {
		t.Fatalf("torn line should be skipped, only step 0 emitted; got %+v", got)
	}

	time.Sleep(30 * time.Millisecond)
	writeFile(t, path,
		`{"step_index":0,"type":"USER_INPUT","status":"DONE"}`+"\n"+
			`{"step_index":1,"type":"PLANNER_RESPONSE","status":"DONE"}`+"\n")

	more := drain(t, ch, 1, time.Second)
	if len(more) != 1 || more[0].Index != 1 {
		t.Fatalf("recovered line should emit step 1; got %+v", more)
	}
}
