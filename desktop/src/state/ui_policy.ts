/// Desktop UI context — the per-surface policy table + focus projection
/// (docs/plans/desktop-ui-context-and-pointing.md §3.0/§3.2, ADR-062 D-2/D-3).
///
/// PRIVACY RULE (the table IS the privacy review): every field the focus
/// snapshot may carry is an id, a path, a fragment-stripped URL, a title, or
/// coordinates/line numbers — NEVER content (no message bodies, no file
/// contents, no document bodies, no vault material, no settings values).
/// Secrets are absent BY CONSTRUCTION: the projection below picks only the
/// allowlisted sub-fields, so a raw focus state that grew a content field
/// would still never emit it. Adding a surface or a field is a one-line diff
/// in THIS file; the unit test fails on an undeclared surface.
///
/// One row per surface, three independent columns (the sensitivities differ):
///   - `snapshot`:  the exact emitted-field allowlist for the semantic
///     projection, as dot paths into RawFocus (empty = existence only —
///     the snapshot degrades to `{surface, captured_at}`);
///   - `capture`:   may pixels of this surface ever be captured (D3);
///   - `highlight`: may an agent draw a pointing annotation over it (D6).
/// Only `snapshot` has a consumer in D1; the other two are the frame the
/// later wedges fill. Vault refuses everything, always; settings declares
/// its existence but refuses pixel capture (a capture shows its values).
///
/// This module is deliberately dependency-free (like webtab_policy.ts): the
/// renderer publisher and the node --test suites import it without dragging
/// stores or electron along.

// ── The UIRef blocks (ADR-062 D-2) ───────────────────────────────────────────
// Every block is optional in a raw focus state; every field inside a block is
// optional too (a surface may not currently have the value — e.g. no active
// tab). The sub-field names here are the allowlist vocabulary: `snapshot`
// entries name top-level block + sub-field (`block.field`).

export interface UiRefAgent {
  id?: string;
  handle?: string;
  session_id?: string;
}

export interface UiRefProject {
  project_id?: string;
  task_id?: string;
}

export interface UiRefTab {
  kind?: string;
  title?: string;
  /// Fragment-stripped (the W2 redaction rule — a guest URL's hash can carry
  /// a bearer, see browserbridge.ts stripFragment).
  url?: string;
  path?: string;
}

export interface UiRefDocument {
  id?: string;
  title?: string;
}

export interface UiRefInspectTab {
  kind?: string;
  path?: string;
}

export interface UiRefInspect {
  path?: string;
  /// [fromLine, toLine] — line numbers only.
  selection?: [number, number];
}

export interface UiRefCompare {
  /// The two refs being compared (ids, never diff bodies). On the comparison
  /// wall these are run ids — dereferenceable with `runs_get` / `run_metrics`,
  /// which is what makes the pair a join key rather than a label (ADR-062 D-2).
  left?: string;
  right?: string;
}

export interface UiRefReplay {
  dataset_id?: string;
  episode_id?: string;
  cursor?: number;
}

export interface UiRefRecord {
  /// The decision record the surface is on. Named `record_id` since G5: the
  /// field this row reserved was `dataset_id`, describing an episode-recording
  /// surface that was never built — Record is decision capture (ADR-shaped
  /// records), and a dataset id there was a handle to the wrong entity.
  record_id?: string;
}

export interface UiRefTerminal {
  pane_id?: string;
  agent_id?: string;
}

export interface UiRefKimiweb {
  /// The kimi-web guest URL (loopback, fragment-stripped).
  url?: string;
}

/// One pane of a split, as the agent sees it (split-pane plan §3.4). Carries its
/// surface id plus that surface's OWN allowlisted blocks — a pinned pane is
/// served under its own policy row, not under the focused pane's.
export interface UiPaneRef {
  surface: string;
  /// …plus the row's allowlisted blocks, same shapes as `RawFocus`'s.
  [block: string]: unknown;
}

