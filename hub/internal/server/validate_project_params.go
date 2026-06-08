package server

import (
	"fmt"
	"math"
	"sort"

	"gopkg.in/yaml.v3"
)

// Typed project parameters (#32). A project spec's `parameters:` block
// declares the inputs a project is instantiated with. Two YAML shapes are
// accepted for back-compat with every template shipped to date:
//
//	parameters:                      parameters:
//	  topic: ""                        topic:
//	  budget_gpu_hours: 24               type: string
//	                                     required: true
//	                                     description: "Research topic"
//	                                   budget_gpu_hours:
//	                                     type: int
//	                                     default: 24
//	                                     min: 1
//	                                     max: 256
//
// A scalar (or any value that is not a mapping carrying a `type:` key) is a
// bare **untyped default** — the historical form. A mapping with a `type:`
// key is a **typed spec** carrying validation metadata. The two may be mixed
// in one block. Validation of a project's `parameters_json` values against
// the declared specs happens on create/update; untyped entries impose no
// constraint beyond their inferred type being advisory.

// paramSpec is one declared project parameter.
type paramSpec struct {
	Name        string
	Type        string // "string" | "int" | "number" | "bool" | "" (untyped/unknown)
	Required    bool
	Default     any
	HasDefault  bool
	Description string
	Min         *float64
	Max         *float64
	Enum        []any
}

// projectParamsDoc is the slice of a project spec this code parses. We keep
// each `parameters:` value as a raw node so we can distinguish the typed
// mapping form from a bare default that merely happens to be a mapping.
type projectParamsDoc struct {
	Parameters map[string]yaml.Node `yaml:"parameters"`
}

// parseProjectParamSpecs reads the `parameters:` block out of a project spec
// (`config_yaml`) and returns the declared parameter specs keyed by name.
// Empty / absent block → empty map. Malformed YAML → error (callers on the
// create path already rejected unparseable config_yaml, so this is belt-and-
// braces). Best-effort per entry: an entry we cannot decode is skipped rather
// than failing the whole parse.
func parseProjectParamSpecs(configYAML string) (map[string]paramSpec, error) {
	out := map[string]paramSpec{}
	if configYAML == "" {
		return out, nil
	}
	var doc projectParamsDoc
	if err := yaml.Unmarshal([]byte(configYAML), &doc); err != nil {
		return out, fmt.Errorf("parameters: invalid YAML: %w", err)
	}
	for name, node := range doc.Parameters {
		n := node // node is a value copy; take its address safely
		spec := paramSpec{Name: name}
		if n.Kind == yaml.MappingNode && mappingHasKey(&n, "type") {
			var raw struct {
				Type        string   `yaml:"type"`
				Required    bool     `yaml:"required"`
				Default     any      `yaml:"default"`
				Description string   `yaml:"description"`
				Min         *float64 `yaml:"min"`
				Max         *float64 `yaml:"max"`
				Enum        []any    `yaml:"enum"`
			}
			if err := n.Decode(&raw); err != nil {
				continue
			}
			spec.Type = normalizeParamType(raw.Type)
			spec.Required = raw.Required
			spec.Description = raw.Description
			spec.Min = raw.Min
			spec.Max = raw.Max
			spec.Enum = raw.Enum
			if mappingHasKey(&n, "default") {
				spec.Default = raw.Default
				spec.HasDefault = true
			}
		} else {
			// Untyped: the value itself is the default. Infer an advisory
			// type from the decoded Go value so reads can still hint a form
			// control, but impose no constraint.
			var v any
			if err := n.Decode(&v); err != nil {
				continue
			}
			spec.Default = v
			spec.HasDefault = true
			spec.Type = inferParamType(v)
		}
		out[name] = spec
	}
	return out, nil
}

// validateProjectParams checks a project's parameter values against the
// declared specs. Returns "" when valid, else a human-readable reason. It is
// lenient on unknown keys (a value with no declared spec is allowed — the
// spec set evolves) and strict on declared ones: required-without-default
// must be present, and a present value must satisfy its type / range / enum.
func validateProjectParams(specs map[string]paramSpec, values map[string]any) string {
	// Deterministic order so the first error a caller sees is stable.
	for _, name := range sortedParamNames(specs) {
		spec := specs[name]
		v, present := values[name]
		if !present {
			if spec.Required && !spec.HasDefault {
				return fmt.Sprintf("parameters: %q is required", name)
			}
			continue
		}
		if msg := spec.validateValue(v); msg != "" {
			return msg
		}
	}
	return ""
}

// validateValue checks a single provided value against this spec. Untyped /
// unknown-type specs accept anything (their type is advisory). JSON-decoded
// numbers arrive as float64; YAML-decoded ones as int or float64 — toFloat
// bridges both.
func (p paramSpec) validateValue(v any) string {
	switch p.Type {
	case "string":
		s, ok := v.(string)
		if !ok {
			return p.typeErr("string")
		}
		return p.checkEnum(s)
	case "bool":
		if _, ok := v.(bool); !ok {
			return p.typeErr("bool")
		}
		return p.checkEnum(v)
	case "int":
		f, ok := toFloat(v)
		if !ok || f != math.Trunc(f) {
			return p.typeErr("int")
		}
		if msg := p.checkRange(f); msg != "" {
			return msg
		}
		return p.checkEnum(f)
	case "number":
		f, ok := toFloat(v)
		if !ok {
			return p.typeErr("number")
		}
		if msg := p.checkRange(f); msg != "" {
			return msg
		}
		return p.checkEnum(f)
	default:
		// Untyped / unknown declared type: accept as-is.
		return ""
	}
}

