/// J2 Author — the **canvas** document model, in **JSON Canvas 1.0**
/// (jsoncanvas.org, MIT). A board is nodes on an infinite surface joined by
/// edges; a node is a free `text` note, a `link` onto a library `Reference` (so
/// the board is wired to J1's reference library, a Zettelkasten — not a
/// disconnected whiteboard), a `group`, or an inert `file`. Serialized as JSON
/// into the owning Author document's `body`, and the SAME shape as an Obsidian
/// `.canvas` on disk, so a board round-trips between the two apps.
///
/// This module is the pure model + (de)serialization; `ui/CanvasEditor.tsx` is
/// the interactive editor (built on React Flow). Three parse outcomes:
///   • a body with a `nodes` array   → JSON Canvas (editable);
///   • a body with a `cards` array   → the legacy `{cards,edges}` board,
///     converted on parse (upgraded to JSON Canvas on the next save);
///   • empty/whitespace              → a new, empty, editable board;
///   • anything else                 → **read-only** (`readOnly: true`), never
///     serialized back, so we can NEVER overwrite a file we didn't understand
///     (including a future spec version) with an empty board.
///
/// Our two extensions ride in a namespaced `"x-termipod"` bag so a foreign app
/// degrades gracefully: a ref card is a `link` node (Obsidian shows a link card;
/// we resolve the live reference via `x-termipod.refId`), and a typed edge is a
/// labeled edge (the type's display text in `label`; `x-termipod.edgeType` is the
/// machine discriminator). Unknown node types and unknown fields are preserved
/// verbatim through parse→serialize (kept in each item's `raw`).

export type EdgeType = 'relates' | 'supports' | 'refutes' | 'cites' | 'leads';
export const EDGE_TYPES: EdgeType[] = ['relates', 'supports', 'refutes', 'cites', 'leads'];

export type NodeType = 'text' | 'link' | 'group' | 'file' | 'unknown';
export type Side = 'top' | 'right' | 'bottom' | 'left';

/// JSON Canvas preset colors are the strings "1".."6"; a `color` may also be any
/// hex string. These are the six the spec + Obsidian define, mapped to CSS for
/// our rendering (a hex value is used directly).
export const CANVAS_PRESETS = ['1', '2', '3', '4', '5', '6'] as const;
export const PRESET_CSS: Record<string, string> = {
  '1': '#e5534b', // red
  '2': '#d98c3f', // orange
  '3': '#d9b34a', // yellow
  '4': '#4fae5a', // green
  '5': '#43a5c0', // cyan
  '6': '#9a6dd7', // purple
};

/// CSS for a JSON Canvas `color` (preset digit or hex), or undefined when unset.
export function colorCss(color: string | undefined): string | undefined {
  if (color === undefined || color === '') return undefined;
  return PRESET_CSS[color] ?? color;
}

export interface CanvasNode {
  id: string;
  type: NodeType;
  x: number;
  y: number;
  width: number;
  height: number;
  color?: string;
  text?: string; // text node — markdown body
  url?: string; // link node — the URL (our ref cards use `termipod://ref/<id>`)
  refId?: string; // library Reference id (our ref cards; from `x-termipod.refId`)
  label?: string; // group / file label
  background?: string; // group background
  file?: string; // file node — path
  subpath?: string; // file node — heading/block subpath
  /// Original JSON Canvas node object, so unknown fields survive a save.
  raw?: Record<string, unknown>;
}

export interface CanvasEdge {
  id: string;
  fromNode: string;
  toNode: string;
  fromSide?: Side;
  toSide?: Side;
  color?: string;
  label?: string;
  edgeType?: EdgeType; // our typed edges (from `x-termipod.edgeType`)
  /// Original JSON Canvas edge object, so unknown fields survive a save.
  raw?: Record<string, unknown>;
}

export interface Board {
  nodes: CanvasNode[];
  edges: CanvasEdge[];
  /// An unrecognized body opens read-only (never serialized back).
  readOnly?: boolean;
  /// The parsed top-level object of a JSON Canvas body, so top-level fields
  /// beyond `nodes`/`edges` (a future spec version, another app's extras)
  /// survive a save the same way per-node/edge unknowns do via `raw`.
  rawRoot?: Record<string, unknown>;
}

export const emptyBoard = (): Board => ({ nodes: [], edges: [] });

export const DEFAULT_W = 260;
export const DEFAULT_H = 120;
export const REF_URL_PREFIX = 'termipod://ref/';

// Unique id. The renderer serves from the secure `app://` origin, so
// crypto.randomUUID is available (ADR-055 §7).
export function newId(prefix: string): string {
  return `${prefix}${crypto.randomUUID()}`;
}

