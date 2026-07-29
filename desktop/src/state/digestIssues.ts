import type { Entity } from '../hub/types';

/// Reader for the digest's structural-issues aggregation (transcript P5 §2 —
/// hub/internal/server/digest_issues.go). The hub folds the findings once and
/// serves them to both clients; this turns the wire map into the shape the
/// Issues drawer renders, and nothing more.
///
/// Pure and i18n-free on purpose: class keys stay keys, the component resolves
/// the display strings. That keeps this unit-testable (node --test, which CI
/// does NOT run — see CLAUDE.md) and keeps the ordering + seek-anchor rules in
/// ONE place instead of re-derived at each call site. Both are the shape of bug
/// this whole wedge exists to catch.

export type IssueSeverity = 'error' | 'warning' | 'info';

/// Worst-first. Must match the hub's issueSeverityRank (digest_issues.go) and
/// mobile's ordering — same severity order on both clients is a review anchor
/// of the plan, this family having shipped four mobile↔desktop misses already.
const SEVERITY_RANK: Record<string, number> = { error: 3, warning: 2, info: 1 };

export interface IssueSample {
  /// The transcript coordinate to seek to: the dense session_ordinal when the
  /// agent has one, else the per-agent seq. Same rule the turns list uses
  /// (AgentTranscript's turn-row anchor), resolved here so a caller never picks
  /// the wrong one — seq collides across a resumed session's agents, ordinal
  /// does not (ADR-042).
  coord?: number;
  ts?: string;
  /// Short headline for the row — the failing tool's name, the stop reason, the
  /// kind.key that changed shape. Absent when the class label carries it all.
  label?: string;
}

export interface IssueClass {
  cls: string;
  severity: IssueSeverity;
  count: number;
  samples: IssueSample[];
  /// True when the sample list is a prefix of `count` findings (the hub caps
  /// them at maxDigestErrorSeqs). Surfaced in the UI — a truncated list must
  /// never read as a complete one.
  capped: boolean;
}

export interface IssueSummary {
  total: number;
  worst?: IssueSeverity;
  classes: IssueClass[];
}

function severityOf(v: unknown): IssueSeverity {
  return v === 'error' || v === 'warning' || v === 'info' ? v : 'warning';
}

function numAt(arr: unknown, i: number): number | undefined {
  if (!Array.isArray(arr)) return undefined;
  const v = arr[i];
  return typeof v === 'number' && Number.isFinite(v) ? v : undefined;
}

function strAt(arr: unknown, i: number): string | undefined {
  if (!Array.isArray(arr)) return undefined;
  const v = arr[i];
  return typeof v === 'string' && v !== '' ? v : undefined;
}

/// Builds the drawer model from a digest entity. A hub that predates the
/// aggregation has no `issues` key at all and yields an empty summary, so the
/// drawer simply never offers itself — rather than rendering a clean bill of
/// health for a hub that never ran the checks.
export function readDigestIssues(digest: Entity | undefined): IssueSummary {
  const raw = digest?.['issues'];
  if (raw === null || raw === undefined || typeof raw !== 'object' || Array.isArray(raw)) {
    return { total: 0, classes: [] };
  }
  const classes: IssueClass[] = [];
  for (const [cls, entry] of Object.entries(raw as Record<string, unknown>)) {
    if (entry === null || typeof entry !== 'object' || Array.isArray(entry)) continue;
    const e = entry as Entity;
    const count = typeof e['count'] === 'number' && Number.isFinite(e['count']) ? (e['count'] as number) : 0;
    if (count <= 0) continue;
    const seqs = e['sample_seqs'];
    const ords = e['sample_ordinals'];
    const n = Array.isArray(seqs) ? seqs.length : 0;
    const samples: IssueSample[] = [];
    for (let i = 0; i < n; i += 1) {
      const ord = numAt(ords, i);
      const seq = numAt(seqs, i);
      samples.push({
        coord: ord !== undefined && ord > 0 ? ord : seq,
        ts: strAt(e['sample_ts'], i),
        label: strAt(e['sample_labels'], i),
      });
    }
    classes.push({ cls, severity: severityOf(e['severity']), count, samples, capped: count > samples.length });
  }

  // Severity-first, then the loudest class, then alphabetical so the order is
  // stable across renders and matches mobile.
  classes.sort((a, b) => {
    const bySev = (SEVERITY_RANK[b.severity] ?? 0) - (SEVERITY_RANK[a.severity] ?? 0);
    if (bySev !== 0) return bySev;
    if (b.count !== a.count) return b.count - a.count;
    return a.cls < b.cls ? -1 : a.cls > b.cls ? 1 : 0;
  });

  const total = classes.reduce((sum, c) => sum + c.count, 0);
  // Prefer the hub's own rollup: it ranks with the server's table, so a severity
  // this client doesn't know yet still tints correctly. Fall back to the
  // computed worst for a hub that predates the field.
  const wire = digest?.['issue_worst_severity'];
  const worst =
    typeof wire === 'string' && wire !== ''
      ? severityOf(wire)
      : classes.length > 0
        ? classes[0].severity
        : undefined;
  return { total, worst, classes };
}
