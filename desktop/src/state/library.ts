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

/// A link to another work in the citation graph (a reference this item cites, or
/// a work that cites it). Carries just enough to display + open it; the full
/// record is fetched on demand, not stored.
export interface WorkLink {
  id?: string; // OpenAlex work id (URL form)
  title: string;
  year?: number;
  doi?: string;
}

/// A code / data / model resource attached to a paper, detected by the scraper
/// (GitHub, GitLab, Hugging Face, Zenodo, OSF, Figshare, Kaggle, …). `kind`
/// classifies it; `host` is the bare hostname for the badge.
export interface ResourceLink {
  url: string;
  kind: 'code' | 'data' | 'model';
  host: string;
}

/// Journal-level metrics for the venue. `twoYearMeanCitedness` is OpenAlex's open
/// analog of the (paywalled, Clarivate-owned) Journal Impact Factor — labelled
/// honestly in the UI, never as "JCR IF".
export interface JournalMetrics {
  name?: string;
  issn?: string[];
  twoYearMeanCitedness?: number; // OpenAlex summary_stats — IF-like, not JCR
  hIndex?: number;
  i10Index?: number;
  worksCount?: number;
  isOa?: boolean;
}

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
  source?: 'semantic-scholar' | 'manual' | 'paste' | 'zotero' | 'scrape';
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
  // --- Scraper enrichment (state/../discovery/scrape.ts) --------------------
  // Rich metadata beyond the core bibliographic fields, populated on demand by
  // the scraper ("Enrich" in the inspector). All optional; absence means the
  // item was never scraped. These always overwrite on re-scrape (they're derived,
  // not user-curated), whereas core fields are only backfilled when empty.
  referenceCount?: number; // works this item cites
  citedByCount?: number; // works citing this item (fresher than citationCount)
  influentialCitationCount?: number;
  references?: WorkLink[]; // a capped sample of cited works
  citations?: WorkLink[]; // a capped sample of citing works (most-cited first)
  journal?: JournalMetrics;
  openAccess?: { status?: string; oaUrl?: string; isOa?: boolean };
  topics?: string[];
  resourceLinks?: ResourceLink[]; // code / data / model links found in metadata
  enrichedAt?: number; // when the scraper last ran
  enrichSource?: string; // provenance, e.g. "OpenAlex"
  // --- Hub sync linkage (state/librarySync.ts) -----------------------------
  hubId?: string; // the id of the linked hub reference_items row, once synced
  syncedAt?: number; // when this row last reconciled with the hub
  // Attachments (0..N). Bytes are NOT stored here — only coordinates. Two
  // provenances resolve differently (see state/zoteroStorage.ts):
  //   - 'zotero'  : imported; keyed `<key>/<file>` under the linked Zotero
  //                 `storage/` folder, resolved through that folder's index.
  //   - 'managed' : added in-app; copied into the active storage root as
  //                 `<key>/<file>` and resolved by its absolute `path`.
  // Legacy single-attachment rows (`zoteroStorage`) are migrated into this array
  // on load (see migrateReference).
  attachments?: Attachment[];
  // Deprecated — read only for migration into `attachments`. Do not write.
  zoteroStorage?: { key: string; file: string; contentType?: string };
}

export interface Attachment {
  id: string;
  file: string; // filename
  contentType?: string;
  source: 'zotero' | 'managed';
  key?: string; // storage subfolder key (Zotero layout: `<key>/<file>`)
  path?: string; // absolute path — set for 'managed' files, read directly
  addedAt: number;
}

export interface Collection {
  id: string;
  name: string;
}

/// A tag TermiPod treats as "internal" and hides from the Read surface: a
/// reading-list plugin marker like "/unread", "/read", "/reading" — a leading
/// slash by convention, a functional flag rather than content the director
/// curated. Used both at Zotero import (alongside the automatic-tag `type` filter,
/// which display can't see once tags are flattened to strings) AND at display, so
/// markers already sitting in an imported or hub-synced library are hidden too.
/// Leading slash only — a tag like "AI/ML" is real content and kept.
export function isInternalTag(name: string): boolean {
  return name.startsWith('/');
}

/// Drop internal tags from a list, for display.
export function visibleTags(tags: string[]): string[] {
  return tags.filter((t) => !isInternalTag(t));
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
  addAttachment: (refId: string, att: Omit<Attachment, 'id' | 'addedAt'>) => void;
  removeAttachment: (refId: string, attId: string) => void;
  addCollection: (name: string) => string;
  renameCollection: (id: string, name: string) => void;
  removeCollection: (id: string) => void;
  renameTag: (from: string, to: string) => void;
  removeTag: (name: string) => void;
  importReferences: (items: ImportItem[]) => ImportResult;
}

