import { useMemo } from 'react';
import { Background, Handle, MarkerType, Position, ReactFlow, type Edge, type Node, type NodeProps } from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { useT } from '../i18n';
import type { ArchNode, ArchSchematic } from '../state/archSchematic';

/// Config-only architecture schematic renderer (round-3 §5a follow-on). Lays out
/// the pure `ArchSchematic` (state/archSchematic.ts) as a paper-style stacked
/// block diagram with React Flow — colour-coded component cards, a dashed "×N"
/// container around the repeated decoder block, and side-routed residual skips.
/// A fixed vertical layout (the architecture is a known template — no auto-layout
/// engine needed); the heavy React Flow dep rides this lazy chunk only, exactly
/// like ModuleGraphView.

// Card geometry (kept in sync with the CSS so the container box wraps correctly).
const W = 260;
const H = 56;
const GAP = 30;
const X = 70;
const PAD = 20;

interface CardData extends Record<string, unknown> {
  node: ArchNode;
}

/// One component card. Four handles (top/bottom for the main stack, left/right
/// for the residual skips) — all invisible; ids let the edges pick sides.
function ArchCardNode({ data }: NodeProps): JSX.Element {
  const { node } = data as CardData;
  return (
    <div className="archgraph-card" data-kind={node.kind}>
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
}

/// The dashed "×N" backdrop behind the repeated decoder block.
function ContainerNode({ data }: NodeProps): JSX.Element {
  const { label } = data as ContainerData;
  return (
    <div className="archgraph-container">
      <span className="archgraph-container-tag">{label}</span>
    </div>
  );
}

const NODE_TYPES = { archCard: ArchCardNode, archContainer: ContainerNode };

function layoutSchematic(s: ArchSchematic, containerLabel: string): { nodes: Node[]; edges: Edge[] } {
  const yOf = (i: number): number => i * (H + GAP);
  const nodes: Node[] = [];

  // The ×N container box wrapping the in-block nodes — pushed first so it sits
  // behind the cards (zIndex also enforces it).
  const blockIdx = s.nodes.map((n, i) => (n.inBlock ? i : -1)).filter((i) => i >= 0);
  if (blockIdx.length > 0 && s.layers > 0) {
    const top = yOf(Math.min(...blockIdx)) - PAD;
    const bottom = yOf(Math.max(...blockIdx)) + H + PAD;
    nodes.push({
      id: '__container',
      type: 'archContainer',
      position: { x: X - PAD, y: top },
      data: { label: containerLabel } satisfies ContainerData,
      style: { width: W + PAD * 2, height: bottom - top },
      selectable: false,
      draggable: false,
      zIndex: 0,
    });
  }

  s.nodes.forEach((n, i) => {
    nodes.push({
      id: n.id,
      type: 'archCard',
      position: { x: X, y: yOf(i) },
      data: { node: n } satisfies CardData,
      style: { width: W, height: H },
      selectable: false,
      draggable: false,
      zIndex: 1,
    });
  });

  const edges: Edge[] = [];
  // Main vertical flow, input (top) → output (bottom).
  for (let i = 0; i < s.nodes.length - 1; i += 1) {
    edges.push({
      id: `m${i}`,
      source: s.nodes[i].id,
      target: s.nodes[i + 1].id,
      sourceHandle: 'b',
      targetHandle: 't',
      type: 'smoothstep',
      className: 'archgraph-edge main',
      markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14 },
    });
  }
  // Residual skips, routed on the right side (the classic "Add" bypass).
  s.residuals.forEach((r, i) => {
    edges.push({
      id: `r${i}`,
      source: r.from,
      target: r.to,
      sourceHandle: 'rout',
      targetHandle: 'rin',
      type: 'default',
      className: 'archgraph-edge residual',
    });
  });

  return { nodes, edges };
}

export function ArchSchematicView({ schematic }: { schematic: ArchSchematic }): JSX.Element {
  const t = useT();
  const containerLabel = schematic.layers > 0 ? `×${schematic.layers} ${t('archgraph.layers')}` : t('archgraph.decoderBlock');
  const { nodes, edges } = useMemo(() => layoutSchematic(schematic, containerLabel), [schematic, containerLabel]);
  return (
    <div className="archgraph-wrap">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={NODE_TYPES}
        fitView
        minZoom={0.2}
        maxZoom={2}
        proOptions={{ hideAttribution: true }}
        nodesDraggable={false}
        nodesConnectable={false}
        elementsSelectable={false}
        panOnScroll
      >
        <Background />
      </ReactFlow>
    </div>
  );
}
