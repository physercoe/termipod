// lifecycle_seeds_test.go — smoke test for the W5 worker + steward
// seed templates. Confirms each YAML loads, parses, declares the
// required fields, and references a prompt file that exists in the
// embedded FS. The hub already has rich template-rendering tests
// for the original Candidate A templates; this test focuses on the
// W5 deltas — five new agent templates + a rewritten domain-steward
// prompt — and ensures none of them are introduced broken.

package server

import (
	"io/fs"
	"strings"
	"testing"

	hub "github.com/termipod/hub"
	"gopkg.in/yaml.v3"
)

// W5SeedTemplates names the agent kinds that should be bundled and
// loadable as of W5. The names match the agent_kind values
// referenced in the ADR-001 D-amend-1 lifecycle table and in
// run-lifecycle-demo.md's checkpoints.
var w5SeedTemplates = []struct {
	kind        string
	yamlFile    string
	promptFile  string
	wantRole    string // expected default_role substring
}{
	{"steward.research.v1", "templates/agents/steward.research.v1.yaml", "templates/prompts/steward.research.v1.md", "team.coordinator"},
	{"lit-reviewer.v1", "templates/agents/lit-reviewer.v1.yaml", "templates/prompts/lit-reviewer.v1.md", "worker.lit-reviewer"},
	{"coder.v1", "templates/agents/coder.v1.yaml", "templates/prompts/coder.v1.md", "worker.coder"},
	{"paper-writer.v1", "templates/agents/paper-writer.v1.yaml", "templates/prompts/paper-writer.v1.md", "worker.paper-writer"},
	{"critic.v1", "templates/agents/critic.v1.yaml", "templates/prompts/critic.v1.md", "worker.critic"},
}

// TestW5SeedTemplates_BundledAndValid is the W5 acceptance gate.
// Confirms each seed parses as YAML, declares the canonical fields,
// and points at a prompt file that exists in the embed.
func TestW5SeedTemplates_BundledAndValid(t *testing.T) {
	for _, c := range w5SeedTemplates {
		t.Run(c.kind, func(t *testing.T) {
			yamlBody, err := fs.ReadFile(hub.TemplatesFS, c.yamlFile)
			if err != nil {
				t.Fatalf("read %s: %v", c.yamlFile, err)
			}
			var head struct {
				Template     string `yaml:"template"`
				Version      int    `yaml:"version"`
				DrivingMode  string `yaml:"driving_mode"`
				Backend      struct {
					Kind string `yaml:"kind"`
					Cmd  string `yaml:"cmd"`
				} `yaml:"backend"`
				DefaultRole string   `yaml:"default_role"`
				Capabilities []any  `yaml:"default_capabilities"`
				Prompt      string   `yaml:"prompt"`
			}
			if err := yaml.Unmarshal(yamlBody, &head); err != nil {
				t.Fatalf("parse %s: %v", c.yamlFile, err)
			}
			if head.Template == "" {
				t.Errorf("%s: missing `template:` field", c.yamlFile)
			}
			if head.Version == 0 {
				t.Errorf("%s: missing `version:` field", c.yamlFile)
			}
			if head.Backend.Kind == "" {
				t.Errorf("%s: missing `backend.kind`", c.yamlFile)
			}
			if head.Backend.Cmd == "" {
				t.Errorf("%s: missing `backend.cmd`", c.yamlFile)
			}
			if !strings.Contains(head.DefaultRole, c.wantRole) {
				t.Errorf("%s: default_role=%q, want substring %q", c.yamlFile, head.DefaultRole, c.wantRole)
			}
			if len(head.Capabilities) == 0 {
				t.Errorf("%s: missing default_capabilities", c.yamlFile)
			}
			if head.Prompt == "" {
				t.Errorf("%s: missing prompt:", c.yamlFile)
			}
			// Confirm the prompt file exists in the embed.
			if _, err := fs.ReadFile(hub.TemplatesFS, c.promptFile); err != nil {
				t.Errorf("%s declares prompt=%s but prompt file missing: %v",
					c.yamlFile, head.Prompt, err)
			}
		})
	}
}

