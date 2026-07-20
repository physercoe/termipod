import { useMemo, useState } from 'react';
import { useQueries, useQuery } from '@tanstack/react-query';
import { useProjects } from '../hub/queries';
import { num, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { parsePoints } from '../ui/Sparkline';
import { ChartView, CHART_PALETTE, type ChartSeries } from '../ui/ChartView';
import { WorkbenchSurface } from '../ui/WorkbenchSurface';

/// J5 — Compare many runs. The headline BUILD from `research-tooling-landscape.md`
/// §3.3: no open tool exports a reusable run-comparison component, but the data
/// already lives in the hub (run digest + `/metrics`). This is the first cut of
/// the comparison wall — pick a project, multi-select its runs, and overlay each
/// metric's curve across the selected runs with a final-value summary table. It
/// is intrinsically wide-screen (the job the phone can't do). Next rounds add the
/// config-diff panel and the optuna-dashboard sweep EMBED.

// Run swatches share the chart renderer's palette (single source, #322) so a
// run's swatch always matches its overlay curve.
const SWATCHES = CHART_PALETTE;

function runLabel(r: Entity): string {
  const id = str(r, 'id') ?? '';
  return id.length > 10 ? `${id.slice(0, 8)}…` : id || '—';
}

export function CompareSurface(): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const projectsQ = useProjects();
  const projects = projectsQ.data ?? [];
  const [projectId, setProjectId] = useState('');
  const [selected, setSelected] = useState<string[]>([]);

  const effectiveProject = projectId !== '' ? projectId : str(projects[0] ?? {}, 'id') ?? '';

  const runsQ = useQuery({
    queryKey: ['runs', effectiveProject],
    enabled: client !== null && effectiveProject !== '',
    refetchInterval: 10000,
    queryFn: () => client!.listRuns(effectiveProject),
  });
  const runs = runsQ.data ?? [];

  // One metrics query per selected run — live-polled so a training curve grows
  // in place. useQueries keeps the array aligned with `selected`.
  const metricQs = useQueries({
    queries: selected.map((id) => ({
      queryKey: ['run-metrics', id],
      enabled: client !== null,
      refetchInterval: 8000,
      queryFn: () => client!.getRunMetrics(id),
    })),
  });

  function toggle(id: string): void {
    setSelected((cur) => (cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id]));
  }

  // Build: metricName -> (runId -> { last, series }). The union of metric names
  // across the selected runs drives one overlay chart each.
  const byMetric = useMemo(() => {
    const map = new Map<string, Map<string, { last: number | undefined; series: ChartSeries }>>();
    selected.forEach((runId, i) => {
      const rows = metricQs[i]?.data ?? [];
      for (const row of rows) {
        const name = str(row, 'name');
        if (name === undefined) continue;
        const pts = parsePoints(row['points']).map((p, idx) => ({ x: p.step ?? idx, y: p.value ?? 0 }));
        if (pts.length === 0) continue;
        if (!map.has(name)) map.set(name, new Map());
        map.get(name)!.set(runId, {
          last: num(row, 'last_value'),
          series: { name: runLabel(runs.find((r) => str(r, 'id') === runId) ?? {}), points: pts },
        });
      }
    });
    return map;
  }, [selected, metricQs, runs]);

  const metricNames = [...byMetric.keys()].sort();
  const anyLoading = metricQs.some((q) => q.isLoading);

  return (
    <WorkbenchSurface
      job="compare"
      actions={
        <>
          <select
            className="surface-select"
            value={effectiveProject}
            onChange={(e) => {
              setProjectId(e.target.value);
              setSelected([]);
            }}
          >
            <option value="">{t('compare.pickProject')}</option>
            {projects.map((p) => {
              const id = str(p, 'id') ?? '';
              return (
                <option key={id} value={id}>
                  {str(p, 'name') ?? id}
                </option>
              );
            })}
          </select>
          <span className="surface-meta muted small">
            {t('compare.selected').replace('{n}', String(selected.length))}
          </span>
        </>
      }
    >
      <div className="compare-layout">
        <aside className="compare-runs">
          <div className="notes-head muted small">{t('compare.runs')}</div>
          {runsQ.isLoading && <div className="muted region-pad">{t('common.loading')}</div>}
          {!runsQ.isLoading && runs.length === 0 && <div className="muted region-pad">{t('compare.noRuns')}</div>}
          {runs.map((r, i) => {
            const id = str(r, 'id') ?? '';
            const on = selected.includes(id);
            const idx = selected.indexOf(id);
            return (
              <label key={id || i} className={`compare-run${on ? ' on' : ''}`}>
                <input type="checkbox" checked={on} onChange={() => toggle(id)} />
                {on && <span className="compare-swatch" style={{ background: SWATCHES[idx % SWATCHES.length] }} />}
                <span className="compare-run-id mono">{runLabel(r)}</span>
                <span className="spacer" />
                <span className="muted small">{str(r, 'status') ?? ''}</span>
              </label>
            );
          })}
        </aside>

        <div className="compare-wall scroll">
          {selected.length === 0 ? (
            <div className="muted region-pad">{t('compare.hint')}</div>
          ) : (
            <>
              <table className="compare-table">
                <thead>
                  <tr>
                    <th>{t('compare.metric')}</th>
                    {selected.map((id, i) => (
                      <th key={id}>
                        <span className="compare-swatch" style={{ background: SWATCHES[i % SWATCHES.length] }} />
                        {runLabel(runs.find((r) => str(r, 'id') === id) ?? {})}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {metricNames.map((name) => (
                    <tr key={name}>
                      <td className="compare-metric-name">{name}</td>
                      {selected.map((id) => {
                        const cell = byMetric.get(name)?.get(id);
                        return (
                          <td key={id} className="mono">
                            {cell?.last !== undefined ? cell.last : '—'}
                          </td>
                        );
                      })}
                    </tr>
                  ))}
                  {metricNames.length === 0 && (
                    <tr>
                      <td className="muted" colSpan={selected.length + 1}>
                        {anyLoading ? t('common.loading') : t('compare.noMetrics')}
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>

              <div className="compare-charts">
                {metricNames.map((name) => {
                  const series = selected
                    .map((id) => byMetric.get(name)?.get(id)?.series)
                    .filter((s): s is ChartSeries => s !== undefined);
                  if (series.length === 0) return null;
                  return (
                    <div key={name} className="compare-chart-card">
                      <div className="compare-chart-title">{name}</div>
                      <ChartView chart={{ series, categorical: false }} />
                    </div>
                  );
                })}
              </div>
            </>
          )}
        </div>
      </div>
    </WorkbenchSurface>
  );
}