/// The pre-projection focus state assembled renderer-side from the stores.
///
/// `surface` is the **primary** pane's workbench job id (workbench.ts) — or a
/// pseudo-surface id (`kimiweb`, `vault`) once a surface exposes one. With no
/// split that is simply the surface on screen. The top level is POSITIONAL so
/// that a split can describe *both* panes: `secondary` is the pinned one and
/// `active_pane` says which of the two the user is actually in.
export interface RawFocus {
  surface: string;
  captured_at: string;
  /// The pinned pane — present only while the split is on screen. A *parked*
  /// pin (kept while the user visits Settings) is never reported: an agent is
  /// not told about a pane the user cannot see.
  secondary?: UiPaneRef;
  /// Which pane holds the user's focus. Present only alongside `secondary`;
  /// absent means there is one pane and it is the one described above.
  active_pane?: 'primary' | 'secondary';
  agent?: UiRefAgent;
  project?: UiRefProject;
  tabs?: UiRefTab[];
  tab?: UiRefTab;
  document?: UiRefDocument;
  inspect_tabs?: UiRefInspectTab[];
  inspect?: UiRefInspect;
  compare?: UiRefCompare;
  replay?: UiRefReplay;
  record?: UiRefRecord;
  terminal?: UiRefTerminal;
  kimiweb?: UiRefKimiweb;
}

/// The agent-facing answer — the UIRef wrapped with its capture time (plan
/// §3.2 example). Same shape as RawFocus, post-projection: only allowlisted
/// fields survive. `captured_at` tells the agent how stale the answer is; the
/// tool never blocks waiting for a fresh one.
export type UiFocusSnapshot = RawFocus;

// ── The policy table (ADR-062 D-3) ───────────────────────────────────────────

export type UiPolicyBit = 'allow' | 'refuse';

export interface UiPolicyRow {
  readonly snapshot: readonly string[];
  readonly capture: UiPolicyBit;
  readonly highlight: UiPolicyBit;
}

/// The rows mirror the §3.2 "Surface coverage" matrix exactly. `settings`
/// (and the `vault` pseudo-surface) carry an empty allowlist: their snapshots
/// are existence-only by row, and — because the ACTIVE surface's row gates
/// the whole projection — nothing else leaks while one of them is focused.
export const UI_POLICY: Readonly<Record<string, UiPolicyRow>> = {
  fleet: { snapshot: ['agent.id', 'agent.handle', 'agent.session_id'], capture: 'allow', highlight: 'allow' },
  projects: { snapshot: ['project.project_id', 'project.task_id'], capture: 'allow', highlight: 'allow' },
  read: {
    snapshot: ['tabs.kind', 'tabs.title', 'tabs.url', 'tabs.path', 'tab.kind', 'tab.title', 'tab.url', 'tab.path'],
    capture: 'allow',
    highlight: 'allow',
  },
  author: { snapshot: ['document.id', 'document.title'], capture: 'allow', highlight: 'allow' },
  debug: {
    snapshot: ['inspect_tabs.kind', 'inspect_tabs.path', 'inspect.path', 'inspect.selection'],
    capture: 'allow',
    highlight: 'allow',
  },
  compare: { snapshot: ['compare.left', 'compare.right'], capture: 'allow', highlight: 'allow' },
  replay: { snapshot: ['replay.dataset_id', 'replay.episode_id', 'replay.cursor'], capture: 'allow', highlight: 'allow' },
  record: { snapshot: ['record.record_id'], capture: 'allow', highlight: 'allow' },
  terminal: { snapshot: ['terminal.pane_id', 'terminal.agent_id'], capture: 'allow', highlight: 'allow' },
  settings: { snapshot: [], capture: 'refuse', highlight: 'allow' },
  kimiweb: { snapshot: ['kimiweb.url'], capture: 'allow', highlight: 'allow' },
  vault: { snapshot: [], capture: 'refuse', highlight: 'refuse' },
};

export function uiPolicyFor(surface: string): UiPolicyRow | null {
  return UI_POLICY[surface] ?? null;
}

// ── The projection ───────────────────────────────────────────────────────────

/// The surface whose row gates the answer: the one the user is actually in.
/// With a split that can be the pinned pane, not the top-level (primary) one —
/// the gate follows *focus*, so "the user is in the vault" suppresses the
/// snapshot no matter which half of the row the vault occupies.
function activeSurfaceOf(raw: RawFocus): string {
  return raw.active_pane === 'secondary' && raw.secondary !== undefined ? raw.secondary.surface : raw.surface;
}

