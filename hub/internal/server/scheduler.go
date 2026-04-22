package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/robfig/cron/v3"
)

// Scheduler owns the running cron engine. On Start it loads enabled cron
// schedules and registers each as a cron entry. On tick the scheduler calls
// fireSchedule, which creates a plan row (status='ready') from the schedule's
// template. Host-runner's plan executor (Phase 1) picks up ready plans.
//
// Manual and on_create schedules do not attach to the cron engine; they're
// fired explicitly — manual via /run, on_create at project creation time.
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

// Register attaches a cron schedule to the running engine. Called both at
// Start (for each row) and by the HTTP handler when a new cron schedule is
// created or re-enabled. The team param is informational; fire() re-reads
// the schedule row at tick time.
func (sc *Scheduler) Register(id, team, cronExpr string) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if prev, ok := sc.ids[id]; ok {
		sc.cron.Remove(prev)
		delete(sc.ids, id)
	}
	entryID, err := sc.cron.AddFunc(cronExpr, func() { sc.tick(id) })
	if err != nil {
		return fmt.Errorf("bad cron expression %q: %w", cronExpr, err)
	}
	sc.ids[id] = entryID

	next := sc.cron.Entry(entryID).Next
	_, _ = sc.s.db.Exec(`UPDATE schedules SET next_run_at = ? WHERE id = ?`,
		next.UTC().Format("2006-01-02T15:04:05.000000000Z07:00"), id)
	return nil
}

func (sc *Scheduler) Unregister(id string) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if prev, ok := sc.ids[id]; ok {
		sc.cron.Remove(prev)
		delete(sc.ids, id)
	}
}

func (sc *Scheduler) tick(id string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	planID, err := sc.s.fireSchedule(ctx, id)
	if err != nil {
		sc.log.Warn("schedule fire failed", "id", id, "err", err)
	}
	sc.updateRunStamps(id, planID)
}

func (sc *Scheduler) updateRunStamps(id, planID string) {
	now := NowUTC()
	sc.mu.Lock()
	entryID, ok := sc.ids[id]
	sc.mu.Unlock()
	next := ""
	if ok {
		next = sc.cron.Entry(entryID).Next.UTC().Format("2006-01-02T15:04:05.000000000Z07:00")
	}
	_, err := sc.s.db.Exec(`
		UPDATE schedules
		   SET last_run_at = ?, last_plan_id = ?, next_run_at = ?
		 WHERE id = ?`, now, nullIfEmpty(planID), nullIfEmpty(next), id)
	if err != nil {
		sc.log.Warn("schedule stamp failed", "id", id, "err", err)
	}
}

func (sc *Scheduler) loadAll(ctx context.Context) error {
	rows, err := sc.s.db.QueryContext(ctx, `
		SELECT s.id, p.team_id, s.cron_expr
		  FROM schedules s JOIN projects p ON p.id = s.project_id
		 WHERE s.enabled = 1 AND s.trigger_kind = 'cron' AND s.cron_expr IS NOT NULL`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, team string
		var expr sql.NullString
		if err := rows.Scan(&id, &team, &expr); err != nil {
			return err
		}
		if !expr.Valid {
			continue
		}
		if err := sc.Register(id, team, expr.String); err != nil {
			sc.log.Warn("schedule load failed", "id", id, "err", err)
		}
	}
	return nil
}

// fireSchedule instantiates a plan from a schedule's template and returns
// the new plan id. Shared between cron ticks and manual /run invocations.
// Plan spec_json starts empty — the plan executor reads the template off
// disk when running. Schedule parameters are copied onto the plan so the
// executor has the bound values even if the schedule is later edited.
func (s *Server) fireSchedule(ctx context.Context, scheduleID string) (string, error) {
	var (
		projectID, templateID, params string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT project_id, template_id, parameters_json
		  FROM schedules WHERE id = ?`, scheduleID).
		Scan(&projectID, &templateID, &params)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("schedule %s not found", scheduleID)
	}
	if err != nil {
		return "", err
	}

	planID := NewID()
	now := NowUTC()
	specJSON := `{"parameters":` + params + `}`
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO plans (
			id, project_id, template_id, version, spec_json, status, created_at
		) VALUES (?, ?, ?, ?, ?, 'ready', ?)`,
		planID, projectID, templateID, 1, specJSON, now); err != nil {
		return "", err
	}
	_, _ = s.db.ExecContext(ctx, `
		UPDATE schedules SET last_run_at = ?, last_plan_id = ? WHERE id = ?`,
		now, planID, scheduleID)
	return planID, nil
}

// ---- swallow cron's verbose logging ----

type nopPrintf struct{}

func (nopPrintf) Printf(string, ...any) {}