function isObj(v: unknown): v is Record<string, unknown> {
  return v !== null && typeof v === 'object' && !Array.isArray(v);
}
function num(v: unknown, d: number): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : d;
}
function side(v: unknown): Side | undefined {
  return v === 'top' || v === 'right' || v === 'bottom' || v === 'left' ? v : undefined;
}
function refIdFromUrl(url: string | undefined): string | undefined {
  return url !== undefined && url.startsWith(REF_URL_PREFIX) ? url.slice(REF_URL_PREFIX.length) : undefined;
}
function xExt(raw: Record<string, unknown>): Record<string, unknown> | undefined {
  const x = raw['x-termipod'];
  return isObj(x) ? x : undefined;
}

function parseNode(raw: Record<string, unknown>): CanvasNode | null {
  const id = typeof raw.id === 'string' ? raw.id : null;
  if (id === null) return null;
  const rt = raw.type;
  const type: NodeType = rt === 'text' || rt === 'link' || rt === 'group' || rt === 'file' ? rt : 'unknown';
  const ext = xExt(raw);
  const node: CanvasNode = {
    id,
    type,
    x: num(raw.x, 0),
    y: num(raw.y, 0),
    width: num(raw.width, DEFAULT_W),
    height: num(raw.height, DEFAULT_H),
    color: typeof raw.color === 'string' ? raw.color : undefined,
    raw,
  };
  if (type === 'text' && typeof raw.text === 'string') node.text = raw.text;
  if (type === 'link') {
    if (typeof raw.url === 'string') node.url = raw.url;
    const rid = ext !== undefined && typeof ext.refId === 'string' ? ext.refId : refIdFromUrl(node.url);
    if (rid !== undefined) node.refId = rid;
  }
  if (type === 'group') {
    if (typeof raw.label === 'string') node.label = raw.label;
    if (typeof raw.background === 'string') node.background = raw.background;
  }
  if (type === 'file') {
    if (typeof raw.file === 'string') node.file = raw.file;
    if (typeof raw.subpath === 'string') node.subpath = raw.subpath;
  }
  return node;
}

function parseEdge(raw: Record<string, unknown>): CanvasEdge | null {
  const id = typeof raw.id === 'string' ? raw.id : null;
  const fromNode = typeof raw.fromNode === 'string' ? raw.fromNode : null;
  const toNode = typeof raw.toNode === 'string' ? raw.toNode : null;
  if (id === null || fromNode === null || toNode === null) return null;
  const ext = xExt(raw);
  const et = ext !== undefined && typeof ext.edgeType === 'string' ? ext.edgeType : undefined;
  return {
    id,
    fromNode,
    toNode,
    fromSide: side(raw.fromSide),
    toSide: side(raw.toSide),
    color: typeof raw.color === 'string' ? raw.color : undefined,
    label: typeof raw.label === 'string' ? raw.label : undefined,
    edgeType: et !== undefined && (EDGE_TYPES as string[]).includes(et) ? (et as EdgeType) : undefined,
    raw,
  };
}

// Convert a legacy `{cards, edges}` board (the pre-JSON-Canvas termipod shape)
// into JSON Canvas nodes/edges. Cards had a fixed 210px width and no height.
function convertLegacy(obj: Record<string, unknown>): Board {
  const cards = (obj.cards as unknown[]).filter(isObj);
  const nodes: CanvasNode[] = cards.map((c) => {
    const id = typeof c.id === 'string' ? c.id : newId('n');
    const isRef = c.kind === 'ref';
    const refId = typeof c.refId === 'string' ? c.refId : undefined;
    return {
      id,
      type: isRef ? 'link' : 'text',
      x: num(c.x, 0),
      y: num(c.y, 0),
      width: 210,
      height: DEFAULT_H,
      text: !isRef && typeof c.text === 'string' ? c.text : undefined,
      url: isRef && refId !== undefined ? REF_URL_PREFIX + refId : undefined,
      refId: isRef ? refId : undefined,
    };
  });
  const rawEdges = Array.isArray(obj.edges) ? (obj.edges as unknown[]).filter(isObj) : [];
  const edges: CanvasEdge[] = rawEdges
    .map((e): CanvasEdge => {
      const type =
        typeof e.type === 'string' && (EDGE_TYPES as string[]).includes(e.type) ? (e.type as EdgeType) : 'relates';
      return {
        id: typeof e.id === 'string' ? e.id : newId('e'),
        fromNode: typeof e.from === 'string' ? e.from : '',
        toNode: typeof e.to === 'string' ? e.to : '',
        edgeType: type,
      };
    })
    .filter((e) => e.fromNode !== '' && e.toNode !== '');
  return { nodes, edges };
}

