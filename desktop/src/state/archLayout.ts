/// Pure geometry for the architecture schematic (archgraph plan W3). The renderer
/// (`ui/ArchSchematicView.tsx`) is a thin mapping from this result onto React
/// Flow nodes/edges — the same spec/renderer split (D-1) that keeps the schematic
/// spec testable, applied to the LAYOUT so the box/card arithmetic is verifiable
/// without a browser: no overlapping rows, every container actually wrapping its
/// own cards, every edge endpoint real.
///
/// Two layouts, chosen by the spec: **uniform** (one ×N container — the classic
/// figure, geometry unchanged from before W3) when `schematic.layout` is absent,
/// and **grouped** when it is present — nested repeat groups expressing the 3×/1×
/// interleave idiom, plus a per-layer pattern strip beside the stack.
import type { ArchNode, ArchSchematic, AttnKind, FfnKind, LayerCell } from './archSchematic.ts';
import type { ArchPanel } from './archPanels.ts';

// Card geometry (kept in sync with the CSS so the container boxes wrap correctly).
export const GEO = {
  W: 260, // card width
  H: 56, // component card (attention / FFN / embed / head)
  HN: 34, // norm card — shorter, so a 4-row block stays compact
  GAP: 26,
  X: 96, // stack x; the pattern strip lives to its left
  PAD: 16, // container inset per nesting level
  STRIP_W: 26,
  STRIP_GAP: 30,
  /// Zoom-in panel geometry (W4): the dotted expansion boxes to the right.
  PANEL_W: 250,
  PANEL_GAP: 90, // horizontal clearance from the stack, room for the leader line
  PANEL_HEAD: 30,
  PANEL_ROW: 32,
  PANEL_PAD: 12,
} as const;

const { W, H, HN, GAP, X, PAD, STRIP_W, STRIP_GAP, PANEL_W, PANEL_GAP, PANEL_HEAD, PANEL_ROW, PANEL_PAD } = GEO;
const STRIP_X = X - PAD * 2 - STRIP_GAP - STRIP_W;
const PANEL_X = X + W + PANEL_GAP;

/// Labels the layout needs but must not invent (they are i18n strings owned by
/// the renderer) — injected so this module stays pure and unit-testable.
export interface ArchLabels {
  attn: (kind: AttnKind) => string;
  norm: string;
  ffnDense: string;
  ffnMoe: string;
  /// Sub-line for a linear/recurrent operator (no KV cache).
  linearSub: string;
  /// The uniform layout's ×N container tag.
  container: string;
}

export interface LaidCard {
  /// Unique rendered id (also the selection key).
  id: string;
  node: ArchNode;
  x: number;
  y: number;
  w: number;
  h: number;
  z: number;
  /// Set on attention cards of a heterogeneous stack — drives the per-kind band.
  attn?: AttnKind;
}

export interface LaidBox {
  id: string;
  x: number;
  y: number;
  w: number;
  h: number;
  z: number;
  label: string;
  variant: 'cycle' | 'run';
}

export interface LaidStrip {
  x: number;
  y: number;
  w: number;
  h: number;
  cells: LayerCell[];
}

export interface LaidEdge {
  id: string;
  source: string;
  target: string;
  /// `leader` is the dotted line from a block to its zoom-in panel (W4).
  kind: 'main' | 'residual' | 'leader';
}

/// A zoom-in panel placed beside the stack, tied to the block it expands.
export interface LaidPanel {
  id: string;
  panel: ArchPanel;
  /// The card whose internals this panel expands (the leader line's source).
  anchorCardId: string;
  x: number;
  y: number;
  w: number;
  h: number;
  z: number;
}

export interface ArchLayoutResult {
  cards: LaidCard[];
  boxes: LaidBox[];
  strip: LaidStrip | null;
  edges: LaidEdge[];
  panels: LaidPanel[];
}

/// Rows a panel occupies: every non-chip item is its own row, and the expert
/// chips (`expert`/`more`) share ONE row (the figure idiom is a chip row).
export function panelRows(panel: ArchPanel): number {
  const chips = panel.items.filter((i) => i.shape === 'expert' || i.shape === 'more').length;
  return panel.items.length - chips + (chips > 0 ? 1 : 0);
}

// A narrative note is a wrapped paragraph, not a row — reserve ~3 lines at the
// panel width so it isn't clipped (the panel hides overflow).
const NOTE_H = 54;

function panelHeight(panel: ArchPanel): number {
  const noteRow = panel.noteKey !== undefined ? NOTE_H : 0;
  return PANEL_HEAD + panelRows(panel) * PANEL_ROW + noteRow + PANEL_PAD * 2;
}

