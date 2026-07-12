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
  source?: 'semantic-scholar' | 'manual' | 'paste' | 'zotero';
  externalId?: string; // e.g. Semantic Scholar paperId / Zotero item key — dedupes imports
  tags: string[];
  collectionIds: string[];
  notes: string; // the reader's own notes on this reference
  bodyMarkdown?: string; // pasted / captured reading content
  addedAt: number;
  // The long tail of source metadata not promoted to a first-class field above
  // (publisher, pages, volume, issue, ISBN/ISSN, place, language, series,
  // edition, extra, libraryCatalog, accessDate, …). Shown read-only in the Read
  // surface's Details section; keyed by the source's own field name.
  details?: Record<string, string>;
  // Zotero attachment coordinates — the attachment item's key is its subdirectory
  // under the Zotero `storage/` folder, `file` the filename within it. Bytes are
  // NOT stored here; the Read surface resolves them from a user-linked storage
  // folder (in-memory, see state/zoteroStorage.ts) so nothing leaves the device.
  zoteroStorage?: { key: string; file: string; contentType?: string };
}

export interface Collection {
  id: string;
  name: string;
}

/// One parsed row from an external importer (e.g. Zotero). Carries collection
/// **names** rather than ids; the store finds-or-creates a `Collection` per name
/// so re-importing merges into the same collections instead of duplicating them.
export interface ImportItem {
  ref: Omit<Reference, 'id' | 'addedAt' | 'collectionIds'>;
  collectionNames: string[];
}

export interface ImportResult {
  added: number;
  updated: number; // already-present references refreshed from the source
  collectionsCreated: number;
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
  importReferences: (items: ImportItem[]) => ImportResult;
}

// Dedupe keys for a reference — strong keys (externalId / DOI / arXiv) win; a
// title+year fallback catches manually-added items that have no identifier.
function dedupeKeys(r: {
  externalId?: string;
  doi?: string;
  arxivId?: string;
  title: string;
  year?: number;
}): string[] {
  const k: string[] = [];
  if (r.externalId !== undefined && r.externalId !== '') k.push(`x:${r.externalId}`);
  if (r.doi !== undefined && r.doi !== '') k.push(`doi:${r.doi.toLowerCase()}`);
  if (r.arxivId !== undefined && r.arxivId !== '') k.push(`ax:${r.arxivId.toLowerCase()}`);
  if (k.length === 0 && r.title.trim() !== '') {
    k.push(`t:${r.title.trim().toLowerCase().slice(0, 80)}|${r.year ?? ''}`);
  }
  return k;
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

  importReferences: (items) => {
    const existing = get().references;
    // Map every dedupe key to the existing reference id, so a re-import of the
    // same library UPDATES rows (backfilling e.g. PDF coordinates / details a
    // newer importer captures) rather than skipping them.
    const keyToId = new Map<string, string>();
    for (const r of existing) {
      dedupeKeys(r).forEach((k) => {
        if (!keyToId.has(k)) keyToId.set(k, r.id);
      });
    }

    // Find-or-create collections by name so re-import merges, not duplicates.
    const collections = [...get().collections];
    const byName = new Map<string, string>();
    collections.forEach((c) => byName.set(c.name, c.id));
    let collectionsCreated = 0;
    const ensureCollection = (name: string): string => {
      const found = byName.get(name);
      if (found !== undefined) return found;
      const id = newId('col');
      collections.push({ id, name });
      byName.set(name, id);
      collectionsCreated += 1;
      return id;
    };

    const byId = new Map(existing.map((r) => [r.id, r] as const));
    const added: Reference[] = [];
    const updatedIds = new Set<string>();
    for (const it of items) {
      const collectionIds = [...new Set(it.collectionNames.map(ensureCollection))];
      const keys = dedupeKeys(it.ref);
      const matchId = keys.map((k) => keyToId.get(k)).find((x) => x !== undefined);
      if (matchId !== undefined) {
        const cur = byId.get(matchId);
        if (cur === undefined) continue;
        // Overwrite bibliographic fields from the source; preserve the reader's
        // own curation (notes, body, and the union of tags/collections).
        byId.set(matchId, {
          ...cur,
          ...it.ref,
          id: cur.id,
          addedAt: cur.addedAt,
          notes: cur.notes,
          bodyMarkdown: cur.bodyMarkdown ?? it.ref.bodyMarkdown,
          tags: [...new Set([...cur.tags, ...it.ref.tags])],
          collectionIds: [...new Set([...cur.collectionIds, ...collectionIds])],
          zoteroStorage: it.ref.zoteroStorage ?? cur.zoteroStorage,
          details: it.ref.details ?? cur.details,
        });
        updatedIds.add(matchId);
      } else {
        const id = newId('ref');
        added.push({ ...it.ref, collectionIds, id, addedAt: Date.now() });
        keys.forEach((k) => {
          if (!keyToId.has(k)) keyToId.set(k, id);
        });
      }
    }

    const references = [...added, ...existing.map((r) => byId.get(r.id) ?? r)];
    set({ references, collections });
    save({ references, collections });
    return { added: added.length, updated: updatedIds.size, collectionsCreated };
  },
}));
