package hostrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/termipod/hub/internal/hostjobs"
)

// jobs.go — the detached job executor (ADR-058 §2).
//
// Why detached at all: tickCommands runs runCommand *inline*, and it does so on
// the host-runner's single main-loop goroutine — the same one that services
// tickPoll (spawn launches), tickReconcile (status transitions) and tickIdle.
// A multi-minute LeRobot export run inline would therefore not merely delay
// pause/resume, it would stop agents from being spawned at all for its whole
// duration. The teleport kinds already run long inline; ADR-057 accepted that
// for a rare, deliberate, user-initiated operation. Exports arrive at browsing
// frequency, which is why this exists.
//
// What stays the same: the transport. Submission, delivery, status, result and
// cancellation all ride the existing pull-only `host_commands` queue, so a
// host-runner behind NAT needs no hub-initiated connection, exactly as today.
//
// What is new is only this file's mechanics — a goroutine per job behind a
// single-flight guard, a progress heartbeat, cancellation, and startup
// reconciliation of rows a dead process left behind.

const (
	// jobMaxDuration is the backstop ceiling on one job's execution, enforced
	// by context and measured from the moment the job *starts* (never from
	// submission — a job that waited an hour in the queue still gets its full
	// budget). A kind may impose something tighter; nothing may exceed this.
	//
	// It exists because the guard below is single-flight: a handler that hangs
	// forever would hold the only slot forever and silently starve every future
	// job on the host.
	jobMaxDuration = 30 * time.Minute

	// jobProgressInterval is how often a running job's progress is pushed to
	// the hub when the handler has reported something new. Handlers may call
	// Report as often as they like — it only touches memory.
	jobProgressInterval = 5 * time.Second

	// jobHeartbeatInterval forces a push even when the handler has reported
	// nothing new, because the same patch is the liveness heartbeat the hub's
	// stale-job sweep reads (ADR-058 §3). Without this floor a legitimately
	// silent phase — one long ffmpeg call — would be swept as a dead host.
	// Must stay comfortably under the hub's staleness threshold.
	jobHeartbeatInterval = 30 * time.Second

	// jobPatchTimeout bounds the terminal PATCH that reports a job's outcome.
	jobPatchTimeout = 30 * time.Second

	// jobQueueMax bounds the in-memory backlog. The list endpoint delivers up
	// to 50 rows in one tick and a director can click "export" as fast as the
	// UI allows, so an unbounded queue would let trivial input pile up hours of
	// work; past this depth a submission is failed with a typed error instead.
	jobQueueMax = 16
)

// jobHandler runs one detached job to completion. The returned map lands in the
// command's result_json; a returned error fails the command with that text.
//
// A handler must write only inside run.Dir, and must honour ctx: it carries
// both the ceiling above and operator cancellation.
type jobHandler func(ctx context.Context, a *Runner, cmd HostCommand, run *JobRun) (map[string]any, error)

// defaultJobHandlers is the registry the executor dispatches through. A kind in
// the hostjobs allowlist with no entry here is failed with a typed
// "not supported by this host-runner build" error rather than silently
// accepted — see invoke.
//
// Empty today: `dataset_export_rrd`'s handler is task #162. This is the seam it
// registers into.
func defaultJobHandlers() map[string]jobHandler {
	return map[string]jobHandler{}
}

// jobProgress is the coarse shape a job reports. Deliberately small: this is a
// heartbeat with a progress bar attached, not a log stream.
type jobProgress struct {
	Phase string `json:"phase"`
	Done  int64  `json:"done,omitempty"`
	Total int64  `json:"total,omitempty"`
}

// JobRun is the executor's per-job facade handed to a handler: where it may
// write, and how it reports progress.
type JobRun struct {
	// Dir is this job's own directory under the jobcache, created before the
	// handler runs. It is the only location a job may write to.
	Dir string

	mu    sync.Mutex
	prog  jobProgress
	dirty bool
}

// Report records coarse progress. It never performs I/O and never blocks on
// the hub, so a handler may call it per frame; the executor's pump owns the
// PATCH and coalesces to the intervals above.
func (r *JobRun) Report(phase string, done, total int64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.prog = jobProgress{Phase: phase, Done: done, Total: total}
	r.dirty = true
	r.mu.Unlock()
}

// take returns the current progress and whether it changed since the last take.
func (r *JobRun) take() (jobProgress, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, d := r.prog, r.dirty
	r.dirty = false
	return p, d
}

