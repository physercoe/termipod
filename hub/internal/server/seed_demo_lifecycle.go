// seed_demo_lifecycle.go — `--shape lifecycle` extension to the
// existing seed-demo harness. Stages a portfolio of five research-
// template projects, each parked at a different phase, so a reviewer
// (or mobile-UI integration test) can tap through the lifecycle UI
// without running phases live.
//
// Why five projects: every phase declares a distinct overview_widget
// (idea_conversation, deliverable_focus, deliverable_focus,
// experiment_dash, paper_acceptance) and a distinct deliverable +
// criterion mix. A single project parked at one phase only exercises
// one slice of the UI vocabulary; five projects exercise:
//   - all five W7 phase heroes (after the resolveOverviewWidget
//     phase-scoped lookup lands)
//   - three deliverable ratification states (draft, in-review, ratified)
//   - all four acceptance-criteria states (pending, met, failed, waived)
//   - all three section states (empty, draft, ratified) on typed docs
//   - all three criterion kinds (text, metric, gate)
//   - phase ribbon at every position (current=highlighted, completed=ok,
//     pending=muted)
//
// Distinct from `--shape ablation` (the original Candidate A single-
// phase sweep demo, which still works for phase-3 isolation testing).
//
// Idempotent — re-running with the same project names reports the
// existing rows without touching anything. Use `--reset` to wipe the
// whole portfolio and re-insert.

package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"math/rand"
	"time"
)

// SeedLifecycleResult summarises the lifecycle seed insert. Keys carry
// what a CLI run prints + what tests assert on.
type SeedLifecycleResult struct {
	// Project IDs for each of the five phase-staged demos. Empty when
	// the corresponding row already existed (Skipped=true).
	IdeaProjectID       string
	LitReviewProjectID  string
	MethodProjectID     string
	ExperimentProjectID string
	PaperProjectID      string

	ProjectIDs []string // convenience: all five, in canonical phase order

	StewardAgentIDs    []string // one steward per project
	DeliverableCount   int      // total deliverables inserted
	CriterionCount     int      // total acceptance_criteria inserted
	CriteriaByState    map[string]int
	DocumentCount      int // typed (W5a) + plain documents
	ArtifactCount      int
	RunCount           int
	AnnotationCount    int // ADR-020 W1 — director annotations on typed docs
	TaskCount          int // kanban tasks (project-scoped, not phase-scoped)
	AttentionItemCount int
	AuditCount         int

	Skipped bool // true when first-project lookup already exists
	Reset   bool // mirrored from the CLI flag for the summary line
}

// Canonical names of the seeded research portfolio. ResetLifecycleDemo
// looks up by these explicit names rather than a LIKE prefix so a stray
// real project named `research-foo` can coexist on the same hub.
const (
	lifecycleProjectIdea       = "research-idea-demo"
	lifecycleProjectLitReview  = "research-litreview-demo"
	lifecycleProjectMethod     = "research-method-demo"
	lifecycleProjectExperiment = "research-experiment-demo"
	lifecycleProjectPaper      = "research-paper-demo"

	lifecycleTemplateID = "research"
)

// lifecycleDemoNames lists the five seeded project names in canonical
// phase order. Used by Reset + tests; the seed pass derives the names
// from a more structured table below.
var lifecycleDemoNames = []string{
	lifecycleProjectIdea,
	lifecycleProjectLitReview,
	lifecycleProjectMethod,
	lifecycleProjectExperiment,
	lifecycleProjectPaper,
}

