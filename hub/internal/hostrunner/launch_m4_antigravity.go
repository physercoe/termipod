// M4 launch path for Antigravity (`agy`) — ADR-035 W7. Mirrors
// launchM4LocalLogTail (claude-code) but much leaner: agy has no
// host-runner hook surface (no permission-prompt-tool, no UDS gateway)
// and its session log is a rewritten snapshot, so this path is just
// "compose the antigravity adapter + driver, write the global MCP
// config, launch the pane, start the driver." On any failure the runner
// falls back to the PaneDriver M4 path so the agent still launches.
package hostrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/termipod/hub"
	"github.com/termipod/hub/internal/agentfamilies"
	locallogtail "github.com/termipod/hub/internal/drivers/local_log_tail"
	"github.com/termipod/hub/internal/drivers/local_log_tail/antigravity"
)

// launchM4Antigravity composes the antigravity adapter into a spawn-time
// path. It reuses M4LocalLogTailLaunchConfig for symmetry but only
// consults Spawn / Launcher / Client / HubURL / Log (no gateway fields —
// agy has no hook surface).
func launchM4Antigravity(ctx context.Context, cfg M4LocalLogTailLaunchConfig) (*M4LocalLogTailLaunchResult, error) {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Spawn.ChildID == "" {
		return nil, fmt.Errorf("antigravity M4: empty ChildID")
	}

	spec, _ := ParseSpec(cfg.Spawn.SpawnSpec)

	// Workdir derivation mirrors the claude-code M4 path. For agy this
	// directory is load-bearing in a second way: it is the key into agy's
	// workspace→conversationId cache, so it MUST equal the cwd the
	// template's backend.cmd cd's into before exec'ing `agy`.
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
		rawWD = filepath.Join("~", "hub-work", pid, handle)
	}
	if rawWD == "" {
		return nil, fmt.Errorf("antigravity M4: backend.default_workdir empty and no project_id to derive (it keys agy's conversation cache)")
	}
	workdir, err := expandHome(rawWD)
	if err != nil {
		return nil, fmt.Errorf("antigravity M4: expand default_workdir: %w", err)
	}
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		return nil, fmt.Errorf("antigravity M4: mkdir workdir %q: %w", workdir, err)
	}

	// Materialize the persona-memory file the hub rendered for this spawn
	// (AGENTS.md for antigravity — see contextFileNameForKind). The
	// post-v1.0.651 smoke caught the consequence of leaving this off:
	// agy spawned with NO persona prompt at all, defaulted to bare-agy
	// behaviour against an empty `[directive from the principal]` envelope,
	// and improvised 357 steps of self-invented work. M1/M2 launch paths
	// do this; M4 LocalLogTail must too — the M4 path doesn't have an
	// alternative channel for persona delivery (no system-prompt CLI flag
	// on agy, no preface turn). Failure is fatal here for the same reason
	// it is in M1/M2: an antigravity agent missing its AGENTS.md is a
	// blank agy session that won't behave like a steward.
	if len(spec.ContextFiles) > 0 {
		if err := writeContextFiles(workdir, spec.ContextFiles); err != nil {
			return nil, fmt.Errorf("antigravity M4: write context_files: %w", err)
		}
	}

	// Write the agy MCP config in TWO places so the token stays fresh
	// across respawns:
	//
	//   - GLOBAL: ~/.gemini/config/mcp_config.json — what new agy
	//     processes read on first launch. Keys: termipod (this spawn's
	//     token), plus any user-managed servers (agytest, ...) we
	//     preserve via merge.
	//
	//   - WORKDIR: <workdir>/.mcp.json — what agy reads in addition to
	//     global, with WORKDIR winning on same-server-name conflicts.
	//     This file is the load-bearing one for token freshness: agy
	//     1.0.1 sync-copies the termipod entry from global → workdir on
	//     first read but never re-syncs on subsequent launches, so a
	//     workdir snapshot from a prior session pins the OLD token
	//     forever (post-v1.0.652 smoke caught this — agy got `401
	//     invalid mcp token` → "client is closing: invalid request"
	//     even though the global config had the correct fresh token).
	//     Writing workdir at every spawn keeps the token current.
	//
	// Both writes are best-effort: a failure degrades to "no hub MCP"
	// but the agent still launches (its transcript is still tailed).
	if cfg.Spawn.MCPToken != "" && cfg.HubURL != "" {
		if werr := writeMCPConfigAntigravityGlobal(cfg.HubURL, cfg.Spawn.MCPToken); werr != nil {
			cfg.Log.Warn("antigravity M4: write global mcp_config failed; launching without hub MCP",
				"handle", cfg.Spawn.Handle, "err", werr)
		}
		if werr := writeMCPConfig(workdir, cfg.HubURL, cfg.Spawn.MCPToken); werr != nil {
			cfg.Log.Warn("antigravity M4: write workdir .mcp.json failed; agy may use a stale cached token",
				"handle", cfg.Spawn.Handle, "workdir", workdir, "err", werr)
		}
	}

	// Pre-trust the workdir so agy doesn't pop its "trust this folder?"
	// arrow-nav dialog at launch — the mobile app has no Up/Down/Enter
	// affordance yet, so a fresh-workdir spawn would otherwise sit
	// blocked on a menu the user can't drive. agy persists trust in
	// ~/.gemini/antigravity-cli/settings.json → trustedWorkspaces[].
	// Idempotent (skips if already present); best-effort (a failure
	// just means the user gets the dialog once and clicks through).
	if werr := preTrustWorkspaceAntigravity(workdir); werr != nil {
		cfg.Log.Warn("antigravity M4: pre-trust workspace failed; user may see the trust dialog",
			"handle", cfg.Spawn.Handle, "workdir", workdir, "err", werr)
	}

	// v1.0.718 (G3 — session-details parity, mirror of codex v1.0.715):
	// resolve launch-time engine identity so the adapter's session.init
	// post can populate the engine/version/cwd/permission_mode fields
	// the mobile session-details sheet reads
	// (`lib/widgets/session_details_sheet.dart`). Without these, the
	// AGENT + WORKDIR sections rendered blank for antigravity stewards.
	//
	// PermissionMode is derived from backend.cmd verbatim: the launch
	// glue invokes agy with or without --dangerously-skip-permissions,
	// and we surface that flag-derived string (no translation per the
	// rationale in docs/discussions/antigravity-statusline-research.md
	// — easier to grep on the hub side than a shorter alias).
	engineVersion := ""
	if fam, ok := agentfamilies.ByName(cfg.Spawn.Kind); ok && fam.VersionFlag != "" {
		if path, perr := exec.LookPath(fam.Bin); perr == nil && path != "" {
			if v, vok := runVersion(ctx, path, fam.VersionFlag); vok {
				engineVersion = v
			}
		}
	}
	adapter, err := antigravity.NewAdapter(antigravity.Config{
		AgentID:        cfg.Spawn.ChildID,
		Workdir:        workdir,
		Engine:         cfg.Spawn.Kind,
		EngineVersion:  engineVersion,
		PermissionMode: permissionModeFromCmd(spec.Backend.Cmd),
		Poster:         cfg.Client,
		Log:            cfg.Log,
	})
	if err != nil {
		return nil, fmt.Errorf("antigravity M4: new adapter: %w", err)
	}
	// Resume cursor (ADR-035 D8): if the template baked `--conversation
	// <id>` into backend.cmd (spliced server-side on respawn), resolve
	// that id directly so Start skips the workspace-cache race.
	if id := conversationIDFromCmd(spec.Backend.Cmd); id != "" {
		adapter.ConversationID = id
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
		return nil, fmt.Errorf("antigravity M4: backend.cmd is empty")
	}
	// Prepend `cd <workdir> &&` so agy's cwd deterministically equals the
	// workdir we resolved — agy keys its conversation cache by cwd, and
	// the pathresolver looks it up by this same workdir, so the two MUST
	// agree. M1/M2 launch paths do the same (launch_m2.go); the M4
	// LocalLogTail path leaves cwd to the template, but agy can't tolerate
	// that ambiguity.
	cmd = fmt.Sprintf("cd %s && %s", shellEscape(workdir), cmd)
	pane, err := cfg.Launcher.LaunchCmd(ctx, cfg.Spawn, cmd)
	if err != nil {
		return nil, fmt.Errorf("antigravity M4: tmux launch: %w", err)
	}
	adapter.PaneID = pane

	if err := driver.Start(ctx); err != nil {
		return nil, fmt.Errorf("antigravity M4: driver start: %w", err)
	}

	return &M4LocalLogTailLaunchResult{
		PaneID: pane,
		Driver: driver,
	}, nil
}