func (p paramSpec) typeErr(want string) string {
	return fmt.Sprintf("parameters: %q must be a %s", p.Name, want)
}

func (p paramSpec) checkRange(f float64) string {
	if p.Min != nil && f < *p.Min {
		return fmt.Sprintf("parameters: %q must be >= %v", p.Name, *p.Min)
	}
	if p.Max != nil && f > *p.Max {
		return fmt.Sprintf("parameters: %q must be <= %v", p.Name, *p.Max)
	}
	return ""
}

// checkEnum verifies the value is one of the declared enum members (when an
// enum is declared). Numbers compare by float value (so a YAML `int` member
// matches a JSON `float64` value); everything else by string form.
func (p paramSpec) checkEnum(v any) string {
	if len(p.Enum) == 0 {
		return ""
	}
	for _, e := range p.Enum {
		if paramValuesEqual(e, v) {
			return ""
		}
	}
	return fmt.Sprintf("parameters: %q must be one of the declared enum values", p.Name)
}

// applyParamDefaults returns a copy of values with any missing declared
// parameter that carries a default filled in. Used so downstream
// {placeholder} substitution and the steward see a complete parameter set.
// Present values win; unknown keys pass through untouched.
func applyParamDefaults(specs map[string]paramSpec, values map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range values {
		out[k] = v
	}
	for name, spec := range specs {
		if _, present := out[name]; present {
			continue
		}
		if spec.HasDefault {
			out[name] = spec.Default
		}
	}
	return out
}

// paramSchemaOut is the wire form of a declared parameter, ordered, so the
// mobile create/edit form can render typed inputs without parsing YAML.
type paramSchemaOut struct {
	Name        string   `json:"name"`
	Type        string   `json:"type,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Default     any      `json:"default,omitempty"`
	Description string   `json:"description,omitempty"`
	Min         *float64 `json:"min,omitempty"`
	Max         *float64 `json:"max,omitempty"`
	Enum        []any    `json:"enum,omitempty"`
}

// projectParamsSchemaOut derives the ordered wire schema from a project's
// own config_yaml. Empty config / no `parameters:` block / unparseable YAML
// → nil (the caller omits the field). Read-only: best-effort, never fatal.
func projectParamsSchemaOut(configYAML string) []paramSchemaOut {
	specs, err := parseProjectParamSpecs(configYAML)
	if err != nil || len(specs) == 0 {
		return nil
	}
	out := make([]paramSchemaOut, 0, len(specs))
	for _, name := range sortedParamNames(specs) {
		p := specs[name]
		out = append(out, paramSchemaOut{
			Name:        p.Name,
			Type:        p.Type,
			Required:    p.Required,
			Default:     p.Default,
			Description: p.Description,
			Min:         p.Min,
			Max:         p.Max,
			Enum:        p.Enum,
		})
	}
	return out
}

// validateSchema checks that a declared spec is internally consistent: a
// sane range (min <= max) and a declared default that satisfies its own
// type / range / enum. Returns "" when consistent. Untyped specs (no `type:`)
// impose no constraints, so they always pass.
func (p paramSpec) validateSchema() string {
	if p.Type == "" {
		return ""
	}
	if p.Min != nil && p.Max != nil && *p.Min > *p.Max {
		return fmt.Sprintf("parameters: %q has min > max", p.Name)
	}
	if p.HasDefault {
		if msg := p.validateValue(p.Default); msg != "" {
			return fmt.Sprintf("parameters: %q default is invalid (%s)", p.Name, msg)
		}
	}
	return ""
}

// sortedParamNames returns the spec names in deterministic order so callers
// surface a stable first error.
func sortedParamNames(specs map[string]paramSpec) []string {
	names := make([]string, 0, len(specs))
	for name := range specs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// --- small helpers -----------------------------------------------------

// mappingHasKey reports whether a YAML mapping node declares the given key.
func mappingHasKey(n *yaml.Node, key string) bool {
	if n == nil || n.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return true
		}
	}
	return false
}

// normalizeParamType folds the accepted type spellings onto a canonical set.
// Unknown values pass through verbatim (treated as untyped by validateValue).
func normalizeParamType(t string) string {
	switch t {
	case "string", "str", "text":
		return "string"
	case "int", "integer":
		return "int"
	case "number", "float", "double":
		return "number"
	case "bool", "boolean":
		return "bool"
	default:
		return t
	}
}

// inferParamType derives an advisory type from a decoded default value.
func inferParamType(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case bool:
		return "bool"
	case int, int64:
		return "int"
	case float32, float64:
		return "number"
	default:
		return ""
	}
}

// toFloat coerces a YAML/JSON numeric value to float64.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}

// paramValuesEqual compares two parameter values for enum matching: numbers
// by float, everything else by fmt string form.
func paramValuesEqual(a, b any) bool {
	if af, aok := toFloat(a); aok {
		if bf, bok := toFloat(b); bok {
			return af == bf
		}
		return false
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}
