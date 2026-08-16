/// User-level MCP reseed for the desktop UI-context loop — kimi-code, claude
/// and codex (D1 §3.5 for kimi; vision-parity **F4** for the other two).
///
/// While the UI-context sharing toggle is on, the desktop deep-merges ONE
/// additive entry (`termipod-desktop`, the same stdio relay as the bridge) into
/// each engine's USER-level MCP config, and removes it when the toggle goes
/// off. That is what lets an **ad-hoc** session — one the user started
/// themselves, not one the hub spawned — pull desktop UI context. The entry
/// carries NO env: the relay's discovery-file fallback resolves the per-run URL
/// + READ token itself, so a static entry survives token rotation.
///
/// Three files, three formats, all verified against the vendors' own CLIs
/// rather than from documentation (`codex mcp add`, `claude mcp add -s user`,
/// run against throwaway config homes):
///
///   | engine | file                      | shape                            |
///   |--------|---------------------------|----------------------------------|
///   | kimi   | `~/.kimi-code/mcp.json`   | JSON `mcpServers`                |
///   | claude | `<cfg>/.claude.json`      | JSON `mcpServers`, `type:"stdio"`|
///   | codex  | `<CODEX_HOME>/config.toml`| TOML `[mcp_servers.<name>]`      |
///
/// Two pinned constraints (review amendments, D1) hold for all three:
///   - the entry points at a STABLE COPY of the relay under
///     `~/.termipod/bridge/` — never `process.resourcesPath`, which is a fresh
///     `/tmp/.mount_*` per launch on Linux AppImage (and quietly goes stale
///     across updates elsewhere);
///   - these files are SHARED territory: every write is additive-only (foreign
///     keys and foreign servers pass through untouched), round-trip safe
///     (merge → remove restores the prior config), atomic (tmp + rename), and a
///     file we cannot parse confidently is left untouched and reported — never
///     clobbered.
///
/// ⚠ **`.claude.json` is claude's live state file**, not a config-only file: it
/// carries caches, ids and project history (85 KiB on a working machine, with
/// no `mcpServers` key at all until something adds one). Claude Code rewrites
/// it whole, so an external read-modify-write races its writer and last-writer
/// wins. We keep the window as small as possible and write atomically, which is
/// the same exposure `claude mcp add -s user` itself has — but it is why this
/// module never rewrites that file except to add or drop its own one key.
///
/// Electron-free (like browserbridge.ts) so the whole lifecycle is unit-tested
/// under plain `node --test`; the glue lives in desktopui.ts.
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

/// The one server name the desktop owns in each user config. Only this key is
/// ever added or removed.
export const MCP_ENTRY_NAME = 'termipod-desktop';

/// The engines whose user-level config we reseed.
export type McpEngine = 'kimi' | 'claude' | 'codex';

/// 'unsupported' is the codex-only outcome: the file expresses `mcp_servers` in
/// a TOML form this splicer will not edit (an inline table or a dotted key). It
/// is a *deliberate* refusal, not a failure — see `spliceTomlEntry`.
export type McpWrite = 'written' | 'noop' | 'corrupt' | 'unsupported';

export type ReseedResult = Record<McpEngine, McpWrite>;

// ── paths ────────────────────────────────────────────────────────────────────

export function kimiMcpConfigPath(home: string = os.homedir()): string {
  return path.join(home, '.kimi-code', 'mcp.json');
}

/// claude's user-scope config. `CLAUDE_CONFIG_DIR` relocates the whole config
/// home and is **per-account** — the local-agent store already persists it for
/// exactly that reason (localagent/store.ts) — so resolving it blindly to
/// `~/.claude.json` would reseed the wrong account's file, or none.
export function claudeMcpConfigPath(
  home: string = os.homedir(),
  env: NodeJS.ProcessEnv = process.env,
): string {
  const dir = env['CLAUDE_CONFIG_DIR'];
  return path.join(dir !== undefined && dir !== '' ? dir : home, '.claude.json');
}

/// codex's config. `CODEX_HOME` relocates it the same way.
export function codexMcpConfigPath(
  home: string = os.homedir(),
  env: NodeJS.ProcessEnv = process.env,
): string {
  const dir = env['CODEX_HOME'];
  return dir !== undefined && dir !== '' ? path.join(dir, 'config.toml') : path.join(home, '.codex', 'config.toml');
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

/// claude's own `mcp add` writes an explicit `type` discriminator alongside
/// command/args (measured). We match it — an entry that declares its transport
/// is not relying on claude's default staying stdio. `env` is deliberately
/// omitted rather than written as `{}`: the constraint is no env, and an empty
/// map states nothing.
export function claudeSharingEntry(home: string): { type: 'stdio'; command: string; args: string[] } {
  return { type: 'stdio', ...sharingEntry(home) };
}

/// (Re)copy the relay script to its stable home. Overwrites so an app update
/// refreshes the copy on the next start. Returns the copy path.
export function installStableRelay(home: string, relaySourcePath: string): string {
  const target = stableRelayCopyPath(home);
  fs.mkdirSync(path.dirname(target), { recursive: true, mode: 0o700 });
  fs.copyFileSync(relaySourcePath, target);
  return target;
}

// ── JSON targets (kimi, claude) ──────────────────────────────────────────────

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return v !== null && typeof v === 'object' && !Array.isArray(v);
}

