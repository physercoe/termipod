package server

import (
	"io/fs"
	"testing"

	hub "github.com/termipod/hub"
	"gopkg.in/yaml.v3"
)

// WS3 — shipped project presets are complete inline specs (ADR-046). The old
// write-memo / reproduce-paper stubs were phase-less empty shells; this test
// is the invariant that prevents another one shipping. Every embedded
// templates/projects/*.yaml must be a structurally complete spec: named,
// goal-bearing, bound to a domain steward, ≥1 phase, and every phase carrying
// criteria + tasks + a plan, with gate criteria that reference a deliverable
// declared in the same phase, and a typed-parameter schema that validates.

// presetDoc is the union of the top-level template fields and the phase_specs
// block this test inspects.
type presetDoc struct {
	Name               string        `yaml:"name"`
	Kind               string        `yaml:"kind"`
	Goal               string        `yaml:"goal"`
	OnCreateTemplateID string        `yaml:"on_create_template_id"`
	Phases             phaseNameList `yaml:"phases"`
	PhaseSpecs         map[string]struct {
		Criteria []struct {
			Kind string         `yaml:"kind"`
			Body map[string]any `yaml:"body"`
		} `yaml:"criteria"`
		Deliverables []struct {
			ID string `yaml:"id"`
		} `yaml:"deliverables"`
		Tasks []struct {
			Title string `yaml:"title"`
		} `yaml:"tasks"`
		Plan *struct {
			Steps []struct {
				Title string `yaml:"title"`
			} `yaml:"steps"`
		} `yaml:"plan"`
	} `yaml:"phase_specs"`
}

func TestShippedProjectPresets_AreStructurallyComplete(t *testing.T) {
	matches, err := fs.Glob(hub.TemplatesFS, "templates/projects/*.yaml")
	if err != nil {
		t.Fatalf("glob presets: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no shipped project presets found")
	}

	seen := map[string]bool{}
	for _, path := range matches {
		raw, err := fs.ReadFile(hub.TemplatesFS, path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var d presetDoc
		if err := yaml.Unmarshal(raw, &d); err != nil {
			t.Fatalf("%s: unmarshal: %v", path, err)
		}
		t.Run(d.Name, func(t *testing.T) {
			if d.Name == "" || d.Kind == "" || d.Goal == "" {
				t.Fatalf("%s: name/kind/goal must all be set", path)
			}
			seen[d.Name] = true

			// Bound domain steward (ADR-046 §4).
			if d.OnCreateTemplateID == "" {
				t.Errorf("%s: on_create_template_id (bound steward) must be set", d.Name)
			}
			// At least one phase, and a typed-parameter schema that validates.
			if len(d.Phases) == 0 {
				t.Fatalf("%s: must declare ≥1 phase", d.Name)
			}
			if msg := validateProjectConfigYAML(string(raw), true); msg != "" {
				t.Errorf("%s: config invalid as template: %s", d.Name, msg)
			}
			specs, err := parseProjectParamSpecs(string(raw))
			if err != nil {
				t.Errorf("%s: parameters parse: %v", d.Name, err)
			}
			if len(specs) == 0 {
				t.Errorf("%s: a complete preset declares typed parameters", d.Name)
			}

			// Every phase is a complete, non-empty unit of work.
			for _, ph := range d.Phases {
				spec, ok := d.PhaseSpecs[ph]
				if !ok {
					t.Errorf("%s: phase %q has no phase_spec (empty shell)", d.Name, ph)
					continue
				}
				if len(spec.Deliverables) == 0 {
					t.Errorf("%s/%s: no deliverable — every phase needs one "+
						"(its criteria bind to it and it is what the director ratifies)",
						d.Name, ph)
				}
				if len(spec.Criteria) == 0 {
					t.Errorf("%s/%s: no acceptance criteria", d.Name, ph)
				}
				if len(spec.Tasks) == 0 {
					t.Errorf("%s/%s: no tasks", d.Name, ph)
				}
				if spec.Plan == nil || len(spec.Plan.Steps) == 0 {
					t.Errorf("%s/%s: no plan steps", d.Name, ph)
				}
				// Gate criteria must reference a deliverable declared in this
				// same phase (so the #21 gate-ref rewrite can resolve it).
				delivIDs := map[string]bool{}
				for _, dl := range spec.Deliverables {
					if dl.ID != "" {
						delivIDs[dl.ID] = true
					}
				}
				for _, c := range spec.Criteria {
					if c.Kind != "gate" {
						continue
					}
					ref := gateDeliverableRef(c.Body)
					if ref == "" {
						continue
					}
					if !delivIDs[ref] {
						t.Errorf("%s/%s: gate references deliverable %q not declared in this phase",
							d.Name, ph, ref)
					}
				}
			}
		})
	}

	// The two reference presets the program documents must both ship.
	for _, want := range []string{"research", "code-migration"} {
		if !seen[want] {
			t.Errorf("expected shipped preset %q is missing", want)
		}
	}
}

// gateDeliverableRef pulls body.params.deliverable_id out of a gate
// criterion's parsed body, or "" when absent.
func gateDeliverableRef(body map[string]any) string {
	params, ok := body["params"].(map[string]any)
	if !ok {
		return ""
	}
	ref, _ := params["deliverable_id"].(string)
	return ref
}
