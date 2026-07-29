import { useState } from 'react';

import { num, obj, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { readDigestIssues, type IssueClass } from '../state/digestIssues';

/// Digest dashboard (parity Phase 1b) — the web analogue of the mobile
/// RunReportCard (lib/widgets/run_report_card.dart). Reads the structured
/// digest map from `GET …/agents/{id}/digest` (wire shape assembled by
/// `digestJSON`, hub/internal/server/handlers_agent_digest.go): outcome +
/// stat tiles + per-model token breakdown + an errors list built from the
/// folded `errors[*].sample_*` (no extra fetch).
///
/// Plus the **Issues drawer** (transcript P5 A3): the digest's structural
/// findings — the failures nothing in the run reported — grouped severity-first,
/// each row seeking the transcript to the event that caused it. Errors and
/// Issues stay two lists because the hub keeps two taxonomies; merging them here
/// would double-count a failure that appears in both readings.
///
/// Strings moved to the i18n dicts in the same change. Half-translating a file
/// is worse than either state, and the surrounding surfaces (95 of 125) already
/// go through `useT`.

function fmtMs(ms: number | undefined): string {
  if (ms === undefined || ms <= 0) return '—';
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  const m = Math.floor(ms / 60_000);
  const s = Math.round((ms % 60_000) / 1000);
  return `${m}m ${s}s`;
}

function fmtCost(usd: number | undefined): string {
  if (usd === undefined || usd === 0) return '$0';
  return usd >= 1 ? `$${usd.toFixed(2)}` : `$${usd.toFixed(4)}`;
}

function fmtTokens(n: number | undefined): string {
  if (n === undefined || n === 0) return '0';
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`;
  return String(n);
}

function outcomeClass(outcome: string): string {
  switch (outcome) {
    case 'success':
      return 'ok';
    case 'failed':
    case 'crashed':
    case 'error':
      return 'err';
    case 'running':
    case 'in_progress':
      return 'live';
    default:
      return 'muted';
  }
}

function StatTile({
  label,
  value,
  hint,
  hintClass,
  onClick,
  title,
}: {
  label: string;
  value: string;
  hint?: string;
  hintClass?: string;
  onClick?: () => void;
  title?: string;
}): JSX.Element {
  const body = (
    <>
      <div className="stat-value">{value}</div>
      <div className="stat-label">{label}</div>
      {hint !== undefined && <div className={`stat-hint ${hintClass ?? ''}`.trim()}>{hint}</div>}
    </>
  );
  if (onClick === undefined) return <div className="stat-tile">{body}</div>;
  return (
    <button type="button" className="stat-tile stat-tile-action" onClick={onClick} title={title}>
      {body}
    </button>
  );
}

/// One issue class: a collapsed severity row that expands to its samples.
function IssueGroup({
  group,
  onSeek,
  t,
}: {
  group: IssueClass;
  onSeek?: (coord: number) => void;
  t: (k: string) => string;
}): JSX.Element {
  const [open, setOpen] = useState(false);
  // A class the dictionary doesn't know yet falls back to its wire key rather
  // than rendering blank, so a rule added hub-side stays readable on an old app.
  const key = `issue.class.${group.cls}`;
  const label = t(key);
  return (
    <li className={`rr-issue sev-${group.severity}`}>
      <button type="button" className="rr-issue-head" onClick={() => setOpen((v) => !v)} aria-expanded={open}>
        <span className="rr-issue-sev">{t(`issue.sev.${group.severity}`)}</span>
        <span className="rr-issue-class">{label === key ? group.cls : label}</span>
        <span className="rr-issue-count">×{group.count}</span>
      </button>
      {open && (
        <ul className="rr-issue-samples">
          {group.samples.map((s, i) => {
            const seekable = onSeek !== undefined && s.coord !== undefined;
            return (
              <li key={`${group.cls}-${i}`}>
                <button
                  type="button"
                  className="rr-issue-sample"
                  disabled={!seekable}
                  onClick={() => seekable && onSeek(s.coord as number)}
                  title={seekable ? t('issue.seek') : undefined}
                >
                  <span className="rr-issue-label">{s.label ?? `#${s.coord ?? '?'}`}</span>
                  {s.ts !== undefined && <span className="muted"> · {s.ts}</span>}
                </button>
              </li>
            );
          })}
          {/* No silent caps: a partial sample list says so. */}
          {group.capped && (
            <li className="muted rr-issue-capped">
              {t('issue.capped')
                .replace('{shown}', String(group.samples.length))
                .replace('{total}', String(group.count))}
            </li>
          )}
        </ul>
      )}
    </li>
  );
}

