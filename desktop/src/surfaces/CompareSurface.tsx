import { useEffect, useMemo } from 'react';
import { useQueries, useQuery } from '@tanstack/react-query';
import { useProjects } from '../hub/queries';
import { num, str } from '../hub/types';
import { useT } from '../i18n';
import { deltaOf, deltaSign, flattenConfig, formatDelta, runMatchesFilter, type RunFacts } from '../state/compareRuns';
import { useCompareWall } from '../state/compareWall';
import { useSession } from '../state/session';
import { parsePoints } from '../ui/Sparkline';
import { ChartView, CHART_PALETTE, type ChartSeries } from '../ui/ChartView';
import { Icon } from '../ui/Icon';
import { WorkbenchSurface } from '../ui/WorkbenchSurface';

/// J5 — Compare many runs. The headline BUILD from `research-tooling-landscape.md`
/// §3.3: no open tool exports a reusable run-comparison component, but the data
/// already lives in the hub (run digest + `/metrics`). Pick a project,
/// multi-select its runs, and overlay each metric's curve across them with a
/// summary table. It is intrinsically wide-screen (the job the phone can't do).
///
/// A1 of `plans/desktop-compare-wall-and-decisions.md` moves the wall's state
/// out of this component and into `state/compareWall.ts` — one state, every
/// panel (§5.2), remembered per project — and adds the three §3.2 affordances
/// the first cut lacked: filter-as-you-type over id/status/config, a baseline
/// pin, and Δ-vs-baseline in every summary cell. A2 adds the extremes table and
/// the diff-only config comparer over the same state.

// Run swatches share the chart renderer's palette (single source, #322) so a
// run's swatch always matches its overlay curve — which is why the curve is now
// handed its colour EXPLICITLY (see `colorOf`): a run with no points for one
// metric drops out of that chart's series array, and colour-by-array-index
// would then shift every later run's curve onto someone else's swatch.
const SWATCHES = CHART_PALETTE;

function runLabel(id: string): string {
  return id.length > 10 ? `${id.slice(0, 8)}…` : id || '—';
}

