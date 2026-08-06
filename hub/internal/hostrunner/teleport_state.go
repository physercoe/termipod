package hostrunner

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	claudecode "github.com/termipod/hub/internal/drivers/local_log_tail/claude_code"
	kimicode "github.com/termipod/hub/internal/drivers/local_log_tail/kimi_code"
)

// Engine-state snapshot/restore for session teleport (ADR-057 D-2/D-4). Beyond
// the git worktree, a session's other host-owned byte-store is the engine's own
// on-disk session directory (claude's per-cwd project JSONL, kimi's session
// tree, …). Teleport tars those files into a host-independent bundle on the
// source and restores them at the equivalent path on the target.
//
// D-4: the paths are NOT declared as agent_families YAML globs — the resolver
// logic (claude's cwd→slug encoding, kimi's workspaces.json lookup + wd_*
// derivation) already lives in drivers/local_log_tail/*/pathresolver.go and
// would only diverge if duplicated. This file reuses those resolvers so the
// same authority computes both the source files to pack and the target paths
// to restore, making the cwd→slug / cwd→wdID remap automatic. T1a covered
// claude-code; T2b (#429) added kimi-code-ts (tree walk + state.json fixup +
// workspaces.json synthesis); the remaining families slot in behind the same
// two functions.
//
// Engine store roots are relocatable by env var ($CLAUDE_CONFIG_DIR,
// $KIMI_CODE_HOME) and teleport follows them, per engine, on both ends —
// see resolveEngineRoot. Each side resolves its OWN root and the bundle
// stays host-independent, so nothing about the source's layout travels: a
// root at /srv/profiles/work on the source and ~/.claude-work on the target
// is a normal move, not a special case.
//
// An earlier revision of this comment claimed following the var required
// carrying the source root in the bundle — an ADR-057 transport change.
// That was wrong, and worth recording as a class: the bundle already
// speaks host-independent TarNames precisely so each end can re-derive its
// own absolute paths, which is the same mechanism a relocated root needs.
// The work was plumbing an override down to two existing resolvers.

// engineStateFile is one file in an engine-state bundle: TarName is the
// host-independent name stored inside the tar, AbsPath is where it lives (pack)
// or must be written (restore) on THIS host.
type engineStateFile struct {
	TarName string
	AbsPath string
}

// errUnsupportedTeleportEngine marks an engine whose state snapshot isn't
// implemented yet — the orchestrator turns it into a clean teleport refusal
// rather than a half-move.
type errUnsupportedTeleportEngine struct{ engine string }

func (e errUnsupportedTeleportEngine) Error() string {
	return "teleport: engine-state snapshot not supported for " + e.engine + " (covered: claude-code, kimi-code-ts)"
}

// resolveEngineRoot picks the on-disk root of `engine`'s session store on
// THIS host, in the same order the engine's own launcher resolves it:
//
//  1. `override` — the agent's env-profile value for the engine's root
//     variable, relayed in the pack/unpack args. Per-agent, so neither
//     host's environment can be asked for it.
//  2. this host-runner's own environment.
//  3. `<home>/<engine default>`.
//
// The engine→variable mapping lives here rather than in the hub: the hub
// relays the agent's plain env_vars and stays out of engine specifics, the
// same division the spawn path already uses.
//
// An unsupported engine is the caller's existing refusal, not a silent
// default — a root guessed for an engine we don't model would pack the
// wrong bytes.
func resolveEngineRoot(engine, override, home string) (string, error) {
	switch engine {
	case "claude-code":
		return claudecode.ResolveConfigHome(override, home), nil
	case "kimi-code-ts":
		return kimicode.ResolveStoreHomeFor(override, home), nil
	default:
		return "", errUnsupportedTeleportEngine{engine}
	}
}

// engineRootFromEnvVars extracts the engine's root override from the plain
// env_vars the hub relayed. Missing/blank → "", which resolveEngineRoot
// treats as "ask this host" — so a hub that sends nothing (or an older hub
// that doesn't know the field) degrades to the previous behaviour rather
// than to a wrong path.
func engineRootFromEnvVars(engine string, envVars map[string]string) string {
	switch engine {
	case "claude-code":
		return envVars[claudecode.ConfigHomeEnvVar]
	case "kimi-code-ts":
		return envVars[kimicode.StoreHomeEnvVar]
	default:
		return ""
	}
}

