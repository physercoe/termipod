/// Lane A of `plans/desktop-compare-wall-and-decisions.md` (§3.2) — the pure
/// half of the comparison wall: everything derived FROM a run row, with no
/// React, no store and no hub client. That is deliberate. The wall's job is to
/// answer "which run is better, and what was different about it", and an
/// arithmetic slip there does not look like a bug — it looks like a result. So
/// the rules live where `node --test` can assert them (CI does NOT run the
/// desktop frontend suites — see CLAUDE.md).
///
/// A2's diff-only comparer and extremes table build on `flattenConfig` too:
/// one flattening, three consumers (filter, diff, group-by), which is why it
/// lives here rather than inside `CompareSurface`.

/// One leaf of a flattened `config_json`.
export interface ConfigEntry {
  /// Dotted path — `optimizer.lr`, `layers[0].width`.
  key: string;
  /// The leaf rendered as text. `null` becomes `"null"` and an empty container
  /// becomes `"{}"` / `"[]"`, so "the key is absent" and "the key is empty" stay
  /// distinguishable — A2's diff rows turn on exactly that difference.
  value: string;
}

function scalarText(v: unknown): string {
  if (typeof v === 'string') return v;
  if (v === null || v === undefined) return 'null';
  return String(v);
}

function walk(v: unknown, path: string, out: ConfigEntry[]): void {
  if (Array.isArray(v)) {
    if (v.length === 0) {
      if (path !== '') out.push({ key: path, value: '[]' });
      return;
    }
    v.forEach((el, i) => walk(el, `${path}[${i}]`, out));
    return;
  }
  if (v !== null && typeof v === 'object') {
    const obj = v as Record<string, unknown>;
    const keys = Object.keys(obj);
    if (keys.length === 0) {
      if (path !== '') out.push({ key: path, value: '{}' });
      return;
    }
    for (const k of keys) walk(obj[k], path === '' ? k : `${path}.${k}`, out);
    return;
  }
  // A scalar at the ROOT has no key to hang on and is dropped: a config that is
  // a bare number is not a config, and inventing a key for it would put a
  // phantom row in the comparer.
  if (path !== '') out.push({ key: path, value: scalarText(v) });
}

/// Flatten a run's config into sorted dotted-path leaves.
///
/// The hub serves `runs.config_json` as a **string** (`runOut.ConfigJSON` is a
/// `string`, not a `json.RawMessage` — handlers_runs.go), so the string case is
/// the normal one, not a defensive branch. A string that isn't JSON flattens to
/// nothing rather than throwing: the wall renders whatever runs exist, and one
/// run with a hand-edited config must not blank the surface.
export function flattenConfig(raw: unknown): ConfigEntry[] {
  let value = raw;
  if (typeof raw === 'string') {
    if (raw.trim() === '') return [];
    try {
      value = JSON.parse(raw) as unknown;
    } catch {
      return [];
    }
  }
  const out: ConfigEntry[] = [];
  walk(value, '', out);
  // Sorted so the comparer's row order is stable across runs whose configs were
  // written with different key order (JSON preserves insertion order; two runs
  // of the same script can still differ).
  out.sort((a, b) => (a.key < b.key ? -1 : a.key > b.key ? 1 : 0));
  return out;
}

/// The run facts the rail's filter searches. Entity-free on purpose — the
/// surface narrows a hub entity into this, and the rule stays testable.
export interface RunFacts {
  id: string;
  status: string;
  config: readonly ConfigEntry[];
}

/// Everything a filter term may match, lowercased once per run.
export function runHaystack(facts: RunFacts): string {
  const parts = [facts.id, facts.status];
  for (const e of facts.config) parts.push(`${e.key}=${e.value}`);
  return parts.join('\n').toLowerCase();
}

/// Filter-as-you-type over id, status and config keys/values (plan §3.2).
///
/// Whitespace splits the query into terms and EVERY term must match — typing
/// narrows, which is the behaviour a filter box teaches. Terms are matched as
/// substrings, so `lr=3e` finds the config leaf and `fail` finds the status.
export function runMatchesFilter(facts: RunFacts, filter: string): boolean {
  const terms = filter.toLowerCase().split(/\s+/).filter((t) => t !== '');
  if (terms.length === 0) return true;
  const hay = runHaystack(facts);
  return terms.every((t) => hay.includes(t));
}

/// `value - baseline`, or null when either side has no number.
///
/// Null is not zero: a metric a run never logged must render as "—", never as
/// "no change from the baseline". Those read identically on a wall and mean
/// opposite things.
export function deltaOf(value: number | undefined, baseline: number | undefined): number | null {
  if (value === undefined || baseline === undefined) return null;
  if (!Number.isFinite(value) || !Number.isFinite(baseline)) return null;
  return value - baseline;
}

export type DeltaSign = 'up' | 'down' | 'flat';

/// Sign only — the wall colours by DIRECTION, never by "better".
///
/// Whether up is good depends on the metric (loss down, reward up) and nothing
/// on this surface knows which is which, so claiming goodness with green/red
/// would be a guess rendered as a fact.
export function deltaSign(delta: number): DeltaSign {
  if (delta > 0) return 'up';
  if (delta < 0) return 'down';
  return 'flat';
}

/// A signed, compact delta. Mirrors `ChartView`'s `fmt` thresholds so a table
/// cell and its curve's axis never disagree about magnitude.
export function formatDelta(delta: number): string {
  if (!Number.isFinite(delta)) return '';
  if (delta === 0) return '0';
  const a = Math.abs(delta);
  const body = a >= 1000 || a < 0.01 ? a.toExponential(1) : a < 1 ? a.toFixed(3) : a.toFixed(2);
  return `${delta > 0 ? '+' : '-'}${body}`;
}

