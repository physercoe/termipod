// seed_demo_lifecycle.go — `--shape lifecycle` extension to the
// existing seed-demo harness (W6 of the lifecycle wedge plan).
//
// Stages a multi-phase research project so a reviewer can tap into
// any phase and see realistic state without running the lifecycle
// live. Distinct from `--shape ablation` (the original Candidate A
// single-phase demo, which still works for phase-3 isolation
// testing — see run-the-demo.md).
//
// What gets inserted:
//
//   - 1 project (kind=goal, name=research-lifecycle-demo)
//   - 1 plan with a 5-phase spec_json mirroring research-project.v1
//   - 5 plan_steps: phase 0 + 1 completed, phase 2 in_progress,
//     phases 3 + 4 pending
//   - 2 agents: a domain steward (running, owns phases 1-4) and a
//     coder worker (running, working on phase 2)
//   - 2 documents: lit-review report (phase 1 output), partial
//     method draft (phase 2 in progress)
//   - 1 attention_item: pending phase-2 approval gate (the
//     director's next action)
//
// Idempotent — re-running with the same name reports the existing
// project. Use `--reset` to wipe and refresh.

package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// SeedLifecycleResult summarises the lifecycle seed insert.
type SeedLifecycleResult struct {
	ProjectID        string
	PlanID           string
	StewardAgentID   string
	CoderAgentID     string
	LitReviewDocID   string
	MethodDocID      string
	AttentionID      string
	Skipped          bool
	Reset            bool
}

const lifecycleDemoProjectName = "research-lifecycle-demo"