/// Place the zoom-in panels to the right of the stack: each is anchored to the
/// first card of the block it expands, and panels never overlap (a later panel
/// slides down past the previous one).
function placePanels(panels: ArchPanel[], cards: LaidCard[]): { panels: LaidPanel[]; edges: LaidEdge[] } {
  const out: LaidPanel[] = [];
  const edges: LaidEdge[] = [];
  let floor = -Infinity;
  for (const p of panels) {
    const anchor =
      p.kind === 'attention'
        ? cards.find((c) => c.node.kind === 'attention')
        : cards.find((c) => c.node.kind === 'moe');
    if (anchor === undefined) continue;
    const h = panelHeight(p);
    const y = Math.max(anchor.y, floor);
    out.push({ id: p.id, panel: p, anchorCardId: anchor.id, x: PANEL_X, y, w: PANEL_W, h, z: 2 });
    edges.push({ id: `ld${p.id}`, source: anchor.id, target: p.id, kind: 'leader' });
    floor = y + h + GAP;
  }
  return { panels: out, edges };
}

/// True for the linear/recurrent operators — novelty accent, O(1) state.
export function isLinearAttn(kind: AttnKind): boolean {
  return kind === 'KDA' || kind === 'GatedDeltaNet';
}

function attnNodeFor(kind: AttnKind, base: ArchNode | undefined, id: string, labels: ArchLabels): ArchNode {
  return { id, kind: 'attention', label: labels.attn(kind), sub: isLinearAttn(kind) ? labels.linearSub : base?.sub, inBlock: true };
}

function ffnNodeFor(ffn: FfnKind, base: ArchNode | undefined, id: string, labels: ArchLabels): ArchNode {
  if (ffn === 'moe' && base?.kind === 'moe') return { ...base, id };
  if (ffn === 'dense' && base?.kind === 'ffn') return { ...base, id };
  return ffn === 'moe'
    ? { id, kind: 'moe', label: labels.ffnMoe, sub: base?.sub, inBlock: true }
    : { id, kind: 'ffn', label: labels.ffnDense, inBlock: true };
}

/// The dominant FFN kind across a layer range (a group is usually homogeneous; a
/// first-K-dense boundary inside one is reported as MoE and explained by the
/// spec's `first-k-dense` annotation).
function ffnForRange(cells: LayerCell[], from: number, to: number): FfnKind {
  let moe = 0;
  let dense = 0;
  for (const c of cells) {
    if (c.index < from || c.index > to) continue;
    if (c.ffn === 'moe') moe += 1;
    else dense += 1;
  }
  return moe >= dense ? 'moe' : 'dense';
}

function mainEdge(id: string, source: string, target: string): LaidEdge {
  return { id, source, target, kind: 'main' };
}
function residualEdge(id: string, source: string, target: string): LaidEdge {
  return { id, source, target, kind: 'residual' };
}

/// The classic uniform layout — one dashed ×N container around the in-block rows,
/// every card `H` tall at a fixed pitch. Geometry identical to pre-W3.
function layoutUniform(s: ArchSchematic, labels: ArchLabels, panels: ArchPanel[]): ArchLayoutResult {
  const yOf = (i: number): number => i * (H + GAP);
  const cards: LaidCard[] = s.nodes.map((n, i) => ({ id: n.id, node: n, x: X, y: yOf(i), w: W, h: H, z: 1 }));

  const boxes: LaidBox[] = [];
  const blockIdx = s.nodes.map((n, i) => (n.inBlock ? i : -1)).filter((i) => i >= 0);
  if (blockIdx.length > 0 && s.layers > 0) {
    const top = yOf(Math.min(...blockIdx)) - PAD;
    const bottom = yOf(Math.max(...blockIdx)) + H + PAD;
    boxes.push({ id: '__container', x: X - PAD, y: top, w: W + PAD * 2, h: bottom - top, z: 0, label: labels.container, variant: 'run' });
  }

  const edges: LaidEdge[] = [];
  for (let i = 0; i < s.nodes.length - 1; i += 1) edges.push(mainEdge(`m${i}`, s.nodes[i].id, s.nodes[i + 1].id));
  s.residuals.forEach((r, i) => edges.push(residualEdge(`r${i}`, r.from, r.to)));

  const placed = placePanels(panels, cards);
  return { cards, boxes, strip: null, edges: [...edges, ...placed.edges], panels: placed.panels };
}

