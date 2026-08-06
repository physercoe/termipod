package hostrunner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	locallogtail "github.com/termipod/hub/internal/drivers/local_log_tail"
	kimicode "github.com/termipod/hub/internal/drivers/local_log_tail/kimi_code"
)

// seedKimiStoreHome points KIMI_CODE_HOME at a temp dir and (optionally)
// seeds a sessions/ tree inside it.
func seedKimiStoreHome(t *testing.T, withSessions bool) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	if withSessions {
		if err := os.MkdirAll(filepath.Join(home, "sessions"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "workspaces.json"),
			[]byte(`{"version":1,"workspaces":{},"deleted_workspace_ids":[]}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func kimiSpawn(kind, workdir, cmd string) Spawn {
	return Spawn{
		ChildID:   "agent-kimi-1",
		Handle:    "@kimi1",
		Kind:      kind,
		MCPToken:  "tok-kimi",
		SpawnSpec: "backend:\n  cmd: " + cmd + "\n  default_workdir: " + workdir + "\n",
	}
}

// Happy path: store present + protocol sniff clean → the wire-tail
// driver is built, the pane launches with the `cd <workdir>` prefix,
// and the kimi-code-ts MCP config lands at <workdir>/.kimi-code/mcp.json.
func TestLaunchM4KimiWireTail_HappyPath(t *testing.T) {
	seedKimiStoreHome(t, true)
	workdir := t.TempDir()
	tl := &trackingLauncher{paneID: "%11"}
	poster := &recordingAgentPoster{}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := launchM4KimiWireTail(ctx, M4LocalLogTailLaunchConfig{
		Spawn:    kimiSpawn("kimi-code-ts", workdir, "kimi --yolo"),
		Launcher: tl,
		Client:   poster,
		HubURL:   "http://127.0.0.1:41825",
	})
	if err != nil {
		t.Fatalf("launchM4KimiWireTail: %v", err)
	}
	defer func() {
		if res.Driver != nil {
			res.Driver.Stop()
		}
	}()

	if res.PaneID != "%11" {
		t.Errorf("PaneID = %q, want %%11", res.PaneID)
	}
	if !strings.HasPrefix(tl.receivedCmd, "cd ") ||
		!strings.Contains(tl.receivedCmd, " && kimi --yolo") {
		t.Errorf("launcher cmd = %q; want cd <workdir> prefix", tl.receivedCmd)
	}
	if strings.Contains(tl.receivedCmd, "--mcp-config-file") {
		t.Errorf("kimi-code-ts needs no --mcp-config-file splice: %q", tl.receivedCmd)
	}
	// kimi-code-ts auto-discovers <workdir>/.kimi-code/mcp.json.
	if _, err := os.Stat(filepath.Join(workdir, ".kimi-code", "mcp.json")); err != nil {
		t.Errorf("workdir .kimi-code/mcp.json not materialized: %v", err)
	}
	// lifecycle.started was posted by driver.Start.
	var sawStarted bool
	for _, e := range poster.events {
		if e["kind"] == "lifecycle" {
			if p, _ := e["payload"].(map[string]any); p["phase"] == "started" {
				sawStarted = true
			}
		}
	}
	if !sawStarted {
		t.Errorf("lifecycle.started never posted: %+v", poster.events)
	}
}

// Fallback gate: no kimi wire store on the host (older kimi / Python
// kimi-cli / KIMI_CODE_HOME moved) → error BEFORE any pane is spawned,
// so the runner's PaneDriver fall-through is safe.
func TestLaunchM4KimiWireTail_NoStoreFallsBack(t *testing.T) {
	seedKimiStoreHome(t, false) // store home exists but has no sessions/
	workdir := t.TempDir()
	tl := &trackingLauncher{paneID: "%13"}

	_, err := launchM4KimiWireTail(context.Background(), M4LocalLogTailLaunchConfig{
		Spawn:    kimiSpawn("kimi-code-ts", workdir, "kimi --yolo"),
		Launcher: tl,
		Client:   &recordingAgentPoster{},
		HubURL:   "http://127.0.0.1:41825",
	})
	if err == nil {
		t.Fatal("want error when the wire store is missing")
	}
	if !strings.Contains(err.Error(), "wire store") {
		t.Errorf("err = %v; want the store-missing reason", err)
	}
	if tl.receivedCmd != "" {
		t.Errorf("pane must NOT be spawned on the fallback path; cmd = %q", tl.receivedCmd)
	}
}

// Protocol gate: an existing wire file at an unsupported version makes
// the launch fail pre-spawn (the new session would write the same
// protocol — it's a property of the installed kimi build).
func TestLaunchM4KimiWireTail_UnsupportedProtocolFallsBack(t *testing.T) {
	store := seedKimiStoreHome(t, true)
	// Seed one prior wire at protocol 9.
	wireDir := filepath.Join(store, "sessions", "wd_old", "session_old", "agents", "main")
	if err := os.MkdirAll(wireDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wireDir, "wire.jsonl"),
		[]byte(`{"type":"metadata","protocol_version":"9","created_at":1}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	workdir := t.TempDir()
	tl := &trackingLauncher{paneID: "%14"}
	_, err := launchM4KimiWireTail(context.Background(), M4LocalLogTailLaunchConfig{
		Spawn:    kimiSpawn("kimi-code-ts", workdir, "kimi --yolo"),
		Launcher: tl,
		Client:   &recordingAgentPoster{},
		HubURL:   "http://127.0.0.1:41825",
	})
	if err == nil {
		t.Fatal("want error on unsupported wire protocol")
	}
	if !strings.Contains(err.Error(), "protocol") {
		t.Errorf("err = %v; want the protocol-mismatch reason", err)
	}
	if tl.receivedCmd != "" {
		t.Errorf("pane must NOT be spawned on the fallback path; cmd = %q", tl.receivedCmd)
	}
}

// A v1.4 prior wire passes the sniff (the version the plan's Appendix B
// pins, and what kimi-code 0.28.1 writes in the captured corpus).
func TestLaunchM4KimiWireTail_SupportedProtocolPasses(t *testing.T) {
	store := seedKimiStoreHome(t, true)
	wireDir := filepath.Join(store, "sessions", "wd_old", "session_old", "agents", "main")
	if err := os.MkdirAll(wireDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wireDir, "wire.jsonl"),
		[]byte(`{"type":"metadata","protocol_version":"1.4","created_at":1}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	workdir := t.TempDir()
	tl := &trackingLauncher{paneID: "%15"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := launchM4KimiWireTail(ctx, M4LocalLogTailLaunchConfig{
		Spawn:    kimiSpawn("kimi-code-ts", workdir, "kimi --yolo"),
		Launcher: tl,
		Client:   &recordingAgentPoster{},
		HubURL:   "http://127.0.0.1:41825",
	})
	if err != nil {
		t.Fatalf("v1.4 store should pass the gate: %v", err)
	}
	defer func() {
		if res.Driver != nil {
			res.Driver.Stop()
		}
	}()
	if res.PaneID != "%15" {
		t.Errorf("PaneID = %q, want %%15", res.PaneID)
	}
}

// Wrong family is rejected before anything is touched.
func TestLaunchM4KimiWireTail_RejectsOtherKinds(t *testing.T) {
	seedKimiStoreHome(t, true)
	workdir := t.TempDir()
	_, err := launchM4KimiWireTail(context.Background(), M4LocalLogTailLaunchConfig{
		Spawn:    kimiSpawn("gemini-cli", workdir, "gemini"),
		Launcher: &trackingLauncher{},
		Client:   &recordingAgentPoster{},
		HubURL:   "http://127.0.0.1:41825",
	})
	if err == nil {
		t.Fatal("want kind rejection")
	}
}

// kimiPermissionModeFromCmd: --yolo → "yolo", anything else →
// "interactive" (both kimi product lines share the flag).
func TestKimiPermissionModeFromCmd(t *testing.T) {
	if got := kimiPermissionModeFromCmd("kimi --yolo"); got != "yolo" {
		t.Errorf("got %q, want yolo", got)
	}
	if got := kimiPermissionModeFromCmd("kimi --yolo --thinking"); got != "yolo" {
		t.Errorf("got %q, want yolo", got)
	}
	if got := kimiPermissionModeFromCmd("kimi"); got != "interactive" {
		t.Errorf("got %q, want interactive", got)
	}
}

// An env profile can point one agent at its own kimi store, and that
// value reaches the CHILD only — host-runner's own environment never
// sees it. The test inverts the two roots so the outcome is unambiguous:
// the process env names a store with no sessions/ (which the pre-flight
// gate rejects) and the spawn names a good one. A launch that succeeds
// can only have used the spawn's value.
//
// It also pins the pairing: the gate and the tail must resolve the SAME
// root. A gate that validates one store while the adapter tails another
// passes for the wrong reason, and the failure downstream is silent —
// a wire tail with no session looks exactly like an agent that has not
// spoken yet.
func TestLaunchM4KimiWireTail_SpawnEnvOverridesStoreHome(t *testing.T) {
	seedKimiStoreHome(t, false) // host-runner's env: store with NO sessions/

	spawnStore := t.TempDir()
	if err := os.MkdirAll(filepath.Join(spawnStore, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(spawnStore, "workspaces.json"),
		[]byte(`{"version":1,"workspaces":{},"deleted_workspace_ids":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	workdir := t.TempDir()
	spawn := kimiSpawn("kimi-code-ts", workdir, "kimi --yolo")
	spawn.SpawnSpec += "env_vars:\n  KIMI_CODE_HOME: " + spawnStore + "\n"

	tl := &trackingLauncher{paneID: "%17"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := launchM4KimiWireTail(ctx, M4LocalLogTailLaunchConfig{
		Spawn:    spawn,
		Launcher: tl,
		Client:   &recordingAgentPoster{},
		HubURL:   "http://127.0.0.1:41825",
	})
	if err != nil {
		t.Fatalf("spawn-level KIMI_CODE_HOME ignored — the gate used host-runner's store: %v", err)
	}
	defer func() {
		if res.Driver != nil {
			res.Driver.Stop()
		}
	}()

	d, ok := res.Driver.(*locallogtail.Driver)
	if !ok {
		t.Fatalf("driver is %T, want *locallogtail.Driver", res.Driver)
	}
	a, ok := d.Adapter.(*kimicode.Adapter)
	if !ok {
		t.Fatalf("adapter is %T, want *kimicode.Adapter", d.Adapter)
	}
	if a.StoreHome != spawnStore {
		t.Errorf("adapter.StoreHome = %q, want the spawn's store %q — the gate and the tail disagree",
			a.StoreHome, spawnStore)
	}
}
