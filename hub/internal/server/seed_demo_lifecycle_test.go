// seed_demo_lifecycle_test.go — coverage for `seed-demo --shape
// lifecycle`. Verifies the seed inserts the expected five-project
// portfolio (one per phase) with the right deliverables/criteria/
// section state mix, plus the idempotency + reset round-trip behavior.

package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
)

func TestSeedLifecycleDemo_InsertsFivePhaseStagedProjects(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/hub.db"
	if _, err := Init(dir, dbPath); err != nil {
		t.Fatalf("Init: %v", err)
	}
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	res, err := SeedLifecycleDemo(ctx, db, "")
	if err != nil {
		t.Fatalf("SeedLifecycleDemo: %v", err)
	}
	if res.Skipped {
		t.Fatal("first call: Skipped=true unexpected")
	}

	// Five projects, all with template_id=research and phase set.
	wantProjects := []struct {
		id, phase string
	}{
		{res.IdeaProjectID, "idea"},
		{res.LitReviewProjectID, "lit-review"},
		{res.MethodProjectID, "method"},
		{res.ExperimentProjectID, "experiment"},
		{res.PaperProjectID, "paper"},
	}
	for _, p := range wantProjects {
		if p.id == "" {
			t.Fatalf("phase %s: project id empty", p.phase)
		}
		var tplID, phase string
		if err := db.QueryRowContext(ctx, `
			SELECT COALESCE(template_id,''), COALESCE(phase,'')
			FROM projects WHERE id = ?`, p.id).Scan(&tplID, &phase); err != nil {
			t.Fatalf("project %s lookup: %v", p.id, err)
		}
		if tplID != "research" {
			t.Errorf("phase %s: template_id=%q; want research", p.phase, tplID)
		}
		if phase != p.phase {
			t.Errorf("project %s: phase=%q; want %q", p.id, phase, p.phase)
		}
	}

	// Deliverable count: idea=0, lit-review=1, method=2,
	// experiment=3, paper=4 → total 10.
	if got := res.DeliverableCount; got != 10 {
		t.Errorf("deliverable count = %d; want 10", got)
	}

	// ADR-020 W1: 3 annotations on the lit-review demo + 2 on the
	// method demo → 5 director annotations seeded. The mix covers
	// open/resolved + every kind except question→open.
	if got := res.AnnotationCount; got != 5 {
		t.Errorf("annotation count = %d; want 5 (3 lit-review + 2 method)", got)
	}
	var openCount, resolvedCount int
	if err := db.QueryRowContext(ctx,
		`SELECT
		   SUM(CASE WHEN status='open' THEN 1 ELSE 0 END),
		   SUM(CASE WHEN status='resolved' THEN 1 ELSE 0 END)
		 FROM document_annotations`,
	).Scan(&openCount, &resolvedCount); err != nil {
		t.Fatalf("annotation status query: %v", err)
	}
	if openCount != 4 || resolvedCount != 1 {
		t.Errorf("annotation status mix: open=%d resolved=%d; want 4/1",
			openCount, resolvedCount)
	}

	// Criteria states: every state value should appear at least once
	// (the seed deliberately exercises pending/met/failed/waived).
	wantStates := []string{"pending", "met", "failed", "waived"}
	for _, st := range wantStates {
		if res.CriteriaByState[st] == 0 {
			t.Errorf("CriteriaByState[%s] = 0; expected ≥ 1 to exercise the pip", st)
		}
	}

	// All three criterion kinds present (text, metric, gate).
	rows, err := db.QueryContext(ctx,
		`SELECT DISTINCT kind FROM acceptance_criteria
		 WHERE project_id IN (?,?,?,?,?)`,
		res.IdeaProjectID, res.LitReviewProjectID, res.MethodProjectID,
		res.ExperimentProjectID, res.PaperProjectID)
	if err != nil {
		t.Fatalf("kind query: %v", err)
	}
	defer rows.Close()
	gotKinds := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatalf("scan kind: %v", err)
		}
		gotKinds[k] = true
	}
	for _, want := range []string{"text", "metric", "gate"} {
		if !gotKinds[want] {
			t.Errorf("criterion kind %q missing from seeded portfolio", want)
		}
	}

	// Deliverable ratification states: all three should appear (draft,
	// in-review, ratified).
	dRows, err := db.QueryContext(ctx, `
		SELECT DISTINCT ratification_state FROM deliverables
		WHERE project_id IN (?,?,?,?,?)`,
		res.IdeaProjectID, res.LitReviewProjectID, res.MethodProjectID,
		res.ExperimentProjectID, res.PaperProjectID)
	if err != nil {
		t.Fatalf("deliverable state query: %v", err)
	}
	defer dRows.Close()
	gotDStates := map[string]bool{}
	for dRows.Next() {
		var s string
		if err := dRows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		gotDStates[s] = true
	}
	for _, want := range []string{"draft", "in-review", "ratified"} {
		if !gotDStates[want] {
			t.Errorf("deliverable state %q missing from seeded portfolio", want)
		}
	}

	// Typed-document section states: every project's document content
	// is structuredBody JSON; every section state (empty/draft/ratified)
	// should be present somewhere in the portfolio.
	docRows, err := db.QueryContext(ctx, `
		SELECT content_inline FROM documents
		WHERE project_id IN (?,?,?,?,?)
		  AND schema_id IS NOT NULL`,
		res.IdeaProjectID, res.LitReviewProjectID, res.MethodProjectID,
		res.ExperimentProjectID, res.PaperProjectID)
	if err != nil {
		t.Fatalf("doc content query: %v", err)
	}
	defer docRows.Close()
	gotSecStates := map[string]bool{}
	for docRows.Next() {
		var inline sql.NullString
		if err := docRows.Scan(&inline); err != nil {
			t.Fatalf("scan content_inline: %v", err)
		}
		var body struct {
			Sections []struct {
				Status string `json:"status"`
			} `json:"sections"`
		}
		if err := json.Unmarshal([]byte(inline.String), &body); err != nil {
			t.Fatalf("decode section body: %v", err)
		}
		for _, s := range body.Sections {
			gotSecStates[s.Status] = true
		}
	}
	for _, want := range []string{"empty", "draft", "ratified"} {
		if !gotSecStates[want] {
			t.Errorf("section state %q missing from typed-doc seeds", want)
		}
	}

	// Idea project's phase_history should record the system →idea transition
	// only; paper project's history should have all five phase transitions.
	for _, p := range []struct {
		id   string
		want int
	}{
		{res.IdeaProjectID, 1},
		{res.PaperProjectID, 5},
	} {
		var hist sql.NullString
		if err := db.QueryRowContext(ctx,
			`SELECT phase_history FROM projects WHERE id = ?`, p.id).
			Scan(&hist); err != nil {
			t.Fatalf("phase_history lookup: %v", err)
		}
		var doc struct {
			Transitions []struct{} `json:"transitions"`
		}
		if err := json.Unmarshal([]byte(hist.String), &doc); err != nil {
			t.Fatalf("decode phase_history: %v", err)
		}
		if len(doc.Transitions) != p.want {
			t.Errorf("project %s: %d transitions; want %d",
				p.id, len(doc.Transitions), p.want)
		}
	}

	// Attention items: one per project (5 total).
	if got := res.AttentionItemCount; got != 5 {
		t.Errorf("AttentionItemCount = %d; want 5", got)
	}

	// ADR-020 W2: the lit-review demo emits a `revision_requested`
	// attention item carrying deliverable_id + annotation_ids in the
	// pending_payload_json so the steward can address each note.
	var revisionPayload string
	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(pending_payload_json, '')
		  FROM attention_items
		 WHERE project_id = ? AND kind = 'revision_requested'`,
		res.LitReviewProjectID).Scan(&revisionPayload); err != nil {
		t.Fatalf("revision_requested lookup: %v", err)
	}
	if revisionPayload == "" {
		t.Fatal("revision_requested attention has no payload")
	}
	var rev map[string]any
	if err := json.Unmarshal([]byte(revisionPayload), &rev); err != nil {
		t.Fatalf("decode revision payload: %v", err)
	}
	if rev["deliverable_id"] == nil || rev["deliverable_id"] == "" {
		t.Errorf("revision payload missing deliverable_id: %v", rev)
	}
	annIDs, _ := rev["annotation_ids"].([]any)
	if len(annIDs) != 3 {
		t.Errorf("revision payload annotation_ids len=%d; want 3 (lit-review seed)",
			len(annIDs))
	}

	// run_metrics rows exist for every lifecycle run — without these,
	// the runs screen renders empty even though a run is recorded.
	var metricCount int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM run_metrics
		WHERE run_id IN (
			SELECT id FROM runs WHERE project_id IN (?,?,?,?,?)
		)`,
		res.IdeaProjectID, res.LitReviewProjectID, res.MethodProjectID,
		res.ExperimentProjectID, res.PaperProjectID,
	).Scan(&metricCount); err != nil {
		t.Fatalf("run_metrics count: %v", err)
	}
	if metricCount == 0 {
		t.Error("expected run_metrics rows for lifecycle runs, got 0")
	}

	// commit components: the method-phase + experiment-phase
	// deliverables anchor a code revision so reviewers can rebuild
	// from source. Both methods and experiments seed at least one
	// `commit` component.
	var commitCount int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM deliverable_components
		WHERE kind = 'commit'
		  AND deliverable_id IN (
		    SELECT id FROM deliverables WHERE project_id IN (?,?,?,?,?)
		  )`,
		res.IdeaProjectID, res.LitReviewProjectID, res.MethodProjectID,
		res.ExperimentProjectID, res.PaperProjectID,
	).Scan(&commitCount); err != nil {
		t.Fatalf("commit components count: %v", err)
	}
	if commitCount == 0 {
		t.Error("expected commit components on method/experiment deliverables, got 0")
	}

	// Audit rows include criterion.created entries and at least one
	// deliverable.ratified entry.
	var critCreated, delivRatified int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM audit_events
		WHERE team_id = ? AND action = 'criterion.created'
		  AND actor_handle = 'steward.lifecycle'`,
		defaultTeamID).Scan(&critCreated); err != nil {
		t.Fatalf("audit count (criterion.created): %v", err)
	}
	if critCreated == 0 {
		t.Error("expected criterion.created audit rows, got 0")
	}
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM audit_events
		WHERE team_id = ? AND action = 'deliverable.ratified'`,
		defaultTeamID).Scan(&delivRatified); err != nil {
		t.Fatalf("audit count (deliverable.ratified): %v", err)
	}
	if delivRatified == 0 {
		t.Error("expected deliverable.ratified audit rows, got 0")
	}

	// W2: every seeded plan_step.kind must be in planStepKinds.
	// The old seed wrote `agent_driven`, which isn't in the validator
	// set and silently slipped in because there's no DB-level check.
	stepRows, err := db.QueryContext(ctx, `
		SELECT DISTINCT kind FROM plan_steps
		WHERE plan_id IN (SELECT id FROM plans WHERE project_id IN (?, ?, ?, ?, ?))`,
		res.IdeaProjectID, res.LitReviewProjectID, res.MethodProjectID,
		res.ExperimentProjectID, res.PaperProjectID)
	if err != nil {
		t.Fatalf("plan_steps kind query: %v", err)
	}
	defer stepRows.Close()
	for stepRows.Next() {
		var kind string
		if err := stepRows.Scan(&kind); err != nil {
			t.Fatalf("scan kind: %v", err)
		}
		if !planStepKinds[kind] {
			t.Errorf("plan_step kind %q not in planStepKinds %v", kind, planStepKinds)
		}
	}

	// W2: every project must seed ≥ 1 task. Tasks are project-scoped
	// (no phase column) and exercise the Tasks tab on project detail.
	if res.TaskCount < 5 {
		t.Errorf("task count = %d; want ≥ 5 (≥ 1 per project across 5 projects)",
			res.TaskCount)
	}
	for _, p := range wantProjects {
		var n int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM tasks WHERE project_id = ?`, p.id).
			Scan(&n); err != nil {
			t.Fatalf("task count for %s: %v", p.phase, err)
		}
		if n == 0 {
			t.Errorf("phase %s: 0 tasks seeded; want ≥ 1", p.phase)
		}
	}
}

