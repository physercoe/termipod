/// J2 Author — the **canvas** document model (spatial thinking board,
/// Zettelkasten-shaped): **cards** placed on an infinite surface, joined by
/// **typed edges**. A card is either a free note or a handle onto a library
/// `Reference` (so the canvas is wired to J1's reference library, not a
/// disconnected whiteboard).
///
/// A canvas is no longer a standalone surface with a single global board — it is
/// **one kind of Author document** (like markdown or diagram), so a workspace can
/// hold many boards as tabs/files. The board is serialized as JSON into the
/// document's `body` (see `state/documents.ts`); this module is the pure model +
/// (de)serialization, and `ui/CanvasEditor.tsx` is the interactive editor.

export type CardKind = 'note' | 'ref';

export type EdgeType = 'relates' | 'supports' | 'refutes' | 'cites' | 'leads';
export const EDGE_TYPES: EdgeType[] = ['relates', 'supports', 'refutes', 'cites', 'leads'];

export interface CanvasCard {
  id: string;
  kind: CardKind;
  x: number;
  y: number;
  text: string; // note body (kind 'note'); title override otherwise unused
  refId?: string; // library Reference id (kind 'ref')
}

export interface CanvasEdge {
  id: string;
  from: string; // card id
  to: string; // card id
  type: EdgeType;
}

export interface Board {
  cards: CanvasCard[];
  edges: CanvasEdge[];
}

export const emptyBoard = (): Board => ({ cards: [], edges: [] });

/// Parse a document body into a board, tolerating anything malformed (a brand-new
/// or corrupt doc reads as an empty board rather than throwing).
export function parseBoard(body: string): Board {
  try {
    const b = JSON.parse(body) as Partial<Board>;
    if (b !== null && Array.isArray(b.cards) && Array.isArray(b.edges)) {
      return { cards: b.cards, edges: b.edges };
    }
  } catch {
    /* fall through to empty */
  }
  return emptyBoard();
}

export const serializeBoard = (b: Board): string => JSON.stringify(b);

// Monotonic id — no crypto.randomUUID under tauri:// (see library.ts note).
let seq = 0;
export function newId(prefix: string): string {
  seq += 1;
  return `${prefix}${Date.now().toString(36)}${seq}`;
}
