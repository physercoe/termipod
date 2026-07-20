import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useProjects } from '../hub/queries';
import { num, obj, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { Modal } from '../ui/Modal';

/// Insights surface (parity Phase 4, ADR-038/039/041). Reads the hub's insights
/// aggregator (`GET /v1/insights`, token-scoped) for a chosen scope — the team
/// as a whole, or one project — and renders spend / latency / errors /
/// concurrency / tool rollups plus by-engine / by-model / by-agent breakdowns.
/// Read-only analytics; the wire shape is `insightsResponse`
/// (handlers_insights.go:137).

function fmtTokens(n: number | undefined): string {
  if (n === undefined || n === 0) return '0';
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`;
  return String(n);
}
function fmtMs(ms: number | undefined): string {
  if (ms === undefined || ms <= 0) return '—';
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  return `${Math.floor(ms / 60_000)}m ${Math.round((ms % 60_000) / 1000)}s`;
}
function pct(n: number | undefined): string {
  return n === undefined ? '—' : `${Math.round(n * 100)}%`;
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

export function InsightsPanel({ onClose }: { onClose: () => void }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const projectsQ = useProjects();
  const projects = projectsQ.data ?? [];
  // scope: '' = whole team; otherwise a project id.
  const [scopeProject, setScopeProject] = useState('');

  const teamId = client?.transport.teamId ?? '';
  const q = useQuery({
    queryKey: ['insights', teamId, scopeProject],
    enabled: client !== null,
    refetchInterval: 30000,
    queryFn: () =>
      client!.getInsights(scopeProject !== '' ? { project_id: scopeProject } : { team_id: teamId }),
  });

  const d = q.data ?? {};
  const spend = obj(d, 'spend') ?? {};
  const latency = obj(d, 'latency') ?? {};
  const errors = obj(d, 'errors') ?? {};
  const concurrency = obj(d, 'concurrency') ?? {};
  const tools = obj(d, 'tools') ?? {};
  const byEngine = obj(d, 'by_engine');
  const byModel = obj(d, 'by_model');
  const byAgent = Array.isArray(d['by_agent']) ? (d['by_agent'] as Entity[]) : [];

  return (
    <Modal onClose={onClose} className="sessions-panel" ariaLabel={t('insights.title')}>
        <div className="admin-tabs">
          <strong>{t('insights.title')}</strong>
          <select value={scopeProject} onChange={(e) => setScopeProject(e.target.value)}>
            <option value="">{t('insights.scopeTeam')}</option>
            {projects.map((p) => {
              const id = str(p, 'id') ?? '';
              return (
                <option key={id} value={id}>
                  {str(p, 'name') ?? id}
                </option>
              );
            })}
          </select>
          <span className="spacer" />
          <button onClick={onClose}>{t('admin.close')}</button>
        </div>
        <div className="region-pad scroll">
          {q.isLoading && <div className="muted">{t('common.loading')}</div>}
          {q.isError && <div className="error">{(q.error as Error).message}</div>}
          {q.data !== undefined && (
            <>
              <div className="stat-grid">
                <StatTile
                  label={t('insights.tokensIn')}
                  value={fmtTokens(num(spend, 'tokens_in'))}
                  hint={`${t('insights.cacheRead')} ${fmtTokens(num(spend, 'cache_read'))}`}
                />
                <StatTile label={t('insights.tokensOut')} value={fmtTokens(num(spend, 'tokens_out'))} />
                <StatTile
                  label={t('insights.turnLatency')}
                  value={fmtMs(num(latency, 'turn_p50_ms'))}
                  hint={`p95 ${fmtMs(num(latency, 'turn_p95_ms'))}`}
                />
                <StatTile
                  label={t('insights.activeAgents')}
                  value={String(num(concurrency, 'active_agents') ?? 0)}
                  hint={`${num(concurrency, 'open_sessions') ?? 0} ${t('insights.openSessions')}`}
                />
                <StatTile
                  label={t('insights.errors')}
                  value={String(num(errors, 'total_errors') ?? 0)}
                  hint={`${num(errors, 'failed_turns') ?? 0} ${t('insights.failedTurns')}`}
                />
                <StatTile
                  label={t('insights.openAttention')}
                  value={String(num(errors, 'open_attention') ?? 0)}
                />
                <StatTile
                  label={t('insights.toolCalls')}
                  value={String(num(tools, 'tool_calls') ?? 0)}
                  hint={`${(num(tools, 'tools_per_turn') ?? 0).toFixed(1)}/turn`}
                />
                <StatTile
                  label={t('insights.approvalRate')}
                  value={pct(num(tools, 'approval_rate'))}
                  hint={`${num(tools, 'approvals_approved') ?? 0}/${num(tools, 'approvals_total') ?? 0}`}
                />
              </div>

              {byModel && Object.keys(byModel).length > 0 && (
                <div className="rr-section">
                  <h4>{t('insights.byModel')}</h4>
                  <table className="rr-table">
                    <thead>
                      <tr>
                        <th>{t('insights.model')}</th>
                        <th>{t('insights.tokensIn')}</th>
                        <th>{t('insights.tokensOut')}</th>
                        <th>{t('insights.cacheRead')}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {Object.entries(byModel).map(([model, raw]) => {
                        const m = (raw !== null && typeof raw === 'object' ? raw : {}) as Entity;
                        return (
                          <tr key={model}>
                            <td className="mono">{model}</td>
                            <td>{fmtTokens(num(m, 'tokens_in'))}</td>
                            <td>{fmtTokens(num(m, 'tokens_out'))}</td>
                            <td>{fmtTokens(num(m, 'cache_read'))}</td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>
              )}

              {byEngine && Object.keys(byEngine).length > 0 && (
                <div className="rr-section">
                  <h4>{t('insights.byEngine')}</h4>
                  <table className="rr-table">
                    <thead>
                      <tr>
                        <th>{t('insights.engine')}</th>
                        <th>{t('insights.tokensIn')}</th>
                        <th>{t('insights.tokensOut')}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {Object.entries(byEngine).map(([engine, raw]) => {
                        const m = (raw !== null && typeof raw === 'object' ? raw : {}) as Entity;
                        return (
                          <tr key={engine}>
                            <td className="mono">{engine}</td>
                            <td>{fmtTokens(num(m, 'tokens_in'))}</td>
                            <td>{fmtTokens(num(m, 'tokens_out'))}</td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>
              )}

              {byAgent.length > 0 && (
                <div className="rr-section">
                  <h4>{t('insights.byAgent')}</h4>
                  <table className="rr-table">
                    <thead>
                      <tr>
                        <th>{t('insights.agent')}</th>
                        <th>{t('insights.tokensIn')}</th>
                        <th>{t('insights.tokensOut')}</th>
                        <th>{t('insights.errors')}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {byAgent.map((a, i) => (
                        <tr key={str(a, 'agent_id') ?? String(i)}>
                          <td className="mono">{str(a, 'handle') ?? str(a, 'agent_id') ?? '—'}</td>
                          <td>{fmtTokens(num(a, 'tokens_in'))}</td>
                          <td>{fmtTokens(num(a, 'tokens_out'))}</td>
                          <td>{num(a, 'total_errors') ?? num(a, 'errors') ?? 0}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </>
          )}
        </div>
    </Modal>
  );
}