/// Project a raw focus state through the table. Three rules:
///   1. the ACTIVE surface's row gates the answer — unknown surface, or a row
///      with an empty allowlist (settings, vault), degrades to existence only
///      (`{surface, captured_at}`), suppressing every cross-surface block, the
///      pinned pane and the focus attribution too;
///   2. otherwise each surface row contributes its own block(s) from the raw
///      state (the §3.2 example: surface=read still carries the focused
///      agent, the Inspect path + selection, the terminal pane), filtered to
///      exactly the row's allowlisted sub-fields;
///   3. a visible split adds `secondary` + `active_pane` (split-pane plan
///      §3.4). The pinned pane is served under ITS OWN row, so pinning a
///      surface can never publish more than opening it would.
export function projectFocus(raw: RawFocus): UiFocusSnapshot {
  const out = { surface: raw.surface, captured_at: raw.captured_at } as UiFocusSnapshot;
  const active = UI_POLICY[activeSurfaceOf(raw)];
  if (active === undefined || active.snapshot.length === 0) return out;
  for (const row of Object.values(UI_POLICY)) applyAllowlist(out as unknown as Record<string, unknown>, raw, row.snapshot);
  if (raw.secondary !== undefined) {
    const row = UI_POLICY[raw.secondary.surface];
    // An undeclared surface is not published at all — same posture as rule 1.
    if (row !== undefined) {
      const pane: Record<string, unknown> = { surface: raw.secondary.surface };
      applyAllowlist(pane, raw, row.snapshot);
      out.secondary = pane as UiPaneRef;
      out.active_pane = raw.active_pane ?? 'primary';
    }
  }
  return out;
}

/// Pick the allowlisted sub-fields of one block. Paths are `block.field`;
/// array blocks apply the sub-field set to every item. Empty results (block
/// absent, or no allowlisted field present) emit nothing.
function applyAllowlist(out: Record<string, unknown>, raw: RawFocus, paths: readonly string[]): void {
  const byBlock = new Map<string, string[]>();
  for (const p of paths) {
    const dot = p.indexOf('.');
    if (dot < 0) continue; // whole-block entries are not used; every row is block.field
    const subs = byBlock.get(p.slice(0, dot)) ?? [];
    subs.push(p.slice(dot + 1));
    byBlock.set(p.slice(0, dot), subs);
  }
  for (const [block, subs] of byBlock) {
    const val = (raw as unknown as Record<string, unknown>)[block];
    if (val === undefined || val === null) continue;
    if (Array.isArray(val)) {
      const items = val
        .map((item) => pickSubFields(item as Record<string, unknown>, subs))
        .filter((item) => Object.keys(item).length > 0);
      if (items.length > 0) out[block] = items;
    } else if (typeof val === 'object') {
      const picked = pickSubFields(val as Record<string, unknown>, subs);
      if (Object.keys(picked).length > 0) out[block] = picked;
    }
  }
}

function pickSubFields(val: Record<string, unknown>, subs: readonly string[]): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const s of subs) {
    const v = val[s];
    if (v !== undefined && v !== null) out[s] = v;
  }
  return out;
}

// ── Assembly (store slices → raw focus state) ────────────────────────────────
// The publisher's pure half: plain inputs in, a RawFocus out — no store or
// platform imports, so the shape is unit-testable without the renderer. The
// zustand glue lives in uiContext.ts.

