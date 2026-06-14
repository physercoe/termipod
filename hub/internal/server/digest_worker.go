package server

import (
	"context"
	"os"
	"strconv"
	"sync"
	"time"
)

// Deferred, bounded-staleness digest fold (ADR-038 amendment 2026-06-06;
// lever 7 / step 1 of docs/discussions/hub-store-separation-and-fold-policy.md,
// the rework of the earlier per-event "A"). The ADR-038 digest fold was the
// single heaviest step on the agent-event ingest hot path — ~half the
// per-event cost (hub-scaling §4.3). Moving it off the POST path is the
// throughput lever; doing it WELL is choosing *when* it runs.
//
// The first cut ("A") marked the agent dirty on every event and folded it
// every tick. That deferred the fold but did not SHRINK it: every event still
// caused a digest-blob rewrite, insert and fold share the one writer, and
// under saturation the worker starved (fold debt grew without bound). The fix
// is to fold an agent LESS often, on a bounded-staleness trigger — fold when:
//
//	(a) a turn closes (turn.result)        — the natural boundary; the only
//	                                          point authoritative cost/by_model
//	                                          exist anyway (digest_fold.go).
//	(b) >= N events have accumulated        — caps how stale a long, still-open
//	    since the last fold                   "goal mode" turn's running
//	                                          counters get (event axis).
//	(c) >= tau has elapsed with pending     — caps staleness for a slow turn
//	    events                                (few events, long gaps; time axis).
//
// The fold itself stays watermark-based (folds every event past the digest
// watermark in one tx), so the per-agent count/turn flags are only TRIGGERS —
// no event is ever skipped. Read-repair (ensureAgentDigest -> digestIsStale ->
// backfill) remains the correctness backstop: a lagged or post-restart digest
// is recomputed on read, so a missed trigger is never observable as wrong data.
//
// N (maxEvents) and tau (maxAge) are the tuning knobs the discussion calls out
// for empirical sweeping; defaults sit in the 20-40 events / 300-1000 ms bands
// and are overridable by env so the load harness (and operators) can tune
// without recompiling.
var (
	// digestFoldTick is how often the worker scans the pending set. It bounds
	// how promptly a turn.result is reflected, and must be <= digestFoldMaxAge.
	digestFoldTick = 100 * time.Millisecond
	// digestFoldMaxEvents is N in trigger (b): fold once this many events have
	// accumulated past the last fold for an agent.
	digestFoldMaxEvents = 32
	// digestFoldMaxAge is tau in trigger (c): fold once the oldest unfolded
	// event for an agent is this old.
	digestFoldMaxAge = 750 * time.Millisecond
)

// digestPending is the per-agent fold accounting kept in memory between folds.
// It carries the trigger inputs only; the authoritative state is the digest's
// watermark in the DB.
type digestPending struct {
	team       string
	count      int       // events seen since the agent's last fold
	turnClosed bool      // a turn.result arrived since the last fold
	firstDirty time.Time // when the oldest unfolded event was marked
}

// foldDue reports whether an agent's pending state meets the bounded-staleness
// trigger and should be folded now.
func (p *digestPending) foldDue(now time.Time, maxEvents int, maxAge time.Duration) bool {
	return p.turnClosed ||
		p.count >= maxEvents ||
		now.Sub(p.firstDirty) >= maxAge
}

// markDigestDirty records that an agent has a new event past its digest
// watermark. Called from the ingest hot path — O(1) and lock-cheap. It only
// updates trigger accounting; it does NOT fold. A turn.result raises the
// turnClosed flag so the next worker scan folds the agent promptly.
func (s *Server) markDigestDirty(team, agent, kind string) {
	if agent == "" {
		return
	}
	s.digestDirtyMu.Lock()
	p := s.digestDirty[agent]
	if p == nil {
		p = &digestPending{team: team, firstDirty: time.Now()}
		s.digestDirty[agent] = p
	}
	p.count++
	if kind == "turn.result" {
		p.turnClosed = true
	}
	s.digestDirtyMu.Unlock()
}

// foldTarget is a due agent removed from the pending set, ready to fold.
type foldTarget struct {
	agent string
	team  string
}

// collectFoldable removes and returns the agents whose pending state is due
// per the bounded-staleness trigger, leaving the rest to accumulate. An agent
// re-marked after it is collected simply reappears (count from 1, firstDirty
// reset) — the watermark-based fold still picks up any events that arrived in
// the gap, so nothing is lost.
func (s *Server) collectFoldable(now time.Time, maxEvents int, maxAge time.Duration) []foldTarget {
	s.digestDirtyMu.Lock()
	defer s.digestDirtyMu.Unlock()
	if len(s.digestDirty) == 0 {
		return nil
	}
	var due []foldTarget
	for agent, p := range s.digestDirty {
		if p.foldDue(now, maxEvents, maxAge) {
			due = append(due, foldTarget{agent: agent, team: p.team})
			delete(s.digestDirty, agent)
		}
	}
	return due
}

