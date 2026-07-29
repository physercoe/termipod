import type { Entity } from '../hub/types';

/// Reader and plot geometry for one episode's channel series (J8 Replay W2 —
/// the fold produced by `datasetmeta.ReadSeries` and proxied by the hub).
///
/// Pure and i18n-free, the same posture as `replayDigest.ts`, and for the same
/// reason: the surface this feeds is one I cannot look at, so everything that
/// can be wrong about a number's *position* is extracted to where `node --test`
/// can assert it. What is left in the component is markup.

export interface ChannelView {
  name: string;
  values: number[];
  /// SVG path data, computed against the feature's shared range.
  path: string;
}

export interface FeatureView {
  key: string;
  dtype: string;
  channels: ChannelView[];
  /// The y-range the whole feature is drawn against. Shared across its
  /// channels ON PURPOSE: a 7-DoF arm's seven joint angles are the same
  /// physical quantity, and normalizing each independently would make a
  /// motionless joint look exactly as busy as a sweeping one — the single
  /// most misleading thing a multi-channel plot can do.
  min: number;
  max: number;
}

export interface SeriesView {
  hasSeries: boolean;
  episode: number;
  /// The episode's real frame count, before decimation.
  length: number;
  fps: number;
  stride: number;
  points: number;
  downsampled: boolean;
  truncated: boolean;
  warnings: string[];
  timestamps: number[];
  /// Seconds covered by the episode, from the last timestamp (or from
  /// length/fps when there are no timestamps to trust).
  durationSec: number;
  features: FeatureView[];
}

const EMPTY: SeriesView = {
  hasSeries: false,
  episode: 0,
  length: 0,
  fps: 0,
  stride: 1,
  points: 0,
  downsampled: false,
  truncated: false,
  warnings: [],
  timestamps: [],
  durationSec: 0,
  features: [],
};

function num(v: unknown): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : 0;
}
function str(v: unknown): string {
  return typeof v === 'string' ? v : '';
}
function arr(v: unknown): unknown[] {
  return Array.isArray(v) ? v : [];
}
function obj(v: unknown): Entity | undefined {
  return v && typeof v === 'object' && !Array.isArray(v) ? (v as Entity) : undefined;
}

/// Numbers off the wire, keeping NaN.
///
/// `num()` cannot be reused here: it maps every non-finite value to 0, and a
/// null reading rendered as 0 is a data point that never existed. JSON has no
/// NaN literal, so the host's NaN arrives as `null` — which is exactly the
/// signal a gap needs.
function values(v: unknown): number[] {
  return arr(v).map((n) => (typeof n === 'number' && Number.isFinite(n) ? n : NaN));
}

/// Read the hub's series page into plot-ready shape.
///
/// `width`/`height` are the plot box one channel is drawn in; paths are built
/// here rather than in the component so the geometry is testable.
export function readSeriesPage(page: Entity | undefined | null, width: number, height: number): SeriesView {
  if (!page) return EMPTY;
  const timestamps = values(page.timestamps);
  const rawFeatures = arr(page.series)
    .map((s) => obj(s))
    .filter((s): s is Entity => !!s);
  // A page with no series at all is not a plot with nothing in it — it is a
  // read that returned nothing, and the surface says so instead of drawing an
  // empty axis that looks like a flat signal.
  if (timestamps.length === 0 && rawFeatures.length === 0) return EMPTY;

  const fps = num(page.fps);
  const length = num(page.length);
  const last = timestamps.length > 0 ? timestamps[timestamps.length - 1] : 0;
  const durationSec = last > 0 ? last : fps > 0 ? length / fps : 0;

  const features = rawFeatures.map((f) => {
    const channels = arr(f.channels)
      .map((c) => obj(c))
      .filter((c): c is Entity => !!c)
      .map((c) => ({ name: str(c.name), values: values(c.values) }));
    const { min, max } = rangeOf(channels.map((c) => c.values));
    return {
      key: str(f.key),
      dtype: str(f.dtype),
      min,
      max,
      channels: channels.map((c) => ({ ...c, path: channelPath(c.values, min, max, width, height) })),
    };
  });

  return {
    hasSeries: true,
    episode: num(page.episode),
    length,
    fps,
    stride: Math.max(1, num(page.stride)),
    points: num(page.points) || timestamps.length,
    downsampled: page.downsampled === true,
    truncated: page.truncated === true,
    warnings: arr(page.warnings)
      .map((w) => str(w))
      .filter(Boolean),
    timestamps,
    durationSec,
    features,
  };
}

