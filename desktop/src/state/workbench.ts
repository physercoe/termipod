import { create } from 'zustand';

/// The desktop workbench's top-level navigation model. The desktop app is no
/// longer a single control-fleet screen (`desktop-research-surface.md` §6): it is
/// a left **activity-bar** of distinct jobs, each a full-height surface in the
/// centre. `fleet` (J7) is the original three-region mission-control; J1–J6 are
/// the research-workbench jobs derived in that discussion and mapped to a
/// build/embed posture by `research-tooling-landscape.md`.
///
/// The registry below is the single source of truth for the rail order, icons,
/// and i18n keys — the ActivityBar renders it and the shell switches on the id,
/// so adding a job is one entry here plus a surface component (never a scattered
/// change).
export type JobId =
  | 'fleet'
  | 'projects'
  | 'read'
  | 'author'
  | 'debug'
  | 'compare'
  | 'replay'
  | 'record'
  | 'terminal'
  | 'settings';

export interface JobDef {
  id: JobId;
  /** J-number from `desktop-research-surface.md` §3 (empty for the fleet home). */
  tag: string;
  /** i18n key for the short rail label. */
  labelKey: string;
  /** i18n key for the tooltip / surface subtitle. */
  hintKey: string;
  /**
   * May this job be pinned as the split's secondary pane
   * (`plans/desktop-shell-split-pane.md` §3.1)? Omitted means yes — a new *work*
   * surface should pair. Only the two chrome jobs opt out: `terminal` is the
   * always-mounted bottom panel and `settings` is a full-surface switch.
   */
  splitEligible?: boolean;
}

export const JOBS: JobDef[] = [
  { id: 'fleet', tag: '', labelKey: 'job.fleet', hintKey: 'job.fleet.hint' },
  // Projects (the units of directed work) are their own tab — pulled out of the
  // fleet tree so the fleet stays an ops roster (hosts · agents · attention),
  // mirroring the mobile Projects tab being separate from Me/Hosts.
  { id: 'projects', tag: '', labelKey: 'job.projects', hintKey: 'job.projects.hint' },
  { id: 'read', tag: 'J1', labelKey: 'job.read', hintKey: 'job.read.hint' },
  // Author (J2) now also hosts the spatial **canvas** and **table/database** as
  // document kinds — the standalone J4 Canvas surface was folded in.
  { id: 'author', tag: 'J2', labelKey: 'job.author', hintKey: 'job.author.hint' },
  { id: 'debug', tag: 'J3', labelKey: 'job.debug', hintKey: 'job.debug.hint' },
  { id: 'compare', tag: 'J5', labelKey: 'job.compare', hintKey: 'job.compare.hint' },
  // Replay (J8) sits between Compare and Record so the rail reads as the
  // lifecycle: Read/Author -> Inspect -> Compare (runs) -> Replay (episodes)
  // -> Record (conclusions). The two analysis jobs are adjacent on purpose.
  { id: 'replay', tag: 'J8', labelKey: 'job.replay', hintKey: 'job.replay.hint' },
  { id: 'record', tag: 'J6', labelKey: 'job.record', hintKey: 'job.record.hint' },
  { id: 'terminal', tag: '', labelKey: 'job.terminal', hintKey: 'job.terminal.hint', splitEligible: false },
];

/// Settings is a job too, but pinned to the *bottom* of the activity bar (the VS
/// Code gear idiom) rather than listed with the working jobs — so it lives out of
/// `JOBS`. The ActivityBar renders it separately; the shell still switches on it.
export const SETTINGS_JOB: JobDef = {
  id: 'settings',
  tag: '',
  labelKey: 'job.settings',
  hintKey: 'job.settings.hint',
  splitEligible: false,
};

// Every id the shell can restore to on launch — the rail jobs plus the pinned
// settings tab.
const KNOWN_JOBS: JobId[] = [...JOBS.map((j) => j.id), SETTINGS_JOB.id];

const ALL_JOBS: JobDef[] = [...JOBS, SETTINGS_JOB];

export function isKnownJob(v: unknown): v is JobId {
  return typeof v === 'string' && KNOWN_JOBS.includes(v as JobId);
}

/// May `id` be pinned as the secondary pane? Registry-driven (the `splitEligible`
/// field above) so the rule lives with the job definition instead of as a
/// scattered `id === 'terminal' || id === 'settings'` check.
export function isSplitEligible(id: JobId): boolean {
  const def = ALL_JOBS.find((j) => j.id === id);
  return def !== undefined && def.splitEligible !== false;
}

// ── Split panes ──────────────────────────────────────────────────────────────
// ADR-050 argues the desktop exists for *simultaneity*, but the shell was a
// modal one-job-at-a-time switch. `plans/desktop-shell-split-pane.md` (S1) adds
// exactly one optional pinned secondary surface — not a docking framework. Every
// rule lives here as a pure reducer over `PaneState` so it is testable without
// React (`workbench.test.ts`); the store actions only persist the result.

