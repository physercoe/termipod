/// The desktop's fetched-recording cache (J8 Replay W4b-2) — where a `.rrd`
/// produced on a remote host lands once it has been pulled over SFTP.
///
/// W4b-1 hands the host's own path straight to the Rerun manager, which only
/// works when the dataset's host is this machine. When it is not, the bytes have
/// to come here first, and the transport is the director's own live SSH session
/// — zero bytes through the hub, which is the data-ownership law
/// (`spine/blueprint.md` §4) and ADR-058 §4's "the hub never stores job
/// artifacts" both.
///
/// **Content-addressed, and that is a safety property, not a tidiness one.** The
/// filename is the host-reported sha256 and nothing else: the remote path is
/// attacker-shaped input as far as this process is concerned, and letting any
/// part of it reach a local filename is how a fetch becomes an arbitrary write.
/// The digest is then verified against the bytes that actually arrived before
/// the file is given its name, so a truncated or substituted transfer never
/// becomes a file the viewer will open.
///
/// Node builtins only — no electron, no local imports — so the streaming, the
/// verification and the eviction all run under plain `node --test`
/// (`rerun_cache.test.ts`). The handler that binds this to a real SFTP channel
/// and to `app.getPath('userData')` is `ipc/rerunfetch.ts`.
import { createHash, randomBytes } from 'node:crypto';
import { createWriteStream } from 'node:fs';
import { readdir, rename, rm, mkdir, stat } from 'node:fs/promises';
import path from 'node:path';
import { Transform } from 'node:stream';
import { pipeline } from 'node:stream/promises';

/// Largest recording this will pull over a session. A multi-camera episode is
/// tens of megabytes; this is orders of magnitude above that and exists to bound
/// a mistake — a wrong path, a host that returned a dataset instead of an
/// episode — rather than a workload.
export const MAX_FETCH_BYTES = 4 * 1024 * 1024 * 1024;

/// How much fetched recording to keep before evicting. Browsing episodes is the
/// use case, so the cache fills one episode at a time and its whole value is not
/// re-pulling the one you just looked at.
export const RECORDING_CACHE_CAP_BYTES = 8 * 1024 * 1024 * 1024;

/// Bytes between progress ticks. The transfer is tens of megabytes over a
/// session that is also carrying a terminal; a tick per 256 KiB (the SFTP read
/// size) would be hundreds of IPC messages for one file.
const PROGRESS_TICK_BYTES = 1024 * 1024;

/// The remote file being pulled — structurally the `SftpFile` `ipc/ssh.ts`
/// returns, restated here so this module keeps its node-only imports and the
/// tests can supply a local file instead of a session.
export interface RemoteFile {
  size: number;
  open: (start: number, end: number) => NodeJS.ReadableStream;
  close: () => void;
}

/// A lowercase sha-256 hex digest and nothing else.
///
/// Exported because it is the gate on everything below: the digest becomes a
/// filename, so anything that is not exactly 64 hex characters must be refused
/// here rather than sanitised into something that looks safe.
export function isContentDigest(s: string): boolean {
  return /^[0-9a-f]{64}$/.test(s);
}

/// Where a recording with this digest lives. Throws rather than returning null:
/// every caller would have to turn a null into the same error, and a fetch that
/// silently skipped its cache would be worse than one that failed loudly.
export function cachedRecordingPath(root: string, sha256: string): string {
  if (!isContentDigest(sha256)) {
    throw new Error(`not a sha-256 digest: '${sha256}'`);
  }
  return path.join(root, `${sha256}.rrd`);
}

export interface CacheEntry {
  name: string;
  size: number;
  mtimeMs: number;
}

/// Which cached files to delete to get back under `cap`, oldest first.
///
/// `keep` is the file the caller just wrote (or is about to hand to the viewer)
/// and is never a victim — evicting it would mean fetching it again immediately,
/// which is the one outcome the cache exists to prevent. Split from the eviction
/// itself because "which" is arithmetic worth asserting and "delete" is not.
///
/// mtime is the ordering key. It is not a true LRU — a cache hit does not touch
/// the file — but atime is unreliable on a `relatime` mount, and the miss it
/// causes (evicting something recently *read* but long ago *written*) costs one
/// re-fetch, while trusting atime can cost eviction of nothing at all.
export function pruneVictims(entries: CacheEntry[], cap: number, keep: string): string[] {
  let total = 0;
  for (const e of entries) total += e.size;
  if (total <= cap) return [];
  const victims: string[] = [];
  const ordered = entries.filter((e) => e.name !== keep).sort((a, b) => a.mtimeMs - b.mtimeMs);
  for (const e of ordered) {
    if (total <= cap) break;
    victims.push(e.name);
    total -= e.size;
  }
  return victims;
}

