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
		GatewayHubClient: &Client{Team: "team-1", Token: "host-token", BaseURL: "http://hub"},
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

	// Verify settings.local.json was materialized with hooks.
	body, err = os.ReadFile(filepath.Join(workdir, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatalf("read settings.local.json: %v", err)
	}
	if !strings.Contains(string(body), "mcp__termipod-host__hook_pre_compact") {
		t.Errorf("settings.local.json missing hook_pre_compact tool reference: %s", body)
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

func TestLaunchM4LocalLogTail_RejectsMissingWorkdir(t *testing.T) {
	tl := &trackingLauncher{}
	sp := Spawn{
		ChildID:   "x", Kind: "claude-code", MCPToken: "tok",
		SpawnSpec: "backend:\n  cmd: claude\n", // no default_workdir
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
