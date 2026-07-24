// M4 launch path that uses the LocalLogTailDriver (ADR-027) — W5a +
// W7. Three adapters are wired: claude-code (this file), antigravity
// (launch_m4_antigravity.go, ADR-035 W7), and kimi-code/kimi-code-ts
// (launch_m4_kimi.go, agent-transcript-redesign §6 P4 — the wire-tail
// adapter, WITH PaneDriver fallback). Other engines stay on the
// PaneDriver M4 path until their adapters land (gemini-cli, codex).
// The runner falls back to PaneDriver M4 if any step here fails, so a
// misconfigured spawn degrades gracefully rather than erroring out.
package hostrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/termipod/hub"
	locallogtail "github.com/termipod/hub/internal/drivers/local_log_tail"
	claudecode "github.com/termipod/hub/internal/drivers/local_log_tail/claude_code"
)

// M4LocalLogTailLaunchConfig is the per-spawn input to
// launchM4LocalLogTail. Mirrors M1/M2 launch configs for symmetry.
type M4LocalLogTailLaunchConfig struct {
	Spawn    Spawn
	Launcher Launcher
	Client   AgentEventPoster
	// HubURL is the URL written into .mcp.json — typically the
	// 127.0.0.1:41825 egress-proxy URL so the public hub URL stays
	// hidden from the agent process.
	HubURL string
	// HubURLForGateway is the URL the host-runner gateway forwards
	// hub-authority + attention HTTP calls to. Usually identical to
	// HubURL — tests pass an httptest server URL.
	HubURLForGateway string
	// GatewayHubClient is the *Client the per-spawn UDS gateway uses
	// to forward hub.* tool calls + attention parking. Typically the
	// runner's own a.Client.
	GatewayHubClient *Client
	Log              *slog.Logger
	// Team is the host-runner's single team (`--team`), used to derive
	// the `<team>`-segmented workdir (ADR-037 D6). Shared by the
	// claude-code (locallogtail) and antigravity M4 launchers. Empty
	// falls back to the legacy team-less path.
	Team string
}

// M4LocalLogTailLaunchResult — same shape as the M1/M2 results so
// runner.go's mode dispatch records pane id / driver uniformly.
type M4LocalLogTailLaunchResult struct {
	PaneID  string
	Driver  Driver
	Gateway *McpGateway
	// HostRunnerExe is the path of the host-runner binary that
	// claude-code's `.mcp.json` references for the `termipod-host`
	// stdio↔UDS shim. Captured so tests can verify the dual-server
	// config writer received the right value; production wiring
	// always uses os.Args[0].
	HostRunnerExe string
}