async function readCache(root: string): Promise<CacheEntry[]> {
  let names: string[];
  try {
    names = await readdir(root);
  } catch {
    return []; // no cache dir yet — nothing to evict
  }
  const out: CacheEntry[] = [];
  for (const name of names) {
    try {
      const st = await stat(path.join(root, name));
      if (st.isFile()) out.push({ name, size: st.size, mtimeMs: st.mtimeMs });
    } catch {
      // Raced with another fetch's rename, or with the user. Skipping it only
      // under-counts the total, which errs toward keeping bytes rather than
      // deleting something that is in use.
    }
  }
  return out;
}

/// Evict oldest-first until the cache is under `cap`. Best-effort: a file that
/// will not delete is not a reason to fail a fetch that has already succeeded.
export async function pruneRecordingCache(
  root: string,
  cap = RECORDING_CACHE_CAP_BYTES,
  keep = '',
): Promise<string[]> {
  const victims = pruneVictims(await readCache(root), cap, keep);
  const removed: string[] = [];
  for (const name of victims) {
    try {
      await rm(path.join(root, name), { force: true });
      removed.push(name);
    } catch {
      /* in use, or gone already */
    }
  }
  return removed;
}

export interface FetchedRecording {
  path: string;
  bytes: number;
  /// True when the file was already in the cache and nothing was transferred.
  cached: boolean;
}

/// Pull one remote recording into the cache and return its local path.
///
/// The source is closed on every path out of here, including the refusals — it
/// holds an SFTP channel on a session the director is also typing into.
export async function fetchRecording(opts: {
  root: string;
  sha256: string;
  source: RemoteFile;
  onProgress?: (done: number) => void;
  cap?: number;
}): Promise<FetchedRecording> {
  const { root, sha256, source } = opts;
  try {
    const dest = cachedRecordingPath(root, sha256); // throws on a bad digest

    // A cache hit is decided by the name alone. That is sound *because* of how
    // the name is earned: a file only takes it after its bytes hashed to it, so
    // a file that is present has already been verified once. An interrupted
    // fetch is still a `.part-…` and cannot be mistaken for one.
    const hit = await stat(dest).catch(() => null);
    if (hit !== null && hit.isFile()) {
      opts.onProgress?.(hit.size);
      return { path: dest, bytes: hit.size, cached: true };
    }

    if (source.size <= 0) {
      throw new Error('the host reported an empty recording');
    }
    if (source.size > MAX_FETCH_BYTES) {
      throw new Error(
        `recording is ${source.size} bytes, over the ${MAX_FETCH_BYTES}-byte fetch limit`,
      );
    }

    await mkdir(root, { recursive: true });
    // Random suffix, not the digest alone: two windows fetching the same episode
    // would otherwise write the same temp file and interleave their bytes.
    const tmp = `${dest}.part-${randomBytes(6).toString('hex')}`;

    const hash = createHash('sha256');
    let done = 0;
    let ticked = 0;
    // A tap in the pipeline rather than a `data` listener beside it: a listener
    // puts the source in flowing mode before `pipeline` has wired the
    // destination, so backpressure from a slow disk would no longer reach the
    // SFTP channel.
    const tap = new Transform({
      transform(chunk: Buffer, _enc, cb) {
        hash.update(chunk);
        done += chunk.length;
        if (done - ticked >= PROGRESS_TICK_BYTES) {
          ticked = done;
          opts.onProgress?.(done);
        }
        cb(null, chunk);
      },
    });

    try {
      await pipeline(source.open(0, source.size - 1), tap, createWriteStream(tmp));
      const got = hash.digest('hex');
      if (got !== sha256) {
        // Not a retry: the same session would produce the same answer, and
        // handing the viewer bytes that are not what the host exported is the
        // failure this whole content-addressed scheme exists to prevent.
        throw new Error(`the recording arrived corrupt (sha256 ${got}, expected ${sha256})`);
      }
      // No separate short-transfer check: the digest already is one. A stream
      // that ended early, or a `size` the host got wrong in either direction,
      // all land as a hash mismatch — and a length check on top could only
      // reject a transfer the digest had just proved correct.
      await rename(tmp, dest);
    } catch (e) {
      await rm(tmp, { force: true }).catch(() => undefined);
      throw e;
    }

    opts.onProgress?.(done);
    await pruneRecordingCache(root, opts.cap ?? RECORDING_CACHE_CAP_BYTES, path.basename(dest));
    return { path: dest, bytes: done, cached: false };
  } finally {
    try {
      source.close();
    } catch {
      /* the channel is already gone */
    }
  }
}
