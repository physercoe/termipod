/// Media-scheme policy (J8 Replay W2d) — the pure half of `mediascheme.ts`.
///
/// Split out for the same reason `webtab_policy.ts` is: the handler imports
/// `electron`, and `schemes.ts` calls `registerSchemesAsPrivileged` at module
/// load, so anything that touches either is unreachable from `node --test`.
/// The range arithmetic is the part most likely to be wrong and the part CI can
/// actually check, so it lives here.
import path from 'node:path';

/// J8 Replay's video source. Prefixed rather than a bare `media` because a
/// custom scheme is a global name inside the app and `media://` is generic
/// enough to collide with something an embedded page expects.
export const MEDIA_SCHEME = 'termipod-media';

/// Extensions this scheme will serve.
///
/// An allowlist, not a denylist: the renderer picks the path, and narrowing the
/// scheme to media means a bug in that path cannot turn it into a general file
/// reader. `.mp4` covers every LeRobot dataset seen so far; the rest are here
/// because a dataset that used them would otherwise fail for no good reason.
///
/// The image / audio / PDF rows are Inspect's preview needs, not Replay's. They
/// widen WHAT is served, never WHERE it is read from: the path still has to
/// clear `mediaPathOf` (absolute, normalized) or `mediaSftpOf`, and the scheme
/// is still reachable only from the app's own renderer session. Every type here
/// is one a `<video>` / `<audio>` / `<img>` / `<iframe>` decodes natively, so
/// nothing added needs a parser of ours.
///
/// **Deliberately absent: `.svg` and `.html`.** Both are active documents —
/// scripts, external fetches, same-origin reach into this scheme — and serving
/// them here would hand a file the renderer merely *pointed at* the app
/// session's privileges. Inspect already shows SVG as text, which is the honest
/// view of a file that is markup.
export const MEDIA_TYPES: Record<string, string> = {
  '.mp4': 'video/mp4',
  '.m4v': 'video/mp4',
  '.mov': 'video/quicktime',
  '.webm': 'video/webm',
  '.mkv': 'video/x-matroska',
  '.avi': 'video/x-msvideo',
  '.ogv': 'video/ogg',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.jpeg': 'image/jpeg',
  '.gif': 'image/gif',
  '.webp': 'image/webp',
  '.bmp': 'image/bmp',
  '.avif': 'image/avif',
  '.ico': 'image/x-icon',
  '.tif': 'image/tiff',
  '.tiff': 'image/tiff',
  '.mp3': 'audio/mpeg',
  '.m4a': 'audio/mp4',
  '.wav': 'audio/wav',
  '.oga': 'audio/ogg',
  '.ogg': 'audio/ogg',
  '.flac': 'audio/flac',
  '.aac': 'audio/aac',
  '.opus': 'audio/opus',
  '.pdf': 'application/pdf',
};

/// Largest file this scheme will open. Robot video is minutes of small frames —
/// the 88-second fixture is 290 KB — so this is orders of magnitude above any
/// real dataset and exists to bound a mistake, not a workload.
export const MAX_MEDIA_BYTES = 8 * 1024 * 1024 * 1024;

/// Largest single range served in one response. A seek asks for a small window
/// and Chromium follows up with more; an open-ended `bytes=0-` on a multi-GB
/// file should not become one unbounded read.
export const MAX_RANGE_BYTES = 64 * 1024 * 1024;

export interface MediaRange {
  start: number;
  end: number;
  /// True when the client asked for a range at all — a plain GET must answer
  /// 200, not 206, or Chromium treats the resource as non-seekable.
  partial: boolean;
}

