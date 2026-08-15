import { useEffect, useMemo, useState } from 'react';
import { useQueries, useQuery } from '@tanstack/react-query';
import { useProjects } from '../hub/queries';
import { num, str } from '../hub/types';
import { useT } from '../i18n';
import {
  aggregateCurves,
  configDiffRows,
  deltaOf,
  deltaSign,
  emaSmooth,
  extremesOf,
  flattenConfig,
  formatDelta,
  groupRunsBy,
  mergeConfigSources,
  runMatchesFilter,
  SEED_GROUP_KEY,
  toRelativeX,
  type CurvePoint,
  type RunFacts,
} from '../state/compareRuns';
import { MAX_SMOOTHING, useCompareWall } from '../state/compareWall';
import { useSession } from '../state/session';
import { parsePoints } from '../ui/Sparkline';
import { ChartView, CHART_PALETTE, type ChartBand, type ChartSeries } from '../ui/ChartView';
import { Icon } from '../ui/Icon';
import { HeaderPaneToggle, WorkbenchSurface } from '../ui/WorkbenchSurface';

/// J5 — Compare many runs. The headline BUILD from `research-tooling-landscape.md`
/// §3.3: no open tool exports a reusable run-comparison component, but the data
/// already lives in the hub (run digest + `/metrics`). Pick a project,
/// multi-select its runs, and overlay each metric's curve across them with a
/// summary table. It is intrinsically wide-screen (the job the phone can't do).
///
/// A1 of `plans/desktop-compare-wall-and-decisions.md` moved the wall's state
/// into `state/compareWall.ts` — one state, every panel (§5.2), remembered per
/// project — and added filter / baseline pin / Δ columns. A2 adds the rest of
/// §3.2: per-run extremes in every cell, the diff-only config comparer, and
/// EMA smoothing with a step/relative x-switch; A3 groups runs by a config key
/// (or by seed) into one mean curve with a ±band. Every derivation is a pure
/// function in `state/compareRuns.ts`, because a comparison wall that quietly
/// mis-computes does not look broken — it looks like a result.

// Run swatches share the chart renderer's palette (single source, #322) so a
// run's swatch always matches its overlay curve — which is why the curve is now
// handed its colour EXPLICITLY (see `colorOf`): a run with no points for one
// metric drops out of that chart's series array, and colour-by-array-index
// would then shift every later run's curve onto someone else's swatch.
const SWATCHES = CHART_PALETTE;

/// How faint the raw curve sits behind its smoothed line (§3.2's ghost).
const RAW_GHOST_OPACITY = 0.28;
/// A group's member runs draw thin and faint under its bold mean (§3.2).
const MEMBER_WIDTH = 0.9;
const MEMBER_OPACITY = 0.45;
const GROUP_MEAN_WIDTH = 2.4;

function runLabel(id: string): string {
  return id.length > 10 ? `${id.slice(0, 8)}…` : id || '—';
}

function fmtValue(v: number | undefined): string {
  return v === undefined ? '—' : String(v);
}

interface MetricCell {
  last: number | undefined;
  min: number | undefined;
  max: number | undefined;
  points: CurvePoint[];
}

