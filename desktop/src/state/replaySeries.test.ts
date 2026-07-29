import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  readSeriesPage,
  rangeOf,
  channelPath,
  timeToX,
  xToTime,
  nearestPointIndex,
  valueAt,
  formatSample,
} from './replaySeries.ts';

// The wire shape below mirrors what hub/internal/hostrunner/datasetmeta emits
// for lerobot/nyu_rot_dataset episode 0 — 40 frames at 5 fps, a 7-channel
// `action` with motor names — rather than an idealised one.
function page(overrides: Record<string, unknown> = {}) {
  return {
    episode: 0,
    length: 40,
    fps: 5,
    stride: 1,
    points: 3,
    timestamps: [0, 0.2, 0.4],
    series: [
      {
        key: 'action',
        dtype: 'float32',
        channels: [
          { name: 'motor_0', values: [0, 1, 2] },
          { name: 'motor_1', values: [1, 1, 1] },
        ],
      },
    ],
    ...overrides,
  };
}

test('a series page reads into plot-ready shape', () => {
  const v = readSeriesPage(page(), 100, 20);
  assert.equal(v.hasSeries, true);
  assert.equal(v.length, 40);
  assert.equal(v.fps, 5);
  assert.equal(v.points, 3);
  assert.equal(v.features.length, 1);
  assert.equal(v.features[0].channels.length, 2);
  assert.equal(v.features[0].channels[0].name, 'motor_0');
  assert.equal(v.durationSec, 0.4);
});

test('an absent or empty page is "no series", not an empty plot', () => {
  // A flat line at zero and "nothing was read" look identical on screen and
  // are completely different facts.
  assert.equal(readSeriesPage(undefined, 100, 20).hasSeries, false);
  assert.equal(readSeriesPage(null, 100, 20).hasSeries, false);
  assert.equal(readSeriesPage({}, 100, 20).hasSeries, false);
  assert.equal(readSeriesPage({ timestamps: [], series: [] }, 100, 20).hasSeries, false);
});

test('duration falls back to length/fps when timestamps are missing', () => {
  const v = readSeriesPage(page({ timestamps: [] }), 100, 20);
  assert.equal(v.durationSec, 8); // 40 frames / 5 fps
});

test('the y-range is shared across a feature, not per channel', () => {
  // Seven joint angles of one arm are the same physical quantity. Normalizing
  // each independently would draw a motionless joint exactly like a sweeping
  // one — the most misleading thing a multi-channel plot can do.
  const v = readSeriesPage(page(), 100, 20);
  const f = v.features[0];
  assert.equal(f.min, 0);
  assert.equal(f.max, 2);
  // motor_1 is constant at 1, i.e. the middle of the feature's 0..2 range, so
  // it must sit at mid-height — NOT at the bottom, which is where a
  // per-channel range would put a flat line.
  assert.equal(f.channels[1].path, 'M0 10 L50 10 L100 10');
});

test('a gap breaks the line instead of interpolating across it', () => {
  // A null reading arrives as JSON null (there is no NaN literal). Drawing
  // through it asserts readings that were never taken.
  const v = readSeriesPage(
    page({
      timestamps: [0, 0.2, 0.4, 0.6, 0.8],
      series: [{ key: 'a', channels: [{ name: '', values: [0, null, 1, 1, null] }] }],
    }),
    100,
    20,
  );
  const path = v.features[0].channels[0].path;
  // Two subpaths: the lone leading sample, then the pair.
  assert.equal(path.split('M').length - 1, 2);
  assert.ok(!path.includes('NaN'), path);
});

test('an isolated sample between two gaps is still drawn', () => {
  // A bare moveto paints nothing, so a single reading surrounded by gaps would
  // silently vanish from the plot.
  const p = channelPath([NaN, 5, NaN], 0, 10, 100, 20);
  assert.equal(p, 'M50 10 L50 10');
});

test('a flat channel draws down the middle rather than dividing by zero', () => {
  const p = channelPath([3, 3, 3], 3, 3, 100, 20);
  assert.equal(p, 'M0 10 L50 10 L100 10');
  assert.ok(!p.includes('NaN'));
});

test('y is inverted so the maximum is at the top', () => {
  const p = channelPath([0, 10], 0, 10, 100, 20);
  assert.equal(p, 'M0 20 L100 0');
});

test('a single-point channel is centred', () => {
  assert.equal(channelPath([5], 0, 10, 100, 20), 'M50 10 L50 10');
});

