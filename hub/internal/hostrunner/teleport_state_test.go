package hostrunner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	claudecode "github.com/termipod/hub/internal/drivers/local_log_tail/claude_code"
	kimicode "github.com/termipod/hub/internal/drivers/local_log_tail/kimi_code"
)

// writeClaudeSession creates a claude-code session JSONL at the resolver-derived
// path for (home, workdir, sessionID) and returns its absolute path.
func writeClaudeSession(t *testing.T, home, workdir, sessionID, content string) string {
	t.Helper()
	dir := claudecode.ProjectDirFor(home, workdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, sessionID+".jsonl")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPackRestoreEngineState_ClaudeRemapsAcrossHosts(t *testing.T) {
	const sessionID = "11111111-2222-3333-4444-555555555555"
	const content = `{"type":"user","text":"hi"}` + "\n" + `{"type":"assistant","text":"hello"}` + "\n"

	// Source host: home + workdir A.
	srcHome := t.TempDir()
	srcWorkdir := "/home/ubuntu/proj-a/wt-worker-1"
	writeClaudeSession(t, srcHome, srcWorkdir, sessionID, content)

	bundle, err := packEngineState("claude-code", claudecode.ConfigHomeFor(srcHome), srcWorkdir, sessionID)
	if err != nil {
		t.Fatalf("packEngineState: %v", err)
	}
	if len(bundle) == 0 {
		t.Fatal("empty bundle")
	}

	// Target host: a DIFFERENT home + workdir. Restore must place the file at
	// the target's resolver-derived path (different slug), not the source's.
	tgtHome := t.TempDir()
	tgtWorkdir := "/data/agents/proj-a/wt-worker-1" // different cwd → different slug
	if err := restoreEngineState("claude-code", claudecode.ConfigHomeFor(tgtHome), tgtWorkdir, sessionID, bundle); err != nil {
		t.Fatalf("restoreEngineState: %v", err)
	}

	want := filepath.Join(claudecode.ProjectDirFor(tgtHome, tgtWorkdir), sessionID+".jsonl")
	got, rerr := os.ReadFile(want)
	if rerr != nil {
		t.Fatalf("restored file not at expected target path %s: %v", want, rerr)
	}
	if string(got) != content {
		t.Fatalf("content mismatch after teleport:\n got %q\nwant %q", got, content)
	}
	// And it must NOT have leaked into a source-slug path under the target home.
	leak := filepath.Join(claudecode.ProjectDirFor(tgtHome, srcWorkdir), sessionID+".jsonl")
	if _, err := os.Stat(leak); err == nil {
		t.Fatalf("engine state leaked to source-slug path %s", leak)
	}
}

func TestPackEngineState_MissingSessionFails(t *testing.T) {
	home := t.TempDir()
	if _, err := packEngineState("claude-code", claudecode.ConfigHomeFor(home), "/w/d", "no-such-session"); err == nil {
		t.Fatal("expected error for missing session id")
	}
	// A real id whose file doesn't exist also fails.
	if _, err := packEngineState("claude-code", claudecode.ConfigHomeFor(home), "/w/d", "deadbeef"); err == nil {
		t.Fatal("expected error for missing session file")
	}
}

func TestEngineState_UnsupportedEngine(t *testing.T) {
	if _, err := packEngineState("codex", t.TempDir(), "/w/d", "s"); err == nil {
		t.Fatal("expected unsupported-engine error on pack")
	}
	// Restore dispatches per tar entry — a VALID bundle is needed to reach
	// the engine switch (a nil bundle fails earlier, in the gzip reader).
	home := t.TempDir()
	writeClaudeSession(t, home, "/w/d", "s", "x\n")
	bundle, err := packEngineState("claude-code", claudecode.ConfigHomeFor(home), "/w/d", "s")
	if err != nil {
		t.Fatal(err)
	}
	if err := restoreEngineState("codex", t.TempDir(), "/w/d", "s", bundle); err == nil {
		t.Fatal("expected unsupported-engine error on restore")
	}
}

func TestRestoreEngineState_OverwritesExisting(t *testing.T) {
	const sessionID = "aaaa"
	srcHome := t.TempDir()
	srcWorkdir := "/src/wt"
	writeClaudeSession(t, srcHome, srcWorkdir, sessionID, "new\n")
	bundle, err := packEngineState("claude-code", claudecode.ConfigHomeFor(srcHome), srcWorkdir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	tgtHome := t.TempDir()
	tgtWorkdir := "/tgt/wt"
	// Pre-existing stale file at the target path.
	writeClaudeSession(t, tgtHome, tgtWorkdir, sessionID, "stale\n")
	if err := restoreEngineState("claude-code", claudecode.ConfigHomeFor(tgtHome), tgtWorkdir, sessionID, bundle); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(claudecode.ProjectDirFor(tgtHome, tgtWorkdir), sessionID+".jsonl"))
	if string(got) != "new\n" {
		t.Fatalf("expected overwrite, got %q", got)
	}
}

// writeKimiSession seeds a kimi-code session tree under the store for (home,
// workdir): workspaces.json entry (id derived as kimi would) + the session
// dir with state.json, two agent wire logs and a nested log file. Returns
// (storeHome, wdID, sessionDir).
func writeKimiSession(t *testing.T, home, workdir, sessionID string) (string, string, string) {
	t.Helper()
	store := kimicode.StoreHomeFor(home)
	root := kimicode.ResolveWorkdirRoot(workdir)
	wdID := kimicode.WorkspaceIDFor(root)
	ws := `{"version":1,"workspaces":{` + jsonQuote(wdID) + `:{"root":` + jsonQuote(root) +
		`,"name":"x","created_at":"2026-07-28T00:00:00.000Z","last_opened_at":"2026-07-28T00:00:00.000Z"}},"deleted_workspace_ids":[]}`
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "workspaces.json"), []byte(ws), 0o600); err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(store, "sessions", wdID, sessionID)
	state := `{"createdAt":"2026-07-28T00:00:00.000Z","workDir":` + jsonQuote(root) + `,` +
		`"agents":{"main":{"homedir":` + jsonQuote(filepath.Join(sessionDir, "agents", "main")) + `,"type":"main","parentAgentId":null},` +
		`"agent-1":{"homedir":` + jsonQuote(filepath.Join(sessionDir, "agents", "agent-1")) + `,"type":"sub","parentAgentId":"main"}},"lastPrompt":"hi"}`
	files := map[string]string{
		"state.json":                state,
		"agents/main/wire.jsonl":    `{"type":"metadata","protocol_version":"1.0"}` + "\n",
		"agents/agent-1/wire.jsonl": `{"type":"metadata","protocol_version":"1.0"}` + "\n",
		"logs/nested/kimi-code.log": "log line\n",
	}
	for rel, body := range files {
		p := filepath.Join(sessionDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return store, wdID, sessionDir
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestPackRestoreEngineState_KimiRemapsAcrossHosts is the kimi mirror of the
// claude remap test, exercising the parts that make kimi's store unique: a
// whole session TREE (multi-file, nested), the cwd→wd_* remap, the state.json
// workDir/homedir rewrite kimi's resume validation needs, and the
// workspaces.json synthesis the M4 adapter's LookupWorkspaceID polls.
func TestPackRestoreEngineState_KimiRemapsAcrossHosts(t *testing.T) {
	const sessionID = "session_11111111-2222-3333-4444-555555555555"

	// Source host: real dirs under a temp home (also exercises the
	// symlink-resolved wd_* path — t.TempDir() lives under /var on macOS).
	srcHome := t.TempDir()
	srcWorkdir := filepath.Join(srcHome, "hub-work", "proj", "wt-1")
	if err := os.MkdirAll(srcWorkdir, 0o755); err != nil {
		t.Fatal(err)
	}
	srcStore, srcWdID, _ := writeKimiSession(t, srcHome, srcWorkdir, sessionID)

	bundle, err := packEngineState("kimi-code-ts", kimicode.StoreHomeFor(srcHome), srcWorkdir, sessionID)
	if err != nil {
		t.Fatalf("packEngineState: %v", err)
	}
	if len(bundle) == 0 {
		t.Fatal("empty bundle")
	}

	// Target host: a DIFFERENT home + workdir → a DIFFERENT wd_*.
	tgtHome := t.TempDir()
	tgtWorkdir := filepath.Join(tgtHome, "agents", "proj", "wt-1")
	if err := os.MkdirAll(tgtWorkdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := restoreEngineState("kimi-code-ts", kimicode.StoreHomeFor(tgtHome), tgtWorkdir, sessionID, bundle); err != nil {
		t.Fatalf("restoreEngineState: %v", err)
	}

	tgtStore := kimicode.StoreHomeFor(tgtHome)
	tgtRoot := kimicode.ResolveWorkdirRoot(tgtWorkdir)
	tgtWdID := kimicode.WorkspaceIDFor(tgtRoot)
	if tgtWdID == srcWdID {
		t.Fatalf("test setup: source and target wd_* collide (%s) — the remap is untested", tgtWdID)
	}
	tgtSessionDir := filepath.Join(tgtStore, "sessions", tgtWdID, sessionID)

	// 1) The whole tree landed at the target-wd_* path, content intact.
	for rel, want := range map[string]string{
		"agents/main/wire.jsonl":    `{"type":"metadata","protocol_version":"1.0"}` + "\n",
		"agents/agent-1/wire.jsonl": `{"type":"metadata","protocol_version":"1.0"}` + "\n",
		"logs/nested/kimi-code.log": "log line\n",
	} {
		got, rerr := os.ReadFile(filepath.Join(tgtSessionDir, filepath.FromSlash(rel)))
		if rerr != nil {
			t.Fatalf("restored %s missing: %v", rel, rerr)
		}
		if string(got) != want {
			t.Fatalf("restored %s content = %q, want %q", rel, got, want)
		}
	}
	// …and nothing leaked into the SOURCE wd_* path on the target.
	if _, serr := os.Stat(filepath.Join(tgtStore, "sessions", srcWdID)); serr == nil {
		t.Fatalf("engine state leaked into source wd_* dir under target store")
	}

	// 2) state.json was rewritten for the target: workDir is the target's
	// resolved workdir, agent homedirs point inside the target tree (kimi's
	// resume validation rejects a session "created under a different
	// directory" — verified 0.28.1).
	stRaw, rerr := os.ReadFile(filepath.Join(tgtSessionDir, "state.json"))
	if rerr != nil {
		t.Fatalf("state.json not restored: %v", rerr)
	}
	var st struct {
		WorkDir string `json:"workDir"`
		Agents  map[string]struct {
			Homedir string `json:"homedir"`
		} `json:"agents"`
	}
	if jerr := json.Unmarshal(stRaw, &st); jerr != nil {
		t.Fatalf("parse restored state.json: %v", jerr)
	}
	if st.WorkDir != tgtRoot {
		t.Fatalf("state.json workDir = %q, want target root %q", st.WorkDir, tgtRoot)
	}
	for id, a := range st.Agents {
		want := filepath.Join(tgtSessionDir, "agents", id)
		if a.Homedir != want {
			t.Fatalf("state.json agents.%s.homedir = %q, want %q", id, a.Homedir, want)
		}
	}

	// 3) The target store's workspaces.json got the synthesized entry, and
	// the adapter's LookupWorkspaceID now resolves the target workdir.
	gotWd, lerr := kimicode.LookupWorkspaceID(tgtStore, tgtWorkdir)
	if lerr != nil {
		t.Fatalf("LookupWorkspaceID on target after restore: %v", lerr)
	}
	if gotWd != tgtWdID {
		t.Fatalf("LookupWorkspaceID = %q, want %q", gotWd, tgtWdID)
	}

	// 4) session_index.jsonl carries the target-correct line.
	idx, ierr := os.ReadFile(filepath.Join(tgtStore, "session_index.jsonl"))
	if ierr != nil {
		t.Fatalf("session_index.jsonl not created: %v", ierr)
	}
	if !strings.Contains(string(idx), `"sessionId":"`+sessionID+`"`) ||
		!strings.Contains(string(idx), tgtSessionDir) {
		t.Fatalf("session_index.jsonl missing target line: %s", idx)
	}

	// The SOURCE store must be untouched by the restore (its state.json
	// still carries the source workDir).
	if _, serr := os.Stat(filepath.Join(srcStore, "sessions", srcWdID, sessionID, "state.json")); serr != nil {
		t.Fatalf("source session vanished: %v", serr)
	}
}

func TestPackEngineState_KimiMissingSessionFails(t *testing.T) {
	home := t.TempDir()
	workdir := filepath.Join(home, "wd")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := packEngineState("kimi-code-ts", kimicode.StoreHomeFor(home), workdir, ""); err == nil {
		t.Fatal("expected error for empty session id")
	}
	// A real-looking id whose tree doesn't exist also fails (no silent
	// cold-start on the target).
	if _, err := packEngineState("kimi-code-ts", kimicode.StoreHomeFor(home), workdir, "session_deadbeef-dead-beef-beef-deadbeefdead"); err == nil {
		t.Fatal("expected error for missing kimi session dir")
	}
}

// TestKimiRestore_RejectsForeignEntry guards the tarName shape check: entries
// not under <sessionID>/ (or traversal attempts) are refused.
func TestKimiRestore_RejectsForeignEntry(t *testing.T) {
	if _, err := engineStateTargetPath("kimi-code-ts", kimicode.StoreHomeFor(t.TempDir()), "/w/d", "session_a", "session_b/state.json"); err == nil {
		t.Fatal("expected error for foreign session prefix")
	}
	if _, err := engineStateTargetPath("kimi-code-ts", kimicode.StoreHomeFor(t.TempDir()), "/w/d", "session_a", "session_a/../../etc/passwd"); err == nil {
		t.Fatal("expected error for traversal")
	}
}

// TestKimiSessionIndexAppend_IsIdempotent pins the re-teleport dedupe: a
// second append of the same session must not duplicate the index line, and a
// line kimi itself wrote (its own field order) must be recognised too. The
// original implementation deduped by substring in ONE field order and so
// double-appended on every re-teleport of a session it had indexed itself.
func TestKimiSessionIndexAppend_IsIdempotent(t *testing.T) {
	store := t.TempDir()
	indexPath := filepath.Join(store, "session_index.jsonl")

	// A pre-existing kimi-written line (kimi's field order) is respected.
	kimiLine := `{"sessionId":"session_pre","sessionDir":"/s/pre","workDir":"/w"}` + "\n"
	if err := os.WriteFile(indexPath, []byte(kimiLine), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := appendKimiSessionIndex(store, "session_pre", "/s/pre", "/w"); err != nil {
		t.Fatal(err)
	}

	// Our own line survives a re-append (the re-teleport case).
	if err := appendKimiSessionIndex(store, "session_new", "/s/new", "/w"); err != nil {
		t.Fatal(err)
	}
	if err := appendKimiSessionIndex(store, "session_new", "/s/new", "/w"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"session_pre", "session_new"} {
		if n := strings.Count(string(data), `"sessionId":"`+id+`"`); n != 1 {
			t.Fatalf("index has %d lines for %s, want exactly 1:\n%s", n, id, data)
		}
	}
	// The line we write is shaped like kimi's own (sessionId first), so any
	// OTHER reader matching kimi's order also finds ours.
	if !strings.Contains(string(data), `{"sessionId":"session_new","sessionDir":"/s/new","workDir":"/w"}`) {
		t.Fatalf("appended line not in kimi's field order:\n%s", data)
	}
}

// resolveEngineRoot is the whole point of the 2026-08-06 change: each side of
// a teleport resolves its OWN root, in the same order the engine's launcher
// does. An unsupported engine must error rather than guess — a root guessed
// for an engine we don't model would pack the wrong bytes and report success.
func TestResolveEngineRoot_PrecedencePerEngine(t *testing.T) {
	t.Setenv(claudecode.ConfigHomeEnvVar, "/host/claude")
	t.Setenv(kimicode.StoreHomeEnvVar, "/host/kimi")

	cases := []struct {
		engine, override, want string
	}{
		{"claude-code", "/agent/claude", "/agent/claude"}, // agent override wins
		{"claude-code", "", "/host/claude"},               // then this host's env
		{"kimi-code-ts", "/agent/kimi", "/agent/kimi"},
		{"kimi-code-ts", "", "/host/kimi"},
	}
	for _, c := range cases {
		got, err := resolveEngineRoot(c.engine, c.override, "/home/frank")
		if err != nil || got != c.want {
			t.Errorf("resolveEngineRoot(%q, %q) = %q, %v; want %q", c.engine, c.override, got, err, c.want)
		}
	}

	t.Setenv(claudecode.ConfigHomeEnvVar, "")
	t.Setenv(kimicode.StoreHomeEnvVar, "")
	for engine, want := range map[string]string{
		"claude-code":  "/home/frank/.claude",
		"kimi-code-ts": "/home/frank/.kimi-code",
	} {
		got, err := resolveEngineRoot(engine, "", "/home/frank")
		if err != nil || got != want {
			t.Errorf("resolveEngineRoot(%q, \"\") = %q, %v; want %q", engine, got, err, want)
		}
	}

	if _, err := resolveEngineRoot("codex", "", "/home/frank"); err == nil {
		t.Error("an engine we do not model must refuse, not default to a guessed root")
	}
}

// The hub relays the agent's whole plain env_vars map; host-runner reads only
// the key its engine uses. Keeping the engine→variable mapping here rather
// than in the hub is the same division the spawn path uses.
func TestEngineRootFromEnvVars_ReadsOnlyItsOwnKey(t *testing.T) {
	env := map[string]string{
		"CLAUDE_CONFIG_DIR": "/c",
		"KIMI_CODE_HOME":    "/k",
		"PATH":              "/usr/bin",
	}
	if got := engineRootFromEnvVars("claude-code", env); got != "/c" {
		t.Errorf("claude-code = %q, want /c", got)
	}
	if got := engineRootFromEnvVars("kimi-code-ts", env); got != "/k" {
		t.Errorf("kimi-code-ts = %q, want /k", got)
	}
	if got := engineRootFromEnvVars("codex", env); got != "" {
		t.Errorf("unmodelled engine = %q, want empty", got)
	}
	if got := engineRootFromEnvVars("claude-code", nil); got != "" {
		t.Errorf("nil env (an older hub) = %q, want empty so this host's env decides", got)
	}
}

// The property that makes this a plumbing change and not a transport change:
// the two hosts need not share a layout. The source keeps its store at one
// relocated root, the target at a differently-shaped one, and the bundle —
// whose TarNames are host-independent — carries nothing about either.
func TestPackRestoreEngineState_ClaudeAcrossDIFFERENTRelocatedRoots(t *testing.T) {
	const sessionID = "99999999-8888-7777-6666-555555555555"
	const content = `{"type":"assistant","text":"survives the move"}` + "\n"

	srcHome := t.TempDir()
	srcRoot := filepath.Join(srcHome, ".claude-work") // ~/.claude-work
	srcWorkdir := "/home/ubuntu/proj-a/wt-1"
	dir := claudecode.ProjectDirIn(srcRoot, srcWorkdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionID+".jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	bundle, err := packEngineState("claude-code", srcRoot, srcWorkdir, sessionID)
	if err != nil {
		t.Fatalf("packEngineState from a relocated source root: %v", err)
	}

	tgtHome := t.TempDir()
	tgtRoot := filepath.Join(tgtHome, "profiles", "work") // deliberately NOT ~/.claude-work
	tgtWorkdir := "/srv/agents/proj-a/wt-1"
	if err := restoreEngineState("claude-code", tgtRoot, tgtWorkdir, sessionID, bundle); err != nil {
		t.Fatalf("restoreEngineState into a relocated target root: %v", err)
	}

	landed := filepath.Join(claudecode.ProjectDirIn(tgtRoot, tgtWorkdir), sessionID+".jsonl")
	got, rerr := os.ReadFile(landed)
	if rerr != nil {
		t.Fatalf("engine state not at the target's own root %s: %v", landed, rerr)
	}
	if string(got) != content {
		t.Errorf("content mismatch: got %q, want %q", got, content)
	}

	// It must not have landed in the target's DEFAULT root — that is another
	// profile's directory, and on a multi-account host another account's.
	stray := filepath.Join(claudecode.ProjectDirFor(tgtHome, tgtWorkdir), sessionID+".jsonl")
	if _, err := os.Stat(stray); err == nil {
		t.Errorf("engine state written to the default root %s; a relocated session must stay in its own profile", stray)
	}
}
