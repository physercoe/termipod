import { num, obj, str, type Entity } from '../hub/types';

/// Digest dashboard (parity Phase 1b) — the web analogue of the mobile
/// RunReportCard (lib/widgets/run_report_card.dart). Reads the structured
/// digest map from `GET …/agents/{id}/digest` (wire shape assembled by
/// `digestJSON`, hub/internal/server/handlers_agent_digest.go:251): outcome +
/// stat tiles + per-model token breakdown + an errors list built from the
/// folded `errors[*].sample_*` (no extra fetch).

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

function StatTile({ label, value, hint }: { label: string; value: string; hint?: string }): JSX.Element {
  return (
    <div className="stat-tile">
      <div className="stat-value">{value}</div>
      <div className="stat-label">{label}</div>
      {hint !== undefined && <div className="stat-hint">{hint}</div>}
    </div>
  );
}

export function RunReport({ digest, stale }: { digest: Entity; stale?: boolean }): JSX.Element {
  const outcome = str(digest, 'outcome') ?? 'unknown';
  const events = num(digest, 'event_count') ?? 0;
  const turns = num(digest, 'turn_count') ?? 0;
  const toolTotal = num(digest, 'tool_total') ?? 0;
  const toolFailed = num(digest, 'tool_failed') ?? 0;
  const errorCount = num(digest, 'error_count') ?? 0;
  const latency = obj(digest, 'latency');
  const byModel = obj(digest, 'by_model');
  const errors = obj(digest, 'errors');

  const models = byModel ? Object.entries(byModel) : [];
  const errClasses = errors ? Object.entries(errors) : [];

  return (
    <div className="run-report">
      <div className="rr-head">
        <span className={`rr-outcome ${outcomeClass(outcome)}`}>{outcome}</span>
        <span className="muted">
          {turns} turns · {events} events
        </span>
      </div>

      <div className="stat-grid">
        <StatTile label="Events" value={String(events)} />
        <StatTile label="Turns" value={String(turns)} />
        <StatTile label="Active" value={fmtMs(num(digest, 'active_ms'))} />
        <StatTile label="Elapsed" value={fmtMs(num(digest, 'duration_ms'))} />
        <StatTile label="Cost" value={fmtCost(num(digest, 'cost_usd'))} />
        <StatTile label="Tools" value={String(toolTotal)} hint={toolFailed > 0 ? `${toolFailed} failed` : undefined} />
        <StatTile label="Errors" value={String(errorCount)} />
        <StatTile
          label="Latency"
          value={fmtMs(latency ? num(latency, 'p50_ms') : undefined)}
          hint={latency && num(latency, 'p95_ms') ? `p95 ${fmtMs(num(latency, 'p95_ms'))}` : undefined}
        />
      </div>

      {models.length > 0 && (
        <div className="rr-section">
          <h4>By model</h4>
          <table className="rr-table">
            <thead>
              <tr>
                <th>Model</th>
                <th>In</th>
                <th>Out</th>
                <th>Cache</th>
                <th>Cost</th>
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
          <h4>Errors</h4>
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
        {str(digest, 'last_ts') !== undefined && `as of ${str(digest, 'last_ts')} · `}
        {stale === true ? 'cached' : 'live'}
      </div>
    </div>
  );
}
