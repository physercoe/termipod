import { create } from 'zustand';

/// The comparison wall's ONE state (plan `desktop-compare-wall-and-decisions.md`
/// §3.1). Every panel — runs rail, summary table, overlay charts, and A2's
/// comparer — subscribes here. That is an architecture constraint, not a
/// convenience: §5.2's rule is "one visible-runs state drives all panels", and
/// the failure it prevents is the wall showing three runs while the comparer
/// diffs two others, each panel individually correct.
///
/// The view is remembered **per project**, because the wall is not a widget you
/// configure once — it is a reading position. Coming back to a project should
/// return you to the runs you were comparing, the way the split-pane store
/// returns you to your panes (workbench.ts, whose persistence idiom this
/// follows: raw localStorage in a try/catch, so `node --test` can import this
/// module without a DOM).

export type CompareXAxis = 'step' | 'relative';

/// One project's remembered wall.
export interface WallView {
  /// Run ids, in pick order — the order that assigns swatch colours.
  selected: string[];
  /// The run every delta is measured against, or null for "no baseline".
  /// Invariant: when non-null it is always a member of `selected` (see
  /// `healView`) — a baseline you cannot see on the wall is a silent lie in
  /// every delta column.
  baseline: string | null;
  /// Free text over id / status / config (compareRuns.runMatchesFilter).
  filter: string;
  /// EMA weight in [0, MAX_SMOOTHING]. 0 = raw. A2 renders it.
  smoothing: number;
  /// A2's x-switch. Wall-clock is deliberately absent: metric points carry
  /// `step` only, and adding per-point timestamps is a tbreader change (§3.2).
  xAxis: CompareXAxis;
  /// A3's grouping config key, or null for "one curve per run".
  groupBy: string | null;
}

/// TensorBoard's slider tops out below 1.0 — at 1 the EMA never moves off its
/// seed and the curve reads as a flat line, which looks like broken data.
export const MAX_SMOOTHING = 0.95;

export const EMPTY_VIEW: WallView = Object.freeze({
  selected: [],
  baseline: null,
  filter: '',
  smoothing: 0,
  xAxis: 'step' as CompareXAxis,
  groupBy: null,
});

// ── Pure reducers (the whole algebra; the store below only persists) ─────────
// Every reducer returns its INPUT object verbatim when nothing changed, so
// identity is the "no-op" signal — the store leans on it to skip writes.

function sameSelection(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((x, i) => x === b[i]);
}

function sameView(a: WallView, b: WallView): boolean {
  return (
    sameSelection(a.selected, b.selected) &&
    a.baseline === b.baseline &&
    a.filter === b.filter &&
    a.smoothing === b.smoothing &&
    a.xAxis === b.xAxis &&
    a.groupBy === b.groupBy
  );
}

/// Restore the cross-field invariants after any edit or a persisted-blob read.
/// One place, so a new action cannot forget one.
export function healView(v: WallView): WallView {
  const selected = v.selected.filter((id, i) => id !== '' && v.selected.indexOf(id) === i);
  const next: WallView = {
    ...v,
    selected,
    baseline: v.baseline !== null && selected.includes(v.baseline) ? v.baseline : null,
    smoothing: Number.isFinite(v.smoothing) ? Math.min(MAX_SMOOTHING, Math.max(0, v.smoothing)) : 0,
    xAxis: v.xAxis === 'relative' ? 'relative' : 'step',
    groupBy: v.groupBy === null || v.groupBy === '' ? null : v.groupBy,
  };
  return sameView(next, v) ? v : next;
}

/// Apply a patch and re-heal, returning the INPUT when the result is
/// equivalent. Every setter goes through here, so normalisation lives in
/// exactly one place and no action can allocate a fresh object for a no-op —
/// the store reads identity as "nothing changed" and skips its write.
function edit(v: WallView, patch: Partial<WallView>): WallView {
  const next = healView({ ...v, ...patch });
  return sameView(next, v) ? v : next;
}

/// Add or remove one run. Un-selecting the baseline drops the baseline too
/// (healView), which is the point of routing every edit through it.
export function applyToggleRun(v: WallView, id: string): WallView {
  if (id === '') return v;
  const selected = v.selected.includes(id) ? v.selected.filter((x) => x !== id) : [...v.selected, id];
  return edit(v, { selected });
}

export function applySetSelected(v: WallView, ids: readonly string[]): WallView {
  return edit(v, { selected: [...ids] });
}

/// Pin (or clear) the baseline. Pinning a run that is not on the wall SELECTS
/// it — the alternative is a star click that appears to do nothing, because
/// healView would immediately drop the pin.
export function applySetBaseline(v: WallView, id: string | null): WallView {
  if (id === null) return edit(v, { baseline: null });
  if (id === '') return v;
  const selected = v.selected.includes(id) ? v.selected : [...v.selected, id];
  return edit(v, { selected, baseline: id });
}

/// The star's own gesture: click the pinned run to unpin it.
export function applyToggleBaseline(v: WallView, id: string): WallView {
  return applySetBaseline(v, v.baseline === id ? null : id);
}

export function applySetFilter(v: WallView, filter: string): WallView {
  return edit(v, { filter });
}

export function applySetSmoothing(v: WallView, smoothing: number): WallView {
  return edit(v, { smoothing });
}

export function applySetXAxis(v: WallView, xAxis: CompareXAxis): WallView {
  return edit(v, { xAxis });
}

export function applySetGroupBy(v: WallView, groupBy: string | null): WallView {
  return edit(v, { groupBy });
}

// ── Persistence ─────────────────────────────────────────────────────────────

const LS_KEY = 'termipod.compare.wall.v1';

