package server

import (
	"context"
	"time"
)

// HostStaleThreshold is how long a host can go without heartbeats before
// we flip it to 'offline'. Host-runners heartbeat every ~10s, so ~9 misses
// is a comfortable margin that still surfaces a genuinely dead runner
// within ~90s.
const HostStaleThreshold = 90 * time.Second

// hostSweepInterval is how often the sweep runs. 30s is a balance between
// promptness and DB chatter — the UI will see an offline flip within
// HostStaleThreshold + hostSweepInterval in the worst case (~120s).
const hostSweepInterval = 30 * time.Second

// runHostSweep loops until ctx is cancelled, marking hosts offline whose
// last_seen_at has fallen past HostStaleThreshold. Safe to run concurrently
// with the heartbeat handler — a fresh heartbeat within the same window
// will simply flip it back to online on the next POST.
func (s *Server) runHostSweep(ctx context.Context) {
	t := time.NewTicker(hostSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sweepHostsOnce(ctx)
		}
	}
}

func (s *Server) sweepHostsOnce(ctx context.Context) {
	cutoff := time.Now().UTC().Add(-HostStaleThreshold).
		Format("2006-01-02T15:04:05.000000000Z07:00")
	res, err := s.db.ExecContext(ctx, `
		UPDATE hosts SET status='offline'
		WHERE status='online'
		  AND last_seen_at IS NOT NULL
		  AND last_seen_at < ?`, cutoff)
	if err != nil {
		s.log.Warn("host sweep failed", "err", err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.log.Info("host sweep marked offline", "count", n)
	}
}