export type Pane = 'primary' | 'secondary';

export interface PaneState {
  /** The primary (left) pane. Unchanged meaning: the job the shell shows. */
  job: JobId;
  /** The pinned secondary (right) pane; `null` = no split. */
  secondary: JobId | null;
  /**
   * Which pane is *active* — the one that drives `setJob`, the audit command and
   * (S3) the ADR-062 focus snapshot. A screen POSITION, not a piece of content:
   * see `applySwapPanes`.
   */
  activePane: Pane;
}

/// The job of the active pane — what "the current surface" means once a split
/// can exist. Callers that mean the left pane specifically still read `job`.
export function activeJob(s: PaneState): JobId {
  return s.activePane === 'secondary' && s.secondary !== null ? s.secondary : s.job;
}

/// The pane facts the ADR-062 focus snapshot needs (S3 / `ui_policy.ts`). The
/// top level of the snapshot is POSITIONAL — the primary pane — and `activePane`
/// names where the user actually is; that is what lets a snapshot describe both
/// panes instead of only the focused one.
///
/// A *parked* pin (§4.1: kept while the user visits a chrome job) is deliberately
/// not reported: `isSplitVisible` decides, so an agent is never told about a pane
/// the user cannot see.
export function panesForFocus(s: PaneState): { job: JobId; secondaryJob: JobId | null; activePane: Pane } {
  return isSplitVisible(s) && s.secondary !== null
    ? { job: s.job, secondaryJob: s.secondary, activePane: s.activePane }
    : { job: s.job, secondaryJob: null, activePane: 'primary' };
}

/// Is the split actually on screen? A pinned secondary is *kept* while the user
/// visits a chrome job (settings is a full-surface switch, terminal is the
/// bottom panel) and reappears when they come back to a work surface — so the
/// pin survives a trip to Settings instead of being silently dropped.
export function isSplitVisible(s: PaneState): boolean {
  return s.secondary !== null && isSplitEligible(s.job);
}

/// Repair an incoming pane state — used on restore, where the three fields come
/// from separate persisted values and can disagree (a job removed from the
/// registry, a `secondary` that now equals `job`, a stale `activePane` with no
/// split). Every reducer below assumes a healed state.
export function healPanes(s: PaneState): PaneState {
  const job = isKnownJob(s.job) ? s.job : 'fleet';
  const secondary =
    s.secondary !== null && isKnownJob(s.secondary) && isSplitEligible(s.secondary) && s.secondary !== job
      ? s.secondary
      : null;
  // `activePane` may only name a pane that is on screen — including the case
  // where the pin is valid but the primary is a chrome job, so the split is
  // hidden. Otherwise `activeJob()` would report a surface the user cannot see.
  const visible = secondary !== null && isSplitEligible(job);
  return { job, secondary, activePane: visible && s.activePane === 'secondary' ? 'secondary' : 'primary' };
}

/// Swap the two panes' contents. `activePane` deliberately does NOT follow the
/// content: it names a position, so after a swap the user's pane shows the other
/// surface. That is what makes `applySetJob`'s swap-on-click land the clicked job
/// in the pane the user is looking at. Only a VISIBLE split can swap: with the
/// pin parked under a chrome primary, a swap would seat the chrome job in the
/// pane it is banned from.
export function applySwapPanes(s: PaneState): PaneState {
  if (!isSplitVisible(s) || s.secondary === null) return s;
  return { job: s.secondary, secondary: s.job, activePane: s.activePane };
}

/// Point the *active* pane at `job` (VS Code's "open in the active group").
export function applySetJob(s: PaneState, job: JobId): PaneState {
  if (!isKnownJob(job)) return s;
  // Chrome jobs are full-surface switches, never a pane — they take the primary
  // and pull focus with them. `secondary` survives (see `isSplitVisible`).
  if (!isSplitEligible(job)) {
    return job === s.job && s.activePane === 'primary' ? s : { job, secondary: s.secondary, activePane: 'primary' };
  }
  // No split: `activePane` is already 'primary' (a healed invariant).
  if (s.secondary === null) return job === s.job ? s : { ...s, job };
  // Surfaces are singletons over singleton stores (one ReadSurface, one library
  // selection), so the same job can never sit in both panes. Clicking the job
  // already pinned in the OTHER pane swaps instead of duplicating it.
  const other = s.activePane === 'primary' ? s.secondary : s.job;
  // A parked pin (chrome primary) has no visible other pane to swap with:
  // clicking its rail icon means "show me that job", so it takes the primary
  // and the now-duplicate pin is released.
  if (job === other) return isSplitVisible(s) ? applySwapPanes(s) : { job, secondary: null, activePane: 'primary' };
  const current = s.activePane === 'primary' ? s.job : s.secondary;
  if (job === current) return s;
  return s.activePane === 'primary' ? { ...s, job } : { ...s, secondary: job };
}

