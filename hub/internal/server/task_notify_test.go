package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

// ADR-029 W2.9: terminal task transitions push a kind='task.notify'
// producer='system' event into the assigner's most-recent active
// session. Covers both call sites: manual PATCH (handlePatchTask) and
// auto-derive (deriveTaskStatusFromAgent on agent terminate).
func TestNotifyTaskAssigner_ManualFlipDelivers(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-notify-manual")
	stewardID, taskID := seedAssignerAndTask(t, s, proj, "Investigate logs")

	// Manual flip to done via the helper, simulating a mobile UI patch.
	s.notifyTaskAssigner(context.Background(), defaultTeamID, taskID,
		"in_progress", "done")

	got := lastTaskNotifyEvent(t, s, stewardID)
	if got.Kind != "task.notify" || got.Producer != "system" {
		t.Errorf("event kind/producer = %q/%q; want task.notify/system",
			got.Kind, got.Producer)
	}
	var p struct {
		TaskID string `json:"task_id"`
		To     string `json:"to"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal([]byte(got.Payload), &p); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if p.TaskID != taskID {
		t.Errorf("payload task_id = %q, want %q", p.TaskID, taskID)
	}
	if p.To != "done" {
		t.Errorf("payload to = %q, want done", p.To)
	}
	if !strings.Contains(p.Body, "Investigate logs") {
		t.Errorf("body missing title: %q", p.Body)
	}
}

// W2.9: when result_summary is populated (e.g. by tasks.complete),
// the notification body carries it inline so the steward sees what
// the worker actually did without a follow-up tap.
func TestNotifyTaskAssigner_IncludesResultSummary(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-notify-summary")
	stewardID, taskID := seedAssignerAndTask(t, s, proj, "Audit migration")
	if _, err := s.db.Exec(
		`UPDATE tasks SET result_summary = ? WHERE id = ?`,
		"Found 3 rows still on the old schema; ran fixup script.", taskID,
	); err != nil {
		t.Fatalf("stamp summary: %v", err)
	}

	s.notifyTaskAssigner(context.Background(), defaultTeamID, taskID,
		"in_progress", "done")

	got := lastTaskNotifyEvent(t, s, stewardID)
	var p struct {
		Body          string `json:"body"`
		ResultSummary string `json:"result_summary"`
	}
	_ = json.Unmarshal([]byte(got.Payload), &p)
	if !strings.Contains(p.Body, "3 rows still on the old schema") {
		t.Errorf("body missing summary text: %q", p.Body)
	}
	if !strings.Contains(p.ResultSummary, "fixup script") {
		t.Errorf("payload result_summary missing: %q", p.ResultSummary)
	}
}

// W2.9: non-terminal flips don't fire the notification. todo→
// in_progress is just "worker is starting", not "worker is done."
func TestNotifyTaskAssigner_SkipsNonTerminalTransitions(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-notify-skip")
	stewardID, taskID := seedAssignerAndTask(t, s, proj, "Will be in progress")

	s.notifyTaskAssigner(context.Background(), defaultTeamID, taskID,
		"todo", "in_progress")

	var count int
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM agent_events
		 WHERE agent_id = ? AND kind = 'task.notify'`, stewardID,
	).Scan(&count); err != nil {
		t.Fatalf("count notify events: %v", err)
	}
	if count != 0 {
		t.Errorf("non-terminal transition fired %d notify event(s); want 0",
			count)
	}
}

// W2.9: principal-direct tasks (created_by_id NULL) have no assigner
// to push to. The helper short-circuits silently rather than blowing up.
func TestNotifyTaskAssigner_NoAssignerSilent(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-notify-noassigner")
	taskID := NewID()
	now := NowUTC()
	if _, err := s.db.Exec(`
		INSERT INTO tasks (id, project_id, title, status, priority, created_at, updated_at)
		VALUES (?, ?, ?, 'in_progress', 'med', ?, ?)`,
		taskID, proj, "Principal-direct task", now, now); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	// Must not panic / log error / write any event.
	s.notifyTaskAssigner(context.Background(), defaultTeamID, taskID,
		"in_progress", "done")
}

