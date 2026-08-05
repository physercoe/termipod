/// Tests for the media scheme's URL parsing and range arithmetic (J8 Replay
/// W2d). Both are enforced main-side by `mediascheme.ts`; this is the half that
/// can run without electron. Run with `node --test`.
///
/// The range cases matter more than their size suggests: `<video>` seeking is
/// entirely built on them, and every one of the three Range forms is issued by
/// Chromium in normal playback — including the suffix form, which is how it
/// finds an mp4's `moov` atom when the muxer put it at the end of the file.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import {
  MAX_RANGE_BYTES,
  MEDIA_SCHEME,
  MEDIA_TYPES,
  mediaPathOf,
  mediaUrl,
  parseRange,
} from './media_policy.ts';

const SIZE = 1000;

test('no Range header is a whole-file 200, not a 206', () => {
  // Answering 206 to a plain GET makes Chromium treat the response as a
  // mismatch; answering 200 to a Range request makes it treat the resource as
  // non-seekable. The distinction is what `partial` carries.
  assert.deepEqual(parseRange(null, SIZE), { start: 0, end: 999, partial: false });
  assert.deepEqual(parseRange('', SIZE), { start: 0, end: 999, partial: false });
  assert.deepEqual(parseRange('   ', SIZE), { start: 0, end: 999, partial: false });
});

test('bytes=start-end is inclusive at both ends', () => {
  assert.deepEqual(parseRange('bytes=0-99', SIZE), { start: 0, end: 99, partial: true });
  assert.deepEqual(parseRange('bytes=500-599', SIZE), { start: 500, end: 599, partial: true });
  // A single byte is a legal range, and off-by-one here would serve zero.
  assert.deepEqual(parseRange('bytes=7-7', SIZE), { start: 7, end: 7, partial: true });
  assert.deepEqual(parseRange(' bytes=0-0 ', SIZE), { start: 0, end: 0, partial: true });
});

test('bytes=start- runs to the end of the file', () => {
  // The commonest form: Chromium opens a video with `bytes=0-`.
  assert.deepEqual(parseRange('bytes=0-', SIZE), { start: 0, end: 999, partial: true });
  assert.deepEqual(parseRange('bytes=999-', SIZE), { start: 999, end: 999, partial: true });
});

test('bytes=-N is the LAST N bytes', () => {
  // Not "up to N". Getting this backwards means files whose moov atom sits at
  // the end never play at all — and they look like a codec problem.
  assert.deepEqual(parseRange('bytes=-100', SIZE), { start: 900, end: 999, partial: true });
  assert.deepEqual(parseRange('bytes=-1', SIZE), { start: 999, end: 999, partial: true });
  // A suffix longer than the file is the whole file, not an error.
  assert.deepEqual(parseRange('bytes=-5000', SIZE), { start: 0, end: 999, partial: true });
});

test('an over-long end clamps but a start past the end is unsatisfiable', () => {
  // Clamping the end is what RFC 9110 requires; a start beyond the file has no
  // valid answer and must be a 416 so the client can correct itself.
  assert.deepEqual(parseRange('bytes=900-99999', SIZE), { start: 900, end: 999, partial: true });
  assert.equal(parseRange('bytes=1000-', SIZE), null);
  assert.equal(parseRange('bytes=1000-1010', SIZE), null);
  assert.equal(parseRange('bytes=5000-6000', SIZE), null);
});

test('malformed ranges are refused rather than guessed at', () => {
  for (const h of [
    'bytes=', 'bytes=-', 'bytes=abc-def', 'bytes=10-5', 'items=0-10',
    'bytes=0-10, 20-30', // multi-range: legal HTTP, not supported here
    'bytes=-0', '0-100', 'bytes 0-100',
  ]) {
    assert.equal(parseRange(h, SIZE), null, h);
  }
});

test('a zero-length file has no satisfiable range', () => {
  assert.deepEqual(parseRange(null, 0), { start: 0, end: 0, partial: false });
  assert.equal(parseRange('bytes=0-', 0), null);
  assert.equal(parseRange('bytes=-1', 0), null);
});

test('an open-ended range on a huge file is capped', () => {
  // `bytes=0-` on a multi-GB file must not become one unbounded read. Serving
  // less than asked is legal: the client reads Content-Range and comes back.
  const huge = 4 * 1024 * 1024 * 1024;
  const r = parseRange('bytes=0-', huge)!;
  assert.equal(r.start, 0);
  assert.equal(r.end, MAX_RANGE_BYTES - 1);
  assert.equal(r.end - r.start + 1, MAX_RANGE_BYTES);
  // The cap applies from wherever the range starts, not from zero.
  const mid = parseRange('bytes=1000000000-', huge)!;
  assert.equal(mid.start, 1000000000);
  assert.equal(mid.end - mid.start + 1, MAX_RANGE_BYTES);
});