export interface FocusSources {
  /// The PRIMARY pane's job — the only surface when there is no split. (D1
  /// published the active pane here, because there was no way to express the
  /// other one; S3 makes the top level positional and names the focused pane in
  /// `activePane`, so a snapshot can carry both.)
  job: string;
  /// The pinned pane's job, or null when no split is on screen. Optional: absent
  /// reads as "no split", which is what every pre-S3 caller meant.
  secondaryJob?: string | null;
  /// Which pane the user is in. Only meaningful alongside `secondaryJob`.
  activePane?: 'primary' | 'secondary';
  /// useFocus.fleet.selection — contributes `agent` when it names an agent.
  fleetSelection: { type: string; id: string; name?: string } | null;
  /// useFocus.projects.selection — contributes `project` when it names one.
  projectSelection: { type: string; id: string; name?: string } | null;
  /// The Author surface's active document, if any.
  activeDocument: { id: string; title: string } | null;
  /// Read's open tabs, already narrowed to the allowlisted sub-fields (G1).
  /// Optional: absent reads as "no tabs", which is what every pre-G1 caller
  /// meant.
  ///
  /// `path` is absent from the type on purpose, though the allowlist reserves
  /// `tabs.path`. A pdf tab addresses its bytes by reference + attachment id —
  /// hub ids, not a filesystem path — so there is nothing truthful to put
  /// there, and the type is what says so (a comment would let a future caller
  /// pass a plausible-looking wrong one).
  readTabs?: ReadonlyArray<{ kind: string; title: string; url?: string }>;
  /// The Read tab the user is in, or null for the library view (which is not a
  /// tab — publishing one would claim they are reading something) (G1).
  readActive?: { kind: string; title: string; url?: string } | null;
  /// Inspect (debug) tabs, already narrowed to {kind, path?}.
  inspectTabs: ReadonlyArray<{ kind: string; path?: string }>;
  /// The active Inspect tab's {path?}, if a tab is active.
  inspectActive: { path?: string } | null;
  /// The active Inspect tab's 1-based line selection, if the user made one (G2).
  inspectSelection?: [number, number] | null;
  /// The comparison wall's selected run ids, in pick order (G4). Optional:
  /// absent reads as "nothing selected", which is what every pre-G4 caller
  /// meant.
  compareSelected?: readonly string[];
  /// The wall's baseline run — the one every delta is measured against — or
  /// null for "no baseline" (G4).
  compareBaseline?: string | null;
  /// The Replay surface's selected dataset id, if any.
  replayDatasetId: string | null;
  /// The open episode's id, if the player is on screen (G3).
  replayEpisodeId?: string | null;
  /// The player's scrub position, if the user has placed one (G3).
  replayCursor?: number | null;
  /// The focused terminal pane id — null when no pane is visible.
  terminalPaneId: string | null;
  capturedAt: string;
}

/// The wall's selection as a left/right pair, or null when the two-field block
/// cannot state it (G4). The baseline goes LEFT when it is one of the two:
/// every delta on the wall is measured against it, so baseline-then-other is
/// the direction the numbers already read in, and it matches the diff idiom
/// the block's field names come from.
function comparePair(selected: readonly string[] | undefined, baseline: string | null | undefined): UiRefCompare | null {
  if (selected === undefined || selected.length !== 2) return null;
  const [a, b] = selected;
  if (baseline === b) return { left: b, right: a };
  return { left: a, right: b };
}

export function assembleRawFocus(s: FocusSources): RawFocus {
  const raw: RawFocus = { surface: s.job, captured_at: s.capturedAt };
  if (s.fleetSelection !== null && s.fleetSelection.type === 'agent') {
    raw.agent = s.fleetSelection.name !== undefined ? { id: s.fleetSelection.id, handle: s.fleetSelection.name } : { id: s.fleetSelection.id };
  }
  if (s.projectSelection !== null && s.projectSelection.type === 'project') {
    raw.project = { project_id: s.projectSelection.id };
  }
  if (s.activeDocument !== null) {
    raw.document = { id: s.activeDocument.id, title: s.activeDocument.title };
  }
  if (s.readTabs !== undefined && s.readTabs.length > 0) {
    raw.tabs = s.readTabs.map((t) => ({ ...t }));
  }
  if (s.readActive !== undefined && s.readActive !== null) {
    raw.tab = { ...s.readActive };
  }
  if (s.inspectTabs.length > 0) {
    raw.inspect_tabs = s.inspectTabs.map((t) => (t.path !== undefined ? { kind: t.kind, path: t.path } : { kind: t.kind }));
  }
  // The selection is a property OF the active tab, so it rides the same block —
  // and a selection with no active tab is not a state the surface can be in.
  if (s.inspectActive !== null) {
    const inspect: UiRefInspect = {};
    if (s.inspectActive.path !== undefined) inspect.path = s.inspectActive.path;
    if (s.inspectSelection !== undefined && s.inspectSelection !== null) inspect.selection = s.inspectSelection;
    if (Object.keys(inspect).length > 0) raw.inspect = inspect;
  }
  // The wall is an N-run surface and this block has two fields, so it speaks
  // ONLY for the case it can state truthfully: exactly two runs selected. With
  // three the pair would be a silent truncation — an agent reading
  // {left, right} has no way to learn there was a third curve on screen — and
  // with one there is no comparison to name. The richer `wall` UIRef
  // ({project_id, runs[], baseline, metric}) is A6's, and carries the whole
  // selection; until it lands, "nothing" beats "two of three".
  const pair = comparePair(s.compareSelected, s.compareBaseline);
  if (pair !== null) {
    raw.compare = pair;
  }
  if (s.replayDatasetId !== null) {
    const replay: UiRefReplay = { dataset_id: s.replayDatasetId };
    if (s.replayEpisodeId !== undefined && s.replayEpisodeId !== null) replay.episode_id = s.replayEpisodeId;
    // `cursor: 0` is a real position — guard on null, never on falsiness.
    if (s.replayCursor !== undefined && s.replayCursor !== null) replay.cursor = s.replayCursor;
    raw.replay = replay;
  }
  if (s.terminalPaneId !== null) {
    raw.terminal = { pane_id: s.terminalPaneId };
  }
  if (s.secondaryJob !== undefined && s.secondaryJob !== null) {
    raw.secondary = { surface: s.secondaryJob };
    raw.active_pane = s.activePane ?? 'primary';
  }
  return raw;
}

