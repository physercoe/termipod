package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// ADR-029 W2.6 + W2.7: spawn-with-inline-task delivers the task body
// through TWO channels into the worker:
//
//  1. Rendered CLAUDE.md gains a `## Task` section (standing reference
//     the worker can re-read).
//  2. A producer='user' input.text agent_events row lands right after
//     the spawn so InputRouter triggers the first turn.
//
// Without (2) the worker would boot with task context but no trigger;
// the steward would have to follow up with a2a.invoke. These two tests
// pin both deliveries.
func TestDoSpawn_WithInlineTask_PostsFirstUserInput(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-task-input")

	out, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "@worker.task",
		Kind:        "claude-code",
		ProjectID:   proj,
		SpawnSpec:   "project_id: " + proj + "\nkind: claude-code\n",
		Task: &spawnTaskInline{
			Title:  "Investigate 502 spike",
			BodyMD: "Look at the last hour of logs and report findings.",
		},
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v (status=%d)", err, status)
	}

	// (1) A task row was materialized with assignee = new agent and
	// status = in_progress.
	var taskID, taskStatus string
	var assigneeID sql.NullString
	if err := s.db.QueryRow(`
		SELECT id, status, COALESCE(assignee_id, '')
		  FROM tasks WHERE project_id = ?`, proj,
	).Scan(&taskID, &taskStatus, &assigneeID); err != nil {
		t.Fatalf("query task: %v", err)
	}
	if taskStatus != "in_progress" {
		t.Errorf("task status = %q, want in_progress", taskStatus)
	}
	if assigneeID.String != out.AgentID {
		t.Errorf("task assignee_id = %q, want %q", assigneeID.String, out.AgentID)
	}

	// (2) An input.text producer='user' agent_events row exists for
	// the new worker, carrying the task title + body.
	var (
		kind, producer, payloadJSON string
	)
	if err := s.db.QueryRow(`
		SELECT kind, producer, payload_json
		  FROM agent_events
		 WHERE agent_id = ?
		 ORDER BY seq DESC LIMIT 1`, out.AgentID,
	).Scan(&kind, &producer, &payloadJSON); err != nil {
		t.Fatalf("query agent_events: %v", err)
	}
	if kind != "input.text" {
		t.Errorf("event kind = %q, want input.text", kind)
	}
	if producer != "user" {
		t.Errorf("event producer = %q, want user", producer)
	}
	var payload struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if !strings.Contains(payload.Body, "Investigate 502 spike") {
		t.Errorf("body missing title: %q", payload.Body)
	}
	if !strings.Contains(payload.Body, "last hour of logs") {
		t.Errorf("body missing body_md: %q", payload.Body)
	}
}

// W2.6: even without a task body to fire as input, the CLAUDE.md
// section carries the title so the worker has a standing reference.
func TestDoSpawn_WithInlineTask_RendersClaudeMdTaskSection(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-task-claude-md")

	out, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "@worker.claude",
		Kind:        "claude-code",
		ProjectID:   proj,
		SpawnSpec: "project_id: " + proj + "\n" +
			"kind: claude-code\n" +
			"prompt: steward.v1.md\n",
		Task: &spawnTaskInline{
			Title:  "Look at the metrics dashboard",
			BodyMD: "Specifically the p99 latency curves.",
		},
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v (status=%d)", err, status)
	}

	var renderedSpec string
	if err := s.db.QueryRow(
		`SELECT spawn_spec_yaml FROM agent_spawns WHERE child_agent_id = ?`,
		out.AgentID,
	).Scan(&renderedSpec); err != nil {
		t.Fatalf("query spawn_spec_yaml: %v", err)
	}
	var parsed struct {
		ContextFiles map[string]string `yaml:"context_files"`
	}
	if err := yaml.Unmarshal([]byte(renderedSpec), &parsed); err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	body := parsed.ContextFiles["CLAUDE.md"]
	if !strings.Contains(body, "## Task") {
		t.Fatalf("CLAUDE.md missing ## Task header:\n%s", body)
	}
	if !strings.Contains(body, "Look at the metrics dashboard") {
		t.Errorf("CLAUDE.md missing task title:\n%s", body)
	}
	if !strings.Contains(body, "p99 latency curves") {
		t.Errorf("CLAUDE.md missing task body:\n%s", body)
	}
}

// W2.7: task_id linkage to an existing task also fires the first user
// input. The body is loaded from the tasks row via buildTaskInstructions.
func TestDoSpawn_WithTaskID_PostsFirstUserInput(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-task-id-input")

	// Seed an existing task in todo status.
	taskID := NewID()
	now := NowUTC()
	if _, err := s.db.Exec(`
		INSERT INTO tasks (id, project_id, title, body_md, status, priority, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'todo', 'med', ?, ?)`,
		taskID, proj, "Backfill seq column", "Run migrate-0042 over the events table.",
		now, now); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	out, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "@worker.byid",
		Kind:        "claude-code",
		ProjectID:   proj,
		SpawnSpec:   "project_id: " + proj + "\nkind: claude-code\n",
		TaskID:      taskID,
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v (status=%d)", err, status)
	}

	// Task flipped from todo to in_progress.
	var taskStatus string
	if err := s.db.QueryRow(`SELECT status FROM tasks WHERE id = ?`, taskID,
	).Scan(&taskStatus); err != nil {
		t.Fatalf("query task status: %v", err)
	}
	if taskStatus != "in_progress" {
		t.Errorf("task status = %q, want in_progress", taskStatus)
	}

	// First user input fired with the existing task's body.
	var payloadJSON string
	if err := s.db.QueryRow(`
		SELECT payload_json
		  FROM agent_events
		 WHERE agent_id = ? AND kind = 'input.text' AND producer = 'user'
		 ORDER BY seq DESC LIMIT 1`, out.AgentID,
	).Scan(&payloadJSON); err != nil {
		t.Fatalf("query input.text event: %v", err)
	}
	var payload struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if !strings.Contains(payload.Body, "Backfill seq column") {
		t.Errorf("body missing existing-task title: %q", payload.Body)
	}
	if !strings.Contains(payload.Body, "migrate-0042") {
		t.Errorf("body missing existing-task body_md: %q", payload.Body)
	}
}

// W2.7: ad-hoc spawn with neither inline task nor task_id does NOT
// auto-post any input — the steward sends the first message through
// the regular channels (mobile chat / a2a.invoke).
func TestDoSpawn_NoTask_DoesNotAutoPostInput(t *testing.T) {
	s, _ := newTestServer(t)

	out, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "@adhoc.worker",
		Kind:        "claude-code",
		SpawnSpec:   "kind: claude-code\n",
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v (status=%d)", err, status)
	}

	var count int
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM agent_events
		 WHERE agent_id = ? AND kind = 'input.text' AND producer = 'user'`,
		out.AgentID,
	).Scan(&count); err != nil {
		t.Fatalf("count input events: %v", err)
	}
	if count != 0 {
		t.Errorf("ad-hoc spawn auto-posted %d input event(s); want 0", count)
	}
}
