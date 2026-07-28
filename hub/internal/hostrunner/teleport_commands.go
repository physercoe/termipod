package hostrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/termipod/hub/internal/handoff"
)

// The two host_commands kinds that carry a session teleport (ADR-057 D-2). The
// hub enqueues session_handoff_pack on the source and session_handoff_unpack on
// the target, polling result_json for the values the re-targeted resume needs.
// Each handler is a thin wrapper that resolves HOME + the hub blob store and
// delegates to a store-injectable core (runHandoffPack / runHandoffUnpack) so
// the whole round-trip is testable without a live hub.
const (
	CmdSessionHandoffPack   = "session_handoff_pack"
	CmdSessionHandoffUnpack = "session_handoff_unpack"
)

type handoffPackArgs struct {
	Engine          string `json:"engine"`
	WorktreePath    string `json:"worktree_path"`
	Branch          string `json:"branch"`
	Remote          string `json:"remote"`
	EngineSessionID string `json:"engine_session_id"`
}

type handoffPackResult struct {
	Branch       string `json:"branch"`
	HeadSHA      string `json:"head_sha"`
	Remote       string `json:"remote"`
	ManifestSHA  string `json:"manifest_sha"`
	WorktreePath string `json:"worktree_path"`
}

type handoffUnpackArgs struct {
	Engine          string `json:"engine"`
	Repo            string `json:"repo"`
	WorktreePath    string `json:"worktree_path"`
	Branch          string `json:"branch"`
	Remote          string `json:"remote"`
	ExpectHead      string `json:"expect_head"`
	EngineSessionID string `json:"engine_session_id"`
	ManifestSHA     string `json:"manifest_sha"`
}

type handoffUnpackResult struct {
	WorktreePath    string `json:"worktree_path"`
	EngineSessionID string `json:"engine_session_id"`
}

// hubBlobStore adapts the host-runner Client to handoff.BlobStore.
type hubBlobStore struct{ c *Client }

func (s hubBlobStore) Put(ctx context.Context, body []byte, mime string) (string, error) {
	out, err := s.c.UploadBlob(ctx, body, mime)
	if err != nil {
		return "", err
	}
	return out.SHA256, nil
}

func (s hubBlobStore) Get(ctx context.Context, sha string) ([]byte, error) {
	return s.c.DownloadBlob(ctx, sha)
}

// runHandoffPack is the SOURCE-side core: commit+push the worktree branch, tar
// the engine state, chunk it into blobs, and return the manifest handle + head
// SHA. Store-injectable for testing (the hub adapter in production, an in-memory
// store in tests). home is the engine-store home (os.UserHomeDir on a real host).
func runHandoffPack(ctx context.Context, store handoff.BlobStore, home string, args handoffPackArgs) (handoffPackResult, error) {
	if args.WorktreePath == "" || args.Engine == "" {
		return handoffPackResult{}, fmt.Errorf("teleport pack: engine and worktree_path are required")
	}
	head, branch, err := gitCommitAndPush(ctx, args.WorktreePath, args.Branch, args.Remote)
	if err != nil {
		return handoffPackResult{}, err
	}
	// For a worktree session the engine cwd IS the worktree path, so it is also
	// the workdir the resolver keys the engine store on.
	bundle, err := packEngineState(args.Engine, home, args.WorktreePath, args.EngineSessionID)
	if err != nil {
		return handoffPackResult{}, err
	}
	manifest, err := handoff.Pack(ctx, bytes.NewReader(bundle), store, handoff.DefaultChunkSize)
	if err != nil {
		return handoffPackResult{}, fmt.Errorf("teleport pack: chunk engine state: %w", err)
	}
	manifestSHA, err := handoff.PutManifest(ctx, manifest, store)
	if err != nil {
		return handoffPackResult{}, fmt.Errorf("teleport pack: store manifest: %w", err)
	}
	remote := args.Remote
	if remote == "" {
		remote = "origin"
	}
	return handoffPackResult{
		// The RESOLVED branch, not args.Branch: with an empty args.Branch the
		// worktree's current branch was used, and the unpack args need its name.
		Branch:       branch,
		HeadSHA:      head,
		Remote:       remote,
		ManifestSHA:  manifestSHA,
		WorktreePath: args.WorktreePath,
	}, nil
}

// runHandoffUnpack is the TARGET-side core: fetch+add the worktree, download and
// verify the engine-state bundle, and restore it at the target's paths.
func runHandoffUnpack(ctx context.Context, store handoff.BlobStore, home string, args handoffUnpackArgs) (handoffUnpackResult, error) {
	if args.Repo == "" || args.WorktreePath == "" || args.Engine == "" || args.ManifestSHA == "" {
		return handoffUnpackResult{}, fmt.Errorf("teleport unpack: engine, repo, worktree_path and manifest_sha are required")
	}
	// Pack refuses to snapshot without a session id; the restore must be as
	// strict — an empty id would write the state at a nonsense path (e.g.
	// claude's "<slug>/.jsonl") and report success, cold-starting the resume.
	if args.EngineSessionID == "" {
		return handoffUnpackResult{}, fmt.Errorf("teleport unpack: engine_session_id is required")
	}
	if err := gitFetchAndAddWorktree(ctx, args.Repo, args.WorktreePath, args.Branch, args.Remote, args.ExpectHead); err != nil {
		return handoffUnpackResult{}, err
	}
	manifest, err := handoff.GetManifest(ctx, args.ManifestSHA, store)
	if err != nil {
		return handoffUnpackResult{}, fmt.Errorf("teleport unpack: load manifest: %w", err)
	}
	var buf bytes.Buffer
	if err := handoff.Unpack(ctx, manifest, store, &buf); err != nil {
		return handoffUnpackResult{}, fmt.Errorf("teleport unpack: reassemble engine state: %w", err)
	}
	if err := restoreEngineState(args.Engine, home, args.WorktreePath, args.EngineSessionID, buf.Bytes()); err != nil {
		return handoffUnpackResult{}, err
	}
	return handoffUnpackResult{
		WorktreePath:    args.WorktreePath,
		EngineSessionID: args.EngineSessionID,
	}, nil
}

// handoffPack is the Runner command handler for session_handoff_pack.
func (a *Runner) handoffPack(ctx context.Context, cmd HostCommand) (map[string]any, error) {
	var args handoffPackArgs
	if err := json.Unmarshal(cmd.Args, &args); err != nil {
		return nil, fmt.Errorf("teleport pack: bad args: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("teleport pack: resolve HOME: %w", err)
	}
	res, err := runHandoffPack(ctx, hubBlobStore{a.Client}, home, args)
	if err != nil {
		return nil, err
	}
	return structToMap(res)
}

// handoffUnpack is the Runner command handler for session_handoff_unpack.
func (a *Runner) handoffUnpack(ctx context.Context, cmd HostCommand) (map[string]any, error) {
	var args handoffUnpackArgs
	if err := json.Unmarshal(cmd.Args, &args); err != nil {
		return nil, fmt.Errorf("teleport unpack: bad args: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("teleport unpack: resolve HOME: %w", err)
	}
	res, err := runHandoffUnpack(ctx, hubBlobStore{a.Client}, home, args)
	if err != nil {
		return nil, err
	}
	return structToMap(res)
}

// structToMap round-trips a result struct through JSON into the map[string]any
// the command result channel carries.
func structToMap(v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}
