package server

import (
	"context"
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
	} `yaml:"phase_specs"`
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

// readProjectTemplateYAML loads the raw YAML for a project template. The
// loader resolution order matches loadProjectTemplates: disk overlay
// first, then the embedded FS. Returns "" when the template doesn't
// exist on either side; callers treat that as "skip hydration".
func (s *Server) readProjectTemplateYAML(templateID string) string {
	if templateID == "" {
		return ""
	}
	// Disk overlays win (cf. loadProjectTemplates), highest-priority team
	// overlay first. The file may be `<id>.yaml` or a versioned basename like
	// `code-migration.v1.yaml` carrying `name: code-migration`, so match on
	// either (matchProjectTemplateName) rather than guessing the filename.
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
			if matchProjectTemplateName(data, p, templateID) {
				return string(data)
			}
		}
	}
	// Embedded FS — same name-or-basename match over the bundled templates.
	matches, _ := fs.Glob(hub.TemplatesFS, "templates/projects/*.yaml")
	for _, p := range matches {
		data, err := fs.ReadFile(hub.TemplatesFS, p)
		if err != nil {
			continue
		}
		if matchProjectTemplateName(data, p, templateID) {
			return string(data)
		}
	}
	return ""
}

// hydratePhaseCriteria reads the template YAML's `phase_specs[<phase>]
// .criteria` block and creates `acceptance_criteria` rows. Errors are
// logged and swallowed — hydration must never fail project creation.
func (s *Server) hydratePhaseCriteria(
	ctx context.Context, team, project, templateID, phase string,
) {
	body := s.readProjectTemplateYAML(templateID)
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
			b, err := json.Marshal(c.Body)
			if err == nil {
				bodyJSON = string(b)
			}
		}
		id := NewID()
		now := NowUTC()
		_, err := s.db.ExecContext(ctx, `
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

// hydratePhase instantiates a phase's template-declared deliverables and
// criteria as DB rows. Called on project create (phase[0]) and on phase
// advance (the newly-entered phase) so a project's deliverables/criteria
// panels reflect its template's phase_specs instead of starting empty
// (issue #20). Both underlying hydrators are idempotent — re-entry after
// a rollback, or a repair, won't duplicate. Best-effort: a hydration
// failure is logged inside each helper, never fatal to the transition.
func (s *Server) hydratePhase(ctx context.Context, team, project, templateID, phase string) {
	if templateID == "" || phase == "" {
		return
	}
	s.hydratePhaseDeliverables(ctx, team, project, templateID, phase)
	s.hydratePhaseCriteria(ctx, team, project, templateID, phase)
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
	body := s.readProjectTemplateYAML(templateID)
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
		_, err := s.db.ExecContext(ctx, `
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