// launchM4LocalLogTail composes the W2 adapter, W5b gateway, W5c
// stdio shim, W5d dual-server .mcp.json writer, and W6 hooks
// installer into a single spawn-time path:
//
//  1. Validate prerequisites (workdir, MCP token, host runner exe).
//  2. Materialize <workdir>/.mcp.json with both `termipod` (egress
//     proxy → hub) and `termipod-host` (UDS gateway) entries (W5d).
//  3. Merge ADR-027 hook entries into <workdir>/.claude/settings.local.json (W6).
//  4. Construct the claude-code Adapter + LocalLogTailDriver but do
//     NOT Start them yet — the gateway must be live first so claude
//     can't make a hook call before HookSink is wired.
//  5. StartGateway on the per-spawn UDS path; assign the driver as
//     HookSink (W5b).
//  6. Launch claude via the tmux launcher with the spawn's resolved
//     backend command. Capture the pane id and stash it on the adapter
//     so HandleInput can route send-keys (W2h).
//  7. Start the driver — this kicks off the JSONL tail + run loop in
//     the adapter. The tail blocks until the session JSONL appears
//     under ~/.claude/projects/<encoded-cwd>/.
//
// Returns a result on success; an error propagates back to runner.go
// where it triggers a fall-through to the PaneDriver M4 path so the
// agent still launches (degraded UX but live). On failure, the
// gateway is closed so we don't leak a dangling listener.
func launchM4LocalLogTail(ctx context.Context, cfg M4LocalLogTailLaunchConfig) (*M4LocalLogTailLaunchResult, error) {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Spawn.ChildID == "" {
		return nil, fmt.Errorf("locallogtail M4: empty ChildID")
	}
	if cfg.Spawn.Kind != "claude-code" {
		return nil, fmt.Errorf("locallogtail M4: only claude-code is wired (got %q)", cfg.Spawn.Kind)
	}

	spec, _ := ParseSpec(cfg.Spawn.SpawnSpec)
	// Workdir resolution mirrors launch_m2.go (ADR-025 W6):
	//   1. spec.Backend.DefaultWorkdir   — explicit template field wins.
	//   2. cfg.Spawn.ProjectID set        — derive ~/hub-work/<pid8>/<handle>
	//      so per-project claude-code stewards stay isolated on shared
	//      hosts. Without this, two project stewards using steward.v1
	//      would collide on .mcp.json / .claude/settings.local.json.
	//   3. neither                        — error: M4 needs a stable
	//      workdir to anchor the JSONL path lookup, no host-cwd fallback.
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
		// `~/hub-work/<team>/<pid8>/<handle>` — the `<team>` segment
		// (ADR-037 D6) keeps two teams off each other's subtree on a
		// shared host; teamWorkRoot collapses to `~/hub-work` when team
		// is empty (legacy spawns).
		rawWD = filepath.Join(teamWorkRoot(cfg.Team), pid, handle)
	}
	if rawWD == "" {
		return nil, fmt.Errorf("locallogtail M4: backend.default_workdir empty and no project_id to derive (JSONL path resolves under <workdir>)")
	}
	if _, err := ensureTeamWorkRoot(cfg.Team); err != nil {
		return nil, fmt.Errorf("locallogtail M4: ensure team work root: %w", err)
	}
	workdir, err := expandHome(rawWD)
	if err != nil {
		return nil, fmt.Errorf("locallogtail M4: expand default_workdir: %w", err)
	}
	// Auto-derive paths may not exist yet on first launch; ensure the
	// directory before .mcp.json / settings.local.json materialization.
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		return nil, fmt.Errorf("locallogtail M4: mkdir workdir %q: %w", workdir, err)
	}

	// Materialize the persona-memory file the hub rendered for this
	// spawn (CLAUDE.md — see contextFileNameForKind). M1/M2 do this;
	// the M4 LocalLogTail path was the lone holdout, so claude-code
	// stewards spawned without their steward persona — same gap agy
	// hit at v1.0.651 (project_session_2026_05_23_part2). M4 has no
	// alternative channel for persona delivery (no `--system-prompt`
	// flag we drive from outside), so the omission is silent: claude
	// starts cleanly, runs the empty prompt, and behaves like a bare
	// claude with no project context. Fatal on failure — a
	// persona-less steward is not a steward.
	if len(spec.ContextFiles) > 0 {
		if err := writeContextFiles(workdir, spec.ContextFiles); err != nil {
			return nil, fmt.Errorf("locallogtail M4: write context_files: %w", err)
		}
	}

	if cfg.Spawn.MCPToken == "" {
		return nil, fmt.Errorf("locallogtail M4: MCPToken is required (claude needs it to reach permission_prompt)")
	}
	if cfg.HubURL == "" {
		return nil, fmt.Errorf("locallogtail M4: HubURL is required (egress proxy URL)")
	}

	// Resolve host-runner exe path — what claude-code spawns from
	// `.mcp.json` for the termipod-host stdio shim. Default to the
	// running binary so tests + dev invocations work without extra
	// config; deployments using a different basename can set the
	// runner field at construction time.
	hostRunnerExe := hostRunnerExePath()

	// W5d: dual-server .mcp.json — termipod (hub authority) +
	// termipod-host (gateway-local hook handlers).
	udsPath := socketPath(cfg.Spawn.ChildID)
	if err := writeMCPConfigClaudeCodeM4(
		workdir, cfg.HubURL, cfg.Spawn.MCPToken, udsPath, hostRunnerExe,
	); err != nil {
		return nil, fmt.Errorf("locallogtail M4: write .mcp.json: %w", err)
	}

	// W6: settings.local.json hooks. v1.0.659 rebuild — emit
	// type:"command" entries that exec the host-runner hook-fire shim
	// against the per-spawn UDS gateway. Pre-v1.0.659 emitted the
	// invalid type:"mcp_tool" form (see hooks_install.go header note);
	// any stale workdir is self-healed by appendTermipodMatcher's
	// strip-then-append idempotency.
	if err := installClaudeHooks(workdir, hostRunnerExe, udsPath); err != nil {
		return nil, fmt.Errorf("locallogtail M4: install hooks: %w", err)
	}

	// ADR-036 W1: settings.local.json statusLine. Sister to
	// installClaudeHooks above; same merge primitive (atomic-rename;
	// wrap-and-passthrough preserves operator-set statusLine).
	// Failure is non-fatal — the chip strip degrades to the pre-ADR-036
	// JSONL-derived baseline if statusLine wiring fails, rather than
	// blocking the spawn. Logged so operators see the regression.
	if err := installClaudeStatusLine(workdir, hostRunnerExe, udsPath); err != nil {
		cfg.Log.Warn("locallogtail M4: install statusLine failed; telemetry chips will use JSONL fallback",
			"handle", cfg.Spawn.Handle, "workdir", workdir, "err", err)
	}

	// Pre-trust the workdir in ~/.claude.json so claude-code doesn't
	// open with its "Do you trust the files in this folder?" prompt —
	// the mobile client has no affordance to drive that picker, so a
	// fresh-workdir spawn would otherwise sit blocked on the welcome
	// screen. claude-code persists trust in ~/.claude.json under
	// `projects.<workdir>.hasTrustDialogAccepted` (host-verified on
	// the dev box; also see the agy parallel at v1.0.644). Idempotent
	// (skips if already accepted); best-effort (a failure just means
	// the user gets the dialog once and may be unable to dismiss it,
	// which is still better than failing the spawn outright).
	if werr := preTrustWorkspaceClaudeCode(workdir); werr != nil {
		cfg.Log.Warn("locallogtail M4: pre-trust workspace failed; user may see the trust dialog",
			"handle", cfg.Spawn.Handle, "workdir", workdir, "err", werr)
	}

	// W2: construct adapter + driver. NewAdapter validates required
	// fields; we wire HomeDir/AttentionClient explicitly so the
	// adapter doesn't fall through to defaults that would break tests.
	adapter, err := claudecode.NewAdapter(claudecode.Config{
		AgentID: cfg.Spawn.ChildID,
		Workdir: workdir,
		Poster:  cfg.Client,
		Log:     cfg.Log,
	})
	if err != nil {
		return nil, fmt.Errorf("locallogtail M4: new adapter: %w", err)
	}
	// v1.0.673: on a `--resume` spawn the JSONL file already exists
	// with the prior session's transcript inline (claude-code APPENDS
	// to the original `<uuid>.jsonl` on resume rather than minting a
	// new one — verified by inspecting a real resumed JSONL on the
	// dev box). The prior agent already posted those lines under its
	// own agent_id; tailing from byte 0 under the resumed agent's id
	// would re-emit every assistant text + thought as a duplicate
	// under the new id, and mobile's session-view (which merges by
	// session_id) renders the result as a duplicated transcript.
	// StartFromEnd seeks to current EOF before the live tail begins,
	// skipping the historical bytes. Claude's own auto-injected
	// `Continue from where you left off.` user-meta + `No response
	// requested.` reply that fire during resume init typically land
	// in the still-loading window before the adapter attaches, so
	// they're skipped too; the mapper-side noise filter
	// (assistantTextNoise) catches any race-condition stragglers.
	if cmdContainsResumeFlag(spec.Backend.Cmd) {
		adapter.TailMode = claudecode.StartFromEnd
		cfg.Log.Info("locallogtail M4: --resume detected, tailing from end to avoid duplicating prior agent's transcript",
			"agent_id", cfg.Spawn.ChildID, "handle", cfg.Spawn.Handle)
	}
	// AttentionClient — host-runner-side client for parked-hook
	// coordination (W2i). Uses the per-spawn MCPToken so the hub's
	// attention API treats the call as the agent's.
	adapter.Attention = &claudecode.HubAttentionClient{
		HubURL:      cfg.HubURL,
		Team:        cfg.GatewayHubClient.Team,
		Token:       cfg.Spawn.MCPToken,
		AgentHandle: cfg.Spawn.Handle,
	}

	driver := &locallogtail.Driver{
		Config: locallogtail.Config{
			AgentID: cfg.Spawn.ChildID,
			Poster:  cfg.Client,
			Log:     cfg.Log,
		},
		Adapter: adapter,
	}

	// W5a: start the per-spawn UDS gateway BEFORE claude launches.
	// HookSink is set right after StartGateway returns — the
	// listener is bound by then but no client has connected yet.
	gw, gwCleanup, err := StartGateway(ctx, cfg.Spawn.ChildID, cfg.GatewayHubClient)
	if err != nil {
		return nil, fmt.Errorf("locallogtail M4: start gateway: %w", err)
	}
	gw.HookSink = driver
	// ADR-036 W2: wire the claude-code adapter as the gateway's
	// StatusLineSink. The gateway invokes adapter.OnStatusLine
	// synchronously after posting the status_line AgentEvent to the
	// hub; the adapter caches the snapshot for in-process field
	// overrides on subsequent JSONL-derived events (session.init's
	// version + usage's context_window). W3 will extend OnStatusLine
	// to detect session_id rotation and re-point the tailer.
	gw.StatusLineSink = adapter

	// Launch claude in tmux. Spawn command precedence: spec.Backend.Cmd
	// wins over template default. (Matches runner.go's existing M4
	// resolution ladder.)
	cmd := spec.Backend.Cmd
	if cmd == "" {
		return nil, gatewayTeardown(gwCleanup,
			fmt.Errorf("locallogtail M4: backend.cmd is empty"))
	}
	// Prepend `cd <workdir> &&` so claude's cwd deterministically
	// equals the workdir we resolved + the .mcp.json / settings.local.json
	// we just wrote — claude-code's pathresolver keys its session JSONL
	// by encoded-cwd (~/.claude/projects/<encoded-cwd>/), so the two
	// MUST agree. TmuxLauncher.LaunchCmd does NOT cd; the M1/M2 paths
	// build a `paneCmd` that wraps cd around the user cmd, and the agy
	// M4 path does the same (launch_m4_antigravity.go). Without this
	// prefix claude lands in the host-runner's cwd and writes its JSONL
	// somewhere the adapter's pathresolver never looks → the tail wait
	// stalls → "M4 LocalLogTail launch failed".
	cmd = fmt.Sprintf("cd %s && %s", shellEscape(workdir), cmd)
	pane, err := cfg.Launcher.LaunchCmd(ctx, cfg.Spawn, cmd)
	if err != nil {
		return nil, gatewayTeardown(gwCleanup, fmt.Errorf("locallogtail M4: tmux launch: %w", err))
	}
	adapter.PaneID = pane

	// Start the driver: emits lifecycle.started + Start the adapter
	// which begins waiting for the JSONL file to appear. The wait
	// happens in adapter.Start synchronously (W2d), so a slow claude
	// cold-start delays this call but doesn't deadlock the runner.
	if err := driver.Start(ctx); err != nil {
		return nil, gatewayTeardown(gwCleanup, fmt.Errorf("locallogtail M4: driver start: %w", err))
	}

	return &M4LocalLogTailLaunchResult{
		PaneID:        pane,
		Driver:        driver,
		Gateway:       gw,
		HostRunnerExe: hostRunnerExe,
	}, nil
}

