import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  readDatasetSummary,
  readEpisodePage,
  episodeEnvRef,
  scaleHistogram,
  formatDuration,
  formatCount,
  formatResolution,
  pageRangeLabel,
  datasetRootFromMetaInfo,
  resolveHandoff,
} from './replayDigest.ts';

// The wire shapes below mirror what hub/internal/hostrunner/datasetmeta
// actually emits for lerobot/nyu_rot_dataset — 14 episodes, 440 frames at
// 5 fps, one 84x84 camera — rather than an idealised shape. In particular
// `names: null` on scalar features is real, and it is the reason dimension is
// read from `shape`.
function nyuDataset(overrides: Record<string, unknown> = {}) {
  return {
    id: 'ds-1',
    name: 'nyu_rot_dataset',
    root_path: '/data/nyu',
    format: 'lerobot_v3.0',
    digest_ts: '2026-07-29T04:00:00Z',
    digest: {
      schema_version: 1,
      format: 'lerobot_v3.0',
      codebase_version: 'v3.0',
      robot_type: 'unknown',
      fps: 5,
      total_episodes: 14,
      total_frames: 440,
      total_tasks: 12,
      duration_sec: 88,
      video_streams: [
        { key: 'observation.images.image', name: 'image', width: 84, height: 84, channels: 3, fps: 5 },
      ],
      features: [
        { key: 'action', dtype: 'float32', shape: [7] },
        { key: 'next.done', dtype: 'bool', shape: [1] },
        { key: 'timestamp', dtype: 'float32', shape: [1], names: null },
      ],
      tasks: ['close the door', 'erase the board'],
      length_histogram: [
        { from: 30, to: 35, count: 12 },
        { from: 35, to: 40, count: 2 },
      ],
      episodes_scanned: 14,
      ...(overrides.digest as Record<string, unknown> | undefined),
    },
    ...overrides,
  };
}

test('reads the headline facts off a real-shaped digest', () => {
  const s = readDatasetSummary(nyuDataset());
  assert.equal(s.hasDigest, true);
  assert.equal(s.format, 'lerobot_v3.0');
  assert.equal(s.codebaseVersion, 'v3.0');
  assert.equal(s.episodes, 14);
  assert.equal(s.frames, 440);
  assert.equal(s.tasksTotal, 12);
  assert.equal(s.fps, 5);
  assert.equal(s.durationSec, 88);
  assert.equal(s.videoStreams.length, 1);
  assert.equal(s.videoStreams[0].name, 'image');
  assert.equal(formatResolution(s.videoStreams[0]), '84x84');
});

// A dataset registered but never read has no digest. That is a different state
// from a dataset whose digest says zero, and the UI has to be able to tell them
// apart — otherwise an unread dataset renders as an empty one and the user has
// no reason to press Refresh.
test('a never-refreshed dataset is distinguishable from an empty one', () => {
  const unread = readDatasetSummary({ id: 'ds-2', name: 'x', root_path: '/d' });
  assert.equal(unread.hasDigest, false);
  assert.equal(unread.episodes, 0);
  assert.equal(unread.digestTS, '');

  // A digest key present but with no timestamp still counts as unread: the
  // timestamp is what proves a host actually answered.
  const noTS = readDatasetSummary({ id: 'ds-3', digest: { total_episodes: 5 } });
  assert.equal(noTS.hasDigest, false);

  const emptyButRead = readDatasetSummary({
    id: 'ds-4',
    digest_ts: '2026-07-29T04:00:00Z',
    digest: { format: 'lerobot_v2.1', total_episodes: 0, total_frames: 0 },
  });
  assert.equal(emptyButRead.hasDigest, true);
  assert.equal(emptyButRead.episodes, 0);
});

