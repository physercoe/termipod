package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

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
// per-phase criteria for hydration.
type phaseSpecsHead struct {
	PhaseSpecs map[string]struct {
		Criteria []phaseCriterionSpec `yaml:"criteria"`
	} `yaml:"phase_specs"`
}

type phaseCriterionSpec struct {
	ID             string         `yaml:"id"`
	Kind           string         `yaml:"kind"`
	Body           map[string]any `yaml:"body"`
	DeliverableRef string         `yaml:"deliverable_ref"`
	Required       *bool          `yaml:"required"`
	Ord            *int           `yaml:"ord"`
}

// readProjectTemplateYAML loads the raw YAML for a project template. The
// loader resolution order matches loadProjectTemplates: disk overlay
// first, then the embedded FS. Returns "" when the template doesn't
// exist on either side; callers treat that as "skip hydration".
func (s *Server) readProjectTemplateYAML(templateID string) string {
	if templateID == "" {
		return ""
	}
	// Disk overlay wins (cf. loadProjectTemplates).
	disk := filepath.Join(s.cfg.DataRoot, "team", "templates", "projects",
		templateID+".yaml")
	if data, err := os.ReadFile(disk); err == nil {
		return string(data)
	}
	// Embedded FS — the file might be `<id>.yaml` or `<id>.<minor>.yaml`
	// (e.g. research.v1.yaml). Walk the directory.
	matches, _ := fs.Glob(hub.TemplatesFS, "templates/projects/*.yaml")
	for _, p := range matches {
		data, err := fs.ReadFile(hub.TemplatesFS, p)
		if err != nil {
			continue
		}
		var head struct {
			Name string `yaml:"name"`
		}
		if err := yaml.Unmarshal(data, &head); err != nil {
			continue
		}
		if head.Name == templateID {
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
				"project_id":      project,
				"phase":           phase,
				"kind":            c.Kind,
				"hydrated_from":   templateID,
				"template_crit_id": c.ID,
			})
	}
	_ = errors.New // keep errors imported in case callers want it later
}
