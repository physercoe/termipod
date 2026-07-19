import { create } from 'zustand';
import { backupCorrupt } from './persist';

/// PDF annotations for the reference library — highlights, underlines, notes,
/// area boxes and freehand ink drawn on a reference's PDF. Round-1 storage is
/// device-local (`localStorage`), exactly like the reference library
/// (`state/library.ts`); the hub-backed target is the `reference_annotations`
/// child entity (ADR-053 amendment, migration 0064). The model below is a clean
/// projection of that hub row so the migration is a sync, not a rewrite —
/// crucially the geometry follows Zotero's convention: **unscaled PDF points,
/// origin bottom-left**, so an overlay just multiplies by the current zoom and
/// lands at any scale, and a director's existing Zotero highlights import 1:1.

export type AnnotationType = 'highlight' | 'underline' | 'note' | 'text' | 'image' | 'ink';

/// The geometry, kept in PDF user space (points, origin bottom-left, unscaled).
/// Rect kinds carry `rects` ([xMin, yMin, xMax, yMax] each, possibly several for a
/// multi-line highlight); ink carries `paths` (flattened [x1,y1,x2,y2,…] point
/// runs) plus a stroke `width` in PDF points.
export interface AnnotationPosition {
  pageIndex: number; // 0-based
  rects?: number[][];
  paths?: number[][];
  width?: number;
}

export interface Annotation {
  id: string;
  referenceId: string; // the desktop Reference id this annotation belongs to
  type: AnnotationType;
  color?: string; // hex, e.g. #ffd400
  pageIndex: number; // 0-based; mirrors position.pageIndex (for filtering)
  sortIndex?: string; // Zotero-style reading-order key (page+y+x); optional locally
  comment?: string;
  text?: string; // the selected text (highlight/underline)
  author?: string;
  position: AnnotationPosition;
  tags: string[];
  createdAt: number;
  updatedAt: number;
  // --- Hub sync linkage (future state/librarySync.ts) ----------------------
  hubId?: string; // id of the linked hub reference_annotations row, once synced
  syncedAt?: number;
}

/// The default highlight palette — Zotero's six annotation colors, so imports and
/// exports line up. The first is the default (yellow).
export const ANNOTATION_COLORS: string[] = [
  '#ffd400', // yellow
  '#ff6666', // red
  '#5fb236', // green
  '#2ea8e5', // blue
  '#a28ae5', // purple
  '#e56eee', // magenta
];

interface AnnotationState {
  items: Annotation[];
  // Undo history — a bounded stack of prior `items` snapshots (#322). A mis-drawn
  // highlight, a deleted comment-bearing annotation, etc. is otherwise permanent.
  past: Annotation[][];
  add: (a: Omit<Annotation, 'id' | 'createdAt' | 'updatedAt'>) => string;
  update: (id: string, patch: Partial<Omit<Annotation, 'id'>>) => void;
  remove: (id: string) => void;
  undo: () => void;
}

const UNDO_CAP = 50;
function pushPast(past: Annotation[][], snapshot: Annotation[]): Annotation[][] {
  const next = [...past, snapshot];
  return next.length > UNDO_CAP ? next.slice(next.length - UNDO_CAP) : next;
}

const LS_KEY = 'termipod.annotations.v1';

function load(): Annotation[] {
  let raw: string | null = null;
  try {
    raw = localStorage.getItem(LS_KEY);
    if (raw !== null) return JSON.parse(raw) as Annotation[];
  } catch (e) {
    // Back up the corrupt blob so the next save doesn't overwrite the only copy
    // of the user's highlights/notes.
    if (raw !== null) backupCorrupt(LS_KEY, raw, e);
  }
  return [];
}

function save(items: Annotation[]): void {
  try {
    localStorage.setItem(LS_KEY, JSON.stringify(items));
  } catch (e) {
    console.error(`[annotations] failed to persist "${LS_KEY}" (quota exceeded?)`, e);
  }
}

// Monotonic id — no crypto.randomUUID (not guaranteed secure-context under the
// tauri:// scheme, per the desktop id convention; matches state/library.ts).
let seq = 0;
function newId(): string {
  seq += 1;
  return `ann${Date.now().toString(36)}${seq}`;
}

// A coarse reading-order key: page, then vertical (higher-on-page first), then
// horizontal. PDF y grows upward, so we sort on (maxPageY - yTop) to get top-down.
// Good enough to order the annotation list without Zotero's exact string scheme.
function computeSortIndex(pos: AnnotationPosition): string {
  const rect = pos.rects?.[0] ?? (pos.paths?.[0] !== undefined ? bboxOfPath(pos.paths[0]) : undefined);
  const yTop = rect !== undefined ? rect[3] : 0;
  const xLeft = rect !== undefined ? rect[0] : 0;
  const page = String(pos.pageIndex).padStart(5, '0');
  // Invert y so larger y (top of page) sorts first; clamp to a fixed width.
  const y = String(Math.max(0, Math.round(100000 - yTop))).padStart(7, '0');
  const x = String(Math.max(0, Math.round(xLeft))).padStart(5, '0');
  return `${page}|${y}|${x}`;
}

function bboxOfPath(flat: number[]): number[] {
  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  let maxY = -Infinity;
  for (let i = 0; i + 1 < flat.length; i += 2) {
    minX = Math.min(minX, flat[i]);
    maxX = Math.max(maxX, flat[i]);
    minY = Math.min(minY, flat[i + 1]);
    maxY = Math.max(maxY, flat[i + 1]);
  }
  return [minX, minY, maxX, maxY];
}

export const useAnnotations = create<AnnotationState>((set, get) => ({
  items: load(),
  past: [],

  add: (a) => {
    const id = newId();
    const now = Date.now();
    const anno: Annotation = {
      ...a,
      id,
      createdAt: now,
      updatedAt: now,
      sortIndex: a.sortIndex ?? computeSortIndex(a.position),
    };
    const prev = get().items;
    const items = [...prev, anno];
    set({ items, past: pushPast(get().past, prev) });
    save(items);
    return id;
  },

  update: (id, patch) => {
    const prev = get().items;
    const items = prev.map((a) => (a.id === id ? { ...a, ...patch, id, updatedAt: Date.now() } : a));
    set({ items, past: pushPast(get().past, prev) });
    save(items);
  },

  remove: (id) => {
    const prev = get().items;
    const items = prev.filter((a) => a.id !== id);
    set({ items, past: pushPast(get().past, prev) });
    save(items);
  },

  undo: () => {
    const past = get().past;
    if (past.length === 0) return;
    const restored = past[past.length - 1];
    set({ items: restored, past: past.slice(0, -1) });
    save(restored);
  },
}));
