import { create } from 'zustand';

/// The J2 Author workspace — multiple open documents as tabs (director request:
/// "the Author tab should support multiple tabs").
///
/// **Where do files live?** By default a document is device-local: its text is
/// persisted to `localStorage` (the WebView's storage, on the user's own
/// machine) under `termipod.documents.v1` — nothing is uploaded. A document can
/// additionally be **saved to a real file on disk** via the native Save dialog
/// (`doc_save`, which records `filePath`); "Save" then writes back to that path.
/// Promoting documents to hub Documents/Deliverables (with run provenance) is a
/// later round — see `research-tooling-landscape.md`.
///
/// A document's `kind` selects its editor and the shape of `body`:
///   • `markdown` — GFM/math/code source (the default);
///   • `diagram`  — draw.io XML;
///   • `canvas`   — a JSON spatial board (cards + typed edges, `state/canvas.ts`);
///   • `table`    — a JSON database grid (typed columns + rows, `ui/TableEditor`).
/// Canvas and table were formerly a separate top-level surface / are new — they
/// now live here as document kinds so a workspace holds many of each as tabs.

export type DocKind = 'markdown' | 'diagram' | 'canvas' | 'table';

export interface Doc {
  id: string;
  kind: DocKind;
  title: string;
  body: string; // markdown source · draw.io XML · canvas JSON · table JSON (per kind)
  filePath?: string; // linked on-disk file, if saved/opened via a native dialog
  dirty?: boolean; // unsaved changes relative to the linked file
  updatedAt: number;
}

// Default body for a freshly-created document of each non-markdown kind. Canvas
// and table bodies are JSON; a blank markdown/diagram starts empty-ish.
const CANVAS_SEED = '{"cards":[],"edges":[]}';
const TABLE_SEED =
  '{"columns":[{"id":"col0","name":"Name","type":"text"}],"rows":[{"id":"row0","cells":{}},{"id":"row1","cells":{}},{"id":"row2","cells":{}}]}';
function seedBody(kind: DocKind): string {
  switch (kind) {
    case 'markdown':
      return '# \n';
    case 'canvas':
      return CANVAS_SEED;
    case 'table':
      return TABLE_SEED;
    default:
      return '';
  }
}

interface DocsState {
  docs: Doc[];
  activeId: string | null;
  create: (kind?: DocKind, seed?: Partial<Doc>) => string;
  update: (id: string, patch: Partial<Doc>) => void;
  remove: (id: string) => void;
  setActive: (id: string | null) => void;
  markSaved: (id: string, filePath: string, title?: string) => void;
}

const LS_KEY = 'termipod.documents.v1';
const OLD_DRAFT = 'termipod.draft.author'; // pre-multi-doc single draft

let seq = 0;
function newId(): string {
  seq += 1;
  return `doc${Date.now().toString(36)}${seq}`;
}

function titleFromBody(body: string): string {
  const firstLine = body.split('\n').find((l) => l.trim() !== '')?.trim() ?? '';
  const stripped = firstLine.replace(/^#+\s*/, '').trim();
  return stripped !== '' ? stripped.slice(0, 60) : 'Untitled';
}

interface Persisted {
  docs: Doc[];
  activeId: string | null;
}

const OLD_CANVAS = 'termipod.canvas.v1'; // pre-merge standalone Canvas surface board
const CANVAS_MIGRATED = 'termipod.canvas.migrated'; // one-shot guard for the merge

// The standalone Canvas surface was folded into Author as a `canvas` document
// kind. Its single global board (if the user drew on it) is migrated once into a
// new canvas document so no work is lost; the guard flag makes it idempotent.
function migrateCanvas(p: Persisted): Persisted {
  try {
    if (localStorage.getItem(CANVAS_MIGRATED) === '1') return p;
    const raw = localStorage.getItem(OLD_CANVAS);
    localStorage.setItem(CANVAS_MIGRATED, '1');
    if (raw === null) return p;
    const board = JSON.parse(raw) as { cards?: unknown[] };
    if (!Array.isArray(board.cards) || board.cards.length === 0) return p;
    const doc: Doc = { id: newId(), kind: 'canvas', title: 'Canvas', body: raw, updatedAt: Date.now() };
    const docs = [...p.docs, doc];
    const next: Persisted = { docs, activeId: p.activeId ?? doc.id };
    save(next);
    return next;
  } catch {
    return p;
  }
}

function readDocs(): Persisted {
  try {
    const raw = localStorage.getItem(LS_KEY);
    if (raw !== null) return JSON.parse(raw) as Persisted;
  } catch {
    /* ignore */
  }
  // Migrate the pre-multi-doc single draft, if any, into the first document so
  // an existing in-progress draft isn't lost.
  try {
    const old = localStorage.getItem(OLD_DRAFT);
    if (old !== null && old.trim() !== '' && old.trim() !== '#') {
      const doc: Doc = {
        id: newId(),
        kind: 'markdown',
        title: titleFromBody(old),
        body: old,
        updatedAt: Date.now(),
      };
      return { docs: [doc], activeId: doc.id };
    }
  } catch {
    /* ignore */
  }
  return { docs: [], activeId: null };
}

function load(): Persisted {
  return migrateCanvas(readDocs());
}

function save(p: Persisted): void {
  try {
    localStorage.setItem(LS_KEY, JSON.stringify(p));
  } catch {
    /* ignore */
  }
}

export const useDocuments = create<DocsState>((set, get) => ({
  ...load(),

  create: (kind = 'markdown', seed) => {
    const id = newId();
    const doc: Doc = {
      id,
      kind,
      title: seed?.title ?? 'Untitled',
      body: seed?.body ?? seedBody(kind),
      filePath: seed?.filePath,
      updatedAt: Date.now(),
    };
    const docs = [...get().docs, doc];
    set({ docs, activeId: id });
    save({ docs, activeId: id });
    return id;
  },

  update: (id, patch) => {
    const docs = get().docs.map((d) => {
      if (d.id !== id) return d;
      const next: Doc = { ...d, ...patch, updatedAt: Date.now() };
      // A body edit re-derives an unsaved title only for markdown (whose first
      // line is prose) and only while the doc has no file; once linked to a file
      // the filename is the title of record. Canvas/diagram/table bodies are
      // structured (JSON/XML), so their titles are kept as set by the user.
      if (patch.body !== undefined && d.filePath === undefined && patch.title === undefined && d.kind === 'markdown') {
        next.title = titleFromBody(patch.body);
      }
      if (patch.body !== undefined && d.filePath !== undefined) next.dirty = true;
      return next;
    });
    set({ docs });
    save({ docs, activeId: get().activeId });
  },

  remove: (id) => {
    const docs = get().docs.filter((d) => d.id !== id);
    const activeId = get().activeId === id ? (docs[docs.length - 1]?.id ?? null) : get().activeId;
    set({ docs, activeId });
    save({ docs, activeId });
  },

  setActive: (id) => {
    set({ activeId: id });
    save({ docs: get().docs, activeId: id });
  },

  markSaved: (id, filePath, title) => {
    const docs = get().docs.map((d) =>
      d.id === id ? { ...d, filePath, title: title ?? d.title, dirty: false, updatedAt: Date.now() } : d,
    );
    set({ docs });
    save({ docs, activeId: get().activeId });
  },
}));