test('degenerate plot boxes produce no path rather than nonsense', () => {
  assert.equal(channelPath([1, 2], 0, 2, 0, 20), '');
  assert.equal(channelPath([1, 2], 0, 2, 100, 0), '');
  assert.equal(channelPath([], 0, 2, 100, 20), '');
});

test('an all-gap feature has a zero range and an empty path', () => {
  assert.deepEqual(rangeOf([[NaN, NaN]]), { min: 0, max: 0 });
  assert.deepEqual(rangeOf([]), { min: 0, max: 0 });
  assert.equal(channelPath([NaN, NaN], 0, 0, 100, 20), '');
});

test('rangeOf ignores gaps and spans every channel', () => {
  assert.deepEqual(
    rangeOf([
      [1, NaN, 3],
      [-2, 0],
    ]),
    { min: -2, max: 3 },
  );
});

test('time maps to x and back', () => {
  assert.equal(timeToX(0, 8, 200), 0);
  assert.equal(timeToX(4, 8, 200), 100);
  assert.equal(timeToX(8, 8, 200), 200);
  // Out-of-range input clamps rather than drawing the cursor off the plot.
  assert.equal(timeToX(-1, 8, 200), 0);
  assert.equal(timeToX(99, 8, 200), 200);
  assert.equal(xToTime(100, 8, 200), 4);
  assert.equal(xToTime(-5, 8, 200), 0);
  assert.equal(xToTime(999, 8, 200), 8);
  // A zero-duration or zero-width plot has nowhere to point at.
  assert.equal(timeToX(4, 0, 200), 0);
  assert.equal(xToTime(50, 8, 0), 0);
});

test('the cursor snaps to the NEAREST sample, not the preceding one', () => {
  // On a decimated series consecutive samples can be seconds apart; always
  // rounding down makes the readout lag the cursor by up to a whole stride.
  const ts = [0, 1, 2, 3];
  assert.equal(nearestPointIndex(ts, 0), 0);
  assert.equal(nearestPointIndex(ts, 1.4), 1);
  assert.equal(nearestPointIndex(ts, 1.6), 2);
  assert.equal(nearestPointIndex(ts, 2.5), 2); // a tie takes the earlier sample
  assert.equal(nearestPointIndex(ts, -5), 0);
  assert.equal(nearestPointIndex(ts, 99), 3);
  assert.equal(nearestPointIndex([], 1), -1);
});

test('the cursor search agrees with a linear scan on an irregular axis', () => {
  // The binary search exists because a scrub fires it on every pointer move.
  // Pin it against the obvious implementation rather than against itself.
  const ts = [0, 0.05, 0.4, 0.41, 2, 7, 7.001, 12];
  const linear = (t: number): number => {
    let best = 0;
    for (let i = 1; i < ts.length; i++) {
      if (Math.abs(ts[i] - t) < Math.abs(ts[best] - t)) best = i;
    }
    return best;
  };
  for (let t = -1; t <= 13; t += 0.017) {
    assert.equal(nearestPointIndex(ts, t), linear(t), `t=${t}`);
  }
});

test('a gap reads as absent, not as zero', () => {
  const ch = { name: 'x', values: [1, NaN, 3], path: '' };
  assert.equal(valueAt(ch, 0), 1);
  assert.equal(valueAt(ch, 1), undefined);
  assert.equal(valueAt(ch, 9), undefined);
  assert.equal(formatSample(valueAt(ch, 1)), '—');
});

test('samples format with enough precision for a joint angle', () => {
  assert.equal(formatSample(0), '0');
  assert.equal(formatSample(0.123456), '0.1235');
  assert.equal(formatSample(3.14159), '3.142');
  assert.equal(formatSample(-142.5), '-142.5');
  assert.equal(formatSample(123456), '1.23e+5');
  assert.equal(formatSample(0.0000123), '1.23e-5');
});

test('caps and warnings survive the read', () => {
  const v = readSeriesPage(
    page({ downsampled: true, truncated: true, stride: 4, warnings: ['no timestamp column'] }),
    100,
    20,
  );
  assert.equal(v.downsampled, true);
  assert.equal(v.truncated, true);
  assert.equal(v.stride, 4);
  assert.deepEqual(v.warnings, ['no timestamp column']);
  // A missing stride must never read as 0 — the UI divides by it.
  assert.equal(readSeriesPage(page({ stride: undefined }), 100, 20).stride, 1);
});
