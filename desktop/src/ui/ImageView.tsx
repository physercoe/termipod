import { useMemo, useRef, useState } from 'react';
import { useT } from '../i18n';
import { Icon } from './Icon';
import { ANNOTATION_COLORS, useAnnotations, type Annotation } from '../state/annotations';

/// Image attachment viewer with freeform annotations — area boxes and freehand
/// ink — on the fixed geometry of an image (screenshots, figures, diagrams).
/// Unlike reflowable text, an image never re-typesets, so pixel geometry is a
/// stable anchor: coordinates are stored as fractions [0..1] of the image box
/// (origin top-left), independent of the rendered size. The SVG overlay uses a
/// unit viewBox with preserveAspectRatio="none" so the same fractions paint at any
/// display size, and `vector-effect: non-scaling-stroke` keeps line width uniform
/// despite the non-uniform viewBox stretch.

const VB = 1000; // viewBox units; store [0..1], paint ×VB
const MIN_AREA = 0.005; // ignore accidental micro-drags

type Tool = null | 'area' | 'ink';
type Draft =
  | { kind: 'area'; x0: number; y0: number; x1: number; y1: number }
  | { kind: 'ink'; pts: number[] };

function ptsToStr(flat: number[]): string {
  const parts: string[] = [];
  for (let i = 0; i + 1 < flat.length; i += 2) parts.push(`${(flat[i] * VB).toFixed(1)},${(flat[i + 1] * VB).toFixed(1)}`);
  return parts.join(' ');
}

