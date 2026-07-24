// M4 launch path for Kimi Code CLI (kimi-code / kimi-code-ts) —
// docs/plans/agent-transcript-redesign.md §6 P4, ticket #372. kimi's M4
// used to fall through to the raw PaneDriver; this path replaces it
// with a LocalLogTail adapter (drivers/local_log_tail/kimi_code) that
// tails kimi's session wire store
// ($KIMI_CODE_HOME|~/.kimi-code/sessions/<wd>/<session>/agents/*/wire.jsonl)
// and emits structured agent events (tool calls, plan, usage,
// approvals, subagent activity) instead of raw pane text.
//
// Unlike the claude-code / antigravity M4 arms (which fail the agent on
// error), this path is a strict UPGRADE over PaneDriver, so the runner
// keeps the PaneDriver fallback: any error here happens BEFORE the
// pane is spawned, and runner.go falls through to the PaneDriver M4
// block.
// That covers older kimi builds without the wire store (the Python
// kimi-cli line writes ~/.kimi, not ~/.kimi-code), hosts where kimi has
// never run, and wire protocol drift (the metadata gate — a prior
// session's protocol_version is sniffed pre-launch; on mismatch the
// spawn degrades to PaneDriver rather than emitting garbage).
package hostrunner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/termipod/hub/internal/agentfamilies"
	locallogtail "github.com/termipod/hub/internal/drivers/local_log_tail"
	kimicode "github.com/termipod/hub/internal/drivers/local_log_tail/kimi_code"
)

