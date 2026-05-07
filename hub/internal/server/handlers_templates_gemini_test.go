package server

import (
	"io/fs"
	"strings"
	"testing"

	hub "github.com/termipod/hub"
)

// TestEmbeddedGeminiStewardTemplate_ShipsExpectedShape pins the
// gemini steward template's launch contract: it loads from the
// embedded fs, declares backend.kind=gemini-cli, drives M1 (ACP),
// and its cmd line invokes `gemini --acp` so host-runner's M1
// launcher gets a long-running JSON-RPC daemon to attach ACPDriver
// to. M2 (exec-per-turn-with-resume) is the documented fallback for
// hosts whose gemini binary doesn't speak ACP.
//
// History: the template originally drove M2/ExecResumeDriver per
// ADR-013 D7. Verification against gemini-cli@0.41.2 confirmed
// --acp is stable and exposes session/request_permission, so the
// preferred shape switched to M1/ACPDriver and the old path moved
// to the fallback list.
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
		"backend.kind drives the launcher's family-keyed dispatch")
	must("driving_mode: M1",
		"M1 = ACP daemon (Zed Agent Client Protocol over stdio); M2 is the fallback")
	must("fallback_modes: [M2, M4]",
		"M2 (exec-per-turn) preserves the ADR-013 path for older gemini builds without --acp")
	must(`cmd: "gemini --acp"`,
		"M1 launcher spawns the engine in ACP daemon mode and wires ACPDriver to its stdio")
	must("prompt: steward.gemini.v1.md",
		"the system prompt file that must also embed")

	// Negative checks: per-turn flags from the legacy exec-per-turn
	// path MUST NOT survive into the cmd line. With --acp the
	// engine reads turns from session/prompt RPCs, not argv.
	cmdMustNot("--output-format",
		"stream-json was the exec-per-turn output format; ACP uses session/update notifications")
	cmdMustNot("--resume",
		"resume was the exec-per-turn cursor; ACP keeps the session live in-process")
	cmdMustNot("-p ",
		"-p was the exec-per-turn prompt arg; ACP delivers turns over JSON-RPC")

	// Prompt file must also be embedded so the spawn renderer can
	// resolve the prompt: reference at startup.
	if _, err := fs.ReadFile(hub.TemplatesFS,
		"templates/prompts/steward.gemini.v1.md"); err != nil {
		t.Fatalf("steward.gemini.v1.md not embedded: %v", err)
	}
}