// ── Throttle + coalescing sender (plan §3.2: ≥500 ms, coalesced) ─────────────
// The first change after a quiet window sends immediately; changes inside the
// window collapse into ONE trailing send carrying the latest snapshot. A
// snapshot content-identical to the last one sent (most store ticks change
// nothing the projection reads) is dropped before any timer is armed —
// `captured_at` is excluded from that comparison, because the assembly mints
// a fresh timestamp on every store tick and would otherwise defeat the dedupe
// (an Inspect typing burst would stream sends forever).

export interface FocusSender {
  push: (snapshot: UiFocusSnapshot) => void;
  /// Drop any pending trailing send (toggle-off) — nothing more is emitted.
  cancel: () => void;
}

/// Timer/clock injection keeps the tests deterministic; production uses the
/// platform defaults.
export function createFocusSender(
  intervalMs: number,
  send: (snapshot: UiFocusSnapshot) => void,
  timing: {
    now?: () => number;
    setTimeout?: (fn: () => void, ms: number) => unknown;
    clearTimeout?: (handle: unknown) => void;
  } = {},
): FocusSender {
  const now = timing.now ?? (() => Date.now());
  const st = timing.setTimeout ?? ((fn, ms) => setTimeout(fn, ms));
  const ct = timing.clearTimeout ?? ((h) => clearTimeout(h as Parameters<typeof clearTimeout>[0]));
  let lastSentAt = Number.NEGATIVE_INFINITY;
  let lastKey = '';
  let pending: UiFocusSnapshot | null = null;
  let timer: unknown = null;

  // Dedupe on content, not on capture time (see the header). The spread keeps
  // key order, so deterministic assembly ⇒ deterministic key.
  const dedupeKey = (snap: UiFocusSnapshot): string => JSON.stringify({ ...snap, captured_at: '' });

  const deliver = (snap: UiFocusSnapshot): void => {
    lastSentAt = now();
    lastKey = dedupeKey(snap);
    send(snap);
  };

  return {
    push(snapshot) {
      const key = dedupeKey(snapshot);
      if (pending === null && key === lastKey) return; // identical to what's already out
      const elapsed = now() - lastSentAt;
      if (elapsed >= intervalMs) {
        if (timer !== null) {
          ct(timer);
          timer = null;
        }
        pending = null;
        deliver(snapshot);
        return;
      }
      pending = snapshot; // latest wins — earlier in-window changes coalesce away
      if (timer === null) {
        timer = st(() => {
          timer = null;
          const p = pending;
          pending = null;
          if (p !== null) deliver(p);
        }, intervalMs - elapsed);
      }
    },
    cancel() {
      if (timer !== null) ct(timer);
      timer = null;
      pending = null;
    },
  };
}
