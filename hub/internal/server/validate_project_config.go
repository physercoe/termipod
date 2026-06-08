package server

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// validateProjectConfigYAML checks the shape of the config_yaml field
// on projects.create / projects.update. Empty is always OK — simple
// ad-hoc projects don't need a config. When present:
//
//   - The YAML must parse (malformed text is rejected).
//   - When the project is being authored as a reusable template
//     (`is_template: true`), the config must declare a non-empty
//     `phases:` array — a template with no phases is an empty shell
//     that downstream code can't act on.
//
// The validator is lenient on extra keys (project-template schema
// evolves; unknown fields are forwarded as-is) and strict on the
// load-bearing fields.
func validateProjectConfigYAML(configYAML string, isTemplate bool) string {
	if strings.TrimSpace(configYAML) == "" {
		return ""
	}
	// phaseNameList accepts both the scalar (`- env-setup`) and mapping
	// (`- name: env-setup`) forms — the same tolerant parse the template
	// loader uses (#38). The previous `[]map[string]any` shape here only
	// accepted the mapping form and silently REJECTED the canonical scalar
	// form every shipped template uses — the inverse of the loader bug.
	var doc struct {
		Phases phaseNameList `yaml:"phases"`
	}
	if err := yaml.Unmarshal([]byte(configYAML), &doc); err != nil {
		return fmt.Sprintf("config_yaml: invalid YAML: %v", err)
	}
	if isTemplate && len(doc.Phases) == 0 {
		return "config_yaml: project templates must declare `phases:` " +
			"with at least one phase entry"
	}
	// Validate the typed-parameter schema when present (#32). The block is
	// optional and the bare `key: value` form is always accepted; only a
	// typed spec is checked — for a sane range (min <= max) and for a
	// declared default that satisfies its own spec. Values supplied per
	// project are validated separately on create (validateProjectParams).
	specs, err := parseProjectParamSpecs(configYAML)
	if err != nil {
		return fmt.Sprintf("config_yaml: %v", err)
	}
	for _, name := range sortedParamNames(specs) {
		if reason := specs[name].validateSchema(); reason != "" {
			return "config_yaml: " + reason
		}
	}
	// Every materialized phase needs a deliverable. The deliverable is the
	// phase's ratification surface — its acceptance criteria bind to it
	// (#56) and it is the unit the director ratifies to clear the phase. A
	// phase_specs entry with zero deliverables is a meaningless phase: its
	// criteria can't bind and there is nothing to ratify. Enforced for any
	// spec that declares phase_specs (templates and concrete inline-spec
	// projects alike).
	for _, ph := range phasesMissingDeliverable(configYAML) {
		return fmt.Sprintf("config_yaml: phase %q declares no deliverable — "+
			"every phase needs at least one (its acceptance criteria bind to "+
			"it, and it is what the director ratifies)", ph)
	}
	return ""
}

// phasesMissingDeliverable returns the sorted names of phase_specs
// entries that declare no deliverable. Empty when the spec has no
// phase_specs (lifecycle-less project) or every phase has one.
func phasesMissingDeliverable(configYAML string) []string {
	var doc struct {
		PhaseSpecs map[string]struct {
			Deliverables []struct {
				ID string `yaml:"id"`
			} `yaml:"deliverables"`
		} `yaml:"phase_specs"`
	}
	if err := yaml.Unmarshal([]byte(configYAML), &doc); err != nil {
		// Malformed YAML is already rejected upstream; treat as no-op here.
		return nil
	}
	var missing []string
	for ph, spec := range doc.PhaseSpecs {
		if len(spec.Deliverables) == 0 {
			missing = append(missing, ph)
		}
	}
	sort.Strings(missing)
	return missing
}