// jobSlot is one accepted job: queued or running.
type jobSlot struct {
	cmd HostCommand
	run *JobRun

	// ctx is the runner-lifetime context this job was accepted under; the
	// per-run deadline is derived from it when the job actually starts, so
	// queue time is not charged against jobMaxDuration.
	ctx    context.Context
	cancel context.CancelFunc

	startedAt time.Time // zero while queued

	// cancelled records that an operator asked for this, which is reported
	// differently from hitting the ceiling or losing the process.
	cancelled bool
}

// jobExecutor runs job-kind commands off the poll tick, one at a time per host.
type jobExecutor struct {
	a *Runner

	mu      sync.Mutex
	slots   map[string]*jobSlot // command id → accepted job (running or queued)
	queue   []string            // command ids awaiting the slot, oldest first
	running string              // command id holding the slot; "" when free

	// wg tracks job goroutines so a test (or a graceful stop) can wait for
	// them to finish reporting.
	wg sync.WaitGroup
}

func newJobExecutor(a *Runner) *jobExecutor {
	return &jobExecutor{a: a, slots: map[string]*jobSlot{}}
}

// submit accepts a delivered job command and returns immediately.
//
// ctx must be the runner-lifetime context. tickCommands is called with Start's
// ctx, which is exactly that; a per-tick context would cancel the job the
// moment the tick that accepted it returned.
func (e *jobExecutor) submit(ctx context.Context, cmd HostCommand) {
	e.mu.Lock()

	if _, dup := e.slots[cmd.ID]; dup {
		e.mu.Unlock()
		// Not defensive padding: the hub's pending→delivered flip is
		// best-effort and logs-and-continues on failure
		// (handlers_commands.go), so the same row genuinely can be listed
		// twice. For an idempotent inline kind a re-run is harmless; for a job
		// it would mean two goroutines exporting the same command id and two
		// terminal patches racing.
		e.a.Log.Debug("host job already accepted; ignoring duplicate delivery",
			"id", cmd.ID, "kind", cmd.Kind)
		return
	}

	if e.running != "" && len(e.queue) >= jobQueueMax {
		e.mu.Unlock()
		e.failNow(cmd, fmt.Errorf("host job queue is full (%d waiting); retry when the host is less busy", jobQueueMax))
		return
	}

	jctx, cancel := context.WithCancel(ctx)
	slot := &jobSlot{cmd: cmd, run: &JobRun{}, ctx: jctx, cancel: cancel}
	e.slots[cmd.ID] = slot

	if e.running == "" {
		e.running = cmd.ID
		e.startLocked(slot)
		e.mu.Unlock()
		return
	}
	e.queue = append(e.queue, cmd.ID)
	depth := len(e.queue)
	e.mu.Unlock()
	e.a.Log.Info("host job queued behind a running job",
		"id", cmd.ID, "kind", cmd.Kind, "queue_depth", depth)
}

// startLocked launches a slot's goroutine. Caller holds e.mu.
func (e *jobExecutor) startLocked(slot *jobSlot) {
	slot.startedAt = time.Now()
	e.wg.Add(1)
	go e.run(slot)
}

// run executes one job and reports its outcome.
func (e *jobExecutor) run(slot *jobSlot) {
	defer e.wg.Done()
	defer slot.cancel()

	e.a.Log.Info("host job started", "id", slot.cmd.ID, "kind", slot.cmd.Kind)

	rctx, rcancel := context.WithTimeout(slot.ctx, jobMaxDuration)
	defer rcancel()

	stop := make(chan struct{})
	var pump sync.WaitGroup
	pump.Add(1)
	go func() {
		defer pump.Done()
		e.pumpProgress(rctx, slot, stop)
	}()

	result, err := e.invoke(rctx, slot)

	close(stop)
	pump.Wait()

	e.finish(slot, result, err)
	e.release(slot.cmd.ID)
}

// invoke resolves the handler, prepares the job's directory, and runs it.
func (e *jobExecutor) invoke(ctx context.Context, slot *jobSlot) (result map[string]any, err error) {
	defer func() {
		if p := recover(); p != nil {
			// A job runs on its own goroutine, so an unrecovered panic here
			// takes the whole host-runner process down with it: every agent on
			// the box loses its supervisor because one export met a malformed
			// parquet file. Fail the job instead, loudly.
			err = fmt.Errorf("job panicked: %v", p)
			e.a.Log.Error("host job panicked",
				"id", slot.cmd.ID, "kind", slot.cmd.Kind,
				"panic", p, "stack", string(debug.Stack()))
		}
	}()

	h, ok := e.a.jobKinds[slot.cmd.Kind]
	if !ok || h == nil {
		// Allowlisted but unregistered: the hub asked for a kind this build
		// does not carry. Fail typed and actionable rather than half-running
		// something (the #394 soft-degrade shape).
		return nil, fmt.Errorf("job kind %q is not supported by this host-runner build", slot.cmd.Kind)
	}

	dir, err := e.a.jobcache.jobDir(slot.cmd.Kind, slot.cmd.ID)
	if err != nil {
		return nil, err
	}
	slot.run.Dir = dir

	// Make room before the job writes, not after: eviction cannot help a disk
	// that is already full by the time the artifact exists.
	e.a.jobcache.evict()

	return h(ctx, e.a, slot.cmd, slot.run)
}

