/// Agent pointing — electron-free core (D6 —
/// docs/plans/desktop-ui-context-and-pointing.md §3.4b, ADR-062 D-5).
///
/// Deixis is symmetric: a collaborating agent needs "look here" as much as
/// "what are you looking at". `ui_highlight` is the pointing half, and it is
/// deliberately the WEAKEST capability in the plan — it draws a glow and
/// expires. It never focuses, scrolls, clicks or types (the no-driving
/// non-goal), so it needs no approval card; consent is the sharing toggle plus
/// the policy table's `highlight` bit.
///
/// What it does need is the discipline that keeps a non-actuating annotation
/// from becoming attention spam or fake UI (plan §6):
///   - the `highlight` COLUMN decides which surfaces may be drawn over at all;
///   - it is always ATTRIBUTED — the marker names the agent, so nothing an
///     agent draws can be mistaken for the app talking;
///   - it is TTL-bounded, and the bound is ours, not the caller's;
///   - it is RATE-LIMITED per agent, so a loop cannot paper the screen;
///   - the note is one short line, never a message channel.
///
/// All of that is decided here so `node --test` proves it without Electron.
import { uiPolicyFor } from '../../src/state/ui_policy.ts';
import { uiRefFromJson, type UiRef } from '../../src/state/uiRef.ts';

export const HIGHLIGHT_TTL_DEFAULT_MS = 8_000;
export const HIGHLIGHT_TTL_MAX_MS = 30_000;
export const HIGHLIGHT_NOTE_MAX = 140;

/// Per-agent budget: how many highlights an agent may raise inside the window.
/// Generous enough for a real pointing conversation ("this one… no, this one")
/// and far short of anything that could paper the screen.
export const HIGHLIGHT_RATE_LIMIT = 6;
export const HIGHLIGHT_RATE_WINDOW_MS = 60_000;

export function clampHighlightTtl(requested: number | null): number {
  if (requested === null) return HIGHLIGHT_TTL_DEFAULT_MS;
  return Math.max(500, Math.min(requested, HIGHLIGHT_TTL_MAX_MS));
}

export function clipHighlightNote(note: string): string {
  const flat = note.replace(/\s+/g, ' ').trim();
  return flat.length > HIGHLIGHT_NOTE_MAX ? `${flat.slice(0, HIGHLIGHT_NOTE_MAX - 1)}…` : flat;
}

/// What the renderer is asked to draw. Everything an overlay needs and nothing
/// more — no pixels, no content, and the attribution is not optional.
export interface HighlightOrder {
  id: string;
  ref: UiRef;
  note: string;
  /// Who is pointing. Rendered on the marker; an unnamed agent shows as its id
  /// rather than as nobody, because an unattributed glow is the failure mode
  /// the risk section names.
  by: string;
  ttl_ms: number;
  at: string;
}

export type HighlightDecision =
  | { ok: true; order: HighlightOrder }
  | { ok: false; code: string; message: string };

export interface HighlightInput {
  ref: unknown;
  note: string;
  ttlMs: number | null;
  agentId: string;
  agentHandle: string;
  /// Monotonic-ish clock, injected for the tests.
  now: number;
  /// Timestamps of this agent's recent highlights, newest last. The caller
  /// owns the store; this function owns the rule.
  recent: readonly number[];
  /// Unique id for the order (the caller supplies it — this module mints
  /// nothing so it stays deterministic under test).
  id: string;
  iso: string;
}

/// Decide one highlight. Order matters: parse, then policy, then rate — an
/// unparseable ref should not consume an agent's budget, and a refused surface
/// should not either (a refusal is the table talking, not abuse).
export function decideHighlight(input: HighlightInput): HighlightDecision {
  const ref = uiRefFromJson(input.ref);
  if (ref === null) {
    return {
      ok: false,
      code: 'INVALID_REF',
      message:
        'ref must be a UIRef: the JSON shape ui_get_focus returns ({"surface":"replay","entity":{…}}) or its URI spelling ("ui://replay?dataset_id=ds_1")',
    };
  }
  const row = uiPolicyFor(ref.surface);
  if (row === null) {
    return { ok: false, code: 'UNKNOWN_SURFACE', message: `'${ref.surface}' is not a surface this desktop declares` };
  }
  if (row.highlight !== 'allow') {
    return {
      ok: false,
      code: 'HIGHLIGHT_REFUSED',
      message: `surface '${ref.surface}' refuses agent annotation by policy (ui_policy.ts highlight column)`,
    };
  }
  const window = input.recent.filter((t) => input.now - t < HIGHLIGHT_RATE_WINDOW_MS);
  if (window.length >= HIGHLIGHT_RATE_LIMIT) {
    return {
      ok: false,
      code: 'HIGHLIGHT_RATE_LIMITED',
      message: `too many highlights (${String(HIGHLIGHT_RATE_LIMIT)} per minute) — say it in words instead`,
    };
  }
  const by = input.agentHandle !== '' ? input.agentHandle : input.agentId !== '' ? input.agentId : 'an agent';
  return {
    ok: true,
    order: {
      id: input.id,
      ref,
      note: clipHighlightNote(input.note),
      by,
      ttl_ms: clampHighlightTtl(input.ttlMs),
      at: input.iso,
    },
  };
}

/// Drop timestamps outside the window — the caller's store stays bounded
/// without a sweeper.
export function pruneHighlightHistory(recent: readonly number[], now: number): number[] {
  return recent.filter((t) => now - t < HIGHLIGHT_RATE_WINDOW_MS);
}