// Real LeRobot files carry `"names": null` on every scalar feature. Deriving a
// dimension from names.length would report 0 for half the feature table.
test('feature dimension comes from shape, not from names', () => {
  const s = readDatasetSummary(nyuDataset());
  const action = s.features.find((f) => f.key === 'action');
  assert.ok(action);
  assert.equal(action.dim, 7);
  assert.equal(action.names, undefined);

  const ts = s.features.find((f) => f.key === 'timestamp');
  assert.ok(ts);
  assert.equal(ts.dim, 1, 'a null names field must not zero the dimension');

  // Multi-axis shapes multiply out.
  const multi = readDatasetSummary({
    digest_ts: 't',
    digest: { features: [{ key: 'grid', dtype: 'float32', shape: [4, 3] }] },
  });
  assert.equal(multi.features[0].dim, 12);
});

test('video features do not appear among the plain features', () => {
  const s = readDatasetSummary(nyuDataset());
  assert.ok(!s.features.some((f) => f.key.includes('images')));
});

test('histogram bars scale against the tallest bar, not the total', () => {
  const bars = scaleHistogram([
    { from: 0, to: 10, count: 5 },
    { from: 10, to: 20, count: 10 },
    { from: 20, to: 30, count: 0 },
  ]);
  assert.equal(bars.length, 3);
  assert.equal(bars[0].height, 0.5);
  assert.equal(bars[1].height, 1);
  // A zero bucket must stay flat so a gap in the distribution reads as a gap.
  assert.equal(bars[2].height, 0);
});

test('an all-zero or empty histogram does not divide by zero', () => {
  assert.deepEqual(scaleHistogram([]), []);
  assert.deepEqual(scaleHistogram(undefined), []);
  assert.deepEqual(scaleHistogram('nonsense'), []);
  const zeros = scaleHistogram([{ from: 0, to: 1, count: 0 }]);
  assert.equal(zeros[0].height, 0);
  assert.ok(Number.isFinite(zeros[0].height));
});

test('caps and warnings survive to the reader', () => {
  const s = readDatasetSummary({
    digest_ts: 't',
    digest: {
      tasks: ['a'],
      tasks_truncated: true,
      stats_partial: true,
      episodes_truncated: true,
      warnings: ['meta/tasks.jsonl is missing; task list omitted', ''],
    },
  });
  assert.equal(s.tasksTruncated, true);
  assert.equal(s.statsPartial, true);
  assert.equal(s.episodesTruncated, true);
  assert.deepEqual(s.warnings, ['meta/tasks.jsonl is missing; task list omitted']);
});

test('malformed wire values degrade instead of throwing', () => {
  const s = readDatasetSummary({
    digest_ts: 't',
    digest: {
      total_episodes: 'lots',
      fps: null,
      video_streams: 'not-an-array',
      features: [null, 42, { key: 'ok', dtype: 'float32', shape: [2] }],
      tasks: [1, 'real', null],
      length_histogram: [{ from: 'a', to: 'b', count: 'c' }],
    },
  });
  assert.equal(s.episodes, 0);
  assert.equal(s.fps, 0);
  assert.deepEqual(s.videoStreams, []);
  assert.equal(s.features.length, 1);
  assert.deepEqual(s.tasks, ['real']);
  assert.equal(s.histogram[0].height, 0);
});

test('reads an episode page', () => {
  const view = readEpisodePage({
    episodes: [
      { index: 0, length: 40, duration_sec: 8, tasks: ['erase the board'], from_index: 0, to_index: 40 },
      { index: 1, length: 30, duration_sec: 6, tasks: ['close the door'], from_index: 40, to_index: 70 },
    ],
    offset: 0,
    limit: 2,
    total: 14,
  });
  assert.equal(view.rows.length, 2);
  assert.equal(view.rows[0].length, 40);
  assert.equal(view.rows[0].toIndex, 40);
  assert.equal(view.total, 14);
  assert.equal(view.truncated, false);
});

// Episode 0's row range starts at 0. Testing presence by truthiness would erase
// it and make the first row of every v3.0 dataset look like a layout with no
// offsets at all.
test('a zero row-offset is kept, not treated as absent', () => {
  const view = readEpisodePage({
    episodes: [{ index: 0, length: 40, from_index: 0, to_index: 40 }],
  });
  assert.equal(view.rows[0].fromIndex, 0);
  assert.equal(view.rows[0].toIndex, 40);
});