export function CompareSurface(): JSX.Element {
  const t = useT();
  const [runsOpen, setRunsOpen] = useState(() => localStorage.getItem('termipod.compare.runsOpen') !== '0');
  const client = useSession((s) => s.client);
  const projectsQ = useProjects();
  const projects = projectsQ.data ?? [];

  const projectId = useCompareWall((s) => s.projectId);
  const view = useCompareWall((s) => s.view);
  const setProject = useCompareWall((s) => s.setProject);
  const toggleRun = useCompareWall((s) => s.toggleRun);
  const toggleBaseline = useCompareWall((s) => s.toggleBaseline);
  const setFilter = useCompareWall((s) => s.setFilter);
  const setSmoothing = useCompareWall((s) => s.setSmoothing);
  const setXAxis = useCompareWall((s) => s.setXAxis);
  const setShowIdentical = useCompareWall((s) => s.setShowIdentical);
  const setGroupBy = useCompareWall((s) => s.setGroupBy);
  const { selected, baseline, filter, smoothing, xAxis, showIdentical, groupBy } = view;

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

  // …and one for the LOGGED config digest, which the comparer unions with the
  // config registered at creation. Polled slowly: a config is written once
  // near the start of a run, but "once" can be after the wall is already open.
  const configQs = useQueries({
    queries: selected.map((id) => ({
      queryKey: ['run-config', id],
      enabled: client !== null,
      refetchInterval: 60000,
      queryFn: () => client!.getRunConfig(id),
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
  // `seed` is a run COLUMN, not a config leaf, and it is the first thing anyone
  // groups by ("same config, five seeds") — so it rides the group-key namespace
  // under SEED_GROUP_KEY, which no flattened config can produce.
  const seedById = useMemo(() => {
    const m = new Map<string, string>();
    for (const r of runs) {
      const id = str(r, 'id') ?? '';
      const seed = num(r, 'seed');
      if (id !== '' && seed !== undefined) m.set(id, String(seed));
    }
    return m;
  }, [runs]);
  // The filter narrows the RAIL, never the wall: a run you selected and then
  // typed past stays on the wall, because hiding a curve as a side effect of
  // searching for another one would silently change the comparison.
  const shown = useMemo(() => facts.filter((f) => runMatchesFilter(f, filter)), [facts, filter]);

  // A run's colour comes from its place in the SELECTION, everywhere.
  const colorOf = (id: string): string => {
    const i = selected.indexOf(id);
    return SWATCHES[(i < 0 ? 0 : i) % SWATCHES.length];
  };

  // Build: metricName -> (runId -> cell). The union of metric names across the
  // selected runs drives one overlay chart each. `last` prefers the hub's own
  // `last_value` (authoritative even when the points were downsampled); min/max
  // come from the points that shipped.
  const byMetric = useMemo(() => {
    const map = new Map<string, Map<string, MetricCell>>();
    selected.forEach((runId, i) => {
      const rows = metricQs[i]?.data ?? [];
      for (const row of rows) {
        const name = str(row, 'name');
        if (name === undefined) continue;
        const pts = parsePoints(row['points']).map((p, idx) => ({ x: p.step ?? idx, y: p.value ?? 0 }));
        if (pts.length === 0) continue;
        const ext = extremesOf(pts);
        if (!map.has(name)) map.set(name, new Map());
        map.get(name)!.set(runId, {
          last: num(row, 'last_value') ?? ext.last,
          min: ext.min,
          max: ext.max,
          points: pts,
        });
      }
    });
    return map;
  }, [selected, metricQs]);

  // The comparer's row model — the same shape `run_config_diff` returns to
  // agents (plan §3.5). Registered config ∪ logged digest, logged winning.
  const configs = useMemo(() => {
    const registeredById = new Map(facts.map((f) => [f.id, f.config]));
    return selected.map((id, i) => {
      const logged = flattenConfig((configQs[i]?.data ?? {})['config']);
      const merged = mergeConfigSources(registeredById.get(id) ?? [], logged);
      return { id, entries: merged.entries, conflicts: merged.conflicts };
    });
  }, [selected, facts, configQs]);
  const diffRows = useMemo(() => configDiffRows(configs), [configs]);
  const visibleRows = showIdentical ? diffRows : diffRows.filter((r) => !r.identical);
  const conflictCount = configs.reduce((n, c) => n + c.conflicts.length, 0);

  // Grouping (A3). The pickable keys are the ones that actually VARY across the
  // selection — grouping by a key every run shares yields one group, which is
  // the ungrouped chart with extra steps. `seed` is offered on the same terms.
  const groupInputs = useMemo(
    () =>
      configs.map((c) => {
        const values = new Map(c.entries.map((e) => [e.key, e.value]));
        const seed = seedById.get(c.id);
        if (seed !== undefined) values.set(SEED_GROUP_KEY, seed);
        return { id: c.id, values };
      }),
    [configs, seedById],
  );
  const groupKeys = useMemo(() => {
    const keys = diffRows.filter((r) => !r.identical).map((r) => r.key);
    const seeds = new Set(groupInputs.map((g) => g.values.get(SEED_GROUP_KEY)));
    if (seeds.size > 1) keys.unshift(SEED_GROUP_KEY);
    return keys;
  }, [diffRows, groupInputs]);
  const groups = useMemo(
    () => (groupBy === null ? null : groupRunsBy(groupInputs, groupBy)),
    [groupBy, groupInputs],
  );

  const metricNames = [...byMetric.keys()].sort();
  const anyLoading = metricQs.some((q) => q.isLoading);

  function toggleRuns(): void {
    setRunsOpen((open) => {
      const next = !open;
      try {
        localStorage.setItem('termipod.compare.runsOpen', next ? '1' : '0');
      } catch {
        /* ignore */
      }
      return next;
    });
  }

  return (
    <WorkbenchSurface
      job="compare"
      leadingPaneWidth={runsOpen ? 240 : 0}
      leadingActions={
        <HeaderPaneToggle
          side="left"
          open={runsOpen}
          showLabel={t('nav.expand')}
          hideLabel={t('nav.collapse')}
          onToggle={toggleRuns}
        />
      }
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
      <div className={`compare-layout${runsOpen ? '' : ' runs-folded'}`}>
        {runsOpen && (
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
        )}

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
                          // min === max means a flat (or single-point) curve —
                          // the extremes add nothing there, so they stay off.
                          const spread = cell !== undefined && cell.min !== undefined && cell.min !== cell.max;
                          return (
                            <td key={id} className="mono">
                              {fmtValue(cell?.last)}
                              {delta !== null && (
                                <span className={`compare-delta ${deltaSign(delta)}`}>{formatDelta(delta)}</span>
                              )}
                              {spread && (
                                <div className="compare-extremes muted">
                                  {t('compare.min')} {fmtValue(cell.min)} · {t('compare.max')} {fmtValue(cell.max)}
                                </div>
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

              {selected.length >= 2 && (
                <div className="compare-diff">
                  <div className="compare-diff-head">
                    <span className="notes-head muted small">{t('compare.configDiff')}</span>
                    <span className="spacer" />
                    {conflictCount > 0 && (
                      <span className="compare-conflict small" title={t('compare.conflictHint')}>
                        {t('compare.conflicts').replace('{n}', String(conflictCount))}
                      </span>
                    )}
                    <label className="muted small">
                      <input
                        type="checkbox"
                        checked={showIdentical}
                        onChange={(e) => setShowIdentical(e.target.checked)}
                      />
                      {t('compare.showIdentical')}
                    </label>
                  </div>
                  <table className="compare-table">
                    <thead>
                      <tr>
                        <th>{t('compare.key')}</th>
                        {selected.map((id) => (
                          <th key={id}>
                            <span className="compare-swatch" style={{ background: colorOf(id) }} />
                            {runLabel(id)}
                          </th>
                        ))}
                      </tr>
                    </thead>
                    <tbody>
                      {visibleRows.map((r) => (
                        <tr key={r.key} className={r.identical ? 'compare-row-same' : ''}>
                          <td className="compare-metric-name mono">{r.key}</td>
                          {r.values.map((v, i) => (
                            <td key={selected[i]} className="mono">
                              {v ?? '—'}
                            </td>
                          ))}
                        </tr>
                      ))}
                      {visibleRows.length === 0 && (
                        <tr>
                          <td className="muted" colSpan={selected.length + 1}>
                            {diffRows.length === 0 ? t('compare.noConfig') : t('compare.allIdentical')}
                          </td>
                        </tr>
                      )}
                    </tbody>
                  </table>
                </div>
              )}

              <div className="compare-chart-controls">
                <label className="muted small">
                  {t('compare.smoothing')}
                  <input
                    type="range"
                    min={0}
                    max={MAX_SMOOTHING}
                    step={0.05}
                    value={smoothing}
                    aria-label={t('compare.smoothing')}
                    onChange={(e) => setSmoothing(Number(e.target.value))}
                  />
                  <span className="mono">{smoothing.toFixed(2)}</span>
                </label>
                <span className="spacer" />
                {groupKeys.length > 0 && (
                  <label className="muted small">
                    {t('compare.groupBy')}
                    <select
                      className="surface-select"
                      value={groupBy ?? ''}
                      onChange={(e) => setGroupBy(e.target.value === '' ? null : e.target.value)}
                    >
                      <option value="">{t('compare.noGrouping')}</option>
                      {groupKeys.map((k) => (
                        <option key={k} value={k}>
                          {k === SEED_GROUP_KEY ? t('compare.seedKey') : k}
                        </option>
                      ))}
                    </select>
                  </label>
                )}
                <span className="muted small">{t('compare.xAxis')}</span>
                <div className="compare-xaxis">
                  <button
                    type="button"
                    className={xAxis === 'step' ? 'on' : ''}
                    aria-pressed={xAxis === 'step'}
                    onClick={() => setXAxis('step')}
                  >
                    {t('compare.xStep')}
                  </button>
                  <button
                    type="button"
                    className={xAxis === 'relative' ? 'on' : ''}
                    aria-pressed={xAxis === 'relative'}
                    title={t('compare.xRelativeHint')}
                    onClick={() => setXAxis('relative')}
                  >
                    {t('compare.xRelative')}
                  </button>
                </div>
              </div>

              <div className="compare-charts">
                {metricNames.map((name) => {
                  const row = byMetric.get(name);
                  const series: ChartSeries[] = [];
                  const bands: ChartBand[] = [];
                  // A run's points, on the chosen x-axis and smoothed if asked.
                  const curveOf = (id: string): CurvePoint[] | null => {
                    const cell = row?.get(id);
                    if (cell === undefined) return null;
                    return xAxis === 'relative' ? toRelativeX(cell.points) : cell.points;
                  };
                  if (groups !== null) {
                    // Grouped: thin members under a bold mean with a ±band. The
                    // members are smoothed BEFORE aggregating, so the mean and
                    // its band describe the same data — smoothing only the mean
                    // would draw a bold line that wanders outside its own band.
                    groups.forEach((g, gi) => {
                      const color = SWATCHES[gi % SWATCHES.length];
                      const curves = g.runIds
                        .map(curveOf)
                        .filter((c): c is CurvePoint[] => c !== null)
                        .map((c) => (smoothing > 0 ? emaSmooth(c, smoothing) : c));
                      if (curves.length === 0) return;
                      for (const points of curves) {
                        series.push({ points, color, width: MEMBER_WIDTH, opacity: MEMBER_OPACITY, legendHidden: true });
                      }
                      const agg = aggregateCurves(curves);
                      series.push({
                        name: `${g.label} (${curves.length})`,
                        points: agg.map((p) => ({ x: p.x, y: p.mean })),
                        color,
                        width: GROUP_MEAN_WIDTH,
                      });
                      // A single-member group has a zero-width band; drawing it
                      // would suggest a spread that was never measured.
                      if (curves.length > 1) {
                        bands.push({ name: g.label, color, points: agg.map((p) => ({ x: p.x, lo: p.lo, hi: p.hi })) });
                      }
                    });
                  } else {
                    for (const id of selected) {
                      const points = curveOf(id);
                      if (points === null) continue;
                      const color = colorOf(id);
                      // The baseline is a RUN, so its dashing has no meaning
                      // under grouping — a group is not a baseline.
                      const dashed = id === baseline;
                      if (smoothing > 0) {
                        // Raw behind, smoothed in front: the smoothing is visibly
                        // an overlay on the data, not a replacement for it.
                        series.push({ name: runLabel(id), points, color, dashed, opacity: RAW_GHOST_OPACITY, legendHidden: true });
                        series.push({ name: runLabel(id), points: emaSmooth(points, smoothing), color, dashed });
                      } else {
                        series.push({ name: runLabel(id), points, color, dashed });
                      }
                    }
                  }
                  if (series.length === 0) return null;
                  return (
                    <div key={name} className="compare-chart-card">
                      <div className="compare-chart-title">{name}</div>
                      <ChartView chart={{ series, bands, categorical: false }} />
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