// ResetLifecycleDemo deletes every research-*-demo project + its
// dependent rows (deliverables, components, criteria, plans, agents,
// attention, audit). Safe to call when no demo exists; returns
// deleted=true iff at least one project was wiped.
func ResetLifecycleDemo(ctx context.Context, db *sql.DB) (deleted bool, err error) {
	if db == nil {
		return false, fmt.Errorf("ResetLifecycleDemo: nil db")
	}
	rows, err := db.QueryContext(ctx,
		`SELECT id FROM projects WHERE team_id = ? AND name IN (?,?,?,?,?)`,
		defaultTeamID,
		lifecycleProjectIdea, lifecycleProjectLitReview,
		lifecycleProjectMethod, lifecycleProjectExperiment,
		lifecycleProjectPaper)
	if err != nil {
		return false, fmt.Errorf("lookup lifecycle demos: %w", err)
	}
	var projectIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return false, err
		}
		projectIDs = append(projectIDs, id)
	}
	rows.Close()
	if len(projectIDs) == 0 {
		return false, nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	// Per-project child rows. acceptance_criteria, deliverable_components,
	// deliverables CASCADE off projects (migration 0034); plans, attention,
	// documents, artifacts, runs do not. Walk explicitly so the seed +
	// reset paths are symmetric.
	perProject := []string{
		`DELETE FROM acceptance_criteria WHERE project_id = ?`,
		`DELETE FROM deliverable_components
		 WHERE deliverable_id IN (SELECT id FROM deliverables WHERE project_id = ?)`,
		`DELETE FROM deliverables WHERE project_id = ?`,
		`DELETE FROM attention_items WHERE project_id = ?`,
		`DELETE FROM documents WHERE project_id = ?`,
		`DELETE FROM artifacts WHERE project_id = ?`,
		`DELETE FROM runs WHERE project_id = ?`,
		`DELETE FROM plan_steps
		 WHERE plan_id IN (SELECT id FROM plans WHERE project_id = ?)`,
		`DELETE FROM plans WHERE project_id = ?`,
		`DELETE FROM tasks WHERE project_id = ?`,
		`DELETE FROM projects WHERE id = ?`,
	}
	for _, pid := range projectIDs {
		for _, q := range perProject {
			if _, err := tx.ExecContext(ctx, q, pid); err != nil {
				return false, fmt.Errorf("reset (%s): %w", pid, err)
			}
		}
	}

	// Demo-scoped agents (handle prefix '@lifecycle-') aren't keyed off
	// project_id; clean by handle convention. Same prefix used by every
	// research-*-demo seed, so this is one statement, not five.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM agents WHERE team_id = ? AND handle LIKE '@lifecycle-%'`,
		defaultTeamID); err != nil {
		return false, fmt.Errorf("reset lifecycle agents: %w", err)
	}

	// Audit rows the seed authored land with actor_handle='steward.lifecycle'
	// so they're identifiable independent of project_id (which the per-
	// project DELETE above already swept their target rows for).
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM audit_events
		 WHERE team_id = ? AND actor_handle = 'steward.lifecycle'`,
		defaultTeamID); err != nil {
		return false, fmt.Errorf("reset lifecycle audit_events: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit reset: %w", err)
	}
	return true, nil
}

// SeedLifecycleDemo inserts the five-project research portfolio. If
// the first-phase project (research-idea-demo) already exists, the
// whole seed is treated as already-done and Skipped=true is returned;
// the caller is expected to pass `--reset` for a refresh.
//
// `dataRoot` is the blob-storage root (matches `Server.cfg.DataRoot`).
// When non-empty, the seed writes deterministic citation bytes via
// `insertDemoBlob` so the W3 References tile resolves to a real
// tabular viewer payload. Tests that don't care about renderable
// citations may pass an empty string — the seed falls back to mock
// URIs in that case.
func SeedLifecycleDemo(ctx context.Context, db *sql.DB, dataRoot string) (*SeedLifecycleResult, error) {
	if db == nil {
		return nil, fmt.Errorf("SeedLifecycleDemo: nil db")
	}

	// Idempotency check — keyed off the idea-phase project. If the
	// portfolio is half-seeded for some reason, the caller should reset
	// rather than partial-insert; the alternative (per-row IGNORE) hides
	// drift between the seed code and the on-disk state.
	var existingID string
	err := db.QueryRowContext(ctx,
		`SELECT id FROM projects WHERE team_id = ? AND name = ?`,
		defaultTeamID, lifecycleProjectIdea).Scan(&existingID)
	if err == nil {
		return &SeedLifecycleResult{
			IdeaProjectID: existingID,
			Skipped:       true,
		}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("check existing lifecycle demo: %w", err)
	}
	if err := ensureTeam(ctx, db, defaultTeamID, "default"); err != nil {
		return nil, fmt.Errorf("ensure team: %w", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	res := &SeedLifecycleResult{
		CriteriaByState: map[string]int{},
	}
	now := NowUTC()

	for _, spec := range lifecycleSpecs() {
		ctxRes, err := seedLifecycleProject(ctx, tx, spec, now, dataRoot)
		if err != nil {
			return nil, fmt.Errorf("seed %s: %w", spec.name, err)
		}
		switch spec.phase {
		case "idea":
			res.IdeaProjectID = ctxRes.projectID
		case "lit-review":
			res.LitReviewProjectID = ctxRes.projectID
		case "method":
			res.MethodProjectID = ctxRes.projectID
		case "experiment":
			res.ExperimentProjectID = ctxRes.projectID
		case "paper":
			res.PaperProjectID = ctxRes.projectID
		}
		res.ProjectIDs = append(res.ProjectIDs, ctxRes.projectID)
		res.StewardAgentIDs = append(res.StewardAgentIDs, ctxRes.stewardID)
		res.DeliverableCount += ctxRes.deliverables
		res.CriterionCount += ctxRes.criteria
		for k, v := range ctxRes.byState {
			res.CriteriaByState[k] += v
		}
		res.DocumentCount += ctxRes.documents
		res.ArtifactCount += ctxRes.artifacts
		res.RunCount += ctxRes.runs
		res.AnnotationCount += ctxRes.annotations
		res.TaskCount += ctxRes.tasks
		res.AttentionItemCount += ctxRes.attentionItems
		res.AuditCount += ctxRes.audits
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit lifecycle seed: %w", err)
	}
	return res, nil
}

// lifecycleSpec describes one of the five seeded projects: which phase
// it lives in, which deliverables/criteria/components belong on it, and
// what role-specific copy the UI surfaces should display.
type lifecycleSpec struct {
	name        string
	phase       string
	idea        string // project goal / parameters_json idea
	pastPhases  []string
	stewardKind string
	workerKinds []string

	// Builders for per-phase content. Each function appends to the
	// transaction and returns counters for the result summary. The
	// builders all carry the project + steward IDs (via the ctx
	// pointer) so they can attach rows to the right parent and bump
	// the doc/artifact/run counters.
	deliverables func(*seedProjectCtx) ([]seededDeliverable, error)
	criteria     func(*seedProjectCtx, []seededDeliverable) ([]seededCriterion, error)
	attention    func(*seedProjectCtx) lifecycleAttention
	planSteps    []lifecyclePlanStep
	tasks        []lifecycleTaskSeed
}

// lifecycleAttention is the per-spec payload for the seeded attention
// item. Payload is non-nil only when the kind needs structured data
// (ADR-020 W2: revision_requested carries deliverable_id +
// annotation_ids in pending_payload_json).
type lifecycleAttention struct {
	kind    string
	summary string
	payload map[string]any
}

// lifecyclePlanStep describes one real work unit inside the project's
// plan. `phase` resolves to a phase_idx via lifecyclePhaseIdx; the step
// kind MUST be one of planStepKinds (agent_spawn | llm_call | shell |
// mcp_call | human_decision) — chassis validators reject anything else,
// and the seed has to model what real plans look like, not a phase
// mirror. step_idx within a phase is assigned by insertion order.
type lifecyclePlanStep struct {
	phase  string
	kind   string         // schema-valid; in planStepKinds
	spec   map[string]any // becomes spec_json
	status string         // pending | in_progress | completed
}

// lifecyclePhaseOrder is the canonical phase order for the research
// template — matches the spec_json `phases` slice the plan row stores.
var lifecyclePhaseOrder = []string{"idea", "lit-review", "method", "experiment", "paper"}

// lifecyclePhaseIdx maps a phase name to its phase_idx for the
// plan_steps insert. Unknown phases yield -1; the seed treats that as
// a hard error (we're not interpreting user input here).
func lifecyclePhaseIdx(phase string) int {
	for i, p := range lifecyclePhaseOrder {
		if p == phase {
			return i
		}
	}
	return -1
}

// lifecycleTaskSeed is a kanban task seeded against the demo project.
// No phase column (tasks are project-scoped, not phase-scoped — see
// docs/reference/glossary.md). `parentIdx` is the index of the parent
// in the same slice, or -1 for top-level tasks.
type lifecycleTaskSeed struct {
	title     string
	body      string
	status    string // todo | in_progress | done
	parentIdx int
}

type seedProjectCtx struct {
	tx        *sql.Tx
	ctx       context.Context
	projectID string
	stewardID string
	now       string
	phase     string

	// Blob-storage root for the running hub instance. When empty, the
	// seed must skip helpers that need to write real bytes (citation
	// tabular artifacts, etc.) and emit mock URIs instead.
	dataRoot string

	// Side-effect counters: bumped by the typed-document / artifact /
	// run helpers so the per-project pass can roll them up into the
	// SeedLifecycleResult without threading return values.
	documentsSeeded   int
	artifactsSeeded   int
	runsSeeded        int
	annotationsSeeded int

	// IDs of every annotation seeded against this project's docs, in
	// insertion order. The `attention` closure can read this when the
	// payload references annotation IDs (ADR-020 W2: revision_requested).
	annotationIDs []string

	// Deliverables seeded for this project, exposed by logID so the
	// attention closure can reference them by name without re-walking
	// the slice.
	deliverableByLogID map[string]string
}

type seededDeliverable struct {
	id    string
	logID string // matches the YAML logical id (e.g. "lit-review-doc")
	phase string
	kind  string
	state string
}

type seededCriterion struct {
	id    string
	state string
	kind  string
}

// lifecycleSpecs builds the per-project specs. Defined as a function
// (not a const) so tests can call it without importing private state
// and so the closures inside have a clear lexical scope.
func lifecycleSpecs() []lifecycleSpec {
	return []lifecycleSpec{
		{
			name:        lifecycleProjectIdea,
			phase:       "idea",
			idea:        "Compare Lion vs AdamW on tiny GPT pretraining; does Lion's advantage hold across model sizes?",
			pastPhases:  nil,
			stewardKind: "steward.research.v1",
			workerKinds: nil,
			planSteps: []lifecyclePlanStep{
				// Idea phase = conversation-first. One real step in flight
				// (steward framing the question), one pending decision.
				{"idea", "llm_call",
					map[string]any{"prompt": "Frame the question and propose a scoped hypothesis"},
					"in_progress"},
				{"idea", "human_decision",
					map[string]any{"prompt": "Director ratifies the scope and direction"},
					"pending"},
				// Future-phase scaffolding — pending until the project
				// advances. Each is one canonical opener; the steward
				// will expand these via plans.steps.create as it goes.
				{"lit-review", "agent_spawn",
					map[string]any{"handle": "lit-reviewer", "template": "lit-reviewer.v1"},
					"pending"},
				{"method", "llm_call",
					map[string]any{"prompt": "Draft an experimental method proposal"},
					"pending"},
				{"experiment", "agent_spawn",
					map[string]any{"handle": "ml-worker", "template": "ml-worker.v1"},
					"pending"},
				{"paper", "agent_spawn",
					map[string]any{"handle": "paper-writer", "template": "paper-writer.v1"},
					"pending"},
			},
			tasks: []lifecycleTaskSeed{
				{title: "Sketch out the question on the whiteboard",
					body: "Capture the framing before the steward formalises it.",
					status: "in_progress", parentIdx: -1},
				{title: "Decide whether to compare Lion vs AdamW only or include Sophia",
					status: "todo", parentIdx: -1},
			},
			deliverables: func(c *seedProjectCtx) ([]seededDeliverable, error) {
				return nil, nil // idea phase declares no deliverables
			},
			criteria: func(c *seedProjectCtx, _ []seededDeliverable) ([]seededCriterion, error) {
				return seedCriteria(c, []criterionSpec{
					{
						logID:    "scope-ratified",
						phase:    "idea",
						kind:     "text",
						body:     map[string]any{"text": "Director ratifies overall scope and direction."},
						state:    "pending",
						required: true,
					},
				})
			},
			attention: func(c *seedProjectCtx) lifecycleAttention {
				return lifecycleAttention{kind: "select",
					summary: "Director: ratify scope and direction so the steward can spawn a literature reviewer."}
			},
		},

		{
			name:        lifecycleProjectLitReview,
			phase:       "lit-review",
			idea:        "Survey of mixture-of-depth transformer routing strategies for small models.",
			pastPhases:  []string{"idea"},
			stewardKind: "steward.research.v1",
			workerKinds: []string{"lit-reviewer.v1"},
			planSteps: []lifecyclePlanStep{
				{"idea", "human_decision",
					map[string]any{"prompt": "Director ratified scope and direction"},
					"completed"},
				{"lit-review", "agent_spawn",
					map[string]any{"handle": "lit-reviewer", "template": "lit-reviewer.v1"},
					"completed"},
				{"lit-review", "llm_call",
					map[string]any{"prompt": "Summarise prior work and identify research gaps"},
					"in_progress"},
				{"lit-review", "human_decision",
					map[string]any{"prompt": "Director ratifies the literature review document"},
					"pending"},
				{"method", "llm_call",
					map[string]any{"prompt": "Draft a method proposal informed by the lit-review gaps"},
					"pending"},
				{"experiment", "agent_spawn",
					map[string]any{"handle": "ml-worker", "template": "ml-worker.v1"},
					"pending"},
				{"paper", "agent_spawn",
					map[string]any{"handle": "paper-writer", "template": "paper-writer.v1"},
					"pending"},
			},
			tasks: []lifecycleTaskSeed{
				{title: "Triage open redlines on the lit-review draft",
					body: "Two annotations are open on the `gaps` section; resolve before ratifying.",
					status: "in_progress", parentIdx: -1},
				{title: "Confirm Lin et al. 2025 §3.2 is the right citation",
					status: "todo", parentIdx: 0},
				{title: "Verify lit-reviewer covered MoD routing strategies",
					status: "done", parentIdx: -1},
			},
			deliverables: func(c *seedProjectCtx) ([]seededDeliverable, error) {
				doc, err := seedTypedDocument(c,
					"Literature review",
					"research-lit-review-v1",
					[]sectionSeed{
						{slug: "domain-overview", title: "Domain overview", status: "ratified",
							body: domainOverviewBody()},
						{slug: "prior-work", title: "Key prior work", status: "ratified",
							body: priorWorkBody()},
						{slug: "gaps", title: "Research gaps", status: "draft",
							body: gapsDraftBody()},
						{slug: "positioning", title: "Project positioning", status: "empty"},
					})
				if err != nil {
					return nil, err
				}
				// ADR-020 W1: 3 director annotations on the lit-review doc
				// (one per kind), so the dress-rehearsal exercises the
				// overlay's redline / suggestion / question glyphs and the
				// open / resolved filter.
				for _, a := range []annotationSeed{
					{docID: doc.id, section: "gaps", kind: "redline",
						body: "Trim the second paragraph — it duplicates what's already in domain-overview."},
					{docID: doc.id, section: "gaps", kind: "suggestion",
						body: "Replace 'recent work' with a concrete pointer to Lin et al. 2025 §3.2."},
					{docID: doc.id, section: "prior-work", kind: "question", status: "resolved",
						body: "Did we cite Hu et al. 2024? Looks adjacent to the routing axis."},
				} {
					if err := seedAnnotation(c, a); err != nil {
						return nil, err
					}
				}
				// Wave 2 W3: also seed the tabular citation artifact for
				// THIS project — testers opening the lit-review-demo
				// project expect References → table of papers, but
				// pre-v1.0.512 only the method-demo project had the
				// citation wired in, so the tile here fell back to the
				// document-only deliverable view.
				citationArt, err := seedCitationArtifact(c, demoCitations())
				if err != nil {
					return nil, err
				}
				litComponents := []componentSpec{
					{kind: "document", refID: doc.id, ord: 0},
				}
				if citationArt.id != "" {
					litComponents = append(litComponents,
						componentSpec{kind: "artifact", refID: citationArt.id, ord: 1})
				}
				return seedDeliverables(c, []deliverableSpec{
					{
						logID:      "lit-review-doc",
						phase:      "lit-review",
						kind:       "lit-review",
						state:      "in-review",
						components: litComponents,
					},
				})
			},
			criteria: func(c *seedProjectCtx, dls []seededDeliverable) ([]seededCriterion, error) {
				deliv := findDeliverableByLogID(dls, "lit-review-doc")
				return seedCriteria(c, []criterionSpec{
					{
						logID:        "lit-review-ratified",
						phase:        "lit-review",
						kind:         "gate",
						deliverableID: deliv,
						body: map[string]any{
							"gate":   "deliverable.ratified",
							"params": map[string]any{"deliverable_id": deliv},
						},
						state:    "pending",
						required: true,
					},
					{
						logID:        "min-citations",
						phase:        "lit-review",
						kind:         "metric",
						deliverableID: deliv,
						body: map[string]any{
							"metric":     "lit_review.citation_count",
							"operator":   ">=",
							"threshold":  5,
							"evaluation": "auto",
							"observed":   8,
						},
						state:        "met",
						evidenceRef:  "metric:lit_review.citation_count=8",
						required:     false,
					},
				})
			},
			attention: func(c *seedProjectCtx) lifecycleAttention {
				// ADR-020 W2 — director sent the lit-review draft back
				// with notes (3 annotations seeded in this spec). The
				// steward's prompt overlay teaches it to read the note,
				// walk the linked annotations, and address each before
				// flipping the deliverable to ratified.
				return lifecycleAttention{
					kind:    "revision_requested",
					summary: "Revision requested · Tighten the gaps section; address the prior-work redline before ratifying.",
					payload: map[string]any{
						"deliverable_id": c.deliverableByLogID["lit-review-doc"],
						"note":           "Tighten the gaps section — the second paragraph duplicates domain-overview. Drop the citations stub and replace with a concrete pointer to Lin et al. 2025.",
						"annotation_ids": append([]string{}, c.annotationIDs...),
					},
				}
			},
		},

		{
			name:        lifecycleProjectMethod,
			phase:       "method",
			idea:        "Replicate paper X with adjusted hyperparameters; falsify or confirm the headline finding.",
			pastPhases:  []string{"idea", "lit-review"},
			stewardKind: "steward.research.v1",
			workerKinds: []string{"critic.v1"},
			planSteps: []lifecyclePlanStep{
				{"idea", "human_decision",
					map[string]any{"prompt": "Director ratified scope"},
					"completed"},
				{"lit-review", "agent_spawn",
					map[string]any{"handle": "lit-reviewer", "template": "lit-reviewer.v1"},
					"completed"},
				{"lit-review", "human_decision",
					map[string]any{"prompt": "Director ratified literature review"},
					"completed"},
				{"method", "llm_call",
					map[string]any{"prompt": "Draft the experimental method document"},
					"completed"},
				{"method", "agent_spawn",
					map[string]any{"handle": "critic", "template": "critic.v1",
						"task": "Red-team the method proposal"},
					"in_progress"},
				{"method", "human_decision",
					map[string]any{"prompt": "Director ratifies the method document"},
					"pending"},
				{"experiment", "agent_spawn",
					map[string]any{"handle": "ml-worker", "template": "ml-worker.v1"},
					"pending"},
				{"experiment", "agent_spawn",
					map[string]any{"handle": "coder", "template": "coder.v1",
						"task": "Prepare data + eval harness"},
					"pending"},
				{"paper", "agent_spawn",
					map[string]any{"handle": "paper-writer", "template": "paper-writer.v1"},
					"pending"},
			},
			tasks: []lifecycleTaskSeed{
				{title: "Address critic.v1's red-team findings on the method doc",
					body: "Critic flagged two assumptions; reconcile before ratification.",
					status: "in_progress", parentIdx: -1},
				{title: "Lock the eval metric set (loss + accuracy + token-count)",
					status: "todo", parentIdx: -1},
				{title: "Pre-register the experiment with the team's MLflow",
					status: "todo", parentIdx: -1},
				{title: "Confirm budget cap with director",
					status: "done", parentIdx: -1},
			},
			deliverables: func(c *seedProjectCtx) ([]seededDeliverable, error) {
				// Carry-over: a ratified lit-review doc from the prior phase.
				litDoc, err := seedTypedDocument(c,
					"Literature review",
					"research-lit-review-v1",
					[]sectionSeed{
						{slug: "domain-overview", title: "Domain overview", status: "ratified",
							body: domainOverviewBody()},
						{slug: "prior-work", title: "Key prior work", status: "ratified",
							body: priorWorkBody()},
						{slug: "gaps", title: "Research gaps", status: "ratified",
							body: gapsRatifiedBody()},
						{slug: "positioning", title: "Project positioning", status: "ratified",
							body: positioningBody()},
					})
				if err != nil {
					return nil, err
				}
				methDoc, err := seedTypedDocument(c,
					"Method",
					"research-method-v1",
					methodSectionSeeds())
				if err != nil {
					return nil, err
				}
				// ADR-020 W1: 2 director annotations on the in-flight method
				// doc — both anchored to the still-draft evaluation-plan
				// section where the director would naturally push back.
				for _, a := range []annotationSeed{
					{docID: methDoc.id, section: "evaluation-plan", kind: "comment",
						body: "We should also report the trajectory at iter 500 — late-only is too lossy for the demo."},
					{docID: methDoc.id, section: "evaluation-plan", kind: "redline",
						body: "Drop the 'intermediate-step deltas' hedge — pick one and commit."},
				} {
					if err := seedAnnotation(c, a); err != nil {
						return nil, err
					}
				}
				// Wave 2 W3: lit-review deliverables gain a structured
				// References component (tabular citation artifact) so the
				// References tile can render a real table instead of
				// falling back to DocumentsScreen.
				citationArt, err := seedCitationArtifact(c, demoCitations())
				if err != nil {
					return nil, err
				}
				litComponents := []componentSpec{{kind: "document", refID: litDoc.id, ord: 0}}
				if citationArt.id != "" {
					litComponents = append(litComponents,
						componentSpec{kind: "artifact", refID: citationArt.id, ord: 1})
				}
				return seedDeliverables(c, []deliverableSpec{
					{
						logID:      "lit-review-doc",
						phase:      "lit-review",
						kind:       "lit-review",
						state:      "ratified",
						components: litComponents,
					},
					{
						logID: "method-doc",
						phase: "method",
						kind:  "method",
						state: "ratified",
						components: []componentSpec{
							{kind: "document", refID: methDoc.id, ord: 0},
							// Method deliverables include the commit
							// that locks the protocol code (training
							// loop + eval harness) so reviewers can
							// trace the run back to a known revision.
							{kind: "commit", ord: 1,
								refID: "https://github.com/example-org/optimizer-research/commit/9a2bf1c0d3e4f0a7b2c5d8e9f1a3b4c5d6e7f8a9"},
						},
					},
				})
			},
			criteria: func(c *seedProjectCtx, dls []seededDeliverable) ([]seededCriterion, error) {
				lit := findDeliverableByLogID(dls, "lit-review-doc")
				meth := findDeliverableByLogID(dls, "method-doc")
				return seedCriteria(c, []criterionSpec{
					{
						logID: "lit-review-ratified", phase: "lit-review", kind: "gate",
						deliverableID: lit,
						body: map[string]any{"gate": "deliverable.ratified",
							"params": map[string]any{"deliverable_id": lit}},
						state:        "met",
						evidenceRef:  "deliverable.ratified:" + lit,
						required:     true,
					},
					{
						logID: "min-citations", phase: "lit-review", kind: "metric",
						deliverableID: lit,
						body: map[string]any{"metric": "lit_review.citation_count",
							"operator": ">=", "threshold": 5, "evaluation": "auto", "observed": 11},
						state:        "met",
						evidenceRef:  "metric:lit_review.citation_count=11",
						required:     false,
					},
					{
						logID: "method-ratified", phase: "method", kind: "gate",
						deliverableID: meth,
						body: map[string]any{"gate": "deliverable.ratified",
							"params": map[string]any{"deliverable_id": meth}},
						state:        "met",
						evidenceRef:  "deliverable.ratified:" + meth,
						required:     true,
					},
					{
						logID: "budget-within-cap", phase: "method", kind: "text",
						deliverableID: meth,
						body: map[string]any{
							"text": "Budget cap declared; total estimated cost under budget."},
						state:        "met",
						evidenceRef:  "approved:director",
						required:     true,
					},
				})
			},
			attention: func(c *seedProjectCtx) lifecycleAttention {
				return lifecycleAttention{kind: "select",
					summary: "Method ratified. Steward will spawn ml-worker + coder when you advance to experiment."}
			},
		},

		{
			name:        lifecycleProjectExperiment,
			phase:       "experiment",
			idea:        "Sweep nanoGPT optimizers across three sizes; confirm the best-metric threshold.",
			pastPhases:  []string{"idea", "lit-review", "method"},
			stewardKind: "steward.research.v1",
			workerKinds: []string{"ml-worker.v1", "coder.v1", "critic.v1"},
			planSteps: []lifecyclePlanStep{
				{"idea", "human_decision",
					map[string]any{"prompt": "Director ratified scope"},
					"completed"},
				{"lit-review", "agent_spawn",
					map[string]any{"handle": "lit-reviewer", "template": "lit-reviewer.v1"},
					"completed"},
				{"lit-review", "human_decision",
					map[string]any{"prompt": "Director ratified literature review"},
					"completed"},
				{"method", "llm_call",
					map[string]any{"prompt": "Draft method document"},
					"completed"},
				{"method", "agent_spawn",
					map[string]any{"handle": "critic", "template": "critic.v1"},
					"completed"},
				{"method", "human_decision",
					map[string]any{"prompt": "Director ratified method"},
					"completed"},
				{"experiment", "agent_spawn",
					map[string]any{"handle": "ml-worker", "template": "ml-worker.v1",
						"task": "Sweep optimizers across three model sizes"},
					"in_progress"},
				{"experiment", "agent_spawn",
					map[string]any{"handle": "coder", "template": "coder.v1",
						"task": "Wire eval harness + post-processing"},
					"in_progress"},
				{"experiment", "shell",
					map[string]any{"cmd": "python train.py --config sweep-384-lion --iters 1000"},
					"in_progress"},
				{"experiment", "agent_spawn",
					map[string]any{"handle": "critic", "template": "critic.v1",
						"task": "Interpret anomalies in the loss trajectory"},
					"pending"},
				{"experiment", "human_decision",
					map[string]any{"prompt": "Director reviews results and ratifies or requests revisions"},
					"pending"},
				{"paper", "agent_spawn",
					map[string]any{"handle": "paper-writer", "template": "paper-writer.v1"},
					"pending"},
			},
			tasks: []lifecycleTaskSeed{
				{title: "Babysit the 384/Lion sweep — abort if loss diverges past iter 500",
					body: "Director set the abort threshold; watch the eval curve.",
					status: "in_progress", parentIdx: -1},
				{title: "Tag the training revision once the run lands",
					status: "todo", parentIdx: -1},
				{title: "Decide whether the 768-d sweep needs a separate budget",
					status: "todo", parentIdx: -1},
				{title: "Cleanup checkpoint storage from the 256-d preliminary run",
					status: "done", parentIdx: -1},
			},
			deliverables: func(c *seedProjectCtx) ([]seededDeliverable, error) {
				litDoc, err := seedTypedDocument(c, "Literature review",
					"research-lit-review-v1", litReviewSectionSeedsRatified())
				if err != nil {
					return nil, err
				}
				methDoc, err := seedTypedDocument(c, "Method",
					"research-method-v1", methodSectionSeedsRatified())
				if err != nil {
					return nil, err
				}
				expDoc, err := seedTypedDocument(c, "Experiment report (draft)",
					"research-experiment-report-v1", experimentReportDraftSeeds())
				if err != nil {
					return nil, err
				}
				// Multi-run experiment phase (plans/multi-run-experiment-phase.md).
				// Each sweep config produces one run + one checkpoint +
				// one per-run metric-chart; the aggregate chart goes in
				// last with a bumped timestamp so the mobile embed's
				// newest-first picker lands on it.
				perRunCharts := make([]seededArtifact, 0, len(defaultSweepConfigs))
				perRunCkpts := make([]seededArtifact, 0, len(defaultSweepConfigs))
				perRuns := make([]seededRun, 0, len(defaultSweepConfigs))
				for _, cfg := range defaultSweepConfigs {
					label := sweepRunLabel(cfg)
					chart, err := seedMetricChartArtifact(c,
						fmt.Sprintf("eval-results-%s.json", label),
						demoPerRunMetricChartBody(cfg), c.now)
					if err != nil {
						return nil, err
					}
					perRunCharts = append(perRunCharts, chart)
					ckpt, err := seedArtifact(c, "external-blob",
						fmt.Sprintf("best-checkpoint-step1000-%s.pt", label),
						"application/octet-stream",
						int64(cfg.nEmbd*4*1024*1024))
					if err != nil {
						return nil, err
					}
					perRunCkpts = append(perRunCkpts, ckpt)
					run, err := seedRun(c, "completed", map[string]any{
						"n_embd":    cfg.nEmbd,
						"optimizer": cfg.optimizer,
						"iters":     1000,
					})
					if err != nil {
						return nil, err
					}
					perRuns = append(perRuns, run)
				}
				aggArt, err := seedMetricChartArtifact(c,
					"eval-results-aggregated.json",
					demoAggregateMetricChartBody(defaultSweepConfigs),
					timeAfter(c.now, 1))
				if err != nil {
					return nil, err
				}
				// W5 run-bundle: 3-file python scaffold (train.py +
				// config.py + README.md) attached to the experiment-
				// results deliverable. Gives reviewers something to read
				// alongside the commit URL + checkpoint blob.
				bundleArt, err := seedCodeBundleArtifact(c, demoRunBundle(),
					"run-bundle.json")
				if err != nil {
					return nil, err
				}
				// canvas-viewer plan W3: interactive SVG line chart over
				// the same eval data the metric-chart artifact carries.
				// Lets testers exercise the WebView sandbox + tap-to-
				// inspect interaction without an agent emitting one.
				canvasArt, err := seedCanvasArtifact(c, demoCanvasBundle(),
					"eval-curve.html")
				if err != nil {
					return nil, err
				}
				// Wave 2 W2 + W4 viewer coverage: tiny PDF + tiny PNG
				// attached so testers can exercise pdfrx + the image
				// viewer via the demo path. Audio + video are
				// upload-only by design; diagram has no viewer yet.
				pdfArt, err := seedPdfArtifact(c, "experiment-summary.pdf")
				if err != nil {
					return nil, err
				}
				imageArt, err := seedImageArtifact(c, "loss-curve.png")
				if err != nil {
					return nil, err
				}
				expComponents := []componentSpec{
					{kind: "document", refID: expDoc.id, ord: 0},
					// Aggregate chart first in display order — it's the
					// headline view of the sweep.
					{kind: "artifact", refID: aggArt.id, ord: 1},
				}
				ord := 2
				for _, art := range perRunCharts {
					expComponents = append(expComponents,
						componentSpec{kind: "artifact", refID: art.id, ord: ord})
					ord++
				}
				for _, art := range perRunCkpts {
					expComponents = append(expComponents,
						componentSpec{kind: "artifact", refID: art.id, ord: ord})
					ord++
				}
				expComponents = append(expComponents,
					componentSpec{kind: "artifact", refID: bundleArt.id, ord: ord})
				ord++
				expComponents = append(expComponents,
					componentSpec{kind: "artifact", refID: canvasArt.id, ord: ord})
				ord++
				expComponents = append(expComponents,
					componentSpec{kind: "artifact", refID: pdfArt.id, ord: ord})
				ord++
				expComponents = append(expComponents,
					componentSpec{kind: "artifact", refID: imageArt.id, ord: ord})
				ord++
				for _, r := range perRuns {
					expComponents = append(expComponents,
						componentSpec{kind: "run", refID: r.id, ord: ord})
					ord++
				}
				expComponents = append(expComponents, componentSpec{
					kind: "commit", ord: ord,
					// The training revision that produced these runs —
					// paired with the run configs so reviewers can
					// rebuild any sweep entry from source.
					refID: "https://github.com/example-org/optimizer-research/commit/c4d5e6f78a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d",
				})
				return seedDeliverables(c, []deliverableSpec{
					{logID: "lit-review-doc", phase: "lit-review", kind: "lit-review",
						state:      "ratified",
						components: []componentSpec{{kind: "document", refID: litDoc.id}}},
					{logID: "method-doc", phase: "method", kind: "method",
						state: "ratified",
						components: []componentSpec{
							{kind: "document", refID: methDoc.id, ord: 0},
							{kind: "commit", ord: 1,
								refID: "https://github.com/example-org/optimizer-research/commit/9a2bf1c0d3e4f0a7b2c5d8e9f1a3b4c5d6e7f8a9"},
						}},
					{logID: "experiment-results", phase: "experiment", kind: "experiment-results",
						state:      "draft",
						components: expComponents},
				})
			},
			criteria: func(c *seedProjectCtx, dls []seededDeliverable) ([]seededCriterion, error) {
				lit := findDeliverableByLogID(dls, "lit-review-doc")
				meth := findDeliverableByLogID(dls, "method-doc")
				exp := findDeliverableByLogID(dls, "experiment-results")
				return seedCriteria(c, []criterionSpec{
					{logID: "lit-review-ratified", phase: "lit-review", kind: "gate",
						deliverableID: lit,
						body: map[string]any{"gate": "deliverable.ratified",
							"params": map[string]any{"deliverable_id": lit}},
						state: "met", evidenceRef: "deliverable.ratified:" + lit, required: true},
					{logID: "method-ratified", phase: "method", kind: "gate",
						deliverableID: meth,
						body: map[string]any{"gate": "deliverable.ratified",
							"params": map[string]any{"deliverable_id": meth}},
						state: "met", evidenceRef: "deliverable.ratified:" + meth, required: true},
					{logID: "budget-within-cap", phase: "method", kind: "text",
						deliverableID: meth,
						body:          map[string]any{"text": "Budget cap declared; total estimated cost under budget."},
						state: "met", evidenceRef: "approved:director", required: true},
					{logID: "best-metric-threshold", phase: "experiment", kind: "metric",
						deliverableID: exp,
						body: map[string]any{
							"metric": "experiment.eval_accuracy", "operator": ">=",
							"threshold": 0.85, "evaluation": "auto", "observed": 0.892},
						state: "met", evidenceRef: "metric:experiment.eval_accuracy=0.892",
						required: true},
					{logID: "report-results-ratified", phase: "experiment", kind: "gate",
						deliverableID: exp,
						body: map[string]any{"gate": "deliverable.ratified",
							"params": map[string]any{"deliverable_id": exp}},
						state: "pending", required: true},
					{logID: "director-reviews", phase: "experiment", kind: "text",
						deliverableID: exp,
						body:          map[string]any{"text": "Director reviews experimental outputs and signs off."},
						state:         "failed",
						evidenceRef:   "director: needs another sweep — request revisions",
						required:      true},
				})
			},
			attention: func(c *seedProjectCtx) lifecycleAttention {
				return lifecycleAttention{kind: "select",
					summary: "Experiment results draft is ready. Inspect the report + checkpoint, then ratify or request revisions."}
			},
		},

		{
			name:        lifecycleProjectPaper,
			phase:       "paper",
			idea:        "Write the paper draft for the optimizer-comparison results.",
			pastPhases:  []string{"idea", "lit-review", "method", "experiment"},
			stewardKind: "steward.research.v1",
			workerKinds: []string{"paper-writer.v1", "critic.v1"},
			planSteps: []lifecyclePlanStep{
				{"idea", "human_decision",
					map[string]any{"prompt": "Director ratified scope"},
					"completed"},
				{"lit-review", "agent_spawn",
					map[string]any{"handle": "lit-reviewer", "template": "lit-reviewer.v1"},
					"completed"},
				{"lit-review", "human_decision",
					map[string]any{"prompt": "Director ratified literature review"},
					"completed"},
				{"method", "llm_call",
					map[string]any{"prompt": "Draft method"},
					"completed"},
				{"method", "agent_spawn",
					map[string]any{"handle": "critic", "template": "critic.v1"},
					"completed"},
				{"method", "human_decision",
					map[string]any{"prompt": "Director ratified method"},
					"completed"},
				{"experiment", "agent_spawn",
					map[string]any{"handle": "ml-worker", "template": "ml-worker.v1"},
					"completed"},
				{"experiment", "shell",
					map[string]any{"cmd": "python train.py --config sweep-384-lion --iters 1000"},
					"completed"},
				{"experiment", "human_decision",
					map[string]any{"prompt": "Director ratified experiment results"},
					"completed"},
				{"paper", "agent_spawn",
					map[string]any{"handle": "paper-writer", "template": "paper-writer.v1"},
					"completed"},
				{"paper", "llm_call",
					map[string]any{"prompt": "Draft introduction + related work sections"},
					"completed"},
				{"paper", "llm_call",
					map[string]any{"prompt": "Draft results + analysis sections"},
					"in_progress"},
				{"paper", "agent_spawn",
					map[string]any{"handle": "critic", "template": "critic.v1",
						"task": "Review the draft for over-claims and missing citations"},
					"pending"},
				{"paper", "human_decision",
					map[string]any{"prompt": "Director ratifies the final paper draft"},
					"pending"},
			},
			tasks: []lifecycleTaskSeed{
				{title: "Pick figure 3 caption — keep both candidates?",
					body: "Paper-writer surfaced two variants; pick one before critic reviews.",
					status: "in_progress", parentIdx: -1},
				{title: "Confirm the abstract's headline numbers match the eval JSON",
					status: "todo", parentIdx: -1},
				{title: "Schedule the critic review window",
					status: "todo", parentIdx: -1},
				{title: "Export the bib for the camera-ready format",
					status: "todo", parentIdx: -1},
				{title: "Reserve the venue submission slot",
					status: "done", parentIdx: -1},
			},
			deliverables: func(c *seedProjectCtx) ([]seededDeliverable, error) {
				litDoc, err := seedTypedDocument(c, "Literature review",
					"research-lit-review-v1", litReviewSectionSeedsRatified())
				if err != nil {
					return nil, err
				}
				methDoc, err := seedTypedDocument(c, "Method",
					"research-method-v1", methodSectionSeedsRatified())
				if err != nil {
					return nil, err
				}
				expDoc, err := seedTypedDocument(c, "Experiment report",
					"research-experiment-report-v1", experimentReportRatifiedSeeds())
				if err != nil {
					return nil, err
				}
				paperDoc, err := seedTypedDocument(c, "Paper draft",
					"research-paper-draft-v1", paperDraftSeeds())
				if err != nil {
					return nil, err
				}
				// Multi-run experiment phase — paper-phase variant.
				// Identical to the experiment-phase site: 3-config sweep
				// + aggregate chart. The deliverable lands in
				// `ratified` state here (paper writing already started)
				// rather than `draft`, but the component shape matches
				// so the structured viewer renders the same scaffold in
				// both demo projects.
				perRunCharts := make([]seededArtifact, 0, len(defaultSweepConfigs))
				perRunCkpts := make([]seededArtifact, 0, len(defaultSweepConfigs))
				perRuns := make([]seededRun, 0, len(defaultSweepConfigs))
				for _, cfg := range defaultSweepConfigs {
					label := sweepRunLabel(cfg)
					chart, err := seedMetricChartArtifact(c,
						fmt.Sprintf("eval-results-%s.json", label),
						demoPerRunMetricChartBody(cfg), c.now)
					if err != nil {
						return nil, err
					}
					perRunCharts = append(perRunCharts, chart)
					ckpt, err := seedArtifact(c, "external-blob",
						fmt.Sprintf("best-checkpoint-step1000-%s.pt", label),
						"application/octet-stream",
						int64(cfg.nEmbd*4*1024*1024))
					if err != nil {
						return nil, err
					}
					perRunCkpts = append(perRunCkpts, ckpt)
					run, err := seedRun(c, "completed", map[string]any{
						"n_embd":    cfg.nEmbd,
						"optimizer": cfg.optimizer,
						"iters":     1000,
					})
					if err != nil {
						return nil, err
					}
					perRuns = append(perRuns, run)
				}
				aggArt, err := seedMetricChartArtifact(c,
					"eval-results-aggregated.json",
					demoAggregateMetricChartBody(defaultSweepConfigs),
					timeAfter(c.now, 1))
				if err != nil {
					return nil, err
				}
				// W5 run-bundle on the ratified experiment-results (paper
				// phase variant). Same shape as the experiment-phase
				// variant above so the W5 viewer renders the same scaffold
				// regardless of which demo project the tester opens.
				bundleArt, err := seedCodeBundleArtifact(c, demoRunBundle(),
					"run-bundle.json")
				if err != nil {
					return nil, err
				}
				// canvas-viewer plan W3 — same interactive chart as the
				// experiment-phase variant. Identical bundle so the
				// content-addressed blob dedups across both demo projects.
				canvasArt, err := seedCanvasArtifact(c, demoCanvasBundle(),
					"eval-curve.html")
				if err != nil {
					return nil, err
				}
				// Same PDF + PNG seeds as the experiment-phase variant.
				// Bytes are deterministic so the content-addressed blob
				// dedups across both demo projects.
				pdfArt, err := seedPdfArtifact(c, "experiment-summary.pdf")
				if err != nil {
					return nil, err
				}
				imageArt, err := seedImageArtifact(c, "loss-curve.png")
				if err != nil {
					return nil, err
				}
				expComponents := []componentSpec{
					{kind: "document", refID: expDoc.id, ord: 0},
					{kind: "artifact", refID: aggArt.id, ord: 1},
				}
				ord := 2
				for _, art := range perRunCharts {
					expComponents = append(expComponents,
						componentSpec{kind: "artifact", refID: art.id, ord: ord})
					ord++
				}
				for _, art := range perRunCkpts {
					expComponents = append(expComponents,
						componentSpec{kind: "artifact", refID: art.id, ord: ord})
					ord++
				}
				expComponents = append(expComponents,
					componentSpec{kind: "artifact", refID: bundleArt.id, ord: ord})
				ord++
				expComponents = append(expComponents,
					componentSpec{kind: "artifact", refID: canvasArt.id, ord: ord})
				ord++
				expComponents = append(expComponents,
					componentSpec{kind: "artifact", refID: pdfArt.id, ord: ord})
				ord++
				expComponents = append(expComponents,
					componentSpec{kind: "artifact", refID: imageArt.id, ord: ord})
				ord++
				for _, r := range perRuns {
					expComponents = append(expComponents,
						componentSpec{kind: "run", refID: r.id, ord: ord})
					ord++
				}
				expComponents = append(expComponents, componentSpec{
					kind: "commit", ord: ord,
					refID: "https://github.com/example-org/optimizer-research/commit/c4d5e6f78a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d",
				})
				return seedDeliverables(c, []deliverableSpec{
					{logID: "lit-review-doc", phase: "lit-review", kind: "lit-review",
						state:      "ratified",
						components: []componentSpec{{kind: "document", refID: litDoc.id}}},
					{logID: "method-doc", phase: "method", kind: "method",
						state: "ratified",
						components: []componentSpec{
							{kind: "document", refID: methDoc.id, ord: 0},
							{kind: "commit", ord: 1,
								refID: "https://github.com/example-org/optimizer-research/commit/9a2bf1c0d3e4f0a7b2c5d8e9f1a3b4c5d6e7f8a9"},
						}},
					{logID: "experiment-results", phase: "experiment", kind: "experiment-results",
						state:      "ratified",
						components: expComponents},
					{logID: "paper-draft", phase: "paper", kind: "paper-draft",
						state:      "in-review",
						components: []componentSpec{{kind: "document", refID: paperDoc.id}}},
				})
			},
			criteria: func(c *seedProjectCtx, dls []seededDeliverable) ([]seededCriterion, error) {
				lit := findDeliverableByLogID(dls, "lit-review-doc")
				meth := findDeliverableByLogID(dls, "method-doc")
				exp := findDeliverableByLogID(dls, "experiment-results")
				paper := findDeliverableByLogID(dls, "paper-draft")
				return seedCriteria(c, []criterionSpec{
					{logID: "lit-review-ratified", phase: "lit-review", kind: "gate",
						deliverableID: lit,
						body: map[string]any{"gate": "deliverable.ratified",
							"params": map[string]any{"deliverable_id": lit}},
						state: "met", evidenceRef: "deliverable.ratified:" + lit, required: true},
					{logID: "method-ratified", phase: "method", kind: "gate",
						deliverableID: meth,
						body: map[string]any{"gate": "deliverable.ratified",
							"params": map[string]any{"deliverable_id": meth}},
						state: "met", evidenceRef: "deliverable.ratified:" + meth, required: true},
					{logID: "best-metric-threshold", phase: "experiment", kind: "metric",
						deliverableID: exp,
						body: map[string]any{
							"metric": "experiment.eval_accuracy", "operator": ">=",
							"threshold": 0.85, "evaluation": "auto", "observed": 0.892},
						state: "met", required: true},
					{logID: "report-results-ratified", phase: "experiment", kind: "gate",
						deliverableID: exp,
						body: map[string]any{"gate": "deliverable.ratified",
							"params": map[string]any{"deliverable_id": exp}},
						state: "met", required: true},
					{logID: "paper-draft-ratified", phase: "paper", kind: "gate",
						deliverableID: paper,
						body: map[string]any{"gate": "deliverable.ratified",
							"params": map[string]any{"deliverable_id": paper}},
						// `pending` (not `waived`) so the paper-demo's AC
						// list shows one OPEN criterion — the paper is in
						// review, the ratify gate is genuinely waiting on
						// the deliverable, not deferred by director
						// decision (v1.0.511 tester report).
						state:    "pending",
						required: true,
					},
					// A non-required `waived` criterion alongside, so the
					// AC list still exercises the waived-state UI (test
					// `TestSeedLifecycleDemo_InsertsFivePhaseStagedProjects`
					// expects ≥1 waived across the lifecycle bundle). Real
					// narrative: director chose to skip the supplementary-
					// materials packaging since the venue accepts code-on-
					// GitHub in place of an appendix.
					{logID: "supplementary-materials-packaged", phase: "paper",
						kind: "text",
						deliverableID: paper,
						body: map[string]any{
							"check":     "Supplementary materials archive uploaded",
							"acceptable": "zip of figures + scripts",
						},
						state:       "waived",
						evidenceRef: "director: venue accepts code-on-github in lieu of archive",
						required:    false,
					},
				})
			},
			attention: func(c *seedProjectCtx) lifecycleAttention {
				return lifecycleAttention{kind: "select",
					summary: "Paper draft submitted to internal review. Ratify after reviewer feedback returns."}
			},
		},
	}
}

// ---------------------------------------------------------------------
// Per-project seed pass.
// ---------------------------------------------------------------------

type lifecycleProjectResult struct {
	projectID      string
	stewardID      string
	deliverables   int
	criteria       int
	byState        map[string]int
	documents      int
	artifacts      int
	runs           int
	annotations    int
	tasks          int
	attentionItems int
	audits         int
}

func seedLifecycleProject(
	ctx context.Context, tx *sql.Tx, spec lifecycleSpec, now, dataRoot string,
) (lifecycleProjectResult, error) {
	res := lifecycleProjectResult{byState: map[string]int{}}

	// 1. The project row. parameters_json carries the director's idea
	// just as a real lifecycle project would. Phase column reflects the
	// current phase; phase_history accumulates the from→to transitions
	// that brought the project here so the ribbon can render the trail.
	res.projectID = NewID()
	params, _ := json.Marshal(map[string]any{"idea": spec.idea})
	history := buildPhaseHistory(spec.pastPhases, spec.phase, now)
	historyJSON, _ := json.Marshal(history)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO projects
			(id, team_id, name, status, config_yaml, created_at,
			 goal, kind, is_template, template_id, parameters_json,
			 phase, phase_history)
		VALUES (?, ?, ?, 'active', '', ?, ?, 'goal', 0, ?, ?, ?, ?)`,
		res.projectID, defaultTeamID, spec.name, now,
		"Research lifecycle demo — phase: "+spec.phase,
		lifecycleTemplateID, string(params),
		spec.phase, string(historyJSON)); err != nil {
		return res, fmt.Errorf("insert project: %w", err)
	}

	// 2. Plan + plan_steps.
	planID := NewID()
	planSpecJSON, _ := json.Marshal(map[string]any{
		"template": lifecycleTemplateID,
		"phases": []map[string]any{
			{"idx": 0, "name": "Idea"},
			{"idx": 1, "name": "Lit review"},
			{"idx": 2, "name": "Method"},
			{"idx": 3, "name": "Experiment"},
			{"idx": 4, "name": "Paper"},
		},
	})
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO plans
			(id, project_id, template_id, version, spec_json, status,
			 created_at, started_at)
		VALUES (?, ?, ?, 1, ?, 'running', ?, ?)`,
		planID, res.projectID, lifecycleTemplateID,
		string(planSpecJSON), now, now); err != nil {
		return res, fmt.Errorf("insert plan: %w", err)
	}
	// Insert real plan_steps: each carries a schema-valid `kind`
	// (agent_spawn | llm_call | shell | human_decision) and a spec
	// describing the actual work. step_idx is assigned per-phase by
	// insertion order so the (phase_idx, step_idx) composite stays
	// unique within each phase.
	stepIdxByPhase := make(map[int]int)
	for i, st := range spec.planSteps {
		phaseIdx := lifecyclePhaseIdx(st.phase)
		if phaseIdx < 0 {
			return res, fmt.Errorf("plan step %d: unknown phase %q", i, st.phase)
		}
		if !planStepKinds[st.kind] {
			return res, fmt.Errorf("plan step %d: invalid kind %q (must be in %v)",
				i, st.kind, planStepKinds)
		}
		stepIdx := stepIdxByPhase[phaseIdx]
		stepIdxByPhase[phaseIdx] = stepIdx + 1
		stepSpec, _ := json.Marshal(st.spec)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO plan_steps
				(id, plan_id, phase_idx, step_idx, kind, spec_json, status)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			NewID(), planID, phaseIdx, stepIdx, st.kind,
			string(stepSpec), st.status); err != nil {
			return res, fmt.Errorf("insert plan_step %d: %w", i, err)
		}
	}

	// 3. Steward + worker agents. Domain steward drives the project; one
	// worker per declared kind so the Children Status hero shows real fan-
	// out for projects past the idea phase.
	res.stewardID = NewID()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agents
			(id, team_id, handle, kind, capabilities_json,
			 status, pause_state, created_at)
		VALUES (?, ?, ?, ?, '[]', 'running', 'running', ?)`,
		res.stewardID, defaultTeamID,
		"@lifecycle-"+spec.phase+"-steward", spec.stewardKind, now); err != nil {
		return res, fmt.Errorf("insert steward: %w", err)
	}
	for i, wk := range spec.workerKinds {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO agents
				(id, team_id, handle, kind, capabilities_json,
				 parent_agent_id, status, pause_state, created_at)
			VALUES (?, ?, ?, ?, '[]', ?, 'running', 'running', ?)`,
			NewID(), defaultTeamID,
			fmt.Sprintf("@lifecycle-%s-worker-%d", spec.phase, i),
			wk, res.stewardID, now); err != nil {
			return res, fmt.Errorf("insert worker %s: %w", wk, err)
		}
	}

	// 4. Phase content (deliverables → components → criteria).
	c := &seedProjectCtx{
		tx: tx, ctx: ctx, projectID: res.projectID,
		stewardID: res.stewardID, now: now, phase: spec.phase,
		dataRoot: dataRoot,
	}

	dls, err := spec.deliverables(c)
	if err != nil {
		return res, err
	}
	res.deliverables = len(dls)
	c.deliverableByLogID = map[string]string{}
	for _, d := range dls {
		c.deliverableByLogID[d.logID] = d.id
	}

	crits, err := spec.criteria(c, dls)
	if err != nil {
		return res, err
	}
	res.criteria = len(crits)
	for _, cr := range crits {
		res.byState[cr.state]++
	}

	// 5. Attention item + audit rows.
	if spec.attention != nil {
		att := spec.attention(c)
		assignees, _ := json.Marshal([]string{"@principal"})
		attID := NewID()
		var payloadArg any
		if att.payload != nil {
			b, _ := json.Marshal(att.payload)
			payloadArg = string(b)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO attention_items
				(id, project_id, scope_kind, scope_id, kind,
				 summary, severity, current_assignees_json,
				 status, created_at,
				 actor_kind, actor_handle, pending_payload_json)
			VALUES (?, ?, 'project', ?, ?,
			        ?, 'major', ?,
			        'open', ?,
			        'agent', 'steward.lifecycle', ?)`,
			attID, res.projectID, res.projectID, att.kind,
			att.summary, string(assignees), now, payloadArg); err != nil {
			return res, fmt.Errorf("insert attention: %w", err)
		}
		res.attentionItems = 1
	}

	res.audits += emitAudit(ctx, tx, "project.create", "project",
		res.projectID, "Created research-lifecycle demo: "+spec.name,
		map[string]any{"project_id": res.projectID, "phase": spec.phase,
			"template_id": lifecycleTemplateID}, now)
	res.audits += emitAudit(ctx, tx, "project.phase_set", "project",
		res.projectID, "Set initial phase "+spec.phase,
		map[string]any{"project_id": res.projectID, "phase": spec.phase,
			"by_template": lifecycleTemplateID}, now)
	for _, d := range dls {
		res.audits += emitAudit(ctx, tx, "deliverable.created", "deliverable",
			d.id, fmt.Sprintf("Created %s deliverable in phase %s", d.kind, d.phase),
			map[string]any{"project_id": res.projectID, "phase": d.phase,
				"kind": d.kind, "state": d.state}, now)
		if d.state == "ratified" {
			res.audits += emitAudit(ctx, tx, "deliverable.ratified",
				"deliverable", d.id, "Director ratified "+d.kind,
				map[string]any{"project_id": res.projectID, "phase": d.phase,
					"kind": d.kind}, now)
		}
	}
	for _, cr := range crits {
		res.audits += emitAudit(ctx, tx, "criterion.created", "criterion",
			cr.id, fmt.Sprintf("Hydrated %s criterion", cr.kind),
			map[string]any{"project_id": res.projectID,
				"kind": cr.kind, "hydrated_from": lifecycleTemplateID}, now)
		switch cr.state {
		case "met":
			res.audits += emitAudit(ctx, tx, "criterion.met", "criterion",
				cr.id, "Marked criterion met",
				map[string]any{"project_id": res.projectID, "kind": cr.kind}, now)
		case "failed":
			res.audits += emitAudit(ctx, tx, "criterion.failed", "criterion",
				cr.id, "Marked criterion failed",
				map[string]any{"project_id": res.projectID, "kind": cr.kind}, now)
		case "waived":
			res.audits += emitAudit(ctx, tx, "criterion.waived", "criterion",
				cr.id, "Waived criterion",
				map[string]any{"project_id": res.projectID, "kind": cr.kind}, now)
		}
	}

	// 6. Tasks — project-scoped kanban entities, independent of plans
	// and phases. Some tasks reference earlier ones as subtasks via
	// parent_task_id so the demo exercises the subtask hierarchy.
	taskIDs := make([]string, len(spec.tasks))
	for i := range spec.tasks {
		taskIDs[i] = NewID()
	}
	for i, t := range spec.tasks {
		var parentID any
		if t.parentIdx >= 0 && t.parentIdx < i {
			parentID = taskIDs[t.parentIdx]
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tasks
				(id, project_id, parent_task_id, title, body_md,
				 status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			taskIDs[i], res.projectID, parentID,
			t.title, t.body, t.status, now, now); err != nil {
			return res, fmt.Errorf("insert task %d: %w", i, err)
		}
	}
	res.tasks = len(spec.tasks)

	// Report side-effect doc/artifact/run counts. The builder closures
	// recorded these on the seedProjectCtx via the helper functions.
	res.documents = c.documentsSeeded
	res.artifacts = c.artifactsSeeded
	res.runs = c.runsSeeded
	res.annotations = c.annotationsSeeded

	return res, nil
}

