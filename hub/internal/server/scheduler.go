package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/robfig/cron/v3"
)

// Scheduler owns the running cron engine. On Start it loads enabled
// agent_schedules, registers each as a cron entry that calls DoSpawn at
// tick time, and records last_run_status / next_run_at back to the row.
//
// Spec storage: the schedule's spawn_spec_yaml column holds a JSON-encoded
// spawnIn (which is valid YAML). That keeps us off a YAML parser until the
// real template engine lands; also means the scheduler speaks the same
// dialect as /v1/.../agents/spawn.
type Scheduler struct {
	s   *Server
	log *slog.Logger

	mu   sync.Mutex
	cron *cron.Cron
	ids  map[string]cron.EntryID // schedule id -> cron entry id
}

func NewScheduler(s *Server, log *slog.Logger) *Scheduler {
	if log == nil {
		log = slog.Default()
	}
	return &Scheduler{
		s:    s,
		log:  log,
		cron: cron.New(cron.WithLogger(cron.VerbosePrintfLogger(nopPrintf{}))),
		ids:  map[string]cron.EntryID{},
	}
}

func (sc *Scheduler) Start(ctx context.Context) error {
	if err := sc.loadAll(ctx); err != nil {
		return err
	}
	sc.cron.Start()
	return nil
}

func (sc *Scheduler) Stop() {
	<-sc.cron.Stop().Done()
}

// Register adds or updates a schedule in the running cron. Called both at
// Start (for each row) and by the HTTP handler when a new schedule POSTs.
func (sc *Scheduler) Register(id, team, cronExpr, specJSON string) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if prev, ok := sc.ids[id]; ok {
		sc.cron.Remove(prev)
		delete(sc.ids, id)
	}
	entryID, err := sc.cron.AddFunc(cronExpr, func() { sc.fire(id, team, specJSON) })
	if err != nil {
		return fmt.Errorf("bad cron expression %q: %w", cronExpr, err)
	}
	sc.ids[id] = entryID

	// Update next_run_at on the row so API callers see when it'll fire.
	next := sc.cron.Entry(entryID).Next
	_, _ = sc.s.db.Exec(`UPDATE agent_schedules SET next_run_at = ? WHERE id = ?`,
		next.UTC().Format("2006-01-02T15:04:05.000000000Z07:00"), id)
	return nil
}

// Unregister removes a schedule from the running cron. The row stays in the
// DB; caller is responsible for deleting or disabling it.
func (sc *Scheduler) Unregister(id string) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if prev, ok := sc.ids[id]; ok {
		sc.cron.Remove(prev)
		delete(sc.ids, id)
	}
}

func (sc *Scheduler) fire(id, team, specJSON string) {
	var in spawnIn
	if err := json.Unmarshal([]byte(specJSON), &in); err != nil {
		sc.markRun(id, "decode-failed: "+err.Error())
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out, _, err := sc.s.DoSpawn(ctx, team, in)
	if err != nil {
		sc.markRun(id, "spawn-failed: "+err.Error())
		return
	}
	sc.markRun(id, "ok:"+out.AgentID)
}

func (sc *Scheduler) markRun(id, status string) {
	now := NowUTC()
	sc.mu.Lock()
	entryID, ok := sc.ids[id]
	sc.mu.Unlock()
	next := ""
	if ok {
		next = sc.cron.Entry(entryID).Next.UTC().Format("2006-01-02T15:04:05.000000000Z07:00")
	}
	_, err := sc.s.db.Exec(`
		UPDATE agent_schedules
		SET last_run_at = ?, last_run_status = ?, next_run_at = ?
		WHERE id = ?`, now, status, next, id)
	if err != nil {
		sc.log.Warn("markRun failed", "id", id, "err", err)
	}
}

func (sc *Scheduler) loadAll(ctx context.Context) error {
	rows, err := sc.s.db.QueryContext(ctx, `
		SELECT id, team_id, cron_expr, spawn_spec_yaml
		FROM agent_schedules WHERE enabled = 1`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, team, expr, spec string
		if err := rows.Scan(&id, &team, &expr, &spec); err != nil {
			return err
		}
		if err := sc.Register(id, team, expr, spec); err != nil {
			sc.log.Warn("schedule load failed", "id", id, "err", err)
		}
	}
	return nil
}

// ---- swallow cron's verbose logging ----

type nopPrintf struct{}

func (nopPrintf) Printf(string, ...any) {}
