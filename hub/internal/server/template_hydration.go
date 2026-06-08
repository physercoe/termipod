package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	hub "github.com/termipod/hub"
	"gopkg.in/yaml.v3"
)

// W7 — phase-criteria hydration. The research template's YAML carries
// per-phase specs under `phase_specs:` (per A6 §3-§7); on project
// create we hydrate the *initial phase's* criteria so the project lands
// with a real `scope-ratified` text criterion (or whatever the template
// declares). Deliverable hydration + per-phase-advance hydration are
// the W7 follow-up; this is the minimal-but-real cut so the chassis
// has something to read on day 1.

// phaseSpecsHead is the only piece of the template YAML this code
// cares about. We deliberately do NOT couple to the rest of the schema
// (deliverables, transitions, worker_hints, section_schemas, …) —
// those are steward-consumed today; the chassis only needs phases +
// per-phase criteria for hydration plus per-phase overview_widget for
// hero swap-in.
type phaseSpecsHead struct {
	PhaseSpecs map[string]struct {
		OverviewWidget string                 `yaml:"overview_widget"`
		Criteria       []phaseCriterionSpec   `yaml:"criteria"`
		Deliverables   []phaseDeliverableSpec `yaml:"deliverables"`
		Tiles          []string               `yaml:"tiles"`
		// Tasks + Plan are part of the inline project spec (ADR-046). They
		// are parsed here so the schema validates and so WS1 can materialize
		// them at create (a `tasks.phase`-stamped row per task; a draft
		// `plans`/`plan_steps` row from the plan). The chassis does not act
		// on them directly — the steward does, post-Start.
		Tasks []phaseTaskSpec `yaml:"tasks"`
		Plan  *phasePlanSpec  `yaml:"plan"`
	} `yaml:"phase_specs"`
}

// phaseTaskSpec is one `phase_specs[<phase>].tasks[]` entry — a first-class
// task (ADR-029) seeded at project create. Only title/ord are load-bearing;
// id is the template-local handle (audit + future cross-references).
type phaseTaskSpec struct {
	ID    string `yaml:"id"`
	Title string `yaml:"title"`
	Ord   *int   `yaml:"ord"`
}

// phasePlanSpec is the `phase_specs[<phase>].plan` block — an ordered set of
// steps WS1 seeds as a draft `plans` + `plan_steps` row (tables exist,
// migration 0009). The steward owns execution semantics post-Start.
type phasePlanSpec struct {
	Title string              `yaml:"title"`
	Steps []phasePlanStepSpec `yaml:"steps"`
}

// phasePlanStepSpec is one ordered step of a phase plan.
type phasePlanStepSpec struct {
	Title string `yaml:"title"`
	Ord   *int   `yaml:"ord"`
}

