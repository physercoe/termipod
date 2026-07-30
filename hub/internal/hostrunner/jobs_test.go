package hostrunner

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/termipod/hub/internal/hostjobs"
)

// jobs_test.go pins the invariants ADR-058 §"Consequences" names as new: a job
// kind never runs inline, two submits never run concurrently, and a restart
// fails the rows a dead process left `delivered`. Everything else here guards
// the paths that would silently lose or duplicate a job.

// ---------------------------------------------------------------------------
// harness
// ---------------------------------------------------------------------------

type jobPatchRec struct {
	ID    string
	Patch CommandPatch
}

// jobHub serves the two endpoints the executor touches: the command list (for
// startup reconciliation) and the command patch (for progress + outcome).
type jobHub struct {
	srv *httptest.Server

	mu        sync.Mutex
	patches   []jobPatchRec
	delivered []HostCommand
}

func newJobHub(t *testing.T) *jobHub {
	t.Helper()
	f := &jobHub{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/commands"):
			f.mu.Lock()
			out := []HostCommand{}
			if r.URL.Query().Get("status") == "delivered" {
				out = append(out, f.delivered...)
			}
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(out)
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/commands/"):
			id := filepath.Base(r.URL.Path)
			var p CommandPatch
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			f.mu.Lock()
			f.patches = append(f.patches, jobPatchRec{ID: id, Patch: p})
			f.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unhandled: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// terminalFor returns the outcome patch recorded for a command id, if any.
// Progress heartbeats carry no status, so they are skipped.
func (f *jobHub) terminalFor(id string) (CommandPatch, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.patches {
		if p.ID == id && p.Patch.Status != "" {
			return p.Patch, true
		}
	}
	return CommandPatch{}, false
}

func (f *jobHub) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.patches)
}

func newJobRunner(t *testing.T, f *jobHub, kinds map[string]jobHandler) *Runner {
	t.Helper()
	a := &Runner{
		Client:       NewClient(f.srv.URL, "tok", "team1"),
		HostID:       "host1",
		Log:          slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})),
		JobCacheRoot: t.TempDir(),
	}
	a.defaults()
	if kinds != nil {
		a.jobKinds = kinds
	}
	return a
}

func jobCmd(id string) HostCommand {
	return HostCommand{
		ID:     id,
		HostID: "host1",
		Kind:   hostjobs.KindDatasetExportRRD,
		Args:   json.RawMessage(`{}`),
		Status: "delivered",
	}
}

// ---------------------------------------------------------------------------
// the three invariants
// ---------------------------------------------------------------------------

// TestRunCommand_JobKindNeverRunsInline is the load-bearing one. tickCommands
// calls runCommand on the host-runner's single main-loop goroutine — the same
// one that spawns agents — so a job that ran inline would stop spawns for its
// whole duration, not merely delay pause/resume.
func TestRunCommand_JobKindNeverRunsInline(t *testing.T) {
	f := newJobHub(t)
	release := make(chan struct{})
	entered := make(chan struct{})
	a := newJobRunner(t, f, map[string]jobHandler{
		hostjobs.KindDatasetExportRRD: func(ctx context.Context, _ *Runner, _ HostCommand, _ *JobRun) (map[string]any, error) {
			close(entered)
			<-release
			return map[string]any{"ok": true}, nil
		},
	})

	returned := make(chan struct{})
	go func() {
		a.runCommand(context.Background(), jobCmd("c1"))
		close(returned)
	}()

	// runCommand must return while the handler is still blocked.
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("runCommand did not return while the job was still running: the job ran inline")
	}

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("job handler never started")
	}

	// And it must not have reported an outcome yet — the job owns that.
	if p, ok := f.terminalFor("c1"); ok {
		t.Fatalf("outcome patched before the job finished: %+v", p)
	}

	close(release)
	a.jobs.wait()

	p, ok := f.terminalFor("c1")
	if !ok {
		t.Fatal("no outcome patch after the job finished")
	}
	if p.Status != "done" {
		t.Fatalf("status = %q, want done (error %q)", p.Status, p.Error)
	}
	if !strings.Contains(string(p.Result), `"ok":true`) {
		t.Fatalf("result_json = %s, want the handler's map", p.Result)
	}
}

