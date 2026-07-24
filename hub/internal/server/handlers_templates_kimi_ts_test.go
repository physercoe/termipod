package server

import (
	"io/fs"
	"strings"
	"testing"

	hub "github.com/termipod/hub"
)

// TestEmbeddedKimiTSStewardTemplate_ShipsExpectedShape pins the
// kimi-ts steward template's launch contract per ADR-054 D2: it loads
// from the embedded fs, declares backend.kind=kimi-code-ts, drives M1
// (ACP), and its cmd line invokes `kimi --yolo acp` so host-runner's
// M1 launcher gets a long-running JSON-RPC daemon with auto-approve
// enabled by default.
//
// M4 is the sole fallback — the TS build's headless
// `-p --output-format stream-json` mode is unwired (its NDJSON schema
// needs its own frame profile), so there is no M2 to fall back to.
//
// The --yolo flag MUST precede the `acp` subcommand because it is a
// kimi top-level flag. Unlike the Python line, there is NO --thinking
// flag (removed upstream; thinking is config-driven) and NO
// --mcp-config-file splice (removed upstream; the engine auto-discovers
// <workdir>/.kimi-code/mcp.json, which launch_m2's writeKimiTSMCPConfig
// materializes per spawn).
func TestEmbeddedKimiTSStewardTemplate_ShipsExpectedShape(t *testing.T) {
	yaml, err := fs.ReadFile(hub.TemplatesFS,
		"templates/agents/steward.kimi-ts.v1.yaml")
	if err != nil {
		t.Fatalf("steward.kimi-ts.v1.yaml not embedded: %v", err)
	}
	body := string(yaml)

	must := func(needle, why string) {
		t.Helper()
		if !strings.Contains(body, needle) {
			t.Errorf("template missing %q (%s)", needle, why)
		}
	}

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

	must("template: agents.steward.kimi-ts",
		"the canonical name spawn requests bind to")
	must("kind: kimi-code-ts",
		"backend.kind drives the launcher's family-keyed dispatch")
	must("driving_mode: M1",
		"M1 = ACP daemon (kimi acp); ADR-054 D1 — M1-only")
	must("fallback_modes: [M4]",
		"M4 is the sole fallback — the TS build's stream-json mode is unwired")
	must(`cmd: "kimi --yolo"`,
		"the interactive base cmd — the acp subcommand is composed on at launch from the family's launch.M1.mode_args (ADR-043, #378)")
	must(`display_label: "Steward (kimi-ts)"`,
		"the picker label that distinguishes this steward from the Python-line one")
	must("prompt: steward.kimi-ts.v1.md",
		"the system prompt file that must also embed")

	// Negative checks: flags from the Python kimi line or from other
	// engines' integrations must not leak into the TS build's cmd.
	cmdMustNot := func(needle, why string) {
		t.Helper()
		if strings.Contains(cmdLine, needle) {
			t.Errorf("backend.cmd contains %q but should not (%s); cmd line = %q", needle, why, cmdLine)
		}
	}
	cmdMustNot("--thinking",
		"--thinking was removed upstream in the TS rewrite; thinking is config-driven (~/.kimi-code/config.toml)")
	cmdMustNot("--mcp-config-file",
		"--mcp-config-file was removed upstream; the engine auto-discovers <workdir>/.kimi-code/mcp.json")
	cmdMustNot("--output-format",
		"--output-format is a per-turn flag from gemini/claude stream-json; ACP uses session/update")
	cmdMustNot(" -p ",
		"-p delivers one-shot prompts; ACP delivers turns over JSON-RPC")
	cmdMustNot("--wire",
		"--wire was the Python line's experimental JSON-RPC flag; ADR-054 chooses acp")

	// The base cmd carries no subcommand: launchM1 composes `acp` from
	// the family's launch.M1.mode_args (#378), so the same cmd also
	// launches a usable TUI when the spawn lands on M4.
	if strings.Contains(cmdLine, " acp") {
		t.Errorf("cmd = %q; want no acp subcommand (composed at launch from launch.M1.mode_args)", cmdLine)
	}
	if !strings.Contains(cmdLine, "--yolo") {
		t.Errorf("cmd missing --yolo (the steward's consent posture): %q", cmdLine)
	}

	if _, err := fs.ReadFile(hub.TemplatesFS,
		"templates/prompts/steward.kimi-ts.v1.md"); err != nil {
		t.Fatalf("steward.kimi-ts.v1.md not embedded: %v", err)
	}
}