/// Read + parse a JSON config. `null` = absent (an empty config is seeded on
/// write); 'corrupt' = present but unparseable / not a JSON object / a
/// non-object mcpServers — the caller leaves the file byte-identical.
function readJsonConfig(target: string): Record<string, unknown> | null | 'corrupt' {
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
/// torn file), 0o600 like the other mcp.json writers (the file can carry other
/// servers' tokens, and `.claude.json` carries account state).
function writeFileAtomic(target: string, body: string): void {
  fs.mkdirSync(path.dirname(target), { recursive: true, mode: 0o700 });
  const tmp = `${target}.tmp-${process.pid}`;
  fs.writeFileSync(tmp, body, { mode: 0o600 });
  fs.renameSync(tmp, target);
}

function mergeJsonEntry(target: string, entry: object, log: (msg: string) => void): McpWrite {
  const cfg = readJsonConfig(target);
  if (cfg === 'corrupt') {
    log(`usermcp: leaving unreadable ${target} untouched`);
    return 'corrupt';
  }
  const next = cfg ?? {};
  const servers = (next.mcpServers ?? {}) as Record<string, unknown>;
  servers[MCP_ENTRY_NAME] = entry;
  next.mcpServers = servers;
  writeFileAtomic(target, `${JSON.stringify(next, null, 2)}\n`);
  return 'written';
}

function removeJsonEntry(target: string, log: (msg: string) => void): McpWrite {
  const cfg = readJsonConfig(target);
  if (cfg === 'corrupt') {
    log(`usermcp: leaving unreadable ${target} untouched`);
    return 'corrupt';
  }
  if (cfg === null) return 'noop';
  const servers = cfg.mcpServers as Record<string, unknown> | undefined;
  if (servers === undefined || !(MCP_ENTRY_NAME in servers)) return 'noop';
  delete servers[MCP_ENTRY_NAME];
  writeFileAtomic(target, `${JSON.stringify(cfg, null, 2)}\n`);
  return 'written';
}

// ── TOML target (codex) ──────────────────────────────────────────────────────

/// Escape a TOML basic string. Backslashes matter: a Windows relay path is full
/// of them, and unescaped they would read as escape sequences.
export function tomlString(v: string): string {
  let out = '';
  for (const ch of v) {
    if (ch === '\\') out += '\\\\';
    else if (ch === '"') out += '\\"';
    else if (ch === '\n') out += '\\n';
    else if (ch === '\r') out += '\\r';
    else if (ch === '\t') out += '\\t';
    else if (ch < ' ') out += `\\u${ch.charCodeAt(0).toString(16).padStart(4, '0')}`;
    else out += ch;
  }
  return `"${out}"`;
}

/// A TOML bare key if the name allows one (`A-Za-z0-9_-`), else quoted.
function tomlKey(name: string): string {
  return /^[A-Za-z0-9_-]+$/.test(name) ? name : tomlString(name);
}

/// Our table block, exactly the shape `codex mcp add` writes (measured).
export function codexEntryBlock(home: string): string {
  const e = sharingEntry(home);
  const args = e.args.map(tomlString).join(', ');
  return `[mcp_servers.${tomlKey(MCP_ENTRY_NAME)}]\ncommand = ${tomlString(e.command)}\nargs = [${args}]\n`;
}

function isTableHeader(line: string): boolean {
  return line.trimStart().startsWith('[');
}

/// True when the line opens OUR table, in either the bare or quoted key form.
function opensOurTable(line: string, name: string): boolean {
  const t = line.trim();
  if (!t.startsWith('[') || !t.endsWith(']')) return false;
  const inner = t.slice(1, -1).replace(/\s+/g, '');
  return inner === `mcp_servers.${name}` || inner === `mcp_servers."${name}"` || inner === `mcp_servers.'${name}'`;
}

/// Whether the file states `mcp_servers` in a form this splicer must not touch:
/// an inline table (`mcp_servers = { ... }`) or a dotted root key
/// (`mcp_servers.foo = ...`). Rewriting either correctly needs a real TOML
/// parser + serializer, and getting it wrong destroys a user's model config —
/// so we decline and say so ('unsupported') instead of guessing.
function hasUnsupportedForm(lines: string[]): boolean {
  return lines.some((l) => /^\s*mcp_servers\s*(\.|=)/.test(l));
}

/// Line-splice the entry into TOML source, preserving every other byte —
/// comments (including trailing ones), key order and formatting all survive,
/// which a parse-and-reserialize round trip would not guarantee. `codex mcp
/// add` appends at the end and `codex mcp remove` restores the file exactly;
/// this matches that behaviour.
export function spliceTomlEntry(src: string, block: string, name: string): string | 'unsupported' {
  const lines = src.split('\n');
  if (hasUnsupportedForm(lines)) return 'unsupported';
  const without = dropOurTable(lines, name);
  if (without === 'unsupported') return 'unsupported';
  // Separate from whatever precedes with exactly one blank line, and never
  // start the file with one.
  const body = without.join('\n').replace(/\n+$/, '');
  return body === '' ? block : `${body}\n\n${block}`;
}

/// Remove our table (header + its key/value lines, up to the next table header
/// or EOF). Returns the remaining lines, or 'unsupported'.
function dropOurTable(lines: string[], name: string): string[] | 'unsupported' {
  const start = lines.findIndex((l) => opensOurTable(l, name));
  if (start < 0) return lines;
  let end = start + 1;
  while (end < lines.length && !isTableHeader(lines[end])) end += 1;
  // A second copy of our own table would mean the file was hand-edited into an
  // ambiguous state; TOML forbids duplicate tables, so refuse rather than pick.
  const rest = lines.slice(end);
  if (rest.some((l) => opensOurTable(l, name))) return 'unsupported';
  return [...lines.slice(0, start), ...rest];
}

/// Drop our table from TOML source. 'unsupported' propagates.
export function spliceTomlWithout(src: string, name: string): string | 'unsupported' {
  const lines = src.split('\n');
  if (hasUnsupportedForm(lines)) return 'unsupported';
  const without = dropOurTable(lines, name);
  if (without === 'unsupported') return 'unsupported';
  return without.join('\n');
}

function readTomlSource(target: string): string | null | 'corrupt' {
  try {
    return fs.readFileSync(target, 'utf8');
  } catch (e) {
    return (e as NodeJS.ErrnoException).code === 'ENOENT' ? null : 'corrupt';
  }
}

function mergeCodexEntry(target: string, home: string, log: (msg: string) => void): McpWrite {
  const src = readTomlSource(target);
  if (src === 'corrupt') {
    log(`usermcp: leaving unreadable ${target} untouched`);
    return 'corrupt';
  }
  const next = spliceTomlEntry(src ?? '', codexEntryBlock(home), MCP_ENTRY_NAME);
  if (next === 'unsupported') {
    log(`usermcp: ${target} declares mcp_servers in a form we will not rewrite; left untouched`);
    return 'unsupported';
  }
  writeFileAtomic(target, next);
  return 'written';
}

function removeCodexEntry(target: string, log: (msg: string) => void): McpWrite {
  const src = readTomlSource(target);
  if (src === 'corrupt') {
    log(`usermcp: leaving unreadable ${target} untouched`);
    return 'corrupt';
  }
  if (src === null) return 'noop';
  const next = spliceTomlWithout(src, MCP_ENTRY_NAME);
  if (next === 'unsupported') {
    log(`usermcp: ${target} declares mcp_servers in a form we will not rewrite; left untouched`);
    return 'unsupported';
  }
  if (next === src) return 'noop';
  writeFileAtomic(target, next);
  return 'written';
}

// ── the three-engine lifecycle ───────────────────────────────────────────────

/// Deep-merge the ONE additive termipod-desktop entry into every engine's user
/// config, preserving every foreign key and server. Idempotent — toggle-on and
/// the per-start refresh both land here. One engine's failure never stops the
/// others: each result is reported separately.
export function mergeSharingEntries(
  home: string,
  env: NodeJS.ProcessEnv = process.env,
  log: (msg: string) => void = console.warn,
): ReseedResult {
  return {
    kimi: mergeJsonEntry(kimiMcpConfigPath(home), sharingEntry(home), log),
    claude: mergeJsonEntry(claudeMcpConfigPath(home, env), claudeSharingEntry(home), log),
    codex: mergeCodexEntry(codexMcpConfigPath(home, env), home, log),
  };
}

/// Remove ONLY the termipod-desktop entry from every engine (toggle-off).
/// Absent file / absent entry → noop; unreadable file → untouched + logged.
export function removeSharingEntries(
  home: string,
  env: NodeJS.ProcessEnv = process.env,
  log: (msg: string) => void = console.warn,
): ReseedResult {
  return {
    kimi: removeJsonEntry(kimiMcpConfigPath(home), log),
    claude: removeJsonEntry(claudeMcpConfigPath(home, env), log),
    codex: removeCodexEntry(codexMcpConfigPath(home, env), log),
  };
}