// gatewayTeardown closes the gateway and forwards the underlying
// error. Used in failure paths to avoid leaking the listener.
func gatewayTeardown(cleanup func(), err error) error {
	if cleanup != nil {
		cleanup()
	}
	return err
}

// hostRunnerExePath returns the path the M4 LocalLogTail launcher
// writes into `.mcp.json` for claude-code's `termipod-host` shim
// command. Production wiring uses the basename "host-runner" so the
// shim resolves via PATH; tests / overrides can replace this hook
// in a follow-up wedge.
//
// We deliberately don't read os.Executable() because host-runner may
// be installed under multiple names (e.g. /usr/local/bin/host-runner
// + /usr/local/bin/hub-mcp-bridge multicall symlink). Using a
// basename keeps the resolution consistent with how
// `hub-mcp-bridge` is invoked from the same writeMCPConfig path.
func hostRunnerExePath() string {
	return "host-runner"
}

// guard against an accidental empty workdir slug producing a bogus
// path that points at ~/.claude/projects/-. Keeping this here means
// the W7 caller (runner.go) doesn't have to re-check.
func init() {
	if strings.Contains(hub.MCPServerName, "/") {
		// Should never happen — would corrupt every spawn's .mcp.json.
		// Panic-on-init is acceptable here because no test would
		// otherwise notice.
		panic("hub.MCPServerName must be a single identifier")
	}
}

