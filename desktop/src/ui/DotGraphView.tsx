import { useCallback, useEffect, useRef, useState } from 'react';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import { renderDot } from '../state/dotGraph';

/// Renders a Graphviz DOT string as a pan/zoomable SVG (plan §5 graph substrate).
/// Lazy chunk — the wasm engine loads via `renderDot`'s dynamic import on first
/// use. The SVG is Graphviz's own output (labels are emitted as escaped SVG text,
/// not markup), injected read-only; interaction is CSS-transform pan/zoom only.
export function DotGraphView({ dot }: { dot: string }): JSX.Element {
  const t = useT();
  const [svg, setSvg] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [view, setView] = useState({ x: 0, y: 0, scale: 1 });
  const drag = useRef<{ x: number; y: number; vx: number; vy: number } | null>(null);
  const wrapRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    let cancelled = false;
    setSvg(null);
    setErr(null);
    void (async () => {
      try {
        const out = await renderDot(dot);
        if (!cancelled) setView({ x: 0, y: 0, scale: 1 });
        if (!cancelled) setSvg(out);
      } catch (e) {
        if (!cancelled) setErr(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [dot]);

  const onWheel = useCallback((e: React.WheelEvent) => {
    e.preventDefault();
    const rect = wrapRef.current?.getBoundingClientRect();
    const cx = rect ? e.clientX - rect.left : 0;
    const cy = rect ? e.clientY - rect.top : 0;
    setView((v) => {
      const factor = e.deltaY < 0 ? 1.15 : 1 / 1.15;
      const scale = Math.min(8, Math.max(0.1, v.scale * factor));
      const k = scale / v.scale;
      // Keep the point under the cursor fixed while zooming.
      return { scale, x: cx - (cx - v.x) * k, y: cy - (cy - v.y) * k };
    });
  }, []);

  const onDown = useCallback((e: React.MouseEvent) => {
    drag.current = { x: e.clientX, y: e.clientY, vx: view.x, vy: view.y };
  }, [view.x, view.y]);
  const onMove = useCallback((e: React.MouseEvent) => {
    if (drag.current === null) return;
    const d = drag.current;
    setView((v) => ({ ...v, x: d.vx + (e.clientX - d.x), y: d.vy + (e.clientY - d.y) }));
  }, []);
  const onUp = useCallback(() => {
    drag.current = null;
  }, []);

  const copy = useCallback(() => {
    if (svg !== null) void navigator.clipboard.writeText(svg);
  }, [svg]);

  if (err !== null)
    return (
      <div className="inspect-error region-pad">
        <Icon name="alert" size={16} /> {err}
      </div>
    );
  if (svg === null) return <div className="muted region-pad">{t('graph.rendering')}</div>;

  return (
    <div className="dotgraph">
      <div className="dotgraph-bar">
        <button className="import-btn" onClick={() => setView({ x: 0, y: 0, scale: 1 })} title={t('graph.reset')}>
          <Icon name="fit-page" size={14} /> {t('graph.reset')}
        </button>
        <button className="import-btn" onClick={() => setView((v) => ({ ...v, scale: Math.min(8, v.scale * 1.15) }))}>+</button>
        <button className="import-btn" onClick={() => setView((v) => ({ ...v, scale: Math.max(0.1, v.scale / 1.15) }))}>−</button>
        <span className="small muted">{Math.round(view.scale * 100)}%</span>
        <span className="spacer" />
        <button className="import-btn" onClick={copy} title={t('graph.copySvg')}>
          <Icon name="file-text" size={14} /> {t('graph.copySvg')}
        </button>
      </div>
      <div
        ref={wrapRef}
        className="dotgraph-canvas"
        onWheel={onWheel}
        onMouseDown={onDown}
        onMouseMove={onMove}
        onMouseUp={onUp}
        onMouseLeave={onUp}
      >
        <div
          className="dotgraph-svg"
          style={{ transform: `translate(${view.x}px, ${view.y}px) scale(${view.scale})` }}
          // eslint-disable-next-line react/no-danger -- Graphviz-generated SVG (labels escaped as text), rendered read-only.
          dangerouslySetInnerHTML={{ __html: svg }}
        />
      </div>
    </div>
  );
}
