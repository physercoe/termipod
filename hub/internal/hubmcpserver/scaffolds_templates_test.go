package hubmcpserver

import (
	"strings"
	"testing"
)

// Scaffolds must be syntactically loadable as the schema expects. We
// don't try to spawn from the skeleton (placeholders fail validation
// downstream) but we do check structural invariants: required keys
// present, no stray template-engine tokens left from copy-paste,
// engine-specific cmd lines diverge correctly.

func TestScaffoldAgent_WorkerHasRequiredFields(t *testing.T) {
	out, err := scaffoldContent("agent", map[string]any{"kind": "worker"})
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	for _, want := range []string{
		"template:",
		"version:",
		"driving_mode:",
		"backend:",
		"  kind: claude-code",
		"prompt:",
		"default_capabilities:",
		"skills:",
		"spawn.descendants: 0",  // worker invariant: no multiplication
	} {
		if !strings.Contains(out, want) {
			t.Errorf("worker scaffold missing %q", want)
		}
	}
	// Must NOT have template_id of the canonical bundled coder, otherwise
	// the agent might think it's authoring "agents.coder" instead of a new id.
	if strings.Contains(out, "agents.coder") {
		t.Errorf("worker scaffold leaked persona id agents.coder")
	}
}

func TestScaffoldAgent_StewardHasElevatedCapabilities(t *testing.T) {
	out, err := scaffoldContent("agent", map[string]any{"kind": "steward"})
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	for _, want := range []string{
		"templates.read",
		"templates.propose",
		"projects.create",
		"spawn.descendants: 20", // stewards spawn workers
		"team.coordinator",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("steward scaffold missing %q", want)
		}
	}
	// The auto-derive workdir comment matters — copies the v1.0.595 wedge
	// rationale so a steward authoring a new domain template doesn't
	// hardcode ~/hub-work and re-introduce the collision class.
	if !strings.Contains(out, "auto-derives") {
		t.Errorf("steward scaffold missing default_workdir auto-derive comment")
	}
}

func TestScaffoldAgent_EngineSwapsCmdLine(t *testing.T) {
	cases := []struct {
		engine  string
		wantCmd string
	}{
		{"claude-code", "claude --model"},
		{"codex", "codex app-server"},
		{"gemini-cli", "gemini --acp"},
		{"kimi-code", "kimi --yolo acp"},
	}
	for _, tc := range cases {
		out, err := scaffoldContent("agent",
			map[string]any{"kind": "worker", "engine": tc.engine})
		if err != nil {
			t.Errorf("engine %q: scaffold: %v", tc.engine, err)
			continue
		}
		if !strings.Contains(out, tc.wantCmd) {
			t.Errorf("engine %q: scaffold cmd line missing %q", tc.engine, tc.wantCmd)
		}
		if !strings.Contains(out, "kind: "+tc.engine) {
			t.Errorf("engine %q: scaffold backend.kind missing", tc.engine)
		}
	}
}

func TestScaffoldPrompt_HasCanonicalSections(t *testing.T) {
	worker, _ := scaffoldContent("prompt", map[string]any{"kind": "worker"})
	for _, want := range []string{
		"## What you do",
		"## Tools you'll reach for",
		"## Behaviour",
		"reports.post",
	} {
		if !strings.Contains(worker, want) {
			t.Errorf("worker prompt scaffold missing %q", want)
		}
	}
	steward, _ := scaffoldContent("prompt", map[string]any{"kind": "steward"})
	for _, want := range []string{
		"## What you do",
		"## Workers you spawn",
		"## Phase walk",
		"## When in doubt",
		"ADR-025",
	} {
		if !strings.Contains(steward, want) {
			t.Errorf("steward prompt scaffold missing %q", want)
		}
	}
}

func TestScaffoldPlan_PhaseCount(t *testing.T) {
	out, err := scaffoldContent("plan", map[string]any{"phases": float64(3)})
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if strings.Count(out, "id: phase-") != 3 {
		t.Errorf("requested 3 phases; got %d", strings.Count(out, "id: phase-"))
	}
	// Default (no arg) is 5.
	def, _ := scaffoldContent("plan", map[string]any{})
	if strings.Count(def, "id: phase-") != 5 {
		t.Errorf("default phases want 5; got %d", strings.Count(def, "id: phase-"))
	}
}

func TestScaffoldRegistered_ToolCatalogIncludesScaffolds(t *testing.T) {
	tools := buildTools()
	want := map[string]bool{
		"templates.agent.scaffold":  false,
		"templates.prompt.scaffold": false,
		"templates.plan.scaffold":   false,
	}
	for _, tool := range tools {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
		}
	}
	for name, present := range want {
		if !present {
			t.Errorf("tool catalog missing %s", name)
		}
	}
}