// ---------------------------------------------------------------------
// Builders for typed documents, deliverables, criteria, artifacts, runs.
// ---------------------------------------------------------------------

type sectionSeed struct {
	slug, title, body, status string
}

type seededDocument struct {
	id    string
	title string
}

type deliverableSpec struct {
	logID      string // matches the YAML logical id
	phase      string
	kind       string
	state      string
	components []componentSpec
}

type componentSpec struct {
	kind  string // document | artifact | run | commit
	refID string
	ord   int
}

type criterionSpec struct {
	logID         string
	phase         string
	kind          string // text | metric | gate
	deliverableID string
	body          map[string]any
	state         string
	evidenceRef   string
	required      bool
}

// annotationSeed describes one director annotation on a section of a
// typed document. ADR-020 W1: kind ∈ {comment, redline, suggestion,
// question}; status ∈ {open, resolved}. char_start/end are optional —
// when both zero the annotation lands as a section-level note.
type annotationSeed struct {
	docID     string
	section   string
	kind      string
	body      string
	status    string // open | resolved
	charStart int
	charEnd   int
}

// seedAnnotation inserts a single director-authored annotation on a
// typed-document section. Author is stamped as the director (the
// principal who would have left the note in the demo) so the
// "edit-by-author" gate can be exercised in the dress-rehearsal.
func seedAnnotation(c *seedProjectCtx, a annotationSeed) error {
	if a.kind == "" {
		a.kind = "comment"
	}
	if a.status == "" {
		a.status = "open"
	}
	var charStartArg, charEndArg any
	if a.charStart != 0 || a.charEnd != 0 {
		charStartArg = a.charStart
		charEndArg = a.charEnd
	}
	var resolvedAt, resolvedBy any
	if a.status == "resolved" {
		resolvedAt = c.now
		resolvedBy = "user:director"
	}
	annID := NewID()
	if _, err := c.tx.ExecContext(c.ctx, `
		INSERT INTO document_annotations (
			id, document_id, section_slug, char_start, char_end,
			kind, body, status, author_kind, author_handle,
			created_at, resolved_at, resolved_by_actor
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'principal', 'director',
		          ?, ?, ?)`,
		annID, a.docID, a.section, charStartArg, charEndArg,
		a.kind, a.body, a.status, c.now, resolvedAt, resolvedBy,
	); err != nil {
		return fmt.Errorf("insert annotation on %s/%s: %w",
			a.docID, a.section, err)
	}
	c.annotationsSeeded++
	c.annotationIDs = append(c.annotationIDs, annID)
	return nil
}

