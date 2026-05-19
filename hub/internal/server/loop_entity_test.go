package server

import (
	"context"
	"testing"
)

// TestLoopEntity_OpenSet: openLoopEntities is the UNION of open tasks and
// open question-kind attention_items — a closed task and a non-question
// (governance) attention item are excluded.
func TestLoopEntity_OpenSet(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-loop")
	_, openTask := seedAssignerAndTask(t, s, proj, "open work")
	doneTask := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO tasks (id, project_id, title, status, created_at, updated_at)
		VALUES (?, ?, 'finished work', 'done', ?, ?)`,
		doneTask, proj, NowUTC(), NowUTC()); err != nil {
		t.Fatalf("seed done task: %v", err)
	}

	qID := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO attention_items (id, project_id, scope_kind, kind, summary, created_at)
		VALUES (?, ?, 'project', 'help_request', 'need a hand', ?)`,
		qID, proj, NowUTC()); err != nil {
		t.Fatalf("seed question: %v", err)
	}
	govID := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO attention_items (id, project_id, scope_kind, kind, summary, created_at)
		VALUES (?, ?, 'project', 'spawn', 'approve a spawn', ?)`,
		govID, proj, NowUTC()); err != nil {
		t.Fatalf("seed governance item: %v", err)
	}

	got, err := s.openLoopEntities(context.Background())
	if err != nil {
		t.Fatalf("openLoopEntities: %v", err)
	}
	src := map[string]string{}
	for _, e := range got {
		src[e.ID] = e.Source
	}
	if src[openTask] != LoopSourceTask {
		t.Errorf("open task missing from the open-set")
	}
	if _, ok := src[doneTask]; ok {
		t.Errorf("a done task must not be in the open-set")
	}
	if src[qID] != LoopSourceQuestion {
		t.Errorf("open question missing from the open-set")
	}
	if _, ok := src[govID]; ok {
		t.Errorf("a governance attention item is not a loop-entity")
	}
}