// pumpProgress pushes progress to the hub until stop closes: on change at
// jobProgressInterval, and unconditionally every jobHeartbeatInterval because
// the same patch is the liveness signal.
func (e *jobExecutor) pumpProgress(ctx context.Context, slot *jobSlot, stop <-chan struct{}) {
	tick := time.NewTicker(jobProgressInterval)
	defer tick.Stop()
	last := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-tick.C:
			p, changed := slot.run.take()
			if !changed && time.Since(last) < jobHeartbeatInterval {
				continue
			}
			if p.Phase == "" {
				p.Phase = "running"
			}
			body, err := json.Marshal(p)
			if err != nil {
				continue
			}
			// A progress patch omits status: the hub reads that as a heartbeat
			// and leaves the lifecycle alone.
			if err := e.a.Client.PatchCommand(ctx, slot.cmd.ID, CommandPatch{Progress: body}); err != nil {
				e.a.Log.Debug("host job progress patch failed", "id", slot.cmd.ID, "err", err)
				continue
			}
			last = time.Now()
		}
	}
}

// finish reports the terminal outcome of a job.
func (e *jobExecutor) finish(slot *jobSlot, result map[string]any, err error) {
	// The terminal PATCH deliberately does not ride the job's own context.
	// Cancellation and the ceiling both cancel that context, and *why a job
	// ended* is precisely what a caller polling the row needs — reporting it
	// over a dead context would leave the row `delivered` until the hub's
	// stale sweep guessed at it.
	ctx, cancel := context.WithTimeout(context.Background(), jobPatchTimeout)
	defer cancel()

	e.mu.Lock()
	cancelled := slot.cancelled
	e.mu.Unlock()

	patch := CommandPatch{Status: "done"}
	switch {
	case err == nil:
		// A job that completed keeps its success even if a cancel arrived
		// while it was finishing: the artifact exists, and "cancelling a job
		// that already finished is a no-op success" (ADR-058 §3).
		if result != nil {
			b, mErr := json.Marshal(result)
			if mErr != nil {
				patch.Status = "failed"
				patch.Error = fmt.Sprintf("job result not serialisable: %v", mErr)
			} else {
				patch.Result = b
			}
		}
	case cancelled:
		patch.Status, patch.Error = "failed", "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		patch.Status = "failed"
		patch.Error = fmt.Sprintf("exceeded the %s host job ceiling", jobMaxDuration)
	default:
		patch.Status, patch.Error = "failed", err.Error()
	}

	if patch.Status == "failed" {
		e.a.Log.Warn("host job failed",
			"id", slot.cmd.ID, "kind", slot.cmd.Kind, "err", patch.Error,
			"elapsed", time.Since(slot.startedAt).Round(time.Second))
	} else {
		e.a.Log.Info("host job done",
			"id", slot.cmd.ID, "kind", slot.cmd.Kind,
			"elapsed", time.Since(slot.startedAt).Round(time.Second))
	}
	if perr := e.a.Client.PatchCommand(ctx, slot.cmd.ID, patch); perr != nil {
		// The row stays `delivered` and the hub's stale-job sweep will fail it
		// after its threshold — degraded, not lost.
		e.a.Log.Warn("host job patch failed", "id", slot.cmd.ID, "err", perr)
	}
}

// failNow reports a submission that was refused before it ever ran.
func (e *jobExecutor) failNow(cmd HostCommand, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), jobPatchTimeout)
	defer cancel()
	e.a.Log.Warn("host job rejected", "id", cmd.ID, "kind", cmd.Kind, "err", err)
	if perr := e.a.Client.PatchCommand(ctx, cmd.ID, CommandPatch{
		Status: "failed", Error: err.Error(),
	}); perr != nil {
		e.a.Log.Warn("host job reject patch failed", "id", cmd.ID, "err", perr)
	}
}