// writeMCPConfigAntigravityGlobal idempotently merges a `termipod` entry
// into agy's GLOBAL MCP config at ~/.gemini/config/mcp_config.json
// (host-verified read location; agy ignores per-workdir files). The entry
// is a stdio bridge (`hub-mcp-bridge` + env), the same shape claude-code's
// `.mcp.json` `termipod` entry uses — agy's stdio MCP path is verified
// end-to-end (the ping round-trip).
//
// Why global, not per-spawn: agy's OAuth token, store, and MCP config all
// live under ~/.gemini, so a per-spawn HOME (the isolation option weighed
// in ADR-035 D7) would break auth. The cost is that the file holds one
// `termipod` entry at a time — on a shared host with concurrent agy
// agents the per-spawn token is last-writer-wins. MVP attributes MCP
// calls to the spawn via the verified `_meta.antigravity.google/
// conversation_id` correlation hook rather than a per-spawn token, so the
// shared entry is acceptable; tighter per-agent isolation is Phase 2.
//
// An empty/invalid existing file is replaced from `{}` — agy emits a JSON
// parse error on every run against a 0-byte file (verified), so we never
// leave it empty.
func writeMCPConfigAntigravityGlobal(hubURL, token string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve HOME: %w", err)
	}
	path := filepath.Join(home, ".gemini", "config", "mcp_config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}

	cfg := map[string]any{}
	if b, rerr := os.ReadFile(path); rerr == nil && len(bytes.TrimSpace(b)) > 0 {
		_ = json.Unmarshal(b, &cfg) // invalid → start from {} (parse-error fix)
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers[hub.MCPServerName] = map[string]any{
		"command": "hub-mcp-bridge",
		"env": map[string]string{
			"HUB_URL":   hubURL,
			"HUB_TOKEN": token,
		},
	}
	cfg["mcpServers"] = servers

	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o600)
}

