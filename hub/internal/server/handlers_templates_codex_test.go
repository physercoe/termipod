package server

import (
	"io/fs"
	"strings"
	"testing"

	hub "github.com/termipod/hub"
)

// TestEmbeddedCodexStewardTemplate_ShipsExpectedShape pins the
// slice-6 contract: the codex steward template loads from the
// embedded fs, declares backend.kind=codex, and its launch command
// sets CODEX_HOME=.codex so codex picks up the project-scoped MCP
// config slice 5 writes — bypassing codex's "trusted projects"
// gate per ADR-012 D5.
//
// The template is what makes the codex driver reachable from the
// steward UX: without an `agents.steward.codex` template the spawn
// path has nothing to render. Locking the cmd shape here means a
// future renaming pass (or a stray edit) can't silently break the
// spawn → driver_appserver wiring chain that slices 1-5 set up.
func TestEmbeddedCodexStewardTemplate_ShipsExpectedShape(t *testing.T) {
	yaml, err := fs.ReadFile(hub.TemplatesFS,
		"templates/agents/steward.codex.v1.yaml")
	if err != nil {
		t.Fatalf("steward.codex.v1.yaml not embedded: %v", err)
	}
	body := string(yaml)

	must := func(needle, why string) {
		t.Helper()
		if !strings.Contains(body, needle) {
			t.Errorf("template missing %q (%s)", needle, why)
		}
	}

	must("template: agents.steward.codex",
		"the canonical name spawn requests bind to")
	must("kind: codex",
		"backend.kind drives the launch_m2 driver dispatch")
	must("driving_mode: M2",
		"app-server JSON-RPC requires structured stdio")
	must("CODEX_HOME=.codex",
		"the trusted-projects bypass that lets codex read our project-scoped config.toml")
	must("codex app-server",
		"the long-lived JSON-RPC daemon driver_appserver speaks to")
	must("--listen stdio://",
		"the only transport that pairs with the host-runner's stdio pipe")
	must("prompt: steward.codex.v1.md",
		"the system prompt file that must also embed")

	// Prompt file must also be embedded so the spawn renderer can
	// resolve the prompt: reference at startup.
	if _, err := fs.ReadFile(hub.TemplatesFS,
		"templates/prompts/steward.codex.v1.md"); err != nil {
		t.Fatalf("steward.codex.v1.md not embedded: %v", err)
	}
}