// release frees the slot and starts the next queued job, if any.
func (e *jobExecutor) release(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.slots, id)
	if e.running == id {
		e.running = ""
	}
	for e.running == "" && len(e.queue) > 0 {
		next := e.queue[0]
		e.queue = e.queue[1:]
		slot, ok := e.slots[next]
		if !ok {
			continue // cancelled while queued
		}
		e.running = next
		e.startLocked(slot)
	}
}

// cancel stops a running or queued job. It reports whether the job was still
// queued, which the caller needs because a queued job has no goroutine of its
// own to report its outcome.
//
// An unknown command id is a no-op — a job that already finished, or one this
// process never accepted, is nothing to stop.
func (e *jobExecutor) cancel(id string) (wasQueued bool) {
	e.mu.Lock()
	slot, ok := e.slots[id]
	if !ok {
		e.mu.Unlock()
		return false
	}
	slot.cancelled = true
	wasQueued = e.running != id
	if wasQueued {
		e.dequeueLocked(id)
		delete(e.slots, id)
	}
	cancel := slot.cancel
	e.mu.Unlock()

	cancel()
	return wasQueued
}

func (e *jobExecutor) dequeueLocked(id string) {
	for i, q := range e.queue {
		if q == id {
			e.queue = append(e.queue[:i], e.queue[i+1:]...)
			return
		}
	}
}

// wait blocks until every accepted job has reported. Test-only helper; the
// production path never needs to join these goroutines because the runner's
// context cancellation is what stops them.
func (e *jobExecutor) wait() { e.wg.Wait() }

// ---------------------------------------------------------------------------
// The job_cancel command kind, and startup reconciliation.
// ---------------------------------------------------------------------------

type jobCancelArgs struct {
	// CommandID is the id of the job command to stop.
	CommandID string `json:"command_id"`
}

// cancelJob implements the job_cancel kind. It runs INLINE (see
// hostjobs.KindCancel): a cancel queued behind the job it is cancelling would
// never arrive.
func (a *Runner) cancelJob(ctx context.Context, cmd HostCommand) error {
	var args jobCancelArgs
	if err := json.Unmarshal(cmd.Args, &args); err != nil {
		return fmt.Errorf("job_cancel: invalid args: %w", err)
	}
	if args.CommandID == "" {
		return fmt.Errorf("job_cancel: command_id required")
	}
	if a.jobs == nil {
		return nil
	}
	if !a.jobs.cancel(args.CommandID) {
		// Either running (its own goroutine will report `cancelled`) or
		// unknown to this process. Both are a no-op success here.
		return nil
	}
	// It never started, so nothing else will ever speak for it.
	if err := a.Client.PatchCommand(ctx, args.CommandID, CommandPatch{
		Status: "failed", Error: "cancelled",
	}); err != nil {
		return fmt.Errorf("job_cancel: patch target %s: %w", args.CommandID, err)
	}
	return nil
}

// reconcileJobsAtStartup fails this host's `delivered` job rows (ADR-058 §3).
//
// The executor's in-memory state died with the previous process, so a job row
// the hub still believes is running is unreachable: nothing will ever patch it,
// and nothing can cancel it. Say so instead of leaving it `delivered` forever.
// Callers resubmit — an export is cheap to redo relative to what resume would
// cost, which is why v1 has no job persistence.
//
// This MUST run before the first tickCommands. It cannot distinguish a row this
// process just accepted from one the previous process abandoned, so running it
// afterwards would fail the jobs that had only just started.
//
// The list endpoint caps a page at 50 rows, and this deliberately does not
// paginate: beyond 50 abandoned jobs on one host the hub's own stale-job sweep
// finishes the work within JobStaleThreshold. Naming the cap here so it is not
// mistaken for full coverage.
func (a *Runner) reconcileJobsAtStartup(ctx context.Context) {
	cmds, err := a.Client.ListCommands(ctx, a.HostID, "delivered")
	if err != nil {
		a.Log.Warn("host job startup reconcile: list delivered commands failed", "err", err)
		return
	}
	for _, c := range cmds {
		// Only detached kinds. The teleport kinds also sit `delivered` while
		// they run, but they run inline inside this process's own poll tick —
		// at startup there are none of ours in flight, and a future migration
		// of those kinds onto this executor should be a deliberate change to
		// the hostjobs allowlist, not a side effect here.
		if !hostjobs.Is(c.Kind) {
			continue
		}
		a.Log.Warn("failing host job lost to a host-runner restart", "id", c.ID, "kind", c.Kind)
		if err := a.Client.PatchCommand(ctx, c.ID, CommandPatch{
			Status: "failed", Error: "lost: host-runner restarted",
		}); err != nil {
			a.Log.Warn("host job startup reconcile: patch failed", "id", c.ID, "err", err)
		}
	}
}
