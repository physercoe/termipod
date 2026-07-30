/// User-level `~/.kimi-code/mcp.json` injection for the desktop UI context
/// loop (D1 — docs/plans/desktop-ui-context-and-pointing.md §3.5).
///
/// kimi-code discovers MCP servers at two levels (ADR-054 D3): user
/// (`~/.kimi-code/mcp.json`) and project (`<cwd>/.kimi-code/mcp.json`). The
/// desktop spawns the `kimi web` SERVER but sessions pick their own
/// workspaces in kimi's UI, so project-level injection can't reach them —
/// while the UI-context sharing toggle is on, the desktop deep-merges ONE
/// additive entry (`termipod-desktop`, the same stdio relay as the bridge)
/// into the user-level file and removes it when the toggle goes off. The
/// entry carries NO env: the relay's discovery-file fallback resolves the
/// per-run URL + READ token itself, so the static entry survives token
/// rotation.
///
/// Two pinned constraints (review amendments):
///   - the entry points at a STABLE COPY of the relay under
///     `~/.termipod/bridge/` — never `process.resourcesPath`, which is a
///     fresh `/tmp/.mount_*` per launch on Linux AppImage (and quietly goes
///     stale across updates elsewhere). The copy is refreshed on toggle-on /
///     every app start via the boot push;
///   - the user-level file is SHARED territory: the merge is additive-only
///     (foreign keys and foreign servers pass through untouched), round-trip
///     safe (merge → remove restores the prior config), atomic (tmp +
///     rename), and a corrupt file is left untouched and logged — never
///     clobbered.
///
/// Electron-free (like browserbridge.ts) so the whole lifecycle is unit-tested
/// under plain `node --test`; the glue lives in desktopui.ts.
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

/// The one server name the desktop owns in the user's mcp.json. Only this key
/// is ever added or removed.
export const KIMI_MCP_ENTRY_NAME = 'termipod-desktop';

export function kimiMcpConfigPath(home: string = os.homedir()): string {
  return path.join(home, '.kimi-code', 'mcp.json');
}

/// Where the stable relay copy lives. `~/.termipod/` is created 0o700 by the
/// discovery-file writer already (the directory holds a bearer at
/// browser-bridge.json, so other users must stay out of it).
export function stableRelayCopyPath(home: string = os.homedir()): string {
  return path.join(home, '.termipod', 'bridge', 'browser_bridge_stdio.mjs');
}

/// The additive entry: stdio, plain node + the stable relay copy, no env —
/// the relay's fallback does discovery (read token only, plan §3.5).
export function sharingEntry(home: string): { command: string; args: string[] } {
  return { command: 'node', args: [stableRelayCopyPath(home)] };
}

/// (Re)copy the relay script to its stable home. Overwrites so an app update
/// refreshes the copy on the next start. Returns the copy path.
export function installStableRelay(home: string, relaySourcePath: string): string {
  const target = stableRelayCopyPath(home);
  fs.mkdirSync(path.dirname(target), { recursive: true, mode: 0o700 });
  fs.copyFileSync(relaySourcePath, target);
  return target;
}

export type KimiMcpWrite = 'written' | 'noop' | 'corrupt';

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return v !== null && typeof v === 'object' && !Array.isArray(v);
}

/// Read + parse the config. `null` = absent (an empty config is seeded on
/// write); 'corrupt' = present but unparseable / not a JSON object / a
/// non-object mcpServers — the caller leaves the file byte-identical.
function readConfig(target: string): Record<string, unknown> | null | 'corrupt' {
  let raw: string;
  try {
    raw = fs.readFileSync(target, 'utf8');
  } catch (e) {
    // Absent is the one benign case. Any OTHER read failure (EACCES, EISDIR,
    // I/O) means something EXISTS at the path whose contents we cannot see —
    // a merge would reseed a fresh config over it, so it gets the corrupt
    // treatment: left untouched.
    return (e as NodeJS.ErrnoException).code === 'ENOENT' ? null : 'corrupt';
  }
  try {
    const parsed: unknown = JSON.parse(raw);
    if (!isPlainObject(parsed)) return 'corrupt';
    if ('mcpServers' in parsed && !isPlainObject(parsed.mcpServers)) return 'corrupt';
    return parsed;
  } catch {
    return 'corrupt';
  }
}

/// Atomic-ish write (sibling tmp + rename — a reader racing us never sees a
/// torn file), 0o600 like the other mcp.json writers (the file can carry
/// other servers' tokens).
function writeConfig(target: string, cfg: Record<string, unknown>): void {
  fs.mkdirSync(path.dirname(target), { recursive: true, mode: 0o700 });
  const tmp = `${target}.tmp-${process.pid}`;
  fs.writeFileSync(tmp, `${JSON.stringify(cfg, null, 2)}\n`, { mode: 0o600 });
  fs.renameSync(tmp, target);
}

/// Deep-merge the ONE additive termipod-desktop entry, preserving every
/// foreign key and server. Idempotent — toggle-on and the per-start refresh
/// both land here.
export function mergeSharingEntry(home: string, log: (msg: string) => void = console.warn): KimiMcpWrite {
  const target = kimiMcpConfigPath(home);
  const cfg = readConfig(target);
  if (cfg === 'corrupt') {
    log(`desktopui: leaving corrupt ${target} untouched`);
    return 'corrupt';
  }
  const next = cfg ?? {};
  const servers = (next.mcpServers ?? {}) as Record<string, unknown>;
  servers[KIMI_MCP_ENTRY_NAME] = sharingEntry(home);
  next.mcpServers = servers;
  writeConfig(target, next);
  return 'written';
}

/// Remove ONLY the termipod-desktop entry (toggle-off). Absent file / absent
/// entry → noop; corrupt file → untouched + logged.
export function removeSharingEntry(home: string, log: (msg: string) => void = console.warn): KimiMcpWrite {
  const target = kimiMcpConfigPath(home);
  const cfg = readConfig(target);
  if (cfg === 'corrupt') {
    log(`desktopui: leaving corrupt ${target} untouched`);
    return 'corrupt';
  }
  if (cfg === null) return 'noop';
  const servers = cfg.mcpServers as Record<string, unknown> | undefined;
  if (servers === undefined || !(KIMI_MCP_ENTRY_NAME in servers)) return 'noop';
  delete servers[KIMI_MCP_ENTRY_NAME];
  writeConfig(target, cfg);
  return 'written';
}