// engineStateEntries returns the on-disk files making up `engine`'s session
// state for (engineRoot, workdir, sessionID). TarName is host-independent so
// the target can re-derive its own AbsPath via engineStateTargetPath.
// engineRoot comes from resolveEngineRoot — NOT a host home; the two differ
// whenever the agent's root is relocated.
func engineStateEntries(engine, engineRoot, workdir, sessionID string) ([]engineStateFile, error) {
	switch engine {
	case "claude-code":
		if sessionID == "" {
			return nil, fmt.Errorf("teleport: claude-code needs an engine session id to snapshot")
		}
		jsonl := filepath.Join(claudecode.ProjectDirIn(engineRoot, workdir), sessionID+".jsonl")
		return []engineStateFile{{TarName: "session.jsonl", AbsPath: jsonl}}, nil
	case "kimi-code-ts":
		if sessionID == "" {
			return nil, fmt.Errorf("teleport: kimi-code-ts needs an engine session id to snapshot")
		}
		_, sessionDir, err := kimiSessionDirFor(engineRoot, workdir, sessionID)
		if err != nil {
			return nil, err
		}
		// kimi's session state is a whole TREE (state.json +
		// agents/<id>/wire.jsonl + logs/…), not claude's single JSONL.
		// Walk it to a flat file list with host-independent subpath
		// TarNames ("<sessionID>/<rel>") — packEngineState's
		// regular-files-only contract holds per entry, and the target
		// re-anchors the tree under ITS OWN wd_* via engineStateTargetPath.
		var entries []engineStateFile
		werr := filepath.WalkDir(sessionDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !d.Type().IsRegular() {
				return nil // directories are recreated on restore; symlinks skipped
			}
			rel, rerr := filepath.Rel(sessionDir, p)
			if rerr != nil {
				return rerr
			}
			entries = append(entries, engineStateFile{
				TarName: sessionID + "/" + filepath.ToSlash(rel),
				AbsPath: p,
			})
			return nil
		})
		if werr != nil {
			return nil, fmt.Errorf("teleport: walk kimi-code-ts session dir: %w", werr)
		}
		if len(entries) == 0 {
			// Same invariant as claude's missing JSONL: never teleport an
			// empty engine state — the target would cold-start and the
			// user loses the conversation they expected to continue.
			return nil, fmt.Errorf("teleport: kimi-code-ts session dir %s is empty or missing", sessionDir)
		}
		return entries, nil
	default:
		return nil, errUnsupportedTeleportEngine{engine}
	}
}

// kimiSessionDirFor resolves where kimi's on-disk session tree lives for
// (store, workdir, sessionID): <store>/sessions/<wd_*>/<sessionID>. `store`
// is the resolved engine root (resolveEngineRoot), which is <home>/.kimi-code
// only when $KIMI_CODE_HOME and the agent's profile both leave it alone. The wd_*
// id comes from workspaces.json when kimi has opened this workdir on this
// host (authoritative — covers any historical id-scheme drift), else from
// kimi's deterministic id algorithm over the symlink-resolved workdir
// (verified on-host against kimi-code 0.28.1 — see WorkspaceIDFor). The
// same helper answers both sides of a teleport: on the source it finds the
// tree to pack; on the target it computes where the tree must land so the
// respawned kimi — which re-derives the SAME id from its cwd — finds it.
func kimiSessionDirFor(store, workdir, sessionID string) (wdID, sessionDir string, err error) {
	wdID, err = kimicode.LookupWorkspaceID(store, workdir)
	if err != nil {
		if !errors.Is(err, kimicode.ErrNoWorkspace) {
			return "", "", fmt.Errorf("teleport: resolve kimi workspace: %w", err)
		}
		// kimi never opened this workdir here (the common TARGET case):
		// derive the id exactly as kimi will at launch.
		wdID = kimicode.WorkspaceIDFor(kimicode.ResolveWorkdirRoot(workdir))
	}
	return wdID, filepath.Join(store, "sessions", wdID, sessionID), nil
}

// engineStateTargetPath is the inverse: where a bundle entry named tarName must
// be written on THIS host for (home, workdir, sessionID). Because it re-derives
// from the target's own home+workdir, the claude cwd→slug remap happens for
// free.
func engineStateTargetPath(engine, engineRoot, workdir, sessionID, tarName string) (string, error) {
	switch engine {
	case "claude-code":
		if tarName != "session.jsonl" {
			return "", fmt.Errorf("teleport: unexpected claude-code bundle entry %q", tarName)
		}
		return filepath.Join(claudecode.ProjectDirIn(engineRoot, workdir), sessionID+".jsonl"), nil
	case "kimi-code-ts":
		rel, ok := strings.CutPrefix(tarName, sessionID+"/")
		if !ok || rel == "" || rel == ".." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
			return "", fmt.Errorf("teleport: unexpected kimi-code-ts bundle entry %q", tarName)
		}
		_, sessionDir, err := kimiSessionDirFor(engineRoot, workdir, sessionID)
		if err != nil {
			return "", err
		}
		return filepath.Join(sessionDir, filepath.FromSlash(rel)), nil
	default:
		return "", errUnsupportedTeleportEngine{engine}
	}
}

