/// How a local codex session reaches an app-server (vision-parity **L4a/L4b**).
///
/// codex speaks the same JSON-RPC app-server protocol whichever way we reach
/// it, so the only real decision is WHICH TRANSPORT we open — and that decision
/// is a pure function of what is installed. This module is that function;
/// `codexchannel.ts` opens what it returns, and the driver that speaks the
/// protocol is L4c.
///
/// ## The two rungs, as they actually exist (codex-cli 0.147.0, measured)
///
///  - **`daemon`** — a shared, long-lived app server. `codex app-server daemon
///    start` brings it up (idempotent by its own definition: "start ... if it
///    is not already running") and it listens as a **WebSocket server bound to
///    a Unix domain socket** — its own argv is `codex app-server --listen
///    unix://`. A session on the daemon outlives our app and is shared with
///    the vendor's own TUI.
///  - **`spawn`** — `codex app-server`, one child per session, speaking
///    line-delimited JSON-RPC on stdio and dying with us.
///
/// ## The daemon rung is a socket, NOT an argv — and that is L4a's correction
///
/// L4a shipped this rung as `codex app-server proxy --sock <path>`, on the
/// strength of that subcommand's summary ("Proxy stdio bytes to the running
/// app-server control socket"). **It does not work, and it fails silently.**
/// Measured by sitting a logging relay between the CLI and the daemon:
///
///  - `daemon version` — the vendor's own client for this socket — opens with
///    `GET / HTTP/1.1` + `Upgrade: websocket` + `Sec-WebSocket-Version: 13`,
///    gets `101 Switching Protocols`, and only then speaks JSON-RPC inside
///    WebSocket frames.
///  - `app-server proxy --sock` sends **our stdin bytes verbatim**, with no
///    upgrade. The daemon closes the connection on the first byte of anything
///    that is not an HTTP upgrade — valid JSON-RPC and pure garbage are closed
///    identically, so this is not a parse error. The proxy then exits **0 with
///    no stdout and no stderr**, which reads exactly like "the agent had
///    nothing to say".
///
/// So the plan's original L4 line was **right that this is a WebSocket** and
/// L4a's "it is not a WebSocket" correction was wrong; what the plan got wrong
/// was the *authentication* ("its bearer scheme") and the *address family*.
/// There is no token: the socket is created `srw-------` and **filesystem
/// permissions are the auth**. `--listen` does accept `ws://IP:PORT` for a TCP
/// WebSocket, but the daemon does not use it, and nothing here should.
///
/// The type below encodes that: a `daemon` plan carries a `socketPath` and
/// **cannot** carry an argv. The shipped bug is now unrepresentable.
///
/// ## Two further measured constraints, unchanged from L4a
///
///  1. **We cannot "spawn it detached if absent".** `daemon start` refuses
///     unless codex was installed by the official installer script — it wants
///     the managed binary at `<CODEX_HOME>/packages/standalone/current/codex`
///     and says so:
///     *"managed standalone Codex install not found ... This command requires
///     the standalone install managed by the Codex installer, because the
///     daemon starts and updates app-server from that fixed path."*
///     An npm / homebrew / distro codex therefore has **no daemon rung at
///     all**, so per-session stdio is not "the fallback rung only" — for the
///     common install it is the only rung, and `daemon` is an opportunistic
///     upgrade.
///  2. **`SUN_LEN`.** The control socket path is subject to the platform's cap
///     (~104–108 bytes). A deeply relocated `CODEX_HOME` fails at CONNECT time
///     with *"path must be shorter than SUN_LEN"* — a message no one would
///     attribute to path length — so a long path disqualifies the daemon rung
///     here, where we can say why.
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

/// Conservative bound on a Unix domain socket path. The real cap is
/// `sizeof(sun_path)` — 108 on Linux, 104 on macOS — so we take the smaller and
/// leave room rather than probe per platform.
export const MAX_UNIX_SOCKET_PATH = 104;

export type CodexAttachMode = 'daemon' | 'spawn';

