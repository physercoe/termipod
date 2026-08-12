import { create } from 'zustand';
import { looksLikeDot } from './dotGraph.ts';
import { mediaKindFor } from './inspectMedia.ts';

/// The Inspect (J3) surface's open-tab model — the multi-source inspector shell
/// that replaces the round-1 paste textarea. Each tab is a viewer over one
/// source; the surface renders the viewer for its `kind`.
///
/// **kind** selects the viewer: `code` (CodeMirror 6 + trace lens + run-scratch,
/// W1) · `diff` (patch / two-blob compare, W2) · `log` (virtualized ANSI viewer,
/// W3) · `model` (checkpoint / graph inspector, W4) · `image`/`video`/`audio`/
/// `pdf` (media previews, streamed rather than read into the store). W1 ships
/// the shell + the `code` viewer; the other three render an honest "coming
/// next" placard until their wedge lands.
///
/// **source** is where the bytes come from: `paste` (device-local scratch),
/// `local` (a file picked via the native dialog), `workspace` (the Author
/// workspace tree), `remote` (SFTP over a saved connection), `hub` (a project
/// doc). Only `paste` bodies are authoritative in the store and persisted;
/// file-backed tabs persist **metadata only** and re-read their content on
/// activate (open question 1's proposed answer), so a huge log or checkpoint is
/// never copied into `localStorage`.

export type InspectKind =
  | 'code' | 'diff' | 'log' | 'model' | 'graph' | 'megraph' | 'modgraph' | 'archgraph'
  | 'markdown' | 'table'
  /// Media previews. These tabs never enter `content` — their bytes stream from
  /// the main process on demand, so a 2 GB video costs the store nothing.
  | 'image' | 'video' | 'audio' | 'pdf'
  /// The pane-state rule debugger (pane-state-manifests P4). Not file-backed:
  /// its subject is a running agent's terminal, or a screen the user pasted,
  /// and its body is fetched from the hub on activate. Never persisted with a
  /// body — a pane's contents are not something to leave in localStorage.
  | 'panestate';
export type InspectSource = 'paste' | 'local' | 'workspace' | 'remote' | 'hub' | 'github' | 'hf';

/// A pinned forge snapshot (round-3 T3). `id` is `owner/repo` (GitHub) or the
/// model id (Hugging Face); `ref` is the human ref (branch / tag), `sha` the
/// resolved immutable commit — every tree + blob read uses the sha, so a moving
/// branch can't tear a tree mid-read.
export interface ForgeRepo {
  id: string;
  ref: string;
  sha: string;
}

/// A reference to one readable source — the two sides of a two-blob compare
/// tab (W2, tier 2). Mirrors the file-locating fields of a tab; `body` carries an
/// inline snapshot for a `paste`/scratch side that has no re-readable path.
export interface InspectRef {
  source: InspectSource;
  title: string;
  path?: string;
  hostId?: string;
  projectId?: string;
  /// For a `github`/`hf` ref: the pinned forge snapshot the path is read from.
  repo?: ForgeRepo;
  lang?: string;
  body?: string;
}

export interface InspectTab {
  id: string;
  kind: InspectKind;
  source: InspectSource;
  title: string;
  /// Absolute (local/remote) or workspace-relative (workspace) or hub path.
  path?: string;
  /// The SFTP connection id, for a `remote` tab.
  hostId?: string;
  /// The hub project id, for a `hub` tab.
  projectId?: string;
  /// For a `panestate` tab in live mode: the agent whose pane is explained.
  /// Mutually exclusive with `family` + a pasted body — the hub route refuses
  /// a request carrying both, and the tab mirrors that rather than choosing.
  agentId?: string;
  /// For a `panestate` tab in supplied-screen mode: which engine's manifest to
  /// evaluate the pasted body against.
  family?: string;
  /// The pinned forge snapshot, for a `github`/`hf` tab.
  repo?: ForgeRepo;
  /// A language-mode override (else inferred from the path / content).
  lang?: string;
  /// For a two-blob **compare** tab (kind `diff`): the two sides. When both are
  /// set the diff viewer renders `@codemirror/merge` instead of the patch
  /// viewer; the tab's own `source`/content are then unused.
  left?: InspectRef;
  right?: InspectRef;
  /// Never persisted (neither the tab nor its body). For paste-source tabs
  /// whose body is bulky and reproducible — e.g. the git lens's working-tree
  /// diff (up to 24 MB, vs the ~5 MB whole-app localStorage quota): persisting
  /// one would make the debounced write throw and silently wedge persistence
  /// for EVERY tab until the app restarts.
  ephemeral?: boolean;
}