test('a per-episode-file layout simply has no row range', () => {
  const view = readEpisodePage({ episodes: [{ index: 0, length: 40 }] });
  assert.equal(view.rows[0].fromIndex, undefined);
  assert.equal(view.rows[0].toIndex, undefined);
});

test('page range label is 1-based and inclusive, and absent when empty', () => {
  assert.deepEqual(
    pageRangeLabel({
      rows: [{ index: 0, length: 1, durationSec: 0, tasks: [], videos: [], envRef: '' }],
      offset: 0,
      limit: 1,
      total: 14,
      truncated: false,
    }),
    { from: 1, to: 1, total: 14 },
  );
  assert.deepEqual(
    pageRangeLabel({
      rows: Array.from({ length: 5 }, (_, i) => ({ index: i, length: 1, durationSec: 0, tasks: [], videos: [], envRef: '' })),
      offset: 200,
      limit: 5,
      total: 50_000,
      truncated: false,
    }),
    { from: 201, to: 205, total: 50_000 },
  );
  assert.equal(pageRangeLabel({ rows: [], offset: 0, limit: 0, total: 0, truncated: false }), null);
});

test('durations read the way the domain reads them', () => {
  // Episodes are commonly 6-40s, where a tenth of a second is a real
  // difference; dataset totals are not.
  assert.equal(formatDuration(6.4), '6.4s');
  assert.equal(formatDuration(8), '8s');
  assert.equal(formatDuration(59.94), '59.9s');
  assert.equal(formatDuration(88), '1m 28s');
  assert.equal(formatDuration(120), '2m');
  assert.equal(formatDuration(3600), '1h');
  assert.equal(formatDuration(3661), '1h 1m');
  assert.equal(formatDuration(0), '0s');
  assert.equal(formatDuration(-5), '0s');
  assert.equal(formatDuration(NaN), '0s');
});

test('counts get thousands separators', () => {
  assert.equal(formatCount(0), '0');
  assert.equal(formatCount(440), '440');
  assert.equal(formatCount(11939), '11,939');
  assert.equal(formatCount(3254196), '3,254,196');
  assert.equal(formatCount(NaN), '0');
});

test('resolution formatting tolerates a half-known geometry', () => {
  assert.equal(formatResolution({ key: 'k', name: 'k', width: 640, height: 480 }), '640x480');
  assert.equal(formatResolution({ key: 'k', name: 'k', width: 640 }), '640');
  assert.equal(formatResolution({ key: 'k', name: 'k' }), '');
});

test('a stream missing its name falls back to the full key', () => {
  const s = readDatasetSummary({
    digest_ts: 't',
    digest: { video_streams: [{ key: 'observation.images.up' }] },
  });
  assert.equal(s.videoStreams[0].name, 'observation.images.up');
});

// ── the Inspect handoff gate (W1d) ───────────────────────────────────────────

test('a meta/info.json row resolves to the directory containing meta/', () => {
  // The root is the dataset directory, NOT the meta directory — getting this
  // wrong registers a root the host will read as an empty dataset.
  assert.equal(datasetRootFromMetaInfo('/data/nyu_rot/meta/info.json'), '/data/nyu_rot');
  assert.equal(datasetRootFromMetaInfo('/data/nyu_rot/meta/'), null);
  assert.equal(datasetRootFromMetaInfo('/data/nyu_rot/meta'), null);
});

test('the gate is separator-agnostic and preserves the input style', () => {
  // A Windows local root and an SFTP path both flow through this one rule, and
  // the result goes back to a host that expects its own separators.
  assert.equal(datasetRootFromMetaInfo('C:\\data\\nyu_rot\\meta\\info.json'), 'C:\\data\\nyu_rot');
  assert.equal(datasetRootFromMetaInfo('/srv/robot data/ds one/meta/info.json'), '/srv/robot data/ds one');
});

