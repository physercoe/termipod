/// `desktop_open` — the NAVIGATE class, electron-free core (coworking lane H;
/// ADR-064 §6).
///
/// The three desktop-UI classes before this one all stop short of the user's
/// attention: `ui_get_focus` describes their screen, `ui_screenshot` photographs
/// it, `ui_highlight` draws over it. None of them MOVES it. `desktop_open` does
/// — it is the first capability in this set that actuates, and the whole design
/// is about earning that.
///
/// **Why no approval card.** The line ADR-062 draws is between describing and
/// acting, and this is on the acting side; the line that matters for CONSENT,
/// though, is what an unwanted call costs. An `author_apply` the user did not
/// want has changed their work and must be asked for. A `desktop_open` they did
/// not want has changed which tab is in front of them, is attributed by name on
/// a banner, and is undone by one click on that banner. A card on every
/// navigation would make "show me where" cost two round trips and a decision,
/// which is how a useful verb becomes one nobody calls. So the counterweights
/// are: the `navigate` policy column, a per-agent rate limit, attribution, and
/// undo — the `ui_highlight` posture (ADR-062 D-5), with undo added because
/// this one is not self-expiring.
///
/// **What it cannot address.** The policy table's `navigate` column is OPTIONAL
/// and its only value is `'allow'`. Terminal, Settings, kimiweb and vault simply
/// have no such field, so they are unreachable by construction rather than by a
/// false bit somebody could flip (ADR-064 §12).
import { uiPolicyFor } from '../../src/state/ui_policy.ts';
import { uiRefFromJson, uiRefLabel, type UiRef } from '../../src/state/uiRef.ts';

/// Per-agent budget. Lower than `ui_highlight`'s six: a highlight is a glow the
/// user can ignore, and a navigation takes the screen. Three in a minute covers
/// "look at this run… and its config… and the diff" and stops a loop from
/// yanking someone around their own app.
export const OPEN_RATE_LIMIT = 3;
export const OPEN_RATE_WINDOW_MS = 60_000;

/// The reason line on the banner. One short sentence — this is a caption, not
/// a message channel, exactly like a highlight's note.
export const OPEN_NOTE_MAX = 140;

export function clipOpenNote(note: string): string {
  const flat = note.replace(/\s+/g, ' ').trim();
  return flat.length > OPEN_NOTE_MAX ? `${flat.slice(0, OPEN_NOTE_MAX - 1)}…` : flat;
}

/// What the renderer is asked to do, and what the banner says about it.
export interface OpenOrder {
  id: string;
  ref: UiRef;
  note: string;
  /// Who navigated. Never empty — an unattributed jump reads as the app
  /// misbehaving, which is the failure mode attribution exists to prevent.
  by: string;
  at: string;
}

export type OpenDecision = { ok: true; order: OpenOrder } | { ok: false; code: string; message: string };

export interface OpenInput {
  ref: unknown;
  note: string;
  agentId: string;
  agentHandle: string;
  now: number;
  recent: readonly number[];
  id: string;
  iso: string;
}

/// Decide one navigation. Same order as `decideHighlight`, for the same reason:
/// parse, then policy, then rate — neither an unparseable ref nor a refused
/// surface is the abuse the budget exists for, so neither spends it.
export function decideOpen(input: OpenInput): OpenDecision {
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
  if (row.navigate !== 'allow') {
    // Deliberately says the surface cannot be navigated to rather than "you are
    // not allowed": for terminal, settings and vault there is no configuration
    // that would change the answer, and an agent told "refused" retries while
    // one told "cannot" moves on.
    return {
      ok: false,
      code: 'NAVIGATE_REFUSED',
      message: `'${ref.surface}' cannot be opened by an agent — it is outside the desktop-UI capability set (ui_policy.ts has no navigate column for it)`,
    };
  }
  const window = input.recent.filter((t) => input.now - t < OPEN_RATE_WINDOW_MS);
  if (window.length >= OPEN_RATE_LIMIT) {
    return {
      ok: false,
      code: 'NAVIGATE_RATE_LIMITED',
      message: `too many navigations (${String(OPEN_RATE_LIMIT)} per minute) — the user is trying to work; point with ui_highlight or say where in words`,
    };
  }
  const by = input.agentHandle !== '' ? input.agentHandle : input.agentId !== '' ? input.agentId : 'an agent';
  return { ok: true, order: { id: input.id, ref, note: clipOpenNote(input.note), by, at: input.iso } };
}

export function pruneOpenHistory(recent: readonly number[], now: number): number[] {
  return recent.filter((t) => now - t < OPEN_RATE_WINDOW_MS);
}

/// What the agent is told once the renderer has acted.
///
/// The DEPTH is in the sentence because the agent's next move depends on it:
/// landing on the surface without the entity means "I put you in Replay" and
/// not "I opened that episode", and an agent that reports the second when the
/// first happened has told the user something false about their own screen.
export function openResultText(ref: UiRef, depth: 'entity' | 'surface'): string {
  const label = uiRefLabel(ref);
  if (depth === 'entity') {
    return `opened ${label} — the user is looking at it now, with a banner naming you and an undo that puts them back.`;
  }
  return (
    `switched the user to the ${ref.surface} surface, but could not open ${label} itself — this build resolves that reference no further. ` +
    `Say what to look for; do not claim you opened it.`
  );
}

/// The refusal for a ref the renderer could not act on at all.
export function openUnresolvedMessage(ref: UiRef): string {
  return `'${ref.surface}' is declared but this build has no way to show it — nothing on the user's screen changed`;
}