/// The min/max across every channel of a feature, ignoring gaps.
///
/// Returns a zero range for an all-gap or empty feature; `channelPath` renders
/// that as a mid-height line rather than dividing by it.
export function rangeOf(channels: number[][]): { min: number; max: number } {
  let min = Infinity;
  let max = -Infinity;
  for (const values of channels) {
    for (const v of values) {
      if (Number.isNaN(v)) continue;
      if (v < min) min = v;
      if (v > max) max = v;
    }
  }
  if (!Number.isFinite(min) || !Number.isFinite(max)) return { min: 0, max: 0 };
  return { min, max };
}

/// SVG path data for one channel.
///
/// Two rules the naive version gets wrong:
///
///   - **A gap breaks the line.** A NaN starts a new subpath rather than
///     interpolating across it, because a line drawn over a hole asserts
///     readings that were never taken. A lone sample between two gaps becomes
///     a zero-length segment so it is still visible.
///   - **A flat channel draws down the middle.** min === max is a motionless
///     joint, not a division to perform; it renders as a horizontal line at
///     half height instead of NaN coordinates that make the whole path vanish.
///
/// y is inverted because SVG grows downward and a plot does not: the maximum
/// belongs at the top.
export function channelPath(values: number[], min: number, max: number, width: number, height: number): string {
  const n = values.length;
  if (n === 0 || width <= 0 || height <= 0) return '';
  const span = max - min;
  const stepX = n > 1 ? width / (n - 1) : 0;
  const out: string[] = [];
  let open = false;
  for (let i = 0; i < n; i++) {
    const v = values[i];
    if (Number.isNaN(v)) {
      open = false;
      continue;
    }
    const x = n > 1 ? i * stepX : width / 2;
    const y = span > 0 ? height - ((v - min) / span) * height : height / 2;
    const px = round(x);
    const py = round(y);
    if (!open) {
      out.push(`M${px} ${py}`);
      // A sample with gaps on both sides has no neighbour to draw to, and an
      // `M` alone paints nothing. Give it a zero-length segment so an isolated
      // reading is still on screen.
      const aloneBefore = i === 0 || Number.isNaN(values[i - 1]);
      const aloneAfter = i === n - 1 || Number.isNaN(values[i + 1]);
      if (aloneBefore && aloneAfter) out.push(`L${px} ${py}`);
      open = true;
    } else {
      out.push(`L${px} ${py}`);
    }
  }
  return out.join(' ');
}

/// Round to a tenth of a pixel: sub-pixel precision no display can show costs
/// real bytes in a path string with thousands of points.
function round(v: number): number {
  return Math.round(v * 10) / 10;
}

/// Where a moment in the episode sits along a plot's x-axis.
export function timeToX(t: number, durationSec: number, width: number): number {
  if (durationSec <= 0 || width <= 0) return 0;
  const clamped = Math.min(Math.max(t, 0), durationSec);
  return (clamped / durationSec) * width;
}

/// The inverse — a click or drag on a plot becomes a moment in the episode.
export function xToTime(x: number, durationSec: number, width: number): number {
  if (durationSec <= 0 || width <= 0) return 0;
  const clamped = Math.min(Math.max(x, 0), width);
  return (clamped / width) * durationSec;
}

/// The sample nearest a moment, for the cursor's numeric readout.
///
/// Nearest rather than preceding: on a decimated series consecutive samples can
/// be seconds apart, and always rounding down makes the readout lag the cursor
/// by up to a whole stride.
///
/// Binary search — a scrub fires this on every pointer move, over a series that
/// may hold thousands of points.
export function nearestPointIndex(timestamps: number[], t: number): number {
  const n = timestamps.length;
  if (n === 0) return -1;
  let lo = 0;
  let hi = n - 1;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (timestamps[mid] < t) lo = mid + 1;
    else hi = mid;
  }
  if (lo > 0 && Math.abs(timestamps[lo - 1] - t) <= Math.abs(timestamps[lo] - t)) return lo - 1;
  return lo;
}

/// A channel's value at a sample index, or undefined at a gap — so the readout
/// can show "—" instead of "NaN".
export function valueAt(channel: ChannelView, index: number): number | undefined {
  const v = channel.values[index];
  return v === undefined || Number.isNaN(v) ? undefined : v;
}

/// Fixed-width-ish formatting for a plot legend: enough significant digits for
/// a joint angle in radians without a column of noise.
export function formatSample(v: number | undefined): string {
  if (v === undefined) return '—';
  if (v === 0) return '0';
  const abs = Math.abs(v);
  if (abs >= 1000 || abs < 0.001) return v.toExponential(2);
  return v.toFixed(abs >= 100 ? 1 : abs >= 1 ? 3 : 4);
}
