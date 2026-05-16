// M4 launch path that uses the LocalLogTailDriver (ADR-027) — W5a +
// W7. Only claude-code is wired today; other engines stay on the
// PaneDriver M4 path until their adapters land (gemini-cli, codex,
// kimi-code: Phase 2/3 of ADR-027). The runner falls back to
// PaneDriver M4 if any step here fails, so a misconfigured spawn
// degrades gracefully rather than erroring out.
package hostrunner

import (
	"context"
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
		rawWD = filepath.Join("~", "hub-work", pid, handle)
	}
	if rawWD == "" {
		return nil, fmt.Errorf("locallogtail M4: backend.default_workdir empty and no project_id to derive (JSONL path resolves under <workdir>)")
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

	// W6: settings.local.json hooks pointing at mcp__termipod-host__*.
	if err := installClaudeHooks(workdir); err != nil {
		return nil, fmt.Errorf("locallogtail M4: install hooks: %w", err)
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

	// Launch claude in tmux. Spawn command precedence: spec.Backend.Cmd
	// wins over template default. (Matches runner.go's existing M4
	// resolution ladder.)
	cmd := spec.Backend.Cmd
	if cmd == "" {
		return nil, gatewayTeardown(gwCleanup,
			fmt.Errorf("locallogtail M4: backend.cmd is empty"))
	}
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