// Dedupe keys for a reference — strong keys (externalId / DOI / arXiv) win; a
// title+year fallback catches manually-added items that have no identifier.
function dedupeKeys(r: {
  hubId?: string;
  externalId?: string;
  doi?: string;
  arxivId?: string;
  title: string;
  year?: number;
}): string[] {
  const k: string[] = [];
  if (r.hubId !== undefined && r.hubId !== '') k.push(`h:${r.hubId}`);
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

// Fold a legacy single `zoteroStorage` field into the `attachments` array so
// every consumer reads one shape. Idempotent: a row already carrying
// `attachments` is left as-is.
function migrateReference(r: Reference): Reference {
  if (r.attachments !== undefined) return r;
  if (r.zoteroStorage === undefined) return { ...r, attachments: [] };
  const z = r.zoteroStorage;
  const att: Attachment = {
    id: `att${r.id}0`,
    file: z.file,
    contentType: z.contentType,
    source: 'zotero',
    key: z.key,
    addedAt: r.addedAt,
  };
  return { ...r, attachments: [att] };
}

function load(): Persisted {
  try {
    const raw = localStorage.getItem(LS_KEY);
    if (raw !== null) {
      const p = JSON.parse(raw) as Persisted;
      return { collections: p.collections ?? [], references: (p.references ?? []).map(migrateReference) };
    }
  } catch {
    /* ignore */
  }
  return { references: [], collections: [] };
}

/// The attachment to open by default for a reference (the first), or undefined.
export function primaryAttachment(r: Pick<Reference, 'attachments'>): Attachment | undefined {
  return r.attachments?.[0];
}

/// Whether a reference has at least one attachment.
export function hasAnyAttachment(r: Pick<Reference, 'attachments'>): boolean {
  return (r.attachments?.length ?? 0) > 0;
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
    const ref: Reference = migrateReference({ ...r, id, addedAt: Date.now() });
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

  addAttachment: (refId, att) => {
    const full: Attachment = { ...att, id: newId('att'), addedAt: Date.now() };
    const references = get().references.map((r) =>
      r.id === refId ? { ...r, attachments: [...(r.attachments ?? []), full] } : r,
    );
    set({ references });
    save({ references, collections: get().collections });
  },

  removeAttachment: (refId, attId) => {
    const references = get().references.map((r) =>
      r.id === refId ? { ...r, attachments: (r.attachments ?? []).filter((a) => a.id !== attId) } : r,
    );
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

  // Tags are plain strings on each reference (no tag entity), so a library-wide
  // rename/delete is a sweep over every reference's `tags`. Rename de-dupes when
  // the target already exists on an item and drops empties; an empty target is
  // treated as a delete.
  renameTag: (from, to) => {
    const to2 = to.trim();
    if (to2 === '' ) {
      get().removeTag(from);
      return;
    }
    if (to2 === from) return;
    const references = get().references.map((r) =>
      r.tags.includes(from)
        ? { ...r, tags: [...new Set(r.tags.map((t) => (t === from ? to2 : t)))] }
        : r,
    );
    set({ references });
    save({ references, collections: get().collections });
  },

  removeTag: (name) => {
    const references = get().references.map((r) =>
      r.tags.includes(name) ? { ...r, tags: r.tags.filter((t) => t !== name) } : r,
    );
    set({ references });
    save({ references, collections: get().collections });
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
        // Preserve user-added (managed) attachments; refresh the Zotero one from
        // the (single-attachment) importer.
        const curAtts = cur.attachments ?? [];
        const managed = curAtts.filter((a) => a.source === 'managed');
        const zot: Attachment[] =
          it.ref.zoteroStorage !== undefined
            ? [
                {
                  id: `att${cur.id}z`,
                  file: it.ref.zoteroStorage.file,
                  contentType: it.ref.zoteroStorage.contentType,
                  source: 'zotero',
                  key: it.ref.zoteroStorage.key,
                  addedAt: cur.addedAt,
                },
              ]
            : curAtts.filter((a) => a.source === 'zotero');
        byId.set(matchId, {
          ...cur,
          ...it.ref,
          id: cur.id,
          addedAt: cur.addedAt,
          notes: cur.notes,
          bodyMarkdown: cur.bodyMarkdown ?? it.ref.bodyMarkdown,
          tags: [...new Set([...cur.tags, ...it.ref.tags])],
          collectionIds: [...new Set([...cur.collectionIds, ...collectionIds])],
          attachments: [...zot, ...managed],
          zoteroStorage: undefined,
          details: it.ref.details ?? cur.details,
        });
        updatedIds.add(matchId);
      } else {
        const id = newId('ref');
        added.push(migrateReference({ ...it.ref, collectionIds, id, addedAt: Date.now() }));
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
