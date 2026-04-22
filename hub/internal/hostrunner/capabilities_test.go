package hostrunner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseVersion_TrimsFirstLine(t *testing.T) {
	cases := map[string]string{
		"":                          "",
		"\n\n":                      "",
		"claude 0.8.1\n":            "claude 0.8.1",
		"   gemini 1.2.3   \nnext":  "gemini 1.2.3",
		"\n\ncodex v4.0\nbuild abc": "codex v4.0",
	}
	for in, want := range cases {
		if got := parseVersion(in); got != want {
			t.Errorf("parseVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProbeCapabilities_UnknownBinaryMarksNotInstalled(t *testing.T) {
	// Empty PATH guarantees exec.LookPath fails for every entry, so every
	// known family should come back as installed=false with no version.
	t.Setenv("PATH", "")
	caps := ProbeCapabilities(context.Background())
	if len(caps.Agents) == 0 {
		t.Fatalf("expected known-agents to be populated even when absent")
	}
	for family, ag := range caps.Agents {
		if ag.Installed {
			t.Errorf("family %s unexpectedly installed with empty PATH", family)
		}
		if ag.Version != "" || len(ag.Supports) != 0 {
			t.Errorf("family %s leaked version/supports while not installed", family)
		}
	}
	if caps.ProbedAt == "" {
		t.Errorf("ProbedAt must be stamped")
	}
}

func TestProbeCapabilities_VersionCapturedFromFakeBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake binary is POSIX-only")
	}
	// Stand up a fake `claude` binary in a temp dir and point PATH at it.
	// This exercises the happy path (lookup succeeds, version parses)
	// without depending on any real agent CLI being installed on the
	// machine running the tests.
	dir := t.TempDir()
	script := filepath.Join(dir, "claude")
	body := "#!/bin/sh\necho 'claude-code 9.9.9-test'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("PATH", dir)

	caps := ProbeCapabilities(context.Background())
	cc, ok := caps.Agents["claude-code"]
	if !ok {
		t.Fatalf("claude-code missing from probe result")
	}
	if !cc.Installed {
		t.Fatalf("claude-code should be installed, got %+v", cc)
	}
	if cc.Version != "claude-code 9.9.9-test" {
		t.Errorf("unexpected version %q", cc.Version)
	}
	if len(cc.Supports) == 0 {
		t.Errorf("expected supports list for installed family")
	}
}

func TestCapabilities_HashStable(t *testing.T) {
	a := Capabilities{
		Agents: map[string]AgentCap{
			"claude-code": {Installed: true, Version: "v1", Supports: []string{"M1", "M2", "M4"}},
			"codex":       {Installed: false},
		},
		ProbedAt: "2026-04-22T10:00:00Z",
	}
	// Same content but different ProbedAt and differently-ordered Supports
	// must still hash identically.
	b := Capabilities{
		Agents: map[string]AgentCap{
			"codex":       {Installed: false},
			"claude-code": {Installed: true, Version: "v1", Supports: []string{"M4", "M1", "M2"}},
		},
		ProbedAt: "2099-01-01T00:00:00Z",
	}
	if a.Hash() != b.Hash() {
		t.Fatalf("hashes should match: a=%s b=%s", a.Hash(), b.Hash())
	}
	// Change a meaningful field — hash must move.
	c := a
	c.Agents = map[string]AgentCap{
		"claude-code": {Installed: true, Version: "v2", Supports: []string{"M1", "M2", "M4"}},
		"codex":       {Installed: false},
	}
	if a.Hash() == c.Hash() {
		t.Fatalf("hash should change when version changes")
	}
}
