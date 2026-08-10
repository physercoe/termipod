/// Reader for the hub's `pane_explain` record (pane-state-manifests P4).
///
/// Hub entities arrive untyped (`Entity = Record<string, unknown>`), so every
/// field that reaches the card crosses one unchecked boundary. This module is
/// that boundary, and it is pure so the crossing is testable without a DOM:
/// the card below renders what this returns and asks no further questions.
///
/// The shape is `hub/internal/panestate.ExplainResult`. Its Go side pins its
/// own JSON key set with a test, so the two agree by assertion rather than by
/// hope — but the names still live in two languages, and this file is where a
/// rename would surface.

export type PaneState = 'idle' | 'working' | 'blocked' | 'unknown';

export interface PaneRuleEvidence {
  /// What the rule was looking for. Empty arrays are dropped by the encoder,
  /// so an absent one means "this rule used none of that matcher".
  contains: string[];
  regex: string[];
  lineRegex: string[];
  /// Nested-gate counts. A rule whose logic is entirely nested shows no
  /// matchers at all, and these are what stop that reading as "no conditions".
  allCount: number;
  anyCount: number;
  notCount: number;
  /// What the rule was looking AT: the size of its region, and a bounded
  /// preview. The preview is the whole reason an unmatched rule is useful —
  /// without it, "did not match" is an unfalsifiable claim.
  regionBytes: number;
  regionPreview: string;
}

export interface PaneRule {
  id: string;
  priority: number;
  region: string;
  state: PaneState;
  matched: boolean;
  evidence: PaneRuleEvidence;
}

export interface PaneExplainView {
  /// 'live' — a pane was captured just now. 'supplied' — the caller handed
  /// over the text. Never inferred: a card that cannot say which it is would
  /// let a hypothetical read as a fact about a running agent.
  mode: 'live' | 'supplied';
  agentId: string;
  paneId: string;
  hostId: string;
  family: string;
  screenBytes: number;
  screenLines: number;
  oscTitle: string;

  manifestId: string;
  manifestVersion: string;
  /// 'vendor' (byte-exact from herdr) or 'overlay' (ours). A rule that
  /// surprises someone is a different conversation depending on which.
  source: string;
  state: PaneState;
  matchedRuleId: string;
  fallbackReason: string;
  visibleIdle: boolean;
  visibleBlocker: boolean;
  visibleWorking: boolean;
  skipStateUpdate: boolean;
  skippedUpdateReason: string;
  rules: PaneRule[];
}

function obj(v: unknown): Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : {};
}

function str(o: Record<string, unknown>, k: string): string {
  const v = o[k];
  return typeof v === 'string' ? v : '';
}

function num(o: Record<string, unknown>, k: string): number {
  const v = o[k];
  return typeof v === 'number' && Number.isFinite(v) ? v : 0;
}

function bool(o: Record<string, unknown>, k: string): boolean {
  return o[k] === true;
}

function strList(o: Record<string, unknown>, k: string): string[] {
  const v = o[k];
  return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : [];
}

const STATES: readonly string[] = ['idle', 'working', 'blocked', 'unknown'];

/// An unrecognised state reads as `unknown`, which is the manifest schema's own
/// name for "no opinion" — not as the string itself. A card that printed a
/// state it cannot style would claim more than it knows.
function state(o: Record<string, unknown>, k: string): PaneState {
  const v = str(o, k);
  return (STATES.includes(v) ? v : 'unknown') as PaneState;
}

