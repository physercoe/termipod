import { useCallback, useMemo, useRef, useState } from 'react';
import {
  Background,
  Handle,
  MarkerType,
  Position,
  ReactFlow,
  type Edge,
  type Node,
  type NodeProps,
  type ReactFlowInstance,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { useT, type TLookup } from '../i18n';
import { Icon } from './Icon';
import { useContextMenu } from './ContextMenu';
import type { ArchCard } from '../state/checkpoint';
import { archNodeDetails, type ArchDetailRow, type ArchNode, type ArchSchematic, type AttnKind, type LayerCell } from '../state/archSchematic';
import { layoutArch, type ArchLabels, type LaidStrip } from '../state/archLayout';
import { buildArchPanels, type ArchPanel, type ArchPanelItem } from '../state/archPanels';
import { archToSvg, archSvgSize, type SvgTheme } from '../state/archSvg';

/// Config-only architecture schematic renderer (round-3 §5a follow-on; archgraph
/// plan W3). A thin mapping of the PURE layout (`state/archLayout.ts`) onto React
/// Flow — the box/card arithmetic lives there and is unit-tested without a
/// browser; this file owns only the visual chrome (node components, edge styling,
/// the detail panel and the context menu) and the i18n labels the layout needs.
///
/// Uniform models render exactly as before (one dashed ×N container). A
/// heterogeneous stack — a hybrid linear-attention interleave, a windowed
/// cadence — renders nested repeat groups (the 3×/1× idiom) with a per-layer
/// pattern strip beside the stack.
///
/// Interactive: click a card to open a detail panel (the full config facts for
/// that block), right-click for a context menu (copy details / fit view), click
/// empty canvas to dismiss. Pan/zoom as before. The heavy React Flow dep rides
/// this lazy chunk only, exactly like ModuleGraphView.

interface CardData extends Record<string, unknown> {
  node: ArchNode;
  selected: boolean;
  /// Present on attention cards of a heterogeneous stack — drives the per-kind
  /// colour band / texture (linear operators get the novelty accent).
  attn?: AttnKind;
}

/// One component card. Four handles (top/bottom for the main stack, left/right
/// for the residual skips) — all invisible; ids let the edges pick sides.
function ArchCardNode({ data }: NodeProps): JSX.Element {
  const { node, selected, attn } = data as CardData;
  return (
    <div className="archgraph-card" data-kind={node.kind} data-attn={attn} data-selected={selected ? 'true' : undefined}>
      <Handle type="target" id="t" position={Position.Top} className="archgraph-h" />
      <Handle type="target" id="rin" position={Position.Right} className="archgraph-h" />
      <div className="archgraph-card-label">{node.label}</div>
      {node.sub !== undefined && node.sub !== '' && <div className="archgraph-card-sub">{node.sub}</div>}
      <Handle type="source" id="b" position={Position.Bottom} className="archgraph-h" />
      <Handle type="source" id="rout" position={Position.Right} className="archgraph-h" />
    </div>
  );
}

interface ContainerData extends Record<string, unknown> {
  label: string;
  /// `cycle` is the outer repeat group of an interleave pattern; `run` is a plain
  /// (possibly nested) block of same-attention layers.
  variant: 'cycle' | 'run';
}

/// The dashed "×N" backdrop behind a repeated block.
function ContainerNode({ data }: NodeProps): JSX.Element {
  const { label, variant } = data as ContainerData;
  return (
    <div className="archgraph-container" data-variant={variant}>
      <span className="archgraph-container-tag">{label}</span>
    </div>
  );
}

interface StripData extends Record<string, unknown> {
  cells: LayerCell[];
  title: string;
  caption: string;
  /// Per-cell tooltip text, pre-formatted ("layer 12 · KDA · MoE").
  tips: string[];
}

/// The per-layer pattern strip: one thin cell per layer, top→bottom, background
/// keyed to the attention operator and a left border keyed to the FFN. Compact
/// enough for a 93-layer stack (cells flex to fill).
function StripNode({ data }: NodeProps): JSX.Element {
  const { cells, title, caption, tips } = data as StripData;
  return (
    <div className="archgraph-strip" title={title}>
      <div className="archgraph-strip-cells">
        {cells.map((c, i) => (
          <div key={c.index} className="archgraph-strip-cell" data-attn={c.attn} data-ffn={c.ffn} title={tips[i]} />
        ))}
      </div>
      <span className="archgraph-strip-caption">{caption}</span>
    </div>
  );
}

interface PanelData extends Record<string, unknown> {
  title: string;
  /// Rows in render order; the expert chips arrive pre-grouped into one row.
  rows: Array<{ id: string; kind: 'item'; item: ArchPanelItem; label: string } | { id: string; kind: 'chips'; items: Array<{ item: ArchPanelItem; label: string }> }>;
  note?: string;
}

/// A dotted zoom-in panel (W4): what is inside an attention or MoE block — the
/// projection chain, or the router → experts fan-out. Config-derived; a shape
/// per item kind (trapezoid projections, glyph ops, a chip row of experts).
function PanelNode({ data }: NodeProps): JSX.Element {
  const { title, rows, note } = data as PanelData;
  return (
    <div className="archgraph-panel">
      <Handle type="target" id="pin" position={Position.Left} className="archgraph-h" />
      <div className="archgraph-panel-title">{title}</div>
      <div className="archgraph-panel-rows">
        {rows.map((r) =>
          r.kind === 'chips' ? (
            <div className="archgraph-panel-chips" key={r.id}>
              {r.items.map(({ item, label }) => (
                <span className="archgraph-panel-chip" data-shape={item.shape} key={item.id} title={item.value}>
                  {item.shape === 'more' ? item.value : label}
                </span>
              ))}
            </div>
          ) : (
            <div className="archgraph-panel-row" data-shape={r.item.shape} key={r.id}>
              <span className="archgraph-panel-row-label">{r.label}</span>
              {r.item.value !== undefined && <span className="archgraph-panel-row-value mono">{r.item.value}</span>}
            </div>
          ),
        )}
      </div>
      {note !== undefined && <div className="archgraph-panel-note">{note}</div>}
    </div>
  );
}

const NODE_TYPES = { archCard: ArchCardNode, archContainer: ContainerNode, archStrip: StripNode, archPanel: PanelNode };

/// Group a panel's items into render rows — every non-chip item is its own row,
/// and the expert chips share one (mirrors `panelRows` in the layout module, so
/// the measured height and the rendered height agree).
function panelRowsFor(panel: ArchPanel, t: TLookup): PanelData['rows'] {
  const rows: PanelData['rows'] = [];
  let chips: Array<{ item: ArchPanelItem; label: string }> = [];
  for (const it of panel.items) {
    const label = t(`archgraph.panel.${it.key}`);
    if (it.shape === 'expert' || it.shape === 'more') {
      chips.push({ item: it, label });
      continue;
    }
    if (chips.length > 0) {
      rows.push({ id: `chips${rows.length}`, kind: 'chips', items: chips });
      chips = [];
    }
    rows.push({ id: it.id, kind: 'item', item: it, label });
  }
  if (chips.length > 0) rows.push({ id: `chips${rows.length}`, kind: 'chips', items: chips });
  return rows;
}

function labelsFor(t: TLookup, containerLabel: string): ArchLabels {
  return {
    attn: (kind) => t(`archgraph.attn.${kind}`),
    norm: 'Norm',
    ffnDense: t('archgraph.ffnDense'),
    ffnMoe: t('archgraph.ffnMoe'),
    linearSub: t('archgraph.attnSubLinear'),
    container: containerLabel,
  };
}

/// Save a Blob under `name` — the app's established download idiom (see
/// TableEditor's CSV export): an object URL on a synthetic anchor, revoked after.
function saveBlob(blob: Blob, name: string): void {
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  a.click();
  URL.revokeObjectURL(url);
}

/// Rasterise an SVG document to PNG via a canvas (D-6: SVG first, PNG as the
/// convenience). Resolves to null if the browser refuses to decode it.
async function svgToPng(svg: string, width: number, height: number, scale = 2): Promise<Blob | null> {
  const url = URL.createObjectURL(new Blob([svg], { type: 'image/svg+xml;charset=utf-8' }));
  try {
    const img = new Image();
    const loaded = new Promise<boolean>((resolve) => {
      img.onload = () => resolve(true);
      img.onerror = () => resolve(false);
    });
    img.src = url;
    if (!(await loaded)) return null;
    const canvas = document.createElement('canvas');
    canvas.width = Math.max(1, Math.round(width * scale));
    canvas.height = Math.max(1, Math.round(height * scale));
    const ctx = canvas.getContext('2d');
    if (ctx === null) return null;
    ctx.scale(scale, scale);
    ctx.drawImage(img, 0, 0);
    return await new Promise<Blob | null>((resolve) => canvas.toBlob((b) => resolve(b), 'image/png'));
  } finally {
    URL.revokeObjectURL(url);
  }
}

/// The attention operators actually present in this stack, in first-appearance
/// order — the legend keys only what the figure shows.
function legendKinds(s: ArchSchematic): AttnKind[] {
  const out: AttnKind[] = [];
  for (const c of s.layout?.strip ?? []) if (!out.includes(c.attn)) out.push(c.attn);
  return out;
}

function stripNode(strip: LaidStrip, t: TLookup): Node {
  const tips = strip.cells.map(
    (c) => `${t('archgraph.layer')} ${c.index} · ${t(`archgraph.attn.${c.attn}`)} · ${c.ffn === 'moe' ? t('archgraph.ffnMoeShort') : t('archgraph.ffnDenseShort')}`,
  );
  return {
    id: '__strip',
    type: 'archStrip',
    position: { x: strip.x, y: strip.y },
    data: { cells: strip.cells, tips, title: t('archgraph.patternStrip'), caption: `${strip.cells.length} ${t('archgraph.layers')}` } satisfies StripData,
    style: { width: strip.w, height: strip.h },
    selectable: false,
    draggable: false,
    zIndex: 1,
  };
}

export function ArchSchematicView({
  schematic,
  config,
  card,
}: {
  schematic: ArchSchematic;
  config?: Record<string, unknown> | null;
  card?: ArchCard | null;
}): JSX.Element {
  const t = useT();
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const rf = useRef<ReactFlowInstance | null>(null);
  // The shared app-wide primitive (viewport-clamped, keyboard-navigable,
  // backdrop-dismissed) — same one the Inspect tree menus use.
  const menu = useContextMenu();

  const containerLabel = schematic.layers > 0 ? `×${schematic.layers} ${t('archgraph.layers')}` : t('archgraph.decoderBlock');
  // Zoom-in panels (W4) need the card + config; a hybrid expands its FULL
  // attention kind, whose projections the config actually describes.
  const panels = useMemo(() => {
    if (config === null || config === undefined || card === null || card === undefined) return [];
    const full = schematic.layout?.strip.find((c) => c.attn === 'MLA' || c.attn === 'GQA' || c.attn === 'MHA' || c.attn === 'global' || c.attn === 'sliding');
    const attn: AttnKind = full?.attn ?? (card.chips.includes('MLA') ? 'MLA' : card.kvHeads !== undefined && card.heads !== undefined && card.kvHeads < card.heads ? 'GQA' : 'MHA');
    return buildArchPanels(card, config, attn);
  }, [card, config, schematic]);
  const laid = useMemo(() => layoutArch(schematic, labelsFor(t, containerLabel), panels), [schematic, containerLabel, t, panels]);

  const { nodes, edges } = useMemo(() => {
    const ns: Node[] = [];
    // Containers first so they sit behind the cards (zIndex also enforces it).
    for (const b of laid.boxes) {
      ns.push({
        id: b.id,
        type: 'archContainer',
        position: { x: b.x, y: b.y },
        data: { label: b.label, variant: b.variant } satisfies ContainerData,
        style: { width: b.w, height: b.h },
        selectable: false,
        draggable: false,
        zIndex: b.z,
      });
    }
    for (const c of laid.cards) {
      ns.push({
        id: c.id,
        type: 'archCard',
        position: { x: c.x, y: c.y },
        data: { node: c.node, selected: c.id === selectedId, attn: c.attn } satisfies CardData,
        style: { width: c.w, height: c.h },
        selectable: true,
        draggable: false,
        zIndex: c.z,
      });
    }
    if (laid.strip !== null) ns.push(stripNode(laid.strip, t));
    for (const p of laid.panels) {
      ns.push({
        id: p.id,
        type: 'archPanel',
        position: { x: p.x, y: p.y },
        data: {
          title: t(`archgraph.panel.${p.panel.titleKey}`),
          rows: panelRowsFor(p.panel, t),
          note: p.panel.noteKey !== undefined ? t(`archgraph.panel.${p.panel.noteKey}`) : undefined,
        } satisfies PanelData,
        style: { width: p.w, height: p.h },
        selectable: false,
        draggable: false,
        zIndex: p.z,
      });
    }

    const es: Edge[] = laid.edges.map((e) => {
      if (e.kind === 'main') {
        return {
          id: e.id,
          source: e.source,
          target: e.target,
          sourceHandle: 'b',
          targetHandle: 't',
          type: 'smoothstep',
          className: 'archgraph-edge main',
          markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14, color: 'var(--archgraph-edge-main)' },
        };
      }
      if (e.kind === 'leader') {
        // The dotted leader line tying a block to its zoom-in panel.
        return { id: e.id, source: e.source, target: e.target, sourceHandle: 'rout', targetHandle: 'pin', type: 'smoothstep', className: 'archgraph-edge leader' };
      }
      return { id: e.id, source: e.source, target: e.target, sourceHandle: 'rout', targetHandle: 'rin', type: 'default', className: 'archgraph-edge residual' };
    });
    return { nodes: ns, edges: es };
  }, [laid, selectedId, t]);

  const archById = useMemo(() => new Map(laid.cards.map((c) => [c.id, c.node])), [laid]);
  const selectedNode = selectedId !== null ? archById.get(selectedId) ?? null : null;
  const detailsFor = useCallback(
    (n: ArchNode): ArchDetailRow[] => (config !== null && config !== undefined && card !== null && card !== undefined ? archNodeDetails(n, config, card) : []),
    [config, card],
  );
  const details = useMemo(() => (selectedNode !== null ? detailsFor(selectedNode) : []), [selectedNode, detailsFor]);

  const onNodeClick = useCallback((_e: React.MouseEvent, node: Node) => {
    if (node.id.startsWith('__') || node.id.endsWith('box') || node.id.startsWith('cyc')) return;
    setSelectedId(node.id);
  }, []);
  // Export the figure as a standalone document (D-6: SVG-first, PNG for
  // convenience). Generated from the pure layout, not scraped from the DOM, so
  // the file is self-contained and identical in either theme.
  const svgOpts = useCallback(
    (theme: SvgTheme) => ({
      theme,
      title: card?.family,
      annotations: schematic.layout?.annotations.map((a) => a.text) ?? [],
      legend: legendKinds(schematic).map((k) => ({ label: t(`archgraph.attn.${k}`), attn: k })),
      labelFor: (k: string) => t(`archgraph.panel.${k}`),
    }),
    [card, schematic, t],
  );
  const baseName = useCallback(() => (card?.family ?? 'architecture').replace(/[^\w.-]+/g, '-').toLowerCase(), [card]);
  const exportSvg = useCallback(
    (theme: SvgTheme) => {
      const svg = archToSvg(laid, svgOpts(theme));
      saveBlob(new Blob([svg], { type: 'image/svg+xml;charset=utf-8' }), `${baseName()}-${theme}.svg`);
    },
    [laid, svgOpts, baseName],
  );
  const exportPng = useCallback(
    (theme: SvgTheme) => {
      const opts = svgOpts(theme);
      const svg = archToSvg(laid, opts);
      const { width, height } = archSvgSize(laid, opts);
      void svgToPng(svg, width, height).then((png) => {
        if (png !== null) saveBlob(png, `${baseName()}-${theme}.png`);
      });
    },
    [laid, svgOpts, baseName],
  );
  const exportItems = useMemo(
    () => [
      { label: t('archgraph.exportSvgDark'), onClick: () => exportSvg('dark') },
      { label: t('archgraph.exportSvgLight'), onClick: () => exportSvg('light') },
      { label: t('archgraph.exportPng'), onClick: () => exportPng('dark') },
    ],
    [t, exportSvg, exportPng],
  );

  const onNodeContextMenu = useCallback(
    (e: React.MouseEvent, node: Node) => {
      const arch = archById.get(node.id);
      if (arch === undefined) return;
      setSelectedId(node.id);
      menu.open(e, [
        {
          label: t('archgraph.copyDetails'),
          onClick: () => {
            const text = [arch.label, ...detailsFor(arch).map((d) => `${d.label}: ${d.value}`)].join('\n');
            void navigator.clipboard?.writeText(text);
          },
        },
        { label: t('archgraph.fitView'), onClick: () => rf.current?.fitView({ duration: 200 }) },
        ...exportItems,
      ]);
    },
    [archById, menu, t, detailsFor, exportItems],
  );
  // Parity: the same export actions are reachable from empty canvas, not only by
  // right-clicking a card.
  const onPaneContextMenu = useCallback(
    (e: React.MouseEvent | MouseEvent) => {
      e.preventDefault();
      menu.open(e as React.MouseEvent, [{ label: t('archgraph.fitView'), onClick: () => rf.current?.fitView({ duration: 200 }) }, ...exportItems]);
    },
    [menu, t, exportItems],
  );
  const dismiss = useCallback(() => setSelectedId(null), []);

  return (
    <div className="archgraph-wrap">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={NODE_TYPES}
        onInit={(inst) => (rf.current = inst)}
        onNodeClick={onNodeClick}
        onNodeContextMenu={onNodeContextMenu}
        onPaneClick={dismiss}
        onPaneContextMenu={onPaneContextMenu}
        fitView
        minZoom={0.2}
        maxZoom={2}
        proOptions={{ hideAttribution: true }}
        nodesDraggable={false}
        nodesConnectable={false}
        elementsSelectable
        panOnScroll
      >
        <Background color="var(--archgraph-grid)" gap={20} size={1.15} />
      </ReactFlow>

      {/* Annotation layer (W4): the config-derived callouts the spec produced,
          plus a colour key for the pattern strip / component bands. Only for a
          heterogeneous stack — a uniform figure needs no key. */}
      {schematic.layout !== undefined && (
        <div className="archgraph-legend">
          {schematic.layout.annotations.length > 0 && (
            <ul className="archgraph-legend-notes">
              {schematic.layout.annotations.map((a) => (
                <li key={a.id}>{a.text}</li>
              ))}
            </ul>
          )}
          <div className="archgraph-legend-keys">
            {legendKinds(schematic).map((k) => (
              <span className="archgraph-legend-key" key={k}>
                <span className="archgraph-legend-swatch" data-attn={k} />
                {t(`archgraph.attn.${k}`)}
              </span>
            ))}
          </div>
        </div>
      )}

      {selectedNode !== null && (
        <div className="archgraph-detail" role="dialog" aria-label={selectedNode.label}>
          <div className="archgraph-detail-head">
            <span className="archgraph-detail-kind" data-kind={selectedNode.kind} />
            <span className="archgraph-detail-title">{selectedNode.label}</span>
            <span className="spacer" />
            <button className="archgraph-detail-close" onClick={dismiss} aria-label={t('archgraph.close')}>
              <Icon name="close" size={13} />
            </button>
          </div>
          {details.length > 0 ? (
            <dl className="archgraph-detail-rows">
              {details.map((d) => (
                <div className="archgraph-detail-row" key={d.label}>
                  <dt className="small muted">{d.label}</dt>
                  <dd className="mono">{d.value}</dd>
                </div>
              ))}
            </dl>
          ) : (
            <div className="small muted archgraph-detail-empty">{t('archgraph.noDetail')}</div>
          )}
        </div>
      )}

      {menu.node}
    </div>
  );
}
