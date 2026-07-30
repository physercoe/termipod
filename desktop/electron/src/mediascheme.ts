/// Range-serving media scheme (J8 Replay W2d) — the bytes behind the episode
/// player's video grid.
///
/// `<video>` cannot play a path; it needs a URL, and it needs **range
/// requests**, because seeking to an episode inside a shared file is the whole
/// point. Chromium issues `Range: bytes=…` and expects `206` plus a
/// `Content-Range`; a handler that always returns the full body forces a
/// re-download per seek and, for a long file, makes the scrub unusable.
///
/// No transcoding and no ffmpeg. A real LeRobot mp4 carries a keyframe every
/// 0.4s (measured: 220 sync samples across the 440-frame v3.0 fixture), so the
/// player seeks to `from_ts` and stops at `to_ts` and the decoder does the rest.
///
/// **Reachability is the security argument.** The handler is attached to
/// `defaultSession` only — the same session the app's own renderer runs in —
/// while every `<webview>` guest runs in an isolated partition
/// (`persist:webtab`, kimiweb) that has no handler for this scheme. Untrusted
/// remote content therefore cannot fetch it at all. Within the app's own
/// renderer this is the same privilege `localfs_read` already grants; what is
/// added here is streaming, not reach.
///
/// The URL parsing and range arithmetic live in `media_policy.ts` so they are
/// reachable from `node --test` — this file is the part that needs electron.
import { createReadStream } from 'node:fs';
import { stat } from 'node:fs/promises';
import { Readable } from 'node:stream';
import path from 'node:path';

import { MEDIA_SCHEME, MEDIA_TYPES, MAX_MEDIA_BYTES, mediaPathOf, mediaSftpOf, parseRange } from './media_policy';
import { openSftpFile } from './ipc/ssh';

/// Shared tail of both flavours: range-check a known size, then stream the
/// window. `open` supplies the bounded byte stream; `done` releases whatever
/// backs it (the SFTP channel; a no-op for local files).
function rangedResponse(
  req: Request,
  size: number,
  type: string,
  open: (start: number, end: number) => NodeJS.ReadableStream,
  done: () => void,
): Response {
  if (size > MAX_MEDIA_BYTES) {
    done();
    return new Response('file too large', { status: 413 });
  }
  const range = parseRange(req.headers.get('range'), size);
  if (range === null) {
    done();
    return new Response('range not satisfiable', {
      status: 416,
      headers: { 'Content-Range': `bytes */${size}`, 'Accept-Ranges': 'bytes' },
    });
  }
  const headers = new Headers({
    'Content-Type': type,
    // Without this Chromium will not offer seeking at all, even though every
    // range request would have been honoured.
    'Accept-Ranges': 'bytes',
    'Content-Length': String(range.end - range.start + 1),
    'Cache-Control': 'no-store',
  });
  if (range.partial) headers.set('Content-Range', `bytes ${range.start}-${range.end}/${size}`);

  // An empty file has no byte to stream; createReadStream(0, -1) would throw.
  if (size === 0) {
    done();
    return new Response(null, { status: range.partial ? 206 : 200, headers });
  }

  const stream = open(range.start, range.end) as Readable;
  stream.once('close', done);
  stream.once('error', done);
  return new Response(Readable.toWeb(stream) as ReadableStream, {
    status: range.partial ? 206 : 200,
    headers,
  });
}

/// Attach the media handler to a session. Call after app ready, and only for
/// `defaultSession` — see the reachability note above.
export function registerMediaScheme(sess: Electron.Session): void {
  sess.protocol.handle(MEDIA_SCHEME, async (req): Promise<Response> => {
    // Remote flavour (J8 remote datasets): bytes ride a live SSH session's
    // SFTP channel — same allowlist, same range discipline, one channel per
    // request, released when the response stream closes.
    const sftpTarget = mediaSftpOf(req.url);
    if (sftpTarget !== null) {
      const type = MEDIA_TYPES[path.posix.extname(sftpTarget.path).toLowerCase()];
      if (type === undefined) return new Response('unsupported media type', { status: 415 });
      const media = await openSftpFile(sftpTarget.sessionId, sftpTarget.path);
      if (media === null) return new Response('not found', { status: 404 });
      return rangedResponse(req, media.size, type, media.open, media.close);
    }

    const target = mediaPathOf(req.url);
    if (target === null) return new Response('bad request', { status: 400 });

    const type = MEDIA_TYPES[path.extname(target).toLowerCase()];
    if (type === undefined) return new Response('unsupported media type', { status: 415 });

    let size: number;
    try {
      const st = await stat(target);
      // Directories and devices are not media. A FIFO would block the read
      // forever, which is a hang rather than an error and therefore worse.
      if (!st.isFile()) return new Response('not found', { status: 404 });
      size = st.size;
    } catch {
      return new Response('not found', { status: 404 });
    }

    return rangedResponse(
      req,
      size,
      type,
      (start, end) => createReadStream(target, { start, end }),
      () => undefined,
    );
  });
}
