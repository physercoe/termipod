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