// cmdContainsResumeFlag returns true when the rendered spawn cmd
// contains a top-level `--resume <id>` or `--resume=<id>` pair. The
// hub's spliceClaudeResume injects exactly this shape after the
// `claude` bin token when handleResumeSession threads the captured
// engine_session_id back into a fresh spawn (ADR-014). The M4 launch
// path uses the signal to switch the tail mode so the resumed
// adapter doesn't re-emit the prior agent's transcript. Splits on
// whitespace (claude doesn't accept quoted/escaped args in this
// position, and spliceClaudeResume never produces them), then scans
// for a bare `--resume` or a `--resume=…` prefix. v1.0.673.
func cmdContainsResumeFlag(cmd string) bool {
	for _, tok := range strings.Fields(cmd) {
		if tok == "--resume" || strings.HasPrefix(tok, "--resume=") {
			return true
		}
	}
	return false
}

// preTrustWorkspaceClaudeCode adds workdir to claude-code's per-project
// trust list in ~/.claude.json so the "Do you trust the files in this
// folder?" welcome-screen dialog never fires for spawned agents.
//
// claude-code persists trust per absolute path inside its top-level
// JSON config (host-verified on the dev box):
//
//	{
//	  "numStartups": 30,
//	  "projects": {
//	    "/some/dir": {
//	      "hasTrustDialogAccepted": true,
//	      "hasCompletedProjectOnboarding": true,
//	      ...other per-project keys...
//	    }
//	  },
//	  ...other top-level keys...
//	}
//
// We touch only the per-project entry for `workdir` — `hasTrustDialogAccepted`
// + `hasCompletedProjectOnboarding` — and preserve every other field
// (user's other projects, allowedTools, history, lastCost, etc.).
//
// Why launch-time, not template/install: the workdir is dynamic (per
// project / per handle), so the trust list has to grow as agents spawn.
// Pre-trusting "~/hub-work" globally would be ideal but claude-code
// keys by absolute path, not prefix, so per-spawn pre-grant is the
// only path that doesn't require user intervention.
//
// Best-effort: a missing ~/.claude.json is treated as the empty config
// (the function creates it with just the projects entry); a malformed
// one falls through with the parse error and the user gets the dialog
// this once. The function never returns a launch-blocking error from
// the caller's perspective — the caller logs and continues either way.
func preTrustWorkspaceClaudeCode(workdir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve HOME: %w", err)
	}
	path := filepath.Join(home, ".claude.json")

	clean := filepath.Clean(workdir)

	cfg := map[string]any{}
	if b, rerr := os.ReadFile(path); rerr == nil && len(bytes.TrimSpace(b)) > 0 {
		if jerr := json.Unmarshal(b, &cfg); jerr != nil {
			return fmt.Errorf("parse existing ~/.claude.json: %w", jerr)
		}
	}

	projects, _ := cfg["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
		cfg["projects"] = projects
	}

	entry, _ := projects[clean].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
		projects[clean] = entry
	}

	// Short-circuit if both flags are already set — re-spawn no-op.
	if accepted, _ := entry["hasTrustDialogAccepted"].(bool); accepted {
		if onboarded, _ := entry["hasCompletedProjectOnboarding"].(bool); onboarded {
			return nil
		}
	}

	entry["hasTrustDialogAccepted"] = true
	entry["hasCompletedProjectOnboarding"] = true

	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o600)
}