// End-to-end: spawn-with-task, terminate the worker, the assigner
// (the parent steward) gets the auto-derived notification. This pins
// the deriveTaskStatusFromAgent → notifyTaskAssigner edge that lights
// up when claude exits cleanly.
func TestNotifyTaskAssigner_AutoDeriveOnAgentTerminate(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-notify-autoderive")

	// Steward agent + its session — must exist before the spawn so
	// inline-create's `created_by_id = parent_agent_id` lands properly
	// and the steward has somewhere to receive the notification.
	stewardID := seedAgentWithActiveSession(t, s, "@steward.proj", "steward.v1")

	// Spawn a worker via DoSpawn with parent = steward + inline task.
	out, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "@worker.terminator",
		Kind:        "claude-code",
		ParentID:    stewardID,
		ProjectID:   proj,
		SpawnSpec:   "project_id: " + proj + "\nkind: claude-code\nbackend:\n  cmd: echo test\n",
		Task: &spawnTaskInline{
			Title:  "Run the migration",
			BodyMD: "Apply migration 0042 and confirm row count.",
		},
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v (status=%d)", err, status)
	}

	// Worker called tasks.complete first (populated result_summary)
	// then was terminated. This is the canonical happy path: result is
	// real, auto-derive flips to done. v1.0.619: empty result_summary
	// would flip to cancelled instead (see _CancelledOnAbandon below).
	if _, err := s.db.Exec(
		`UPDATE tasks SET result_summary = ? WHERE assignee_id = ?`,
		"Migration applied; 12 318 rows.", out.AgentID,
	); err != nil {
		t.Fatalf("seed result_summary: %v", err)
	}

	// Flip the worker to terminated — the agent-side path the host
	// runner takes when claude exits cleanly. This triggers
	// deriveTaskStatusFromAgent which auto-flips the task to done
	// and fires the W2.9 notification.
	if _, err := s.db.Exec(
		`UPDATE agents SET status = 'terminated' WHERE id = ?`, out.AgentID,
	); err != nil {
		t.Fatalf("terminate worker: %v", err)
	}
	if err := s.deriveTaskStatusFromAgent(context.Background(),
		defaultTeamID, out.AgentID, "terminated"); err != nil {
		t.Fatalf("deriveTaskStatusFromAgent: %v", err)
	}

	got := lastTaskNotifyEvent(t, s, stewardID)
	if got.Kind != "task.notify" {
		t.Fatalf("expected task.notify event in steward feed; got %+v", got)
	}
	var p struct {
		To    string `json:"to"`
		Title string `json:"title"`
	}
	_ = json.Unmarshal([]byte(got.Payload), &p)
	if p.To != "done" {
		t.Errorf("payload to = %q, want done", p.To)
	}
	if p.Title != "Run the migration" {
		t.Errorf("payload title = %q, want Run the migration", p.Title)
	}
}

// v1.0.619 rule refinement: a worker terminated WITHOUT having called
// tasks.complete (result_summary still empty) means the task was
// abandoned, not finished. Auto-derive must flip to `cancelled`, not
// `done` — otherwise the activity feed lies about successful
// completion. The audit summary tags it `(no result_summary; worker
// abandoned)` so feed readers can distinguish from operator cancel.
func TestNotifyTaskAssigner_AutoDeriveAbandonedTerminate_Cancels(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-notify-abandoned")
	stewardID := seedAgentWithActiveSession(t, s, "@steward.proj", "steward.v1")

	out, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "@worker.stuck",
		Kind:        "claude-code",
		ParentID:    stewardID,
		ProjectID:   proj,
		SpawnSpec:   "project_id: " + proj + "\nkind: claude-code\nbackend:\n  cmd: echo test\n",
		Task: &spawnTaskInline{
			Title:  "Investigate the regression",
			BodyMD: "Find why p99 latency tripled.",
		},
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v (status=%d)", err, status)
	}

	// Critically: do NOT seed result_summary. The worker got stuck;
	// the steward is killing it without a completion report.
	if _, err := s.db.Exec(
		`UPDATE agents SET status = 'terminated' WHERE id = ?`, out.AgentID,
	); err != nil {
		t.Fatalf("terminate worker: %v", err)
	}
	if err := s.deriveTaskStatusFromAgent(context.Background(),
		defaultTeamID, out.AgentID, "terminated"); err != nil {
		t.Fatalf("deriveTaskStatusFromAgent: %v", err)
	}

	// Task must be cancelled, not done.
	var taskStatus string
	if err := s.db.QueryRow(
		`SELECT status FROM tasks WHERE assignee_id = ?`, out.AgentID,
	).Scan(&taskStatus); err != nil {
		t.Fatalf("read task: %v", err)
	}
	if taskStatus != "cancelled" {
		t.Errorf("task status = %q, want cancelled (no result_summary on terminate)", taskStatus)
	}

	// completed_at should be stamped so the Tasks tab reads
	// "cancelled <N>m ago" without falling back to updated_at.
	var completedAt sql.NullString
	if err := s.db.QueryRow(
		`SELECT completed_at FROM tasks WHERE assignee_id = ?`, out.AgentID,
	).Scan(&completedAt); err != nil {
		t.Fatalf("read completed_at: %v", err)
	}
	if !completedAt.Valid || completedAt.String == "" {
		t.Errorf("completed_at not stamped on abandoned cancellation")
	}

	// Audit row carries abandoned=true so feed readers can tell auto-
	// derived abandonment from explicit operator cancellation.
	var meta string
	if err := s.db.QueryRow(`
		SELECT meta_json FROM audit_events
		 WHERE action = 'task.status' AND target_id = (
		   SELECT id FROM tasks WHERE assignee_id = ?
		 )
		 ORDER BY ts DESC LIMIT 1`, out.AgentID,
	).Scan(&meta); err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if !strings.Contains(meta, `"abandoned":true`) {
		t.Errorf("audit meta missing abandoned flag: %s", meta)
	}

	// Steward gets the task.notify with to=cancelled — not done.
	got := lastTaskNotifyEvent(t, s, stewardID)
	if got.Kind != "task.notify" {
		t.Fatalf("expected task.notify; got %+v", got)
	}
	var p struct {
		To string `json:"to"`
	}
	_ = json.Unmarshal([]byte(got.Payload), &p)
	if p.To != "cancelled" {
		t.Errorf("payload to = %q, want cancelled", p.To)
	}
}