/// Where the app-server protocol will flow. A discriminated union because the
/// two rungs are not the same kind of thing: one is a socket we open, the other
/// is a process we spawn. L4a modelled both as `argv` and shipped a daemon rung
/// that could never carry a byte.
export type CodexAttachPlan =
  | {
      mode: 'daemon';
      /// The Unix domain socket to open a **WebSocket** on. Not an argv: no
      /// child process is involved in the data path at all.
      socketPath: string;
      /// Must succeed FIRST — brings the shared daemon up if it is not already
      /// running. This one IS an argv.
      startArgv: string[];
      reason: string;
    }
  | {
      mode: 'spawn';
      /// Argv for the child whose stdio carries the protocol.
      argv: string[];
      reason: string;
    };

/// The daemon's control socket, given a codex home.
export function controlSocketPath(codexHome: string): string {
  return path.join(codexHome, 'app-server-control', 'app-server-control.sock');
}

/// The managed standalone binary `daemon start` insists on. (It is a symlink to
/// `bin/codex` in the same tree, so it is both the path the daemon reports and
/// a runnable binary.)
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

/// Directories a codex install lands in that a GUI-launched app will not have
/// on its PATH. The official installer writes `~/.local/bin/codex` and appends
/// its PATH line to `.bashrc` — which an Electron app started from a Dock or
/// Start-menu icon never sources, so the standalone install is *less*
/// discoverable than a distro package was. Same class of problem `kimiweb.ts`
/// solves for kimi.
export function codexBinDirs(home: string, env: NodeJS.ProcessEnv = process.env): string[] {
  const dirs = [
    path.join(home, '.local', 'bin'),
    path.join(codexHome(home, env), 'packages', 'standalone', 'current', 'bin'),
  ];
  if (process.platform === 'win32') {
    dirs.push(path.join(env['APPDATA'] ?? path.join(home, 'AppData', 'Roaming'), 'npm'));
  }
  return dirs;
}

/// The launcher filenames a codex install creates, in runnable order. `.ps1` is
/// excluded for the reason kimiweb.ts documents: `cmd /C x.ps1` opens it, it
/// does not run it.
const WIN_CODEX_NAMES = ['codex.cmd', 'codex.exe', 'codex.bat'];

/// Resolve codex to an ABSOLUTE path by scanning a PATH value plus the
/// well-known dirs. Pure (the existence check is injected) and exported so the
/// discoverability rule is testable without an install. Returns null when
/// nothing matches — callers may still fall back to the bare name and let the
/// OS try.
export function findCodexOnPath(
  pathValue: string | undefined,
  extraDirs: string[] = [],
  exists: (p: string) => boolean = fs.existsSync,
): string | null {
  const names = process.platform === 'win32' ? WIN_CODEX_NAMES : ['codex'];
  const dirs = [...(pathValue ?? '').split(path.delimiter), ...extraDirs];
  for (const raw of dirs) {
    const dir = raw.trim();
    if (dir === '') continue;
    for (const name of names) {
      const p = path.join(dir, name);
      if (exists(p)) return p;
    }
  }
  return null;
}

/// The codex binary to run: an explicit override, else a real file found on
/// PATH or in a well-known dir, else the bare name.
export function codexBinary(
  home: string = os.homedir(),
  env: NodeJS.ProcessEnv = process.env,
  exists: (p: string) => boolean = fs.existsSync,
): string {
  const explicit = env['TERMIPOD_CODEX_BIN'];
  if (explicit !== undefined && explicit !== '') return explicit;
  return findCodexOnPath(env['PATH'], codexBinDirs(home, env), exists) ?? 'codex';
}

export interface CodexAttachProbe {
  /// Does `<codexHome>/packages/standalone/current/codex` exist? The caller
  /// supplies this (an `fs.existsSync`) so the decision itself stays pure.
  managedInstallPresent: boolean;
  /// Resolved codex binary. Defaults to the bare name so the decision table can
  /// be tested with no install on the box.
  bin?: string;
}

/// Choose the rung. Pure: every input is a value, so the whole decision table
/// is unit-testable without a codex on the box.
export function planCodexAttach(
  home: string,
  probe: CodexAttachProbe,
  env: NodeJS.ProcessEnv = process.env,
): CodexAttachPlan {
  const codexBin = probe.bin ?? env['TERMIPOD_CODEX_BIN'] ?? 'codex';
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
    socketPath: sock,
    startArgv: [codexBin, 'app-server', 'daemon', 'start'],
    reason: 'attached to the shared codex app-server daemon; this session survives the app and is visible to the codex TUI',
  };
}
