package server

import (
	"io/fs"
	"strings"
	"testing"

	hub "github.com/termipod/hub"
	"github.com/termipod/hub/internal/agentfamilies"
	"gopkg.in/yaml.v3"
)

// TestBundledAgentTemplates_M2LaunchContract pins the driving-mode launch
// contract: the argv that selects a driving mode is a property of the
// (engine, mode), and every bundled persona template that runs in that mode
// MUST resolve to a launch command carrying it. This is the invariant whose
// absence let ml-worker.v1 / briefing.v1 ship an M2 claude-code cmd with no
// stream-json flags — the StdioDriver never got a parseable frame and the
// spawn failed, found only when a tester ran the agent. Encoding it as a test
// fails CI on drift instead.
//
// ADR-043 P2 has landed: the mode flags now live on the engine family
// (launch.M2.mode_args) and the launcher composes them onto the persona cmd
// via Family.ComposeLaunchCmd. So this test asserts the COMPOSED command —
// the same composition the launcher runs — rather than the raw template cmd.
// It also asserts the raw template cmd does NOT carry the flags, locking the
// single-source property: the contract lives on the family, and a persona that
// re-types it (drifting back to the pre-P2 duplication) fails here.
//
// gemini-cli M2 is intentionally exempt from the composed-cmd check: it is
// exec-per-turn, so the ExecResumeDriver builds its own argv from the family's
// LaunchArgs(M2) (driver_exec_resume.go) rather than running the cmd verbatim.
// Only the engines whose StdioDriver/AppServerDriver run the composed cmd are
// checked here.
func TestBundledAgentTemplates_M2LaunchContract(t *testing.T) {
	type tmpl struct {
		DrivingMode string `yaml:"driving_mode"`
		Backend     struct {
			Kind string `yaml:"kind"`
			Cmd  string `yaml:"cmd"`
		} `yaml:"backend"`
	}
	matches, err := fs.Glob(hub.TemplatesFS, "templates/agents/*.yaml")
	if err != nil {
		t.Fatalf("glob bundled agent templates: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no bundled agent templates found — wrong embed path?")
	}
	// The flags that put each engine into structured-stdio mode. The launcher
	// appends these from the family; we assert the composed result carries
	// them and the raw template does not.
	m2Required := map[string][]string{
		"claude-code": {"--output-format stream-json", "--input-format stream-json"},
		"codex":       {"app-server"},
	}
	checked := 0
	for _, p := range matches {
		data, err := fs.ReadFile(hub.TemplatesFS, p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		var d tmpl
		if err := yaml.Unmarshal(data, &d); err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		if d.DrivingMode != "M2" {
			continue
		}
		needs, ok := m2Required[d.Backend.Kind]
		if !ok {
			continue // engine whose driver builds its own argv (e.g. gemini-cli)
		}
		fam, famOK := agentfamilies.ByName(d.Backend.Kind)
		if !famOK {
			t.Errorf("%s: engine family %q not in registry", p, d.Backend.Kind)
			continue
		}
		// Compose exactly as launchM2 does.
		composed := fam.ComposeLaunchCmd("M2", d.Backend.Cmd)
		checked++
		for _, need := range needs {
			if !strings.Contains(composed, need) {
				t.Errorf("%s: composed M2 %s launch cmd is missing %q — the driver "+
					"needs it to speak the mode protocol; is launch.M2.mode_args set on "+
					"the family? (ADR-043)\n  template cmd = %q\n  composed     = %q",
					p, d.Backend.Kind, need, d.Backend.Cmd, composed)
			}
			// Single-source: the raw persona template must NOT carry the
			// flag — it belongs to the family now.
			if strings.Contains(d.Backend.Cmd, need) {
				t.Errorf("%s: persona template re-types the family-owned mode flag %q "+
					"in backend.cmd — drop it; the launcher appends it from "+
					"launch.M2.mode_args (ADR-043)\n  cmd = %q",
					p, need, d.Backend.Cmd)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no M2 claude-code/codex templates checked — the invariant isn't exercising anything")
	}
}

// TestBundledAgentTemplates_PermissionContract is the P3 analog of the
// launch-contract guard: a claude-code persona template that references
// {{permission_flag}} but carries no permission_modes of its own MUST be
// covered by the claude-code family's permission_modes (ADR-043 P3), or
// the spawn resolver expands {{permission_flag}} to "" and the agent
// spawns unable to write files. It also asserts the inverse — a template
// that DOES declare permission_modes is exercising the override path
// deliberately (steward.claude-m4) — so the single source stays legible.
func TestBundledAgentTemplates_PermissionContract(t *testing.T) {
	type tmpl struct {
		Backend struct {
			Kind            string            `yaml:"kind"`
			Cmd             string            `yaml:"cmd"`
			PermissionModes map[string]string `yaml:"permission_modes"`
		} `yaml:"backend"`
	}
	matches, err := fs.Glob(hub.TemplatesFS, "templates/agents/*.yaml")
	if err != nil {
		t.Fatalf("glob bundled agent templates: %v", err)
	}
	fam, ok := agentfamilies.ByName("claude-code")
	if !ok {
		t.Fatal("claude-code family not in registry")
	}
	checked := 0
	for _, p := range matches {
		data, err := fs.ReadFile(hub.TemplatesFS, p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		var d tmpl
		if err := yaml.Unmarshal(data, &d); err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		if d.Backend.Kind != "claude-code" || !strings.Contains(d.Backend.Cmd, "{{permission_flag}}") {
			continue
		}
		checked++
		if len(d.Backend.PermissionModes) > 0 {
			continue // deliberate override (e.g. steward.claude-m4's M4 skip)
		}
		// No local map → must resolve from the family for both modes.
		for _, mode := range []string{"skip", "prompt"} {
			if fam.PermissionFlag(mode) == "" {
				t.Errorf("%s: drops permission_modes but family has no %q flag — "+
					"{{permission_flag}} would resolve empty (ADR-043 P3)", p, mode)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no claude-code templates with {{permission_flag}} checked")
	}
}