export function ImageView({
  url,
  fileName,
  referenceId,
  attId,
}: {
  url: string;
  fileName: string;
  referenceId?: string;
  attId: string;
}): JSX.Element {
  const t = useT();
  const svgRef = useRef<SVGSVGElement | null>(null);
  const drawing = useRef(false);
  const [tool, setTool] = useState<Tool>(null);
  const [color, setColor] = useState(ANNOTATION_COLORS[0]);
  const [active, setActive] = useState<string | null>(null);
  const [draft, setDraft] = useState<Draft | null>(null);

  const allAnnos = useAnnotations((s) => s.items);
  const addAnno = useAnnotations((s) => s.add);
  const updateAnno = useAnnotations((s) => s.update);
  const removeAnno = useAnnotations((s) => s.remove);
  const annos = useMemo(
    () => allAnnos.filter((a) => a.referenceId === referenceId && a.attId === attId && a.position.space === 'image'),
    [allAnnos, referenceId, attId],
  );
  const activeAnno = active !== null ? annos.find((a) => a.id === active) : undefined;
  const canAnnotate = referenceId !== undefined;

  function norm(e: React.PointerEvent): [number, number] {
    const svg = svgRef.current;
    if (svg === null) return [0, 0];
    const r = svg.getBoundingClientRect();
    const x = (e.clientX - r.left) / r.width;
    const y = (e.clientY - r.top) / r.height;
    return [Math.min(1, Math.max(0, x)), Math.min(1, Math.max(0, y))];
  }

  function onDown(e: React.PointerEvent): void {
    if (tool === null || !canAnnotate) return;
    e.preventDefault();
    try {
      (e.currentTarget as Element).setPointerCapture(e.pointerId); // the svg is large — capture is reliable here
    } catch {
      /* WebView2 can refuse capture; onPointerLeave still commits */
    }
    drawing.current = true;
    const [x, y] = norm(e);
    setDraft(tool === 'area' ? { kind: 'area', x0: x, y0: y, x1: x, y1: y } : { kind: 'ink', pts: [x, y] });
  }
  function onMove(e: React.PointerEvent): void {
    if (!drawing.current || draft === null) return;
    const [x, y] = norm(e);
    setDraft(draft.kind === 'area' ? { ...draft, x1: x, y1: y } : { kind: 'ink', pts: [...draft.pts, x, y] });
  }
  function onUp(): void {
    if (!drawing.current || draft === null) {
      drawing.current = false;
      return;
    }
    drawing.current = false;
    if (referenceId !== undefined) {
      if (draft.kind === 'area') {
        const x0 = Math.min(draft.x0, draft.x1);
        const y0 = Math.min(draft.y0, draft.y1);
        const x1 = Math.max(draft.x0, draft.x1);
        const y1 = Math.max(draft.y0, draft.y1);
        if (x1 - x0 > MIN_AREA && y1 - y0 > MIN_AREA) {
          addAnno({
            referenceId,
            attId,
            type: 'image', // an area box (matches the PDF reader's area kind)
            color,
            pageIndex: 0,
            position: { pageIndex: 0, space: 'image', rects: [[x0, y0, x1, y1]] },
            tags: [],
          });
        }
      } else if (draft.pts.length >= 4) {
        addAnno({
          referenceId,
          attId,
          type: 'ink',
          color,
          pageIndex: 0,
          position: { pageIndex: 0, space: 'image', paths: [draft.pts] },
          tags: [],
        });
      }
    }
    setDraft(null);
  }

  function renderShape(a: Annotation): JSX.Element | null {
    const c = a.color ?? ANNOTATION_COLORS[0];
    const sel = a.id === active;
    const select = (e: React.MouseEvent): void => {
      e.stopPropagation();
      if (tool === null) setActive(a.id);
    };
    if (a.type === 'ink' && a.position.paths?.[0] !== undefined) {
      return (
        <polyline
          key={a.id}
          className="img-anno-shape"
          points={ptsToStr(a.position.paths[0])}
          fill="none"
          stroke={c}
          strokeWidth={sel ? 5 : 3}
          strokeOpacity={0.9}
          strokeLinecap="round"
          strokeLinejoin="round"
          vectorEffect="non-scaling-stroke"
          onClick={select}
        />
      );
    }
    const r = a.position.rects?.[0];
    if (r === undefined) return null;
    return (
      <rect
        key={a.id}
        className="img-anno-shape"
        x={r[0] * VB}
        y={r[1] * VB}
        width={(r[2] - r[0]) * VB}
        height={(r[3] - r[1]) * VB}
        fill={c}
        fillOpacity={0.18}
        stroke={c}
        strokeWidth={sel ? 4 : 2.5}
        vectorEffect="non-scaling-stroke"
        onClick={select}
      />
    );
  }

  function renderDraft(d: Draft): JSX.Element {
    if (d.kind === 'ink') {
      return (
        <polyline
          points={ptsToStr(d.pts)}
          fill="none"
          stroke={color}
          strokeWidth={3}
          strokeOpacity={0.9}
          strokeLinecap="round"
          strokeLinejoin="round"
          vectorEffect="non-scaling-stroke"
        />
      );
    }
    const x0 = Math.min(d.x0, d.x1);
    const y0 = Math.min(d.y0, d.y1);
    return (
      <rect
        x={x0 * VB}
        y={y0 * VB}
        width={Math.abs(d.x1 - d.x0) * VB}
        height={Math.abs(d.y1 - d.y0) * VB}
        fill={color}
        fillOpacity={0.18}
        stroke={color}
        strokeWidth={2.5}
        vectorEffect="non-scaling-stroke"
      />
    );
  }

  return (
    <div className="img-anno">
      {activeAnno !== undefined ? (
        <div className="img-anno-bar img-anno-editor">
          <span className="img-anno-colors">
            {ANNOTATION_COLORS.map((c) => (
              <button
                key={c}
                className={`epub-swatch${(activeAnno.color ?? ANNOTATION_COLORS[0]) === c ? ' active' : ''}`}
                style={{ background: c }}
                aria-label={c}
                onClick={() => updateAnno(activeAnno.id, { color: c })}
              />
            ))}
          </span>
          <input
            className="img-anno-comment"
            placeholder={t('read.epubNotePlaceholder')}
            value={activeAnno.comment ?? ''}
            onChange={(e) => updateAnno(activeAnno.id, { comment: e.target.value })}
          />
          <button
            className="small danger"
            title={t('read.epubRemoveHl')}
            aria-label={t('read.epubRemoveHl')}
            onClick={() => {
              removeAnno(activeAnno.id);
              setActive(null);
            }}
          >
            <Icon name="trash" size={14} />
          </button>
          <button className="small" title={t('common.close')} aria-label={t('common.close')} onClick={() => setActive(null)}>
            <Icon name="close" size={14} />
          </button>
        </div>
      ) : (
        canAnnotate && (
          <div className="img-anno-bar img-anno-toolbar" role="group" aria-label={t('read.imgAnnotate')}>
            <button
              className={tool === 'area' ? 'small active' : 'small'}
              title={t('read.imgArea')}
              aria-label={t('read.imgArea')}
              aria-pressed={tool === 'area'}
              onClick={() => setTool((v) => (v === 'area' ? null : 'area'))}
            >
              <Icon name="square" size={14} />
            </button>
            <button
              className={tool === 'ink' ? 'small active' : 'small'}
              title={t('read.imgDraw')}
              aria-label={t('read.imgDraw')}
              aria-pressed={tool === 'ink'}
              onClick={() => setTool((v) => (v === 'ink' ? null : 'ink'))}
            >
              <Icon name="pen" size={14} />
            </button>
            <span className="img-anno-colors">
              {ANNOTATION_COLORS.map((c) => (
                <button
                  key={c}
                  className={`epub-swatch${color === c ? ' active' : ''}`}
                  style={{ background: c }}
                  aria-label={c}
                  onClick={() => setColor(c)}
                />
              ))}
            </span>
          </div>
        )
      )}
      <div className="att-image-wrap img-anno-scroll">
        <div className="img-anno-stage">
          <img className="att-image" src={url} alt={fileName} draggable={false} />
          <svg
            ref={svgRef}
            className="img-anno-svg"
            viewBox={`0 0 ${VB} ${VB}`}
            preserveAspectRatio="none"
            style={{ cursor: tool !== null ? 'crosshair' : 'default', pointerEvents: canAnnotate ? 'auto' : 'none' }}
            onPointerDown={onDown}
            onPointerMove={onMove}
            onPointerUp={onUp}
            onPointerLeave={onUp}
            onClick={(e) => {
              if (tool === null && e.target === svgRef.current) setActive(null);
            }}
          >
            {annos.map(renderShape)}
            {draft !== null && renderDraft(draft)}
          </svg>
        </div>
      </div>
    </div>
  );
}