// runDigestFold is the background fold loop. Started from Serve(); runs until
// ctx is cancelled. Tests don't call Serve(), so the worker is inert there —
// the read-repair path keeps the digest correct on demand. Knobs are read once
// at start so a run is internally consistent; env overrides let the load
// harness sweep N/tau.
func (s *Server) runDigestFold(ctx context.Context) {
	tick := digestFoldTick
	maxEvents := digestFoldMaxEvents
	maxAge := digestFoldMaxAge
	if v, ok := digestEnvDur("HUB_DIGEST_FOLD_TICK_MS"); ok {
		tick = v
	}
	if v, ok := digestEnvInt("HUB_DIGEST_FOLD_MAX_EVENTS"); ok {
		maxEvents = v
	}
	if v, ok := digestEnvDur("HUB_DIGEST_FOLD_MAX_AGE_MS"); ok {
		maxAge = v
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.foldDueByTeam(ctx, s.collectFoldable(now, maxEvents, maxAge))
		}
	}
}

// foldDueByTeam folds the tick's due agents, parallelising ACROSS teams while
// keeping a single team's agents serial. Each team has its own digest.db writer
// (ADR-045 P2), so cross-team folds run on independent write locks and cores —
// the single global worker was the next bottleneck once the stores sharded (fold
// lag climbed with team count under load, hub-scaling §4.6). Agents of one team
// share that team's one writer, so folding them serially costs nothing.
//
// One goroutine per team with due work this tick; the caller's tick waits for
// foldDueByTeam to return before the next scan, so a team is never folded by two
// goroutines at once (and the goroutine count is bounded by the live team
// count). The single-team case — the overwhelmingly common one — folds inline,
// with no goroutine or WaitGroup overhead.
func (s *Server) foldDueByTeam(ctx context.Context, due []foldTarget) {
	if len(due) == 0 {
		return
	}
	byTeam := make(map[string][]string, 4)
	for _, t := range due {
		byTeam[t.team] = append(byTeam[t.team], t.agent)
	}
	if len(byTeam) == 1 {
		for _, t := range due {
			s.foldDirtyAgent(ctx, t.team, t.agent)
		}
		return
	}
	var wg sync.WaitGroup
	for team, agents := range byTeam {
		wg.Add(1)
		go func(team string, agents []string) {
			defer wg.Done()
			for _, agent := range agents {
				s.foldDirtyAgent(ctx, team, agent)
			}
		}(team, agents)
	}
	wg.Wait()
}

// foldDirtyAgent folds every event past the agent's digest watermark in one
// transaction. Off the hot path, so simplicity beats micro-optimization: it
// reuses foldEventIncremental per new event (each sees the prior step's
// in-tx state). A fold error rolls the whole tx back and leaves the watermark
// where it was — a later trigger (or a read-repair) retries.
//
// ADR-045 step 2/P2: the event read and the digest write are decoupled — read
// the events past the watermark from the team's eventsReader, then fold them
// into the digest in its own digest-store tx (the team's digestWriter), with
// no ATTACH. Safe because the digest is idempotent from the watermark: any
// event committed between the read and the write just leaves the watermark
// behind, and the next tick (or a read-repair) folds it.
func (s *Server) foldDirtyAgent(ctx context.Context, team, agent string) {
	// Resolve the team's shard pools up front; the fold worker is a background
	// goroutine, so an unroutable team is logged and skipped (the next tick or a
	// read-repair retries).
	dr, err := s.digestReader(team)
	if err != nil {
		s.log.Warn("digest worker: digest store", "agent", agent, "team", team, "err", err)
		return
	}
	er, err := s.eventsReader(team)
	if err != nil {
		s.log.Warn("digest worker: events store", "agent", agent, "team", team, "err", err)
		return
	}
	dw, err := s.digestWriter(team)
	if err != nil {
		s.log.Warn("digest worker: digest writer", "agent", agent, "team", team, "err", err)
		return
	}
	// Watermark + events from their own stores, before opening the digest tx.
	d, ok, err := loadAgentDigest(ctx, dr, agent)
	if err != nil {
		s.log.Warn("digest worker: load", "agent", agent, "err", err)
		return
	}
	var watermark int64
	if ok {
		watermark = d.WatermarkSeq
	}
	events, err := loadFoldEventsAfter(ctx, er, agent, watermark)
	if err != nil {
		s.log.Warn("digest worker: events", "agent", agent, "err", err)
		return
	}
	if len(events) == 0 {
		return
	}

	tx, err := dw.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	for i := range events {
		if ferr := foldEventIncremental(ctx, er, tx, agent, team, events[i]); ferr != nil {
			s.log.Warn("digest worker: fold", "agent", agent, "seq", events[i].Seq, "err", ferr)
			return
		}
	}
	if cerr := tx.Commit(); cerr != nil {
		// A dropped commit silently loses the digest update for this
		// batch; surface it so the watermark/read-repair gap is visible.
		s.log.Warn("digest worker: commit", "agent", agent, "team", team, "err", cerr)
	}
}

// digestEnvInt reads a non-negative integer override; ok is false if unset or
// unparseable so the caller keeps its default.
func digestEnvInt(key string) (int, bool) {
	v := os.Getenv(key)
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// digestEnvDur reads a millisecond override and returns it as a Duration.
func digestEnvDur(key string) (time.Duration, bool) {
	n, ok := digestEnvInt(key)
	if !ok {
		return 0, false
	}
	return time.Duration(n) * time.Millisecond, true
}
