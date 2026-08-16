/// How a local codex session reaches an app-server (vision-parity **L4a**).
///
/// codex speaks the same JSON-RPC-over-stdio app-server protocol whichever way
/// we reach it, so the only real decision is WHICH PROCESS we spawn — and that
/// decision is a pure function of what is installed. This module is that
/// function; the driver that speaks the protocol is L4b.
///
/// ## The two rungs, as they actually exist (codex-cli 0.147.0, measured)
///
///  - **`daemon`** — a shared, long-lived app server. `codex app-server daemon
///    start` brings it up (idempotent by its own definition: "start ... if it
///    is not already running"), and `codex app-server proxy --sock <path>`
///    pipes our stdio to its **Unix domain control socket**. A session on the
///    daemon outlives our app and is shared with the vendor's own TUI.
///  - **`spawn`** — `codex app-server`, one child per session, dying with us.
///
/// ## Three corrections to the plan's L4 line, all measured
///
///  1. **It is not a WebSocket and there is no bearer scheme.** The vendor's
///     shared-daemon transport is a Unix domain socket reached through a stdio
///     proxy. `--code-mode-host <WS_URL>` exists but is a different feature
///     (where *code mode* runs), not the session transport. Nothing here needs
///     a WebSocket client, which is why this module hands back argv instead.
///  2. **We cannot "spawn it detached if absent".** `daemon start` refuses
///     unless codex was installed by the official installer script — it wants
///     the managed binary at `<CODEX_HOME>/packages/standalone/current/codex`
///     and says so:
///     *"managed standalone Codex install not found ... This command requires
///     the standalone install managed by the Codex installer, because the
///     daemon starts and updates app-server from that fixed path."*
///     An npm / homebrew / distro codex therefore has **no daemon rung at
///     all**.
///  3. **So the rung order inverts.** The plan called stdio spawn "the fallback
///     rung only"; for the common install it is the ONLY rung. `daemon` is an
///     opportunistic upgrade we take when the managed install is present.
///
/// Also measured: the control socket path is subject to the platform's
/// `SUN_LEN` cap (~108 bytes). A relocated `CODEX_HOME` nested deeply enough
/// makes the daemon unreachable with *"path must be shorter than SUN_LEN"* —
/// so a long path disqualifies the daemon rung rather than failing later, at
/// connect time, with a message no one would attribute to path length.
import path from 'node:path';

/// Conservative bound on a Unix domain socket path. The real cap is
/// `sizeof(sun_path)` — 108 on Linux, 104 on macOS — so we take the smaller and
/// leave room rather than probe per platform.
export const MAX_UNIX_SOCKET_PATH = 104;

export type CodexAttachMode = 'daemon' | 'spawn';

export interface CodexAttachPlan {
  mode: CodexAttachMode;
  /// Argv for the process whose stdio carries the app-server protocol.
  argv: string[];
  /// The command that must succeed FIRST for `mode: 'daemon'` — bringing the
  /// shared daemon up. Absent for `spawn`, which needs no preparation.
  startArgv?: string[];
  /// Why this rung, in a sentence, for the session record and the UI. A user
  /// whose session is not shared with their TUI should be able to find out why
  /// without reading code.
  reason: string;
}

/// The daemon's control socket, given a codex home.
export function controlSocketPath(codexHome: string): string {
  return path.join(codexHome, 'app-server-control', 'app-server-control.sock');
}

/// The managed standalone binary `daemon start` insists on.
export function managedCodexPath(codexHome: string): string {
  return path.join(codexHome, 'packages', 'standalone', 'current', 'codex');
}

/// `CODEX_HOME` relocates the whole codex home, exactly as `CLAUDE_CONFIG_DIR`
/// does for claude (usermcp.ts resolves the same pair) — so neither the socket
/// nor the managed-binary probe may assume `~/.codex`.
export function codexHome(home: string, env: NodeJS.ProcessEnv = process.env): string {
  const dir = env['CODEX_HOME'];
  return dir !== undefined && dir !== '' ? dir : path.join(home, '.codex');
}

export interface CodexAttachProbe {
  /// Does `<codexHome>/packages/standalone/current/codex` exist? The caller
  /// supplies this (an `fs.existsSync`) so the decision itself stays pure.
  managedInstallPresent: boolean;
}

/// Choose the rung. Pure: every input is a value, so the whole decision table
/// is unit-testable without a codex on the box.
export function planCodexAttach(
  home: string,
  probe: CodexAttachProbe,
  env: NodeJS.ProcessEnv = process.env,
): CodexAttachPlan {
  const codexBin = env['TERMIPOD_CODEX_BIN'] ?? 'codex';
  const chome = codexHome(home, env);
  const sock = controlSocketPath(chome);
  if (!probe.managedInstallPresent) {
    return {
      mode: 'spawn',
      argv: [codexBin, 'app-server'],
      reason:
        'no installer-managed codex at ' +
        managedCodexPath(chome) +
        ', so `app-server daemon start` would refuse; running a per-session app server instead',
    };
  }
  if (sock.length > MAX_UNIX_SOCKET_PATH) {
    return {
      mode: 'spawn',
      argv: [codexBin, 'app-server'],
      reason: `control socket path is ${String(sock.length)} bytes, over the ~${String(MAX_UNIX_SOCKET_PATH)}-byte limit for a Unix socket; running a per-session app server instead`,
    };
  }
  return {
    mode: 'daemon',
    argv: [codexBin, 'app-server', 'proxy', '--sock', sock],
    startArgv: [codexBin, 'app-server', 'daemon', 'start'],
    reason: 'attached to the shared codex app-server daemon; this session survives the app and is visible to the codex TUI',
  };
}
