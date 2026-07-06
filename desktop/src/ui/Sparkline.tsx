/// A dependency-free sparkline (parity — mobile _SparklinePainter). Draws an SVG
/// polyline over a metric's points. `sampleOrdinalX` plots by sample index rather
/// than the step value (used for system metrics whose steps are wall-clock-ish).
/// We deliberately avoid a charting library — the desktop ships no ML-dashboard
/// dependency; an inline SVG polyline is enough for the run-metrics view.
export interface MetricPoint {
  step?: number;
  value?: number;
}

export function parsePoints(raw: unknown): MetricPoint[] {
  if (!Array.isArray(raw)) return [];
  const out: MetricPoint[] = [];
  for (const p of raw) {
    if (Array.isArray(p)) {
      // The hub serialises each point as a [step, value] tuple (parity —
      // mobile runs_screen.dart `_parsePoints`). This is the real wire shape.
      if (p.length >= 2 && typeof p[0] === 'number' && typeof p[1] === 'number') {
        out.push({ step: p[0], value: p[1] });
      } else if (p.length === 1 && typeof p[0] === 'number') {
        out.push({ value: p[0] });
      }
    } else if (p && typeof p === 'object') {
      const o = p as Record<string, unknown>;
      const value = typeof o.value === 'number' ? o.value : typeof o.v === 'number' ? o.v : undefined;
      const step = typeof o.step === 'number' ? o.step : typeof o.s === 'number' ? o.s : undefined;
      if (value !== undefined) out.push({ step, value });
    } else if (typeof p === 'number') {
      out.push({ value: p });
    }
  }
  return out;
}

export function Sparkline({
  points,
  width = 260,
  height = 48,
  sampleOrdinalX = false,
}: {
  points: MetricPoint[];
  width?: number;
  height?: number;
  sampleOrdinalX?: boolean;
}): JSX.Element {
  const vals = points.map((p) => p.value ?? 0);
  if (vals.length === 0) return <svg width={width} height={height} className="sparkline" />;
  const xs = sampleOrdinalX ? points.map((_, i) => i) : points.map((p, i) => p.step ?? i);
  const minX = Math.min(...xs);
  const maxX = Math.max(...xs);
  const minY = Math.min(...vals);
  const maxY = Math.max(...vals);
  const spanX = maxX - minX || 1;
  const spanY = maxY - minY || 1;
  const pad = 3;
  const coords = points.map((p, i) => {
    const x = pad + ((xs[i] - minX) / spanX) * (width - 2 * pad);
    const y = height - pad - (((p.value ?? 0) - minY) / spanY) * (height - 2 * pad);
    return `${x.toFixed(1)},${y.toFixed(1)}`;
  });
  return (
    <svg width={width} height={height} className="sparkline" preserveAspectRatio="none">
      <polyline points={coords.join(' ')} fill="none" strokeWidth={1.5} />
    </svg>
  );
}
