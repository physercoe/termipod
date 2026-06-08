package server

import (
	"fmt"
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
	return ""
}
