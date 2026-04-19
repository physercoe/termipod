package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writePolicy seeds the per-team policy.yaml then forces the server's
// policyStore to reload. The test doesn't go through SIGHUP so it can
// assert the effect synchronously.
func writePolicy(t *testing.T, s *Server, dataRoot, body string) {
	t.Helper()
	dir := filepath.Join(dataRoot, "team")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "policy.yaml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	s.policy.reload()
}

// insertAttention inserts one attention_items row with the given tier, a
// fabricated created_at, and an empty escalation history. Returns its id so
// the test can read it back post-sweep.
func insertAttention(t *testing.T, s *Server, tier, createdAt string, assignees []string) string {
	t.Helper()
	id := NewID()
	aj, _ := json.Marshal(assignees)
	if _, err := s.db.Exec(`
		INSERT INTO attention_items (
			id, scope_kind, scope_id, kind, summary, severity,
			current_assignees_json, decisions_json, escalation_history_json,
			tier, status, created_at
		) VALUES (?, 'team', ?, 'approval_request', ?, 'major',
		          ?, '[]', '[]',
		          ?, 'open', ?)`,
		id, defaultTeamID, "test item", string(aj), tier, createdAt); err != nil {
		t.Fatalf("insert attention: %v", err)
	}
	return id
}

func readAttention(t *testing.T, s *Server, id string) (assignees []string, history []map[string]any) {
	t.Helper()
	var aj, hj string
	if err := s.db.QueryRow(
		`SELECT current_assignees_json, escalation_history_json FROM attention_items WHERE id = ?`,
		id).Scan(&aj, &hj); err != nil {
		t.Fatalf("read attention: %v", err)
	}
	_ = json.Unmarshal([]byte(aj), &assignees)
	_ = json.Unmarshal([]byte(hj), &history)
	return
}

func TestEscalator_PromotesStaleItem(t *testing.T) {
	s, dataRoot := newTestServer(t)
	writePolicy(t, s, dataRoot, `
tiers:
  spawn: moderate
escalation:
  moderate:
    after: "1ms"
    widen_to: ["@principal", "@steward"]
`)

	old := time.Now().Add(-1 * time.Minute).UTC().Format(time.RFC3339Nano)
	id := insertAttention(t, s, TierModerate, old, []string{"@steward"})

	e := NewEscalator(s, s.log, 10*time.Millisecond)
	e.sweep(context.Background())

	got, history := readAttention(t, s, id)
	want := []string{"@principal", "@steward"}
	if len(got) != len(want) {
		t.Fatalf("assignees = %v, want %v", got, want)
	}
	for i, h := range want {
		if got[i] != h {
			t.Errorf("assignees[%d] = %q, want %q", i, got[i], h)
		}
	}
	if len(history) != 1 {
		t.Fatalf("history len = %d, want 1", len(history))
	}
	if history[0]["tier"] != TierModerate {
		t.Errorf("history tier = %v, want moderate", history[0]["tier"])
	}
	if history[0]["reason"] != "deadline_exceeded" {
		t.Errorf("history reason = %v", history[0]["reason"])
	}
}

func TestEscalator_SkipsFreshItem(t *testing.T) {
	s, dataRoot := newTestServer(t)
	writePolicy(t, s, dataRoot, `
escalation:
  critical:
    after: "10m"
    widen_to: ["@principal"]
`)
	// Created just now — well inside the 10m window.
	id := insertAttention(t, s, TierCritical,
		time.Now().UTC().Format(time.RFC3339Nano), []string{"@steward"})

	e := NewEscalator(s, s.log, 10*time.Millisecond)
	e.sweep(context.Background())

	got, history := readAttention(t, s, id)
	if len(got) != 1 || got[0] != "@steward" {
		t.Errorf("assignees modified before deadline: %v", got)
	}
	if len(history) != 0 {
		t.Errorf("history wrote early: %v", history)
	}
}

func TestEscalator_Idempotent(t *testing.T) {
	s, dataRoot := newTestServer(t)
	writePolicy(t, s, dataRoot, `
escalation:
  moderate:
    after: "1ms"
    widen_to: ["@principal"]
`)
	old := time.Now().Add(-1 * time.Minute).UTC().Format(time.RFC3339Nano)
	id := insertAttention(t, s, TierModerate, old, []string{"@steward"})

	e := NewEscalator(s, s.log, 10*time.Millisecond)
	e.sweep(context.Background()) // first
	e.sweep(context.Background()) // second must not append another history entry

	_, history := readAttention(t, s, id)
	if len(history) != 1 {
		t.Errorf("history len after double-sweep = %d, want 1", len(history))
	}
}

func TestEscalator_NoRuleForTierLeavesAlone(t *testing.T) {
	s, dataRoot := newTestServer(t)
	// Policy has escalation for critical, but the item is moderate.
	writePolicy(t, s, dataRoot, `
escalation:
  critical:
    after: "1ms"
    widen_to: ["@principal"]
`)
	old := time.Now().Add(-1 * time.Minute).UTC().Format(time.RFC3339Nano)
	id := insertAttention(t, s, TierModerate, old, []string{"@steward"})

	e := NewEscalator(s, s.log, 10*time.Millisecond)
	e.sweep(context.Background())

	got, history := readAttention(t, s, id)
	if len(got) != 1 || got[0] != "@steward" {
		t.Errorf("unrelated tier was touched: %v", got)
	}
	if len(history) != 0 {
		t.Errorf("history written for unconfigured tier")
	}
}