/// How many projects' views survive. A wall view is tiny, but the map is
/// append-only otherwise: every project ever opened would keep a row forever.
/// The current project is always kept (it is written last).
export const MAX_REMEMBERED_PROJECTS = 20;

export interface WallPersist {
  projectId: string;
  byProject: Record<string, WallView>;
}

function viewFrom(raw: unknown): WallView | null {
  if (typeof raw !== 'object' || raw === null) return null;
  const o = raw as Record<string, unknown>;
  const selected = Array.isArray(o.selected) ? o.selected.filter((x): x is string => typeof x === 'string') : [];
  return healView({
    selected,
    baseline: typeof o.baseline === 'string' ? o.baseline : null,
    filter: typeof o.filter === 'string' ? o.filter : '',
    smoothing: typeof o.smoothing === 'number' ? o.smoothing : 0,
    xAxis: o.xAxis === 'relative' ? 'relative' : 'step',
    groupBy: typeof o.groupBy === 'string' && o.groupBy !== '' ? o.groupBy : null,
  });
}

/// Parse the persisted blob. Per-field fallback, per-project fallback: one
/// corrupt project's view must not cost the user every other project's, and a
/// blob from a build that added a field must not void the ones we understand.
export function parseWall(raw: string | null): WallPersist {
  const none: WallPersist = { projectId: '', byProject: {} };
  if (raw === null) return none;
  let obj: unknown;
  try {
    obj = JSON.parse(raw);
  } catch {
    return none;
  }
  if (typeof obj !== 'object' || obj === null) return none;
  const o = obj as Record<string, unknown>;
  const byProject: Record<string, WallView> = {};
  if (typeof o.byProject === 'object' && o.byProject !== null) {
    for (const [pid, v] of Object.entries(o.byProject as Record<string, unknown>)) {
      const view = viewFrom(v);
      if (pid !== '' && view !== null) byProject[pid] = view;
    }
  }
  return { projectId: typeof o.projectId === 'string' ? o.projectId : '', byProject };
}

/// Keep at most `max` projects, dropping the oldest insertion first and never
/// the current one.
export function trimProjects(
  byProject: Record<string, WallView>,
  current: string,
  max = MAX_REMEMBERED_PROJECTS,
): Record<string, WallView> {
  const keys = Object.keys(byProject);
  if (keys.length <= max) return byProject;
  const drop = new Set(keys.filter((k) => k !== current).slice(0, keys.length - max));
  const out: Record<string, WallView> = {};
  for (const k of keys) if (!drop.has(k)) out[k] = byProject[k];
  return out;
}

function readRaw(): string | null {
  try {
    return localStorage.getItem(LS_KEY);
  } catch {
    /* no localStorage (node tests) */
    return null;
  }
}

function writeRaw(blob: WallPersist): void {
  try {
    localStorage.setItem(LS_KEY, JSON.stringify(blob));
  } catch {
    /* quota / unavailable — the wall still works, it just forgets */
  }
}

// ── The store ───────────────────────────────────────────────────────────────

interface CompareWallState {
  /// The project the wall is reading. '' until the surface resolves one.
  projectId: string;
  /// That project's view. Swapping projects swaps this wholesale.
  view: WallView;
  setProject: (id: string) => void;
  toggleRun: (id: string) => void;
  setSelected: (ids: string[]) => void;
  setBaseline: (id: string | null) => void;
  toggleBaseline: (id: string) => void;
  setFilter: (filter: string) => void;
  setSmoothing: (smoothing: number) => void;
  setXAxis: (xAxis: CompareXAxis) => void;
  setGroupBy: (groupBy: string | null) => void;
}

export const useCompareWall = create<CompareWallState>((set, get) => {
  const initial = parseWall(readRaw());
  let byProject = { ...initial.byProject };

  // Writes are synchronous, unlike workbench.ts's debounced split ratio. That
  // one fires on every pointermove of a drag (dozens/s); the fastest thing
  // here is typing in the filter box, and the blob is bounded at 20 tiny
  // views. Deferring the write would buy sub-millisecond savings and cost a
  // lost-edit case to defend (a project switch racing a pending write).
  const persist = (): void => {
    const s = get();
    // A view with no project has nowhere to be filed — the surface always
    // resolves a project before anything is selectable, so this is the
    // first-paint window only.
    if (s.projectId === '') return;
    byProject = trimProjects({ ...byProject, [s.projectId]: s.view }, s.projectId);
    writeRaw({ projectId: s.projectId, byProject });
  };

  const commit = (next: WallView): void => {
    if (next === get().view) return; // reducers return their input on a no-op
    set({ view: next });
    persist();
  };

  return {
    projectId: initial.projectId,
    view: initial.byProject[initial.projectId] ?? EMPTY_VIEW,
    setProject: (id) => {
      if (id === get().projectId) return;
      set({ projectId: id, view: byProject[id] ?? EMPTY_VIEW });
      persist();
    },
    toggleRun: (id) => commit(applyToggleRun(get().view, id)),
    setSelected: (ids) => commit(applySetSelected(get().view, ids)),
    setBaseline: (id) => commit(applySetBaseline(get().view, id)),
    toggleBaseline: (id) => commit(applyToggleBaseline(get().view, id)),
    setFilter: (filter) => commit(applySetFilter(get().view, filter)),
    setSmoothing: (smoothing) => commit(applySetSmoothing(get().view, smoothing)),
    setXAxis: (xAxis) => commit(applySetXAxis(get().view, xAxis)),
    setGroupBy: (groupBy) => commit(applySetGroupBy(get().view, groupBy)),
  };
});