/// Parse a document body into a board (see the module header for the four
/// outcomes). Never throws; an unrecognized body is read-only, not empty.
export function parseCanvas(body: string): Board {
  const trimmed = body.trim();
  if (trimmed === '') return emptyBoard();
  let data: unknown;
  try {
    data = JSON.parse(trimmed);
  } catch {
    return { nodes: [], edges: [], readOnly: true };
  }
  if (!isObj(data)) return { nodes: [], edges: [], readOnly: true };
  if (Array.isArray(data.nodes)) {
    const nodes = data.nodes.filter(isObj).map(parseNode).filter((n): n is CanvasNode => n !== null);
    const edges = Array.isArray(data.edges)
      ? data.edges.filter(isObj).map(parseEdge).filter((e): e is CanvasEdge => e !== null)
      : [];
    return { nodes, edges, rawRoot: data };
  }
  if (Array.isArray(data.cards)) return convertLegacy(data);
  return { nodes: [], edges: [], readOnly: true };
}

function serializeNode(n: CanvasNode): Record<string, unknown> {
  const out: Record<string, unknown> = { ...(n.raw ?? {}) };
  out.id = n.id;
  if (n.type !== 'unknown') out.type = n.type;
  out.x = Math.round(n.x);
  out.y = Math.round(n.y);
  out.width = Math.round(n.width);
  out.height = Math.round(n.height);
  if (n.color !== undefined) out.color = n.color;
  else delete out.color;
  if (n.type === 'text') out.text = n.text ?? '';
  if (n.type === 'link') {
    out.url = n.url ?? (n.refId !== undefined ? REF_URL_PREFIX + n.refId : '');
    if (n.refId !== undefined) out['x-termipod'] = { ...(isObj(out['x-termipod']) ? out['x-termipod'] : {}), refId: n.refId };
  }
  if (n.type === 'group') {
    if (n.label !== undefined) out.label = n.label;
    if (n.background !== undefined) out.background = n.background;
  }
  if (n.type === 'file') {
    if (n.file !== undefined) out.file = n.file;
    if (n.subpath !== undefined) out.subpath = n.subpath;
  }
  return out;
}

function serializeEdge(e: CanvasEdge): Record<string, unknown> {
  const out: Record<string, unknown> = { ...(e.raw ?? {}) };
  out.id = e.id;
  out.fromNode = e.fromNode;
  out.toNode = e.toNode;
  if (e.fromSide !== undefined) out.fromSide = e.fromSide;
  if (e.toSide !== undefined) out.toSide = e.toSide;
  if (e.color !== undefined) out.color = e.color;
  if (e.label !== undefined) out.label = e.label;
  if (e.edgeType !== undefined) {
    out['x-termipod'] = { ...(isObj(out['x-termipod']) ? out['x-termipod'] : {}), edgeType: e.edgeType };
  }
  return out;
}

/// Serialize a board to a JSON Canvas 1.0 document string. Always writes JSON
/// Canvas, so a legacy body or an Obsidian `.canvas` upgrades on first save;
/// unknown fields/nodes carried in `raw` survive untouched.
export function serializeCanvas(b: Board): string {
  // Spreading `rawRoot` first keeps unknown top-level fields; re-assigning
  // `nodes`/`edges` after preserves their original key positions.
  return JSON.stringify({ ...(b.rawRoot ?? {}), nodes: b.nodes.map(serializeNode), edges: b.edges.map(serializeEdge) });
}

/// A bounded undo/redo snapshot stack over serialized board strings — the editor
/// pushes a snapshot before each mutation and swaps the whole board on undo/redo.
export interface History {
  push: (snapshot: string) => void;
  undo: (current: string) => string | null;
  redo: (current: string) => string | null;
  canUndo: () => boolean;
  canRedo: () => boolean;
}
export function createHistory(cap = 100): History {
  let past: string[] = [];
  let future: string[] = [];
  return {
    push: (snapshot) => {
      past.push(snapshot);
      if (past.length > cap) past = past.slice(-cap);
      future = [];
    },
    undo: (current) => {
      const prev = past.pop();
      if (prev === undefined) return null;
      future.push(current);
      return prev;
    },
    redo: (current) => {
      const next = future.pop();
      if (next === undefined) return null;
      past.push(current);
      return next;
    },
    canUndo: () => past.length > 0,
    canRedo: () => future.length > 0,
  };
}
