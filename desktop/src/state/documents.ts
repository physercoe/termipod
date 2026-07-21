import { create } from 'zustand';
import { backupCorrupt } from './persist';
import { csvToTable, isTableBody, parseTable, serializeTable, tableToCsv } from './table';
import { figureBySpec, specForFile, type FigureSpec } from './figures';

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
///   • `figure`   — a text-to-figure source (mermaid/graphviz/vega-lite, …); the
///     `spec` field selects the renderer via the `state/figures` registry.
/// Canvas and table were formerly a separate top-level surface / are new — they
/// now live here as document kinds so a workspace holds many of each as tabs.

export type DocKind = 'markdown' | 'diagram' | 'canvas' | 'table' | 'figure';

export interface Doc {
  id: string;
  kind: DocKind;
  title: string;
  body: string; // markdown source · draw.io XML · canvas JSON · table JSON · figure source (per kind)
  // For a `figure` doc: which renderer its `body` targets (registry discriminator).
  // Additive — persisted `termipod.documents.v1` blobs without it are unaffected.
  spec?: FigureSpec;
  filePath?: string; // linked on-disk file, if saved/opened via a native dialog
  dirty?: boolean; // unsaved changes relative to the linked file
  updatedAt: number;
}

// Default body for a freshly-created document of each non-markdown kind. Canvas
// and table bodies are JSON; a blank markdown/diagram starts empty-ish.
const CANVAS_SEED = '{"cards":[],"edges":[]}';
const TABLE_SEED =
  '{"columns":[{"id":"col0","name":"Name","type":"text"}],"rows":[{"id":"row0","cells":{}},{"id":"row1","cells":{}},{"id":"row2","cells":{}}]}';
export function seedBody(kind: DocKind): string {
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

// ── On-disk file round-trip ─────────────────────────────────────────────────
// A document's kind ↔ file extension, and the body ↔ file-text bridge, so canvas
// and table save/open as real files in the workspace tree.
//
// The in-app body is ALWAYS JSON for canvas/table. On disk:
//   • canvas → `.canvas` JSON (verbatim);
//   • table  → **`.json` (canonical, lossless)** — the whole typed model. A `.csv`
//     is still supported for import/export interop, but it is lossy (CSV is
//     untyped), so it converts at the boundary; a table linked to a `.csv` keeps
//     re-saving as CSV, while a new table defaults to `.json`.
//   • markdown → `.md`; diagram → draw.io `.drawio` XML.

/// Default extension for a NEW save of each kind (table → the lossless `.json`).
/// Figures are spec-dependent — use `extForDoc`, which knows the spec; a bare
/// figure kind here falls back to `.txt` (should never be reached for figures).
export function extForKind(kind: DocKind): string {
  switch (kind) {
    case 'canvas':
      return 'canvas';
    case 'table':
      return 'json';
    case 'diagram':
      return 'drawio';
    case 'figure':
      return 'txt';
    default:
      return 'md';
  }
}

/// Doc-aware extension: a `figure` saves as its spec's registry extension
/// (`.mmd`/`.dot`/`.vl.json`); every other kind defers to `extForKind`.
export function extForDoc(doc: Pick<Doc, 'kind' | 'spec'>): string {
  if (doc.kind === 'figure' && doc.spec !== undefined) return figureBySpec(doc.spec)?.ext ?? 'txt';
  return extForKind(doc.kind);
}

/// The document kind (+ figure spec) for a file being opened. Extension decides
/// everything except `.json`, which is content-sniffed in order: a `{columns,
/// rows}` doc is a table → a Vega-Lite spec (registry sniff) → otherwise plain
/// text (markdown), so arbitrary JSON is never hijacked. A registry figure
/// extension (`.mmd`/`.dot`/`.vl.json`) resolves to `figure` + its spec.
export function kindForFile(ext: string, content: string): { kind: DocKind; spec?: FigureSpec } {
  switch (ext.toLowerCase()) {
    case 'canvas':
      return { kind: 'canvas' };
    case 'csv':
      return { kind: 'table' };
    case 'drawio':
      return { kind: 'diagram' };
    case 'json': {
      if (isTableBody(content)) return { kind: 'table' };
      const spec = specForFile('json', content);
      return spec !== undefined ? { kind: 'figure', spec } : { kind: 'markdown' };
    }
    default: {
      const spec = specForFile(ext, content);
      return spec !== undefined ? { kind: 'figure', spec } : { kind: 'markdown' };
    }
  }
}

// The disk representation depends on the linked file's extension, not just the
// kind: a table is JSON-verbatim in a `.json` and lossy-CSV in a `.csv`.
export function bodyToFile(kind: DocKind, body: string, ext: string, nameFallback: string): string {
  return kind === 'table' && ext.toLowerCase() === 'csv' ? tableToCsv(parseTable(body, nameFallback)) : body;
}
export function fileToBody(kind: DocKind, text: string, ext: string, nameFallback: string): string {
  return kind === 'table' && ext.toLowerCase() === 'csv' ? serializeTable(csvToTable(text, nameFallback)) : text;
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
  let raw: string | null = null;
  try {
    raw = localStorage.getItem(LS_KEY);
    if (raw !== null) return JSON.parse(raw) as Persisted;
  } catch (e) {
    // Back up the corrupt blob before falling through to migration/empty, so a
    // later save doesn't destroy the only (recoverable) copy of the user's docs.
    if (raw !== null) backupCorrupt(LS_KEY, raw, e);
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

// Persist is debounced: `update` runs on every keystroke while editing a doc,
// and JSON.stringify-ing the whole document set + writing it synchronously on
// each one is a main-thread stall on a large workspace (#311). Coalesce to a
// trailing write ~400ms after typing pauses. A `beforeunload` flush guarantees
// the last edits land even if the window closes inside the debounce window.
let saveTimer: ReturnType<typeof setTimeout> | undefined;
let pending: Persisted | null = null;
function writeNow(): void {
  if (pending === null) return;
  try {
    localStorage.setItem(LS_KEY, JSON.stringify(pending));
  } catch (e) {
    console.error(`[documents] failed to persist "${LS_KEY}" (quota exceeded?)`, e);
  }
  pending = null;
}
function save(p: Persisted): void {
  pending = p;
  if (saveTimer !== undefined) clearTimeout(saveTimer);
  saveTimer = setTimeout(() => {
    saveTimer = undefined;
    writeNow();
  }, 400);
}
if (typeof window !== 'undefined') {
  window.addEventListener('beforeunload', writeNow);
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
      spec: seed?.spec, // which renderer, for a figure doc (dropped for other kinds)
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