// packEngineState snapshots the engine's session state into a gzip-compressed
// tar. A missing source file is an error — teleport must not silently move an
// empty engine state (the target would cold-start, losing the conversation the
// user expects to continue). Engine JSONL compresses heavily, so gzip keeps the
// bundle (and thus the number of transport chunks) small.
func packEngineState(engine, engineRoot, workdir, sessionID string) ([]byte, error) {
	entries, err := engineStateEntries(engine, engineRoot, workdir, sessionID)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		info, serr := os.Stat(e.AbsPath)
		if serr != nil {
			return nil, fmt.Errorf("teleport: engine-state file %s: %w", e.AbsPath, serr)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("teleport: engine-state entry %s is a directory (unsupported in T1)", e.AbsPath)
		}
		body, rerr := os.ReadFile(e.AbsPath)
		if rerr != nil {
			return nil, fmt.Errorf("teleport: read engine-state %s: %w", e.AbsPath, rerr)
		}
		hdr := &tar.Header{
			Name:    e.TarName,
			Mode:    0o600,
			Size:    int64(len(body)),
			ModTime: info.ModTime(),
		}
		if werr := tw.WriteHeader(hdr); werr != nil {
			return nil, fmt.Errorf("teleport: tar header %s: %w", e.TarName, werr)
		}
		if _, werr := tw.Write(body); werr != nil {
			return nil, fmt.Errorf("teleport: tar write %s: %w", e.TarName, werr)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// restoreEngineState untars a bundle produced by packEngineState onto THIS
// host, mapping each entry through engineStateTargetPath so the files land in
// the target's own engine store (remapped for the target home+workdir). Parent
// directories are created as needed. An existing file is overwritten — the
// bundle is authoritative for the session being teleported.
func restoreEngineState(engine, engineRoot, workdir, sessionID string, bundle []byte) error {
	gz, err := gzip.NewReader(bytes.NewReader(bundle))
	if err != nil {
		return fmt.Errorf("teleport: open engine-state gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, terr := tr.Next()
		if terr == io.EOF {
			break
		}
		if terr != nil {
			return fmt.Errorf("teleport: read engine-state tar: %w", terr)
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA { //nolint:staticcheck // TypeRegA for older tars
			continue
		}
		dst, perr := engineStateTargetPath(engine, engineRoot, workdir, sessionID, hdr.Name)
		if perr != nil {
			return perr
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return fmt.Errorf("teleport: mkdir %s: %w", filepath.Dir(dst), err)
		}
		// Read the entry fully (bounded by the tar header size) and write it.
		body := make([]byte, hdr.Size)
		if _, rerr := io.ReadFull(tr, body); rerr != nil {
			return fmt.Errorf("teleport: read entry %s: %w", hdr.Name, rerr)
		}
		if err := os.WriteFile(dst, body, 0o600); err != nil {
			return fmt.Errorf("teleport: write %s: %w", dst, err)
		}
	}
	if engine == "kimi-code-ts" {
		if err := finalizeKimiRestore(engineRoot, workdir, sessionID); err != nil {
			return err
		}
	}
	return nil
}

// finalizeKimiRestore makes a restored kimi session tree RESUMABLE from the
// target workdir. Verified on-host against kimi-code 0.28.1 (ticket #429):
// `kimi -r <id>` / ACP session/load resolves cwd → wd_* (computed, not via
// workspaces.json) → sessions/<wd_*>/<id>/state.json and REFUSES a session
// whose state.json workDir differs from the resolved cwd ("created under a
// different directory"). Three fixups:
//
//  1. state.json: workDir → the target's resolved workdir; agents.*.homedir →
//     the tree's new absolute location (both carried source-host paths).
//  2. workspaces.json: synthesize the wd_* → root entry when absent. Resume
//     doesn't consult it (verified), but the M4 wire-tail adapter's
//     WaitForSession polls LookupWorkspaceID, which does — without the entry
//     the target spawn's transcript never resolves.
//  3. session_index.jsonl: append the target-correct {sessionId, sessionDir,
//     workDir} line so kimi's interactive picker / vis / export stay coherent
//     (the resume path itself ignores the index — verified).
func finalizeKimiRestore(engineRoot, workdir, sessionID string) error {
	wdID, sessionDir, err := kimiSessionDirFor(engineRoot, workdir, sessionID)
	if err != nil {
		return err
	}
	root := kimicode.ResolveWorkdirRoot(workdir)
	if err := rewriteKimiStateJSON(sessionDir, root); err != nil {
		return err
	}
	if err := ensureKimiWorkspaceEntry(engineRoot, wdID, root); err != nil {
		return err
	}
	return appendKimiSessionIndex(engineRoot, sessionID, sessionDir, root)
}

// rewriteKimiStateJSON patches the restored state.json in place, preserving
// every field it doesn't own (map round-trip, not a struct).
func rewriteKimiStateJSON(sessionDir, root string) error {
	statePath := filepath.Join(sessionDir, "state.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		// Hard error like a missing pack source: without state.json kimi
		// cannot validate-or-resume the session — no silent cold-start.
		return fmt.Errorf("teleport: restored kimi state.json: %w", err)
	}
	var st map[string]any
	if err := json.Unmarshal(data, &st); err != nil {
		return fmt.Errorf("teleport: parse kimi state.json: %w", err)
	}
	st["workDir"] = root
	if agents, ok := st["agents"].(map[string]any); ok {
		for id, a := range agents {
			if m, ok := a.(map[string]any); ok {
				m["homedir"] = filepath.Join(sessionDir, "agents", id)
			}
		}
	}
	out, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("teleport: re-encode kimi state.json: %w", err)
	}
	if err := os.WriteFile(statePath, append(out, '\n'), 0o600); err != nil {
		return fmt.Errorf("teleport: write kimi state.json: %w", err)
	}
	return nil
}

// ensureKimiWorkspaceEntry adds the wd_* → root mapping to the target store's
// workspaces.json if kimi hasn't recorded it yet. The on-disk shape (verified
// 0.28.1):
//
//	{"version":1,
//	 "workspaces":{"<wdID>":{"root":…,"name":…,"created_at":…,"last_opened_at":…}},
//	 "deleted_workspace_ids":[]}
//
// An existing entry is kimi's own and is left untouched. Map round-trip so
// unrelated fields (deleted_workspace_ids, other workspaces) survive.
func ensureKimiWorkspaceEntry(store, wdID, root string) error {
	wsPath := filepath.Join(store, "workspaces.json")
	wf := map[string]any{}
	data, err := os.ReadFile(wsPath)
	switch {
	case err == nil:
		if jerr := json.Unmarshal(data, &wf); jerr != nil {
			return fmt.Errorf("teleport: parse kimi workspaces.json: %w", jerr)
		}
	case os.IsNotExist(err):
		wf["version"] = 1
		if _, ok := wf["deleted_workspace_ids"]; !ok {
			wf["deleted_workspace_ids"] = []any{}
		}
	default:
		return fmt.Errorf("teleport: read kimi workspaces.json: %w", err)
	}
	workspaces, ok := wf["workspaces"].(map[string]any)
	if !ok {
		workspaces = map[string]any{}
	}
	if _, exists := workspaces[wdID]; exists {
		return nil // kimi (or an earlier teleport) already recorded it
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	workspaces[wdID] = map[string]any{
		"root":           root,
		"name":           filepath.Base(root),
		"created_at":     now,
		"last_opened_at": now,
	}
	wf["workspaces"] = workspaces
	out, err := json.MarshalIndent(wf, "", "  ")
	if err != nil {
		return fmt.Errorf("teleport: re-encode kimi workspaces.json: %w", err)
	}
	if err := os.WriteFile(wsPath, append(out, '\n'), 0o600); err != nil {
		return fmt.Errorf("teleport: write kimi workspaces.json: %w", err)
	}
	return nil
}

// kimiIndexLine is one session_index.jsonl record. A struct (not a map) so
// Marshal emits kimi's own field order ({"sessionId","sessionDir","workDir"} —
// verified shape); a map would marshal alphabetically (sessionDir first) and
// write lines shaped unlike kimi's.
type kimiIndexLine struct {
	SessionID  string `json:"sessionId"`
	SessionDir string `json:"sessionDir"`
	WorkDir    string `json:"workDir"`
}

// appendKimiSessionIndex appends the target-correct line to the store's
// session_index.jsonl, skipping the append when a line with the same
// sessionId+sessionDir is already present (a re-teleport of the same session
// must not duplicate it). The dedupe parses each line rather than matching a
// substring: a substring needle would bake in one field order and silently
// miss lines written with another — including, before kimiIndexLine, this
// function's own.
func appendKimiSessionIndex(store, sessionID, sessionDir, root string) error {
	indexPath := filepath.Join(store, "session_index.jsonl")
	if data, err := os.ReadFile(indexPath); err == nil {
		for _, ln := range strings.Split(string(data), "\n") {
			var e kimiIndexLine
			if json.Unmarshal([]byte(ln), &e) == nil && e.SessionID == sessionID && e.SessionDir == sessionDir {
				return nil
			}
		}
	}
	line, err := json.Marshal(kimiIndexLine{
		SessionID:  sessionID,
		SessionDir: sessionDir,
		WorkDir:    root,
	})
	if err != nil {
		return fmt.Errorf("teleport: encode kimi session index line: %w", err)
	}
	f, err := os.OpenFile(indexPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("teleport: open kimi session_index.jsonl: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("teleport: append kimi session_index.jsonl: %w", err)
	}
	return nil
}
