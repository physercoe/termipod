import { useEffect, useMemo, useRef, useState } from 'react';
import { useT } from '../i18n';
import {
  EDGE_TYPES,
  useCanvas,
  type CanvasCard,
} from '../state/canvas';
import { useLibrary, type Reference } from '../state/library';
import { WorkbenchSurface } from '../ui/WorkbenchSurface';

/// J4 — a spatial thinking canvas (BUILD, not a tldraw embed): note & reference
/// cards on an infinite pan/zoom surface, joined by typed edges, wired to the J1
/// reference library. Drag a card's header to move it, scroll to zoom, drag the
/// background to pan; "Link" arms a connect from the selected card. The inspector
/// shows a card's content and its backlinks (what points at it) so the board
/// reads as a Zettelkasten, not just a whiteboard. Round-1 storage is
/// device-local (`state/canvas.ts`), the same posture as the reference library.

const CARD_W = 210;
const MIN_SCALE = 0.35;
const MAX_SCALE = 2.4;

interface View {
  ox: number;
  oy: number;
  scale: number;
}

function anchor(c: CanvasCard): { x: number; y: number } {
  return { x: c.x + CARD_W / 2, y: c.y + 20 };
}

function refTitle(r: Reference | undefined, fallback: string): string {
  if (r === undefined) return fallback;
  return r.title !== '' ? r.title : fallback;
}