// phaseTemplateTiles returns the per-phase tile slugs declared in the
// template YAML's `phase_specs:` block. Returned shape:
// `{"<phase>": ["documents", "outputs", ...]}`. Missing template /
// missing `tiles:` field → nil (mobile then falls through to the Dart
// chassis default). Read on each call (template set is small, low-QPS
// reads — same caching tradeoff as phaseOverviewWidget).
func (s *Server) phaseTemplateTiles(templateID string) map[string][]string {
	if templateID == "" {
		return nil
	}
	body := s.readProjectTemplateYAML(templateID)
	if body == "" {
		return nil
	}
	var head phaseSpecsHead
	if err := yaml.Unmarshal([]byte(body), &head); err != nil {
		return nil
	}
	out := make(map[string][]string)
	for phase, spec := range head.PhaseSpecs {
		if len(spec.Tiles) == 0 {
			continue
		}
		// Pass slugs through verbatim; mobile-side `_slugFromString`
		// drops unknowns. The chassis (Go) doesn't care about the
		// vocab, only the composition.
		out[phase] = append([]string(nil), spec.Tiles...)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// phaseTemplateOverviewWidgets returns the per-phase hero slugs declared
// in the template YAML's `phase_specs[<phase>].overview_widget`. Mirrors
// phaseTemplateTiles. Returned shape: `{"<phase>": "<slug>"}`. Missing
// template / no per-phase entries → nil (mobile picker shows the global
// template default instead).
//
// Used by the projectOut.OverviewWidgetTemplate field so the mobile hero
// picker can show "what would this phase render without the override"
// alongside the live override value.
func (s *Server) phaseTemplateOverviewWidgets(templateID string) map[string]string {
	if templateID == "" {
		return nil
	}
	body := s.readProjectTemplateYAML(templateID)
	if body == "" {
		return nil
	}
	var head phaseSpecsHead
	if err := yaml.Unmarshal([]byte(body), &head); err != nil {
		return nil
	}
	out := make(map[string]string)
	for phase, spec := range head.PhaseSpecs {
		if spec.OverviewWidget == "" {
			continue
		}
		if !validOverviewWidgets[spec.OverviewWidget] {
			continue
		}
		out[phase] = spec.OverviewWidget
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// phaseOverviewWidget returns the phase-scoped overview_widget declared
// at phase_specs[<phase>].overview_widget, or "" when the template
// doesn't declare one for that phase. Empty result means "fall back to
// the project-level overview_widget".
func (s *Server) phaseOverviewWidget(templateID, phase string) string {
	if templateID == "" || phase == "" {
		return ""
	}
	body := s.readProjectTemplateYAML(templateID)
	if body == "" {
		return ""
	}
	var head phaseSpecsHead
	if err := yaml.Unmarshal([]byte(body), &head); err != nil {
		return ""
	}
	specs, ok := head.PhaseSpecs[phase]
	if !ok {
		return ""
	}
	if specs.OverviewWidget == "" {
		return ""
	}
	if !validOverviewWidgets[specs.OverviewWidget] {
		return ""
	}
	return specs.OverviewWidget
}

type phaseCriterionSpec struct {
	ID             string         `yaml:"id"`
	Kind           string         `yaml:"kind"`
	Body           map[string]any `yaml:"body"`
	DeliverableRef string         `yaml:"deliverable_ref"`
	Required       *bool          `yaml:"required"`
	Ord            *int           `yaml:"ord"`
}

// phaseDeliverableSpec is one `phase_specs[<phase>].deliverables[]` entry
// (see templates/projects/research.v1.yaml). DisplayName,
// RatificationAuthority, and Components are richer than the runtime
// `deliverables` table (id/project_id/phase/kind/ratification_state/
// required/ord): the table has no column for them and the component
// `ref`s are template slugs, not real document/artifact rows that exist
// at hydration time. So hydration creates the draft deliverable slot
// (kind/required/ord); components are attached at runtime as the agent
// produces them, and DisplayName/RatificationAuthority are forward-
// looking spec the table doesn't persist yet. Parsed in full so the
// unmarshal doesn't choke and so a later migration can pick them up.
type phaseDeliverableSpec struct {
	ID                    string `yaml:"id"`
	Kind                  string `yaml:"kind"`
	DisplayName           string `yaml:"display_name"`
	RatificationAuthority string `yaml:"ratification_authority"`
	Required              *bool  `yaml:"required"`
	Ord                   *int   `yaml:"ord"`
	Components            []struct {
		Kind string `yaml:"kind"`
		Ref  string `yaml:"ref"`
	} `yaml:"components"`
}

// projectSpecYAML returns the YAML spec that defines a project's phases,
// deliverables, criteria, and tasks. In the inline-spec model (ADR-046) the
// authoritative source is the project's OWN config_yaml; when that carries
// no `phase_specs:` we fall back to the named template file (the
// template-by-id path) so already-created projects keep hydrating. Empty on
// both sides → "" (caller skips hydration).
func (s *Server) projectSpecYAML(ctx context.Context, project, templateID string) string {
	if project != "" {
		var cfg sql.NullString
		if err := s.db.QueryRowContext(ctx,
			`SELECT config_yaml FROM projects WHERE id = ?`, project).Scan(&cfg); err == nil &&
			cfg.Valid && strings.TrimSpace(cfg.String) != "" {
			var head phaseSpecsHead
			if yaml.Unmarshal([]byte(cfg.String), &head) == nil && len(head.PhaseSpecs) > 0 {
				return cfg.String
			}
		}
	}
	return s.readProjectTemplateYAML(templateID)
}

// projectPhases resolves a project's ordered phase set, preferring the
// inline spec's own `phases:` (ADR-046) and falling back to the named
// template's phase list. Used at create to decide the initial phase and to
// drive early-bind materialization of every phase.
func (s *Server) projectPhases(configYAML, templateID string) []string {
	if strings.TrimSpace(configYAML) != "" {
		var doc struct {
			Phases phaseNameList `yaml:"phases"`
		}
		if yaml.Unmarshal([]byte(configYAML), &doc) == nil && len(doc.Phases) > 0 {
			return []string(doc.Phases)
		}
	}
	return s.templatePhases(templateID)
}

// projectSpecOnCreateTemplateID extracts the `on_create_template_id:` field
// (the bound domain steward, ADR-046) from a project's inline config_yaml.
// Returns "" when the spec is empty or declares no steward; callers fall back
// to the request field or leave the column NULL.
func projectSpecOnCreateTemplateID(configYAML string) string {
	if strings.TrimSpace(configYAML) == "" {
		return ""
	}
	var doc struct {
		OnCreateTemplateID string `yaml:"on_create_template_id"`
	}
	if yaml.Unmarshal([]byte(configYAML), &doc) != nil {
		return ""
	}
	return strings.TrimSpace(doc.OnCreateTemplateID)
}

// projectSpecGoal extracts the `goal:` text from a project's inline
// config_yaml (ADR-046 — the spec carries its own goal). Returns "" when the
// spec is empty or declares no goal; the create path then falls back to the
// named template's goal.
func projectSpecGoal(configYAML string) string {
	if strings.TrimSpace(configYAML) == "" {
		return ""
	}
	var doc struct {
		Goal string `yaml:"goal"`
	}
	if yaml.Unmarshal([]byte(configYAML), &doc) != nil {
		return ""
	}
	return strings.TrimSpace(doc.Goal)
}

// readProjectTemplateYAML loads the raw YAML for a project template. The
// loader resolution order matches loadProjectTemplates: disk overlay
// first, then the embedded FS. Returns "" when the template doesn't
// exist on either side; callers treat that as "skip hydration".
func (s *Server) readProjectTemplateYAML(templateID string) string {
	if templateID == "" {
		return ""
	}
	// find scans the disk overlays (highest-priority team overlay first, cf.
	// loadProjectTemplates) then the embedded FS for a file whose basename is
	// `<id>.yaml` or whose `name:` field equals id (matchProjectTemplateName).
	find := func(id string) string {
		for _, dir := range projectTemplateDiskDirs(s.cfg.DataRoot) {
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
					continue
				}
				p := filepath.Join(dir, e.Name())
				data, err := os.ReadFile(p)
				if err != nil {
					continue
				}
				if matchProjectTemplateName(data, p, id) {
					return string(data)
				}
			}
		}
		matches, _ := fs.Glob(hub.TemplatesFS, "templates/projects/*.yaml")
		for _, p := range matches {
			data, err := fs.ReadFile(hub.TemplatesFS, p)
			if err != nil {
				continue
			}
			if matchProjectTemplateName(data, p, id) {
				return string(data)
			}
		}
		return ""
	}
	if body := find(templateID); body != "" {
		return body
	}
	// #41: templateID may be the template's DB-row ULID rather than its
	// YAML name. Resolve and retry once so hydration reads the right file
	// regardless of which form the project stored.
	if name := s.templateNameForID(templateID); name != "" && name != templateID {
		return find(name)
	}
	return ""
}

// hydratePhaseCriteria reads the template YAML's `phase_specs[<phase>]
// .criteria` block and creates `acceptance_criteria` rows. Errors are
// logged and swallowed — hydration must never fail project creation.
func (s *Server) hydratePhaseCriteria(
	ctx context.Context, team, project, templateID, phase string,
) {
	body := s.projectSpecYAML(ctx, project, templateID)
	if body == "" {
		return
	}
	var head phaseSpecsHead
	if err := yaml.Unmarshal([]byte(body), &head); err != nil {
		s.log.Warn("phase_specs unmarshal", "err", err, "template", templateID)
		return
	}
	specs, ok := head.PhaseSpecs[phase]
	if !ok {
		return
	}
	// Idempotency: if this phase already has criteria rows, it was
	// hydrated before (project create, a prior phase entry, or a repair).
	// Skip so re-entry / rollback-then-readvance can't duplicate.
	if n, err := s.countRowsForPhase(ctx, "acceptance_criteria", project, phase); err == nil && n > 0 {
		return
	}
	// Resolved once for the whole phase: the project's parameters (for
	// {placeholder} substitution in criteria bodies, #27) and a map from
	// each template deliverable id → its kind (to rewrite gate references
	// onto the runtime deliverable ULID, #21). Deliverables are hydrated
	// before criteria (see hydratePhase), so their rows already exist.
	params := s.projectParams(ctx, project)
	kindByTemplateDeliverableID := map[string]string{}
	for _, d := range specs.Deliverables {
		if d.ID != "" && d.Kind != "" {
			kindByTemplateDeliverableID[d.ID] = d.Kind
		}
	}
	for _, c := range specs.Criteria {
		if c.Kind == "" {
			continue
		}
		if !isValidCriterionKind(c.Kind) {
			s.log.Warn("hydrate criterion: invalid kind",
				"template", templateID, "phase", phase, "kind", c.Kind)
			continue
		}
		required := 1
		if c.Required != nil && !*c.Required {
			required = 0
		}
		ord := 0
		if c.Ord != nil {
			ord = *c.Ord
		}
		bodyJSON := "{}"
		if len(c.Body) > 0 {
			// Resolve {placeholder} tokens in the body text (#27) and, for
			// gate criteria, rewrite body.params.deliverable_id from the
			// template-level id to the runtime deliverable ULID (#21).
			substituteParamsInMap(c.Body, params)
			if c.Kind == "gate" {
				s.resolveGateDeliverableRef(ctx, project, phase, c.Body, kindByTemplateDeliverableID)
			}
			b, err := json.Marshal(c.Body)
			if err == nil {
				bodyJSON = string(b)
			}
		}
		id := NewID()
		now := NowUTC()
		_, err := s.writeDB.ExecContext(ctx, `
			INSERT INTO acceptance_criteria (id, project_id, phase, kind, body,
				state, required, ord, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, 'pending', ?, ?, ?, ?)`,
			id, project, phase, c.Kind, bodyJSON, required, ord, now, now)
		if err != nil {
			s.log.Warn("hydrate criterion: insert", "err", err,
				"template", templateID, "phase", phase, "criterion_id", c.ID)
			continue
		}
		s.recordAudit(ctx, team, "criterion.created", "criterion", id,
			fmt.Sprintf("hydrated %s criterion in phase %s", c.Kind, phase),
			map[string]any{
				"project_id":       project,
				"phase":            phase,
				"kind":             c.Kind,
				"hydrated_from":    templateID,
				"template_crit_id": c.ID,
			})
	}
	_ = errors.New // keep errors imported in case callers want it later
}

// projectParams reads a project's parameters_json as a decoded map for
// {placeholder} substitution. Returns nil (not an error) when the project
// has no parameters or the JSON is unreadable — substitution is best-effort.
func (s *Server) projectParams(ctx context.Context, project string) map[string]any {
	var raw sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT parameters_json FROM projects WHERE id = ?`, project).Scan(&raw)
	if err != nil || !raw.Valid || raw.String == "" {
		return nil
	}
	m := map[string]any{}
	if json.Unmarshal([]byte(raw.String), &m) != nil {
		return nil
	}
	return m
}

// resolveGateDeliverableRef rewrites a hydrated gate criterion's
// body.params.deliverable_id from a template-level deliverable id (e.g.
// "env-setup-report") to the runtime deliverable ULID, bridged by kind (#21).
//
// The template criterion names the deliverable by its template id, but
// hydratePhaseDeliverables minted a fresh ULID for the runtime row and kept
// only its kind. cascadeDeliverableRatified fires a gate when
// body.params.deliverable_id == the just-ratified deliverable's ULID — so an
// un-rewritten gate (still holding the template id) would never match and the
// phase would never auto-advance. We resolve template-id → kind → the runtime
// row's ULID in the same phase and patch the reference in place.
//
// No-op when the body has no params.deliverable_id, the id isn't one this
// template declared (so it's already a ULID or points elsewhere), or no
// runtime deliverable of that kind exists yet.
func (s *Server) resolveGateDeliverableRef(
	ctx context.Context, project, phase string,
	body map[string]any, kindByTemplateDeliverableID map[string]string,
) {
	params, ok := body["params"].(map[string]any)
	if !ok {
		return
	}
	ref, ok := params["deliverable_id"].(string)
	if !ok || ref == "" {
		return
	}
	kind, ok := kindByTemplateDeliverableID[ref]
	if !ok {
		return
	}
	var ulid string
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM deliverables
		 WHERE project_id = ? AND phase = ? AND kind = ?
		 ORDER BY created_at LIMIT 1`, project, phase, kind).Scan(&ulid)
	if err != nil || ulid == "" {
		return
	}
	params["deliverable_id"] = ulid
}

// hydratePhase instantiates a phase's template-declared deliverables and
// criteria as DB rows. Called on project create (phase[0]) and on phase
// advance (the newly-entered phase) so a project's deliverables/criteria
// panels reflect its template's phase_specs instead of starting empty
// (issue #20). Both underlying hydrators are idempotent — re-entry after
// a rollback, or a repair, won't duplicate. Best-effort: a hydration
// failure is logged inside each helper, never fatal to the transition.
func (s *Server) hydratePhase(ctx context.Context, team, project, templateID, phase string) {
	// templateID may be empty for an inline-spec project (ADR-046): the spec
	// then lives in the project's own config_yaml, which projectSpecYAML
	// resolves. Only an empty phase is a hard no-op.
	if phase == "" {
		return
	}
	s.hydratePhaseDeliverables(ctx, team, project, templateID, phase)
	s.hydratePhaseCriteria(ctx, team, project, templateID, phase)
	s.hydratePhaseTasks(ctx, team, project, templateID, phase)
}

// hydratePhaseTasks walks the spec's phase_specs[<phase>].tasks and creates
// a first-class `tasks` row per entry (ADR-029), stamped with the phase
// (#22). Mirrors hydratePhaseDeliverables: only the columns the table
// carries are written; the template-local task id is kept in the audit meta.
// Idempotent — skips the phase when phase-stamped task rows already exist.
func (s *Server) hydratePhaseTasks(
	ctx context.Context, team, project, templateID, phase string,
) {
	body := s.projectSpecYAML(ctx, project, templateID)
	if body == "" {
		return
	}
	var head phaseSpecsHead
	if err := yaml.Unmarshal([]byte(body), &head); err != nil {
		s.log.Warn("phase_specs unmarshal", "err", err, "template", templateID)
		return
	}
	specs, ok := head.PhaseSpecs[phase]
	if !ok || len(specs.Tasks) == 0 {
		return
	}
	if n, err := s.countRowsForPhase(ctx, "tasks", project, phase); err == nil && n > 0 {
		return
	}
	params := s.projectParams(ctx, project)
	for _, t := range specs.Tasks {
		title := substituteTemplateParams(t.Title, params)
		if strings.TrimSpace(title) == "" {
			continue
		}
		ord := 0
		if t.Ord != nil {
			ord = *t.Ord
		}
		id := NewID()
		now := NowUTC()
		// priority defaults via the column DEFAULT; we order tasks within a
		// phase by reusing the milestone-free `body_md` only for the title.
		_, err := s.writeDB.ExecContext(ctx, `
			INSERT INTO tasks (id, project_id, title, status, phase,
				created_at, updated_at)
			VALUES (?, ?, ?, 'todo', ?, ?, ?)`,
			id, project, title, phase, now, now)
		if err != nil {
			s.log.Warn("hydrate task: insert", "err", err,
				"template", templateID, "phase", phase, "task", t.ID)
			continue
		}
		s.recordAudit(ctx, team, "task.created", "task", id,
			fmt.Sprintf("hydrated task %q in phase %s", title, phase),
			map[string]any{
				"project_id":       project,
				"phase":            phase,
				"hydrated_from":    templateID,
				"template_task_id": t.ID,
				"ord":              ord,
			})
	}
}

// hydratePhaseDeliverables walks the template's
// phase_specs[<phase>].deliverables and creates a draft `deliverables`
// row per entry (issue #20). Mirrors hydratePhaseCriteria. Only the
// columns the table carries are written — kind/required/ord; the
// template's display_name/ratification_authority/components are not
// persisted (see phaseDeliverableSpec). Idempotent: skips the phase when
// deliverable rows already exist for it.
func (s *Server) hydratePhaseDeliverables(
	ctx context.Context, team, project, templateID, phase string,
) {
	body := s.projectSpecYAML(ctx, project, templateID)
	if body == "" {
		return
	}
	var head phaseSpecsHead
	if err := yaml.Unmarshal([]byte(body), &head); err != nil {
		s.log.Warn("phase_specs unmarshal", "err", err, "template", templateID)
		return
	}
	specs, ok := head.PhaseSpecs[phase]
	if !ok {
		return
	}
	if n, err := s.countRowsForPhase(ctx, "deliverables", project, phase); err == nil && n > 0 {
		return
	}
	for _, d := range specs.Deliverables {
		if d.Kind == "" {
			continue
		}
		required := 1
		if d.Required != nil && !*d.Required {
			required = 0
		}
		ord := 0
		if d.Ord != nil {
			ord = *d.Ord
		}
		id := NewID()
		now := NowUTC()
		_, err := s.writeDB.ExecContext(ctx, `
			INSERT INTO deliverables (id, project_id, phase, kind,
				ratification_state, required, ord, created_at, updated_at)
			VALUES (?, ?, ?, ?, 'draft', ?, ?, ?, ?)`,
			id, project, phase, d.Kind, required, ord, now, now)
		if err != nil {
			s.log.Warn("hydrate deliverable: insert", "err", err,
				"template", templateID, "phase", phase, "deliverable", d.ID)
			continue
		}
		s.recordAudit(ctx, team, "deliverable.created", "deliverable", id,
			fmt.Sprintf("hydrated %s deliverable in phase %s", d.Kind, phase),
			map[string]any{
				"project_id":        project,
				"phase":             phase,
				"kind":              d.Kind,
				"hydrated_from":     templateID,
				"template_deliv_id": d.ID,
			})
	}
}

// countRowsForPhase returns how many rows the given table holds for a
// (project, phase) pair. The table name is a fixed internal literal
// (never user input) so the fmt-built query is safe. Used to make
// hydration idempotent.
func (s *Server) countRowsForPhase(ctx context.Context, table, project, phase string) (int, error) {
	var n int
	q := fmt.Sprintf(
		`SELECT COUNT(*) FROM %s WHERE project_id = ? AND phase = ?`, table)
	err := s.db.QueryRowContext(ctx, q, project, phase).Scan(&n)
	return n, err
}
