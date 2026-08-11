/// Persisted metadata for incremental Zotero attachment sync.
///
/// The cache is only an accelerator: local entries are reused when filename,
/// size, mtime and ctime match; remote entries are reused only when the server's
/// reliable object marker (ETag) matches the fresh directory/object listing.
/// Missing, malformed or stale cache data always falls back to the full check.
import { createHash } from 'node:crypto';
import { mkdir, readFile, rename, writeFile } from 'node:fs/promises';
import path from 'node:path';

export interface CachedLocalAttachment {
  signature: string;
  hash: string;
}

export interface CachedRemoteProp {
  marker: string;
  mtime: number;
  hash: string;
}

export interface ZoteroSyncCache {
  version: 1;
  local: Record<string, CachedLocalAttachment>;
  remote: Record<string, CachedRemoteProp>;
}

export function emptyZoteroSyncCache(): ZoteroSyncCache {
  return { version: 1, local: {}, remote: {} };
}

export function zoteroCacheScope(kind: 'webdav' | 's3', parts: string[]): string {
  return createHash('sha256').update([kind, ...parts].join('\0')).digest('hex');
}

export function planIncrementalZoteroWork(
  locals: Map<string, { hash: string }>,
  remotes: Map<string, string>,
  cached: Record<string, CachedRemoteProp>,
): { work: string[]; skipped: number; remote: Record<string, CachedRemoteProp> } {
  const work: string[] = [];
  const remote: Record<string, CachedRemoteProp> = {};
  let skipped = 0;
  const all = new Set<string>([...locals.keys(), ...remotes.keys()]);
  for (const key of all) {
    const local = locals.get(key);
    const marker = remotes.get(key);
    const prior = cached[key];
    if (
      local !== undefined
      && marker !== undefined
      && marker !== ''
      && prior?.marker === marker
      && prior.hash !== ''
      && prior.hash.toLowerCase() === local.hash.toLowerCase()
    ) {
      remote[key] = prior;
      skipped += 1;
    } else {
      work.push(key);
    }
  }
  return { work, skipped, remote };
}

function cacheFile(userData: string, scope: string): string {
  return path.join(userData, 'sync-cache', `zotero-${scope}.json`);
}

function object(value: unknown): Record<string, unknown> | null {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null;
}

function readLocal(value: unknown): Record<string, CachedLocalAttachment> {
  const out: Record<string, CachedLocalAttachment> = {};
  const raw = object(value);
  if (raw === null) return out;
  for (const [key, value2] of Object.entries(raw).slice(0, 200_000)) {
    const row = object(value2);
    if (row === null || typeof row.signature !== 'string' || typeof row.hash !== 'string') continue;
    out[key] = { signature: row.signature, hash: row.hash };
  }
  return out;
}

function readRemote(value: unknown): Record<string, CachedRemoteProp> {
  const out: Record<string, CachedRemoteProp> = {};
  const raw = object(value);
  if (raw === null) return out;
  for (const [key, value2] of Object.entries(raw).slice(0, 200_000)) {
    const row = object(value2);
    if (
      row === null
      || typeof row.marker !== 'string'
      || row.marker === ''
      || typeof row.mtime !== 'number'
      || !Number.isFinite(row.mtime)
      || typeof row.hash !== 'string'
    ) continue;
    out[key] = { marker: row.marker, mtime: row.mtime, hash: row.hash };
  }
  return out;
}

export async function loadZoteroSyncCache(userData: string, scope: string): Promise<ZoteroSyncCache> {
  try {
    const raw = object(JSON.parse(await readFile(cacheFile(userData, scope), 'utf8')));
    if (raw === null || raw.version !== 1) return emptyZoteroSyncCache();
    return { version: 1, local: readLocal(raw.local), remote: readRemote(raw.remote) };
  } catch {
    return emptyZoteroSyncCache();
  }
}

export async function saveZoteroSyncCache(
  userData: string,
  scope: string,
  cache: ZoteroSyncCache,
): Promise<void> {
  const dir = path.join(userData, 'sync-cache');
  await mkdir(dir, { recursive: true });
  const target = cacheFile(userData, scope);
  const temp = `${target}.${process.pid}.${Date.now()}.tmp`;
  await writeFile(temp, JSON.stringify(cache), { encoding: 'utf8', mode: 0o600 });
  await rename(temp, target);
}