// TestJobExecutor_TwoSubmitsNeverRunConcurrently pins the single-flight guard:
// one running job per host, the rest FIFO behind it. A .rrd export decodes every
// frame of an episode; two at once on one box is how a host runs out of memory.
func TestJobExecutor_TwoSubmitsNeverRunConcurrently(t *testing.T) {
	f := newJobHub(t)

	var (
		live    atomic.Int32
		maxLive atomic.Int32
		mu      sync.Mutex
		order   []string
	)
	gate := make(chan struct{})
	a := newJobRunner(t, f, map[string]jobHandler{
		hostjobs.KindDatasetExportRRD: func(ctx context.Context, _ *Runner, cmd HostCommand, _ *JobRun) (map[string]any, error) {
			n := live.Add(1)
			for {
				old := maxLive.Load()
				if n <= old || maxLive.CompareAndSwap(old, n) {
					break
				}
			}
			mu.Lock()
			order = append(order, cmd.ID)
			mu.Unlock()
			<-gate
			live.Add(-1)
			return nil, nil
		},
	})

	ctx := context.Background()
	a.jobs.submit(ctx, jobCmd("first"))
	a.jobs.submit(ctx, jobCmd("second"))
	a.jobs.submit(ctx, jobCmd("third"))

	// Give a broken implementation time to start all three before we unblock.
	time.Sleep(150 * time.Millisecond)
	if got := live.Load(); got != 1 {
		t.Fatalf("%d jobs running at once, want 1", got)
	}

	close(gate)
	a.jobs.wait()

	if got := maxLive.Load(); got != 1 {
		t.Fatalf("peak concurrency %d, want 1", got)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"first", "second", "third"}
	if len(order) != len(want) {
		t.Fatalf("ran %v, want all of %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("ran in order %v, want FIFO %v", order, want)
		}
	}
}

// TestReconcileJobsAtStartup_FailsDeliveredJobRows pins the restart semantics.
// The executor's in-memory state dies with the process, so nothing will ever
// speak for a row the hub still thinks is running.
func TestReconcileJobsAtStartup_FailsDeliveredJobRows(t *testing.T) {
	f := newJobHub(t)
	f.delivered = []HostCommand{
		jobCmd("lost-job"),
		// An inline kind that also sits `delivered` while it runs. It must be
		// left alone: teleport packs run long inside the poll tick and are not
		// this executor's to declare dead.
		{ID: "teleport", HostID: "host1", Kind: CmdSessionHandoffPack,
			Args: json.RawMessage(`{}`), Status: "delivered"},
	}
	a := newJobRunner(t, f, nil)

	a.reconcileJobsAtStartup(context.Background())

	p, ok := f.terminalFor("lost-job")
	if !ok {
		t.Fatal("the delivered job row was not failed")
	}
	if p.Status != "failed" || !strings.Contains(p.Error, "host-runner restarted") {
		t.Fatalf("patch = %+v, want failed / host-runner restarted", p)
	}
	if _, ok := f.terminalFor("teleport"); ok {
		t.Fatal("an inline teleport command was swept as a lost job")
	}
}

// ---------------------------------------------------------------------------
// cancellation
// ---------------------------------------------------------------------------

