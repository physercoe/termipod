import { num, str, type Entity } from '../hub/types.ts';

/// R3 — the two structural markers a transcript needs and desktop threw away:
/// where each turn ended, and where the engine's memory of the conversation
/// stopped matching the transcript.
///
/// Pure so both decisions are unit-tested; `EventCard` only draws what these
/// return. Kept out of feedLens because these answer "what IS this row",
/// not "should it be shown".

export interface TurnFooter {
  /// Wall-clock for the turn, when the engine reported one. Claude ships it on
  /// `turn.result`; codex ships `durationMs` (lifted by the profile, filled in
  /// from the driver clock when absent — vision-parity E2b).
  durationMs?: number;
  messages?: number;
  /// Engine-reported only. The digest's imputed figure is a different number
  /// and is labeled where it is shown.
  costUsd?: number;
  status?: string;
  /// A turn that ended badly reads differently from one that ended. Mirrors
  /// the hub's `isFailedTurn` (digest_fold.go) rather than inventing a second
  /// definition: anything that is not a success status.
  failed: boolean;
}

const OK_STATUSES = new Set(['success', 'completed', 'ok', 'done', 'end_turn', 'stop']);

/// The quiet line under a closed turn. Always returns a footer — a turn that
/// reported nothing but its own existence still ended, and the row marks the
/// boundary even when every field is absent.
export function turnFooter(payload: Entity): TurnFooter {
  const status = str(payload, 'status') ?? str(payload, 'stop_reason');
  return {
    durationMs: num(payload, 'duration_ms'),
    messages: num(payload, 'message_count') ?? num(payload, 'num_turns'),
    costUsd: num(payload, 'cost_usd'),
    status,
    failed: status !== undefined && status !== '' && !OK_STATUSES.has(status),
  };
}

export type ContextVerb = 'compact' | 'clear' | 'rewind';

export interface ContextDivider {
  verb: ContextVerb;
  /// The engine's own word for it (`compress` on gemini), when the producer
  /// said. Shown on hover; the divider's label follows the typed KIND so one
  /// vocabulary reads the same across engines.
  engineVerb?: string;
  /// kimi's M4 tap reports the token delta across a compaction; claude's
  /// `compact_boundary` carries only the subtype, and the hub's input-route
  /// markers carry none at all. Absent means "not reported", and the divider
  /// then says only that the boundary happened.
  tokensBefore?: number;
  tokensAfter?: number;
  summary?: string;
}

/// True when the event is a structural context boundary rather than a message.
/// Recognizes both producers:
///
///   - the hub's input-route markers (`context.compacted` / `.cleared` /
///     `.rewound`), which record the user's INTENT — the hub sees the slash
///     command, never the engine's confirmation (ADR-014 OQ-4);
///   - the engines' own boundary notices, which arrive as `system` with a
///     subtype: claude M4 `compact_boundary`, kimi M4 `compaction` (the only
///     one carrying a token delta).
///
/// Returns undefined for everything else, including a `system` event with no
/// subtype — a generic system notice is not a boundary and must not draw a
/// rule across the transcript.
export function contextDivider(kind: string, payload: Entity): ContextDivider | undefined {
  switch (kind) {
    case 'context.compacted':
      return { verb: 'compact', engineVerb: str(payload, 'verb') };
    case 'context.cleared':
      return { verb: 'clear', engineVerb: str(payload, 'verb') };
    case 'context.rewound':
      return { verb: 'rewind', engineVerb: str(payload, 'verb') };
    case 'system':
      break;
    default:
      return undefined;
  }
  const subtype = str(payload, 'subtype');
  if (subtype !== 'compact_boundary' && subtype !== 'compaction') return undefined;
  return {
    verb: 'compact',
    tokensBefore: num(payload, 'tokens_before'),
    tokensAfter: num(payload, 'tokens_after'),
    summary: str(payload, 'summary'),
  };
}

/// Whether a `system` event is one of the boundary notices above. feedLens
/// needs this to keep the divider out of the verbose-only tier: a compaction
/// is structure, not the lifecycle chatter the rest of `system` is.
export function isContextBoundarySystem(kind: string, payload: Entity): boolean {
  return kind === 'system' && contextDivider(kind, payload) !== undefined;
}

/// `4312` → `4.3s`. Raw milliseconds are cognitive load in a footer; mobile
/// formats the same field the same way (`event_card.dart:_fmtDuration`) so a
/// turn reads identically on both clients.
export function fmtDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '';
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const s = Math.floor(ms / 1000);
  if (s < 60) {
    const tenths = Math.floor((ms % 1000) / 100);
    return tenths === 0 ? `${s}s` : `${s}.${tenths}s`;
  }
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s`;
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
}

/// `0.0123` → `$0.0123`, `1.5` → `$1.50`. Sub-dollar turns are the common case
/// and rounding them to cents would print `$0.00` for most of them.
export function fmtCost(usd: number): string {
  if (!Number.isFinite(usd)) return '';
  return `$${usd.toFixed(usd >= 1 ? 2 : 4)}`;
}
