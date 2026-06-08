package server

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// WS2 — typed project parameters (#32). These tests pin the parse +
// validation contract: the two accepted YAML shapes (bare default / typed
// spec), type/range/enum enforcement, required handling, default-fill, and
// the schema-consistency checks surfaced through validateProjectConfigYAML.

func TestParseProjectParamSpecs_BareAndTyped(t *testing.T) {
	const cfg = `
phases: [build]
parameters:
  topic: ""
  budget_gpu_hours: 24
  threshold:
    type: number
    required: true
    min: 0
    max: 1
    description: "Pass threshold"
  mode:
    type: string
    default: fast
    enum: [fast, thorough]
`
	specs, err := parseProjectParamSpecs(cfg)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(specs) != 4 {
		t.Fatalf("want 4 specs, got %d (%v)", len(specs), specs)
	}

	// Bare string default ⇒ untyped string with HasDefault.
	if s := specs["topic"]; s.Type != "string" || !s.HasDefault || s.Required {
		t.Errorf("topic: %+v", s)
	}
	// Bare int default ⇒ inferred int, default present.
	if s := specs["budget_gpu_hours"]; s.Type != "int" || !s.HasDefault {
		t.Errorf("budget_gpu_hours: %+v", s)
	}
	// Typed number, required, with range, no default.
	if s := specs["threshold"]; s.Type != "number" || !s.Required || s.HasDefault {
		t.Errorf("threshold: %+v", s)
	} else if s.Min == nil || *s.Min != 0 || s.Max == nil || *s.Max != 1 {
		t.Errorf("threshold range: %+v", s)
	}
	// Typed string with enum + default.
	if s := specs["mode"]; s.Type != "string" || !s.HasDefault || len(s.Enum) != 2 {
		t.Errorf("mode: %+v", s)
	}
}

func TestValidateProjectParams_RequiredTypeRangeEnum(t *testing.T) {
	const cfg = `
phases: [build]
parameters:
  threshold:
    type: number
    required: true
    min: 0
    max: 1
  retries:
    type: int
    default: 3
  mode:
    type: string
    enum: [fast, thorough]
`
	specs, err := parseProjectParamSpecs(cfg)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	cases := []struct {
		name    string
		values  map[string]any
		wantErr bool
	}{
		{"required present, valid", map[string]any{"threshold": 0.5}, false},
		{"required missing", map[string]any{}, true},
		{"out of range high", map[string]any{"threshold": 1.5}, true},
		{"out of range low", map[string]any{"threshold": -0.1}, true},
		{"wrong type", map[string]any{"threshold": "high"}, true},
		{"int non-integer", map[string]any{"threshold": 0.5, "retries": 2.5}, true},
		{"int ok + retries default omitted", map[string]any{"threshold": 0.5}, false},
		{"enum member", map[string]any{"threshold": 0.5, "mode": "fast"}, false},
		{"enum non-member", map[string]any{"threshold": 0.5, "mode": "sideways"}, true},
		{"unknown key allowed", map[string]any{"threshold": 0.5, "extra": "x"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := validateProjectParams(specs, tc.values)
			if tc.wantErr && got == "" {
				t.Fatalf("want error, got none")
			}
			if !tc.wantErr && got != "" {
				t.Fatalf("want ok, got %q", got)
			}
		})
	}
}

func TestApplyParamDefaults(t *testing.T) {
	const cfg = `
phases: [build]
parameters:
  retries:
    type: int
    default: 3
  topic: "seed"
  required_no_default:
    type: string
    required: true
`
	specs, err := parseProjectParamSpecs(cfg)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out := applyParamDefaults(specs, map[string]any{"topic": "given"})
	if out["topic"] != "given" {
		t.Errorf("present value must win, got %v", out["topic"])
	}
	if f, _ := toFloat(out["retries"]); f != 3 {
		t.Errorf("default not filled: %v", out["retries"])
	}
	if _, ok := out["required_no_default"]; ok {
		t.Errorf("required-without-default must not be synthesised")
	}
}

func TestValidateProjectConfigYAML_ParamSchemaConsistency(t *testing.T) {
	cases := []struct {
		name    string
		cfg     string
		wantErr bool
	}{
		{
			name: "valid typed schema",
			cfg: `
phases: [build]
parameters:
  n:
    type: int
    default: 2
    min: 1
    max: 4
`,
			wantErr: false,
		},
		{
			name: "min greater than max",
			cfg: `
phases: [build]
parameters:
  n:
    type: int
    min: 5
    max: 1
`,
			wantErr: true,
		},
		{
			name: "default violates range",
			cfg: `
phases: [build]
parameters:
  n:
    type: int
    default: 9
    min: 1
    max: 4
`,
			wantErr: true,
		},
		{
			name: "default violates enum",
			cfg: `
phases: [build]
parameters:
  mode:
    type: string
    default: sideways
    enum: [fast, thorough]
`,
			wantErr: true,
		},
		{
			name:    "bare params always ok",
			cfg:     "phases: [build]\nparameters:\n  topic: \"\"\n  budget: 24\n",
			wantErr: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := validateProjectConfigYAML(tc.cfg, false)
			if tc.wantErr && got == "" {
				t.Fatalf("want error, got none")
			}
			if !tc.wantErr && got != "" {
				t.Fatalf("want ok, got %q", got)
			}
		})
	}
}

func TestProjectParamsSchemaOut_OrderedDerivation(t *testing.T) {
	const cfg = `
phases: [build]
parameters:
  zeta:
    type: string
  alpha:
    type: int
    default: 1
`
	out := projectParamsSchemaOut(cfg)
	if len(out) != 2 {
		t.Fatalf("want 2, got %d", len(out))
	}
	// Sorted by name for stable wire output.
	if out[0].Name != "alpha" || out[1].Name != "zeta" {
		t.Fatalf("want sorted [alpha, zeta], got [%s, %s]", out[0].Name, out[1].Name)
	}
	if projectParamsSchemaOut("") != nil {
		t.Errorf("empty config must yield nil schema")
	}
	if projectParamsSchemaOut("phases: [build]\n") != nil {
		t.Errorf("no parameters block must yield nil schema")
	}
}

func TestParsePhaseSpecsTasksAndPlan(t *testing.T) {
	// Parse-only contract for the WS1 materializer: per-phase tasks + plan
	// decode off the inline spec.
	const cfg = `
phases: [build]
phase_specs:
  build:
    tasks:
      - id: scaffold
        title: "Scaffold the package"
        ord: 0
      - id: wire
        title: "Wire the handler"
        ord: 1
    plan:
      title: "Build plan"
      steps:
        - title: "Design"
          ord: 0
        - title: "Implement"
          ord: 1
`
	var head phaseSpecsHead
	if err := yaml.Unmarshal([]byte(cfg), &head); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	spec, ok := head.PhaseSpecs["build"]
	if !ok {
		t.Fatal("build phase missing")
	}
	if len(spec.Tasks) != 2 || spec.Tasks[0].Title != "Scaffold the package" {
		t.Errorf("tasks: %+v", spec.Tasks)
	}
	if spec.Plan == nil || spec.Plan.Title != "Build plan" || len(spec.Plan.Steps) != 2 {
		t.Errorf("plan: %+v", spec.Plan)
	}
	if spec.Plan.Steps[1].Title != "Implement" {
		t.Errorf("plan step: %+v", spec.Plan.Steps)
	}
}
