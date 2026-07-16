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

function pwKey(id: string): string {
  return `password_${id}`;
}
function jumpPwKey(id: string): string {
  return `password_${id}_jump`;
}

export function listConnections(): Connection[] {
  return loadJson<Connection[]>(STORAGE_KEY, []);
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
    createdAt: existing?.createdAt ?? new Date().toISOString(),
    lastConnectedAt: input.lastConnectedAt ?? existing?.lastConnectedAt ?? null,
    deepLinkId: existing?.deepLinkId ?? null,
    jumpHost: input.jumpHost ?? existing?.jumpHost ?? null,
  };
  const next = existing ? list.map((c) => (c.id === id ? conn : c)) : [...list, conn];
  persist(next);
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
