package hostrunner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	claudecode "github.com/termipod/hub/internal/drivers/local_log_tail/claude_code"
)

// trackingLauncher captures whatever cmd LaunchCmd was called with;
// returns a synthetic pane id. Used by the LocalLogTail launch test
// to verify the command resolved the way W7 expects.
type trackingLauncher struct {
	receivedCmd string
	paneID      string
	err         error
}

func (l *trackingLauncher) Launch(_ context.Context, sp Spawn) (string, error) {
	return l.paneID, l.err
}

func (l *trackingLauncher) LaunchCmd(_ context.Context, sp Spawn, cmd string) (string, error) {
	l.receivedCmd = cmd
	if l.err != nil {
		return "", l.err
	}
	return l.paneID, nil
}

// recordingAgentPoster captures emitted agent_events so the test can
// confirm the adapter wired in correctly.
type recordingAgentPoster struct {
	events []map[string]any
}

func (r *recordingAgentPoster) PostAgentEvent(_ context.Context, agentID, kind, producer string, payload any) error {
	pm, _ := payload.(map[string]any)
	r.events = append(r.events, map[string]any{
		"agent_id": agentID, "kind": kind, "producer": producer, "payload": pm,
	})
	return nil
}

func TestLaunchM4LocalLogTail_HappyPath_MaterializesConfigAndStartsGateway(t *testing.T) {
	// Set up a fake home so the adapter's path resolver looks under
	// our temp dir instead of the real ~/.claude.
	home := t.TempDir()
	t.Setenv("HOME", home)

	workdir := t.TempDir()
	projectDir := claudecode.ProjectDirFor(home, workdir)
	_ = os.MkdirAll(projectDir, 0o755)
	// Pre-seed the session JSONL so the adapter's WaitForSession
	// returns immediately. Real spawns would have claude write this
	// after launch; we short-circuit for the test.
	if err := os.WriteFile(
		filepath.Join(projectDir, "test-session.jsonl"),
		[]byte(`{"type":"user","message":{"content":"hi"}}`+"\n"), 0o644,
	); err != nil {
		t.Fatalf("seed jsonl: %v", err)
	}

	tl := &trackingLauncher{paneID: "%9"}
	poster := &recordingAgentPoster{}

	specYAML := "backend:\n  cmd: claude --dangerously-skip-permissions\n  default_workdir: " + workdir + "\n"
	sp := Spawn{
		ChildID:   "agent-x1",
		Handle:    "@x1",
		Kind:      "claude-code",
		MCPToken:  "tok-xyz",
		SpawnSpec: specYAML,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := launchM4LocalLogTail(ctx, M4LocalLogTailLaunchConfig{
		Spawn:            sp,
		Launcher:         tl,
		Client:           poster,
		HubURL:           "http://127.0.0.1:41825",
		GatewayHubClient: NewClient("http://hub", "host-token", "team-1"),
	})
	if err != nil {
		t.Fatalf("launchM4LocalLogTail: %v", err)
	}
	defer func() {
		if res.Gateway != nil {
			_ = res.Gateway.Close()
		}
		if res.Driver != nil {
			res.Driver.Stop()
		}
	}()

	if res.PaneID != "%9" {
		t.Errorf("PaneID = %q, want %%9", res.PaneID)
	}
	if tl.receivedCmd == "" || !strings.Contains(tl.receivedCmd, "claude") {
		t.Errorf("launcher cmd = %q; expected the spec's backend.cmd", tl.receivedCmd)
	}

	// Verify .mcp.json was materialized with both servers.
	body, err := os.ReadFile(filepath.Join(workdir, ".mcp.json"))
	if err != nil {
		t.Fatalf("read .mcp.json: %v", err)
	}
	var parsed struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	_ = json.Unmarshal(body, &parsed)
	if _, ok := parsed.MCPServers["termipod"]; !ok {
		t.Errorf(".mcp.json missing termipod server entry")
	}
	if _, ok := parsed.MCPServers["termipod-host"]; !ok {
		t.Errorf(".mcp.json missing termipod-host server entry")
	}

	// Verify settings.local.json was materialized with hooks. v1.0.659
	// shifted the hook shape from type:"mcp_tool" + tool:"mcp__..."
	// (which claude-code's schema rejected) to type:"command" +
	// command:"host-runner hook-fire ...". The new shape carries the
	// event name on the command line; check for both the shim
	// invocation AND the PreCompact event reference.
	body, err = os.ReadFile(filepath.Join(workdir, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatalf("read settings.local.json: %v", err)
	}
	if !strings.Contains(string(body), "host-runner hook-fire") {
		t.Errorf("settings.local.json missing hook-fire shim invocation: %s", body)
	}
	if !strings.Contains(string(body), "--event PreCompact") {
		t.Errorf("settings.local.json missing PreCompact event reference: %s", body)
	}

	// Verify gateway is reachable + the driver implements HookSink.
	if res.Gateway == nil {
		t.Fatal("Gateway nil")
	}
	if res.Gateway.HookSink == nil {
		t.Fatal("Gateway.HookSink not wired")
	}
	if res.Driver == nil {
		t.Fatal("Driver nil")
	}
}

func TestLaunchM4LocalLogTail_RejectsNonClaudeCode(t *testing.T) {
	tl := &trackingLauncher{}
	_, err := launchM4LocalLogTail(context.Background(), M4LocalLogTailLaunchConfig{
		Spawn:    Spawn{ChildID: "x", Kind: "gemini-cli"},
		Launcher: tl,
		HubURL:   "http://x",
	})
	if err == nil {
		t.Fatal("non-claude-code kind was accepted")
	}
}

func TestLaunchM4LocalLogTail_RejectsMissingWorkdirAndNoProject(t *testing.T) {
	tl := &trackingLauncher{}
	sp := Spawn{
		ChildID:   "x", Kind: "claude-code", MCPToken: "tok",
		SpawnSpec: "backend:\n  cmd: claude\n", // no default_workdir, no project_id
	}
	_, err := launchM4LocalLogTail(context.Background(), M4LocalLogTailLaunchConfig{
		Spawn: sp, Launcher: tl, HubURL: "http://x",
	})
	if err == nil {
		t.Fatal("missing default_workdir was accepted")
	}
	if !strings.Contains(err.Error(), "default_workdir") {
		t.Errorf("err = %v; want mention of default_workdir", err)
	}
}

// When the template omits default_workdir but the spawn carries a
// project_id, the launcher derives ~/hub-work/<pid8>/<handle>. Same
// rule M2 already enforces (launch_m2.go:198-209). Without this,
// project-scoped claude-code stewards on a shared host would have to
// hardcode unique workdirs in every overlay or collide on the same
// .mcp.json — see ADR-025 W6 + the workdir-scoping audit note.
func TestLaunchM4LocalLogTail_AutoDerivesWorkdirFromProjectAndHandle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	const projectID = "01KRP01BY6G6G6Y9T0QQG615AQ"
	const handle = "@steward.01KRP01B"
	const pid8 = "01KRP01B"
	derivedWD := filepath.Join(home, "hub-work", pid8, handle)

	// Pre-seed the JSONL under the path the launcher will derive so
	// the adapter's WaitForSession returns immediately.
	projectDir := claudecode.ProjectDirFor(home, derivedWD)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(projectDir, "test-session.jsonl"),
		[]byte(`{"type":"user","message":{"content":"hi"}}`+"\n"), 0o644,
	); err != nil {
		t.Fatalf("seed jsonl: %v", err)
	}

	tl := &trackingLauncher{paneID: "%9"}
	poster := &recordingAgentPoster{}
	sp := Spawn{
		ChildID:   "agent-y1",
		Handle:    handle,
		Kind:      "claude-code",
		MCPToken:  "tok-xyz",
		ProjectID: projectID,
		SpawnSpec: "backend:\n  cmd: claude --dangerously-skip-permissions\n", // no default_workdir
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := launchM4LocalLogTail(ctx, M4LocalLogTailLaunchConfig{
		Spawn:            sp,
		Launcher:         tl,
		Client:           poster,
		HubURL:           "http://127.0.0.1:41825",
		GatewayHubClient: NewClient("http://hub", "host-token", "team-1"),
	})
	if err != nil {
		t.Fatalf("launchM4LocalLogTail: %v", err)
	}
	defer func() {
		if res.Gateway != nil {
			_ = res.Gateway.Close()
		}
		if res.Driver != nil {
			res.Driver.Stop()
		}
	}()

	// .mcp.json should land at the derived workdir, proving the auto-derive
	// fired — not at any template-supplied path.
	if _, err := os.Stat(filepath.Join(derivedWD, ".mcp.json")); err != nil {
		t.Errorf("expected .mcp.json at derived workdir %s; stat err=%v",
			derivedWD, err)
	}
	if _, err := os.Stat(filepath.Join(derivedWD, ".claude", "settings.local.json")); err != nil {
		t.Errorf("expected settings.local.json at derived workdir %s; stat err=%v",
			derivedWD, err)
	}
}

func TestLaunchM4LocalLogTail_RejectsMissingToken(t *testing.T) {
	tl := &trackingLauncher{}
	workdir := t.TempDir()
	sp := Spawn{
		ChildID:   "x", Kind: "claude-code",
		SpawnSpec: "backend:\n  cmd: claude\n  default_workdir: " + workdir + "\n",
	}
	_, err := launchM4LocalLogTail(context.Background(), M4LocalLogTailLaunchConfig{
		Spawn: sp, Launcher: tl, HubURL: "http://x",
	})
	if err == nil {
		t.Fatal("missing MCPToken was accepted")
	}
	if !strings.Contains(err.Error(), "MCPToken") {
		t.Errorf("err = %v; want mention of MCPToken", err)
	}
}

// The cmd passed to the launcher MUST be prefixed with `cd <workdir> &&`
// so claude lands in the resolved workdir — claude-code keys its
// session JSONL by encoded-cwd (~/.claude/projects/<encoded-cwd>/),
// so the adapter's pathresolver only finds the tail if cwd == workdir.
// Without this prefix the launch hangs at WaitForSession and runner.go
// reports "M4 LocalLogTail launch failed". Locks the v1.0.657 fix.
func TestLaunchM4LocalLogTail_PrefixesCmdWithCdWorkdir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workdir := t.TempDir()
	projectDir := claudecode.ProjectDirFor(home, workdir)
	_ = os.MkdirAll(projectDir, 0o755)
	if err := os.WriteFile(
		filepath.Join(projectDir, "test-session.jsonl"),
		[]byte(`{"type":"user","message":{"content":"hi"}}`+"\n"), 0o644,
	); err != nil {
		t.Fatalf("seed jsonl: %v", err)
	}

	tl := &trackingLauncher{paneID: "%9"}
	poster := &recordingAgentPoster{}
	sp := Spawn{
		ChildID: "agent-cd1", Handle: "@cd1", Kind: "claude-code", MCPToken: "tok",
		SpawnSpec: "backend:\n  cmd: claude --dangerously-skip-permissions\n  default_workdir: " + workdir + "\n",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := launchM4LocalLogTail(ctx, M4LocalLogTailLaunchConfig{
		Spawn: sp, Launcher: tl, Client: poster, HubURL: "http://127.0.0.1:41825",
		GatewayHubClient: NewClient("http://hub", "host-token", "team-1"),
	})
	if err != nil {
		t.Fatalf("launchM4LocalLogTail: %v", err)
	}
	defer func() {
		if res.Gateway != nil {
			_ = res.Gateway.Close()
		}
		if res.Driver != nil {
			res.Driver.Stop()
		}
	}()

	// shellEscape may quote the path; check both bare and quoted forms.
	wantPrefix := "cd " + shellEscape(workdir) + " &&"
	if !strings.HasPrefix(tl.receivedCmd, wantPrefix) {
		t.Errorf("launcher cmd = %q; want prefix %q", tl.receivedCmd, wantPrefix)
	}
	if !strings.Contains(tl.receivedCmd, "claude --dangerously-skip-permissions") {
		t.Errorf("launcher cmd = %q; want backend.cmd appended after cd", tl.receivedCmd)
	}
}

// When spec.ContextFiles is non-empty the launcher MUST materialize
// each entry into the workdir. M1 and M2 do this for claude-code's
// CLAUDE.md persona; the M4 LocalLogTail path was the silent holdout
// since v1.0.592 — the same gap agy hit at v1.0.651. Without this
// the steward spawns persona-less and the first turn is bare-claude.
func TestLaunchM4LocalLogTail_WritesContextFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workdir := t.TempDir()
	projectDir := claudecode.ProjectDirFor(home, workdir)
	_ = os.MkdirAll(projectDir, 0o755)
	if err := os.WriteFile(
		filepath.Join(projectDir, "test-session.jsonl"),
		[]byte(`{"type":"user","message":{"content":"hi"}}`+"\n"), 0o644,
	); err != nil {
		t.Fatalf("seed jsonl: %v", err)
	}

	tl := &trackingLauncher{paneID: "%9"}
	poster := &recordingAgentPoster{}
	const personaBody = "# Steward persona\n\nYou are a steward agent.\n"
	specYAML := strings.Join([]string{
		"backend:",
		"  cmd: claude --dangerously-skip-permissions",
		"  default_workdir: " + workdir,
		"context_files:",
		"  CLAUDE.md: |",
		"    # Steward persona",
		"",
		"    You are a steward agent.",
	}, "\n") + "\n"
	sp := Spawn{
		ChildID: "agent-ctx1", Handle: "@ctx1", Kind: "claude-code", MCPToken: "tok",
		SpawnSpec: specYAML,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := launchM4LocalLogTail(ctx, M4LocalLogTailLaunchConfig{
		Spawn: sp, Launcher: tl, Client: poster, HubURL: "http://127.0.0.1:41825",
		GatewayHubClient: NewClient("http://hub", "host-token", "team-1"),
	})
	if err != nil {
		t.Fatalf("launchM4LocalLogTail: %v", err)
	}
	defer func() {
		if res.Gateway != nil {
			_ = res.Gateway.Close()
		}
		if res.Driver != nil {
			res.Driver.Stop()
		}
	}()

	body, rerr := os.ReadFile(filepath.Join(workdir, "CLAUDE.md"))
	if rerr != nil {
		t.Fatalf("CLAUDE.md not written: %v", rerr)
	}
	if string(body) != personaBody {
		t.Errorf("CLAUDE.md body = %q; want %q", string(body), personaBody)
	}
}

// preTrustWorkspaceClaudeCode MUST mark the workdir trusted in
// ~/.claude.json so claude doesn't open with the "Do you trust this
// folder?" welcome-screen dialog the mobile client can't drive.
func TestPreTrustWorkspaceClaudeCode_FreshFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workdir := "/home/ubuntu/hub-work/abcd1234/@steward.x"
	if err := preTrustWorkspaceClaudeCode(workdir); err != nil {
		t.Fatalf("preTrust: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatalf("read .claude.json: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("parse: %v", err)
	}
	projects, _ := cfg["projects"].(map[string]any)
	entry, _ := projects[workdir].(map[string]any)
	if entry == nil {
		t.Fatalf("projects[%q] missing: %+v", workdir, cfg)
	}
	if entry["hasTrustDialogAccepted"] != true {
		t.Errorf("hasTrustDialogAccepted = %v; want true", entry["hasTrustDialogAccepted"])
	}
	if entry["hasCompletedProjectOnboarding"] != true {
		t.Errorf("hasCompletedProjectOnboarding = %v; want true", entry["hasCompletedProjectOnboarding"])
	}
}

// Pre-existing top-level fields and OTHER per-project entries MUST be
// preserved untouched — the user's interactive claude config lives in
// the same file. Locks the surgical-edit invariant.
func TestPreTrustWorkspaceClaudeCode_PreservesOtherKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	const existing = `{
  "numStartups": 30,
  "installMethod": "native",
  "projects": {
    "/home/user/elsewhere": {
      "hasTrustDialogAccepted": false,
      "lastCost": 12.5
    }
  }
}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(existing), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	workdir := "/home/ubuntu/hub-work/abcd1234/@steward.x"
	if err := preTrustWorkspaceClaudeCode(workdir); err != nil {
		t.Fatalf("preTrust: %v", err)
	}

	body, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	var cfg map[string]any
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg["numStartups"].(float64) != 30 {
		t.Errorf("numStartups lost: %v", cfg["numStartups"])
	}
	if cfg["installMethod"] != "native" {
		t.Errorf("installMethod lost: %v", cfg["installMethod"])
	}
	projects, _ := cfg["projects"].(map[string]any)
	elsewhere, _ := projects["/home/user/elsewhere"].(map[string]any)
	if elsewhere["hasTrustDialogAccepted"] != false {
		t.Errorf("elsewhere.hasTrustDialogAccepted mutated: %v", elsewhere["hasTrustDialogAccepted"])
	}
	if elsewhere["lastCost"].(float64) != 12.5 {
		t.Errorf("elsewhere.lastCost mutated: %v", elsewhere["lastCost"])
	}
	entry, _ := projects[workdir].(map[string]any)
	if entry["hasTrustDialogAccepted"] != true {
		t.Errorf("target entry not flipped trusted: %+v", entry)
	}
}

// Re-spawn with an already-trusted workdir MUST be a no-op (idempotent)
// — should not touch the file at all when both flags are already true.
func TestPreTrustWorkspaceClaudeCode_AlreadyTrusted_NoMutation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workdir := "/home/ubuntu/hub-work/abcd1234/@steward.x"
	preExisting := map[string]any{
		"projects": map[string]any{
			workdir: map[string]any{
				"hasTrustDialogAccepted":        true,
				"hasCompletedProjectOnboarding": true,
				"lastCost":                      42.0,
			},
		},
	}
	body, _ := json.Marshal(preExisting)
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), body, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	statBefore, _ := os.Stat(filepath.Join(home, ".claude.json"))

	if err := preTrustWorkspaceClaudeCode(workdir); err != nil {
		t.Fatalf("preTrust: %v", err)
	}

	statAfter, _ := os.Stat(filepath.Join(home, ".claude.json"))
	if statBefore.ModTime() != statAfter.ModTime() {
		t.Errorf("file was rewritten despite already-trusted state (mtime changed)")
	}
}
