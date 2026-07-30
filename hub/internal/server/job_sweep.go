package server

import (
	"context"
	"time"

	"github.com/termipod/hub/internal/hostjobs"
)

// job_sweep.go — the hub half of ADR-058 §3's restart semantics.
//
// A host-runner fails its own `delivered` job rows when it restarts, which
// covers the common case: the process died, the machine did not. It cannot
// cover the other case. A host that loses power, drops off the network for
// good, or has its runner killed and never restarted leaves job rows sitting
// `delivered` forever, and a caller polling one waits forever with it.
//
// So the hub applies its own liveness rule: a job that has not heartbeat within
// JobStaleThreshold is failed. The signal is progress_at — stamped by the hub
// when a heartbeat arrives, never read out of the host's payload, so a skewed
// clock on one box cannot decide whether its jobs are declared dead. Before the
// first heartbeat there is no progress_at, so delivered_at stands in.
//
// Scoped to detached kinds only (hostjobs.Kinds). The teleport kinds also sit
// `delivered` for minutes while they run, but they run inline and never
// heartbeat, so sweeping them would kill healthy work.

// JobStaleThreshold is how long a detached job may go without a heartbeat
// before the hub declares its host gone. Jobs heartbeat every 30s
// (jobHeartbeatInterval in the host-runner), so this is ~10 consecutive misses
// — wide enough to ride out a slow patch or a brief network partition, narrow
// enough that a dead host does not strand a caller for long.
const JobStaleThreshold = 5 * time.Minute

// jobSweepInterval is how often the sweep runs. Worst-case time to notice is
// JobStaleThreshold + jobSweepInterval.
const jobSweepInterval = time.Minute

// runJobSweep loops until ctx is cancelled.
func (s *Server) runJobSweep(ctx context.Context) {
	t := time.NewTicker(jobSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sweepStaleJobsOnce(ctx)
		}
	}
}

// sweepStaleJobsOnce fails every detached job row whose heartbeat has gone
// stale. Safe to run concurrently with the PATCH handler: a heartbeat that
// lands in the same window simply moves progress_at forward and the row is no
// longer a candidate, and a terminal patch takes the row out of `delivered`.
//
// created_at backs the COALESCE because SQL's NULL comparison would silently
// exempt a row rather than sweep it: a `delivered` row is always given a
// delivered_at, but "always" plus a NULL-propagating predicate is how a row
// escapes a sweep forever, and created_at is NOT NULL.
func (s *Server) sweepStaleJobsOnce(ctx context.Context) {
	kinds := hostjobs.Kinds()
	if len(kinds) == 0 {
		return
	}
	// Same padded layout the host sweep uses, so a string comparison against
	// the RFC3339Nano values NowUTC writes orders correctly at the resolution
	// that matters here (the threshold is minutes; the layouts differ only in
	// trailing fractional zeros).
	cutoff := time.Now().UTC().Add(-JobStaleThreshold).
		Format("2006-01-02T15:04:05.000000000Z07:00")

	args := make([]any, 0, len(kinds)+2)
	args = append(args, NowUTC())
	for _, k := range kinds {
		args = append(args, k)
	}
	args = append(args, cutoff)

	res, err := s.writeDB.ExecContext(ctx, `
		UPDATE host_commands
		   SET status = 'failed',
		       error = 'host stopped reporting',
		       completed_at = ?
		 WHERE status = 'delivered'
		   AND kind IN (?`+strings_repeat(",?", len(kinds)-1)+`)
		   AND COALESCE(progress_at, delivered_at, created_at) < ?`, args...)
	if err != nil {
		s.log.Warn("job sweep failed", "err", err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.log.Info("job sweep failed stale host jobs", "count", n,
			"threshold", JobStaleThreshold)
	}
}
