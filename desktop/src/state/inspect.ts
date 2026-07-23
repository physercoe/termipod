import { create } from 'zustand';
import { looksLikeDot } from './dotGraph';

/// The Inspect (J3) surface's open-tab model — the multi-source inspector shell
/// that replaces the round-1 paste textarea. Each tab is a viewer over one
/// source; the surface renders the viewer for its `kind`.
///
/// **kind** selects the viewer: `code` (CodeMirror 6 + trace lens + run-scratch,
/// W1) · `diff` (patch / two-blob compare, W2) · `log` (virtualized ANSI viewer,
/// W3) · `model` (checkpoint / graph inspector, W4). W1 ships the shell + the
/// `code` viewer; the other three render an honest "coming next" placard until
/// their wedge lands.
///
/// **source** is where the bytes come from: `paste` (device-local scratch),
/// `local` (a file picked via the native dialog), `workspace` (the Author
/// workspace tree), `remote` (SFTP over a saved connection), `hub` (a project
/// doc). Only `paste` bodies are authoritative in the store and persisted;
/// file-backed tabs persist **metadata only** and re-read their content on
/// activate (open question 1's proposed answer), so a huge log or checkpoint is
/// never copied into `localStorage`.

export type InspectKind = 'code' | 'diff' | 'log' | 'model' | 'graph' | 'megraph';
export type InspectSource = 'paste' | 'local' | 'workspace' | 'remote' | 'hub';

/// A reference to one readable source — the two sides of a two-blob compare
/// tab (W2, tier 2). Mirrors the file-locating fields of a tab; `body` carries an
/// inline snapshot for a `paste`/scratch side that has no re-readable path.
export interface InspectRef {
  source: InspectSource;
  title: string;
  path?: string;
  hostId?: string;
  projectId?: string;
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
  /// A language-mode override (else inferred from the path / content).
  lang?: string;
  /// For a two-blob **compare** tab (kind `diff`): the two sides. When both are
  /// set the diff viewer renders `@codemirror/merge` instead of the patch
  /// viewer; the tab's own `source`/content are then unused.
  left?: InspectRef;
  right?: InspectRef;
}

// Extension → kind dispatch, mirroring `documents.ts kindForFile` in spirit. A
// content sniff catches pasted patches (no extension). W1 only renders `code`
// live; diff/log/model still open (as tabs) and show their wedge placard.
const DIFF_EXTS = new Set(['diff', 'patch']);
const LOG_EXTS = new Set(['log']);
const MODEL_EXTS = new Set(['safetensors', 'gguf', 'onnx']);
const GRAPH_EXTS = new Set(['dot', 'gv']);

export function kindForInspectFile(ext: string, content: string): InspectKind {
  const e = ext.toLowerCase();
  if (MODEL_EXTS.has(e)) return 'model';
  if (GRAPH_EXTS.has(e)) return 'graph';
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
  open: (tab: Omit<InspectTab, 'id'>, body?: string) => string;
  close: (id: string) => void;
  setActive: (id: string | null) => void;
  setContent: (id: string, body: string) => void;
  setLoading: (id: string, v: boolean) => void;
  setError: (id: string, msg: string | undefined) => void;
  rename: (id: string, title: string) => void;
  setLang: (id: string, lang: string | undefined) => void;
  setKind: (id: string, kind: InspectKind) => void;
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
  const paste: Record<string, string> = {};
  for (const t of s.tabs) if (t.source === 'paste') paste[t.id] = s.content[t.id] ?? '';
  pending = { tabs: s.tabs, activeId: s.activeId, paste };
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

  open: (tab, body) => {
    // Focus an already-open file-backed tab instead of duplicating it.
    if (tab.source !== 'paste') {
      const existing = get().tabs.find(
        (t) => t.kind === tab.kind && t.source === tab.source && t.path === tab.path && t.hostId === tab.hostId && t.projectId === tab.projectId,
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
    set({ tabs, activeId, content });
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
}));