// preTrustWorkspaceAntigravity appends workdir to agy's trustedWorkspaces
// list in ~/.gemini/antigravity-cli/settings.json so the "trust this
// folder?" arrow-nav dialog never fires for spawned agents.
//
// agy persists trust per absolute path — the shape is host-verified:
//
//	{
//	  "enableTelemetry": false,
//	  "statusLine": { ... },
//	  "trustedWorkspaces": ["/abs/path1", "/abs/path2"]
//	}
//
// This function preserves any unrelated keys (enableTelemetry, statusLine,
// future agy additions). Reads, deduplicates against the existing list,
// appends only if missing, writes back atomically. workdir is
// filepath.Clean'd to match agy's storage convention.
//
// Why launch-time, not template/install: the workdir is dynamic (per
// project / per handle), so the trust list has to grow as agents spawn.
// The user pre-trusting "~/hub-work" globally would be ideal but agy
// stores absolute paths, not prefixes, so per-spawn pre-grant is the
// only path that doesn't require user intervention.
//
// Best-effort: a missing settings.json is treated as the empty config
// (write a fresh `{"trustedWorkspaces": [...]}`); a malformed one falls
// through silently and the user gets the dialog this once. The function
// never returns a launch-blocking error from the caller's perspective —
// the caller logs and continues either way.
func preTrustWorkspaceAntigravity(workdir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve HOME: %w", err)
	}
	path := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir settings dir: %w", err)
	}

	clean := filepath.Clean(workdir)

	cfg := map[string]any{}
	if b, rerr := os.ReadFile(path); rerr == nil && len(bytes.TrimSpace(b)) > 0 {
		if jerr := json.Unmarshal(b, &cfg); jerr != nil {
			return fmt.Errorf("parse existing settings (%s): %w", path, jerr)
		}
	}

	// trustedWorkspaces lands as []any after json.Unmarshal into map[string]any.
	rawList, _ := cfg["trustedWorkspaces"].([]any)
	for _, v := range rawList {
		if s, ok := v.(string); ok && filepath.Clean(s) == clean {
			return nil // already trusted; no-op write
		}
	}
	rawList = append(rawList, clean)
	cfg["trustedWorkspaces"] = rawList

	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o600)
}

// conversationIDFromCmd extracts the value of a `--conversation <id>` (or
// `--conversation=<id>`) flag from a backend.cmd string, or "" if absent.
// The hub splices this flag on respawn (spliceAntigravityResume); reading
// it back lets the adapter resolve the conversation deterministically
// instead of polling agy's workspace cache.
func conversationIDFromCmd(cmd string) string {
	tokens := strings.Fields(cmd)
	for i, tok := range tokens {
		if tok == "--conversation" && i+1 < len(tokens) {
			return tokens[i+1]
		}
		if v, ok := strings.CutPrefix(tok, "--conversation="); ok {
			return v
		}
	}
	return ""
}

// permissionModeFromCmd inspects backend.cmd for agy's auto-approve flag
// and returns a verbatim flag-derived label for the session.init
// payload's permission_mode field. Surfaced to mobile's session-details
// sheet (`lib/widgets/session_details_sheet.dart`).
//
// agy 1.0.2 surface (`agy --help`): only --dangerously-skip-permissions
// auto-approves; absent that flag, every tool gate raises an
// interactive arrow-nav menu the operator must answer. (--sandbox
// modifies execution, not permission policy, so it doesn't change the
// label.) v1.0.718 G3 — value strings are intentionally raw, not
// translated to claude-code's "bypassPermissions" / "default" alias
// vocabulary; the mobile _permModeColor switch is extended to map both
// vocabularies to the same colour family.
func permissionModeFromCmd(cmd string) string {
	for _, tok := range strings.Fields(cmd) {
		if tok == "--dangerously-skip-permissions" {
			return "dangerously-skip-permissions"
		}
	}
	return "interactive"
}
