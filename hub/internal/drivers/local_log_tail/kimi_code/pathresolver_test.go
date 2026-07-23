package kimi_code

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seedStore builds a fake kimi store: workspaces.json mapping
// cwd→wd_xxx, plus the given session dirs under sessions/<wd>/.
func seedStore(t *testing.T, cwd string, sessionNames ...string) (storeHome string) {
	t.Helper()
	storeHome = t.TempDir()
	ws := `{"version":1,"workspaces":{"wd_test_000000000001":{"root":` +
		jsonQuote(cwd) + `,"name":"test","created_at":"2026-07-23T00:00:00.000Z","last_opened_at":"2026-07-23T00:00:00.000Z"}},"deleted_workspace_ids":[]}`
	if err := os.WriteFile(filepath.Join(storeHome, "workspaces.json"), []byte(ws), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range sessionNames {
		dir := filepath.Join(storeHome, "sessions", "wd_test_000000000001", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return storeHome
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestStoreHome_EnvOverrideAndDefault(t *testing.T) {
	t.Setenv("KIMI_CODE_HOME", "/tmp/kimi-custom")
	got, err := StoreHome()
	if err != nil || got != "/tmp/kimi-custom" {
		t.Fatalf("StoreHome with env = %q, %v", got, err)
	}
	t.Setenv("KIMI_CODE_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err = StoreHome()
	if err != nil || got != filepath.Join(home, ".kimi-code") {
		t.Fatalf("StoreHome default = %q, %v", got, err)
	}
}

func TestLookupWorkspaceID(t *testing.T) {
	cwd := t.TempDir()
	store := seedStore(t, cwd)

	id, err := LookupWorkspaceID(store, cwd)
	if err != nil || id != "wd_test_000000000001" {
		t.Fatalf("LookupWorkspaceID = %q, %v", id, err)
	}

	// Trailing slash cleans to the same path.
	if id, err := LookupWorkspaceID(store, cwd+string(filepath.Separator)); err != nil || id == "" {
		t.Fatalf("LookupWorkspaceID(trailing slash) = %q, %v", id, err)
	}

	// Unknown cwd → ErrNoWorkspace (the wait loop keeps polling).
	if _, err := LookupWorkspaceID(store, "/no/such/dir"); !errors.Is(err, ErrNoWorkspace) {
		t.Fatalf("unknown cwd err = %v, want ErrNoWorkspace", err)
	}

	// Missing workspaces.json → ErrNoWorkspace (kimi hasn't opened any
	// workspace on this host yet).
	empty := t.TempDir()
	if _, err := LookupWorkspaceID(empty, cwd); !errors.Is(err, ErrNoWorkspace) {
		t.Fatalf("missing workspaces.json err = %v, want ErrNoWorkspace", err)
	}

	// Corrupt workspaces.json → hard parse error (NOT ErrNoWorkspace —
	// waiting won't fix it).
	bad := t.TempDir()
	_ = os.WriteFile(filepath.Join(bad, "workspaces.json"), []byte("{nope"), 0o600)
	if _, err := LookupWorkspaceID(bad, cwd); err == nil || errors.Is(err, ErrNoWorkspace) {
		t.Fatalf("corrupt workspaces.json err = %v, want parse error", err)
	}
}

func TestResolveLatestSessionSince(t *testing.T) {
	cwd := t.TempDir()
	store := seedStore(t, cwd, "session_old", "session_new")

	// Make session_new strictly newer than session_old.
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(store, "sessions", "wd_test_000000000001", "session_old"), old, old)

	got, err := ResolveLatestSessionSince(store, "wd_test_000000000001", time.Time{})
	if err != nil || filepath.Base(got) != "session_new" {
		t.Fatalf("ResolveLatest = %q, %v", got, err)
	}

	// since-cutoff filters out the older session entirely; a cutoff in
	// the future finds nothing.
	if _, err := ResolveLatestSessionSince(store, "wd_test_000000000001", time.Now().Add(time.Hour)); !errors.Is(err, ErrNoSession) {
		t.Fatalf("future since err = %v, want ErrNoSession", err)
	}

	// Unknown wd id → ErrNoSession.
	if _, err := ResolveLatestSessionSince(store, "wd_nope", time.Time{}); !errors.Is(err, ErrNoSession) {
		t.Fatalf("unknown wd err = %v, want ErrNoSession", err)
	}
}

func TestWaitForSession_AppearsMidWait(t *testing.T) {
	cwd := t.TempDir()
	store := seedStore(t, cwd) // no sessions yet

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// kimi "starts" a beat later: the session dir appears mid-wait.
	go func() {
		time.Sleep(150 * time.Millisecond)
		dir := filepath.Join(store, "sessions", "wd_test_000000000001", "session_abc")
		_ = os.MkdirAll(dir, 0o755)
	}()

	got, err := WaitForSession(ctx, store, cwd, 25*time.Millisecond, time.Now().Add(-time.Second))
	if err != nil || filepath.Base(got) != "session_abc" {
		t.Fatalf("WaitForSession = %q, %v", got, err)
	}
}

func TestWaitForSession_Timeout(t *testing.T) {
	cwd := t.TempDir()
	store := seedStore(t, cwd)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := WaitForSession(ctx, store, cwd, 25*time.Millisecond, time.Time{}); err == nil {
		t.Fatal("want timeout error when no session appears")
	}
}

func TestSniffProtocolVersion(t *testing.T) {
	cwd := t.TempDir()
	store := seedStore(t, cwd, "session_a", "session_b")

	// Empty store (no wire files) → not found, no error (first-ever
	// kimi run proceeds optimistically).
	if v, found, err := SniffProtocolVersion(store); err != nil || found || v != "" {
		t.Fatalf("empty store sniff = %q,%v,%v", v, found, err)
	}

	write := func(session, version string, mtime time.Time) {
		wire := filepath.Join(store, "sessions", "wd_test_000000000001", session, "agents", "main")
		if err := os.MkdirAll(wire, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"metadata","protocol_version":"` + version + `","created_at":1}` + "\n"
		if err := os.WriteFile(filepath.Join(wire, "wire.jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(filepath.Join(wire, "wire.jsonl"), mtime, mtime)
	}
	write("session_a", "1.4", time.Now().Add(-time.Hour))
	write("session_b", "9", time.Now()) // newest wins

	v, found, err := SniffProtocolVersion(store)
	if err != nil || !found || v != "9" {
		t.Fatalf("sniff = %q,%v,%v; want 9,true,nil (newest file wins)", v, found, err)
	}
}

func TestReadAgentParents(t *testing.T) {
	dir := t.TempDir()
	state := `{"createdAt":"2026-07-23T00:00:00.000Z","agents":{
		"main":{"homedir":"/x/agents/main","type":"main","parentAgentId":null},
		"agent-9":{"homedir":"/x/agents/agent-9","type":"sub","parentAgentId":"main"}}}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	parents, err := ReadAgentParents(dir)
	if err != nil {
		t.Fatal(err)
	}
	if parents["main"] != "" || parents["agent-9"] != "main" {
		t.Fatalf("parents = %+v", parents)
	}

	if _, err := ReadAgentParents(t.TempDir()); !errors.Is(err, ErrNoState) {
		t.Fatalf("missing state.json err = %v, want ErrNoState", err)
	}
}

func TestListAgentWireFiles(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"main", "agent-0", "agent-9"} {
		wd := filepath.Join(dir, "agents", id)
		if err := os.MkdirAll(wd, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wd, "wire.jsonl"), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A dir WITHOUT a wire file must not be listed.
	if err := os.MkdirAll(filepath.Join(dir, "agents", "agent-empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ListAgentWireFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got["main"] == "" || got["agent-0"] == "" || got["agent-9"] == "" {
		t.Fatalf("wires = %+v", got)
	}
}
