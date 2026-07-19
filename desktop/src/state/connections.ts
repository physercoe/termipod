import { loadJson, newId, saveJson, secretDeleteMany, secretGet, secretSet } from './persist';

/// Saved SSH connections (parity Phase 2a). The shape mirrors the mobile
/// `Connection` (lib/providers/connection_provider.dart) key-for-key so the
/// zero-knowledge vault (Phase 2b) can seal/open the same bundle across
/// devices. The connection password is NOT in this JSON — it lives in the OS
/// keychain under `password_<id>` (jump password `password_<id>_jump`),
/// matching the mobile secure-storage key patterns.

export interface Connection {
  id: string;
  name: string;
  host: string;
  port: number;
  username: string;
  authMethod: 'password' | 'key';
  keyId: string | null;
  tmuxPath: string | null;
  terminalMode?: string | null;
  // Nav grouping (desktop). Absent on older records and on the mobile bundle →
  // treated as the DEFAULT_GROUP by `connectionGroup`.
  group?: string | null;
  createdAt: string; // ISO-8601
  lastConnectedAt: string | null;
  deepLinkId: string | null;
  // Jump host (stored for data + vault parity; jump-through connect is a
  // transport follow-up — the Rust ssh_connect has no ProxyJump yet).
  jumpHost?: string | null;
  jumpPort?: number | null;
  jumpUsername?: string | null;
  jumpAuthMethod?: string | null;
  jumpKeyId?: string | null;
  proxyHost?: string | null;
  proxyPort?: number | null;
  proxyUsername?: string | null;
  proxyPassword?: string | null;
}

const STORAGE_KEY = 'connections';
const GROUPS_KEY = 'connection_groups';

/// The bucket a connection with no explicit group falls into. Existing records
/// (and mobile ones) have no `group` field, so they read as this.
export const DEFAULT_GROUP = 'default';

function pwKey(id: string): string {
  return `password_${id}`;
}
function jumpPwKey(id: string): string {
  return `password_${id}_jump`;
}

export function listConnections(): Connection[] {
  return loadJson<Connection[]>(STORAGE_KEY, []);
}

/// A connection's nav group — its `group` field, or DEFAULT_GROUP when unset/blank.
export function connectionGroup(c: Connection): string {
  const g = (c.group ?? '').trim();
  return g === '' ? DEFAULT_GROUP : g;
}

// Explicitly-created groups (so an empty group survives having no connections).
// The nav also derives groups from the connections themselves; `navGroups` unions
// the two. DEFAULT_GROUP is always present and always sorts first.
function listStoredGroups(): string[] {
  return loadJson<string[]>(GROUPS_KEY, []);
}
function persistGroups(names: string[]): void {
  // Dedupe, drop blanks + the implicit default (never stored explicitly).
  const seen = new Set<string>();
  const out: string[] = [];
  for (const n of names) {
    const g = n.trim();
    if (g === '' || g.toLowerCase() === DEFAULT_GROUP) continue;
    const key = g.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(g);
  }
  saveJson(GROUPS_KEY, out);
}

/// The ordered group list for the nav: DEFAULT_GROUP first, then every stored or
/// in-use group name, case-insensitively deduped and alpha-sorted.
export function navGroups(conns: Connection[] = listConnections()): string[] {
  const rest = new Map<string, string>(); // lowercase → display
  for (const g of listStoredGroups()) {
    const t = g.trim();
    if (t !== '' && t.toLowerCase() !== DEFAULT_GROUP) rest.set(t.toLowerCase(), t);
  }
  for (const c of conns) {
    const g = connectionGroup(c);
    if (g.toLowerCase() !== DEFAULT_GROUP && !rest.has(g.toLowerCase())) rest.set(g.toLowerCase(), g);
  }
  return [DEFAULT_GROUP, ...[...rest.values()].sort((a, b) => a.localeCompare(b))];
}

/// Register an empty group so it shows in the nav before any connection uses it.
export function addGroup(name: string): void {
  const g = name.trim();
  if (g === '' || g.toLowerCase() === DEFAULT_GROUP) return;
  persistGroups([...listStoredGroups(), g]);
}