export function CanvasSurface(): JSX.Element {
  const t = useT();
  const cards = useCanvas((s) => s.cards);
  const edges = useCanvas((s) => s.edges);
  const addCard = useCanvas((s) => s.addCard);
  const updateCard = useCanvas((s) => s.updateCard);
  const removeCard = useCanvas((s) => s.removeCard);
  const addEdge = useCanvas((s) => s.addEdge);
  const setEdgeType = useCanvas((s) => s.setEdgeType);
  const removeEdge = useCanvas((s) => s.removeEdge);
  const clearBoard = useCanvas((s) => s.clear);
  const references = useLibrary((s) => s.references);

  const [view, setView] = useState<View>({ ox: 0, oy: 0, scale: 1 });
  const [selected, setSelected] = useState<string | null>(null);
  const [connectFrom, setConnectFrom] = useState<string | null>(null);

  const viewportRef = useRef<HTMLDivElement>(null);
  const pan = useRef<{ sx: number; sy: number; ox: number; oy: number } | null>(null);
  const drag = useRef<{ id: string; sx: number; sy: number; cx: number; cy: number; moved: boolean } | null>(null);
  const movedRef = useRef(false);

  const cardById = useMemo(() => {
    const m = new Map<string, CanvasCard>();
    cards.forEach((c) => m.set(c.id, c));
    return m;
  }, [cards]);
  const refById = useMemo(() => {
    const m = new Map<string, Reference>();
    references.forEach((r) => m.set(r.id, r));
    return m;
  }, [references]);

  // Zoom toward the cursor. Native listener so preventDefault isn't passive.
  useEffect(() => {
    const el = viewportRef.current;
    if (el === null) return;
    const onWheel = (e: WheelEvent): void => {
      e.preventDefault();
      const rect = el.getBoundingClientRect();
      const cx = e.clientX - rect.left;
      const cy = e.clientY - rect.top;
      setView((v) => {
        const f = e.deltaY < 0 ? 1.1 : 1 / 1.1;
        const scale = Math.min(MAX_SCALE, Math.max(MIN_SCALE, v.scale * f));
        const k = scale / v.scale;
        return { scale, ox: cx - (cx - v.ox) * k, oy: cy - (cy - v.oy) * k };
      });
    };
    el.addEventListener('wheel', onWheel, { passive: false });
    return () => el.removeEventListener('wheel', onWheel);
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') setConnectFrom(null);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  function onViewportPointerDown(e: React.PointerEvent): void {
    if (e.button !== 0) return;
    pan.current = { sx: e.clientX, sy: e.clientY, ox: view.ox, oy: view.oy };
    viewportRef.current?.setPointerCapture(e.pointerId);
    setSelected(null);
    setConnectFrom(null);
  }

  function onViewportPointerMove(e: React.PointerEvent): void {
    if (drag.current !== null) {
      const d = drag.current;
      if (Math.abs(e.clientX - d.sx) + Math.abs(e.clientY - d.sy) > 3) d.moved = true;
      updateCard(d.id, {
        x: d.cx + (e.clientX - d.sx) / view.scale,
        y: d.cy + (e.clientY - d.sy) / view.scale,
      });
      return;
    }
    if (pan.current !== null) {
      const p = pan.current;
      setView((v) => ({ ...v, ox: p.ox + (e.clientX - p.sx), oy: p.oy + (e.clientY - p.sy) }));
    }
  }

  function onViewportPointerUp(): void {
    movedRef.current = drag.current?.moved ?? false;
    drag.current = null;
    pan.current = null;
  }

  function onCardHeadDown(e: React.PointerEvent, card: CanvasCard): void {
    if (e.button !== 0) return;
    e.stopPropagation();
    drag.current = { id: card.id, sx: e.clientX, sy: e.clientY, cx: card.x, cy: card.y, moved: false };
    viewportRef.current?.setPointerCapture(e.pointerId);
  }

  function onCardClick(card: CanvasCard): void {
    if (connectFrom !== null && connectFrom !== card.id) {
      addEdge(connectFrom, card.id, 'relates');
      setConnectFrom(null);
      setSelected(card.id);
      return;
    }
    if (movedRef.current) {
      movedRef.current = false;
      return;
    }
    setSelected(card.id);
  }

  function viewCenterWorld(): { x: number; y: number } {
    const rect = viewportRef.current?.getBoundingClientRect();
    const w = rect?.width ?? 800;
    const h = rect?.height ?? 600;
    return { x: (w / 2 - view.ox) / view.scale, y: (h / 2 - view.oy) / view.scale };
  }

  function addNote(): void {
    const p = viewCenterWorld();
    setSelected(addCard({ kind: 'note', x: p.x - CARD_W / 2, y: p.y - 30, text: '' }));
  }

  function addRefCard(refId: string): void {
    const p = viewCenterWorld();
    setSelected(addCard({ kind: 'ref', x: p.x - CARD_W / 2 + 16, y: p.y - 30 + 16, text: '', refId }));
  }

  function cardTitle(card: CanvasCard): string {
    if (card.kind === 'ref') return refTitle(refById.get(card.refId ?? ''), t('canvas.missingRef'));
    const firstLine = card.text.split('\n')[0].trim();
    return firstLine !== '' ? firstLine : t('canvas.untitledNote');
  }

  const selectedCard = selected !== null ? cardById.get(selected) : undefined;

  return (
    <WorkbenchSurface
      job="canvas"
      actions={
        <>
          <button onClick={addNote}>+ {t('canvas.addNote')}</button>
          <select
            className="canvas-add-ref"
            value=""
            disabled={references.length === 0}
            onChange={(e) => {
              if (e.target.value !== '') addRefCard(e.target.value);
            }}
          >
            <option value="">+ {t('canvas.addRef')}</option>
            {references.map((r) => (
              <option key={r.id} value={r.id}>
                {r.title !== '' ? r.title : t('read.untitled')}
              </option>
            ))}
          </select>
          <button onClick={() => setView({ ox: 0, oy: 0, scale: 1 })}>{t('canvas.resetView')}</button>
          {cards.length > 0 && (
            <button
              className="link-btn danger"
              onClick={() => {
                if (window.confirm(t('canvas.clearConfirm'))) {
                  clearBoard();
                  setSelected(null);
                }
              }}
            >
              {t('canvas.clear')}
            </button>
          )}
        </>
      }
    >
      <div className="canvas-layout">
        <div
          className={`canvas-viewport${connectFrom !== null ? ' connecting' : ''}`}
          ref={viewportRef}
          onPointerDown={onViewportPointerDown}
          onPointerMove={onViewportPointerMove}
          onPointerUp={onViewportPointerUp}
        >
          {connectFrom !== null && <div className="canvas-hint">{t('canvas.connectHint')}</div>}
          {cards.length === 0 && <div className="canvas-empty">{t('canvas.empty')}</div>}

          <div
            className="canvas-world"
            style={{ transform: `translate(${view.ox}px, ${view.oy}px) scale(${view.scale})` }}
          >
            <svg className="canvas-edges" aria-hidden>
              <defs>
                <marker
                  id="canvas-arrow"
                  viewBox="0 0 10 10"
                  refX="9"
                  refY="5"
                  markerWidth="7"
                  markerHeight="7"
                  orient="auto-start-reverse"
                >
                  <path d="M 0 0 L 10 5 L 0 10 z" />
                </marker>
              </defs>
              {edges.map((e) => {
                const a = cardById.get(e.from);
                const b = cardById.get(e.to);
                if (a === undefined || b === undefined) return null;
                const pa = anchor(a);
                const pb = anchor(b);
                return (
                  <line
                    key={e.id}
                    className="canvas-edge-line"
                    x1={pa.x}
                    y1={pa.y}
                    x2={pb.x}
                    y2={pb.y}
                    markerEnd="url(#canvas-arrow)"
                  />
                );
              })}
            </svg>

            {edges.map((e) => {
              const a = cardById.get(e.from);
              const b = cardById.get(e.to);
              if (a === undefined || b === undefined) return null;
              const pa = anchor(a);
              const pb = anchor(b);
              const mx = (pa.x + pb.x) / 2;
              const my = (pa.y + pb.y) / 2;
              const cur = EDGE_TYPES.indexOf(e.type);
              return (
                <div key={e.id} className="canvas-edge-label" style={{ left: mx, top: my }}>
                  <button
                    className="canvas-edge-type"
                    title={t('canvas.cycleType')}
                    onClick={() => setEdgeType(e.id, EDGE_TYPES[(cur + 1) % EDGE_TYPES.length])}
                  >
                    {t(`canvas.edge.${e.type}`)}
                  </button>
                  <button className="canvas-edge-x" title={t('canvas.removeEdge')} onClick={() => removeEdge(e.id)}>
                    ×
                  </button>
                </div>
              );
            })}

            {cards.map((card) => {
              const cref = card.kind === 'ref' ? refById.get(card.refId ?? '') : undefined;
              return (
                <div
                  key={card.id}
                  className={`canvas-card ${card.kind}${selected === card.id ? ' selected' : ''}${
                    connectFrom === card.id ? ' connect-src' : ''
                  }`}
                  style={{ left: card.x, top: card.y, width: CARD_W }}
                  onPointerDown={(e) => e.stopPropagation()}
                  onClick={() => onCardClick(card)}
                >
                  <div className="canvas-card-head" onPointerDown={(e) => onCardHeadDown(e, card)}>
                    <span className="canvas-card-kind">{card.kind === 'ref' ? '❋' : '▤'}</span>
                    <span className="canvas-card-title">{cardTitle(card)}</span>
                  </div>
                  {card.kind === 'note' ? (
                    <textarea
                      className="canvas-card-note"
                      value={card.text}
                      placeholder={t('canvas.notePlaceholder')}
                      onChange={(e) => updateCard(card.id, { text: e.target.value })}
                    />
                  ) : (
                    <div className="canvas-card-ref">
                      {cref !== undefined ? (
                        <>
                          <div className="canvas-card-ref-meta muted small">
                            {cref.authors.slice(0, 2).join(', ')}
                            {cref.authors.length > 2 ? ' et al.' : ''}
                            {cref.year !== undefined ? ` · ${cref.year}` : ''}
                          </div>
                          {cref.tldr !== undefined && <div className="canvas-card-ref-tldr">{cref.tldr}</div>}
                        </>
                      ) : (
                        <div className="muted small">{t('canvas.missingRef')}</div>
                      )}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        </div>

        {selectedCard !== undefined && (
          <aside className="canvas-inspector">
            <Inspector
              card={selectedCard}
              reference={refById.get(selectedCard.refId ?? '')}
              connecting={connectFrom === selectedCard.id}
              onArmConnect={() => setConnectFrom(selectedCard.id)}
              onRemove={() => {
                removeCard(selectedCard.id);
                setSelected(null);
              }}
              onSelect={setSelected}
            />
          </aside>
        )}
      </div>
    </WorkbenchSurface>
  );
}

// ---- Inspector -------------------------------------------------------------

function Inspector(props: {
  card: CanvasCard;
  reference: Reference | undefined;
  connecting: boolean;
  onArmConnect: () => void;
  onRemove: () => void;
  onSelect: (id: string) => void;
}): JSX.Element {
  const { card, reference, connecting, onArmConnect, onRemove, onSelect } = props;
  const t = useT();
  const cards = useCanvas((s) => s.cards);
  const edges = useCanvas((s) => s.edges);
  const updateCard = useCanvas((s) => s.updateCard);

  const title = (id: string): string => {
    const c = cards.find((x) => x.id === id);
    if (c === undefined) return id;
    if (c.kind === 'note') {
      const l = c.text.split('\n')[0].trim();
      return l !== '' ? l : t('canvas.untitledNote');
    }
    return t('canvas.refCard');
  };

  const outgoing = edges.filter((e) => e.from === card.id);
  const incoming = edges.filter((e) => e.to === card.id);

  return (
    <div className="canvas-inspector-body scroll">
      <div className="canvas-insp-kind muted small">
        {card.kind === 'ref' ? t('canvas.refCard') : t('canvas.noteCard')}
      </div>

      {card.kind === 'ref' ? (
        <div className="canvas-insp-ref">
          <div className="canvas-insp-title">{refTitle(reference, t('canvas.missingRef'))}</div>
          {reference !== undefined && (
            <>
              <div className="muted small">
                {reference.authors.join(', ')}
                {reference.year !== undefined ? ` · ${reference.year}` : ''}
              </div>
              {reference.venue !== undefined && reference.venue !== '' && (
                <div className="muted small">{reference.venue}</div>
              )}
              {reference.abstract !== undefined && reference.abstract !== '' && (
                <p className="canvas-insp-abstract">{reference.abstract}</p>
              )}
            </>
          )}
        </div>
      ) : (
        <textarea
          className="canvas-insp-note editor-pane"
          value={card.text}
          placeholder={t('canvas.notePlaceholder')}
          onChange={(e) => updateCard(card.id, { text: e.target.value })}
        />
      )}

      <div className="canvas-insp-actions">
        <button className={connecting ? 'primary' : ''} onClick={onArmConnect}>
          {connecting ? t('canvas.linking') : t('canvas.link')}
        </button>
        <button className="link-btn danger" onClick={onRemove}>
          {t('canvas.deleteCard')}
        </button>
      </div>

      {(outgoing.length > 0 || incoming.length > 0) && (
        <div className="canvas-links">
          {outgoing.length > 0 && (
            <div className="canvas-links-group">
              <div className="canvas-links-label">{t('canvas.linksOut')}</div>
              {outgoing.map((e) => (
                <button key={e.id} className="canvas-link-row" onClick={() => onSelect(e.to)}>
                  <span className="canvas-link-type">{t(`canvas.edge.${e.type}`)}</span>
                  <span className="canvas-link-target">{title(e.to)}</span>
                </button>
              ))}
            </div>
          )}
          {incoming.length > 0 && (
            <div className="canvas-links-group">
              <div className="canvas-links-label">{t('canvas.backlinks')}</div>
              {incoming.map((e) => (
                <button key={e.id} className="canvas-link-row" onClick={() => onSelect(e.from)}>
                  <span className="canvas-link-target">{title(e.from)}</span>
                  <span className="canvas-link-type">{t(`canvas.edge.${e.type}`)}</span>
                </button>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
