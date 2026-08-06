package hostrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

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
	Engine       string `json:"engine"`
	WorktreePath string `json:"worktree_path"`
	// Workdir is the NON-worktree session's working directory, in portable
	// (`~/…`) form (T2a). Mutually exclusive with WorktreePath: a worktree
	// session's files ride git, a non-worktree session's ride a workdir tar.
	Workdir         string `json:"workdir"`
	Repo            string `json:"repo"`
	Branch          string `json:"branch"`
	Remote          string `json:"remote"`
	EngineSessionID string `json:"engine_session_id"`
	// EnvVars is the agent's PLAIN env-profile vars, relayed by the hub so
	// this host can resolve the engine's session root the same way the
	// child does — an agent can be pointed at its own $CLAUDE_CONFIG_DIR /
	// $KIMI_CODE_HOME, which no host's own environment knows about. Only
	// the engine's root key is read (engineRootFromEnvVars); secrets never
	// travel here — they ride the sealed envelope (ADR-056 D-5). Absent (an
	// older hub) → resolution falls back to this host's env, i.e. the
	// previous behaviour.
	EnvVars map[string]string `json:"env_vars"`
}

type handoffPackResult struct {
	Branch      string `json:"branch"`
	HeadSHA     string `json:"head_sha"`
	Remote      string `json:"remote"`
	ManifestSHA string `json:"manifest_sha"`
	// PortableWorktreePath / PortableRepo are the source paths rewritten
	// home-relative (ADR-057 D-6): the target re-anchors them against its own
	// $HOME so two hosts needn't share a home or absolute layout. When a path
	// isn't under the source home it travels verbatim.
	PortableWorktreePath string `json:"portable_worktree_path"`
	PortableRepo         string `json:"portable_repo"`
	// WorkdirManifestSHA / PortableWorkdirPath are set only for a non-worktree
	// session (T2a): the tar'd workdir bundle handle and the workdir in portable
	// form. ManifestSHA still carries the engine-state bundle in both modes.
	WorkdirManifestSHA  string `json:"workdir_manifest_sha"`
	PortableWorkdirPath string `json:"portable_workdir_path"`
}

type handoffUnpackArgs struct {
	Engine          string `json:"engine"`
	Repo            string `json:"repo"`
	WorktreePath    string `json:"worktree_path"`
	Workdir         string `json:"workdir"`
	Branch          string `json:"branch"`
	Remote          string `json:"remote"`
	ExpectHead      string `json:"expect_head"`
	EngineSessionID string `json:"engine_session_id"`
	ManifestSHA     string `json:"manifest_sha"`
	// WorkdirManifestSHA is the non-worktree workdir bundle (T2a), restored
	// before the engine state.
	WorkdirManifestSHA string `json:"workdir_manifest_sha"`
	// EnvVars mirrors handoffPackArgs.EnvVars — the TARGET resolves its own
	// root from the same agent override, so a source at /srv/profiles/work
	// and a target at ~/.claude-work both land correctly.
	EnvVars map[string]string `json:"env_vars"`
}

type handoffUnpackResult struct {
	WorktreePath string `json:"worktree_path"`
	// Workdir is the target-absolute working directory a non-worktree session
	// was restored to (empty for a worktree session, which reports WorktreePath).
	Workdir         string `json:"workdir"`
	EngineSessionID string `json:"engine_session_id"`
}

// handoffPartTTL is how long a teleport part or manifest may live in the hub
// blob store (ADR-061 D-4).
//
// The floor is named by the mechanism itself: unpack polls for up to 15 minutes
// (`handlers_teleport.go`) and resume-on-source can stretch a retry past one
// poll window, so the TTL must comfortably exceed both. A day costs nothing —
// the alternative these parts had until now was *forever* — and no legitimate
// reader exists beyond the teleport that wrote them. After the row flip the
// target has untarred them; before it, the rollback is resume-on-source. The
// only referrer is a `host_commands` row, which cascade-deletes.
const handoffPartTTL = 24 * time.Hour

// hubBlobStore adapts the host-runner Client to handoff.BlobStore.
type hubBlobStore struct{ c *Client }

func (s hubBlobStore) Put(ctx context.Context, body []byte, mime string) (string, error) {
	// `derived`, not `owned`: these are transient relocation, never storage.
	// Calling them permanent was the specific hole ADR-057 D-3 recorded and
	// deferred, and it made every teleport leak its bundle into the hub forever.
	out, err := s.c.UploadDerivedBlob(ctx, body, mime, handoffPartTTL)
	if err != nil {
		return "", err
	}
	return out.SHA256, nil
}

func (s hubBlobStore) Get(ctx context.Context, sha string) ([]byte, error) {
	return s.c.DownloadBlob(ctx, sha)
}