// ── A2: curve math ──────────────────────────────────────────────────────────

export interface CurvePoint {
  x: number;
  y: number;
}

export interface MetricExtremes {
  last: number | undefined;
  min: number | undefined;
  max: number | undefined;
}

/// The min/max/last of one curve (plan §3.2, ClearML's extremes table).
///
/// Computed from the points the hub already shipped — no request. Non-finite
/// samples are skipped rather than poisoning the pair: one NaN in a long run
/// would otherwise make every extreme NaN, which reads as "no data" for a run
/// that has plenty.
export function extremesOf(points: readonly CurvePoint[]): MetricExtremes {
  let min: number | undefined;
  let max: number | undefined;
  let last: number | undefined;
  for (const p of points) {
    if (!Number.isFinite(p.y)) continue;
    if (min === undefined || p.y < min) min = p.y;
    if (max === undefined || p.y > max) max = p.y;
    last = p.y;
  }
  return { last, min, max };
}

/// TensorBoard's debiased exponential moving average — the smoothing every
/// researcher's hand already knows (plan §3.2: "the TensorBoard/W&B muscle
/// memory").
///
/// The debias term is the part people re-derive wrong: a plain EMA seeded at 0
/// drags the first points toward zero, so a loss curve appears to start far
/// below where it did. Dividing by `1 - weight^n` removes that bias, which is
/// what TensorBoard does. `weight` is the slider's 0..1 value; 0 returns the
/// input untouched (identity, so the caller can always smooth).
export function emaSmooth(points: readonly CurvePoint[], weight: number): CurvePoint[] {
  if (!Number.isFinite(weight) || weight <= 0) return [...points];
  const w = Math.min(weight, 0.999);
  const out: CurvePoint[] = [];
  let acc = 0;
  let n = 0;
  for (const p of points) {
    if (!Number.isFinite(p.y)) {
      // Keep the sample's x so the curve does not silently shorten, but do not
      // let a non-finite value into the accumulator.
      out.push(p);
      continue;
    }
    acc = acc * w + (1 - w) * p.y;
    n += 1;
    const debias = 1 - Math.pow(w, n);
    out.push({ x: p.x, y: debias === 0 ? p.y : acc / debias });
  }
  return out;
}

/// Re-base each curve on its own first step (the wall's `relative` x-axis).
///
/// Deliberately NOT wall-clock: metric points carry `step` only, and adding
/// per-point timestamps is a tbreader/digest change to make on purpose (plan
/// §3.2). What this DOES answer is the fork case — a run resumed at step 5000
/// and a run started from scratch are comparable by progress-since-start, and
/// on an absolute axis they sit in different halves of the chart.
export function toRelativeX(points: readonly CurvePoint[]): CurvePoint[] {
  const first = points[0];
  if (first === undefined) return [];
  const base = first.x;
  return points.map((p) => ({ x: p.x - base, y: p.y }));
}

// ── A2: the diff-only config comparer ───────────────────────────────────────

/// One run's flattened config, from whichever sources it has.
export interface RunConfig {
  id: string;
  entries: readonly ConfigEntry[];
}

/// One row of the comparer: a key, one cell per run in the given order, and
/// whether every run agrees.
export interface ConfigDiffRow {
  key: string;
  /// `undefined` = the key is ABSENT for that run, which is not the same as
  /// present-and-empty (`{}` / `[]` / `null` all render as themselves).
  values: (string | undefined)[];
  identical: boolean;
}

/// Union two flattened configs, the LOGGED one winning on a collision.
///
/// A run has two config sources: `runs.config_json`, registered when the run
/// was created, and the `/config` digest the host-runner pushes from what the
/// training script actually loaded. When they disagree, the logged one is what
/// ran — so it wins — but the disagreement is itself a finding, so the keys
/// that differ are returned rather than swallowed. (Reconciling them properly
/// is A4's provenance triad; this is the honest interim: prefer the truth,
/// name the conflict.)
export function mergeConfigSources(
  registered: readonly ConfigEntry[],
  logged: readonly ConfigEntry[],
): { entries: ConfigEntry[]; conflicts: string[] } {
  const byKey = new Map<string, string>();
  for (const e of registered) byKey.set(e.key, e.value);
  const conflicts: string[] = [];
  for (const e of logged) {
    const prev = byKey.get(e.key);
    if (prev !== undefined && prev !== e.value) conflicts.push(e.key);
    byKey.set(e.key, e.value);
  }
  const entries = [...byKey.entries()].map(([key, value]) => ({ key, value }));
  entries.sort((a, b) => (a.key < b.key ? -1 : a.key > b.key ? 1 : 0));
  conflicts.sort();
  return { entries, conflicts };
}

/// The comparer's row model: the sorted union of every run's config keys, one
/// cell per run, with the "every run agrees" bit precomputed.
///
/// `identical` is what the "show identical" toggle hides, and it counts an
/// absent key as a value: two runs where only one sets `resume_from` DIFFER,
/// even though only one of them has anything to show. Hiding that row would
/// hide the actual difference between the runs.
///
/// This is the shape `run_config_diff` returns to agents (plan §3.5) — one row
/// model, two consumers.
export function configDiffRows(runs: readonly RunConfig[]): ConfigDiffRow[] {
  const keys = new Set<string>();
  const maps = runs.map((r) => {
    const m = new Map<string, string>();
    for (const e of r.entries) {
      m.set(e.key, e.value);
      keys.add(e.key);
    }
    return m;
  });
  const rows: ConfigDiffRow[] = [];
  for (const key of [...keys].sort()) {
    const values = maps.map((m) => m.get(key));
    const identical = values.every((v) => v === values[0]);
    rows.push({ key, values, identical });
  }
  return rows;
}
