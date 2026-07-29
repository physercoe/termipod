import type { Entity } from '../hub/types';

/// Reader for a dataset's digest (J8 Replay W1 — the fold produced by
/// hub/internal/hostrunner/datasetmeta and stored on the `datasets` row).
///
/// Pure and i18n-free, the same posture as `digestIssues.ts`: wire keys stay
/// keys and the component resolves display strings. That keeps the shape rules
/// in ONE place and makes them testable with `node --test`, which matters more
/// than usual here — the surface this feeds is a visual one I cannot look at,
/// so the arithmetic is extracted to where it can be asserted instead
/// (`feedback_pure_layout_module_is_testable_eyes`).
///
/// Everything is defensive about absence. A dataset that has never been
/// refreshed has NO digest at all, and that is a different state from a dataset
/// whose digest says zero — the UI must be able to tell "not read yet" from
/// "read, and empty".

export interface VideoStreamInfo {
  key: string;
  name: string;
  width?: number;
  height?: number;
  codec?: string;
  fps?: number;
  isDepth?: boolean;
}

export interface FeatureInfo {
  key: string;
  dtype: string;
  /// Product of the shape dims — the number a UI means by "7-DoF". Undefined
  /// when the dataset declared no shape.
  dim?: number;
  shape?: number[];
  names?: string[];
}

export interface LengthBar {
  from: number;
  to: number;
  count: number;
  /// count scaled to 0..1 against the tallest bar, for a sparkline.
  height: number;
}

export interface DatasetSummary {
  /// False when the dataset has never been read by a host. Every other field
  /// is meaningless in that case, and the UI shows a "not read yet" state
  /// rather than a row of zeroes.
  hasDigest: boolean;
  format: string;
  codebaseVersion: string;
  robotType: string;
  fps: number;
  episodes: number;
  frames: number;
  tasksTotal: number;
  durationSec: number;
  videoStreams: VideoStreamInfo[];
  features: FeatureInfo[];
  tasks: string[];
  tasksTruncated: boolean;
  histogram: LengthBar[];
  /// Set when the host's fold stopped early against a cap. A capped aggregate
  /// must not be presented as the whole dataset's.
  statsPartial: boolean;
  episodesTruncated: boolean;
  /// Non-fatal oddities the host reported (a missing optional file, a feature
  /// whose stats did not parse).
  warnings: string[];
  /// When the digest was folded, ISO-8601. Empty when never.
  digestTS: string;
}

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

/// Scale bucket counts to 0..1 against the tallest bar.
///
/// Against the MAX, not the total: the histogram is a shape, and dividing by
/// the total would flatten every bar to invisibility on a dataset with many
/// buckets. A zero-count bucket keeps height 0 so a gap reads as a gap.
export function scaleHistogram(buckets: unknown): LengthBar[] {
  const raw = arr(buckets)
    .map((b) => obj(b))
    .filter((b): b is Entity => !!b)
    .map((b) => ({ from: num(b.from), to: num(b.to), count: num(b.count) }));
  if (raw.length === 0) return [];
  const max = raw.reduce((m, b) => (b.count > m ? b.count : m), 0);
  return raw.map((b) => ({ ...b, height: max > 0 ? b.count / max : 0 }));
}

/// Human duration. Seconds below a minute keep one decimal because episodes are
/// commonly 6–40s and "6s" vs "6.4s" is a real difference at that scale;
/// anything longer rounds, because nobody reads a dataset's total length to the
/// tenth of a second.
export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return '0s';
  if (seconds < 60) {
    const oneDp = Math.round(seconds * 10) / 10;
    return Number.isInteger(oneDp) ? `${oneDp}s` : `${oneDp.toFixed(1)}s`;
  }
  const total = Math.round(seconds);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  if (h > 0) return m > 0 ? `${h}h ${m}m` : `${h}h`;
  return s > 0 ? `${m}m ${s}s` : `${m}m`;
}

/// Thousands separators without pulling in a formatter or depending on locale
/// — the surrounding numbers are counts, not currency.
export function formatCount(n: number): string {
  if (!Number.isFinite(n)) return '0';
  return Math.round(n)
    .toString()
    .replace(/\B(?=(\d{3})+(?!\d))/g, ',');
}

/// "1920x1080" when both dims are known, else whichever is.
export function formatResolution(s: VideoStreamInfo): string {
  if (s.width && s.height) return `${s.width}x${s.height}`;
  if (s.width) return `${s.width}`;
  if (s.height) return `${s.height}`;
  return '';
}