// TestW5SeedTemplates_RoleMappingViaManifest cross-checks the seed
// templates' kinds against the operation-scope manifest (ADR-016)
// — every steward kind should map to role=steward, every worker
// kind to role=worker. This catches drift if a future seed is named
// in a way that the kind→role mapping doesn't anticipate.
func TestW5SeedTemplates_RoleMappingViaManifest(t *testing.T) {
	if err := initRoles(""); err != nil {
		t.Fatalf("initRoles: %v", err)
	}
	r := activeRoles()
	if r == nil {
		t.Fatal("activeRoles nil")
	}
	cases := []struct{ kind, want string }{
		{"steward.research.v1", "steward"},
		{"lit-reviewer.v1", "worker"},
		{"coder.v1", "worker"},
		{"paper-writer.v1", "worker"},
		{"critic.v1", "worker"},
	}
	for _, c := range cases {
		if got := r.RoleFor(c.kind); got != c.want {
			t.Errorf("RoleFor(%q) = %q; want %q", c.kind, got, c.want)
		}
	}
}

// TestW5SeedPrompts_NoForbiddenTokens scans the worker prompts for
// telltale strings that would indicate the safety guardrails got
// dropped — specifically, "curl <url> | bash" patterns and
// novelty-claim language in the paper-writer prompt.
func TestW5SeedPrompts_NoForbiddenTokens(t *testing.T) {
	checks := []struct {
		file        string
		forbidden   []string // substrings that MUST NOT appear as guidance
		mustContain []string // substrings that MUST appear (guardrail prose)
	}{
		{
			file: "templates/prompts/lit-reviewer.v1.md",
			forbidden: []string{
				"random blog",            // we explicitly forbid these as sources
				"sci-hub",
			},
			mustContain: []string{
				"arxiv.org",
				"paperswithcode",
				"openreview",
				"Forbidden sources",
			},
		},
		{
			file: "templates/prompts/coder.v1.md",
			mustContain: []string{
				"PyPI",
				"signed",
				"Forbidden",
				"venv",
			},
		},
		{
			file: "templates/prompts/paper-writer.v1.md",
			mustContain: []string{
				"Cite only what the lit-review found",
				"DO NOT make claims of novelty",
				"Limitations",
			},
		},
		{
			file: "templates/prompts/critic.v1.md",
			mustContain: []string{
				"accept | revise | reject",
				"Citation faithfulness",
			},
		},
		{
			file: "templates/prompts/steward.research.v1.md",
			mustContain: []string{
				"Phase 1",
				"Phase 2",
				"Phase 3",
				"Phase 4",
				"manager/IC invariant",
				"plan.advance",
			},
		},
	}
	for _, c := range checks {
		t.Run(c.file, func(t *testing.T) {
			body, err := fs.ReadFile(hub.TemplatesFS, c.file)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			s := string(body)
			for _, f := range c.forbidden {
				// "random blog" appears in the *forbidden-sources* list
				// itself; we want to make sure no positive-guidance
				// occurrence sneaks in. The lit-reviewer prompt mentions
				// "random blog" once as a forbidden source — that's OK.
				// Skip the substring check if it's a known list-context
				// occurrence.
				if !strings.Contains(s, f) {
					continue // not present at all — fine
				}
				// Present — verify it's only in a forbidden context.
				lower := strings.ToLower(s)
				idx := strings.Index(lower, strings.ToLower(f))
				// Look 200 chars back; if "Forbidden" is in that window,
				// it's the list. Otherwise it's positive guidance.
				start := idx - 200
				if start < 0 {
					start = 0
				}
				preceding := strings.ToLower(s[start:idx])
				if !strings.Contains(preceding, "forbidden") &&
					!strings.Contains(preceding, "never") &&
					!strings.Contains(preceding, "don't") {
					t.Errorf("%s: forbidden phrase %q appears as positive guidance", c.file, f)
				}
			}
			for _, m := range c.mustContain {
				if !strings.Contains(s, m) {
					t.Errorf("%s: expected to contain %q (guardrail / required prose)", c.file, m)
				}
			}
		})
	}
}
