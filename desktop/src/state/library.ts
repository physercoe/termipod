import { create } from 'zustand';

/// The reference library — a Zotero-shaped store of research references
/// (papers, books, reports, notes) with collections and tags. Round-1 storage is
/// device-local (`localStorage`); the design doc
/// (`reference-library-and-reading.md`) specs the hub-backed target — reference
/// **metadata** in the hub (the data-ownership law: hub owns names+events) and
/// PDF **bytes** as content-addressed blobs on hosts. The model below is
/// deliberately a clean projection of that eventual hub `Reference` entity so the
/// migration is a sync, not a rewrite.

export type RefType = 'article' | 'preprint' | 'book' | 'report' | 'webpage' | 'note';

export const REF_TYPES: RefType[] = ['article', 'preprint', 'book', 'report', 'webpage', 'note'];

export interface Reference {
  id: string;
  type: RefType;
  title: string;
  authors: string[];
  year?: number;
  venue?: string; // journal / conference / publisher
  doi?: string;
  arxivId?: string;
  url?: string;
  pdfUrl?: string; // open-access PDF link (bytes not yet stored locally)
  abstract?: string;
  tldr?: string; // Semantic Scholar one-line summary
  citationCount?: number;
  source?: 'semantic-scholar' | 'manual' | 'paste';
  externalId?: string; // e.g. Semantic Scholar paperId — used to dedupe imports
  tags: string[];
  collectionIds: string[];
  notes: string; // the reader's own notes on this reference
  bodyMarkdown?: string; // pasted / captured reading content
  addedAt: number;
}

export interface Collection {
  id: string;
  name: string;
}

interface LibraryState {
  references: Reference[];
  collections: Collection[];
  addReference: (r: Omit<Reference, 'id' | 'addedAt'>) => string;
  updateReference: (id: string, patch: Partial<Reference>) => void;
  removeReference: (id: string) => void;
  addCollection: (name: string) => string;
  renameCollection: (id: string, name: string) => void;
  removeCollection: (id: string) => void;
}

const LS_KEY = 'termipod.library.v1';

interface Persisted {
  references: Reference[];
  collections: Collection[];
}

function load(): Persisted {
  try {
    const raw = localStorage.getItem(LS_KEY);
    if (raw !== null) return JSON.parse(raw) as Persisted;
  } catch {
    /* ignore */
  }
  return { references: [], collections: [] };
}

function save(p: Persisted): void {
  try {
    localStorage.setItem(LS_KEY, JSON.stringify(p));
  } catch {
    /* ignore */
  }
}

// Monotonic id — no crypto.randomUUID (not guaranteed secure-context under the
// tauri:// scheme, per the desktop id convention).
let seq = 0;
function newId(prefix: string): string {
  seq += 1;
  return `${prefix}${Date.now().toString(36)}${seq}`;
}

export const useLibrary = create<LibraryState>((set, get) => ({
  ...load(),

  addReference: (r) => {
    const id = newId('ref');
    const ref: Reference = { ...r, id, addedAt: Date.now() };
    const references = [ref, ...get().references];
    set({ references });
    save({ references, collections: get().collections });
    return id;
  },

  updateReference: (id, patch) => {
    const references = get().references.map((r) => (r.id === id ? { ...r, ...patch } : r));
    set({ references });
    save({ references, collections: get().collections });
  },

  removeReference: (id) => {
    const references = get().references.filter((r) => r.id !== id);
    set({ references });
    save({ references, collections: get().collections });
  },

  addCollection: (name) => {
    const id = newId('col');
    const collections = [...get().collections, { id, name }];
    set({ collections });
    save({ references: get().references, collections });
    return id;
  },

  renameCollection: (id, name) => {
    const collections = get().collections.map((c) => (c.id === id ? { ...c, name } : c));
    set({ collections });
    save({ references: get().references, collections });
  },

  removeCollection: (id) => {
    const collections = get().collections.filter((c) => c.id !== id);
    // Drop the collection from every reference's membership, but keep the refs.
    const references = get().references.map((r) =>
      r.collectionIds.includes(id) ? { ...r, collectionIds: r.collectionIds.filter((c) => c !== id) } : r,
    );
    set({ collections, references });
    save({ references, collections });
  },
}));