// White-space-only result_summary still counts as "no real result" for
// the abandonment rule. Pins TrimSpace behaviour so a worker that
// stamps `summary=" "` doesn't accidentally claim completion.
func TestDeriveTaskStatus_BlankSummaryStillCancels(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-blank-summary")
	stewardID := seedAgentWithActiveSession(t, s, "@steward.proj", "steward.v1")

	out, _, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "@worker.blank",
		Kind:        "claude-code",
		ParentID:    stewardID,
		ProjectID:   proj,
		SpawnSpec:   "project_id: " + proj + "\nkind: claude-code\nbackend:\n  cmd: echo test\n",
		Task:        &spawnTaskInline{Title: "Quick task", BodyMD: "x"},
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v", err)
	}

	if _, err := s.db.Exec(
		`UPDATE tasks SET result_summary = ? WHERE assignee_id = ?`,
		"   \n  \t  ", out.AgentID,
	); err != nil {
		t.Fatalf("seed blank summary: %v", err)
	}
	_, _ = s.db.Exec(`UPDATE agents SET status = 'terminated' WHERE id = ?`, out.AgentID)
	if err := s.deriveTaskStatusFromAgent(context.Background(),
		defaultTeamID, out.AgentID, "terminated"); err != nil {
		t.Fatalf("derive: %v", err)
	}

	var taskStatus string
	_ = s.db.QueryRow(`SELECT status FROM tasks WHERE assignee_id = ?`, out.AgentID).Scan(&taskStatus)
	if taskStatus != "cancelled" {
		t.Errorf("blank summary → status %q, want cancelled", taskStatus)
	}
}

// --- test helpers --------------------------------------------------

type notifyEvent struct {
	Kind     string
	Producer string
	Payload  string
}

func lastTaskNotifyEvent(t *testing.T, s *Server, agentID string) notifyEvent {
	t.Helper()
	var ev notifyEvent
	err := s.db.QueryRow(`
		SELECT kind, producer, payload_json
		  FROM agent_events
		 WHERE agent_id = ? AND kind = 'task.notify'
		 ORDER BY seq DESC LIMIT 1`, agentID,
	).Scan(&ev.Kind, &ev.Producer, &ev.Payload)
	if err != nil {
		t.Fatalf("query task.notify event: %v", err)
	}
	return ev
}

// seedAssignerAndTask creates a steward-shaped agent with an active
// session, plus a task in `in_progress` assigned to a worker and
// created_by the steward. Returns (stewardID, taskID).
func seedAssignerAndTask(t *testing.T, s *Server, project, title string) (string, string) {
	t.Helper()
	stewardID := seedAgentWithActiveSession(t, s, "@steward.notify", "steward.v1")
	workerID := seedAgentWithActiveSession(t, s, "@worker.notify", "worker.v1")
	taskID := NewID()
	now := NowUTC()
	if _, err := s.db.Exec(`
		INSERT INTO tasks (
			id, project_id, title, status, priority,
			assignee_id, created_by_id, started_at, created_at, updated_at
		) VALUES (?, ?, ?, 'in_progress', 'med', ?, ?, ?, ?, ?)`,
		taskID, project, title, workerID, stewardID, now, now, now); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	return stewardID, taskID
}

// seedAgentWithActiveSession inserts an agent + an active session pointing
// at it. Returns the agent id.
func seedAgentWithActiveSession(t *testing.T, s *Server, handle, kind string) string {
	t.Helper()
	id := NewID()
	now := NowUTC()
	if _, err := s.db.Exec(`
		INSERT INTO agents (id, team_id, handle, kind, status, created_at)
		VALUES (?, ?, ?, ?, 'running', ?)`,
		id, defaultTeamID, handle, kind, now); err != nil {
		t.Fatalf("seed agent %s: %v", handle, err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO sessions (id, team_id, current_agent_id, status, opened_at, last_active_at)
		VALUES (?, ?, ?, 'active', ?, ?)`,
		NewID(), defaultTeamID, id, now, now); err != nil {
		t.Fatalf("seed session for %s: %v", handle, err)
	}
	return id
}