// runHandoffPack is the SOURCE-side core. A worktree session's files ride git
// (commit+push the branch) and only its engine state is tar'd; a non-worktree
// session (T2a) has no branch, so its workdir is tar'd too. Both return the
// engine-state manifest; the non-worktree path adds the workdir manifest. Store-
// injectable for testing (the hub adapter in production, an in-memory store in
// tests). home is the engine-store home (os.UserHomeDir on a real host).
func runHandoffPack(ctx context.Context, store handoff.BlobStore, home string, args handoffPackArgs) (handoffPackResult, error) {
	if args.Engine == "" {
		return handoffPackResult{}, fmt.Errorf("teleport pack: engine is required")
	}
	engineRoot, err := resolveEngineRoot(args.Engine, engineRootFromEnvVars(args.Engine, args.EnvVars), home)
	if err != nil {
		return handoffPackResult{}, err
	}
	switch {
	case args.WorktreePath != "":
		return packWorktreeSession(ctx, store, home, engineRoot, args)
	case args.Workdir != "":
		return packNonWorktreeSession(ctx, store, home, engineRoot, args)
	default:
		return handoffPackResult{}, fmt.Errorf("teleport pack: worktree_path or workdir is required")
	}
}

// packWorktreeSession is the T1 path: git push the worktree branch + tar the
// engine state keyed on the worktree cwd.
func packWorktreeSession(ctx context.Context, store handoff.BlobStore, home, engineRoot string, args handoffPackArgs) (handoffPackResult, error) {
	head, branch, err := gitCommitAndPush(ctx, args.WorktreePath, args.Branch, args.Remote)
	if err != nil {
		return handoffPackResult{}, err
	}
	// For a worktree session the engine cwd IS the worktree path, so it is also
	// the workdir the resolver keys the engine store on.
	engineSHA, err := chunkBundle(ctx, store, "engine state", func() ([]byte, error) {
		return packEngineState(args.Engine, engineRoot, args.WorktreePath, args.EngineSessionID)
	})
	if err != nil {
		return handoffPackResult{}, err
	}
	remote := args.Remote
	if remote == "" {
		remote = "origin"
	}
	return handoffPackResult{
		// The RESOLVED branch, not args.Branch: with an empty args.Branch the
		// worktree's current branch was used, and the unpack args need its name.
		Branch:               branch,
		HeadSHA:              head,
		Remote:               remote,
		ManifestSHA:          engineSHA,
		PortableWorktreePath: homeRelativePath(args.WorktreePath, home),
		PortableRepo:         homeRelativePath(args.Repo, home),
	}, nil
}

// packNonWorktreeSession is the T2a path: no git, so tar the workdir tree AND
// the engine state (both keyed on the workdir).
func packNonWorktreeSession(ctx context.Context, store handoff.BlobStore, home, engineRoot string, args handoffPackArgs) (handoffPackResult, error) {
	workdir, err := expandHomeWith(args.Workdir, home)
	if err != nil {
		return handoffPackResult{}, err
	}
	workdirSHA, err := chunkBundle(ctx, store, "workdir", func() ([]byte, error) {
		return packWorkdir(workdir, maxWorkdirBundleBytes)
	})
	if err != nil {
		return handoffPackResult{}, err
	}
	engineSHA, err := chunkBundle(ctx, store, "engine state", func() ([]byte, error) {
		return packEngineState(args.Engine, engineRoot, workdir, args.EngineSessionID)
	})
	if err != nil {
		return handoffPackResult{}, err
	}
	return handoffPackResult{
		ManifestSHA:         engineSHA,
		WorkdirManifestSHA:  workdirSHA,
		PortableWorkdirPath: homeRelativePath(workdir, home),
	}, nil
}

// chunkBundle builds a bundle, chunks it through the transport, stores the
// manifest, and returns the manifest handle. `what` names the bundle for errors.
func chunkBundle(ctx context.Context, store handoff.BlobStore, what string, build func() ([]byte, error)) (string, error) {
	bundle, err := build()
	if err != nil {
		return "", err
	}
	manifest, err := handoff.Pack(ctx, bytes.NewReader(bundle), store, handoff.DefaultChunkSize)
	if err != nil {
		return "", fmt.Errorf("teleport pack: chunk %s: %w", what, err)
	}
	sha, err := handoff.PutManifest(ctx, manifest, store)
	if err != nil {
		return "", fmt.Errorf("teleport pack: store %s manifest: %w", what, err)
	}
	return sha, nil
}

