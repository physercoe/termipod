package server

import (
	"io/fs"
	"strings"
	"testing"

	hub "github.com/termipod/hub"
)

// TestEmbeddedGeminiStewardTemplate_ShipsExpectedShape pins the
// slice-6 contract: the gemini steward template loads from the
// embedded fs, declares backend.kind=gemini-cli, and its launch
// command resolves to the gemini bin via PATH (the exec-per-turn
// driver appends -p / --output-format / --resume per turn — those
// flags are NOT in the template cmd). Per ADR-013 D7 the template
// stays minimal because the driver owns argv construction.
//
// The template is what makes the gemini driver reachable from the
// steward UX: without an `agents.steward.gemini` template the spawn
// path has nothing to render. Locking the cmd shape here means a
// future renaming pass (or a stray edit) can't silently break the
// spawn → ExecResumeDriver wiring chain that slices 1-5 set up.
func TestEmbeddedGeminiStewardTemplate_ShipsExpectedShape(t *testing.T) {
	yaml, err := fs.ReadFile(hub.TemplatesFS,
		"templates/agents/steward.gemini.v1.yaml")
	if err != nil {
		t.Fatalf("steward.gemini.v1.yaml not embedded: %v", err)
	}
	body := string(yaml)

	must := func(needle, why string) {
		t.Helper()
		if !strings.Contains(body, needle) {
			t.Errorf("template missing %q (%s)", needle, why)
		}
	}
	// cmdLine returns the literal value of the cmd: key in backend.
	// We only check that the cmd VALUE is bin-only — the template
	// comments above the value are free to discuss flags the driver
	// appends so a reader knows what's actually launched at runtime.
	cmdLine := ""
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "cmd:") {
			cmdLine = trimmed
			break
		}
	}
	if cmdLine == "" {
		t.Fatal("no cmd: line found in template")
	}
	cmdMustNot := func(needle, why string) {
		t.Helper()
		if strings.Contains(cmdLine, needle) {
			t.Errorf("backend.cmd contains %q but should not (%s); cmd line = %q", needle, why, cmdLine)
		}
	}

	must("template: agents.steward.gemini",
		"the canonical name spawn requests bind to")
	must("kind: gemini-cli",
		"backend.kind drives the launch_m2 driver dispatch")
	must("driving_mode: M2",
		"stream-json output requires structured stdio")
	must(`cmd: "gemini"`,
		"bin name only — the exec-per-turn driver appends per-turn flags itself (ADR-013 D7)")
	must("prompt: steward.gemini.v1.md",
		"the system prompt file that must also embed")

	// Negative checks: per-turn flags MUST NOT be baked into cmd.
	// The exec-per-turn driver appends them itself; including them
	// here would be ignored at best and produce a broken argv at
	// worst.
	cmdMustNot("--output-format",
		"per-turn flag — driver appends, not the template")
	cmdMustNot("--resume",
		"per-turn flag — driver derives the UUID and threads --resume itself (ADR-013 D2)")
	cmdMustNot("-p ",
		"the prompt argument is the per-turn user text, not a template constant")

	// Prompt file must also be embedded so the spawn renderer can
	// resolve the prompt: reference at startup.
	if _, err := fs.ReadFile(hub.TemplatesFS,
		"templates/prompts/steward.gemini.v1.md"); err != nil {
		t.Fatalf("steward.gemini.v1.md not embedded: %v", err)
	}
}
