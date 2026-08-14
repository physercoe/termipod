/// Where local sessions live on disk (vision-parity L3b).
///
/// One directory per session under the app's data root:
///
///     <userData>/local-sessions/<session-id>/
///         meta.json      the descriptor — what the session IS
///         events.jsonl   the transcript — what it SAID (durablelog.ts)
///
/// The split matters. `meta.json` is small, rewritten whole, and holds the
/// facts a rebind needs before any transcript is read: which family, which
/// working directory, which tool posture, and the engine's own session id.
/// `events.jsonl` is append-only and can be enormous. Rewriting a 30 MB
/// transcript to record that a session stopped would be absurd, and holding the
/// descriptor inside the transcript would mean parsing the transcript to list
/// sessions.
///
/// **The posture is persisted, and that is a safety property, not bookkeeping.**
/// A rebind respawns a real engine child on the director's machine. If the
/// posture did not survive the restart, the respawn would fall back to a
/// default — and a session the director deliberately opened as `unrestricted`
/// would silently come back narrower (annoying), or, if the default ever
/// changed, a `converse` session would come back able to read files (not
/// annoying at all). What was granted is what is restored, and nothing else.
///
/// Pure fs, no Electron: `host.ts` supplies the root, and the tests supply a
/// temp directory.

import { existsSync, mkdirSync, readdirSync, readFileSync, renameSync, rmSync, writeFileSync } from 'node:fs';
import path from 'node:path';

import type { ToolPosture } from './claudewire.ts';

export const SESSIONS_DIRNAME = 'local-sessions';
export const META_FILENAME = 'meta.json';

/// The persisted half of a session descriptor.
///
/// Deliberately not the whole `SessionDescriptor`: `status` is excluded because
/// a status read off disk is always a lie. Every session in a file was, by
/// definition, written by a process that is no longer running, so anything
/// claiming `running` at load time describes a child that died with its parent.
/// The service assigns the status on load instead.
export interface PersistedSession {
  id: string;
  family: string;
  cwd: string;
  posture: ToolPosture;
  model?: string;
  created_at: string;
  /// The engine's own session id — the handle `--resume` takes. Since L3b this
  /// is ASSIGNED at spawn rather than learned from the init frame, so it is
  /// present from the moment the directory exists.
  engine_session_id?: string;
  /// The config root the session was spawned against, resolved at create time.
  /// Persisted because `CLAUDE_CONFIG_DIR` is per-account and may differ
  /// between the run that created the session and the run that resumes it — a
  /// rebind against the wrong root finds no conversation at all.
  config_home?: string;
}

export interface SessionPaths {
  dir: string;
  meta: string;
}

export function sessionsRoot(userDataDir: string): string {
  return path.join(userDataDir, SESSIONS_DIRNAME);
}

export function sessionPaths(userDataDir: string, id: string): SessionPaths {
  const dir = path.join(sessionsRoot(userDataDir), id);
  return { dir, meta: path.join(dir, META_FILENAME) };
}

/// Write a descriptor, replacing any previous one.
///
/// Temp-and-rename so a crash mid-write cannot leave a half-parsed descriptor —
/// which would strand the transcript beside it, readable but unattributable.
export function writeSessionMeta(userDataDir: string, meta: PersistedSession): void {
  const { dir, meta: file } = sessionPaths(userDataDir, meta.id);
  mkdirSync(dir, { recursive: true });
  const tmp = file + '.tmp';
  writeFileSync(tmp, JSON.stringify(meta, null, 2) + '\n', 'utf-8');
  renameSync(tmp, file);
}

/// Read one descriptor back, or null if it is absent or unreadable.
///
/// Null rather than throw: one corrupt directory must not stop the other
/// sessions from loading. The caller reports the count it skipped.
export function readSessionMeta(userDataDir: string, id: string): PersistedSession | null {
  const { meta: file } = sessionPaths(userDataDir, id);
  if (!existsSync(file)) return null;
  try {
    return parseSessionMeta(readFileSync(file, 'utf-8'));
  } catch {
    return null;
  }
}

/// Validate a descriptor read off disk.
///
/// Exported for the tests, and strict on purpose: these fields become spawn
/// arguments. A `cwd` that decayed to `undefined` would spawn an engine in
/// whatever directory the app happens to be running from, which is the one
/// mistake this feature refuses to make anywhere else (`create` requires a cwd
/// rather than defaulting it).
export function parseSessionMeta(json: string): PersistedSession | null {
  const raw: unknown = JSON.parse(json);
  if (typeof raw !== 'object' || raw === null) return null;
  const o = raw as Record<string, unknown>;
  if (typeof o.id !== 'string' || o.id === '') return null;
  if (typeof o.family !== 'string' || o.family === '') return null;
  if (typeof o.cwd !== 'string' || o.cwd === '') return null;
  if (typeof o.posture !== 'string') return null;
  if (o.posture !== 'converse' && o.posture !== 'read_local' && o.posture !== 'unrestricted') return null;
  if (typeof o.created_at !== 'string' || o.created_at === '') return null;
  const out: PersistedSession = {
    id: o.id,
    family: o.family,
    cwd: o.cwd,
    posture: o.posture,
    created_at: o.created_at,
  };
  if (typeof o.model === 'string' && o.model !== '') out.model = o.model;
  if (typeof o.engine_session_id === 'string' && o.engine_session_id !== '') {
    out.engine_session_id = o.engine_session_id;
  }
  if (typeof o.config_home === 'string' && o.config_home !== '') out.config_home = o.config_home;
  return out;
}

export interface ListedSessions {
  sessions: PersistedSession[];
  /// Directories that looked like sessions but could not be read. Surfaced so
  /// "you have three sessions" is never quietly "you had four".
  skipped: string[];
}

/// Every persisted session, newest first.
export function listSessionMetas(userDataDir: string): ListedSessions {
  const root = sessionsRoot(userDataDir);
  const out: ListedSessions = { sessions: [], skipped: [] };
  if (!existsSync(root)) return out;

  let entries: string[];
  try {
    entries = readdirSync(root, { withFileTypes: true }).filter((d) => d.isDirectory()).map((d) => d.name);
  } catch {
    return out;
  }

  for (const name of entries) {
    const meta = readSessionMeta(userDataDir, name);
    if (meta === null) {
      out.skipped.push(name);
      continue;
    }
    if (meta.id !== name) {
      // The directory name is the id everywhere else in this module; a
      // descriptor claiming a different one would make `forget(id)` delete
      // someone else's directory.
      out.skipped.push(name);
      continue;
    }
    out.sessions.push(meta);
  }
  out.sessions.sort((a, b) => (a.created_at < b.created_at ? 1 : a.created_at > b.created_at ? -1 : 0));
  return out;
}

/// Delete a session's directory, transcript included.
export function removeSessionDir(userDataDir: string, id: string): void {
  const { dir } = sessionPaths(userDataDir, id);
  // Guard against an id that would escape the root — ids are generated uuids
  // today, but this function takes a string from an IPC boundary and a
  // traversal here deletes an arbitrary directory.
  const root = sessionsRoot(userDataDir);
  const resolved = path.resolve(dir);
  if (path.dirname(resolved) !== path.resolve(root)) {
    throw new Error(`refusing to remove ${resolved}: outside the session root`);
  }
  rmSync(resolved, { recursive: true, force: true });
}