// Extension → kind dispatch, mirroring `documents.ts kindForFile` in spirit. A
// content sniff catches pasted patches (no extension). W1 only renders `code`
// live; diff/log/model still open (as tabs) and show their wedge placard.
const DIFF_EXTS = new Set(['diff', 'patch']);
const LOG_EXTS = new Set(['log']);
const MODEL_EXTS = new Set(['safetensors', 'gguf', 'onnx']);
const GRAPH_EXTS = new Set(['dot', 'gv']);
const MARKDOWN_EXTS = new Set(['md', 'markdown']);
const TABLE_EXTS = new Set(['csv', 'tsv']);

export function kindForInspectFile(ext: string, content: string): InspectKind {
  const e = ext.toLowerCase();
  // Media first, and by extension only. Every branch below this point reasons
  // about TEXT — the content sniff, the DOT probe, the `code` fallback — and a
  // binary file reaching any of them ends in a strict-UTF-8 read that throws a
  // decoder error at the user. `content` is empty for every picker anyway
  // (nothing has been read yet), so a sniff could not save a video regardless.
  const media = mediaKindFor(e);
  if (media !== null) return media;
  if (MODEL_EXTS.has(e)) return 'model';
  if (GRAPH_EXTS.has(e)) return 'graph';
  if (MARKDOWN_EXTS.has(e)) return 'markdown';
  if (TABLE_EXTS.has(e)) return 'table';
  if (DIFF_EXTS.has(e)) return 'diff';
  if (LOG_EXTS.has(e)) return 'log';
  // Content sniff: a unified diff / git patch pasted without an extension.
  const head = content.slice(0, 2048);
  if (/^(diff --git |Index: |--- \S+\n\+\+\+ )/m.test(head) && /^@@ /m.test(head)) return 'diff';
  if (looksLikeDot(content)) return 'graph';
  return 'code';
}

interface InspectState {
  tabs: InspectTab[];
  activeId: string | null;
  /// Body per tab — authoritative + persisted for `paste`, a lazily-filled cache
  /// for file-backed tabs.
  content: Record<string, string>;
  /// True while a file-backed tab's content is being (re-)read.
  loading: Record<string, boolean>;
  /// Read error for a tab, if its source failed to load.
  error: Record<string, string | undefined>;
  /// The user's 1-based `[fromLine, toLine]` line selection per tab (coworking
  /// G2). Deliberately NOT persisted: a selection is about what the user is
  /// looking at right now, and restoring one across a restart would tell an
  /// agent they had picked out lines they last touched days ago.
  selection: Record<string, [number, number] | undefined>;
  open: (tab: Omit<InspectTab, 'id'>, body?: string) => string;
  close: (id: string) => void;
  setActive: (id: string | null) => void;
  setContent: (id: string, body: string) => void;
  setLoading: (id: string, v: boolean) => void;
  setError: (id: string, msg: string | undefined) => void;
  setSelection: (id: string, sel: [number, number] | null) => void;
  rename: (id: string, title: string) => void;
  setLang: (id: string, lang: string | undefined) => void;
  setKind: (id: string, kind: InspectKind) => void;
  /// Which engine's manifest a supplied-screen `panestate` tab evaluates
  /// against. Persisted with the tab: re-picking it on every restart would
  /// make the pasted screen useless.
  setFamily: (id: string, family: string) => void;
}

const LS_KEY = 'termipod.debug.tabs';
const OLD_SCRATCH = 'termipod.draft.debug'; // round-1 single paste draft

let seq = 0;
function newId(): string {
  seq += 1;
  return `insp${Date.now().toString(36)}${seq}`;
}

interface Persisted {
  tabs: InspectTab[];
  activeId: string | null;
  // Only paste-tab bodies are stored (keyed by tab id).
  paste: Record<string, string>;
}

function load(): { tabs: InspectTab[]; activeId: string | null; content: Record<string, string> } {
  try {
    const raw = localStorage.getItem(LS_KEY);
    if (raw !== null) {
      const p = JSON.parse(raw) as Persisted;
      const content: Record<string, string> = {};
      for (const t of p.tabs) if (t.source === 'paste') content[t.id] = p.paste[t.id] ?? '';
      return { tabs: p.tabs, activeId: p.activeId, content };
    }
  } catch {
    /* fall through to migration */
  }
  // Migrate the round-1 single scratch draft into one paste tab so nothing is
  // lost when the surface upgrades from textarea to tabbed inspector.
  try {
    const old = localStorage.getItem(OLD_SCRATCH);
    if (old !== null && old.trim() !== '') {
      const id = newId();
      const tab: InspectTab = { id, kind: 'code', source: 'paste', title: 'Scratch' };
      return { tabs: [tab], activeId: id, content: { [id]: old } };
    }
  } catch {
    /* ignore */
  }
  return { tabs: [], activeId: null, content: {} };
}

