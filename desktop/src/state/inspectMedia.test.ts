/// Inspect's media previews. Run locally:
/// `node --test src/state/inspectMedia.test.ts` from `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  canStreamMedia,
  isNotTextError,
  mediaFileUrl,
  mediaKindFor,
  mediaSftpUrl,
} from './inspectMedia.ts';
import { kindForInspectFile } from './inspect.ts';

test('the reported bug: an mp4 classifies as video, never as code', () => {
  // Before this, every media extension fell through to `code`, whose read path
  // is a strict-UTF-8 slurp — so opening one surfaced the decoder's
  // "The encoded data was not valid for encoding utf-8" instead of a player.
  assert.equal(kindForInspectFile('mp4', ''), 'video');
  assert.equal(kindForInspectFile('MP4', ''), 'video');
  assert.equal(kindForInspectFile('png', ''), 'image');
  assert.equal(kindForInspectFile('wav', ''), 'audio');
  assert.equal(kindForInspectFile('pdf', ''), 'pdf');
});

test('media wins over every text branch, including the content sniff', () => {
  // A file can be named `.mp4` and still hold bytes that look like a diff to a
  // sniffer. The extension is the honest signal here: the sniff exists for
  // PASTED text with no extension, and running it over binary is how a video
  // ends up in a text viewer.
  const patch = 'diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n';
  assert.equal(kindForInspectFile('mp4', patch), 'video');
  assert.equal(kindForInspectFile('png', 'digraph { a -> b }'), 'image');
  // …and a real pasted patch is still a diff.
  assert.equal(kindForInspectFile('', patch), 'diff');
});

test('non-media extensions are untouched', () => {
  assert.equal(kindForInspectFile('py', ''), 'code');
  assert.equal(kindForInspectFile('log', ''), 'log');
  assert.equal(kindForInspectFile('patch', ''), 'diff');
  assert.equal(kindForInspectFile('safetensors', ''), 'model');
  assert.equal(kindForInspectFile('dot', ''), 'graph');
});

test('svg stays text', () => {
  // It is markup, the main process refuses to serve it through the media
  // scheme for that reason, and its source is the useful view of a file you
  // opened in an INSPECTOR.
  assert.equal(mediaKindFor('svg'), null);
  assert.equal(kindForInspectFile('svg', ''), 'code');
});

test('mediaKindFor tolerates a leading dot, case and padding', () => {
  assert.equal(mediaKindFor('.MP4'), 'video');
  assert.equal(mediaKindFor(' jpeg '), 'image');
  assert.equal(mediaKindFor(''), null);
  assert.equal(mediaKindFor('.'), null);
});

test('only the byte-streaming sources claim media', () => {
  // hub/github/hf decode to text in transport before the renderer sees them —
  // a preview there is a transport change, not a viewer change, so the surface
  // says so rather than mounting a player that can never load.
  assert.equal(canStreamMedia('local'), true);
  assert.equal(canStreamMedia('workspace'), true);
  assert.equal(canStreamMedia('remote'), true);
  assert.equal(canStreamMedia('hub'), false);
  assert.equal(canStreamMedia('github'), false);
  assert.equal(canStreamMedia('hf'), false);
  assert.equal(canStreamMedia('paste'), false);
});

test('URLs put the path in a query parameter, encoded', () => {
  // Absolute paths carry characters the URL path grammar fights over — a
  // Windows drive letter, spaces, `&`, `#`. Query-encoding sidesteps all of it,
  // and `media_policy.ts` reads it back with the same rule.
  assert.equal(mediaFileUrl('/data/a b/clip.mp4'), 'termipod-media://file/?p=%2Fdata%2Fa%20b%2Fclip.mp4');
  assert.equal(mediaFileUrl('C:\\data\\a&b.mp4'), 'termipod-media://file/?p=C%3A%5Cdata%5Ca%26b.mp4');
  assert.equal(mediaSftpUrl('sess-1', '/srv/x#1.mp4'), 'termipod-media://sftp/?s=sess-1&p=%2Fsrv%2Fx%231.mp4');
});

test('a missing path yields null, not a URL that 404s', () => {
  // A dead <video> reads as a broken player; null lets the caller say what is
  // actually wrong.
  assert.equal(mediaFileUrl(undefined), null);
  assert.equal(mediaFileUrl(''), null);
  assert.equal(mediaFileUrl('   '), null);
  assert.equal(mediaSftpUrl('', '/x.mp4'), null);
  assert.equal(mediaSftpUrl('s', '  '), null);
});

test('the not-text marker survives the IPC wrapper', () => {
  // This is the exact string the user saw, and the exact string the main
  // process now produces. The match must be a SUBSTRING test: Electron wraps a
  // rejected handler's error before the renderer ever sees it.
  const wrapped =
    "Error invoking remote method 'bridge:invoke': Error: not a UTF-8 text file: /home/u/clip.mp4";
  assert.equal(isNotTextError(wrapped), true);
  assert.equal(isNotTextError('not a UTF-8 text file: /x'), true);
  // The old, unhelpful message must NOT match — if it ever reappears the
  // placard would swallow it and we would lose the signal that the main
  // process regressed.
  assert.equal(
    isNotTextError("Error invoking remote method 'bridge:invoke': TypeError: The encoded data was not valid for encoding utf-8"),
    false,
  );
  assert.equal(isNotTextError('ENOENT: no such file or directory'), false);
  assert.equal(isNotTextError(''), false);
});