/// The heterogeneous layout: embed → [nested repeat groups] → final norm → head,
/// with the per-layer pattern strip on the left. Each group renders one decoder
/// block (norm → attention → norm → FFN) inside a dashed ×N container; an
/// interleave cycle nests its runs inside an outer ×repeat container.
function layoutGrouped(s: ArchSchematic, labels: ArchLabels, panels: ArchPanel[]): ArchLayoutResult {
  const layout = s.layout;
  if (layout === undefined) return layoutUniform(s, labels, panels);

  const cards: LaidCard[] = [];
  const boxes: LaidBox[] = [];
  const edges: LaidEdge[] = [];
  const flow: string[] = []; // emission order of the main stack
  const residualPairs: Array<{ norm1: string; norm2: string }> = [];

  const base = {
    embed: s.nodes.find((n) => n.id === 'embed'),
    norm: s.nodes.find((n) => n.id === 'norm1'),
    attn: s.nodes.find((n) => n.id === 'attn'),
    ffn: s.nodes.find((n) => n.id === 'ffn'),
    finalnorm: s.nodes.find((n) => n.id === 'finalnorm'),
    head: s.nodes.find((n) => n.id === 'head'),
  };
  const normLabel = base.norm?.label ?? 'Norm';

  let y = 0;
  const push = (n: ArchNode, attn?: AttnKind): void => {
    const h = n.kind === 'norm' ? HN : H;
    cards.push({ id: n.id, node: n, x: X, y, w: W, h, z: 2, attn });
    flow.push(n.id);
    y += h + GAP;
  };

  if (base.embed) push(base.embed);

  const blockTop = y;
  let blockIdx = 0;
  // One decoder block (4 rows) inside its own dashed container — always the
  // innermost box (level 0), whether standalone or nested inside a cycle.
  const emitBlock = (attn: AttnKind, ffn: FfnKind, count: number): void => {
    const gid = `blk${blockIdx}`;
    const top = y;
    y += PAD;
    const n1 = `${gid}norm1`;
    const n2 = `${gid}norm2`;
    push({ id: n1, kind: 'norm', label: normLabel, inBlock: true });
    push(attnNodeFor(attn, base.attn, `${gid}attn`, labels), attn);
    push({ id: n2, kind: 'norm', label: normLabel, inBlock: true });
    push(ffnNodeFor(ffn, base.ffn, `${gid}ffn`, labels));
    y -= GAP; // the last row's trailing gap sits outside the container
    y += PAD;
    boxes.push({ id: `${gid}box`, x: X - PAD, y: top, w: W + PAD * 2, h: y - top, z: 1, label: `×${count} · ${labels.attn(attn)}`, variant: 'run' });
    residualPairs.push({ norm1: n1, norm2: n2 });
    blockIdx += 1;
    y += GAP;
  };

  for (const g of layout.groups) {
    if (g.kind === 'run') {
      emitBlock(g.run.attn, ffnForRange(layout.strip, g.run.from, g.run.to), g.run.count);
    } else {
      const cyc = g.cycle;
      const outerTop = y;
      y += PAD;
      for (const r of cyc.unit) emitBlock(r.attn, ffnForRange(layout.strip, r.from, r.to), r.count);
      y -= GAP; // the last inner block's trailing gap sits inside the outer box
      y += PAD;
      // The outer cycle wrapper: wider inset than its inner blocks, and BEHIND
      // them (cards sit above both).
      boxes.push({ id: `cyc${outerTop}`, x: X - PAD * 2, y: outerTop, w: W + PAD * 4, h: y - outerTop, z: 0, label: `×${cyc.repeat}`, variant: 'cycle' });
      y += GAP;
    }
  }
  const blockBottom = y - GAP;

  if (base.finalnorm) push(base.finalnorm);
  if (base.head) push(base.head);

  for (let i = 0; i < flow.length - 1; i += 1) edges.push(mainEdge(`m${i}`, flow[i], flow[i + 1]));
  // Per-block residual skips: around attention, and around the FFN into whatever
  // the block exits into (the next block's first row, or the final norm).
  for (const { norm1, norm2 } of residualPairs) {
    edges.push(residualEdge(`ra${norm1}`, norm1, norm2));
    const exit = flow[flow.indexOf(norm2) + 2]; // norm2 → ffn → exit
    if (exit !== undefined) edges.push(residualEdge(`rf${norm2}`, norm2, exit));
  }

  const strip: LaidStrip = { x: STRIP_X, y: blockTop, w: STRIP_W, h: Math.max(blockBottom - blockTop, H), cells: layout.strip };
  const placed = placePanels(panels, cards);
  return { cards, boxes, strip, edges: [...edges, ...placed.edges], panels: placed.panels };
}

/// Lay out a schematic: grouped when the spec carries a heterogeneous `layout`,
/// otherwise the classic uniform stack. `panels` (W4) are placed to the right and
/// tied to the block each expands. Pure.
export function layoutArch(s: ArchSchematic, labels: ArchLabels, panels: ArchPanel[] = []): ArchLayoutResult {
  return s.layout !== undefined ? layoutGrouped(s, labels, panels) : layoutUniform(s, labels, panels);
}