export function RunReport({
  digest,
  stale,
  onSeek,
}: {
  digest: Entity;
  stale?: boolean;
  /// Seeks the transcript to a coordinate (session_ordinal, else seq). Absent
  /// where there is no transcript to seek in (the Sessions panel), which makes
  /// the sample rows inert rather than broken.
  onSeek?: (coord: number) => void;
}): JSX.Element {
  const t = useT();
  const [issuesOpen, setIssuesOpen] = useState(false);
  const outcome = str(digest, 'outcome') ?? 'unknown';
  const events = num(digest, 'event_count') ?? 0;
  const turns = num(digest, 'turn_count') ?? 0;
  const toolTotal = num(digest, 'tool_total') ?? 0;
  const toolFailed = num(digest, 'tool_failed') ?? 0;
  const errorCount = num(digest, 'error_count') ?? 0;
  const latency = obj(digest, 'latency');
  const byModel = obj(digest, 'by_model');
  const errors = obj(digest, 'errors');
  const issues = readDigestIssues(digest);
  const p95 = latency ? num(latency, 'p95_ms') : undefined;
  const lastTs = str(digest, 'last_ts');

  const models = byModel ? Object.entries(byModel) : [];
  const errClasses = errors ? Object.entries(errors) : [];

  return (
    <div className="run-report">
      <div className="rr-head">
        <span className={`rr-outcome ${outcomeClass(outcome)}`}>{outcome}</span>
        <span className="muted">
          {t('rr.turnsEvents').replace('{turns}', String(turns)).replace('{events}', String(events))}
        </span>
      </div>

      <div className="stat-grid">
        <StatTile label={t('rr.events')} value={String(events)} />
        <StatTile label={t('rr.turns')} value={String(turns)} />
        <StatTile label={t('rr.active')} value={fmtMs(num(digest, 'active_ms'))} />
        <StatTile label={t('rr.elapsed')} value={fmtMs(num(digest, 'duration_ms'))} />
        <StatTile label={t('rr.cost')} value={fmtCost(num(digest, 'cost_usd'))} />
        <StatTile
          label={t('rr.tools')}
          value={String(toolTotal)}
          hint={toolFailed > 0 ? t('rr.toolsFailed').replace('{n}', String(toolFailed)) : undefined}
        />
        <StatTile label={t('rr.errors')} value={String(errorCount)} />
        {/* Hidden at zero — a "0 issues" tile is noise on the overwhelming
            majority of runs, and its absence on a pre-v7 hub is honest: that
            hub never ran the checks, so it cannot report a clean run. */}
        {issues.total > 0 && (
          <StatTile
            label={t('rr.issues')}
            value={String(issues.total)}
            hint={issues.worst !== undefined ? t(`issue.sev.${issues.worst}`) : undefined}
            hintClass={`sev-${issues.worst ?? 'info'}`}
            onClick={() => setIssuesOpen((v) => !v)}
            title={t('issue.openDrawer')}
          />
        )}
        <StatTile
          label={t('rr.latency')}
          value={fmtMs(latency ? num(latency, 'p50_ms') : undefined)}
          hint={p95 !== undefined && p95 > 0 ? t('rr.p95').replace('{v}', fmtMs(p95)) : undefined}
        />
      </div>

      {issuesOpen && issues.total > 0 && (
        <div className="rr-section rr-issues-drawer">
          <h4>{t('rr.issues')}</h4>
          <p className="muted rr-issues-note">{t('issue.explainer')}</p>
          <ul className="rr-issues">
            {issues.classes.map((g) => (
              <IssueGroup key={g.cls} group={g} onSeek={onSeek} t={t} />
            ))}
          </ul>
        </div>
      )}

      {models.length > 0 && (
        <div className="rr-section">
          <h4>{t('rr.byModel')}</h4>
          <table className="rr-table">
            <thead>
              <tr>
                <th>{t('rr.model')}</th>
                <th>{t('rr.in')}</th>
                <th>{t('rr.out')}</th>
                <th>{t('rr.cache')}</th>
                <th>{t('rr.cost')}</th>
              </tr>
            </thead>
            <tbody>
              {models.map(([model, raw]) => {
                const m = (raw !== null && typeof raw === 'object' ? raw : {}) as Entity;
                return (
                  <tr key={model}>
                    <td className="mono">{model}</td>
                    <td>{fmtTokens(num(m, 'in'))}</td>
                    <td>{fmtTokens(num(m, 'out'))}</td>
                    <td>{fmtTokens(num(m, 'cache_read'))}</td>
                    <td>{fmtCost(num(m, 'cost_usd'))}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {errClasses.length > 0 && (
        <div className="rr-section">
          <h4>{t('rr.errors')}</h4>
          <ul className="rr-errors">
            {errClasses.map(([cls, raw]) => {
              const e = (raw !== null && typeof raw === 'object' ? raw : {}) as Entity;
              const labels = Array.isArray(e['sample_labels']) ? (e['sample_labels'] as unknown[]) : [];
              const sample = labels.filter((l) => typeof l === 'string' && l !== '').slice(0, 3).join(', ');
              return (
                <li key={cls}>
                  <span className="rr-err-class">{cls}</span>
                  <span className="rr-err-count">×{num(e, 'count') ?? 0}</span>
                  {sample !== '' && <span className="muted"> — {sample}</span>}
                </li>
              );
            })}
          </ul>
        </div>
      )}

      <div className="rr-foot muted">
        {lastTs !== undefined && `${t('rr.asOf').replace('{ts}', lastTs)} · `}
        {stale === true ? t('rr.cached') : t('rr.liveState')}
      </div>
    </div>
  );
}
