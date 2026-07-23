import { useEffect, useMemo, useState } from 'react';
import { Background, Handle, Position, ReactFlow, type Edge, type Node, type NodeProps } from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import ELK from 'elkjs/lib/elk.bundled.js';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import { listConnections } from '../state/connections';
import type { InspectTab } from '../state/inspect';
import { runModuleAst } from '../state/moduleAst';
import { buildModuleGraph, type ModuleGraph, type ModuleGraphNode } from '../state/moduleAstCore';

// Card sizing (kept in sync with the CSS below so elkjs reserves the right box).
const HEADER = 34;
const ROW = 19;
const PAD = 12;
function nodeWidth(n: ModuleGraphNode): number {
  const longest = Math.max(n.label.length, ...n.submodules.map((s) => s.attr.length + s.type.length + 3), 8);
  return Math.min(320, Math.max(150, longest * 7 + 28));
}
function nodeHeight(n: ModuleGraphNode): number {
  return HEADER + (n.bases.length > 0 ? ROW : 0) + n.submodules.length * ROW + PAD;
}

interface CardData extends Record<string, unknown> {
  node: ModuleGraphNode;
  onReveal: (lineno: number) => void;
}

/// A class card — name header + `extends` line + one row per submodule (attr: type,
/// local types tinted). Clicking the card reveals the class in the code tab.
function ClassCard({ data }: NodeProps): JSX.Element {
  const { node, onReveal } = data as CardData;
  return (
    <div className="modgraph-card" onClick={() => onReveal(node.lineno)} title={`line ${node.lineno}`}>
      <Handle type="target" position={Position.Top} className="modgraph-handle" />
      <div className="modgraph-card-name">{node.label}</div>
      {node.bases.length > 0 && <div className="modgraph-card-base small muted">: {node.bases.join(', ')}</div>}
      {node.submodules.map((s, i) => (
        <div key={i} className={`modgraph-row small${s.local ? ' local' : ''}`}>
          <span className="modgraph-attr">{s.attr}</span>
          <span className="muted">: {s.type}</span>
        </div>
      ))}
      <Handle type="source" position={Position.Bottom} className="modgraph-handle" />
    </div>
  );
}

const NODE_TYPES = { classCard: ClassCard };

const elk = new ELK();

async function layout(graph: ModuleGraph, onReveal: (l: number) => void): Promise<{ nodes: Node[]; edges: Edge[] }> {
  const sized = graph.nodes.map((n) => ({ n, w: nodeWidth(n), h: nodeHeight(n) }));
  const laid = await elk.layout({
    id: 'root',
    layoutOptions: {
      'elk.algorithm': 'layered',
      'elk.direction': 'DOWN',
      'elk.spacing.nodeNode': '36',
      'elk.layered.spacing.nodeNodeBetweenLayers': '56',
    },
    children: sized.map(({ n, w, h }) => ({ id: n.id, width: w, height: h })),
    edges: graph.edges.map((e, i) => ({ id: `e${i}`, sources: [e.source], targets: [e.target] })),
  });
  const pos = new Map((laid.children ?? []).map((c) => [c.id, { x: c.x ?? 0, y: c.y ?? 0 }]));
  const nodes: Node[] = sized.map(({ n, w, h }) => ({
    id: n.id,
    type: 'classCard',
    position: pos.get(n.id) ?? { x: 0, y: 0 },
    data: { node: n, onReveal } satisfies CardData,
    style: { width: w, height: h },
  }));
  const edges: Edge[] = graph.edges.map((e, i) => ({
    id: `e${i}`,
    source: e.source,
    target: e.target,
    label: e.kind === 'composition' ? e.label : undefined,
    animated: false,
    className: `modgraph-edge ${e.kind}`,
  }));
  return { nodes, edges };
}

/// The W4b interactive class-composition graph (plan §4b). Runs the stdlib-`ast`
/// module reader on the file's venue, builds the class graph, lays it out with elkjs,
/// and renders it with React Flow. Clicking a class card reveals it in the code tab
/// (the code-sync). Heavy deps (React Flow + elkjs) ride this lazy chunk only.
export function ModuleGraphView({ tab, onReveal }: { tab: InspectTab; onReveal: (lineno: number) => void }): JSX.Element {
  const t = useT();
  const [flow, setFlow] = useState<{ nodes: Node[]; edges: Edge[] } | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const connection = useMemo(() => (tab.source === 'remote' && tab.hostId ? listConnections().find((c) => c.id === tab.hostId) : undefined), [tab.hostId, tab.source]);

  useEffect(() => {
    if (tab.path === undefined) return;
    let cancelled = false;
    setFlow(null);
    setErr(null);
    void (async () => {
      try {
        const model = await runModuleAst({ filePath: tab.path!, connection });
        const graph = buildModuleGraph(model);
        if (graph.nodes.length === 0) throw new Error(t('modgraph.empty'));
        const f = await layout(graph, onReveal);
        if (!cancelled) setFlow(f);
      } catch (e) {
        if (!cancelled) setErr(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab.path, connection]);

  if (err !== null)
    return (
      <div className="inspect-error region-pad">
        <Icon name="alert" size={16} /> {err}
      </div>
    );
  if (flow === null) return <div className="muted region-pad">{t('modgraph.reading')}</div>;

  return (
    <div className="modgraph-wrap">
      <ReactFlow nodes={flow.nodes} edges={flow.edges} nodeTypes={NODE_TYPES} fitView minZoom={0.1} proOptions={{ hideAttribution: true }} nodesDraggable={false} nodesConnectable={false}>
        <Background />
      </ReactFlow>
    </div>
  );
}
