import { test } from 'node:test';
import assert from 'node:assert/strict';
import { episodeVideoUrl, episodeTimeOf, fileTimeOf, isPastEnd, joinDatasetPath } from './replayMedia.ts';
import type { EpisodeVideo } from './replayDigest.ts';

// The two shapes below are the real ones, taken from the pinned fixtures:
// v3.0 packs all 14 episodes into one 88-second file and episode 1 is the
// 8s–14s slice of it; v2.1 gives episode 1 its own file, so the same six
// seconds are 0s–6s. Everything here exists so the player can treat them alike.
const v30: EpisodeVideo = {
  key: 'observation.images.image',
  path: 'videos/observation.images.image/chunk-000/file-000.mp4',
  fromTS: 8,
  toTS: 14,
};
const v21: EpisodeVideo = {
  key: 'observation.images.image',
  path: 'videos/chunk-000/observation.images.image/episode_000001.mp4',
  fromTS: 0,
  toTS: 6,
};

test('a dataset root joins a host-relative path', () => {
  assert.equal(joinDatasetPath('/data/nyu', 'videos/cam.mp4'), '/data/nyu/videos/cam.mp4');
  // Trailing and leading separators must not double up.
  assert.equal(joinDatasetPath('/data/nyu/', '/videos/cam.mp4'), '/data/nyu/videos/cam.mp4');
  assert.equal(joinDatasetPath('/data/nyu///', 'videos/cam.mp4'), '/data/nyu/videos/cam.mp4');
});

test('a Windows root keeps its own separator at the seam', () => {
  // The host always resolves the relative half with forward slashes, so only
  // the join needs to care; Windows accepts the mixed interior.
  assert.equal(joinDatasetPath('C:\\data\\nyu', 'videos/cam.mp4'), 'C:\\data\\nyu\\videos/cam.mp4');
  assert.equal(joinDatasetPath('C:\\data\\nyu\\', 'videos/cam.mp4'), 'C:\\data\\nyu\\videos/cam.mp4');
  // A path that already mixes separators is treated as POSIX-ish, which is
  // what a root typed into the register form on any platform looks like.
  assert.equal(joinDatasetPath('C:/data/nyu', 'videos/cam.mp4'), 'C:/data/nyu/videos/cam.mp4');
});

test('a video URL percent-encodes the whole path', () => {
  const url = episodeVideoUrl('/data/nyu', v30)!;
  assert.ok(url.startsWith('termipod-media://file/?p='), url);
  // The separators must be encoded rather than left for the URL parser to
  // interpret as path segments — that is the contract media_policy.ts reads.
  assert.ok(url.includes('%2F'), url);
  assert.equal(
    decodeURIComponent(url.slice('termipod-media://file/?p='.length)),
    '/data/nyu/videos/observation.images.image/chunk-000/file-000.mp4',
  );
});

test('awkward characters survive the round trip', () => {
  const url = episodeVideoUrl('/srv/robot data/ds one', { ...v30, path: 'videos/a+b&c/cam.mp4' })!;
  assert.equal(
    decodeURIComponent(url.slice('termipod-media://file/?p='.length)),
    '/srv/robot data/ds one/videos/a+b&c/cam.mp4',
  );
});

test('nothing to play yields null, not a broken URL', () => {
  // A `<video>` that fails to load reads as a broken player; an honest "no
  // video" reads as a fact about the dataset.
  assert.equal(episodeVideoUrl('', v30), null);
  assert.equal(episodeVideoUrl('   ', v30), null);
  assert.equal(episodeVideoUrl('/data/nyu', { ...v30, path: '' }), null);
  assert.equal(episodeVideoUrl('/data/nyu', { ...v30, path: '   ' }), null);
});

test('episode time maps onto the file clock for both generations', () => {
  // The whole point of the uniform slice: the caller works in episode time and
  // never learns which generation it is looking at.
  assert.equal(fileTimeOf(v30, 0), 8);
  assert.equal(fileTimeOf(v30, 2.5), 10.5);
  assert.equal(fileTimeOf(v21, 0), 0);
  assert.equal(fileTimeOf(v21, 2.5), 2.5);
  // Same episode moment, two files, two file times — one episode time.
  assert.equal(episodeTimeOf(v30, fileTimeOf(v30, 3)), 3);
  assert.equal(episodeTimeOf(v21, fileTimeOf(v21, 3)), 3);
});

test('a seek past the episode end clamps to the slice', () => {
  // Without this a scrub to the right-hand edge of a plot would run the shared
  // v3.0 file into the NEXT episode, which looks like the robot teleporting.
  assert.equal(fileTimeOf(v30, 99), 14);
  assert.equal(fileTimeOf(v21, 99), 6);
  assert.equal(fileTimeOf(v30, -5), 8);
  assert.equal(fileTimeOf(v30, NaN), 8);
});

test('an unknown duration is left to the file to bound', () => {
  // v2.1 with no fps: the host cannot derive a range, so it reports [0,0).
  // Clamping to that would pin the playhead at the start forever.
  const unknown: EpisodeVideo = { key: 'k', path: 'v.mp4', fromTS: 0, toTS: 0 };
  assert.equal(fileTimeOf(unknown, 4), 4);
  assert.equal(isPastEnd(unknown, 1000), false);
});

test('the end of a slice is where playback must stop', () => {
  assert.equal(isPastEnd(v30, 13.9), false);
  assert.equal(isPastEnd(v30, 14), true);
  assert.equal(isPastEnd(v30, 20), true);
  assert.equal(isPastEnd(v21, 5.9), false);
  assert.equal(isPastEnd(v21, 6), true);
});

test('episodeVideoSftpUrl mirrors the sftp flavour shape', async () => {
  const { episodeVideoSftpUrl } = await import('./replayMedia.ts');
  const v = { key: 'cam', path: 'videos/cam.mp4', fromTS: 0, toTS: 1 };
  assert.equal(
    episodeVideoSftpUrl('s7', '/data/ds', v),
    `termipod-media://sftp/?s=s7&p=${encodeURIComponent('/data/ds/videos/cam.mp4')}`,
  );
  // Null for anything unplayable, same contract as the local builder.
  assert.equal(episodeVideoSftpUrl('', '/data/ds', v), null);
  assert.equal(episodeVideoSftpUrl('s7', '', v), null);
  assert.equal(episodeVideoSftpUrl('s7', '/data/ds', { ...v, path: '' }), null);
});