test('only meta/info.json opens the handoff', () => {
  for (const p of [
    '/data/ds/meta/episodes.jsonl',
    '/data/ds/info.json',
    '/data/ds/meta/info.jsonl',
    '/data/ds/meta/info.json.bak',
    '/data/ds/Meta/info.json', // LeRobot writes it lowercase; don't guess at casing
    '/data/ds/meta/sub/info.json',
    '',
  ]) {
    assert.equal(datasetRootFromMetaInfo(p), null, p);
  }
});

test('a dataset nested under a directory named meta resolves to its own root', () => {
  // Greedy prefix: the last meta/info.json wins.
  assert.equal(datasetRootFromMetaInfo('/srv/meta/ds/meta/info.json'), '/srv/meta/ds');
});

test('a root with nothing above it is refused rather than guessed at', () => {
  assert.equal(datasetRootFromMetaInfo('/meta/info.json'), null);
  assert.equal(datasetRootFromMetaInfo('meta/info.json'), null);
});

test('a relative root survives the gate for the form to correct', () => {
  // Not a validity check: the host refuses a relative root with a message that
  // names the problem, which beats an action that silently never appears.
  assert.equal(datasetRootFromMetaInfo('datasets/ds/meta/info.json'), 'datasets/ds');
});

const LIB = [
  { id: 'ds-local', root_path: '/data/nyu_rot', host_id: '' },
  { id: 'ds-star', root_path: '/srv/data/nyu_rot', host_id: 'host-star' },
];

test('a handoff to an already-registered location selects it', () => {
  assert.deepEqual(resolveHandoff({ rootPath: '/srv/data/nyu_rot' }, LIB), {
    action: 'select',
    datasetId: 'ds-star',
  });
  assert.deepEqual(resolveHandoff({ rootPath: '/data/nyu_rot' }, LIB), {
    action: 'select',
    datasetId: 'ds-local',
  });
});

test('the same path on two hosts is ambiguous, so it goes to the form', () => {
  // The handoff carries no host — Inspect has an SSH connection id, not a hub
  // host id — so two rows sharing a path cannot be told apart here. Picking
  // either would be a coin flip between two machines' datasets; the form's host
  // select is where that question actually gets answered.
  const twoHosts = [
    { id: 'ds-a', root_path: '/srv/data/ds', host_id: 'host-star' },
    { id: 'ds-b', root_path: '/srv/data/ds', host_id: 'host-gpu' },
  ];
  assert.deepEqual(resolveHandoff({ rootPath: '/srv/data/ds' }, twoHosts), {
    action: 'register',
    rootPath: '/srv/data/ds',
  });
});

test('a trailing separator is not part of a root path identity', () => {
  // The hub stores the string as sent (only the host cleans it, at read time),
  // so both spellings are the same dataset — a raw compare would offer to
  // register one that is already registered.
  assert.deepEqual(resolveHandoff({ rootPath: '/data/nyu_rot/' }, LIB), {
    action: 'select',
    datasetId: 'ds-local',
  });
  assert.deepEqual(resolveHandoff({ rootPath: '/data/nyu_rot' }, [{ id: 'x', root_path: '/data/nyu_rot/' }]), {
    action: 'select',
    datasetId: 'x',
  });
});

test('an unregistered location, and an empty library, resolve to register', () => {
  assert.deepEqual(resolveHandoff({ rootPath: '/data/other' }, LIB), {
    action: 'register',
    rootPath: '/data/other',
  });
  assert.deepEqual(resolveHandoff({ rootPath: '/data/ds' }, []), {
    action: 'register',
    rootPath: '/data/ds',
  });
});

test('a row without a usable id cannot be selected', () => {
  // Defensive for the same reason the rest of this module is: the hub row is an
  // untyped map, and "select ''" would silently fall back to the first dataset.
  assert.deepEqual(resolveHandoff({ rootPath: '/data/ds' }, [{ root_path: '/data/ds' }]), {
    action: 'register',
    rootPath: '/data/ds',
  });
  // …and a malformed row must not hide a good one at the same path.
  assert.deepEqual(
    resolveHandoff({ rootPath: '/data/ds' }, [{ root_path: '/data/ds' }, { id: 'good', root_path: '/data/ds' }]),
    { action: 'select', datasetId: 'good' },
  );
});