/// Parse an HTTP Range header against a known file size.
///
/// Returns null for a syntactically valid but unsatisfiable range, which the
/// caller turns into a 416 — the response Chromium needs in order to correct
/// itself, rather than a 200 whose body does not match what it asked for.
///
/// Exported for the unit tests: this is fiddly, off-by-one-prone arithmetic
/// with three distinct forms, and it is the part of this file worth asserting.
export function parseRange(header: string | null, size: number): MediaRange | null {
  if (header === null || header.trim() === '') {
    return { start: 0, end: Math.max(0, size - 1), partial: false };
  }
  const m = /^bytes=(\d*)-(\d*)$/.exec(header.trim());
  if (m === null) return null;
  const [, rawStart, rawEnd] = m;
  if (size <= 0) return null;

  let start: number;
  let end: number;
  if (rawStart === '') {
    // "bytes=-N" — the LAST N bytes. Chromium uses this to read an mp4's moov
    // atom when it is at the end of the file, so getting it wrong means some
    // files never play at all.
    if (rawEnd === '') return null;
    const n = Number(rawEnd);
    if (n <= 0) return null;
    start = Math.max(0, size - n);
    end = size - 1;
  } else {
    start = Number(rawStart);
    if (start >= size) return null; // unsatisfiable → 416
    end = rawEnd === '' ? size - 1 : Number(rawEnd);
    if (end < start) return null;
    if (end > size - 1) end = size - 1; // over-long ranges clamp, they do not fail
  }
  // Cap the window. Serving less than was asked for is legal and expected —
  // the client reads Content-Range and comes back for the rest.
  if (end - start + 1 > MAX_RANGE_BYTES) end = start + MAX_RANGE_BYTES - 1;
  return { start, end, partial: true };
}

/// The absolute file path a media URL points at, or null if it is malformed.
///
/// The path travels as a query parameter rather than as the URL path: a
/// dataset root is an absolute path on some platform or other, and jamming
/// `C:\data\…` or `/srv/…` into a pathname means fighting the URL parser over
/// leading slashes, drive letters and percent-encoding.
export function mediaPathOf(rawUrl: string): string | null {
  let url: URL;
  try {
    url = new URL(rawUrl);
  } catch {
    return null;
  }
  // Host discriminates the flavour: 'file' is the local-disk read. Anything
  // else (notably 'sftp') must NOT fall through here, or a remote path would
  // be read off the local disk.
  if (url.host !== 'file') return null;
  const p = url.searchParams.get('p');
  if (p === null || p.trim() === '') return null;
  if (!path.isAbsolute(p)) return null;
  // Normalize before the extension check so `.mp4/../../etc/passwd` cannot
  // pass one and resolve to the other.
  return path.normalize(p);
}

/// Build a media URL for an absolute path. Exported so the renderer and the
/// tests agree on the shape by construction rather than by convention.
export function mediaUrl(absPath: string): string {
  return `${MEDIA_SCHEME}://file/?p=${encodeURIComponent(absPath)}`;
}

/// The remote flavour (J8 remote datasets): bytes streamed over a live SSH
/// session's SFTP channel instead of the local disk. `s` names the ssh session
/// (`ipc/ssh.ts` session id — ephemeral, minted by ssh_connect), `p` the
/// POSIX-absolute path on the REMOTE machine. Same extension allowlist and
/// range arithmetic as the file flavour; the session id adds no privilege the
/// renderer doesn't already hold (it can `sftp_read` arbitrary remote paths).
export interface MediaSftpTarget {
  sessionId: string;
  path: string;
}

export function mediaSftpOf(rawUrl: string): MediaSftpTarget | null {
  let url: URL;
  try {
    url = new URL(rawUrl);
  } catch {
    return null;
  }
  if (url.host !== 'sftp') return null;
  const s = url.searchParams.get('s');
  const p = url.searchParams.get('p');
  if (s === null || s.trim() === '' || p === null || p.trim() === '') return null;
  // Remote hosts are POSIX; require absolute and normalize BEFORE the
  // caller's extension check (the same order the file flavour uses). An
  // absolute posix path cannot keep a `..` segment through normalize, so no
  // further traversal check is needed — and the allowlist bounds what serves.
  if (!p.startsWith('/')) return null;
  return { sessionId: s, path: path.posix.normalize(p) };
}

export function mediaSftpUrl(sessionId: string, remotePath: string): string {
  return `${MEDIA_SCHEME}://sftp/?s=${encodeURIComponent(sessionId)}&p=${encodeURIComponent(remotePath)}`;
}