func TestCancelJob_RunningJobReportsCancelled(t *testing.T) {
	f := newJobHub(t)
	entered := make(chan struct{})
	a := newJobRunner(t, f, map[string]jobHandler{
		hostjobs.KindDatasetExportRRD: func(ctx context.Context, _ *Runner, _ HostCommand, _ *JobRun) (map[string]any, error) {
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})

	a.jobs.submit(context.Background(), jobCmd("running"))
	<-entered

	cancelCmd := HostCommand{
		ID: "cancel-1", HostID: "host1", Kind: hostjobs.KindCancel,
		Args: json.RawMessage(`{"command_id":"running"}`), Status: "delivered",
	}
	a.runCommand(context.Background(), cancelCmd)
	a.jobs.wait()

	p, ok := f.terminalFor("running")
	if !ok {
		t.Fatal("cancelled job never reported")
	}
	if p.Status != "failed" || p.Error != "cancelled" {
		t.Fatalf("patch = %+v, want failed / cancelled", p)
	}
}

// A queued job has no goroutine of its own, so the inline cancel handler is the
// only thing that can ever speak for it. Losing that leaves the row `delivered`
// until the hub's stale sweep guesses at it minutes later.
func TestCancelJob_QueuedJobIsReportedByTheCancelHandler(t *testing.T) {
	f := newJobHub(t)
	gate := make(chan struct{})
	started := make(chan string, 4)
	a := newJobRunner(t, f, map[string]jobHandler{
		hostjobs.KindDatasetExportRRD: func(ctx context.Context, _ *Runner, cmd HostCommand, _ *JobRun) (map[string]any, error) {
			started <- cmd.ID
			<-gate
			return nil, nil
		},
	})

	ctx := context.Background()
	a.jobs.submit(ctx, jobCmd("running"))
	<-started
	a.jobs.submit(ctx, jobCmd("queued"))

	a.runCommand(ctx, HostCommand{
		ID: "cancel-1", HostID: "host1", Kind: hostjobs.KindCancel,
		Args: json.RawMessage(`{"command_id":"queued"}`), Status: "delivered",
	})

	p, ok := f.terminalFor("queued")
	if !ok {
		t.Fatal("cancelled queued job never reported")
	}
	if p.Status != "failed" || p.Error != "cancelled" {
		t.Fatalf("patch = %+v, want failed / cancelled", p)
	}

	close(gate)
	a.jobs.wait()

	// The cancelled job must never have run, and the running one must be
	// untouched by the cancel.
	close(started)
	for id := range started {
		if id == "queued" {
			t.Fatal("a cancelled queued job ran anyway")
		}
	}
	if p, _ := f.terminalFor("running"); p.Status != "done" {
		t.Fatalf("running job = %+v, want done", p)
	}
}

// Cancelling something that already finished is a no-op success — idempotent,
// like pause on a dead pane (ADR-058 §3).
func TestCancelJob_UnknownIDIsNoOpSuccess(t *testing.T) {
	f := newJobHub(t)
	a := newJobRunner(t, f, nil)
	err := a.cancelJob(context.Background(), HostCommand{
		ID: "cancel-1", Kind: hostjobs.KindCancel,
		Args: json.RawMessage(`{"command_id":"never-existed"}`),
	})
	if err != nil {
		t.Fatalf("cancel of an unknown job errored: %v", err)
	}
	if n := f.count(); n != 0 {
		t.Fatalf("%d patches sent, want 0", n)
	}
}

func TestCancelJob_MissingCommandIDIsAnError(t *testing.T) {
	f := newJobHub(t)
	a := newJobRunner(t, f, nil)
	if err := a.cancelJob(context.Background(), HostCommand{
		Kind: hostjobs.KindCancel, Args: json.RawMessage(`{}`),
	}); err == nil {
		t.Fatal("job_cancel with no command_id was accepted")
	}
}

// ---------------------------------------------------------------------------
// the ways a job could be lost or doubled
// ---------------------------------------------------------------------------

// The hub's pending→delivered flip is best-effort and logs-and-continues on
// failure, so the same row genuinely can be listed twice. Two goroutines
// exporting one command id would race two terminal patches.
func TestJobExecutor_DuplicateDeliveryRunsOnce(t *testing.T) {
	f := newJobHub(t)
	var runs atomic.Int32
	gate := make(chan struct{})
	a := newJobRunner(t, f, map[string]jobHandler{
		hostjobs.KindDatasetExportRRD: func(ctx context.Context, _ *Runner, _ HostCommand, _ *JobRun) (map[string]any, error) {
			runs.Add(1)
			<-gate
			return nil, nil
		},
	})

	ctx := context.Background()
	a.jobs.submit(ctx, jobCmd("dup"))
	a.jobs.submit(ctx, jobCmd("dup"))
	time.Sleep(100 * time.Millisecond)

	// Asserting on the run count alone is not enough, and finding that out is
	// the reason this block exists: a duplicate that reached the map would
	// overwrite the live slot under the same key, and release would then find
	// nothing to promote — so the job still runs exactly once, by accident,
	// while the first slot's cancel func is leaked and its context never
	// cancelled. The queue depth is what actually distinguishes "rejected the
	// duplicate" from "swallowed it".
	a.jobs.mu.Lock()
	depth, slots := len(a.jobs.queue), len(a.jobs.slots)
	a.jobs.mu.Unlock()
	if depth != 0 || slots != 1 {
		t.Fatalf("duplicate delivery left queue depth %d and %d slots; want 0 and 1", depth, slots)
	}

	close(gate)
	a.jobs.wait()

	if got := runs.Load(); got != 1 {
		t.Fatalf("the same command ran %d times, want 1", got)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	terminal := 0
	for _, p := range f.patches {
		if p.ID == "dup" && p.Patch.Status != "" {
			terminal++
		}
	}
	if terminal != 1 {
		t.Fatalf("%d terminal patches for one command, want 1", terminal)
	}
}

// A kind may be allowlisted before its handler exists (dataset_export_rrd is,
// until #162). Accepting the work and going quiet would be the worst outcome.
func TestJobExecutor_AllowlistedKindWithNoHandlerFailsTyped(t *testing.T) {
	f := newJobHub(t)
	a := newJobRunner(t, f, map[string]jobHandler{})

	a.jobs.submit(context.Background(), jobCmd("nohandler"))
	a.jobs.wait()

	p, ok := f.terminalFor("nohandler")
	if !ok {
		t.Fatal("no outcome for an unregistered kind")
	}
	if p.Status != "failed" || !strings.Contains(p.Error, "not supported by this host-runner build") {
		t.Fatalf("patch = %+v, want a typed unsupported-kind failure", p)
	}
}

// A job runs on its own goroutine: an unrecovered panic there takes the whole
// host-runner down, and every agent on the box loses its supervisor.
func TestJobExecutor_HandlerPanicFailsTheJobNotTheProcess(t *testing.T) {
	f := newJobHub(t)
	a := newJobRunner(t, f, map[string]jobHandler{
		hostjobs.KindDatasetExportRRD: func(ctx context.Context, _ *Runner, _ HostCommand, _ *JobRun) (map[string]any, error) {
			panic("malformed parquet")
		},
	})

	a.jobs.submit(context.Background(), jobCmd("boom"))
	a.jobs.wait()

	p, ok := f.terminalFor("boom")
	if !ok {
		t.Fatal("a panicking job reported nothing")
	}
	if p.Status != "failed" || !strings.Contains(p.Error, "malformed parquet") {
		t.Fatalf("patch = %+v, want failed carrying the panic value", p)
	}

	// The slot must be free again, or one panic wedges the host forever.
	done := make(chan struct{})
	a.jobKinds = map[string]jobHandler{
		hostjobs.KindDatasetExportRRD: func(ctx context.Context, _ *Runner, _ HostCommand, _ *JobRun) (map[string]any, error) {
			close(done)
			return nil, nil
		},
	}
	a.jobs.submit(context.Background(), jobCmd("after"))
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the slot was never released after a panic")
	}
	a.jobs.wait()
}

// The list endpoint delivers up to 50 rows per tick, so an unbounded queue lets
// trivial input pile up hours of work in memory.
func TestJobExecutor_QueueFullIsRejectedTyped(t *testing.T) {
	f := newJobHub(t)
	gate := make(chan struct{})
	var runs atomic.Int32
	a := newJobRunner(t, f, map[string]jobHandler{
		hostjobs.KindDatasetExportRRD: func(ctx context.Context, _ *Runner, _ HostCommand, _ *JobRun) (map[string]any, error) {
			runs.Add(1)
			<-gate
			return nil, nil
		},
	})

	ctx := context.Background()
	a.jobs.submit(ctx, jobCmd("running"))
	for i := 0; i < jobQueueMax; i++ {
		a.jobs.submit(ctx, jobCmd("q"+string(rune('a'+i))))
	}
	// One past the bound.
	a.jobs.submit(ctx, jobCmd("overflow"))

	p, ok := f.terminalFor("overflow")
	if !ok {
		t.Fatal("the over-bound submission was neither run nor refused")
	}
	if p.Status != "failed" || !strings.Contains(p.Error, "queue is full") {
		t.Fatalf("patch = %+v, want a typed queue-full failure", p)
	}

	close(gate)
	a.jobs.wait()
	if got := runs.Load(); got != int32(jobQueueMax+1) {
		t.Fatalf("%d jobs ran, want the running one plus %d queued", got, jobQueueMax)
	}
}

// ---------------------------------------------------------------------------
// progress + the job's directory
// ---------------------------------------------------------------------------

// A progress patch carries no status: that is what tells the hub to leave the
// lifecycle alone and treat it as a heartbeat.
func TestJobExecutor_ProgressPatchesCarryNoStatus(t *testing.T) {
	f := newJobHub(t)
	a := newJobRunner(t, f, map[string]jobHandler{
		hostjobs.KindDatasetExportRRD: func(ctx context.Context, _ *Runner, _ HostCommand, run *JobRun) (map[string]any, error) {
			run.Report("decoding", 3, 10)
			// The pump coalesces; wait for one interval to elapse.
			time.Sleep(jobProgressInterval + 500*time.Millisecond)
			return nil, nil
		},
	})

	a.jobs.submit(context.Background(), jobCmd("prog"))
	a.jobs.wait()

	f.mu.Lock()
	defer f.mu.Unlock()
	var sawProgress bool
	for _, p := range f.patches {
		if p.ID != "prog" || len(p.Patch.Progress) == 0 {
			continue
		}
		sawProgress = true
		if p.Patch.Status != "" {
			t.Fatalf("progress patch carried status %q; the hub would treat it as terminal", p.Patch.Status)
		}
		if !strings.Contains(string(p.Patch.Progress), `"phase":"decoding"`) {
			t.Fatalf("progress = %s, want the reported phase", p.Patch.Progress)
		}
	}
	if !sawProgress {
		t.Fatal("no progress heartbeat was sent")
	}
}

// A job is handed exactly one writable location, under the cache root.
func TestJobExecutor_HandlerGetsAJobDirUnderTheCacheRoot(t *testing.T) {
	f := newJobHub(t)
	var gotDir string
	a := newJobRunner(t, f, map[string]jobHandler{
		hostjobs.KindDatasetExportRRD: func(ctx context.Context, _ *Runner, _ HostCommand, run *JobRun) (map[string]any, error) {
			gotDir = run.Dir
			return nil, os.WriteFile(filepath.Join(run.Dir, "out.rrd"), []byte("x"), 0o600)
		},
	})

	a.jobs.submit(context.Background(), jobCmd("dirs"))
	a.jobs.wait()

	want := filepath.Join(a.jobcache.Root, hostjobs.KindDatasetExportRRD, "dirs")
	if gotDir != want {
		t.Fatalf("job dir = %q, want %q", gotDir, want)
	}
	if _, err := os.Stat(filepath.Join(want, "out.rrd")); err != nil {
		t.Fatalf("job could not write to its own dir: %v", err)
	}
}
