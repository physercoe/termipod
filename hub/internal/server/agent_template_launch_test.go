package server

import (
	"io/fs"
	"strings"
	"testing"

	hub "github.com/termipod/hub"
	"gopkg.in/yaml.v3"
)

// TestBundledAgentTemplates_M2LaunchContract pins the driving-mode launch
// contract: the argv that selects a driving mode is a property of the
// (engine, mode), and every bundled persona template that runs in that mode
// MUST carry it in backend.cmd. This is the invariant whose absence let
// ml-worker.v1 / briefing.v1 ship an M2 claude-code cmd with no stream-json
// flags — the StdioDriver never got a parseable frame and the spawn failed,
// found only when a tester ran the agent. Encoding it as a test fails CI on
// drift instead.
//
// This is Option C (the guard) from docs/discussions/engine-launch-contract.md.
// When Option A lands — the mode flags move onto the engine family and the
// launcher composes them — this test moves to asserting the COMPOSED launch
// command (so it stays green after the persona templates drop the literal
// flags). Today the contract still lives in the cmd string, so we assert there.
//
// gemini-cli M2 is intentionally exempt: the launcher trims its cmd to the bin
// (launch_m2.go) and the ExecResumeDriver injects --output-format stream-json
// itself (driver_exec_resume.go), so a gemini M2 template need not carry the
// flags. Only the engines whose StdioDriver/AppServerDriver run the cmd
// verbatim are checked.
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
	// The flags that put each engine into structured-stdio mode. Sourced here
	// (not from the family yet) because Option A hasn't moved them onto the
	// family; when it does, this map becomes family.launch[M2].mode_args.
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
		checked++
		for _, need := range needs {
			if !strings.Contains(d.Backend.Cmd, need) {
				t.Errorf("%s: M2 %s cmd is missing %q — the driver needs it to "+
					"speak the mode protocol (see docs/discussions/engine-launch-contract.md)\n  cmd = %q",
					p, d.Backend.Kind, need, d.Backend.Cmd)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no M2 claude-code/codex templates checked — the invariant isn't exercising anything")
	}
}
