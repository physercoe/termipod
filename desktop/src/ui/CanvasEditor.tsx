import { createContext, Fragment, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import {
  addEdge as rfAddEdge,
  applyEdgeChanges,
  applyNodeChanges,
  BaseEdge,
  Background,
  Controls,
  EdgeLabelRenderer,
  getBezierPath,
  getNodesBounds,
  Handle,
  MarkerType,
  MiniMap,
  NodeResizer,
  Position,
  ReactFlow,
  ReactFlowProvider,
  useReactFlow,
  type Connection,
  type Edge,
  type EdgeChange,
  type EdgeProps,
  type Node,
  type NodeChange,
  type NodeProps,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { useT } from '../i18n';
import {
  colorCss,
  createHistory,
  CANVAS_PRESETS,
  DEFAULT_H,
  DEFAULT_W,
  EDGE_TYPES,
  newId,
  parseCanvas,
  serializeCanvas,
  type Board,
  type CanvasNode,
  type EdgeType,
  type NodeType,
  type Side,
} from '../state/canvas';
import { useLibrary, type Reference } from '../state/library';
import { ConfirmButton } from './ConfirmButton';
import { Icon } from './Icon';

/// The canvas document editor — note & reference cards on an infinite pan/zoom
/// surface joined by typed edges, wired to the J1 reference library. Built on
/// React Flow (`@xyflow/react`, MIT): the interaction layer (pan/zoom, drag,
/// marquee multi-select, NodeResizer, side-anchored edges, minimap, controls,
/// delete-key) is React Flow's; the model, its JSON Canvas 1.0 serialization,
/// the reference cards, typed edges, backlink inspector, colors, and undo are
/// ours (`state/canvas.ts`). React Flow is render+interaction only — swappable
/// the way the webview is contained in BrowserView.
///
/// State: the board is parsed from the owning document's `body` on mount (keyed
/// per doc by the caller, so switching docs remounts) and every mutation is
/// serialized back through `onChange`. An unrecognized body opens **read-only**
/// (a notice, no writes) so a file we didn't understand is never overwritten.

// ── React Flow data shapes (type aliases, so they satisfy Node<T>'s Record
//    constraint — an interface would not). ────────────────────────────────────
type NodeData = {
  kind: NodeType;
  text?: string;
  url?: string;
  refId?: string;
  label?: string;
  background?: string;
  file?: string;
  subpath?: string;
  color?: string;
  raw?: Record<string, unknown>;
};
type EdgeData = {
  edgeType?: EdgeType;
  label?: string;
  color?: string;
  raw?: Record<string, unknown>;
};
type RFNode = Node<NodeData>;
type RFEdge = Edge<EdgeData>;

// Custom-node/edge components can't take handler props (React Flow owns their
// props), so the board's mutators + reference lookup ride a context.
interface CanvasCtx {
  refById: Map<string, Reference>;
  readOnly: boolean;
  t: (k: string) => string;
  setNoteText: (id: string, text: string) => void;
  cycleEdge: (id: string) => void;
  removeEdge: (id: string) => void;
}
const CanvasContext = createContext<CanvasCtx | null>(null);
function useCanvasCtx(): CanvasCtx {
  const c = useContext(CanvasContext);
  if (c === null) throw new Error('CanvasContext missing');
  return c;
}

// ── Board ↔ React Flow mapping ───────────────────────────────────────────────
function rfType(t: NodeType): string {
  return t === 'link' ? 'ref' : t === 'group' ? 'group' : t === 'text' ? 'text' : 'inert';
}
function nodeToRf(n: CanvasNode): RFNode {
  return {
    id: n.id,
    type: rfType(n.type),
    position: { x: n.x, y: n.y },
    width: n.width,
    height: n.height,
    // A group is a backdrop — it sits behind the cards and never steals a click
    // meant for a card on top of it.
    ...(n.type === 'group' ? { zIndex: 0 } : { zIndex: 1 }),
    data: {
      kind: n.type,
      text: n.text,
      url: n.url,
      refId: n.refId,
      label: n.label,
      background: n.background,
      file: n.file,
      subpath: n.subpath,
      color: n.color,
      raw: n.raw,
    },
  };
}
function edgeToRf(e: { id: string; fromNode: string; toNode: string; fromSide?: Side; toSide?: Side; color?: string; label?: string; edgeType?: EdgeType; raw?: Record<string, unknown> }): RFEdge {
  return {
    id: e.id,
    source: e.fromNode,
    target: e.toNode,
    sourceHandle: e.fromSide,
    targetHandle: e.toSide,
    type: 'typed',
    data: { edgeType: e.edgeType, label: e.label, color: e.color, raw: e.raw },
    markerEnd: { type: MarkerType.ArrowClosed },
  };
}
function boardToRf(b: Board): { nodes: RFNode[]; edges: RFEdge[] } {
  return { nodes: b.nodes.map(nodeToRf), edges: b.edges.map(edgeToRf) };
}
function rfToBoard(nodes: RFNode[], edges: RFEdge[]): Board {
  return {
    nodes: nodes.map((rn) => {
      const d = rn.data;
      return {
        id: rn.id,
        type: d.kind,
        x: rn.position.x,
        y: rn.position.y,
        width: rn.width ?? rn.measured?.width ?? DEFAULT_W,
        height: rn.height ?? rn.measured?.height ?? DEFAULT_H,
        color: d.color,
        text: d.text,
        url: d.url,
        refId: d.refId,
        label: d.label,
        background: d.background,
        file: d.file,
        subpath: d.subpath,
        raw: d.raw,
      };
    }),
    edges: edges.map((re) => {
      const d = re.data ?? {};
      const fs = typeof re.sourceHandle === 'string' ? (re.sourceHandle as Side) : undefined;
      const ts = typeof re.targetHandle === 'string' ? (re.targetHandle as Side) : undefined;
      return {
        id: re.id,
        fromNode: re.source,
        toNode: re.target,
        fromSide: fs,
        toSide: ts,
        color: d.color,
        label: d.label,
        edgeType: d.edgeType,
        raw: d.raw,
      };
    }),
  };
}

// ── Handles: a source + target on each of the four sides, so an edge can anchor
//    to any side (mapping 1:1 to JSON Canvas fromSide/toSide). ─────────────────
const SIDES: { side: Side; pos: Position }[] = [
  { side: 'top', pos: Position.Top },
  { side: 'right', pos: Position.Right },
  { side: 'bottom', pos: Position.Bottom },
  { side: 'left', pos: Position.Left },
];
function SideHandles({ connectable }: { connectable: boolean }): JSX.Element {
  return (
    <>
      {SIDES.map(({ side, pos }) => (
        <Fragment key={side}>
          <Handle type="target" id={side} position={pos} className="canvas-handle" isConnectable={connectable} />
          <Handle type="source" id={side} position={pos} className="canvas-handle" isConnectable={connectable} />
        </Fragment>
      ))}
    </>
  );
}

// A node's color rides as a left accent bar, applied inline (a runtime value,
// not a design token — so no CSS custom property to define).
function nodeColorStyle(color: string | undefined): React.CSSProperties {
  const c = colorCss(color);
  return c !== undefined ? { borderLeftColor: c, borderLeftWidth: 3, borderLeftStyle: 'solid' } : {};
}

// ── Custom nodes ─────────────────────────────────────────────────────────────
function TextNode({ id, data, selected }: NodeProps<RFNode>): JSX.Element {
  const ctx = useCanvasCtx();
  return (
    <div className={`canvas-node text${selected === true ? ' selected' : ''}`} style={nodeColorStyle(data.color)}>
      <NodeResizer isVisible={selected === true && !ctx.readOnly} minWidth={140} minHeight={72} />
      <SideHandles connectable={!ctx.readOnly} />
      {/* Drag handle: the textarea fills the card and carries `nodrag` (so typing
          never starts a drag), which would leave a note card with no draggable
          surface at all. This grip strip is that surface. */}
      <div className="canvas-node-grip" title={ctx.t('canvas.dragHandle')}>
        <span className="canvas-node-grip-dots" aria-hidden="true" />
      </div>
      <textarea
        className="canvas-node-note nodrag nowheel"
        value={data.text ?? ''}
        placeholder={ctx.t('canvas.notePlaceholder')}
        readOnly={ctx.readOnly}
        onChange={(e) => ctx.setNoteText(id, e.target.value)}
      />
    </div>
  );
}

function RefNode({ data, selected }: NodeProps<RFNode>): JSX.Element {
  const ctx = useCanvasCtx();
  const ref = data.refId !== undefined ? ctx.refById.get(data.refId) : undefined;
  return (
    <div className={`canvas-node ref${selected === true ? ' selected' : ''}`} style={nodeColorStyle(data.color)}>
      <NodeResizer isVisible={selected === true && !ctx.readOnly} minWidth={160} minHeight={90} />
      <SideHandles connectable={!ctx.readOnly} />
      <div className="canvas-node-head">
        <span className="canvas-node-kind">❋</span>
        <span className="canvas-node-title">{ref !== undefined && ref.title !== '' ? ref.title : ctx.t('canvas.missingRef')}</span>
      </div>
      {ref !== undefined ? (
        <div className="canvas-node-ref nowheel">
          <div className="canvas-node-ref-meta muted small">
            {ref.authors.slice(0, 2).join(', ')}
            {ref.authors.length > 2 ? ' et al.' : ''}
            {ref.year !== undefined ? ` · ${ref.year}` : ''}
          </div>
          {ref.tldr !== undefined && <div className="canvas-node-ref-tldr">{ref.tldr}</div>}
        </div>
      ) : (
        <div className="muted small canvas-node-ref">{ctx.t('canvas.missingRef')}</div>
      )}
    </div>
  );
}

function GroupNode({ data, selected }: NodeProps<RFNode>): JSX.Element {
  const ctx = useCanvasCtx();
  const bg = colorCss(data.color) ?? data.background;
  return (
    <div className={`canvas-node group${selected === true ? ' selected' : ''}`} style={bg !== undefined ? { background: `color-mix(in srgb, ${bg} 12%, transparent)`, borderColor: bg } : undefined}>
      <NodeResizer isVisible={selected === true && !ctx.readOnly} minWidth={120} minHeight={80} />
      <SideHandles connectable={!ctx.readOnly} />
      {data.label !== undefined && data.label !== '' && <div className="canvas-node-grouplabel">{data.label}</div>}
    </div>
  );
}

function InertNode({ data }: NodeProps<RFNode>): JSX.Element {
  const ctx = useCanvasCtx();
  return (
    <div className="canvas-node inert">
      <SideHandles connectable={!ctx.readOnly} />
      <div className="canvas-node-head">
        <span className="canvas-node-kind">▤</span>
        <span className="canvas-node-title">{data.file ?? data.label ?? ctx.t('canvas.inertNode')}</span>
      </div>
      {data.subpath !== undefined && <div className="muted small canvas-node-ref">{data.subpath}</div>}
    </div>
  );
}

const NODE_TYPES = { text: TextNode, ref: RefNode, group: GroupNode, inert: InertNode };

// ── Custom typed edge (labeled + click-to-cycle + delete) ────────────────────
function TypedEdge(props: EdgeProps<RFEdge>): JSX.Element {
  const ctx = useCanvasCtx();
  const [path, labelX, labelY] = getBezierPath({
    sourceX: props.sourceX,
    sourceY: props.sourceY,
    sourcePosition: props.sourcePosition,
    targetX: props.targetX,
    targetY: props.targetY,
    targetPosition: props.targetPosition,
  });
  const et = props.data?.edgeType;
  return (
    <>
      <BaseEdge id={props.id} path={path} markerEnd={props.markerEnd} />
      <EdgeLabelRenderer>
        <div
          className="canvas-edge-label nodrag nopan"
          style={{ position: 'absolute', transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`, pointerEvents: 'all' }}
        >
          {ctx.readOnly ? (
            <span className="canvas-edge-type">{props.data?.label ?? (et !== undefined ? ctx.t(`canvas.edge.${et}`) : '')}</span>
          ) : (
            <>
              <button className="canvas-edge-type" title={ctx.t('canvas.cycleType')} onClick={() => ctx.cycleEdge(props.id)}>
                {ctx.t(`canvas.edge.${et ?? 'relates'}`)}
              </button>
              <button className="canvas-edge-x" title={ctx.t('canvas.removeEdge')} onClick={() => ctx.removeEdge(props.id)}>
                ×
              </button>
            </>
          )}
        </div>
      </EdgeLabelRenderer>
    </>
  );
}
const EDGE_TYPES_MAP = { typed: TypedEdge };

// ── The board (inside a ReactFlowProvider) ───────────────────────────────────
function Board({ value, onChange }: { value: string; onChange: (next: string) => void }): JSX.Element {
  const t = useT();
  const references = useLibrary((s) => s.references);
  const rf = useReactFlow();
  const wrapperRef = useRef<HTMLDivElement>(null);

  const initial = useMemo(() => parseCanvas(value), [value]);
  const readOnly = initial.readOnly === true;
  const [{ nodes: initNodes, edges: initEdges }] = useState(() => boardToRf(initial));
  const [nodes, setNodes] = useState<RFNode[]>(initNodes);
  const [edges, setEdges] = useState<RFEdge[]>(initEdges);
  // Right-click context menu: `pane` opens over empty canvas (add-note at cursor),
  // `node` opens over a card (recolor / delete). `sx/sy` are viewport pixels for
  // the fixed-positioned menu; `flow` is the cursor in board coords.
  const [menu, setMenu] = useState<
    | { kind: 'pane'; sx: number; sy: number; flow: { x: number; y: number } }
    | { kind: 'node'; sx: number; sy: number; nodeId: string }
    | null
  >(null);
  // Top-level fields beyond nodes/edges (a future spec version's extras) ride
  // along so every serialize writes them back — same policy as per-node `raw`.
  const rootRef = useRef(initial.rawRoot);

  // Refs are the source of truth inside callbacks (no stale closures); state
  // drives the render.
  const nodesRef = useRef(nodes);
  nodesRef.current = nodes;
  const edgesRef = useRef(edges);
  edgesRef.current = edges;
  const tRef = useRef(t);
  tRef.current = t;
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;
  const history = useRef(createHistory());

  const refById = useMemo(() => {
    const m = new Map<string, Reference>();
    references.forEach((r) => m.set(r.id, r));
    return m;
  }, [references]);

  const snapshot = useCallback(
    (): string => serializeCanvas({ ...rfToBoard(nodesRef.current, edgesRef.current), rawRoot: rootRef.current }),
    [],
  );
  const pushHistory = useCallback((): void => history.current.push(snapshot()), [snapshot]);
  const emit = useCallback(
    (ns: RFNode[], es: RFEdge[]): void => {
      if (readOnly) return;
      onChangeRef.current(serializeCanvas({ ...rfToBoard(ns, es), rawRoot: rootRef.current }));
    },
    [readOnly],
  );

  const commitNodes = useCallback(
    (ns: RFNode[]): void => {
      nodesRef.current = ns;
      setNodes(ns);
      emit(ns, edgesRef.current);
    },
    [emit],
  );
  const commitEdges = useCallback(
    (es: RFEdge[]): void => {
      edgesRef.current = es;
      setEdges(es);
      emit(nodesRef.current, es);
    },
    [emit],
  );

  // A change worth persisting: not a mere selection, and not a measurement-only
  // `dimensions` event — React Flow measures every node's DOM after mount and
  // reports it as a dimensions change with neither `setAttributes` nor
  // `resizing`; emitting on those would rewrite (and dirty, #315-class) the body
  // of every file-backed board on open. A real NodeResizer resize carries
  // `resizing` (true while dragging, false at the end) and/or `setAttributes`.
  const persistable = (c: NodeChange<RFNode>): boolean => {
    if (c.type === 'select') return false;
    if (c.type === 'dimensions' && c.setAttributes === undefined && c.resizing === undefined) return false;
    return true;
  };
  // True while a NodeResizer drag is in flight — its start is the undoable step.
  const resizingRef = useRef(false);
  const onNodesChange = useCallback(
    (changes: NodeChange<RFNode>[]): void => {
      // A removal (delete-key / control) is a discrete undoable step; so is the
      // START of a resize drag (snapshot the pre-resize board once, not per tick).
      if (changes.some((c) => c.type === 'remove')) pushHistory();
      for (const c of changes) {
        if (c.type !== 'dimensions') continue;
        if (c.resizing === true && !resizingRef.current) {
          pushHistory();
          resizingRef.current = true;
        }
        if (c.resizing === false) resizingRef.current = false;
      }
      const next = applyNodeChanges(changes, nodesRef.current);
      nodesRef.current = next;
      setNodes(next);
      if (changes.some(persistable)) emit(next, edgesRef.current);
    },
    [emit, pushHistory],
  );
  const onEdgesChange = useCallback(
    (changes: EdgeChange<RFEdge>[]): void => {
      if (changes.some((c) => c.type === 'remove')) pushHistory();
      const next = applyEdgeChanges(changes, edgesRef.current);
      edgesRef.current = next;
      setEdges(next);
      if (changes.some((c) => c.type !== 'select')) emit(nodesRef.current, next);
    },
    [emit, pushHistory],
  );
  const onConnect = useCallback(
    (c: Connection): void => {
      if (readOnly) return;
      pushHistory();
      const et: EdgeType = 'relates';
      const edge: RFEdge = {
        id: newId('e'),
        source: c.source,
        target: c.target,
        sourceHandle: c.sourceHandle ?? undefined,
        targetHandle: c.targetHandle ?? undefined,
        type: 'typed',
        data: { edgeType: et, label: tRef.current(`canvas.edge.${et}`) },
        markerEnd: { type: MarkerType.ArrowClosed },
      };
      commitEdges(rfAddEdge(edge, edgesRef.current));
    },
    [commitEdges, pushHistory, readOnly],
  );
  const onNodeDragStart = useCallback((): void => pushHistory(), [pushHistory]);

  // ── Domain mutators exposed to the custom node/edge components ──────────────
  const setNoteText = useCallback(
    (id: string, text: string): void => {
      commitNodes(nodesRef.current.map((n) => (n.id === id ? { ...n, data: { ...n.data, text } } : n)));
    },
    [commitNodes],
  );
  const cycleEdge = useCallback(
    (id: string): void => {
      pushHistory();
      commitEdges(
        edgesRef.current.map((e) => {
          if (e.id !== id) return e;
          const cur = EDGE_TYPES.indexOf(e.data?.edgeType ?? 'relates');
          const next = EDGE_TYPES[(cur + 1) % EDGE_TYPES.length];
          return { ...e, data: { ...e.data, edgeType: next, label: tRef.current(`canvas.edge.${next}`) } };
        }),
      );
    },
    [commitEdges, pushHistory],
  );
  const removeEdge = useCallback(
    (id: string): void => {
      pushHistory();
      commitEdges(edgesRef.current.filter((e) => e.id !== id));
    },
    [commitEdges, pushHistory],
  );
  const setColor = useCallback(
    (id: string, color: string | undefined): void => {
      pushHistory();
      commitNodes(nodesRef.current.map((n) => (n.id === id ? { ...n, data: { ...n.data, color } } : n)));
    },
    [commitNodes, pushHistory],
  );
  const removeNode = useCallback(
    (id: string): void => {
      pushHistory();
      const ns = nodesRef.current.filter((n) => n.id !== id);
      const es = edgesRef.current.filter((e) => e.source !== id && e.target !== id);
      nodesRef.current = ns;
      edgesRef.current = es;
      setNodes(ns);
      setEdges(es);
      emit(ns, es);
    },
    [emit, pushHistory],
  );

  const ctx = useMemo<CanvasCtx>(
    () => ({ refById, readOnly, t, setNoteText, cycleEdge, removeEdge }),
    [refById, readOnly, t, setNoteText, cycleEdge, removeEdge],
  );

  // ── Toolbar actions ────────────────────────────────────────────────────────
  const centerFlow = useCallback((): { x: number; y: number } => {
    const rect = wrapperRef.current?.getBoundingClientRect();
    if (rect === undefined) return { x: 0, y: 0 };
    return rf.screenToFlowPosition({ x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 });
  }, [rf]);

  // `at` (flow coords) places the note under the cursor for the right-click menu;
  // the toolbar button passes nothing and drops it in the viewport centre.
  const addNote = useCallback((at?: { x: number; y: number }): void => {
    if (readOnly) return;
    pushHistory();
    const p = at ?? centerFlow();
    const node = nodeToRf({ id: newId('n'), type: 'text', x: p.x - DEFAULT_W / 2, y: p.y - DEFAULT_H / 2, width: DEFAULT_W, height: DEFAULT_H, text: '' });
    node.selected = true;
    commitNodes([...nodesRef.current.map((n) => ({ ...n, selected: false })), node]);
  }, [centerFlow, commitNodes, pushHistory, readOnly]);

  const addRefCard = useCallback(
    (refId: string): void => {
      if (readOnly || refId === '') return;
      pushHistory();
      const p = centerFlow();
      const node = nodeToRf({ id: newId('n'), type: 'link', x: p.x - DEFAULT_W / 2, y: p.y - DEFAULT_H / 2, width: DEFAULT_W, height: DEFAULT_H, refId, url: `termipod://ref/${refId}` });
      node.selected = true;
      commitNodes([...nodesRef.current.map((n) => ({ ...n, selected: false })), node]);
    },
    [centerFlow, commitNodes, pushHistory, readOnly],
  );

  const groupSelection = useCallback((): void => {
    if (readOnly) return;
    const sel = nodesRef.current.filter((n) => n.selected === true && n.data.kind !== 'group');
    if (sel.length < 1) return;
    pushHistory();
    const b = getNodesBounds(sel);
    const pad = 24;
    const group = nodeToRf({
      id: newId('n'),
      type: 'group',
      x: b.x - pad,
      y: b.y - pad,
      width: b.width + pad * 2,
      height: b.height + pad * 2,
      label: t('canvas.group'),
    });
    // Prepend so the backdrop renders behind the members.
    commitNodes([group, ...nodesRef.current]);
  }, [commitNodes, pushHistory, readOnly, t]);

  // ── Right-click context menu ────────────────────────────────────────────────
  const closeMenu = useCallback((): void => setMenu(null), []);
  const onPaneContextMenu = useCallback(
    (e: React.MouseEvent | MouseEvent): void => {
      if (readOnly) return;
      e.preventDefault();
      setMenu({ kind: 'pane', sx: e.clientX, sy: e.clientY, flow: rf.screenToFlowPosition({ x: e.clientX, y: e.clientY }) });
    },
    [readOnly, rf],
  );
  const onNodeContextMenu = useCallback(
    (e: React.MouseEvent, node: RFNode): void => {
      if (readOnly) return;
      e.preventDefault();
      // Select the target so the menu's recolor/delete act on it (and the
      // inspector reflects it) — selection is view state, so no emit.
      const ns = nodesRef.current.map((n) => ({ ...n, selected: n.id === node.id }));
      nodesRef.current = ns;
      setNodes(ns);
      setMenu({ kind: 'node', sx: e.clientX, sy: e.clientY, nodeId: node.id });
    },
    [readOnly],
  );

  const applyBoardString = useCallback(
    (s: string): void => {
      const b = parseCanvas(s);
      rootRef.current = b.rawRoot; // undo/redo snapshots carry the extras too
      const rfb = boardToRf(b);
      nodesRef.current = rfb.nodes;
      edgesRef.current = rfb.edges;
      setNodes(rfb.nodes);
      setEdges(rfb.edges);
      emit(rfb.nodes, rfb.edges);
    },
    [emit],
  );
  const undo = useCallback((): void => {
    const prev = history.current.undo(snapshot());
    if (prev !== null) applyBoardString(prev);
  }, [applyBoardString, snapshot]);
  const redo = useCallback((): void => {
    const next = history.current.redo(snapshot());
    if (next !== null) applyBoardString(next);
  }, [applyBoardString, snapshot]);

  // Undo/redo shortcuts — suppressed while a text field owns focus so the
  // textarea/inspector keeps its own edit history (native).
  useEffect(() => {
    const onKey = (e: KeyboardEvent): void => {
      if (!(e.metaKey || e.ctrlKey) || e.key.toLowerCase() !== 'z') return;
      const el = document.activeElement;
      const typing = el instanceof HTMLElement && (el.tagName === 'TEXTAREA' || el.tagName === 'INPUT' || el.isContentEditable);
      if (typing) return;
      e.preventDefault();
      if (e.shiftKey) redo();
      else undo();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [undo, redo]);

  const selectedNode = nodes.find((n) => n.selected === true);

  return (
    <div className="canvas-editor">
      <div className="author-doc-bar canvas-toolbar">
        <button className="import-btn" onClick={() => addNote()} disabled={readOnly}>
          <Icon name="plus" size={14} />
          {t('canvas.addNote')}
        </button>
        <select
          className="canvas-add-ref"
          value=""
          disabled={readOnly || references.length === 0}
          onChange={(e) => addRefCard(e.target.value)}
        >
          <option value="">+ {t('canvas.addRef')}</option>
          {references.map((r) => (
            <option key={r.id} value={r.id}>
              {r.title !== '' ? r.title : t('read.untitled')}
            </option>
          ))}
        </select>
        <button className="import-btn" onClick={groupSelection} disabled={readOnly}>
          {t('canvas.group')}
        </button>
        <span className="canvas-toolbar-sep" />
        <button className="import-btn" onClick={undo} disabled={readOnly} title={t('canvas.undo')}>
          <Icon name="undo" size={15} />
        </button>
        <button className="import-btn" onClick={redo} disabled={readOnly} title={t('canvas.redo')}>
          <Icon name="undo" size={15} className="mirror-x" />
        </button>
        <span className="spacer" />
        {readOnly && <span className="canvas-readonly muted small">{t('canvas.readOnlyNotice')}</span>}
        {nodes.length > 0 && !readOnly && (
          <ConfirmButton
            danger
            label={t('canvas.clear')}
            onConfirm={() => {
              // Snapshot first — Clear is destructive and must be one undo away.
              pushHistory();
              applyBoardString(serializeCanvas({ nodes: [], edges: [], rawRoot: rootRef.current }));
            }}
          />
        )}
      </div>

      <div className="canvas-layout">
        <div className="canvas-flow" ref={wrapperRef}>
          <CanvasContext.Provider value={ctx}>
            <ReactFlow
              nodes={nodes}
              edges={edges}
              nodeTypes={NODE_TYPES}
              edgeTypes={EDGE_TYPES_MAP}
              onNodesChange={onNodesChange}
              onEdgesChange={onEdgesChange}
              onConnect={onConnect}
              onNodeDragStart={onNodeDragStart}
              onPaneContextMenu={onPaneContextMenu}
              onNodeContextMenu={onNodeContextMenu}
              onPaneClick={closeMenu}
              onMoveStart={closeMenu}
              nodesDraggable={!readOnly}
              nodesConnectable={!readOnly}
              elementsSelectable
              deleteKeyCode={readOnly ? null : ['Delete', 'Backspace']}
              selectionOnDrag
              // Middle-button drag pans; the right button is reserved for the
              // context menu (so a right-click isn't swallowed by a pan).
              panOnDrag={[1]}
              minZoom={0.2}
              maxZoom={2.5}
              fitView
              proOptions={{ hideAttribution: true }}
            >
              <Background gap={18} />
              <Controls showInteractive={false} />
              <MiniMap pannable zoomable className="canvas-minimap" />
            </ReactFlow>
          </CanvasContext.Provider>
          {nodes.length === 0 && !readOnly && <div className="canvas-empty">{t('canvas.empty')}</div>}
          {menu !== null && !readOnly && (
            <CanvasMenu
              menu={menu}
              node={menu.kind === 'node' ? nodes.find((n) => n.id === menu.nodeId) : undefined}
              t={t}
              onClose={closeMenu}
              onAddNote={() => menu.kind === 'pane' && addNote(menu.flow)}
              onSetColor={(c) => menu.kind === 'node' && setColor(menu.nodeId, c)}
              onRemove={() => menu.kind === 'node' && removeNode(menu.nodeId)}
            />
          )}
        </div>

        {selectedNode !== undefined && (
          <aside className="canvas-inspector">
            <Inspector
              node={selectedNode}
              nodes={nodes}
              edges={edges}
              reference={selectedNode.data.refId !== undefined ? refById.get(selectedNode.data.refId) : undefined}
              readOnly={readOnly}
              onSetColor={setColor}
              onSetNote={setNoteText}
              onRemove={() => removeNode(selectedNode.id)}
              onSelect={(id) => {
                // Selection is view state, not board content — update locally
                // without emitting (an emit would rewrite + dirty the body).
                const ns = nodesRef.current.map((n) => ({ ...n, selected: n.id === id }));
                nodesRef.current = ns;
                setNodes(ns);
              }}
            />
          </aside>
        )}
      </div>
    </div>
  );
}

export function CanvasEditor({ value, onChange }: { value: string; onChange: (next: string) => void }): JSX.Element {
  return (
    <ReactFlowProvider>
      <Board value={value} onChange={onChange} />
    </ReactFlowProvider>
  );
}

// ── Right-click context menu ─────────────────────────────────────────────────
// A fixed-positioned popover over the flow surface with a full-screen backdrop
// that dismisses it. Pane menus add a note at the cursor; node menus recolor or
// delete the card (mutators are owned by Board and passed down).
function CanvasMenu(props: {
  menu: { kind: 'pane'; sx: number; sy: number } | { kind: 'node'; sx: number; sy: number };
  node: RFNode | undefined;
  t: (k: string) => string;
  onClose: () => void;
  onAddNote: () => void;
  onSetColor: (color: string | undefined) => void;
  onRemove: () => void;
}): JSX.Element {
  const { menu, node, t, onClose, onAddNote, onSetColor, onRemove } = props;
  useEffect(() => {
    const onKey = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const run = (fn: () => void) => (): void => {
    fn();
    onClose();
  };

  return (
    <>
      <div className="canvas-menu-backdrop" onClick={onClose} onContextMenu={(e) => { e.preventDefault(); onClose(); }} />
      <div className="canvas-menu" style={{ left: menu.sx, top: menu.sy }} onContextMenu={(e) => e.preventDefault()}>
        {menu.kind === 'pane' ? (
          <button className="canvas-menu-item" onClick={run(onAddNote)}>
            <Icon name="plus" size={14} />
            {t('canvas.addNoteHere')}
          </button>
        ) : (
          <>
            <div className="canvas-menu-colors">
              <button
                className={`canvas-swatch none${node?.data.color === undefined ? ' active' : ''}`}
                title={t('canvas.colorNone')}
                onClick={run(() => onSetColor(undefined))}
              />
              {CANVAS_PRESETS.map((p) => (
                <button
                  key={p}
                  className={`canvas-swatch${node?.data.color === p ? ' active' : ''}`}
                  style={{ background: colorCss(p) }}
                  title={t('canvas.color')}
                  onClick={run(() => onSetColor(p))}
                />
              ))}
            </div>
            <div className="canvas-menu-sep" />
            <button className="canvas-menu-item danger" onClick={run(onRemove)}>
              <Icon name="trash" size={14} />
              {t('canvas.deleteCard')}
            </button>
          </>
        )}
      </div>
    </>
  );
}

// ── Inspector ────────────────────────────────────────────────────────────────
function nodeTitle(n: RFNode | undefined, refById: Map<string, Reference>, t: (k: string) => string): string {
  if (n === undefined) return '';
  if (n.data.kind === 'link') {
    const r = n.data.refId !== undefined ? refById.get(n.data.refId) : undefined;
    return r !== undefined && r.title !== '' ? r.title : t('canvas.missingRef');
  }
  if (n.data.kind === 'group') return n.data.label !== undefined && n.data.label !== '' ? n.data.label : t('canvas.group');
  const line = (n.data.text ?? '').split('\n')[0].trim();
  return line !== '' ? line : t('canvas.untitledNote');
}

function Inspector(props: {
  node: RFNode;
  nodes: RFNode[];
  edges: RFEdge[];
  reference: Reference | undefined;
  readOnly: boolean;
  onSetColor: (id: string, color: string | undefined) => void;
  onSetNote: (id: string, text: string) => void;
  onRemove: () => void;
  onSelect: (id: string) => void;
}): JSX.Element {
  const { node, nodes, edges, reference, readOnly, onSetColor, onSetNote, onRemove, onSelect } = props;
  const t = useT();
  const references = useLibrary((s) => s.references);
  const refById = useMemo(() => {
    const m = new Map<string, Reference>();
    references.forEach((r) => m.set(r.id, r));
    return m;
  }, [references]);

  const outgoing = edges.filter((e) => e.source === node.id);
  const incoming = edges.filter((e) => e.target === node.id);
  const byId = (id: string): RFNode | undefined => nodes.find((n) => n.id === id);

  return (
    <div className="canvas-inspector-body scroll">
      <div className="canvas-insp-kind muted small">
        {node.data.kind === 'link' ? t('canvas.refCard') : node.data.kind === 'group' ? t('canvas.group') : node.data.kind === 'text' ? t('canvas.noteCard') : t('canvas.inertNode')}
      </div>

      {node.data.kind === 'link' ? (
        <div className="canvas-insp-ref">
          <div className="canvas-insp-title">{reference !== undefined && reference.title !== '' ? reference.title : t('canvas.missingRef')}</div>
          {reference !== undefined && (
            <>
              <div className="muted small">
                {reference.authors.join(', ')}
                {reference.year !== undefined ? ` · ${reference.year}` : ''}
              </div>
              {reference.venue !== undefined && reference.venue !== '' && <div className="muted small">{reference.venue}</div>}
              {reference.abstract !== undefined && reference.abstract !== '' && <p className="canvas-insp-abstract">{reference.abstract}</p>}
            </>
          )}
        </div>
      ) : node.data.kind === 'text' ? (
        <textarea
          className="canvas-insp-note editor-pane"
          value={node.data.text ?? ''}
          placeholder={t('canvas.notePlaceholder')}
          readOnly={readOnly}
          onChange={(e) => onSetNote(node.id, e.target.value)}
        />
      ) : (
        <div className="canvas-insp-title">{nodeTitle(node, refById, t)}</div>
      )}

      {!readOnly && (
        <div className="canvas-insp-colors">
          <button
            className={`canvas-swatch none${node.data.color === undefined ? ' active' : ''}`}
            title={t('canvas.colorNone')}
            onClick={() => onSetColor(node.id, undefined)}
          />
          {CANVAS_PRESETS.map((p) => (
            <button
              key={p}
              className={`canvas-swatch${node.data.color === p ? ' active' : ''}`}
              style={{ background: colorCss(p) }}
              title={t('canvas.color')}
              onClick={() => onSetColor(node.id, p)}
            />
          ))}
        </div>
      )}

      {!readOnly && (
        <div className="canvas-insp-actions">
          <button className="link-btn danger" onClick={onRemove}>
            {t('canvas.deleteCard')}
          </button>
        </div>
      )}

      {(outgoing.length > 0 || incoming.length > 0) && (
        <div className="canvas-links">
          {outgoing.length > 0 && (
            <div className="canvas-links-group">
              <div className="canvas-links-label">{t('canvas.linksOut')}</div>
              {outgoing.map((e) => (
                <button key={e.id} className="canvas-link-row" onClick={() => onSelect(e.target)}>
                  <span className="canvas-link-type">{t(`canvas.edge.${e.data?.edgeType ?? 'relates'}`)}</span>
                  <span className="canvas-link-target">{nodeTitle(byId(e.target), refById, t)}</span>
                </button>
              ))}
            </div>
          )}
          {incoming.length > 0 && (
            <div className="canvas-links-group">
              <div className="canvas-links-label">{t('canvas.backlinks')}</div>
              {incoming.map((e) => (
                <button key={e.id} className="canvas-link-row" onClick={() => onSelect(e.source)}>
                  <span className="canvas-link-target">{nodeTitle(byId(e.source), refById, t)}</span>
                  <span className="canvas-link-type">{t(`canvas.edge.${e.data?.edgeType ?? 'relates'}`)}</span>
                </button>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
