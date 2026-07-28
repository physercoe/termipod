package hostrunner

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"

	claudecode "github.com/termipod/hub/internal/drivers/local_log_tail/claude_code"
)

// Engine-state snapshot/restore for session teleport (ADR-057 D-2/D-4). Beyond
// the git worktree, a session's other host-owned byte-store is the engine's own
// on-disk session directory (claude's per-cwd project JSONL, kimi's session
// tree, …). Teleport tars those files into a host-independent bundle on the
// source and restores them at the equivalent path on the target.
//
// D-4: the paths are NOT declared as agent_families YAML globs — the resolver
// logic (claude's cwd→slug encoding, kimi's workspaces.json lookup) already
// lives in drivers/local_log_tail/*/pathresolver.go and would only diverge if
// duplicated. This file reuses those resolvers so the same authority computes
// both the source files to pack and the target paths to restore, making the
// cwd→slug / cwd→wdID remap automatic. T1a covers claude-code; kimi-code-ts and
// the remaining families slot in behind the same two functions.

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
	return "teleport: engine-state snapshot not supported for " + e.engine + " (T1: claude-code only)"
}

// engineStateEntries returns the on-disk files making up `engine`'s session
// state for (home, workdir, sessionID). TarName is host-independent so the
// target can re-derive its own AbsPath via engineStateTargetPath.
func engineStateEntries(engine, home, workdir, sessionID string) ([]engineStateFile, error) {
	switch engine {
	case "claude-code":
		if sessionID == "" {
			return nil, fmt.Errorf("teleport: claude-code needs an engine session id to snapshot")
		}
		jsonl := filepath.Join(claudecode.ProjectDirFor(home, workdir), sessionID+".jsonl")
		return []engineStateFile{{TarName: "session.jsonl", AbsPath: jsonl}}, nil
	default:
		return nil, errUnsupportedTeleportEngine{engine}
	}
}

// engineStateTargetPath is the inverse: where a bundle entry named tarName must
// be written on THIS host for (home, workdir, sessionID). Because it re-derives
// from the target's own home+workdir, the claude cwd→slug remap happens for
// free.
func engineStateTargetPath(engine, home, workdir, sessionID, tarName string) (string, error) {
	switch engine {
	case "claude-code":
		if tarName != "session.jsonl" {
			return "", fmt.Errorf("teleport: unexpected claude-code bundle entry %q", tarName)
		}
		return filepath.Join(claudecode.ProjectDirFor(home, workdir), sessionID+".jsonl"), nil
	default:
		return "", errUnsupportedTeleportEngine{engine}
	}
}

// packEngineState snapshots the engine's session state into a gzip-compressed
// tar. A missing source file is an error — teleport must not silently move an
// empty engine state (the target would cold-start, losing the conversation the
// user expects to continue). Engine JSONL compresses heavily, so gzip keeps the
// bundle (and thus the number of transport chunks) small.
func packEngineState(engine, home, workdir, sessionID string) ([]byte, error) {
	entries, err := engineStateEntries(engine, home, workdir, sessionID)
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
func restoreEngineState(engine, home, workdir, sessionID string, bundle []byte) error {
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
		dst, perr := engineStateTargetPath(engine, home, workdir, sessionID, hdr.Name)
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
	return nil
}