/// Pin `job` beside the primary, or close the split with `null`. Opening focuses
/// the new pane (the user asked to look at it) — unless the primary is a chrome
/// job, where the pin parks off screen (`isSplitVisible`) and focus may not
/// name it (the healed invariant: `activeJob()` never reports an unseen
/// surface). Closing forces focus back to the primary, which is then the only
/// pane there is.
export function applySetSecondary(s: PaneState, job: JobId | null): PaneState {
  if (job === null) return { ...s, secondary: null, activePane: 'primary' };
  if (!isKnownJob(job) || !isSplitEligible(job) || job === s.job) return s;
  return { ...s, secondary: job, activePane: isSplitEligible(s.job) ? 'secondary' : 'primary' };
}

/// Move focus attribution to a pane. Focusing a pane that isn't there is a no-op.
export function applyFocusPane(s: PaneState, pane: Pane): PaneState {
  if (pane === 'secondary' && s.secondary === null) return s;
  return s.activePane === pane ? s : { ...s, activePane: pane };
}

// ── S2: the divider ratio and the toggle's memory ────────────────────────────

/// The primary pane's share of the row. S1 shipped 50/50 fixed; S2 makes the
/// divider draggable, so the ratio is a single persisted number — no per-pane
/// geometry, which keeps "not a docking framework" true.
export const DEFAULT_SPLIT_RATIO = 0.5;

/// Below this a pane stops being usable (§3.2). The clamp is expressed in pixels
/// because that is what the constraint actually is; `clampSplitRatio` converts.
export const MIN_PANE_PX = 480;

/// Clamp a ratio to the allowed band. Without a width this is the plain
/// 0.25–0.75 guard (used on restore, where no element has been measured yet);
/// with one it also honours `minPx` per pane. On a window too narrow for two
/// minimum panes the pixel rule is unsatisfiable, so the plain guard stands —
/// refusing to resize at all would be worse than a cramped pane the user asked
/// for. NaN/±Infinity from a corrupt persisted value degrade to the default.
export function clampSplitRatio(r: number, width = 0, minPx = MIN_PANE_PX): number {
  const base = Math.min(0.75, Math.max(0.25, Number.isFinite(r) ? r : DEFAULT_SPLIT_RATIO));
  if (width <= 0 || width < minPx * 2) return base;
  return Math.min(1 - minPx / width, Math.max(minPx / width, base));
}

/// What `toggleSplit` pins when nothing was ever pinned — the first eligible job
/// in rail order that isn't already the primary. Deterministic on purpose: the
/// shortcut must always *do* something visible on a fresh install, and one rail
/// click changes it. `null` only if no eligible job exists at all.
export function firstPinCandidate(job: JobId): JobId | null {
  return JOBS.find((j) => j.id !== job && isSplitEligible(j.id))?.id ?? null;
}

// The primary pane keeps the original key and its original shape (a bare job
// id): no migration, and an older build simply ignores the split. The split
// rides in its own key, absent when there is none.
const LS_KEY = 'termipod.workbench.job';
const LS_SPLIT_KEY = 'termipod.workbench.split.v1';

function initialJob(): JobId {
  try {
    const v = localStorage.getItem(LS_KEY);
    if (v === 'canvas') return 'author'; // Canvas folded into Author
    if (isKnownJob(v)) return v;
  } catch {
    /* ignore */
  }
  return 'fleet';
}

/// Everything the split key carries. `secondary`/`activePane` are the live pane
/// state; `ratio` and `lastSecondary` are S2's, and neither is part of
/// `PaneState` — no pane reducer needs them, and keeping them out means the
/// reducers stay a 3-field algebra.
export interface SplitPersist {
  secondary: JobId | null;
  activePane: Pane;
  ratio: number;
  lastSecondary: JobId | null;
}

/// Parse the persisted split blob. Pure over the raw string so the malformed /
/// legacy cases are testable; cross-field repair is `healPanes`'s job. Every
/// field is optional in practice — an S1-era blob has no `ratio`/`lastSecondary`,
/// and each falls back independently rather than voiding the whole blob.
export function parseSplit(raw: string | null): SplitPersist {
  const none: SplitPersist = {
    secondary: null,
    activePane: 'primary',
    ratio: DEFAULT_SPLIT_RATIO,
    lastSecondary: null,
  };
  if (raw === null) return none;
  try {
    const obj: unknown = JSON.parse(raw);
    if (typeof obj !== 'object' || obj === null) return none;
    const o = obj as Record<string, unknown>;
    return {
      secondary: isKnownJob(o.secondary) ? o.secondary : null,
      activePane: o.activePane === 'secondary' ? 'secondary' : 'primary',
      ratio: clampSplitRatio(typeof o.ratio === 'number' ? o.ratio : DEFAULT_SPLIT_RATIO),
      // Only useful if it is still pinnable; `toggleSplit` re-checks against the
      // primary of the moment anyway.
      lastSecondary: isKnownJob(o.lastSecondary) && isSplitEligible(o.lastSecondary) ? o.lastSecondary : null,
    };
  } catch {
    /* malformed JSON — no split */
    return none;
  }
}