func seedTypedDocument(
	c *seedProjectCtx, title, schemaID string, sections []sectionSeed,
) (seededDocument, error) {
	docID := NewID()
	contentInline := buildStructuredBody(schemaID, sections, c.now, "agent:steward.lifecycle")
	if _, err := c.tx.ExecContext(c.ctx, `
		INSERT INTO documents
			(id, project_id, kind, title, version, content_inline,
			 schema_id, author_agent_id, created_at)
		VALUES (?, ?, 'typed', ?, 1, ?, ?, ?, ?)`,
		docID, c.projectID, title, contentInline, schemaID,
		c.stewardID, c.now); err != nil {
		return seededDocument{}, fmt.Errorf("insert typed document %s: %w", schemaID, err)
	}
	c.documentsSeeded++
	return seededDocument{id: docID, title: title}, nil
}

// seedDeliverables inserts the declared deliverables + their components
// into the transaction and returns the resulting seededDeliverable list.
// Ratified deliverables get their ratified_at + ratified_by_actor stamped
// at seed-time so the W5b viewer renders the closed-pip state.
func seedDeliverables(
	c *seedProjectCtx, specs []deliverableSpec,
) ([]seededDeliverable, error) {
	out := make([]seededDeliverable, 0, len(specs))
	for ord, d := range specs {
		id := NewID()
		var ratifiedAt sql.NullString
		var ratifiedByActor sql.NullString
		if d.state == "ratified" {
			ratifiedAt = sql.NullString{String: c.now, Valid: true}
			ratifiedByActor = sql.NullString{
				String: "user:director", Valid: true,
			}
		}
		if _, err := c.tx.ExecContext(c.ctx, `
			INSERT INTO deliverables
				(id, project_id, phase, kind, ratification_state,
				 ratified_at, ratified_by_actor,
				 required, ord, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?)`,
			id, c.projectID, d.phase, d.kind, d.state,
			ratifiedAt, ratifiedByActor, ord, c.now, c.now); err != nil {
			return nil, fmt.Errorf("insert deliverable %s: %w", d.kind, err)
		}
		for ci, comp := range d.components {
			cord := comp.ord
			if cord == 0 {
				cord = ci
			}
			if _, err := c.tx.ExecContext(c.ctx, `
				INSERT INTO deliverable_components
					(id, deliverable_id, kind, ref_id, required, ord, created_at)
				VALUES (?, ?, ?, ?, 1, ?, ?)`,
				NewID(), id, comp.kind, comp.refID, cord, c.now); err != nil {
				return nil, fmt.Errorf("insert component %s: %w", comp.kind, err)
			}
		}
		out = append(out, seededDeliverable{
			id: id, logID: d.logID, phase: d.phase, kind: d.kind, state: d.state,
		})
	}
	return out, nil
}