// ── episode video slices (W2d) ───────────────────────────────────────────────

test('video slices read into a key-sorted list', () => {
  // Sorted, because map order off the wire is not a layout decision and a
  // multi-camera grid that reshuffles its panes between reads is unusable.
  const view = readEpisodePage({
    episodes: [
      {
        index: 1,
        length: 30,
        videos: {
          'observation.images.up': { path: 'videos/up/chunk-000/file-000.mp4', from_ts: 8, to_ts: 14 },
          'observation.images.side': { path: 'videos/side/chunk-000/file-000.mp4', from_ts: 8, to_ts: 14 },
        },
      },
    ],
  });
  const vs = view.rows[0].videos;
  assert.equal(vs.length, 2);
  assert.deepEqual(
    vs.map((v) => v.key),
    ['observation.images.side', 'observation.images.up'],
  );
  assert.equal(vs[1].path, 'videos/up/chunk-000/file-000.mp4');
  assert.equal(vs[1].fromTS, 8);
  assert.equal(vs[1].toTS, 14);
});

test('a slice with no path is dropped, not rendered as a broken pane', () => {
  // The host omits the path when the template could not be resolved. A pane
  // pointing nowhere looks like broken video; an absent one is honest.
  const view = readEpisodePage({
    episodes: [{ index: 0, length: 10, videos: { cam: { from_ts: 0, to_ts: 2 }, ok: { path: 'v.mp4' } } }],
  });
  assert.deepEqual(
    view.rows[0].videos.map((v) => v.key),
    ['ok'],
  );
});

test('an episode with no videos reads as an empty list, never undefined', () => {
  // The grid maps over this directly; undefined would be a crash on a
  // video-less dataset, which is a legitimate thing to register.
  const view = readEpisodePage({ episodes: [{ index: 0, length: 10 }] });
  assert.deepEqual(view.rows[0].videos, []);
  const bad = readEpisodePage({ episodes: [{ index: 0, videos: 'nonsense' }] });
  assert.deepEqual(bad.rows[0].videos, []);
});

// ── env_ref (environments plan E0) ───────────────────────────────────────────

test('a dataset env_ref is read from the row, not the digest', () => {
  // env_ref is a dataset COLUMN — a human can set it on a root no host has
  // ever read. Reading it out of the digest would make it invisible exactly
  // when it is the only thing known about the dataset.
  const s = readDatasetSummary(nyuDataset({ env_ref: 'lerobot:so100_follower' }));
  assert.equal(s.envRef, 'lerobot:so100_follower');

  const unread = readDatasetSummary({
    id: 'ds-2',
    root_path: '/data/new',
    env_ref: 'lab:bench-3@2026-07',
  });
  assert.equal(unread.hasDigest, false);
  assert.equal(unread.envRef, 'lab:bench-3@2026-07');
});

test('an episode inherits its dataset env_ref and overrides it when it has one', () => {
  const summary = readDatasetSummary(nyuDataset({ env_ref: 'lerobot:so100_follower' }));
  const view = readEpisodePage({
    episodes: [
      { index: 0, length: 10 },
      { index: 1, length: 10, env_ref: 'lab:bench-3@2026-07' },
    ],
  });
  // Inherit: the host does not repeat the dataset's handle on every row, so
  // reading `row.envRef` directly would report "no environment" for the whole
  // dataset — the resolution is the point.
  assert.equal(view.rows[0].envRef, '');
  assert.equal(episodeEnvRef(view.rows[0], summary), 'lerobot:so100_follower');
  // Override: an episode recorded somewhere else wins over its dataset.
  assert.equal(episodeEnvRef(view.rows[1], summary), 'lab:bench-3@2026-07');
});

test('an episode of a dataset with no env_ref resolves to nothing, not to a guess', () => {
  const summary = readDatasetSummary(nyuDataset());
  const view = readEpisodePage({ episodes: [{ index: 0, length: 10 }] });
  assert.equal(summary.envRef, '');
  assert.equal(episodeEnvRef(view.rows[0], summary), '');
});