function readVideoStreams(digest: Entity): VideoStreamInfo[] {
  return arr(digest.video_streams)
    .map((v) => obj(v))
    .filter((v): v is Entity => !!v)
    .map((v) => ({
      key: str(v.key),
      // The trailing segment is what labels a pane; fall back to the full key
      // so a stream whose name the host omitted is still identifiable.
      name: str(v.name) || str(v.key),
      width: num(v.width) || undefined,
      height: num(v.height) || undefined,
      codec: str(v.codec) || undefined,
      fps: num(v.fps) || undefined,
      isDepth: v.is_depth === true,
    }));
}

function readFeatures(digest: Entity): FeatureInfo[] {
  return arr(digest.features)
    .map((f) => obj(f))
    .filter((f): f is Entity => !!f)
    .map((f) => {
      const shape = arr(f.shape).map((n) => num(n));
      const names = arr(f.names)
        .map((n) => str(n))
        .filter(Boolean);
      return {
        key: str(f.key),
        dtype: str(f.dtype),
        // Dimension comes from the SHAPE, never from names.length: real LeRobot
        // files carry `"names": null` on every scalar feature, so a
        // names-derived dimension reads as zero for half the table.
        dim: shape.length > 0 ? shape.reduce((a, b) => a * b, 1) : undefined,
        shape: shape.length > 0 ? shape : undefined,
        names: names.length > 0 ? names : undefined,
      };
    });
}

/// Turn a dataset row into the shape the header card renders.
export function readDatasetSummary(dataset: Entity | undefined | null): DatasetSummary {
  const empty: DatasetSummary = {
    hasDigest: false,
    format: '',
    codebaseVersion: '',
    robotType: '',
    fps: 0,
    episodes: 0,
    frames: 0,
    tasksTotal: 0,
    durationSec: 0,
    videoStreams: [],
    features: [],
    tasks: [],
    tasksTruncated: false,
    histogram: [],
    statsPartial: false,
    episodesTruncated: false,
    warnings: [],
    digestTS: '',
  };
  if (!dataset) return empty;
  const digest = obj(dataset.digest);
  const digestTS = str(dataset.digest_ts);
  // A digest key can be present but empty on a row that was never refreshed;
  // the timestamp is what actually proves a host read happened.
  if (!digest || !digestTS) return { ...empty, digestTS };

  return {
    hasDigest: true,
    format: str(digest.format) || str(dataset.format),
    codebaseVersion: str(digest.codebase_version),
    robotType: str(digest.robot_type),
    fps: num(digest.fps),
    episodes: num(digest.total_episodes),
    frames: num(digest.total_frames),
    tasksTotal: num(digest.total_tasks),
    durationSec: num(digest.duration_sec),
    videoStreams: readVideoStreams(digest),
    features: readFeatures(digest),
    tasks: arr(digest.tasks)
      .map((t) => str(t))
      .filter(Boolean),
    tasksTruncated: digest.tasks_truncated === true,
    histogram: scaleHistogram(digest.length_histogram),
    statsPartial: digest.stats_partial === true,
    episodesTruncated: digest.episodes_truncated === true,
    warnings: arr(digest.warnings)
      .map((wv) => str(wv))
      .filter(Boolean),
    digestTS,
  };
}

export interface EpisodeRow {
  index: number;
  length: number;
  durationSec: number;
  tasks: string[];
  /// Present only for layouts where many episodes share a file (LeRobot v3.0).
  /// The UI shows the row range as provenance for what the player will cut.
  fromIndex?: number;
  toIndex?: number;
}

export interface EpisodePageView {
  rows: EpisodeRow[];
  offset: number;
  limit: number;
  total: number;
  /// The host clamped the requested page size.
  truncated: boolean;
}

export function readEpisodePage(page: Entity | undefined | null): EpisodePageView {
  if (!page) return { rows: [], offset: 0, limit: 0, total: 0, truncated: false };
  const rows = arr(page.episodes)
    .map((e) => obj(e))
    .filter((e): e is Entity => !!e)
    .map((e) => {
      const row: EpisodeRow = {
        index: num(e.index),
        length: num(e.length),
        durationSec: num(e.duration_sec),
        tasks: arr(e.tasks)
          .map((t) => str(t))
          .filter(Boolean),
      };
      // 0 is a legitimate offset, so presence is tested on the key, not on
      // truthiness — `num(e.from_index) || undefined` would erase episode 0's
      // range and make the first row look like a per-episode-file layout.
      if (typeof e.from_index === 'number') row.fromIndex = e.from_index;
      if (typeof e.to_index === 'number') row.toIndex = e.to_index;
      return row;
    });
  return {
    rows,
    offset: num(page.offset),
    limit: num(page.limit),
    total: num(page.total),
    truncated: page.truncated === true,
  };
}