func seedCriteria(
	c *seedProjectCtx, specs []criterionSpec,
) ([]seededCriterion, error) {
	out := make([]seededCriterion, 0, len(specs))
	for ord, cr := range specs {
		id := NewID()
		bodyJSON, _ := json.Marshal(cr.body)
		req := 0
		if cr.required {
			req = 1
		}
		var deliv sql.NullString
		if cr.deliverableID != "" {
			deliv = sql.NullString{String: cr.deliverableID, Valid: true}
		}
		var metAt, metBy sql.NullString
		if cr.state == "met" {
			metAt = sql.NullString{String: c.now, Valid: true}
			metBy = sql.NullString{String: "user:director", Valid: true}
		}
		var evid sql.NullString
		if cr.evidenceRef != "" {
			evid = sql.NullString{String: cr.evidenceRef, Valid: true}
		}
		if _, err := c.tx.ExecContext(c.ctx, `
			INSERT INTO acceptance_criteria
				(id, project_id, phase, deliverable_id, kind, body,
				 state, met_at, met_by_actor, evidence_ref,
				 required, ord, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, c.projectID, cr.phase, deliv, cr.kind, string(bodyJSON),
			cr.state, metAt, metBy, evid, req, ord, c.now, c.now); err != nil {
			return nil, fmt.Errorf("insert criterion %s: %w", cr.logID, err)
		}
		out = append(out, seededCriterion{id: id, state: cr.state, kind: cr.kind})
	}
	return out, nil
}

type seededArtifact struct{ id, name string }

func seedArtifact(
	c *seedProjectCtx, kind, name, mime string, size int64,
) (seededArtifact, error) {
	id := NewID()
	if _, err := c.tx.ExecContext(c.ctx, `
		INSERT INTO artifacts
			(id, project_id, kind, name, uri, size, mime,
			 producer_agent_id, lineage_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), '{}', ?)`,
		id, c.projectID, kind, name,
		fmt.Sprintf("blob:mock/lifecycle/%s", id),
		size, mime, c.stewardID, c.now); err != nil {
		return seededArtifact{}, fmt.Errorf("insert artifact: %w", err)
	}
	c.artifactsSeeded++
	return seededArtifact{id: id, name: name}, nil
}

// demoCitations is the fixed citation set surfaced by every ratified
// lit-review deliverable. Eight rows is enough to exercise the
// TabularViewer's row count, schema-known column ordering, and a
// `notes`-style free-text column. Keep the slice deterministic so
// blob dedup keeps the seed idempotent.
func demoCitations() []map[string]any {
	return []map[string]any{
		{"author": "Lin et al.", "year": 2025, "title": "Routing in sparse MoE under bandwidth caps",
			"venue": "ICML", "doi": "10.1234/icml.2025.routing", "notes": "Canonical routing axis"},
		{"author": "Hu et al.", "year": 2024, "title": "Token-level expert balancing",
			"venue": "NeurIPS", "doi": "10.1234/neurips.2024.balance", "notes": "Adjacent — flagged in review"},
		{"author": "Park & Singh", "year": 2024, "title": "Lion vs AdamW at small batch",
			"venue": "TMLR", "doi": "10.1234/tmlr.2024.lion", "notes": "Optimizer prior"},
		{"author": "Ramesh et al.", "year": 2023, "title": "Curriculum order for nanoGPT",
			"venue": "Workshop", "doi": "10.1234/ws.2023.curriculum", "notes": ""},
		{"author": "Zhao & Lee", "year": 2023, "title": "Evaluation harness drift",
			"venue": "EMNLP", "doi": "10.1234/emnlp.2023.harness", "notes": "Cite if we change eval cadence"},
		{"author": "Goldberg", "year": 2017, "title": "Neural network methods for NLP",
			"venue": "Book", "doi": "", "notes": "Textbook reference"},
		{"author": "Touvron et al.", "year": 2024, "title": "Open-weight scaling limits",
			"venue": "arXiv", "doi": "10.1234/arxiv.2024.scaling", "notes": ""},
		{"author": "Vaswani et al.", "year": 2017, "title": "Attention is all you need",
			"venue": "NeurIPS", "doi": "10.1234/neurips.2017.attention", "notes": "Foundational"},
	}
}

// seedCitationArtifact materialises a `tabular`-kind citation artifact
// for the W3 References tile. When `c.dataRoot` is set, the citation
// rows are serialised as JSON and written to the content-addressed
// blob store so the mobile TabularViewer resolves real bytes;
// otherwise the artifact gets a mock URI (visible-failure path).
// MIME carries the `schema=citation` discriminator (Q6 option (a)).
func seedCitationArtifact(
	c *seedProjectCtx, rows []map[string]any,
) (seededArtifact, error) {
	data, err := json.Marshal(rows)
	if err != nil {
		return seededArtifact{}, fmt.Errorf("marshal citations: %w", err)
	}
	mime := "application/json; schema=citation"
	uri := fmt.Sprintf("blob:mock/lifecycle/citations-%s", c.projectID)
	if c.dataRoot != "" {
		sha, berr := insertDemoBlob(c.ctx, c.tx, c.dataRoot, data, mime, c.now)
		if berr != nil {
			return seededArtifact{}, fmt.Errorf("write citations blob: %w", berr)
		}
		uri = "blob:sha256/" + sha
	}
	id := NewID()
	name := "references.json"
	if _, err := c.tx.ExecContext(c.ctx, `
		INSERT INTO artifacts
			(id, project_id, kind, name, uri, size, mime,
			 producer_agent_id, lineage_json, created_at)
		VALUES (?, ?, 'tabular', ?, ?, ?, ?, NULLIF(?, ''), '{}', ?)`,
		id, c.projectID, name, uri, int64(len(data)), mime,
		c.stewardID, c.now); err != nil {
		return seededArtifact{}, fmt.Errorf("insert citation artifact: %w", err)
	}
	c.artifactsSeeded++
	return seededArtifact{id: id, name: name}, nil
}

// demoRunBundle returns the three-file training scaffold attached to
// the ratified experiment-results deliverable as a `code-bundle`
// artifact. Kept short + deterministic so blob dedup keeps the seed
// idempotent; mirrors the shape Cursor and Claude Artifacts emit for
// multi-file scaffolds. Emits AFM-V1 (`version: 1`) per
// `docs/plans/canvas-viewer.md`; the legacy unversioned form still
// parses, but new seeds adopt the explicit schema.
func demoRunBundle() map[string]any {
	return map[string]any{
		"version": 1,
		"files": []map[string]any{
			{
				"path":    "train.py",
				"content": "" + `"""Single-file training loop used by the ablation sweep."""
import torch
from torch.optim import AdamW
from config import RunConfig
from data import build_loader


def train(cfg: RunConfig) -> dict:
    model = cfg.build_model()
    opt = AdamW(model.parameters(), lr=cfg.lr)
    loader = build_loader(cfg.batch_size)
    for step, batch in enumerate(loader):
        loss = model(batch).loss
        loss.backward()
        opt.step()
        opt.zero_grad()
        if step >= cfg.iters:
            break
    return {"final_loss": float(loss)}


if __name__ == "__main__":
    train(RunConfig.from_cli())
`,
			},
			{
				"path":    "config.py",
				"content": "" + `from dataclasses import dataclass


@dataclass
class RunConfig:
    n_embd: int = 384
    optimizer: str = "lion"
    iters: int = 1000
    lr: float = 3e-4
    batch_size: int = 32

    def build_model(self):
        from model import build
        return build(n_embd=self.n_embd)

    @classmethod
    def from_cli(cls) -> "RunConfig":
        import argparse
        p = argparse.ArgumentParser()
        p.add_argument("--n_embd", type=int, default=384)
        p.add_argument("--optimizer", default="lion")
        p.add_argument("--iters", type=int, default=1000)
        args = p.parse_args()
        return cls(**vars(args))
`,
			},
			{
				"path":    "README.md",
				"content": "" + `# Optimizer ablation — run bundle

Reference implementation that produced the eval curves on the
ratified experiment-results deliverable.

## Files

- ` + "`train.py`" + ` — single-file training loop
- ` + "`config.py`" + ` — RunConfig + CLI parser

## Reproducibility

Reproduce the headline run with:

` + "```bash\npython train.py --n_embd 384 --optimizer lion --iters 1000\n```" + `

Eval is split out to keep the bundle inspectable on mobile.
`,
			},
		},
	}
}

// demoCanvasBundle returns the 3-file AFM-V1 manifest the canvas
// viewer (canvas-viewer plan W2) renders on the ratified
// experiment-results deliverable. Three files
// (`index.html` + `chart.js` + `style.css`) draw an interactive SVG
// dual-line chart (train + val loss) with gridlines, axis labels,
// a legend, a series-toggle button, hover crosshair, and click-to-
// pin readouts — enough surface to verify JS execution + DOM events
// + dynamic style updates inside the sandboxed WebView (canvas-viewer
// plan W4 allowlist applies). Bumped v1.0.511 — the prior single-
// curve fixed-320x200 SVG rendered as a tiny chip in the top-left
// on wider/folded screens.
func demoCanvasBundle() map[string]any {
	const indexHTML = `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Eval curves</title>
<link rel="stylesheet" href="style.css">
</head>
<body>
<header>
  <h1>Training run · WT2 micro-LM · 12.4M params</h1>
  <p class="sub">Train / val cross-entropy loss vs steps (1M tokens).
     Lower is better. Tap a point to pin a readout.</p>
</header>
<div class="legend">
  <button class="series train active" data-key="train">
    <span class="dot"></span>train
  </button>
  <button class="series val active" data-key="val">
    <span class="dot"></span>val
  </button>
  <div class="grow"></div>
  <span id="readout">hover or tap a point</span>
</div>
<svg id="chart" viewBox="0 0 800 420"
     preserveAspectRatio="xMidYMid meet"></svg>
<table class="summary">
  <thead><tr><th>series</th><th>start</th><th>final</th>
             <th>Δ</th><th>min</th></tr></thead>
  <tbody id="sumbody"></tbody>
</table>
<script src="chart.js"></script>
</body>
</html>`
	const chartJS = `// Synthetic but realistic-looking train/val curves: train drops
// smoothly, val tracks then plateaus higher (the gap is the
// generalization signal the seed wants to make visible).
const SERIES = {
  train: { color: '#e94e8a', label: 'train', points: [
    [0, 4.42],[40, 3.95],[80, 3.55],[120, 3.22],[160, 2.96],
    [200, 2.74],[240, 2.57],[280, 2.43],[320, 2.31],[360, 2.21],
    [400, 2.13],[440, 2.06],[480, 2.00],[520, 1.95],[560, 1.90],
    [600, 1.86],[640, 1.83],[680, 1.80],[720, 1.78],[760, 1.76],
    [800, 1.74],[840, 1.73],[880, 1.72],[920, 1.71],[960, 1.71],
    [1000, 1.70]
  ]},
  val: { color: '#5fc6e0', label: 'val', points: [
    [0, 4.40],[40, 3.99],[80, 3.62],[120, 3.32],[160, 3.10],
    [200, 2.94],[240, 2.81],[280, 2.71],[320, 2.63],[360, 2.57],
    [400, 2.52],[440, 2.48],[480, 2.45],[520, 2.43],[560, 2.41],
    [600, 2.39],[640, 2.38],[680, 2.37],[720, 2.36],[760, 2.36],
    [800, 2.35],[840, 2.35],[880, 2.34],[920, 2.34],[960, 2.34],
    [1000, 2.34]
  ]}
};
const NS = 'http://www.w3.org/2000/svg';
const svg = document.getElementById('chart');
const W = 800, H = 420, PAD_L = 56, PAD_R = 20, PAD_T = 24, PAD_B = 40;
const all = Object.values(SERIES).flatMap(s => s.points);
const xMax = Math.max(...all.map(p => p[0]));
const yMin = Math.min(...all.map(p => p[1]));
const yMax = Math.max(...all.map(p => p[1]));
const xPad = 0;
const yPad = (yMax - yMin) * 0.08;
const x = v => PAD_L + (W - PAD_L - PAD_R) *
  (v - xPad) / (xMax - xPad);
const y = v => H - PAD_B - (H - PAD_T - PAD_B) *
  (v - (yMin - yPad)) / ((yMax + yPad) - (yMin - yPad));
function add(tag, attrs, parent) {
  const e = document.createElementNS(NS, tag);
  for (const k in attrs) e.setAttribute(k, attrs[k]);
  (parent || svg).appendChild(e);
  return e;
}
// Gridlines + Y axis labels (5 horizontal)
for (let i = 0; i <= 5; i++) {
  const yv = yMin - yPad + ((yMax + yPad) - (yMin - yPad)) * (i / 5);
  add('line', {
    x1: PAD_L, x2: W - PAD_R, y1: y(yv), y2: y(yv),
    stroke: '#2a2a2a', 'stroke-width': 1
  });
  add('text', {
    x: PAD_L - 8, y: y(yv) + 4,
    'text-anchor': 'end', fill: '#888',
    'font-size': 11, 'font-family': 'ui-monospace'
  }, svg).textContent = yv.toFixed(2);
}
// X axis ticks every 200 steps
for (let xv = 0; xv <= xMax; xv += 200) {
  add('line', {
    x1: x(xv), x2: x(xv), y1: H - PAD_B, y2: H - PAD_B + 4,
    stroke: '#555', 'stroke-width': 1
  });
  add('text', {
    x: x(xv), y: H - PAD_B + 18,
    'text-anchor': 'middle', fill: '#888',
    'font-size': 11, 'font-family': 'ui-monospace'
  }, svg).textContent = xv;
}
// Axis labels
add('text', {
  x: (PAD_L + W - PAD_R) / 2, y: H - 6,
  'text-anchor': 'middle', fill: '#aaa', 'font-size': 12
}, svg).textContent = 'steps';
add('text', {
  x: 14, y: (PAD_T + H - PAD_B) / 2,
  transform: 'rotate(-90 14 ' + ((PAD_T + H - PAD_B) / 2) + ')',
  'text-anchor': 'middle', fill: '#aaa', 'font-size': 12
}, svg).textContent = 'loss';
// Series groups
const groups = {};
for (const key in SERIES) {
  const s = SERIES[key];
  const g = add('g', { 'data-key': key }, svg);
  groups[key] = g;
  const d = s.points.map((p, i) =>
    (i === 0 ? 'M' : 'L') + x(p[0]) + ',' + y(p[1])).join(' ');
  add('path', {
    d: d, fill: 'none', stroke: s.color, 'stroke-width': 2.2,
    'stroke-linecap': 'round', 'stroke-linejoin': 'round'
  }, g);
  for (const p of s.points) {
    const c = add('circle', {
      cx: x(p[0]), cy: y(p[1]), r: 4, fill: s.color
    }, g);
    c.style.cursor = 'pointer';
    c.addEventListener('click', () => {
      pin(key, p[0], p[1]);
    });
    c.addEventListener('mouseenter', () => {
      hover(key, p[0], p[1]);
    });
  }
}
// Crosshair (shared)
const cross = add('line', {
  x1: 0, x2: 0, y1: PAD_T, y2: H - PAD_B,
  stroke: '#fff', 'stroke-width': 1, 'stroke-dasharray': '3 3',
  opacity: 0
}, svg);
const readout = document.getElementById('readout');
function hover(key, step, loss) {
  cross.setAttribute('x1', x(step));
  cross.setAttribute('x2', x(step));
  cross.setAttribute('opacity', 0.4);
  readout.textContent = key + ' · step ' + step + ' · loss ' +
    loss.toFixed(3);
  readout.style.color = SERIES[key].color;
}
function pin(key, step, loss) {
  hover(key, step, loss);
  readout.textContent += ' (pinned)';
}
svg.addEventListener('mouseleave', () => {
  cross.setAttribute('opacity', 0);
});
// Series toggles
for (const btn of document.querySelectorAll('.series')) {
  btn.addEventListener('click', () => {
    btn.classList.toggle('active');
    const k = btn.dataset.key;
    groups[k].style.opacity = btn.classList.contains('active') ? 1 : 0.15;
  });
}
// Summary table
const body = document.getElementById('sumbody');
for (const key in SERIES) {
  const s = SERIES[key];
  const start = s.points[0][1];
  const final = s.points[s.points.length - 1][1];
  const min = Math.min(...s.points.map(p => p[1]));
  const row = document.createElement('tr');
  row.innerHTML =
    '<td><span class="dot" style="background:' + s.color + '"></span>'
      + s.label + '</td>' +
    '<td>' + start.toFixed(2) + '</td>' +
    '<td>' + final.toFixed(2) + '</td>' +
    '<td>' + (final - start).toFixed(2) + '</td>' +
    '<td>' + min.toFixed(2) + '</td>';
  body.appendChild(row);
}`
	// Single flat background — testers on folded phones read the
	// previous "body / card / chartwrap" three-color layering as
	// "the curve is trapped in a fixed-width region" (same vibe as
	// the B4 code-block report). Everything sits directly on #0d0d0d
	// now; the chart is edge-to-edge, the chrome lives in dividers
	// instead of backgrounds.
	const styleCSS = `* { box-sizing: border-box; }
html, body { margin: 0; padding: 0; width: 100%; height: 100%; }
body {
  font-family: -apple-system, BlinkMacSystemFont, sans-serif;
  background: #0d0d0d;
  color: #eee;
  padding: 12px;
  min-height: 100vh;
}
header { margin-bottom: 10px; }
header h1 {
  margin: 0 0 4px;
  font-size: 15px;
  font-weight: 600;
  color: #f4f4f4;
}
header .sub {
  margin: 0;
  font-size: 12px;
  color: #888;
}
.legend {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 8px;
  flex-wrap: wrap;
}
.legend .grow { flex: 1; }
.series {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  background: transparent;
  border: 1px solid #333;
  border-radius: 999px;
  color: #ccc;
  cursor: pointer;
  padding: 4px 10px;
  font-size: 12px;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
.series:not(.active) { opacity: 0.45; }
.series.train .dot { background: #e94e8a; }
.series.val .dot { background: #5fc6e0; }
.dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 50%;
}
#readout {
  font-size: 12px;
  color: #aaa;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
#chart {
  width: 100%;
  height: auto;
  display: block;
  margin-bottom: 10px;
}
.summary {
  width: 100%;
  border-collapse: collapse;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
}
.summary th, .summary td {
  text-align: left;
  padding: 6px 8px;
  border-bottom: 1px solid #1f1f1f;
  color: #ccc;
}
.summary th { color: #777; font-weight: 600; }
.summary td:first-child {
  display: flex;
  align-items: center;
  gap: 6px;
}
@media (max-width: 480px) {
  header h1 { font-size: 13px; }
  .summary { font-size: 11px; }
}`
	return map[string]any{
		"version": 1,
		"entry":   "index.html",
		"files": []map[string]any{
			{"path": "index.html", "content": indexHTML},
			{"path": "chart.js", "content": chartJS},
			{"path": "style.css", "content": styleCSS},
		},
	}
}

// seedCanvasArtifact materialises a `canvas-app`-kind artifact for the
// demo canvas bundle. Mirrors seedCodeBundleArtifact's
// blob-or-mock-uri pattern so the mobile CanvasViewer round-trips
// through the same HubClient.downloadBlobCached path as the rest of
// the wave-2 viewers.
func seedCanvasArtifact(
	c *seedProjectCtx, bundle map[string]any, name string,
) (seededArtifact, error) {
	data, err := json.Marshal(bundle)
	if err != nil {
		return seededArtifact{}, fmt.Errorf("marshal canvas bundle: %w", err)
	}
	mime := "application/vnd.termipod.canvas+json"
	uri := fmt.Sprintf("blob:mock/lifecycle/canvas-%s-%s", c.projectID, name)
	if c.dataRoot != "" {
		sha, berr := insertDemoBlob(c.ctx, c.tx, c.dataRoot, data, mime, c.now)
		if berr != nil {
			return seededArtifact{}, fmt.Errorf("write canvas blob: %w", berr)
		}
		uri = "blob:sha256/" + sha
	}
	id := NewID()
	if _, err := c.tx.ExecContext(c.ctx, `
		INSERT INTO artifacts
			(id, project_id, kind, name, uri, size, mime,
			 producer_agent_id, lineage_json, created_at)
		VALUES (?, ?, 'canvas-app', ?, ?, ?, ?, NULLIF(?, ''), '{}', ?)`,
		id, c.projectID, name, uri, int64(len(data)), mime,
		c.stewardID, c.now); err != nil {
		return seededArtifact{}, fmt.Errorf("insert canvas artifact: %w", err)
	}
	c.artifactsSeeded++
	return seededArtifact{id: id, name: name}, nil
}

// seedCodeBundleArtifact materialises a `code-bundle`-kind artifact
// (wave 2 W5) for the demo run-bundle. Mirrors seedCitationArtifact's
// "real bytes when dataRoot present, mock URI otherwise" pattern so the
// mobile CodeBundleViewer round-trips through the blob endpoint.
func seedCodeBundleArtifact(
	c *seedProjectCtx, bundle map[string]any, name string,
) (seededArtifact, error) {
	data, err := json.Marshal(bundle)
	if err != nil {
		return seededArtifact{}, fmt.Errorf("marshal bundle: %w", err)
	}
	mime := "application/vnd.termipod.code+json"
	uri := fmt.Sprintf("blob:mock/lifecycle/bundle-%s-%s", c.projectID, name)
	if c.dataRoot != "" {
		sha, berr := insertDemoBlob(c.ctx, c.tx, c.dataRoot, data, mime, c.now)
		if berr != nil {
			return seededArtifact{}, fmt.Errorf("write bundle blob: %w", berr)
		}
		uri = "blob:sha256/" + sha
	}
	id := NewID()
	if _, err := c.tx.ExecContext(c.ctx, `
		INSERT INTO artifacts
			(id, project_id, kind, name, uri, size, mime,
			 producer_agent_id, lineage_json, created_at)
		VALUES (?, ?, 'code-bundle', ?, ?, ?, ?, NULLIF(?, ''), '{}', ?)`,
		id, c.projectID, name, uri, int64(len(data)), mime,
		c.stewardID, c.now); err != nil {
		return seededArtifact{}, fmt.Errorf("insert code bundle artifact: %w", err)
	}
	c.artifactsSeeded++
	return seededArtifact{id: id, name: name}, nil
}

// sweepRunConfig captures one run inside the demo's multi-run
// experiment phase. The plateau field controls where the synthesised
// accuracy curve flattens out — smaller models plateau lower so the
// aggregate metric-chart shows a clean ranking across configs.
type sweepRunConfig struct {
	nEmbd     int
	optimizer string
	plateau   float64
}

// defaultSweepConfigs is the 3-run sweep used by the lifecycle demo.
// Three model sizes × the same optimizer; plateau levels stagger so
// the aggregate chart's three series visibly separate. Kept
// deterministic for blob dedup + reproducible test output.
var defaultSweepConfigs = []sweepRunConfig{
	{nEmbd: 128, optimizer: "lion", plateau: 0.78},
	{nEmbd: 256, optimizer: "lion", plateau: 0.84},
	{nEmbd: 384, optimizer: "lion", plateau: 0.88},
}

func sweepRunLabel(cfg sweepRunConfig) string {
	return fmt.Sprintf("n%d-%s", cfg.nEmbd, cfg.optimizer)
}

// generateSweepPoints synthesises a 0→1000 step eval-accuracy curve
// that ramps smoothly from 0.50 toward the supplied plateau via a
// 1-exp ramp. Eleven points so the rendered line is recognisably
// curved without overrunning the artifact body cap.
func generateSweepPoints(plateau float64) [][]float64 {
	points := make([][]float64, 0, 11)
	for i := 0; i <= 10; i++ {
		x := float64(i * 100)
		t := float64(i) / 10.0
		y := 0.50 + (plateau-0.50)*(1-math.Exp(-3*t))
		y = math.Round(y*100) / 100
		points = append(points, []float64{x, y})
	}
	return points
}

// demoPerRunMetricChartBody returns a single-series `metric-chart`
// body for one sweep run. Used by the seed to produce per-run eval
// curves attached to the experiment-results deliverable.
func demoPerRunMetricChartBody(cfg sweepRunConfig) map[string]any {
	return map[string]any{
		"version": 1,
		"title":   fmt.Sprintf("Eval accuracy · %s", sweepRunLabel(cfg)),
		"x_label": "Step",
		"y_label": "Accuracy",
		"series": []map[string]any{
			{
				"name":   "eval_accuracy",
				"points": generateSweepPoints(cfg.plateau),
			},
		},
	}
}

// demoAggregateMetricChartBody returns the multi-series body that
// overlays one curve per sweep run. The mobile `experiment_dash`
// embed picks this artifact (newest by created_at, ensured via the
// timeAfter() bump in the seed) and renders all series inline.
func demoAggregateMetricChartBody(cfgs []sweepRunConfig) map[string]any {
	series := make([]map[string]any, 0, len(cfgs))
	for _, cfg := range cfgs {
		series = append(series, map[string]any{
			"name":   sweepRunLabel(cfg),
			"points": generateSweepPoints(cfg.plateau),
		})
	}
	return map[string]any{
		"version": 1,
		"title":   "Eval accuracy — sweep",
		"x_label": "Step",
		"y_label": "Accuracy",
		"series":  series,
	}
}

// timeAfter returns `now` (RFC3339Nano) offset by n seconds, used to
// give the aggregate metric-chart a strictly-later created_at than
// its per-run siblings. On parse failure returns `now` unchanged —
// the embed's newest picker still works against per-row insertion
// order in that case.
func timeAfter(now string, secs int) string {
	t, err := time.Parse(time.RFC3339Nano, now)
	if err != nil {
		return now
	}
	return t.Add(time.Duration(secs) * time.Second).Format(time.RFC3339Nano)
}

// seedMetricChartArtifact materialises a `metric-chart` artifact with
// real JSON bytes so the mobile MetricChartViewer can render a graph
// (the prior `seedArtifact` shortcut only inserted a mock URI row,
// which left the viewer showing "unsupported uri scheme"). Mirrors
// seedCanvasArtifact's blob-or-mock-uri pattern.
//
// Accepts an explicit body + createdAt so callers can vary per-run
// shapes and assign a strictly-later timestamp to the aggregate
// chart in a multi-run sweep.
func seedMetricChartArtifact(
	c *seedProjectCtx, name string,
	body map[string]any, createdAt string,
) (seededArtifact, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return seededArtifact{}, fmt.Errorf("marshal metric chart: %w", err)
	}
	mime := "application/vnd.termipod.metrics+json"
	uri := fmt.Sprintf("blob:mock/lifecycle/metric-%s-%s", c.projectID, name)
	if c.dataRoot != "" {
		sha, berr := insertDemoBlob(c.ctx, c.tx, c.dataRoot, data, mime, createdAt)
		if berr != nil {
			return seededArtifact{}, fmt.Errorf("write metric blob: %w", berr)
		}
		uri = "blob:sha256/" + sha
	}
	id := NewID()
	if _, err := c.tx.ExecContext(c.ctx, `
		INSERT INTO artifacts
			(id, project_id, kind, name, uri, size, mime,
			 producer_agent_id, lineage_json, created_at)
		VALUES (?, ?, 'metric-chart', ?, ?, ?, ?, NULLIF(?, ''), '{}', ?)`,
		id, c.projectID, name, uri, int64(len(data)), mime,
		c.stewardID, createdAt); err != nil {
		return seededArtifact{}, fmt.Errorf("insert metric chart artifact: %w", err)
	}
	c.artifactsSeeded++
	return seededArtifact{id: id, name: name}, nil
}

// buildDemoPdfBytes returns a small, syntactically-valid PDF (~800
// bytes) sized at US-Letter (612x792 pt) whose single page reads
// "Lifecycle demo PDF" centered with a couple of subtitle lines. Used
// by the lifecycle seed so testers can exercise the wave 2 W2 pdfrx
// viewer without uploading a real document.
//
// Two iterations of "the page is totally empty / gray" from testers:
//   - v1.0.507: 300x80 MediaBox rendered as a tiny scaled strip
//   - v1.0.508: 612x792 page rendered but pdfium produced no glyphs
//     because the Helvetica font had no /Encoding entry (the standard
//     14 fonts are technically implicit-encoding but some pdfium builds
//     drop them on the floor). Fixed v1.0.509 by adding explicit
//     /Encoding/WinAnsiEncoding and a /ProcSet on the page.
//
// Built at runtime via fmt.Sprintf so the xref offsets stay accurate
// without hand-counting.
func buildDemoPdfBytes() []byte {
	var buf bytes.Buffer
	write := func(s string) int {
		off := buf.Len()
		buf.WriteString(s)
		return off
	}
	write("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	o1 := write("1 0 obj <</Type/Catalog/Pages 2 0 R>> endobj\n")
	o2 := write("2 0 obj <</Type/Pages/Kids[3 0 R]/Count 1>> endobj\n")
	o3 := write(
		"3 0 obj <</Type/Page/Parent 2 0 R" +
			"/MediaBox[0 0 612 792]" +
			"/Resources<</Font<</F1 4 0 R>>/ProcSet[/PDF/Text]>>" +
			"/Contents 5 0 R>> endobj\n")
	o4 := write(
		"4 0 obj <</Type/Font/Subtype/Type1/BaseFont/Helvetica" +
			"/Encoding/WinAnsiEncoding>> endobj\n")
	// Content stream: title + subtitle + footer. Coordinates are PDF
	// user-space (origin at bottom-left of MediaBox). Title sits near
	// the top of the visible area when pdfrx fits-to-width on a phone.
	const content = "BT /F1 28 Tf 80 700 Td (Lifecycle demo PDF) Tj ET\n" +
		"BT /F1 14 Tf 80 660 Td (Synthetic seed artifact for the wave 2 viewer) Tj ET\n" +
		"BT /F1 12 Tf 80 620 Td (Tap to verify the PDF renderer works.) Tj ET\n" +
		"BT /F1 11 Tf 80 80 Td (termipod lifecycle demo) Tj ET"
	o5 := write(fmt.Sprintf(
		"5 0 obj <</Length %d>>\nstream\n%s\nendstream\nendobj\n",
		len(content), content))
	xrefStart := buf.Len()
	fmt.Fprintf(&buf,
		"xref\n0 6\n"+
			"0000000000 65535 f \n"+
			"%010d 00000 n \n"+
			"%010d 00000 n \n"+
			"%010d 00000 n \n"+
			"%010d 00000 n \n"+
			"%010d 00000 n \n"+
			"trailer <</Size 6/Root 1 0 R>>\n"+
			"startxref\n%d\n%%%%EOF\n",
		o1, o2, o3, o4, o5, xrefStart)
	return buf.Bytes()
}

// seedPdfArtifact materialises a tiny but valid PDF artifact so
// testers can exercise the wave 2 W2 PDF viewer via the demo path
// without bringing their own file. Mirrors seedCanvasArtifact's
// blob-or-mock-uri pattern.
func seedPdfArtifact(
	c *seedProjectCtx, name string,
) (seededArtifact, error) {
	data := buildDemoPdfBytes()
	mime := "application/pdf"
	uri := fmt.Sprintf("blob:mock/lifecycle/pdf-%s-%s", c.projectID, name)
	if c.dataRoot != "" {
		sha, berr := insertDemoBlob(c.ctx, c.tx, c.dataRoot, data, mime, c.now)
		if berr != nil {
			return seededArtifact{}, fmt.Errorf("write pdf blob: %w", berr)
		}
		uri = "blob:sha256/" + sha
	}
	id := NewID()
	if _, err := c.tx.ExecContext(c.ctx, `
		INSERT INTO artifacts
			(id, project_id, kind, name, uri, size, mime,
			 producer_agent_id, lineage_json, created_at)
		VALUES (?, ?, 'pdf', ?, ?, ?, ?, NULLIF(?, ''), '{}', ?)`,
		id, c.projectID, name, uri, int64(len(data)), mime,
		c.stewardID, c.now); err != nil {
		return seededArtifact{}, fmt.Errorf("insert pdf artifact: %w", err)
	}
	c.artifactsSeeded++
	return seededArtifact{id: id, name: name}, nil
}

// buildDemoPngBytes returns a 128x64 PNG with a magenta-to-cyan
// diagonal gradient. Pure-stdlib (image/png) so the bytes are valid
// and tiny (~250 bytes after PNG's zlib pass). Renders as a visibly
// non-test-pattern band in the wave 2 W4 image viewer.
func buildDemoPngBytes() ([]byte, error) {
	const w, h = 128, 64
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			// Termipod brand magenta → cyan gradient. Deterministic
			// per pixel so blob dedup keeps the seed idempotent.
			t := float64(x+y) / float64(w+h)
			r := uint8(233.0 * (1.0 - t))
			g := uint8(78.0 + 100.0*t)
			b := uint8(138.0 + 90.0*t)
			img.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("png encode: %w", err)
	}
	return buf.Bytes(), nil
}

// seedImageArtifact materialises a tiny PNG so testers can exercise
// the wave 2 W4 image viewer via the demo path. Same blob-or-mock
// pattern as seedPdfArtifact / seedCanvasArtifact.
func seedImageArtifact(
	c *seedProjectCtx, name string,
) (seededArtifact, error) {
	data, err := buildDemoPngBytes()
	if err != nil {
		return seededArtifact{}, err
	}
	mime := "image/png"
	uri := fmt.Sprintf("blob:mock/lifecycle/image-%s-%s", c.projectID, name)
	if c.dataRoot != "" {
		sha, berr := insertDemoBlob(c.ctx, c.tx, c.dataRoot, data, mime, c.now)
		if berr != nil {
			return seededArtifact{}, fmt.Errorf("write image blob: %w", berr)
		}
		uri = "blob:sha256/" + sha
	}
	id := NewID()
	if _, err := c.tx.ExecContext(c.ctx, `
		INSERT INTO artifacts
			(id, project_id, kind, name, uri, size, mime,
			 producer_agent_id, lineage_json, created_at)
		VALUES (?, ?, 'image', ?, ?, ?, ?, NULLIF(?, ''), '{}', ?)`,
		id, c.projectID, name, uri, int64(len(data)), mime,
		c.stewardID, c.now); err != nil {
		return seededArtifact{}, fmt.Errorf("insert image artifact: %w", err)
	}
	c.artifactsSeeded++
	return seededArtifact{id: id, name: name}, nil
}

type seededRun struct{ id string }

func seedRun(
	c *seedProjectCtx, status string, configBlob map[string]any,
) (seededRun, error) {
	id := NewID()
	cfg, _ := json.Marshal(configBlob)
	if _, err := c.tx.ExecContext(c.ctx, `
		INSERT INTO runs
			(id, project_id, config_json, seed, status,
			 started_at, finished_at, trackio_run_uri, agent_id, created_at)
		VALUES (?, ?, ?, 42, ?, ?, ?, ?, ?, ?)`,
		id, c.projectID, string(cfg), status, c.now, c.now,
		fmt.Sprintf("trackio://lifecycle/%s", id), c.stewardID, c.now); err != nil {
		return seededRun{}, fmt.Errorf("insert run: %w", err)
	}
	c.runsSeeded++

	// Emit metric curves (synthRunCurves, carried over from the retired
	// legacy seed) so the run-detail screen renders sparklines + charts
	// for each completed run. Non-completed runs (running/queued) skip
	// metrics — there's nothing meaningful to backfill yet.
	if status == "completed" {
		size := intFromConfig(configBlob, "n_embd", 384)
		opt := stringFromConfig(configBlob, "optimizer", "lion")
		iters := intFromConfig(configBlob, "iters", 1000)
		// Deterministic seed per run id: the project should look the same
		// every time the demo is re-seeded.
		rng := rand.New(rand.NewSource(deterministicSeed(id)))
		curves := synthRunCurves(rng, size, opt, iters, 100)
		for _, m := range curves {
			pointsJSON, _ := json.Marshal(m.points)
			if _, err := c.tx.ExecContext(c.ctx, `
				INSERT INTO run_metrics (
					id, run_id, metric_name, points_json, sample_count,
					last_step, last_value, updated_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				NewID(), id, m.name, string(pointsJSON),
				len(m.points), m.lastStep, m.lastValue, c.now,
			); err != nil {
				return seededRun{}, fmt.Errorf(
					"insert run_metrics %s: %w", m.name, err)
			}
		}
	}
	return seededRun{id: id}, nil
}

func intFromConfig(m map[string]any, key string, def int) int {
	v, ok := m[key]
	if !ok {
		return def
	}
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	}
	return def
}