export function CompareSurface(): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const projectsQ = useProjects();
  const projects = projectsQ.data ?? [];

  const projectId = useCompareWall((s) => s.projectId);
  const view = useCompareWall((s) => s.view);
  const setProject = useCompareWall((s) => s.setProject);
  const toggleRun = useCompareWall((s) => s.toggleRun);
  const toggleBaseline = useCompareWall((s) => s.toggleBaseline);
  const setFilter = useCompareWall((s) => s.setFilter);
  const { selected, baseline, filter } = view;

  // ONE project id, not a store id plus a locally-computed fallback. The wall's
  // remembered view is filed UNDER this id, so a surface-local "effective
  // project" would render project A's remembered runs beside project B's run
  // list — each half correct, the screen wrong. Resolving into the store keeps
  // a single answer to "which project is the wall reading".
  const firstProject = str(projects[0] ?? {}, 'id') ?? '';
  useEffect(() => {
    if (projects.length === 0) return;
    if (projects.some((p) => str(p, 'id') === projectId)) return;
    // Either nothing chosen yet, or the remembered project is gone from the team.
    setProject(firstProject);
  }, [projects, projectId, firstProject, setProject]);

  const runsQ = useQuery({
    queryKey: ['runs', projectId],
    enabled: client !== null && projectId !== '',
    refetchInterval: 10000,
    queryFn: () => client!.listRuns(projectId),
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

  // The rail's rows, narrowed to what the filter searches. `config_json` rides
  // the run list already, so filtering by a config key costs no extra request.
  const facts: RunFacts[] = useMemo(
    () =>
      runs.map((r) => ({
        id: str(r, 'id') ?? '',
        status: str(r, 'status') ?? '',
        config: flattenConfig(r['config_json']),
      })),
    [runs],
  );
  // The filter narrows the RAIL, never the wall: a run you selected and then
  // typed past stays on the wall, because hiding a curve as a side effect of
  // searching for another one would silently change the comparison.
  const shown = useMemo(() => facts.filter((f) => runMatchesFilter(f, filter)), [facts, filter]);

  // A run's colour comes from its place in the SELECTION, everywhere.
  const colorOf = (id: string): string => {
    const i = selected.indexOf(id);
    return SWATCHES[(i < 0 ? 0 : i) % SWATCHES.length];
  };

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
          series: { name: runLabel(runId), points: pts },
        });
      }
    });
    return map;
  }, [selected, metricQs]);

  const metricNames = [...byMetric.keys()].sort();
  const anyLoading = metricQs.some((q) => q.isLoading);

  return (
    <WorkbenchSurface
      job="compare"
      actions={
        <>
          <select className="surface-select" value={projectId} onChange={(e) => setProject(e.target.value)}>
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
          {/* Reuses the Inspect tree's filter-input styling (generic token-based
              input) rather than duplicating a near-identical rule. */}
          <input
            className="inspect-tree-filter"
            placeholder={t('compare.filter')}
            aria-label={t('compare.filter')}
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
          {runsQ.isLoading && <div className="muted region-pad">{t('common.loading')}</div>}
          {!runsQ.isLoading && runs.length === 0 && <div className="muted region-pad">{t('compare.noRuns')}</div>}
          {!runsQ.isLoading && runs.length > 0 && shown.length === 0 && (
            <div className="muted region-pad">{t('compare.noMatch')}</div>
          )}
          {shown.map((f, i) => {
            const on = selected.includes(f.id);
            const isBaseline = baseline === f.id;
            return (
              <label key={f.id || i} className={`compare-run${on ? ' on' : ''}`}>
                <input type="checkbox" checked={on} onChange={() => toggleRun(f.id)} />
                {on && <span className="compare-swatch" style={{ background: colorOf(f.id) }} />}
                <span className="compare-run-id mono">{runLabel(f.id)}</span>
                <span className="spacer" />
                <span className="muted small">{f.status}</span>
                {/* A button inside the label: per HTML, a click on interactive
                    content does not activate the label's control, so pinning a
                    baseline never toggles the checkbox underneath it. */}
                <button
                  type="button"
                  className={`compare-star${isBaseline ? ' on' : ''}`}
                  aria-pressed={isBaseline}
                  aria-label={isBaseline ? t('compare.unpinBaseline') : t('compare.pinBaseline')}
                  title={isBaseline ? t('compare.unpinBaseline') : t('compare.pinBaseline')}
                  onClick={(e) => {
                    e.stopPropagation();
                    toggleBaseline(f.id);
                  }}
                >
                  <Icon name="star" size={13} />
                </button>
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
                    {selected.map((id) => (
                      <th key={id}>
                        <span className="compare-swatch" style={{ background: colorOf(id) }} />
                        {runLabel(id)}
                        {baseline === id && <span className="compare-baseline-tag">{t('compare.baseline')}</span>}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {metricNames.map((name) => {
                    const row = byMetric.get(name);
                    const base = baseline !== null ? row?.get(baseline)?.last : undefined;
                    return (
                      <tr key={name}>
                        <td className="compare-metric-name">{name}</td>
                        {selected.map((id) => {
                          const cell = row?.get(id);
                          const delta = id === baseline ? null : deltaOf(cell?.last, base);
                          return (
                            <td key={id} className="mono">
                              {cell?.last !== undefined ? cell.last : '—'}
                              {delta !== null && (
                                <span className={`compare-delta ${deltaSign(delta)}`}>{formatDelta(delta)}</span>
                              )}
                            </td>
                          );
                        })}
                      </tr>
                    );
                  })}
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
                  const row = byMetric.get(name);
                  const series = selected
                    .map((id): ChartSeries | undefined => {
                      const cell = row?.get(id);
                      if (cell === undefined) return undefined;
                      return { ...cell.series, color: colorOf(id), dashed: id === baseline };
                    })
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
