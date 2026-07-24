package kimi_code

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// StoreHome returns the root of kimi's local session store:
// $KIMI_CODE_HOME when set, else ~/.kimi-code. Both kimi-code-ts
// (verified 0.28.1) and the directory layout in the plan's §2.2 use
// this root; the env override is kimi's own, honoured so per-spawn
// isolation and tests can point the adapter at a scratch store.
func StoreHome() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("KIMI_CODE_HOME")); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve HOME: %w", err)
	}
	return filepath.Join(home, ".kimi-code"), nil
}

// workspacesFile is the on-disk shape of <store>/workspaces.json
// (verified on kimi-code 0.28.1):
//
//	{"version":1, "workspaces":{"wd_wb_059d826285bc":{"root":"/Users/wb", ...}}, ...}
type workspacesFile struct {
	Workspaces map[string]struct {
		Root string `json:"root"`
	} `json:"workspaces"`
}

// LookupWorkspaceID maps a cwd to its wd_* id via workspaces.json.
// Returns ErrNoWorkspace when the file is missing or no entry matches
// (both mean "kimi hasn't opened this cwd yet — keep polling"); an
// unparseable file is a hard error on purpose, so the launch glue and
// the wait loop treat corruption as fatal rather than spinning on it.
// Matching is on the cleaned absolute path; a
// symlink-resolved retry covers macOS's /tmp ↔ /private/tmp split
// (kimi records both forms as separate entries — verified).
func LookupWorkspaceID(storeHome, cwd string) (string, error) {
	data, err := os.ReadFile(filepath.Join(storeHome, "workspaces.json"))
	if err != nil {
		if os.IsNotExist(err) {
			// kimi hasn't opened any workspace on this host yet (the
			// file appears at first workspace open) — the wait loop
			// keeps polling.
			return "", ErrNoWorkspace
		}
		return "", fmt.Errorf("read workspaces.json: %w", err)
	}
	var wf workspacesFile
	if err := json.Unmarshal(data, &wf); err != nil {
		return "", fmt.Errorf("parse workspaces.json: %w", err)
	}
	clean := filepath.Clean(cwd)
	for id, ws := range wf.Workspaces {
		if filepath.Clean(ws.Root) == clean {
			return id, nil
		}
	}
	// Symlink-resolved retry: a spawn cd'd into /tmp/... while kimi
	// recorded /private/tmp/... (or vice versa).
	if resolved, rerr := filepath.EvalSymlinks(clean); rerr == nil && resolved != clean {
		for id, ws := range wf.Workspaces {
			rootResolved, rerr := filepath.EvalSymlinks(filepath.Clean(ws.Root))
			if rerr == nil && rootResolved == resolved {
				return id, nil
			}
		}
	}
	return "", ErrNoWorkspace
}

// ErrNoWorkspace is returned when workspaces.json has no entry for the
// cwd. Sentinel so the session wait loop can distinguish "kimi hasn't
// opened this workspace yet" (keep polling — the entry appears when
// the spawned process starts) from hard filesystem errors.
var ErrNoWorkspace = errNoWorkspace{}

type errNoWorkspace struct{}

func (errNoWorkspace) Error() string { return "no kimi-code workspace entry for cwd" }

// ResolveLatestSessionSince returns the newest session_* directory
// under <store>/sessions/<wdID>/ whose mtime is strictly after
// `since`. The mtime cutoff is how a fresh spawn ignores sessions from
// earlier kimi runs in the same cwd (mirrors the claude adapter's
// ResolveLatestSince rationale): kimi creates the session dir within
// milliseconds of process start, so "created around spawn time" ⇔
// "mtime after adapter construction". A zero `since` disables the
// cutoff. ErrNoSession when nothing qualifies.
func ResolveLatestSessionSince(storeHome, wdID string, since time.Time) (string, error) {
	dir := filepath.Join(storeHome, "sessions", wdID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrNoSession
		}
		return "", err
	}
	var best string
	var bestT time.Time
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "session_") {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		if !since.IsZero() && !info.ModTime().After(since) {
			continue
		}
		if best == "" || info.ModTime().After(bestT) {
			best = filepath.Join(dir, e.Name())
			bestT = info.ModTime()
		}
	}
	if best == "" {
		return "", ErrNoSession
	}
	return best, nil
}

// ErrNoSession is returned when no qualifying session dir exists.
var ErrNoSession = errNoSession{}

type errNoSession struct{}

func (errNoSession) Error() string { return "no kimi-code session dir found" }