func stringFromConfig(m map[string]any, key, def string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return def
}

// deterministicSeed turns an id (UUID-ish hex string) into a stable
// int64 so metric noise is reproducible per run across reseeds.
func deterministicSeed(id string) int64 {
	var h int64 = 1469598103934665603
	for i := 0; i < len(id); i++ {
		h ^= int64(id[i])
		h *= 1099511628211
	}
	if h < 0 {
		h = -h
	}
	return h
}

// ---------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------

func buildStructuredBody(
	schemaID string, sections []sectionSeed, now, ratifiedActor string,
) string {
	type sectionWire struct {
		Slug                    string `json:"slug"`
		Title                   string `json:"title,omitempty"`
		Body                    string `json:"body"`
		Status                  string `json:"status"`
		LastAuthoredAt          string `json:"last_authored_at,omitempty"`
		LastAuthoredBySessionID string `json:"last_authored_by_session_id,omitempty"`
		RatifiedAt              string `json:"ratified_at,omitempty"`
		RatifiedByActor         string `json:"ratified_by_actor,omitempty"`
	}
	type bodyWire struct {
		SchemaVersion int           `json:"schema_version"`
		SchemaID      string        `json:"schema_id"`
		Sections      []sectionWire `json:"sections"`
	}
	out := bodyWire{SchemaVersion: 1, SchemaID: schemaID}
	for _, s := range sections {
		w := sectionWire{Slug: s.slug, Title: s.title, Body: s.body, Status: s.status}
		switch s.status {
		case "draft":
			w.LastAuthoredAt = now
		case "ratified":
			w.LastAuthoredAt = now
			w.RatifiedAt = now
			w.RatifiedByActor = ratifiedActor
		}
		out.Sections = append(out.Sections, w)
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func buildPhaseHistory(past []string, current, now string) phaseHistoryDoc {
	doc := phaseHistoryDoc{}
	prev := ""
	for _, p := range past {
		doc.Transitions = append(doc.Transitions, phaseTransition{
			From: prev, To: p, At: now, ByActor: "system",
		})
		prev = p
	}
	doc.Transitions = append(doc.Transitions, phaseTransition{
		From: prev, To: current, At: now, ByActor: "system",
	})
	return doc
}

func emitAudit(
	ctx context.Context, tx *sql.Tx, action, targetKind, targetID,
	summary string, meta map[string]any, now string,
) int {
	metaJSON := "{}"
	if len(meta) > 0 {
		if b, err := json.Marshal(meta); err == nil {
			metaJSON = string(b)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_events
			(id, team_id, ts, actor_kind, actor_handle,
			 action, target_kind, target_id, summary, meta_json)
		VALUES (?, ?, ?, 'agent', 'steward.lifecycle', ?, ?, ?, ?, ?)`,
		NewID(), defaultTeamID, now,
		action, targetKind, targetID, summary, metaJSON); err != nil {
		return 0
	}
	return 1
}

func findDeliverableByLogID(dls []seededDeliverable, logID string) string {
	for _, d := range dls {
		if d.logID == logID {
			return d.id
		}
	}
	return ""
}

// ---------------------------------------------------------------------
// Section content (per-schema) — written here rather than as flat const
// strings so the per-state mix (empty/draft/ratified) stays
// self-documenting.
// ---------------------------------------------------------------------

func domainOverviewBody() string {
	return "## Domain overview\n\n" +
		"This study sits in the small-language-model optimizer-comparison\n" +
		"sub-area. The closest peers are the Lion paper (Chen et al. 2023)\n" +
		"and Cramming (Geiping & Goldstein 2023). Both cover one corner of\n" +
		"the (size × optimizer × budget) cube; this project completes the\n" +
		"square at sizes ≤ 384.\n"
}

func priorWorkBody() string {
	return "## Key prior work\n\n" +
		"- **Chen et al. 2023** — Lion. Symbolic-discovery optimizer\n" +
		"  with smaller memory footprint than AdamW; results on transformer\n" +
		"  language models.\n" +
		"- **Loshchilov & Hutter 2017** — AdamW. Decoupled weight decay\n" +
		"  baseline.\n" +
		"- **Karpathy nanoGPT** — Reference small-transformer training\n" +
		"  recipe used for setup.\n" +
		"- **Geiping & Goldstein 2023** — Cramming. Argues optimizer choice\n" +
		"  matters more at small batch sizes than large.\n"
}

func gapsDraftBody() string {
	return "## Research gaps\n\n" +
		"_Draft — pending steward+director review._\n\n" +
		"No direct A/B comparison of Lion vs AdamW at n_embd ≤ 384 with\n" +
		"a 1000-iter budget on a small text corpus exists. Closest peers\n" +
		"hold one variable constant while sweeping the other.\n"
}

func gapsRatifiedBody() string {
	return "## Research gaps\n\n" +
		"No direct A/B comparison of Lion vs AdamW at n_embd ≤ 384 with\n" +
		"a 1000-iter budget on a small text corpus exists. Closest peers\n" +
		"hold one variable constant while sweeping the other. This\n" +
		"project fills the (size × optimizer) gap at the small end.\n"
}

func positioningBody() string {
	return "## Project positioning\n\n" +
		"Three sizes (n_embd ∈ {128, 256, 384}) × two optimizers (Lion,\n" +
		"AdamW) × 1000 iters on Shakespeare. Reports val_loss at the end\n" +
		"of training. Goal: confirm or falsify Lion's small-scale\n" +
		"advantage over AdamW.\n"
}

func methodSectionSeeds() []sectionSeed {
	// "Method in progress" mix: 4 ratified + 1 draft + 2 empty.
	return []sectionSeed{
		{slug: "research-question", title: "Research question", status: "ratified",
			body: "Does Lion's reported optimizer advantage over AdamW persist " +
				"at n_embd ≤ 384 with a 1000-iter Shakespeare budget?\n"},
		{slug: "hypothesis", title: "Hypothesis", status: "ratified",
			body: "Lion's val_loss at iter 1000 is at least 0.02 lower than " +
				"AdamW's at every size in {128, 256, 384}.\n"},
		{slug: "approach", title: "Approach", status: "ratified",
			body: "Six runs (2 optimizers × 3 sizes); identical seed; " +
				"Shakespeare-char tokenization; nanoGPT defaults aside from " +
				"optimizer hyperparameters.\n"},
		{slug: "experimental-setup", title: "Experimental setup", status: "ratified",
			body: "Single A100 host, deterministic seeds, trackio logging\n" +
				"every 100 iters.\n"},
		{slug: "evaluation-plan", title: "Evaluation plan", status: "draft",
			body: "_Draft._ Final-step val_loss is the headline metric. Need\n" +
				"to decide whether to also report intermediate-step deltas.\n"},
		{slug: "risks", title: "Risks", status: "empty"},
		{slug: "budget", title: "Budget", status: "empty"},
	}
}

func methodSectionSeedsRatified() []sectionSeed {
	out := make([]sectionSeed, 0, 7)
	for _, s := range methodSectionSeeds() {
		s.status = "ratified"
		if s.body == "" {
			s.body = "Frozen at method-ratify time.\n"
		}
		out = append(out, s)
	}
	return out
}

func litReviewSectionSeedsRatified() []sectionSeed {
	return []sectionSeed{
		{slug: "domain-overview", title: "Domain overview", status: "ratified",
			body: domainOverviewBody()},
		{slug: "prior-work", title: "Key prior work", status: "ratified",
			body: priorWorkBody()},
		{slug: "gaps", title: "Research gaps", status: "ratified",
			body: gapsRatifiedBody()},
		{slug: "positioning", title: "Project positioning", status: "ratified",
			body: positioningBody()},
	}
}

func experimentReportDraftSeeds() []sectionSeed {
	// 2 draft, 1 empty so the W5a viewer + W5b component pip both land
	// in mixed states.
	return []sectionSeed{
		{slug: "setup-recap", title: "Setup recap", status: "draft",
			body: "_Draft._ Six runs (Lion vs AdamW × {128, 256, 384}) on\n" +
				"a single A100 with the nanoGPT-Shakespeare default split.\n"},
		{slug: "results", title: "Results", status: "draft",
			body: "_Draft._ Best val_loss on n_embd=384 was Lion@1.74 vs\n" +
				"AdamW@1.81. Lion advantage at small sizes is +0.04–+0.07\n" +
				"depending on size.\n"},
		{slug: "ablations", title: "Ablations", status: "empty"},
		{slug: "analysis", title: "Analysis", status: "empty"},
		{slug: "limitations", title: "Limitations", status: "empty"},
	}
}

func experimentReportRatifiedSeeds() []sectionSeed {
	out := make([]sectionSeed, 0, 5)
	for _, s := range experimentReportDraftSeeds() {
		s.status = "ratified"
		if s.body == "" {
			s.body = "Filled in for the ratified report.\n"
		}
		out = append(out, s)
	}
	return out
}

func paperDraftSeeds() []sectionSeed {
	// Mixed: 7 ratified, 1 draft, 1 empty — exercises every pip.
	return []sectionSeed{
		{slug: "abstract", title: "Abstract", status: "ratified",
			body: "We compare Lion and AdamW at small transformer sizes …\n"},
		{slug: "introduction", title: "Introduction", status: "ratified",
			body: "Optimizer choice at small scale remains under-studied. …\n"},
		{slug: "related-work", title: "Related work", status: "ratified",
			body: "Lion (Chen 2023), AdamW (Loshchilov & Hutter 2017), …\n"},
		{slug: "method", title: "Method", status: "ratified",
			body: "We follow nanoGPT defaults aside from the optimizer …\n"},
		{slug: "experiments", title: "Experiments", status: "ratified",
			body: "Six runs across (size × optimizer); identical seeds …\n"},
		{slug: "results", title: "Results", status: "ratified",
			body: "Lion outperforms AdamW at every size by 0.04–0.07 in val_loss.\n"},
		{slug: "discussion", title: "Discussion", status: "draft",
			body: "_Draft._ The advantage at small scale is consistent with\n" +
				"Geiping 2023's claim, but our budget is much tighter.\n"},
		{slug: "conclusion", title: "Conclusion", status: "ratified",
			body: "Lion's advantage holds at small scale within our budget.\n"},
		{slug: "references", title: "References", status: "empty"},
	}
}