/// Parse one record. Returns null only when the payload is not a record at all
/// — every other shortfall degrades to an empty field, because a card that
/// renders nine of ten fields is more useful than one that renders none.
export function readPaneExplain(raw: unknown): PaneExplainView | null {
  const o = obj(raw);
  if (Object.keys(o).length === 0) return null;
  const ex = obj(o['explain']);
  // The evaluation is the payload. A body without one is an error shape (the
  // hub's `{error, family, detail}`), and the caller renders that instead.
  if (Object.keys(ex).length === 0) return null;

  const matched = obj(ex['matched_rule']);
  const rawRules = Array.isArray(ex['rules']) ? ex['rules'] : [];

  return {
    mode: str(o, 'mode') === 'live' ? 'live' : 'supplied',
    agentId: str(o, 'agent_id'),
    paneId: str(o, 'pane_id'),
    hostId: str(o, 'host_id'),
    family: str(o, 'family'),
    screenBytes: num(o, 'screen_bytes'),
    screenLines: num(o, 'screen_lines'),
    oscTitle: str(o, 'osc_title'),

    manifestId: str(ex, 'manifest_id'),
    manifestVersion: str(ex, 'manifest_version'),
    source: str(ex, 'source'),
    state: state(ex, 'state'),
    matchedRuleId: str(matched, 'id'),
    fallbackReason: str(ex, 'fallback_reason'),
    visibleIdle: bool(ex, 'visible_idle'),
    visibleBlocker: bool(ex, 'visible_blocker'),
    visibleWorking: bool(ex, 'visible_working'),
    skipStateUpdate: bool(ex, 'skip_state_update'),
    skippedUpdateReason: str(ex, 'skipped_update_reason'),
    rules: rawRules.map((r) => {
      const ro = obj(r);
      const ev = obj(ro['evidence']);
      return {
        id: str(ro, 'id'),
        priority: num(ro, 'priority'),
        region: str(ro, 'region'),
        state: state(ro, 'state'),
        matched: bool(ro, 'matched'),
        evidence: {
          contains: strList(ev, 'contains'),
          regex: strList(ev, 'regex'),
          lineRegex: strList(ev, 'line_regex'),
          allCount: num(ev, 'all_count'),
          anyCount: num(ev, 'any_count'),
          notCount: num(ev, 'not_count'),
          regionBytes: num(ev, 'region_bytes'),
          regionPreview: str(ev, 'region_preview'),
        },
      };
    }),
  };
}

/// The hub's refusal shapes, for the two a caller can act on: an unmapped
/// family names the engine nobody wrote rules for, and everything else is a
/// message. Returns null when the body is not an error.
export function readPaneExplainError(raw: unknown): { code: string; family: string; detail: string } | null {
  const o = obj(raw);
  const code = str(o, 'error');
  if (code === '') return null;
  return { code, family: str(o, 'family'), detail: str(o, 'detail') };
}

/// Rules sorted the way a reader wants them: the winner first, then the rest
/// by descending priority, ties by file order.
///
/// Priority order is not cosmetic — it IS the manifest's semantics (highest
/// priority wins, ties to the earliest rule), so a list sorted any other way
/// would misrepresent why the winner won. `sort` is stable in every engine
/// this ships to, which is what preserves the file-order tie-break.
export function orderedRules(view: PaneExplainView): PaneRule[] {
  const out = [...view.rules];
  out.sort((a, b) => {
    const aw = a.id === view.matchedRuleId && view.matchedRuleId !== '';
    const bw = b.id === view.matchedRuleId && view.matchedRuleId !== '';
    if (aw !== bw) return aw ? -1 : 1;
    return b.priority - a.priority;
  });
  return out;
}

/// One line summarising what a rule was looking for, for the collapsed row.
/// Empty when the rule's logic is entirely nested — the counts say so instead,
/// and claiming "no conditions" would be false.
export function matcherSummary(e: PaneRuleEvidence): string {
  const parts: string[] = [];
  for (const c of e.contains) parts.push(`contains ${JSON.stringify(c)}`);
  for (const r of e.regex) parts.push(`regex /${r}/`);
  for (const r of e.lineRegex) parts.push(`line /${r}/`);
  const nested: string[] = [];
  if (e.allCount > 0) nested.push(`all×${e.allCount}`);
  if (e.anyCount > 0) nested.push(`any×${e.anyCount}`);
  if (e.notCount > 0) nested.push(`not×${e.notCount}`);
  if (nested.length > 0) parts.push(nested.join(' '));
  return parts.join(' · ');
}