test('a URL round-trips an absolute path through mediaUrl', () => {
  for (const p of [
    '/data/nyu_rot/videos/observation.images.image/chunk-000/file-000.mp4',
    '/srv/robot data/ds one/videos/cam.mp4', // spaces
    '/data/ünïcode/vidéo.mp4',
    '/data/a+b&c=d/e?f.mp4', // characters that would break a naive query string
    '/data/100%/cam.mp4', // a bare % is invalid percent-encoding if not escaped
  ]) {
    assert.equal(mediaPathOf(mediaUrl(p)), p, p);
  }
});

test('absoluteness is the platform\'s definition, not a hardcoded one', () => {
  // `path.isAbsolute` is platform-specific, and deliberately so: `C:\\data` is
  // absolute on Windows and meaningless on POSIX. This test therefore asserts
  // the DELEGATION rather than a fixed answer — running it on Linux and
  // asserting the Windows form would pin the wrong behaviour, and running it on
  // Windows would then fail. The URL layer above it is platform-neutral: only
  // this one check varies.
  const win = 'C:\\data\\nyu\\videos\\cam.mp4';
  assert.ok(path.win32.isAbsolute(win), 'a drive path is absolute under win32 rules');
  assert.ok(!path.posix.isAbsolute(win), '…and is not under posix rules');
  // Whichever platform this runs on, mediaPathOf agrees with it.
  assert.equal(mediaPathOf(mediaUrl(win)) !== null, path.isAbsolute(win));
});

test('the path travels as a query parameter, not as the URL path', () => {
  const url = mediaUrl('/data/x.mp4');
  assert.ok(url.startsWith(`${MEDIA_SCHEME}://file/?p=`), url);
  assert.ok(url.includes('%2F'), 'the path must be percent-encoded, not left to the URL parser');
});

test('a relative or missing path is refused', () => {
  // The handler resolves nothing on the renderer's behalf: a relative path has
  // no defined base in the main process, so it can only be a mistake.
  assert.equal(mediaPathOf(`${MEDIA_SCHEME}://file/?p=videos/cam.mp4`), null);
  assert.equal(mediaPathOf(`${MEDIA_SCHEME}://file/?p=`), null);
  assert.equal(mediaPathOf(`${MEDIA_SCHEME}://file/?p=%20%20`), null);
  assert.equal(mediaPathOf(`${MEDIA_SCHEME}://file/`), null);
  assert.equal(mediaPathOf('not a url'), null);
});

test('traversal is normalized away before the extension is checked', () => {
  // `/data/x.mp4/../../etc/passwd` ends in neither `.mp4` nor a media type once
  // normalized — which is the point: normalizing AFTER the extension check
  // would let a path pass one and resolve to the other.
  const sneaky = mediaPathOf(mediaUrl('/data/x.mp4/../../etc/passwd'));
  assert.equal(sneaky, '/etc/passwd');
  assert.equal(MEDIA_TYPES['.passwd'], undefined);
  assert.equal(MEDIA_TYPES[''], undefined);
  // The normalized form is what the handler extension-checks and opens, so the
  // two can never disagree.
  assert.equal(mediaPathOf(mediaUrl('/data/videos/./cam.mp4')), '/data/videos/cam.mp4');
});

test('only media extensions are servable', () => {
  for (const ext of ['.mp4', '.m4v', '.mov', '.webm', '.mkv', '.avi']) {
    assert.ok(MEDIA_TYPES[ext] !== undefined, ext);
  }
  // An allowlist, not a denylist: the renderer picks the path, and keeping the
  // scheme narrow means a bug there cannot make it a general file reader.
  for (const ext of ['.json', '.parquet', '.js', '.pem', '.sh', '.txt', '']) {
    assert.equal(MEDIA_TYPES[ext], undefined, ext);
  }
});

test('sftp flavour: round-trip, host discrimination, traversal refused', async () => {
  const { mediaSftpOf, mediaSftpUrl } = await import('./media_policy.ts');
  const url = mediaSftpUrl('s7', '/data/ds/videos/cam.mp4');
  assert.deepEqual(mediaSftpOf(url), { sessionId: 's7', path: '/data/ds/videos/cam.mp4' });
  // The two flavours never cross: a file URL is not an sftp target and an
  // sftp URL must never resolve as a LOCAL path.
  assert.equal(mediaSftpOf(mediaUrl('/data/x.mp4')), null);
  assert.equal(mediaPathOf(url), null);
  // Remote paths are POSIX-absolute and normalize BEFORE the extension check
  // — the same posture as the local flavour: any absolute path is requestable
  // (the renderer already holds sftp_read), the allowlist bounds what serves.
  assert.equal(mediaSftpOf(mediaSftpUrl('s7', 'videos/cam.mp4')), null);
  assert.equal(mediaSftpOf(mediaSftpUrl('s7', '/../etc/passwd'))?.path, '/etc/passwd');
  assert.equal(mediaSftpOf(mediaSftpUrl('s7', '/a/../b/cam.mp4'))?.path, '/b/cam.mp4');
  assert.equal(mediaSftpOf(`${MEDIA_SCHEME}://sftp/?p=%2Fx.mp4`), null); // no session
  assert.equal(mediaSftpOf(`${MEDIA_SCHEME}://sftp/?s=s7`), null); // no path
});

