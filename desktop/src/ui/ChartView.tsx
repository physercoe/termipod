/// Dependency-free chart renderer for JSON artifacts that are actually chart
/// data (director feedback: "some json is chart data, it should be rendered as
/// chart/graph"). `chartFromJson` sniffs the common wire shapes an agent emits —
/// a numeric array, `[x,y]` tuples, an array of `{label,value}` objects,
/// `{labels,values}`, or a `{series|datasets:[{name,data}]}` bundle — and
/// returns a normalised series set, or `null` when the JSON isn't chartable (the
/// caller then falls back to pretty-printed JSON). Rendering is inline SVG (a
/// line chart, or a bar chart for a single small categorical series), matching
/// the design system's palette — no charting library ships on the desktop.

export interface ChartPoint {
  x?: number;
  label?: string;
  y: number;
}
export interface ChartSeries {
  name?: string;
  points: ChartPoint[];
}
export interface ChartData {
  series: ChartSeries[];
  /** Categorical single-series data renders as bars; everything else as lines. */
  categorical: boolean;
}

const NUM_KEYS = ['value', 'y', 'count', 'total', 'amount', 'val', 'v'];
const LABEL_KEYS = ['label', 'name', 'category', 'key', 'date', 'time', 'x', 't'];

function fin(v: unknown): number | undefined {
  return typeof v === 'number' && Number.isFinite(v) ? v : undefined;
}

/** Normalise one array into points, or null if any element isn't chartable. */
function pointsFromArray(arr: unknown[]): ChartPoint[] | null {
  const pts: ChartPoint[] = [];
  for (const el of arr) {
    const n = fin(el);
    if (n !== undefined) {
      pts.push({ y: n });
    } else if (Array.isArray(el)) {
      const a = fin(el[0]);
      const b = fin(el[1]);
      if (el.length >= 2 && a !== undefined && b !== undefined) pts.push({ x: a, y: b });
      else if (el.length === 1 && a !== undefined) pts.push({ y: a });
      else return null;
    } else if (el !== null && typeof el === 'object') {
      const o = el as Record<string, unknown>;
      let y: number | undefined;
      for (const k of NUM_KEYS) {
        const v = fin(o[k]);
        if (v !== undefined) {
          y = v;
          break;
        }
      }
      if (y === undefined) {
        const numeric = Object.values(o).filter((v) => fin(v) !== undefined);
        if (numeric.length === 1) y = fin(numeric[0]);
      }
      if (y === undefined) return null;
      let label: string | undefined;
      let x: number | undefined;
      for (const k of LABEL_KEYS) {
        const v = o[k];
        if (typeof v === 'string') {
          label = v;
          break;
        }
        const xn = fin(v);
        if (xn !== undefined) {
          x = xn;
          break;
        }
      }
      pts.push({ x, label, y });
    } else {
      return null;
    }
  }
  return pts.length >= 2 ? pts : null;
}

export function chartFromJson(data: unknown): ChartData | null {
  if (Array.isArray(data)) {
    const pts = pointsFromArray(data);
    if (!pts) return null;
    const categorical = pts.every((p) => p.label !== undefined && p.x === undefined) && pts.length <= 24;
    return { series: [{ points: pts }], categorical };
  }
  if (data === null || typeof data !== 'object') return null;
  const o = data as Record<string, unknown>;

  const labels = Array.isArray(o.labels) ? o.labels : Array.isArray(o.categories) ? o.categories : null;

  // { series | datasets: [{ name|label, data }] }
  const rawSeries = Array.isArray(o.series) ? o.series : Array.isArray(o.datasets) ? o.datasets : null;
  if (rawSeries !== null) {
    const out: ChartSeries[] = [];
    for (const s of rawSeries) {
      if (s === null || typeof s !== 'object') return null;
      const so = s as Record<string, unknown>;
      const arr = Array.isArray(so.data) ? so.data : Array.isArray(so.points) ? so.points : null;
      if (arr === null) return null;
      const pts = pointsFromArray(arr);
      if (!pts) return null;
      const name = typeof so.name === 'string' ? so.name : typeof so.label === 'string' ? so.label : undefined;
      out.push({ name, points: pts });
    }
    if (out.length === 0) return null;
    if (labels !== null) {
      for (const s of out) {
        s.points.forEach((p, i) => {
          if (p.label === undefined && labels[i] !== undefined) p.label = String(labels[i]);
        });
      }
    }
    const categorical = out.length === 1 && labels !== null && labels.length <= 24;
    return { series: out, categorical };
  }

  // { labels, values | data }
  const values = Array.isArray(o.values) ? o.values : Array.isArray(o.data) ? o.data : null;
  if (labels !== null && values !== null && values.every((v) => fin(v) !== undefined)) {
    const pts = values.map((v, i) => ({ label: String(labels[i] ?? i), y: fin(v) as number }));
    if (pts.length < 2) return null;
    return { series: [{ points: pts }], categorical: pts.length <= 24 };
  }

  return null;
}

// The one chart palette for the whole app — CompareSurface's run swatches use
// the same source so an overlay curve and its table swatch never drift (#322).
export const CHART_PALETTE = [
  'var(--color-primary)',
  'var(--color-terminal-cyan)',
  'var(--color-terminal-yellow)',
  'var(--color-secondary)',
  'var(--color-terminal-green)',
];