// WaitForSession polls until the session dir for `workdir` created
// after `since` appears, or ctx fires. Two things can lag the spawn:
// the workspaces.json entry (kimi writes it when it opens the cwd) and
// the session dir itself. pollEvery defaults to 250ms.
//
// Session-selection heuristic (documented per the P4 wedge): cwd →
// wd_* id via workspaces.json → newest session_* dir under that wd
// with mtime after the adapter's construction time. session_index.jsonl
// (a global append log of {sessionId, sessionDir, workDir}) was
// considered as the primary source but is only an optimisation — the
// per-wd directory listing answers the same question without parsing
// an ever-growing file — so it's not consulted.
func WaitForSession(ctx context.Context, storeHome, workdir string, pollEvery time.Duration, since time.Time) (string, error) {
	if pollEvery <= 0 {
		pollEvery = 250 * time.Millisecond
	}
	try := func() (string, bool, error) {
		wdID, err := LookupWorkspaceID(storeHome, workdir)
		if err != nil {
			if errors.Is(err, ErrNoWorkspace) {
				return "", false, nil
			}
			// A corrupt workspaces.json is a hard failure — waiting
			// won't fix it.
			return "", false, err
		}
		dir, err := ResolveLatestSessionSince(storeHome, wdID, since)
		if err != nil {
			if errors.Is(err, ErrNoSession) {
				return "", false, nil
			}
			return "", false, err
		}
		return dir, true, nil
	}
	if dir, ok, err := try(); ok || err != nil {
		return dir, err
	}
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("waiting for kimi-code session (workdir %s): %w",
				workdir, ctx.Err())
		case <-t.C:
			if dir, ok, err := try(); ok || err != nil {
				return dir, err
			}
		}
	}
}

// SniffProtocolVersion reads the metadata line of the NEWEST wire.jsonl
// anywhere under <store>/sessions and returns its protocol_version.
// The launch glue uses it for the spawn-time protocol gate: the wire
// protocol is a property of the kimi build installed on the host, so a
// prior session's version predicts what the new session will write.
// found=false means no wire files exist yet (first-ever kimi run —
// proceed optimistically; the adapter re-gates when the new session's
// own metadata lands). Candidates are capped at the 8 newest files so
// a long-lived store doesn't pay a full walk per spawn.
func SniffProtocolVersion(storeHome string) (version string, found bool, err error) {
	matches, err := filepath.Glob(filepath.Join(storeHome, "sessions", "*", "*", "agents", "*", "wire.jsonl"))
	if err != nil {
		return "", false, err
	}
	if len(matches) == 0 {
		return "", false, nil
	}
	type cand struct {
		path  string
		mtime time.Time
	}
	cands := make([]cand, 0, len(matches))
	for _, p := range matches {
		info, ierr := os.Stat(p)
		if ierr != nil {
			continue
		}
		cands = append(cands, cand{p, info.ModTime()})
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mtime.After(cands[j].mtime) })
	if len(cands) > 8 {
		cands = cands[:8]
	}
	for _, c := range cands {
		v, rerr := ReadWireProtocolVersion(c.path)
		if rerr == nil {
			return v, true, nil
		}
	}
	// Wire files exist but none yielded a version — treat as not found
	// (a partially-written first line shouldn't block the spawn; the
	// runtime gate catches a real mismatch).
	return "", false, nil
}

// ReadWireProtocolVersion opens a wire.jsonl and extracts
// protocol_version from its first line (the metadata event).
func ReadWireProtocolVersion(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	// The metadata line is small (~80 bytes observed); read a bounded
	// prefix and split off the first line instead of buffering the
	// whole (potentially multi-MB) file.
	buf := make([]byte, 64*1024)
	n, err := f.Read(buf)
	if n == 0 {
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("empty wire file %s", path)
	}
	line := buf[:n]
	if i := indexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	var md struct {
		Type            string `json:"type"`
		ProtocolVersion string `json:"protocol_version"`
	}
	if jerr := json.Unmarshal(line, &md); jerr != nil {
		return "", fmt.Errorf("parse first wire line of %s: %w", path, jerr)
	}
	if md.Type != "metadata" {
		return "", fmt.Errorf("first wire line of %s is %q, want metadata", path, md.Type)
	}
	return md.ProtocolVersion, nil
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// ReadAgentParents parses <sessionDir>/state.json and returns
// agentID → parentAgentID ("" for main / no parent). Verified shape:
//
//	{"agents": {"main":  {"type":"main", "parentAgentId": null, ...},
//	            "agent-9":{"type":"sub",  "parentAgentId": "main", ...}}}
//
// Missing file → ErrNoState (the caller retries — state.json lags the
// creation of a subagent's wire dir by a beat).
func ReadAgentParents(sessionDir string) (map[string]string, error) {
	data, err := os.ReadFile(filepath.Join(sessionDir, "state.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoState
		}
		return nil, err
	}
	var st struct {
		Agents map[string]struct {
			ParentAgentID *string `json:"parentAgentId"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse state.json: %w", err)
	}
	out := make(map[string]string, len(st.Agents))
	for id, a := range st.Agents {
		if a.ParentAgentID != nil {
			out[id] = *a.ParentAgentID
		} else {
			out[id] = ""
		}
	}
	return out, nil
}

// ErrNoState is returned when state.json doesn't exist yet.
var ErrNoState = errNoState{}

type errNoState struct{}

func (errNoState) Error() string { return "no kimi-code state.json yet" }

// ListAgentWireFiles scans <sessionDir>/agents/*/wire.jsonl and returns
// agentID → absolute wire path ("main" included when present).
func ListAgentWireFiles(sessionDir string) (map[string]string, error) {
	agentsDir := filepath.Join(sessionDir, "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	out := map[string]string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		wire := filepath.Join(agentsDir, e.Name(), "wire.jsonl")
		if _, serr := os.Stat(wire); serr == nil {
			out[e.Name()] = wire
		}
	}
	return out, nil
}