let saveTimer: ReturnType<typeof setTimeout> | undefined;
let pending: Persisted | null = null;
function writeNow(): void {
  if (pending === null) return;
  try {
    localStorage.setItem(LS_KEY, JSON.stringify(pending));
  } catch (e) {
    console.error(`[inspect] failed to persist "${LS_KEY}"`, e);
  }
  pending = null;
}
function persist(s: Pick<InspectState, 'tabs' | 'activeId' | 'content'>): void {
  // Ephemeral tabs are dropped wholesale (tab + body); if the active tab is
  // ephemeral, fall back to the last persistable tab so the restored activeId
  // never dangles.
  const tabs = s.tabs.filter((t) => t.ephemeral !== true);
  const activeId = tabs.some((t) => t.id === s.activeId) ? s.activeId : (tabs[tabs.length - 1]?.id ?? null);
  const paste: Record<string, string> = {};
  for (const t of tabs) if (t.source === 'paste') paste[t.id] = s.content[t.id] ?? '';
  pending = { tabs, activeId, paste };
  if (saveTimer !== undefined) clearTimeout(saveTimer);
  saveTimer = setTimeout(() => {
    saveTimer = undefined;
    writeNow();
  }, 400);
}
if (typeof window !== 'undefined') window.addEventListener('beforeunload', writeNow);

export const useInspect = create<InspectState>((set, get) => ({
  ...load(),
  loading: {},
  error: {},
  selection: {},

  open: (tab, body) => {
    // Focus an already-open file-backed tab instead of duplicating it.
    if (tab.source !== 'paste') {
      const existing = get().tabs.find(
        (t) =>
          t.kind === tab.kind &&
          t.source === tab.source &&
          t.path === tab.path &&
          t.hostId === tab.hostId &&
          t.projectId === tab.projectId &&
          t.repo?.id === tab.repo?.id &&
          t.repo?.sha === tab.repo?.sha,
      );
      if (existing) {
        set({ activeId: existing.id });
        persist({ ...get(), activeId: existing.id });
        return existing.id;
      }
    }
    const id = newId();
    const next: InspectTab = { ...tab, id };
    const tabs = [...get().tabs, next];
    const content = body !== undefined ? { ...get().content, [id]: body } : get().content;
    set({ tabs, activeId: id, content });
    persist({ tabs, activeId: id, content });
    return id;
  },

  close: (id) => {
    const tabs = get().tabs.filter((t) => t.id !== id);
    const activeId = get().activeId === id ? (tabs[tabs.length - 1]?.id ?? null) : get().activeId;
    const content = { ...get().content };
    delete content[id];
    // Ids are minted from a counter + Date.now(), so a closed tab's key would
    // not be reused — but leaving it would leak one entry per closed tab for
    // the session's lifetime.
    const selection = { ...get().selection };
    delete selection[id];
    set({ tabs, activeId, content, selection });
    persist({ tabs, activeId, content });
  },

  setActive: (id) => {
    set({ activeId: id });
    persist({ ...get(), activeId: id });
  },

  setContent: (id, body) => {
    const content = { ...get().content, [id]: body };
    set({ content });
    persist({ ...get(), content });
  },

  setLoading: (id, v) => set({ loading: { ...get().loading, [id]: v } }),
  setError: (id, msg) => set({ error: { ...get().error, [id]: msg } }),

  // A cleared selection deletes the key rather than storing `undefined`, so
  // `sourcesNow()` reads absence the same way whether the tab never had one or
  // the user just clicked it away.
  setSelection: (id, sel) => {
    const selection = { ...get().selection };
    if (sel === null) delete selection[id];
    else selection[id] = sel;
    set({ selection });
  },

  rename: (id, title) => {
    const tabs = get().tabs.map((t) => (t.id === id ? { ...t, title } : t));
    set({ tabs });
    persist({ ...get(), tabs });
  },

  setLang: (id, lang) => {
    const tabs = get().tabs.map((t) => (t.id === id ? { ...t, lang } : t));
    set({ tabs });
    persist({ ...get(), tabs });
  },

  setKind: (id, kind) => {
    const tabs = get().tabs.map((t) => (t.id === id ? { ...t, kind } : t));
    set({ tabs });
    persist({ ...get(), tabs });
  },

  setFamily: (id, family) => {
    const tabs = get().tabs.map((t) => (t.id === id ? { ...t, family } : t));
    set({ tabs });
    persist({ ...get(), tabs });
  },
}));