// runHandoffUnpack is the TARGET-side core: re-anchor the portable source paths
// against the target's home (ADR-057 D-6), fetch+add the worktree, download and
// verify the engine-state bundle, and restore it at the target's paths. args
// .WorktreePath and .Repo arrive in the portable (`~/…`) form the pack step
// produced; `home` is the target's $HOME.
func runHandoffUnpack(ctx context.Context, store handoff.BlobStore, home string, args handoffUnpackArgs) (handoffUnpackResult, error) {
	if args.Engine == "" || args.ManifestSHA == "" {
		return handoffUnpackResult{}, fmt.Errorf("teleport unpack: engine and manifest_sha are required")
	}
	// Pack refuses to snapshot without a session id; the restore must be as
	// strict — an empty id would write the state at a nonsense path (e.g.
	// claude's "<slug>/.jsonl") and report success, cold-starting the resume.
	if args.EngineSessionID == "" {
		return handoffUnpackResult{}, fmt.Errorf("teleport unpack: engine_session_id is required")
	}
	// The root the RESPAWN will read from — resolved on this host, from this
	// agent's override, so it need not equal the source's path at all.
	engineRoot, err := resolveEngineRoot(args.Engine, engineRootFromEnvVars(args.Engine, args.EnvVars), home)
	if err != nil {
		return handoffUnpackResult{}, err
	}
	switch {
	case args.WorktreePath != "":
		return unpackWorktreeSession(ctx, store, home, engineRoot, args)
	case args.Workdir != "":
		return unpackNonWorktreeSession(ctx, store, home, engineRoot, args)
	default:
		return handoffUnpackResult{}, fmt.Errorf("teleport unpack: worktree_path or workdir is required")
	}
}

// unpackWorktreeSession is the T1 path: git fetch+add the worktree, then restore
// the engine state at the worktree cwd.
func unpackWorktreeSession(ctx context.Context, store handoff.BlobStore, home, engineRoot string, args handoffUnpackArgs) (handoffUnpackResult, error) {
	if args.Repo == "" {
		return handoffUnpackResult{}, fmt.Errorf("teleport unpack: repo is required for a worktree session")
	}
	worktreePath, err := expandHomeWith(args.WorktreePath, home)
	if err != nil {
		return handoffUnpackResult{}, err
	}
	repo, err := expandHomeWith(args.Repo, home)
	if err != nil {
		return handoffUnpackResult{}, err
	}
	if err := gitFetchAndAddWorktree(ctx, repo, worktreePath, args.Branch, args.Remote, args.ExpectHead); err != nil {
		return handoffUnpackResult{}, err
	}
	engine, err := reassembleBundle(ctx, store, args.ManifestSHA, "engine state")
	if err != nil {
		return handoffUnpackResult{}, err
	}
	if err := restoreEngineState(args.Engine, engineRoot, worktreePath, args.EngineSessionID, engine); err != nil {
		return handoffUnpackResult{}, err
	}
	// Return the TARGET-absolute worktree path so the hub records the session's
	// new on-disk location.
	return handoffUnpackResult{
		WorktreePath:    worktreePath,
		EngineSessionID: args.EngineSessionID,
	}, nil
}

// unpackNonWorktreeSession is the T2a path: restore the workdir tar first, then
// the engine state on top of it (both at the target-anchored workdir). The
// respawn re-derives the same workdir on the target, so the hub records no path.
func unpackNonWorktreeSession(ctx context.Context, store handoff.BlobStore, home, engineRoot string, args handoffUnpackArgs) (handoffUnpackResult, error) {
	if args.WorkdirManifestSHA == "" {
		return handoffUnpackResult{}, fmt.Errorf("teleport unpack: workdir_manifest_sha is required for a non-worktree session")
	}
	workdir, err := expandHomeWith(args.Workdir, home)
	if err != nil {
		return handoffUnpackResult{}, err
	}
	workdirBundle, err := reassembleBundle(ctx, store, args.WorkdirManifestSHA, "workdir")
	if err != nil {
		return handoffUnpackResult{}, err
	}
	if err := restoreWorkdir(workdir, workdirBundle); err != nil {
		return handoffUnpackResult{}, err
	}
	engine, err := reassembleBundle(ctx, store, args.ManifestSHA, "engine state")
	if err != nil {
		return handoffUnpackResult{}, err
	}
	if err := restoreEngineState(args.Engine, engineRoot, workdir, args.EngineSessionID, engine); err != nil {
		return handoffUnpackResult{}, err
	}
	return handoffUnpackResult{
		Workdir:         workdir,
		EngineSessionID: args.EngineSessionID,
	}, nil
}

// reassembleBundle loads a manifest and reassembles its verified byte stream.
func reassembleBundle(ctx context.Context, store handoff.BlobStore, manifestSHA, what string) ([]byte, error) {
	manifest, err := handoff.GetManifest(ctx, manifestSHA, store)
	if err != nil {
		return nil, fmt.Errorf("teleport unpack: load %s manifest: %w", what, err)
	}
	var buf bytes.Buffer
	if err := handoff.Unpack(ctx, manifest, store, &buf); err != nil {
		return nil, fmt.Errorf("teleport unpack: reassemble %s: %w", what, err)
	}
	return buf.Bytes(), nil
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
