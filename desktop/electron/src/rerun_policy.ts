import fs from 'node:fs';
import path from 'node:path';

/// The pure half of the Rerun companion (J8 Replay W4) — what to launch, where
/// it will be reachable, and what a recording path is allowed to be.
///
/// Split from `rerun.ts` the same way `media_policy.ts` is split from
/// `mediascheme.ts` and `webtab_policy.ts` from `webtab.ts`: node builtins only,
/// no local imports, so the whole decision surface runs under plain
/// `node --test`. The manager beside it spawns processes and cannot.
///
/// Every flag here was read off rerun's own CLI definition
/// (`crates/top/rerun/src/commands/entrypoint.rs`) and the viewer URL off
/// `crates/top/re_sdk/src/web_viewer.rs`, rather than remembered. What has NOT
/// happened is a live run: no rerun process has been started against this code.

/// Loading a recording parses the whole file, so this is generous next to
/// kimi's 15s — a multi-camera episode is tens of megabytes.
export const RERUN_START_TIMEOUT_MS = 40_000;

/// The bind address, and NOT a default worth trusting.
///
/// Rerun's `WebViewerConfig.bind_ip` defaults to **0.0.0.0**
/// (`re_sdk/src/web_viewer.rs` — "Defaults to 0.0.0.0"), so a `--serve-web`
/// started without this flag publishes the robot's episodes, video and all, to
/// every machine on the network — and looks perfectly fine to whoever launched
/// it. Passing it explicitly is the reason this argv is built here rather than
/// inline at the spawn.
export const RERUN_BIND = '127.0.0.1';

/// The launcher filenames a rerun install creates. `pip install rerun-sdk`
/// writes a `rerun` console script; a cargo install writes the bare binary.
const WIN_RERUN_NAMES = ['rerun.exe', 'rerun.cmd', 'rerun.bat'];

export interface RerunPorts {
  webPort: number;
  grpcPort: number;
}

/// Whether a path is something rerun may be handed.
///
/// Absolute, because the spawn's working directory is the app's and not the
/// user's, so a relative path would name a different file than whoever passed
/// it meant. `.rrd`, because pointing rerun at an arbitrary file is a way to
/// turn a path we were given into a process argument.
export function isRecordingPath(p: string): boolean {
  const trimmed = p.trim();
  if (trimmed === '') return false;
  if (!path.isAbsolute(trimmed) && !path.win32.isAbsolute(trimmed)) return false;
  return trimmed.toLowerCase().endsWith('.rrd');
}

/// The argv for `rerun`, serving one recording over a loopback web viewer.
///
/// `--serve-web` with a file positional is rerun's own documented form ("Host a
/// Rerun Server which serves a recording from a file over gRPC to any
/// connecting Rerun Viewers: `rerun --serve-web recording.rrd`",
/// entrypoint.rs). The two ports must differ — rerun compares them and bails —
/// which is why the caller picks both instead of letting one default.
export function rerunArgs(recording: string, ports: RerunPorts): string[] {
  if (ports.webPort === ports.grpcPort) {
    throw new Error(`the web-viewer port and the gRPC port must differ (both ${ports.webPort})`);
  }
  return [
    '--serve-web',
    '--bind',
    RERUN_BIND,
    '--port',
    String(ports.grpcPort),
    '--web-viewer-port',
    String(ports.webPort),
    recording,
  ];
}

/// The URL the web viewer is reachable at, given the ports we chose.
///
/// Constructed rather than only parsed, because we pick the ports and rerun's
/// formatting is fixed: `re_sdk/src/web_viewer.rs` builds
/// `"{server_url}?url=rerun%2Bhttp://{grpc_addr}/proxy"`, and `server_url()` is
/// `http://<addr>` with no trailing slash — hence the `?` directly after the
/// port. The parse below is preferred when it fires, since rerun is the
/// authority on its own URL; this is the fallback for a log line that never
/// arrives in a shape we recognise.
export function viewerUrl(ports: RerunPorts): string {
  return `http://${RERUN_BIND}:${ports.webPort}?url=rerun%2Bhttp://${RERUN_BIND}:${ports.grpcPort}/proxy`;
}

/// Pull the viewer URL out of rerun's startup log.
///
/// It logs `"Hosting a web-viewer at {bound} - connect at {viewer}"`
/// (`re_sdk/src/web_viewer.rs`) through `re_log`, i.e. on **stderr**. Matching
/// the `?url=` URL itself rather than the "connect at" wording means a reworded
/// banner does not break the parse — the rule `extractServerUrl` already uses
/// for kimi. The bare bound URL earlier in the same line has no query string,
/// so it cannot be mistaken for the viewer one.
export function extractViewerUrl(text: string): string | null {
  const m = /https?:\/\/[^\s"']+\?url=[^\s"']+/.exec(text);
  return m === null ? null : m[0];
}

/// Resolve the rerun launcher to an absolute path.
///
/// `$TERMIPOD_RERUN_BIN` wins when set, and a set-but-missing override resolves
/// to null rather than falling through to PATH: rerun is commonly installed
/// into a virtualenv that is on nobody's global PATH, and someone who pointed
/// us somewhere specific deserves to be told it is not there rather than to get
/// a different rerun than they asked for.
export function resolveRerunBinary(
  env: NodeJS.ProcessEnv = process.env,
  exists: (p: string) => boolean = fs.existsSync,
): string | null {
  const explicit = (env.TERMIPOD_RERUN_BIN ?? '').trim();
  if (explicit !== '') return exists(explicit) ? explicit : null;
  const names = process.platform === 'win32' ? WIN_RERUN_NAMES : ['rerun'];
  for (const raw of (env.PATH ?? '').split(path.delimiter)) {
    const dir = raw.trim();
    if (dir === '') continue;
    for (const name of names) {
      const p = path.join(dir, name);
      if (exists(p)) return p;
    }
  }
  return null;
}
