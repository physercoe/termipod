package server

import (
	"io/fs"
	"strings"
	"testing"

	hub "github.com/termipod/hub"
)

// TestEmbeddedKimiStewardTemplate_ShipsExpectedShape pins the kimi
// steward template's launch contract per ADR-026 D2: it loads from
// the embedded fs, declares backend.kind=kimi-code, drives M1 (ACP),
// and its cmd line invokes `kimi --yolo --thinking acp` so
// host-runner's M1 launcher gets a long-running JSON-RPC daemon
// with auto-approve and reasoning enabled by default.
//
// M4 is the sole fallback — kimi has no stream-json one-shot mode
// and no JSON-RPC app-server (only `--wire`, marked experimental
// upstream), so there is no M2 to fall back to.
//
// The --yolo / --thinking flags MUST precede the `acp` subcommand
// because they are kimi-cli top-level flags. launch_m1.go splices
// --mcp-config-file between `kimi` and `--yolo`, so this test also
// guards the splice-point convention by asserting the cmd starts
// with `kimi ` followed by --yolo before `acp`.
func TestEmbeddedKimiStewardTemplate_ShipsExpectedShape(t *testing.T) {
	yaml, err := fs.ReadFile(hub.TemplatesFS,
		"templates/agents/steward.kimi.v1.yaml")
	if err != nil {
		t.Fatalf("steward.kimi.v1.yaml not embedded: %v", err)
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

	must("template: agents.steward.kimi",
		"the canonical name spawn requests bind to")
	must("kind: kimi-code",
		"backend.kind drives the launcher's family-keyed dispatch")
	must("driving_mode: M1",
		"M1 = ACP daemon (kimi acp); ADR-026 D1 — M1-only")
	must("fallback_modes: [M4]",
		"M4 is the sole fallback — kimi has no production-stable M2 path")
	must(`cmd: "kimi --yolo --thinking acp"`,
		"top-level flags --yolo + --thinking MUST precede the `acp` subcommand")
	must("prompt: steward.kimi.v1.md",
		"the system prompt file that must also embed")

	// Negative checks: no per-turn / stream-json artifacts from other
	// engines' integrations should leak into kimi's cmd.
	cmdMustNot := func(needle, why string) {
		t.Helper()
		if strings.Contains(cmdLine, needle) {
			t.Errorf("backend.cmd contains %q but should not (%s); cmd line = %q", needle, why, cmdLine)
		}
	}
	cmdMustNot("--output-format",
		"--output-format is a per-turn flag from gemini/claude stream-json; ACP uses session/update")
	cmdMustNot("-p ",
		"-p delivers one-shot prompts; ACP delivers turns over JSON-RPC")
	cmdMustNot("--wire",
		"--wire is kimi-cli's experimental JSON-RPC flag; ADR-026 explicitly chooses --acp instead")
	cmdMustNot("--mcp-config-file",
		"--mcp-config-file is spliced by launch_m1.go at materialization time; template must not pin it")

	// Flag order matters: --yolo / --thinking are top-level kimi
	// flags and must precede the subcommand. The launch_m1 splice
	// inserts --mcp-config-file between `kimi` and the first flag,
	// so the leading `kimi ` token is the anchor.
	yoloIdx := strings.Index(cmdLine, "--yolo")
	thinkIdx := strings.Index(cmdLine, "--thinking")
	acpIdx := strings.Index(cmdLine, " acp")
	if yoloIdx < 0 || thinkIdx < 0 || acpIdx < 0 {
		t.Fatalf("cmd missing expected tokens: %q", cmdLine)
	}
	if !(yoloIdx < acpIdx && thinkIdx < acpIdx) {
		t.Errorf("flag order in cmd = %q; want --yolo and --thinking both before `acp`", cmdLine)
	}

	if _, err := fs.ReadFile(hub.TemplatesFS,
		"templates/prompts/steward.kimi.v1.md"); err != nil {
		t.Fatalf("steward.kimi.v1.md not embedded: %v", err)
	}
}