// launchM4KimiWireTail composes the kimi wire-tail adapter into a
// spawn-time path. It reuses M4LocalLogTailLaunchConfig for symmetry
// but only consults Spawn / Launcher / Client / HubURL / Log / Team (no
// gateway fields — kimi has no host-runner hook or statusLine surface).
//
// Ordering invariant: every fallible step runs BEFORE LaunchCmd, so an
// error return always means "no pane spawned" and the runner's
// PaneDriver fall-through can't double-launch (the exact bug class the
// claude/agy arms avoid by failing the agent instead; kimi can afford
// the fallback because PaneDriver is a working degraded mode, not a
// misconfiguration).
func launchM4KimiWireTail(ctx context.Context, cfg M4LocalLogTailLaunchConfig) (*M4LocalLogTailLaunchResult, error) {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Spawn.ChildID == "" {
		return nil, fmt.Errorf("kimi wire-tail M4: empty ChildID")
	}
	if cfg.Spawn.Kind != "kimi-code" && cfg.Spawn.Kind != "kimi-code-ts" {
		return nil, fmt.Errorf("kimi wire-tail M4: only kimi-code/kimi-code-ts are wired (got %q)", cfg.Spawn.Kind)
	}

	spec, _ := ParseSpec(cfg.Spawn.SpawnSpec)
	// Workdir derivation mirrors the claude-code / antigravity M4
	// paths. It is doubly load-bearing here: it is the key into kimi's
	// workspaces.json cwd → wd_* mapping, so it MUST equal the cwd the
	// backend.cmd runs kimi from.
	rawWD := spec.Backend.DefaultWorkdir
	if rawWD == "" && cfg.Spawn.ProjectID != "" {
		pid := cfg.Spawn.ProjectID
		if len(pid) > 8 {
			pid = pid[:8]
		}
		handle := cfg.Spawn.Handle
		if handle == "" {
			handle = cfg.Spawn.ChildID
		}
		// `<team>` segment (ADR-037 D6) isolates teams on a shared host;
		// teamWorkRoot collapses to `~/hub-work` when team is empty.
		rawWD = filepath.Join(teamWorkRoot(cfg.Team), pid, handle)
	}
	if rawWD == "" {
		return nil, fmt.Errorf("kimi wire-tail M4: backend.default_workdir empty and no project_id to derive (it keys kimi's workspace→session mapping)")
	}
	if _, err := ensureTeamWorkRoot(cfg.Team); err != nil {
		return nil, fmt.Errorf("kimi wire-tail M4: ensure team work root: %w", err)
	}
	workdir, err := expandHome(rawWD)
	if err != nil {
		return nil, fmt.Errorf("kimi wire-tail M4: expand default_workdir: %w", err)
	}
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		return nil, fmt.Errorf("kimi wire-tail M4: mkdir workdir %q: %w", workdir, err)
	}

	// Materialize the persona-memory file the hub rendered for this
	// spawn (AGENTS.md for kimi — see contextFileNameForKind). Same
	// rationale as the claude/agy M4 paths: the pane has no other
	// persona channel.
	if len(spec.ContextFiles) > 0 {
		if err := writeContextFiles(workdir, spec.ContextFiles); err != nil {
			return nil, fmt.Errorf("kimi wire-tail M4: write context_files: %w", err)
		}
	}

	// Wire-store pre-flight — the PaneDriver fallback gate. The store
	// must exist AND workspaces.json must parse: without the cwd→wd_*
	// mapping the adapter can never resolve this spawn's session, so
	// degrade now (pane text) instead of mid-session.
	storeHome, err := kimicode.StoreHome()
	if err != nil {
		return nil, fmt.Errorf("kimi wire-tail M4: resolve store home: %w", err)
	}
	if st, serr := os.Stat(filepath.Join(storeHome, "sessions")); serr != nil || !st.IsDir() {
		return nil, fmt.Errorf("kimi wire-tail M4: wire store %s not found (kimi-code TS build never ran here, or Python kimi-cli) — falling back to PaneDriver", storeHome)
	}
	if _, serr := kimicode.LookupWorkspaceID(storeHome, workdir); serr != nil {
		// ErrNoWorkspace is EXPECTED on a fresh per-spawn workdir (kimi
		// adds the entry when it opens the cwd at launch) — only a
		// corrupt workspaces.json (a non-ErrNoWorkspace error) blocks
		// the adapter, since the runtime resolver treats it as a hard
		// failure too.
		if !errors.Is(serr, kimicode.ErrNoWorkspace) {
			return nil, fmt.Errorf("kimi wire-tail M4: workspaces.json unusable: %w", serr)
		}
	}

	// Protocol gate (plan §6 P4): sniff the newest existing wire file's
	// protocol_version — it's a property of the installed kimi build,
	// so it predicts what the new session will write. found=false (no
	// prior wire files) proceeds optimistically; the adapter re-gates
	// on the session's own metadata line at runtime.
	if version, found, serr := kimicode.SniffProtocolVersion(storeHome); serr != nil {
		return nil, fmt.Errorf("kimi wire-tail M4: sniff wire protocol: %w", serr)
	} else if found && !kimicode.SupportedProtocolVersion(version) {
		return nil, fmt.Errorf("kimi wire-tail M4: unsupported wire protocol %q — falling back to PaneDriver", version)
	}

	// Per-spawn MCP config so kimi's request_* attention tools reach
	// the hub. kimi-code-ts auto-discovers <workdir>/.kimi-code/
	// mcp.json; kimi-code (Python) needs the --mcp-config-file argv
	// splice (mirroring launch_m1.go). Best-effort like the
	// antigravity path: a failure degrades hub-MCP reachability, not
	// the transcript.
	if cfg.Spawn.MCPToken != "" && cfg.HubURL != "" {
		if werr := writeMCPConfigForFamily(cfg.Spawn.Kind, workdir, cfg.HubURL, cfg.Spawn.MCPToken); werr != nil {
			cfg.Log.Warn("kimi wire-tail M4: write mcp config failed; agent runs without hub MCP",
				"handle", cfg.Spawn.Handle, "workdir", workdir, "err", werr)
		}
	}

	// Launch-time engine identity for session.init (mirror of the
	// antigravity v1.0.718 G3 block).
	engineVersion := ""
	if fam, ok := agentfamilies.ByName(cfg.Spawn.Kind); ok && fam.VersionFlag != "" {
		if path, perr := exec.LookPath(fam.Bin); perr == nil && path != "" {
			if v, vok := runVersion(ctx, path, fam.VersionFlag); vok {
				engineVersion = v
			}
		}
	}

	adapter, err := kimicode.NewAdapter(kimicode.Config{
		AgentID:        cfg.Spawn.ChildID,
		Workdir:        workdir,
		Engine:         cfg.Spawn.Kind,
		EngineVersion:  engineVersion,
		PermissionMode: kimiPermissionModeFromCmd(spec.Backend.Cmd),
		Poster:         cfg.Client,
		Log:            cfg.Log,
	})
	if err != nil {
		return nil, fmt.Errorf("kimi wire-tail M4: new adapter: %w", err)
	}

	driver := &locallogtail.Driver{
		Config: locallogtail.Config{
			AgentID: cfg.Spawn.ChildID,
			Poster:  cfg.Client,
			Log:     cfg.Log,
		},
		Adapter: adapter,
	}

	cmd := spec.Backend.Cmd
	if cmd == "" {
		return nil, fmt.Errorf("kimi wire-tail M4: backend.cmd is empty")
	}
	// Python kimi-cli only: splice --mcp-config-file so the per-spawn
	// .kimi/mcp.json wins over ~/.kimi/mcp.json (mirrors launch_m1.go —
	// the TS build auto-discovers its project-level file and needs no
	// flag). Idempotent against templates that already carry the flag.
	if cfg.Spawn.Kind == "kimi-code" && cfg.Spawn.MCPToken != "" && cfg.HubURL != "" &&
		!strings.Contains(cmd, "--mcp-config-file") {
		mcpPath := filepath.Join(workdir, ".kimi", "mcp.json")
		cmd = strings.Replace(cmd, "kimi ",
			"kimi --mcp-config-file "+shellEscape(mcpPath)+" ", 1)
	}
	// Prepend `cd <workdir> &&` so kimi's cwd deterministically equals
	// the workdir we resolved — kimi keys its workspace→session mapping
	// by cwd, and the adapter looks the session up by this same
	// workdir, so the two MUST agree (the claude/agy M4 paths do the
	// same).
	cmd = fmt.Sprintf("cd %s && %s", shellEscape(workdir), cmd)

	// Everything fallible is behind us. Launch the pane, then start the
	// driver; adapter.Start is async (resolver + tails spin up in the
	// background and degrade to system notices on failure), so
	// driver.Start effectively cannot fail post-launch.
	pane, err := cfg.Launcher.LaunchCmd(ctx, cfg.Spawn, cmd)
	if err != nil {
		return nil, fmt.Errorf("kimi wire-tail M4: tmux launch: %w", err)
	}
	adapter.PaneID = pane

	if err := driver.Start(ctx); err != nil {
		return nil, fmt.Errorf("kimi wire-tail M4: driver start: %w", err)
	}

	return &M4LocalLogTailLaunchResult{
		PaneID: pane,
		Driver: driver,
	}, nil
}

// kimiPermissionModeFromCmd inspects backend.cmd for kimi's
// auto-approve flag and returns a verbatim flag-derived label for the
// session.init payload's permission_mode field (mirrors
// permissionModeFromCmd for agy). Both kimi product lines use --yolo.
func kimiPermissionModeFromCmd(cmd string) string {
	for _, tok := range strings.Fields(cmd) {
		if tok == "--yolo" {
			return "yolo"
		}
	}
	return "interactive"
}
