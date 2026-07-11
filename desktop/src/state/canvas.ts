import { create } from 'zustand';

/// J4 Canvas — a spatial thinking board (Zettelkasten-shaped): **cards** placed
/// on an infinite surface, joined by **typed edges**. A card is either a free
/// note or a handle onto a library `Reference` (so the canvas is wired to J1's
/// reference library, not a disconnected whiteboard). Round-1 storage is
/// device-local (`localStorage`), the same posture as the reference library; a
/// later round promotes boards to hub-backed incubation notes.

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

interface CanvasState {
  cards: CanvasCard[];
  edges: CanvasEdge[];
  addCard: (c: Omit<CanvasCard, 'id'>) => string;
  updateCard: (id: string, patch: Partial<Omit<CanvasCard, 'id'>>) => void;
  removeCard: (id: string) => void;
  addEdge: (from: string, to: string, type: EdgeType) => void;
  setEdgeType: (id: string, type: EdgeType) => void;
  removeEdge: (id: string) => void;
  clear: () => void;
}

const LS_KEY = 'termipod.canvas.v1';

interface Persisted {
  cards: CanvasCard[];
  edges: CanvasEdge[];
}

function load(): Persisted {
  try {
    const raw = localStorage.getItem(LS_KEY);
    if (raw !== null) return JSON.parse(raw) as Persisted;
  } catch {
    /* ignore */
  }
  return { cards: [], edges: [] };
}

function save(p: Persisted): void {
  try {
    localStorage.setItem(LS_KEY, JSON.stringify(p));
  } catch {
    /* ignore */
  }
}

// Monotonic id — no crypto.randomUUID under tauri:// (see library.ts note).
let seq = 0;
function newId(prefix: string): string {
  seq += 1;
  return `${prefix}${Date.now().toString(36)}${seq}`;
}

export const useCanvas = create<CanvasState>((set, get) => ({
  ...load(),

  addCard: (c) => {
    const id = newId('card');
    const cards = [...get().cards, { ...c, id }];
    set({ cards });
    save({ cards, edges: get().edges });
    return id;
  },

  updateCard: (id, patch) => {
    const cards = get().cards.map((c) => (c.id === id ? { ...c, ...patch } : c));
    set({ cards });
    save({ cards, edges: get().edges });
  },

  removeCard: (id) => {
    const cards = get().cards.filter((c) => c.id !== id);
    const edges = get().edges.filter((e) => e.from !== id && e.to !== id);
    set({ cards, edges });
    save({ cards, edges });
  },

  addEdge: (from, to, type) => {
    if (from === to) return;
    const exists = get().edges.some((e) => e.from === from && e.to === to);
    if (exists) return;
    const edges = [...get().edges, { id: newId('edge'), from, to, type }];
    set({ edges });
    save({ cards: get().cards, edges });
  },

  setEdgeType: (id, type) => {
    const edges = get().edges.map((e) => (e.id === id ? { ...e, type } : e));
    set({ edges });
    save({ cards: get().cards, edges });
  },

  removeEdge: (id) => {
    const edges = get().edges.filter((e) => e.id !== id);
    set({ edges });
    save({ cards: get().cards, edges });
  },

  clear: () => {
    set({ cards: [], edges: [] });
    save({ cards: [], edges: [] });
  },
}));