/// The 1-based inclusive range this page covers, for a "showing 1–200 of 50,000"
/// footer. Returns null when there is nothing to describe, so the caller renders
/// nothing rather than "0–0 of 0".
export function pageRangeLabel(view: EpisodePageView): { from: number; to: number; total: number } | null {
  if (view.rows.length === 0) return null;
  return { from: view.offset + 1, to: view.offset + view.rows.length, total: view.total };
}

/// The Inspect handoff's gate (W1d). A tree row is a dataset entry point iff it
/// is the `meta/info.json` that marks a LeRobot root — the same file the host's
/// format sniff opens. Returns the **dataset root**, which is the directory
/// CONTAINING `meta/`, not the meta directory: `/data/ds/meta/info.json` →
/// `/data/ds`. Returns null for anything else.
///
/// Separator-agnostic, because the one rule runs over three path shapes: a
/// Windows local path (`C:\data\ds\meta\info.json`), a POSIX local one, and an
/// SFTP path joined with '/'. The root keeps the input's own separators so it
/// round-trips to whichever host is asked to read it.
///
/// Deliberately NOT a validity check. A relative path survives this and is
/// handed to the register form to be corrected, because the host refuses a
/// relative root with a message that names the problem (`cleanDatasetRoot`:
/// "root_path must be absolute"). Silently declining to offer the action would
/// be indistinguishable, on screen, from the feature being broken.
export function datasetRootFromMetaInfo(path: string): string | null {
  // Greedy prefix: the LAST `meta/info.json` wins, so a dataset that happens to
  // live under a directory called `meta` still resolves to its own root.
  const m = /^(.*)[\\/]meta[\\/]info\.json$/.exec(path);
  if (m === null) return null;
  const root = m[1].replace(/[\\/]+$/, '');
  return root === '' ? null : root;
}

export interface DatasetHandoff {
  rootPath: string;
}

/// What the Replay surface should do with an incoming handoff, given the
/// datasets already registered in the project on screen.
///
/// `select` when this location is unambiguously already a dataset here — the
/// "take me there" case. `register` otherwise, which the surface turns into a
/// **prefilled form**, not a silent write: registering needs a project and a
/// host, the sender has neither, and the identity key is
/// `(project, host, root_path)` — so a guess would create a second row rather
/// than find the existing one, which is the opposite of idempotent.
export type HandoffResolution =
  | { action: 'select'; datasetId: string }
  | { action: 'register'; rootPath: string };

/// Trailing separators are not part of a path's identity. The hub stores the
/// root string as sent (only the host cleans it, at read time), so `/data/ds`
/// and `/data/ds/` are the same dataset registered two different ways, and a
/// raw string compare would offer to register one that is already there.
function samePath(a: string, b: string): boolean {
  return a.replace(/[\\/]+$/, '') === b.replace(/[\\/]+$/, '');
}

/// Matching is on the path ALONE, and only a unique hit selects.
///
/// The handoff carries no host on purpose: what Inspect knows for a remote root
/// is an SSH *connection* id (`InspectRoot.hostId` is named for the machine, not
/// for the hub entity — `state/connections.ts` has no hub-host field at all),
/// and passing that as `datasets.host_id` would write a dangling foreign key.
/// So the same path registered against two hosts is genuinely ambiguous here,
/// and ambiguity resolves to the form — where the host select is the answer —
/// rather than to a coin flip between two machines' datasets.
export function resolveHandoff(h: DatasetHandoff, datasets: Entity[]): HandoffResolution {
  const hits = datasets.filter((d) => {
    const rootPath = typeof d.root_path === 'string' ? d.root_path : '';
    return samePath(rootPath, h.rootPath) && typeof d.id === 'string' && d.id !== '';
  });
  if (hits.length === 1) return { action: 'select', datasetId: hits[0].id as string };
  return { action: 'register', rootPath: h.rootPath };
}