func TestSeedLifecycleDemo_Idempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/hub.db"
	if _, err := Init(dir, dbPath); err != nil {
		t.Fatalf("Init: %v", err)
	}
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	first, err := SeedLifecycleDemo(ctx, db, "")
	if err != nil {
		t.Fatalf("first seed: %v", err)
	}
	second, err := SeedLifecycleDemo(ctx, db, "")
	if err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if !second.Skipped {
		t.Errorf("second call: Skipped=false; want true")
	}
	if second.IdeaProjectID != first.IdeaProjectID {
		t.Errorf("second call returned different idea-project id: %s vs %s",
			second.IdeaProjectID, first.IdeaProjectID)
	}
}

func TestResetLifecycleDemo_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/hub.db"
	if _, err := Init(dir, dbPath); err != nil {
		t.Fatalf("Init: %v", err)
	}
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	// Reset on empty DB → no-op, returns deleted=false.
	if deleted, err := ResetLifecycleDemo(ctx, db); err != nil || deleted {
		t.Fatalf("empty reset: deleted=%v err=%v", deleted, err)
	}

	// Seed, then reset, then verify all five projects gone.
	first, err := SeedLifecycleDemo(ctx, db, "")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	deleted, err := ResetLifecycleDemo(ctx, db)
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if !deleted {
		t.Error("reset: deleted=false after seed")
	}
	for _, pid := range []string{
		first.IdeaProjectID, first.LitReviewProjectID,
		first.MethodProjectID, first.ExperimentProjectID,
		first.PaperProjectID,
	} {
		var count int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM projects WHERE id = ?`, pid).
			Scan(&count); err != nil {
			t.Fatalf("post-reset project count: %v", err)
		}
		if count != 0 {
			t.Errorf("project %s still exists after reset (count=%d)", pid, count)
		}
	}
	// All deliverables + criteria + annotations for the seeded set should
	// be gone. document_annotations cascade off documents (migration 0035),
	// which the reset path explicitly DELETEs.
	for _, table := range []string{"deliverables", "acceptance_criteria", "document_annotations"} {
		var count int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM `+table).Scan(&count); err != nil {
			t.Fatalf("%s count: %v", table, err)
		}
		if count != 0 {
			t.Errorf("table %s still has %d rows after reset", table, count)
		}
	}
	// Re-seed succeeds (proves reset cleaned thoroughly).
	if _, err := SeedLifecycleDemo(ctx, db, ""); err != nil {
		t.Fatalf("re-seed after reset: %v", err)
	}
}

// TestLifecycleSeed_BundledPlanTemplate verifies the bundled
// research.v1.yaml file is reachable via the embed.FS so stewards (and
// tests against template-loading) can read it.
func TestLifecycleSeed_BundledPlanTemplate(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/hub.db"
	if _, err := Init(dir, dbPath); err != nil {
		t.Fatalf("Init: %v", err)
	}
	s, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	body, err := s.loadBuiltinAgentTemplate("steward.research.v1.yaml")
	if err != nil {
		t.Fatalf("seed agent template missing: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("seed agent body empty")
	}
	overlayPath := dir + "/team/templates/projects/research.v1.yaml"
	if _, err := os.Stat(overlayPath); err != nil {
		t.Errorf("plan template not copied to overlay at %s: %v", overlayPath, err)
	}
}