// ResetLifecycleDemo deletes the prior lifecycle demo project and
// dependent rows. Mirrors ResetDemo's transactional shape; safe to
// call when no demo exists.
func ResetLifecycleDemo(ctx context.Context, db *sql.DB) (deleted bool, err error) {
	if db == nil {
		return false, fmt.Errorf("ResetLifecycleDemo: nil db")
	}
	var projectID string
	err = db.QueryRowContext(ctx,
		`SELECT id FROM projects WHERE team_id = ? AND name = ?`,
		defaultTeamID, lifecycleDemoProjectName).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lookup lifecycle demo: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	// Order matters — child rows first, then the project. ON DELETE
	// CASCADE on projects covers most of these (plans, attention_items),
	// but agents reference team_id rather than project_id and need
	// explicit cleanup.
	statements := []struct{ sql string }{
		{`DELETE FROM attention_items WHERE project_id = ?`},
		{`DELETE FROM documents WHERE project_id = ?`},
		{`DELETE FROM plan_steps WHERE plan_id IN (SELECT id FROM plans WHERE project_id = ?)`},
		{`DELETE FROM plans WHERE project_id = ?`},
		// agents have no project_id column; clean by handle convention
		// — the seed handles are namespaced under @lifecycle-* so
		// they're easy to find without false positives.
		{`DELETE FROM agents WHERE team_id = ? AND handle LIKE '@lifecycle-%'`},
		{`DELETE FROM projects WHERE id = ?`},
	}
	for i, st := range statements {
		var arg any = projectID
		if i == 4 {
			arg = defaultTeamID
		}
		var execErr error
		if i == 4 {
			_, execErr = tx.ExecContext(ctx, st.sql, defaultTeamID)
		} else {
			_, execErr = tx.ExecContext(ctx, st.sql, arg)
		}
		if execErr != nil {
			return false, fmt.Errorf("reset step %d: %w", i, execErr)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit reset: %w", err)
	}
	return true, nil
}

// SeedLifecycleDemo inserts a 5-phase research project with mixed
// per-phase state so the mobile UI can render every checkpoint of
// run-lifecycle-demo.md without the lifecycle running live.
func SeedLifecycleDemo(ctx context.Context, db *sql.DB) (*SeedLifecycleResult, error) {
	if db == nil {
		return nil, fmt.Errorf("SeedLifecycleDemo: nil db")
	}
	var existingID string
	err := db.QueryRowContext(ctx,
		`SELECT id FROM projects WHERE team_id = ? AND name = ?`,
		defaultTeamID, lifecycleDemoProjectName).Scan(&existingID)
	if err == nil {
		return &SeedLifecycleResult{ProjectID: existingID, Skipped: true}, nil
	}
	if err != sql.ErrNoRows {
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

	res := &SeedLifecycleResult{}
	now := NowUTC()

	// 1. The project. parameters_json carries the director's idea
	//    just as a real lifecycle project would.
	res.ProjectID = NewID()
	params, _ := json.Marshal(map[string]any{
		"idea": "Compare Lion vs AdamW on tiny GPT pretraining; does Lion's advantage hold across model sizes?",
	})
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO projects
			(id, team_id, name, status, config_yaml, created_at,
			 goal, kind, is_template, template_id, parameters_json)
		VALUES (?, ?, ?, 'active', '', ?, ?, 'goal', 0,
		        'research-project.v1', ?)`,
		res.ProjectID, defaultTeamID, lifecycleDemoProjectName, now,
		"Lifecycle demo: idea → lit-review → method → experiment → paper",
		string(params),
	); err != nil {
		return nil, fmt.Errorf("insert project: %w", err)
	}

	// 2. Plan with spec_json mirroring research-project.v1's 5
	//    phases. The director's plan viewer reads spec_json.
	res.PlanID = NewID()
	planSpec := lifecyclePlanSpecJSON()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO plans
			(id, project_id, template_id, version, spec_json, status,
			 created_at, started_at)
		VALUES (?, ?, 'research-project.v1', 1, ?, 'running', ?, ?)`,
		res.PlanID, res.ProjectID, planSpec, now, now,
	); err != nil {
		return nil, fmt.Errorf("insert plan: %w", err)
	}

	// 3. Five plan_steps, one per phase. Statuses:
	//    0 completed, 1 completed, 2 in_progress, 3+4 pending.
	phaseStates := []struct {
		idx    int
		kind   string
		status string
		spec   string
	}{
		{0, "agent_driven", "completed", `{"phase":"bootstrap","steward":"steward.general.v1"}`},
		{1, "agent_driven", "completed", `{"phase":"lit_review","workers":["lit-reviewer.v1"]}`},
		{2, "agent_driven", "in_progress", `{"phase":"method_and_code","workers":["coder.v1"]}`},
		{3, "agent_driven", "pending", `{"phase":"experiment","workers":["ml-worker.v1"]}`},
		{4, "agent_driven", "pending", `{"phase":"paper","workers":["paper-writer.v1"]}`},
	}
	for _, st := range phaseStates {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO plan_steps
				(id, plan_id, phase_idx, step_idx, kind, spec_json, status)
			VALUES (?, ?, ?, 0, ?, ?, ?)`,
			NewID(), res.PlanID, st.idx, st.kind, st.spec, st.status,
		); err != nil {
			return nil, fmt.Errorf("insert plan step %d: %w", st.idx, err)
		}
	}

	// 4. Agents: domain steward (running, owns the project) +
	//    coder (running, working on phase 2). General steward is
	//    deliberately NOT seeded as a project-scoped agent — it's
	//    team-scoped and persistent; the seed reflects the post-
	//    bootstrap state where it has handed off.
	res.StewardAgentID = NewID()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agents
			(id, team_id, handle, kind, capabilities_json,
			 status, pause_state, created_at)
		VALUES (?, ?, ?, ?, '[]', 'running', 'running', ?)`,
		res.StewardAgentID, defaultTeamID, "@lifecycle-steward",
		"steward.research.v1", now,
	); err != nil {
		return nil, fmt.Errorf("insert steward: %w", err)
	}
	res.CoderAgentID = NewID()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agents
			(id, team_id, handle, kind, capabilities_json,
			 parent_agent_id, status, pause_state, created_at)
		VALUES (?, ?, ?, ?, '[]', ?, 'running', 'running', ?)`,
		res.CoderAgentID, defaultTeamID, "@lifecycle-coder",
		"coder.v1", res.StewardAgentID, now,
	); err != nil {
		return nil, fmt.Errorf("insert coder: %w", err)
	}

	// 5. Phase artifacts. Lit-review (phase 1 done) is a complete
	//    document; method draft (phase 2 in progress) is partial.
	res.LitReviewDocID = NewID()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO documents
			(id, project_id, kind, title, version,
			 content_inline, author_agent_id, created_at)
		VALUES (?, ?, 'report', ?, 1, ?, ?, ?)`,
		res.LitReviewDocID, res.ProjectID,
		"Lit review: Lion vs AdamW on tiny GPT",
		litReviewBody(), res.StewardAgentID, now,
	); err != nil {
		return nil, fmt.Errorf("insert lit-review doc: %w", err)
	}
	res.MethodDocID = NewID()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO documents
			(id, project_id, kind, title, version,
			 content_inline, author_agent_id, created_at)
		VALUES (?, ?, 'memo', ?, 1, ?, ?, ?)`,
		res.MethodDocID, res.ProjectID,
		"Method (draft): nanoGPT-Shakespeare optimizer × size sweep",
		methodDraftBody(), res.CoderAgentID, now,
	); err != nil {
		return nil, fmt.Errorf("insert method draft doc: %w", err)
	}

	// 6. Attention item: pending phase-2 approval gate. The director
	//    will see this on the Me tab as the next action — "method
	//    & code ready, approve to start the experiment".
	res.AttentionID = NewID()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO attention_items
			(id, project_id, scope_kind, scope_id, kind,
			 summary, severity, current_assignees_json,
			 status, created_at)
		VALUES (?, ?, 'project', ?, 'select',
		        ?, 'major', '["@principal"]',
		        'open', ?)`,
		res.AttentionID, res.ProjectID, res.ProjectID,
		"Method + code ready for phase 2 approval. Approve to begin the experiment, request revisions, or abort.",
		now,
	); err != nil {
		return nil, fmt.Errorf("insert attention item: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return res, nil
}

// lifecyclePlanSpecJSON returns the spec_json blob for the seeded
// plan. Mirrors the structure in research-project.v1.yaml's
// `phases` array, abbreviated to what the plan viewer needs to
// render the 5-phase outline.
//
// Uses an explicit json.Encoder with SetEscapeHTML(false) — the
// default Marshal HTML-escapes `&` (and `<`, `>`) which would turn
// "Method & Code" into "Method & Code", an unnecessary
// surprise for the plan viewer's display.
func lifecyclePlanSpecJSON() string {
	spec := map[string]any{
		"template": "research-project.v1",
		"phases": []map[string]any{
			{"idx": 0, "name": "Bootstrap", "kind": "agent_driven", "goal": "General steward authors templates + plan", "steward": "steward.general.v1"},
			{"idx": 1, "name": "Lit Review", "kind": "agent_driven", "goal": "Survey relevant work", "steward": "steward.research.v1", "workers": []string{"lit-reviewer.v1"}},
			{"idx": 2, "name": "Method & Code", "kind": "agent_driven", "goal": "Implement experiment + freeze matrix", "steward": "steward.research.v1", "workers": []string{"coder.v1"}},
			{"idx": 3, "name": "Experiment", "kind": "agent_driven", "goal": "Run matrix on GPU host", "steward": "steward.research.v1", "workers": []string{"ml-worker.v1"}},
			{"idx": 4, "name": "Paper", "kind": "agent_driven", "goal": "Write 6-section paper", "steward": "steward.research.v1", "workers": []string{"paper-writer.v1"}},
		},
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(spec)
	// json.Encoder.Encode appends a trailing newline; trim it.
	return strings.TrimRight(buf.String(), "\n")
}

// litReviewBody returns the seeded lit-review document content.
// Sample-quality — five fake citations on the project's idea so the
// document viewer renders a realistic page.
func litReviewBody() string {
	return `# Lit review: Lion vs AdamW on tiny GPT

**Scope:** This review covers optimizer comparisons on small
transformer language models (≤1B params) trained from scratch on
small text corpora.

**Headline finding:** Lion shows modest advantages over AdamW on
small models; the gap narrows or reverses at certain sequence-length
and warmup-schedule combinations.

## Foundational work

- **Chen et al. (2023)** [arxiv:2302.06675] — *Symbolic Discovery
  of Optimization Algorithms*. Introduces Lion; shows competitive
  results on transformer language models with smaller memory
  footprint than AdamW.
- **Kingma & Ba (2014)** [arxiv:1412.6980] — Original Adam paper;
  AdamW (Loshchilov & Hutter, 2017) [arxiv:1711.05101] is the
  reference baseline this study should compare against.

## At small scale

- **Karpathy nanoGPT** [github.com/karpathy/nanoGPT] — De-facto
  reference for tiny GPT pretraining. Configurable, MIT-licensed,
  no fork required. Default trains on Shakespeare with AdamW.
- **Geiping & Goldstein (2023)** [arxiv:2212.14034] — *Cramming:
  Training a Language Model on a Single GPU in One Day*. Notes
  that optimizer choice matters more at small batch sizes than at
  large.

## Open question this study addresses

The cited work covers Lion at large scale (Chen 2023) and AdamW
at small scale (Geiping 2023), but no direct A/B comparison at
the model sizes this study targets (n_embd ∈ {128, 256, 384},
1000 iters on Shakespeare). The result-summary phase will fill
this gap.

## What's known
- Lion has lower memory cost than AdamW.
- AdamW is robust across schedule choices.
- Optimizer effects are sensitive to batch size at small scale.

## What's open
- Lion's behavior at n_embd ≤ 384 with limited training budget.
- Whether the optimizer × size interaction is monotonic.

## References
- [arxiv:2302.06675](https://arxiv.org/abs/2302.06675)
- [arxiv:1412.6980](https://arxiv.org/abs/1412.6980)
- [arxiv:1711.05101](https://arxiv.org/abs/1711.05101)
- [github.com/karpathy/nanoGPT](https://github.com/karpathy/nanoGPT)
- [arxiv:2212.14034](https://arxiv.org/abs/2212.14034)
`
}

// methodDraftBody returns the seeded partial method-spec content
// (phase 2 is in_progress; the document is a draft, not the
// frozen spec).
func methodDraftBody() string {
	return `# Method (draft): nanoGPT-Shakespeare optimizer × size sweep

**Status:** Draft — coder is still implementing the training loop.
Will freeze the experiment matrix once smoke-test passes.

## Dataset
Shakespeare (the karpathy/nanoGPT default split). Tokenized at
char level for simplicity.

## Model
nanoGPT (karpathy reference implementation), unmodified. Sizes:
- n_embd ∈ {128, 256, 384}
- depth fixed at 6 layers
- Other hyperparameters per nanoGPT defaults

## Training loop
PyTorch + the karpathy nanoGPT training script with two
modifications:
1. Optimizer parametrized — AdamW (baseline) vs Lion
2. Trackio integration for metric logging

## Optimizer settings
- AdamW: lr=1e-3, betas=(0.9, 0.95), wd=0.1
- Lion: lr=2e-4, betas=(0.9, 0.99), wd=0.1
  (Following Chen et al. 2023's recommended scaling — Lion
  needs ~5× smaller lr than AdamW)

## Evaluation
Validation loss every 100 iters. Final reported metric is
val_loss at iter=1000.

## Experiment matrix (draft — to freeze on smoke-test pass)
| cell | n_embd | optimizer |
|---|---|---|
| 1 | 128 | adamw |
| 2 | 128 | lion |
| 3 | 256 | adamw |
| 4 | 256 | lion |
| 5 | 384 | adamw |
| 6 | 384 | lion |

## Code
Worktree at ~/hub-work/coder/<spawn-id>/lifecycle-demo. Smoke test
not yet run. Will add commit SHA + entry-point command to this
document before requesting steward approval.

## TODO before freeze
- Run smoke test (` + "`python train.py --iters 1`" + `)
- Pin requirements.txt versions
- Verify trackio writes are visible to host-runner
- Write reproducibility section
`
}