test('Inspect previews widen WHAT is served, not where it is read from', async () => {
  const { mediaSftpOf, mediaSftpUrl } = await import('./media_policy.ts');
  // Inspect can be pointed at any file, so image/audio/pdf join video in the
  // allowlist. Each is a type a <video>/<audio>/<img>/<iframe> decodes on its
  // own — nothing here needs a parser of ours.
  for (const ext of ['.png', '.jpg', '.jpeg', '.gif', '.webp', '.avif', '.bmp', '.ico', '.tif', '.tiff']) {
    assert.match(MEDIA_TYPES[ext] ?? '', /^image\//, ext);
  }
  for (const ext of ['.mp3', '.m4a', '.wav', '.oga', '.ogg', '.flac', '.aac', '.opus']) {
    assert.match(MEDIA_TYPES[ext] ?? '', /^audio\//, ext);
  }
  assert.equal(MEDIA_TYPES['.pdf'], 'application/pdf');
  // ACTIVE documents stay out. Both can script and reach back into this
  // scheme's own session, so serving one would hand a file the renderer merely
  // pointed at the app's privileges.
  assert.equal(MEDIA_TYPES['.svg'], undefined);
  assert.equal(MEDIA_TYPES['.html'], undefined);
  assert.equal(MEDIA_TYPES['.htm'], undefined);
  assert.equal(MEDIA_TYPES['.xhtml'], undefined);
  // Widening the type list must not have widened the path rules.
  assert.equal(mediaPathOf(`${MEDIA_SCHEME}://file/?p=relative.png`), null);
  assert.equal(mediaSftpOf(mediaSftpUrl('s1', 'rel.png')), null);
});

test('every extension the renderer previews is one this scheme serves', async () => {
  // The renderer package mirrors this list (`src/state/inspectMedia.ts`) because
  // the two packages cannot import each other. A row present there and absent
  // here is a tab that mounts a player against a 415 — an empty box with no
  // error anywhere. Pin the union so the drift fails HERE.
  const rendererPreviews = [
    'mp4', 'm4v', 'mov', 'webm', 'mkv', 'avi', 'ogv',
    'png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp', 'avif', 'ico', 'tif', 'tiff',
    'mp3', 'm4a', 'wav', 'oga', 'ogg', 'flac', 'aac', 'opus',
    'pdf',
  ];
  for (const ext of rendererPreviews) {
    assert.ok(MEDIA_TYPES[`.${ext}`] !== undefined, `renderer previews .${ext} but the scheme will not serve it`);
  }
});

test('readTextStrict names the file instead of leaking the decoder error', async () => {
  const { NOT_TEXT_PREFIX, readTextStrict } = await import('./ipc/fsutil.ts');
  const fs = await import('node:fs/promises');
  const os = await import('node:os');
  const path = await import('node:path');
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'fsutil-'));
  const bin = path.join(dir, 'clip.mp4');
  // A lone 0xFF is not valid UTF-8 in any position — the shortest possible
  // stand-in for the mp4 that produced the reported crash.
  await fs.writeFile(bin, Buffer.from([0x00, 0x01, 0xff, 0xfe]));
  await assert.rejects(
    () => readTextStrict(bin),
    (e: Error) => {
      // The old behaviour was a bare TypeError from the decoder, which names
      // neither the file nor anything a UI can branch on.
      assert.ok(e.message.includes(NOT_TEXT_PREFIX), `message was: ${e.message}`);
      assert.ok(e.message.includes(bin), 'the message must name the file');
      assert.ok(!e.message.includes('The encoded data'), 'the decoder message must not leak through');
      return true;
    },
  );
  // Valid UTF-8 still reads, multi-byte included — the strictness is the point
  // for Author's tree walk and must survive this change. Written as escapes so
  // this source file stays ASCII (repo rule) while the BYTES on disk are 2-, 3-
  // and 4-byte sequences, which is what the decoder is being asked about.
  const good = path.join(dir, 'ok.txt');
  const multibyte = '\u00e9 \u2014 \u6f22 \u{1f600}\n'; // 2-, 3-, 3-, 4-byte
  await fs.writeFile(good, multibyte, 'utf8');
  assert.equal(await readTextStrict(good), multibyte);
  // A missing file is a DIFFERENT failure and must keep its own error.
  await assert.rejects(() => readTextStrict(path.join(dir, 'nope.txt')), (e: Error) => {
    assert.ok(!e.message.includes(NOT_TEXT_PREFIX), 'ENOENT must not read as "not text"');
    return true;
  });
  await fs.rm(dir, { recursive: true, force: true });
});