const W = 640;
const H = 280;
const PAD = { l: 48, r: 16, t: 16, b: 34 };

function niceTicks(min: number, max: number, count = 4): number[] {
  if (min === max) return [min];
  const step = (max - min) / count;
  return Array.from({ length: count + 1 }, (_, i) => min + step * i);
}

function fmt(n: number): string {
  const a = Math.abs(n);
  if (a >= 1000 || (a > 0 && a < 0.01)) return n.toExponential(1);
  return Number.isInteger(n) ? String(n) : n.toFixed(2);
}

export function ChartView({ chart }: { chart: ChartData }): JSX.Element {
  const allY = chart.series.flatMap((s) => s.points.map((p) => p.y));
  const nPts = Math.max(...chart.series.map((s) => s.points.length));
  const rawMin = Math.min(...allY);
  const rawMax = Math.max(...allY);
  // Bars are read against a zero baseline; lines get a little headroom.
  const yMin = chart.categorical ? Math.min(0, rawMin) : rawMin;
  const yMax = chart.categorical ? Math.max(0, rawMax) : rawMax;
  const spanY = yMax - yMin || 1;
  const plotW = W - PAD.l - PAD.r;
  const plotH = H - PAD.t - PAD.b;

  const yToPx = (y: number): number => PAD.t + plotH - ((y - yMin) / spanY) * plotH;
  const idxToPx = (i: number, n: number): number => PAD.l + (n <= 1 ? plotW / 2 : (i / (n - 1)) * plotW);

  const ticks = niceTicks(yMin, yMax);
  const multi = chart.series.length > 1;
  const labels = chart.series[0]?.points.map((p) => p.label);
  const showXLabels = labels !== undefined && labels.some((l) => l !== undefined) && nPts <= 12;

  // A screen-reader summary: a chart is opaque without it (#316).
  const seriesNames = chart.series.map((s) => s.name).filter((n): n is string => n !== undefined);
  const chartLabel =
    `${multi ? `${chart.series.length}-series ` : ''}${chart.categorical ? 'bar' : 'line'} chart, ${nPts} point${nPts === 1 ? '' : 's'}` +
    (seriesNames.length > 0 ? `: ${seriesNames.join(', ')}` : '');

  return (
    <div className="chart-view">
      <svg
        viewBox={`0 0 ${W} ${H}`}
        className="chart-svg"
        preserveAspectRatio="xMidYMid meet"
        role="img"
        aria-label={chartLabel}
      >
        <title>{chartLabel}</title>
        {/* y grid + labels */}
        {ticks.map((tv, i) => {
          const y = yToPx(tv);
          return (
            <g key={`t${i}`}>
              <line x1={PAD.l} y1={y} x2={W - PAD.r} y2={y} className="chart-grid" />
              <text x={PAD.l - 6} y={y + 3} className="chart-axis" textAnchor="end">
                {fmt(tv)}
              </text>
            </g>
          );
        })}

        {chart.categorical ? (
          // Single categorical series → bars.
          chart.series[0].points.map((p, i) => {
            const n = chart.series[0].points.length;
            const bw = (plotW / n) * 0.62;
            const cx = PAD.l + (i + 0.5) * (plotW / n);
            const y0 = yToPx(Math.max(0, yMin));
            const y1 = yToPx(p.y);
            return (
              <rect
                key={`b${i}`}
                x={cx - bw / 2}
                y={Math.min(y0, y1)}
                width={bw}
                height={Math.abs(y1 - y0)}
                className="chart-bar"
              />
            );
          })
        ) : (
          chart.series.map((s, si) => {
            const color = CHART_PALETTE[si % CHART_PALETTE.length];
            const pts = s.points
              .map((p, i) => `${idxToPx(i, s.points.length).toFixed(1)},${yToPx(p.y).toFixed(1)}`)
              .join(' ');
            const last = s.points[s.points.length - 1];
            return (
              <g key={`s${si}`}>
                <polyline points={pts} fill="none" stroke={color} strokeWidth={1.75} className="chart-line" />
                {last !== undefined && (
                  <circle cx={idxToPx(s.points.length - 1, s.points.length)} cy={yToPx(last.y)} r={2.5} fill={color} />
                )}
              </g>
            );
          })
        )}

        {/* x labels (categorical / small series only) */}
        {showXLabels &&
          labels!.map((l, i) => (
            <text
              key={`x${i}`}
              x={chart.categorical ? PAD.l + (i + 0.5) * (plotW / labels!.length) : idxToPx(i, labels!.length)}
              y={H - PAD.b + 16}
              className="chart-axis"
              textAnchor="middle"
            >
              {l !== undefined ? (l.length > 8 ? `${l.slice(0, 7)}…` : l) : ''}
            </text>
          ))}
      </svg>

      {multi && (
        <div className="chart-legend">
          {chart.series.map((s, si) => (
            <span key={`l${si}`} className="chart-legend-item">
              <span className="chart-swatch" style={{ background: CHART_PALETTE[si % CHART_PALETTE.length] }} />
              {s.name ?? `series ${si + 1}`}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}