function initialSplit(): SplitPersist {
  try {
    return parseSplit(localStorage.getItem(LS_SPLIT_KEY));
  } catch {
    /* no localStorage (node tests) */
    return { secondary: null, activePane: 'primary', ratio: DEFAULT_SPLIT_RATIO, lastSecondary: null };
  }
}

function writeSplit(s: PaneState & { ratio: number; lastSecondary: JobId | null }): void {
  try {
    localStorage.setItem(LS_KEY, s.job);
    // A closed split still records `ratio`/`lastSecondary` — they are what makes
    // reopening restore the shape the user had, so the key is only cleared when
    // there is genuinely nothing to remember.
    if (s.secondary === null && s.lastSecondary === null && s.ratio === DEFAULT_SPLIT_RATIO) {
      localStorage.removeItem(LS_SPLIT_KEY);
      return;
    }
    localStorage.setItem(
      LS_SPLIT_KEY,
      JSON.stringify({
        secondary: s.secondary,
        activePane: s.activePane,
        ratio: s.ratio,
        lastSecondary: s.lastSecondary,
      }),
    );
  } catch {
    /* ignore */
  }
}

interface WorkbenchState extends PaneState {
  /** The primary pane's share of the row (S2, `clampSplitRatio`'s band). */
  ratio: number;
  /** The last job that was pinned, so `toggleSplit` can reopen it. */
  lastSecondary: JobId | null;
  setJob: (job: JobId) => void;
  setSecondary: (job: JobId | null) => void;
  focusPane: (pane: Pane) => void;
  swapPanes: () => void;
  /** Close a live split, or reopen the last one (S2's `Mod+\`). */
  toggleSplit: () => void;
  setRatio: (ratio: number) => void;
}

export const useWorkbench = create<WorkbenchState>((set, get) => {
  const split = initialSplit();
  // Every reducer above returns its INPUT object verbatim for a no-op, so
  // identity is the "nothing changed" signal. Checking it matters: the shell
  // calls `focusPane` from a capture-phase mousedown on each pane, i.e. on every
  // click in the app — without this, each one would rewrite localStorage.
  const commit = (next: PaneState): void => {
    if (next === get()) return;
    // Remember whatever is pinned, so closing and reopening returns to it. A
    // close leaves the memory alone (`null ?? previous`), which is the whole
    // point of the toggle.
    set({ ...next, lastSecondary: next.secondary ?? get().lastSecondary });
    writeSplit(get());
  };
  // A drag fires `setRatio` on every pointermove (dozens/s) and a synchronous
  // localStorage write each time is a main-thread stall (#311) — the same reason
  // `usePanelWidth` debounces. The ratio itself updates live for a smooth drag;
  // only the write is deferred until the gesture settles.
  let ratioTimer: ReturnType<typeof setTimeout> | undefined;
  return {
    ...healPanes({ job: initialJob(), secondary: split.secondary, activePane: split.activePane }),
    ratio: split.ratio,
    lastSecondary: split.lastSecondary,
    setJob: (job) => commit(applySetJob(get(), job)),
    setSecondary: (job) => commit(applySetSecondary(get(), job)),
    focusPane: (pane) => commit(applyFocusPane(get(), pane)),
    swapPanes: () => commit(applySwapPanes(get())),
    toggleSplit: () => {
      const s = get();
      // Nothing pairs with a chrome primary, so the chord is inert there — the
      // same rule that keeps the split commands out of the palette on Settings.
      if (!isSplitEligible(s.job)) return;
      if (s.secondary !== null) {
        commit(applySetSecondary(s, null));
        return;
      }
      const remembered = s.lastSecondary !== null && s.lastSecondary !== s.job ? s.lastSecondary : null;
      const pick = remembered ?? firstPinCandidate(s.job);
      if (pick !== null) commit(applySetSecondary(s, pick));
    },
    setRatio: (ratio) => {
      const next = clampSplitRatio(ratio);
      if (next === get().ratio) return;
      set({ ratio: next });
      if (ratioTimer !== undefined) clearTimeout(ratioTimer);
      ratioTimer = setTimeout(() => writeSplit(get()), 250);
    },
  };
});
