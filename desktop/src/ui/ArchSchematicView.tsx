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

const NODE_TYPES = { archCard: ArchCardNode, archContainer: ContainerNode, archStrip: StripNode };

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
  const laid = useMemo(() => layoutArch(schematic, labelsFor(t, containerLabel)), [schematic, containerLabel, t]);

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

    const es: Edge[] = laid.edges.map((e) =>
      e.kind === 'main'
        ? {
            id: e.id,
            source: e.source,
            target: e.target,
            sourceHandle: 'b',
            targetHandle: 't',
            type: 'smoothstep',
            className: 'archgraph-edge main',
            markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14 },
          }
        : { id: e.id, source: e.source, target: e.target, sourceHandle: 'rout', targetHandle: 'rin', type: 'default', className: 'archgraph-edge residual' },
    );
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
      ]);
    },
    [archById, menu, t, detailsFor],
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
        onPaneContextMenu={(e) => e.preventDefault()}
        fitView
        minZoom={0.2}
        maxZoom={2}
        proOptions={{ hideAttribution: true }}
        nodesDraggable={false}
        nodesConnectable={false}
        elementsSelectable
        panOnScroll
      >
        <Background />
      </ReactFlow>

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