/// Rename a group everywhere: the stored list AND every connection in it.
export function renameGroup(from: string, to: string): void {
  const target = to.trim();
  if (target === '' || target.toLowerCase() === DEFAULT_GROUP) return;
  if (from.toLowerCase() === DEFAULT_GROUP) return; // the default bucket is fixed
  persistGroups(listStoredGroups().map((g) => (g.toLowerCase() === from.toLowerCase() ? target : g)).concat(target));
  const list = listConnections().map((c) =>
    connectionGroup(c).toLowerCase() === from.toLowerCase() ? { ...c, group: target } : c,
  );
  persist(list);
}

/// Delete a group: drop it from the stored list and move its connections back to
/// the default bucket. DEFAULT_GROUP itself can't be deleted.
export function removeGroup(name: string): void {
  if (name.trim().toLowerCase() === DEFAULT_GROUP) return;
  persistGroups(listStoredGroups().filter((g) => g.toLowerCase() !== name.trim().toLowerCase()));
  const list = listConnections().map((c) =>
    connectionGroup(c).toLowerCase() === name.trim().toLowerCase() ? { ...c, group: DEFAULT_GROUP } : c,
  );
  persist(list);
}

/// Move one connection into a group (creating the group if new).
export function setConnectionGroup(id: string, group: string): void {
  const g = group.trim() === '' ? DEFAULT_GROUP : group.trim();
  if (g.toLowerCase() !== DEFAULT_GROUP) addGroup(g);
  const list = listConnections().map((c) => (c.id === id ? { ...c, group: g } : c));
  persist(list);
}

function persist(list: Connection[]): void {
  saveJson(STORAGE_KEY, list);
}

/** Create or update a connection's metadata; returns the stored record. */
export function upsertConnection(input: Partial<Connection> & { name: string; host: string; username: string }): Connection {
  const list = listConnections();
  const id = input.id ?? newId();
  const existing = list.find((c) => c.id === id);
  const conn: Connection = {
    id,
    name: input.name,
    host: input.host,
    port: input.port ?? 22,
    username: input.username,
    authMethod: input.authMethod ?? 'password',
    keyId: input.keyId ?? null,
    tmuxPath: input.tmuxPath ?? existing?.tmuxPath ?? null,
    terminalMode: input.terminalMode ?? existing?.terminalMode ?? null,
    group: (input.group ?? existing?.group ?? '').trim() || DEFAULT_GROUP,
    createdAt: existing?.createdAt ?? new Date().toISOString(),
    lastConnectedAt: input.lastConnectedAt ?? existing?.lastConnectedAt ?? null,
    deepLinkId: existing?.deepLinkId ?? null,
    // Carry over the whole jump/proxy cluster — these are stored for vault parity
    // (sync can populate them from a mobile bundle) and a form that doesn't touch
    // them must not wipe them on save.
    jumpHost: input.jumpHost ?? existing?.jumpHost ?? null,
    jumpPort: input.jumpPort ?? existing?.jumpPort ?? null,
    jumpUsername: input.jumpUsername ?? existing?.jumpUsername ?? null,
    jumpAuthMethod: input.jumpAuthMethod ?? existing?.jumpAuthMethod ?? null,
    jumpKeyId: input.jumpKeyId ?? existing?.jumpKeyId ?? null,
    proxyHost: input.proxyHost ?? existing?.proxyHost ?? null,
    proxyPort: input.proxyPort ?? existing?.proxyPort ?? null,
    proxyUsername: input.proxyUsername ?? existing?.proxyUsername ?? null,
    proxyPassword: input.proxyPassword ?? existing?.proxyPassword ?? null,
  };
  const next = existing ? list.map((c) => (c.id === id ? conn : c)) : [...list, conn];
  persist(next);
  // Keep a non-default group registered so it survives even if its last
  // connection later moves out.
  if (conn.group !== null && conn.group !== undefined && conn.group.toLowerCase() !== DEFAULT_GROUP)
    addGroup(conn.group);
  return conn;
}

export async function deleteConnection(id: string): Promise<void> {
  persist(listConnections().filter((c) => c.id !== id));
  await secretDeleteMany([pwKey(id), jumpPwKey(id)]);
}

export function touchConnection(id: string): void {
  const list = listConnections();
  const conn = list.find((c) => c.id === id);
  if (conn === undefined) return;
  conn.lastConnectedAt = new Date().toISOString();
  persist(list);
}

export function setConnectionPassword(id: string, password: string): Promise<void> {
  return secretSet(pwKey(id), password);
}
export function getConnectionPassword(id: string): Promise<string | null> {
  return secretGet(pwKey(id));
}
