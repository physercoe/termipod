import { create } from 'zustand';

/// Inspect (J3) **roots** — pinned, browsable origins for the tree pane
/// (round-3 plan §2). A root is *not* a tab and *not* an `InspectTab` field: it
/// is a separate, small, metadata-only list persisted under its own key, so the
/// round-2 persisted-tab shape (`termipod.debug.tabs`) is untouched.
///
/// T1 ships the `local` source (a pinned folder); `remote`/`hub`/`github`/`hf`
/// are the later wedges — the type carries them now so the store, persistence
/// and the tree pane generalize without a reshape.
///
/// Only the *metadata* of a root is persisted here — never any listing bytes.
/// Tree node state (which dirs are expanded, their loaded listings) is in-memory
/// only, owned by the tree component, so a stale tree is one collapse away from
/// truth and nothing unbounded is ever written to `localStorage`.

export interface InspectRoot {
  id: string;
  source: 'local' | 'remote' | 'hub' | 'github' | 'hf';
  /// Basename / repo@ref; user-renamable.
  label: string;
  /// local: absolute root · remote: abs/rel dir · hub: '' (docs root).
  path?: string;
  /// remote: the SFTP connection id.
  hostId?: string;
  /// hub: the project id.
  projectId?: string;
  /// github: `{ id: 'owner/repo', ref, sha }` · hf: `{ id: model-id, ref, sha }`.
  repo?: { id: string; ref: string; sha: string };
}

interface RootsState {
  roots: InspectRoot[];
  /// Pin a root. A `local` root with a path already pinned is a no-op that
  /// returns the existing id (no duplicate); others always append.
  addRoot: (root: Omit<InspectRoot, 'id'>) => string;
  removeRoot: (id: string) => void;
  renameRoot: (id: string, label: string) => void;
}

const LS_KEY = 'termipod.inspect.roots';

let seq = 0;
function newId(): string {
  seq += 1;
  return `root${Date.now().toString(36)}${seq}`;
}

function load(): InspectRoot[] {
  try {
    const raw = localStorage.getItem(LS_KEY);
    if (raw !== null) {
      const arr = JSON.parse(raw) as unknown;
      if (Array.isArray(arr)) return arr as InspectRoot[];
    }
  } catch {
    /* ignore malformed persisted roots */
  }
  return [];
}

function persist(roots: InspectRoot[]): void {
  try {
    localStorage.setItem(LS_KEY, JSON.stringify(roots));
  } catch (e) {
    console.error(`[inspect] failed to persist "${LS_KEY}"`, e);
  }
}

export const useInspectRoots = create<RootsState>((set, get) => ({
  roots: load(),

  addRoot: (root) => {
    if (root.source === 'local' && root.path !== undefined) {
      const existing = get().roots.find((r) => r.source === 'local' && r.path === root.path);
      if (existing) return existing.id;
    }
    const id = newId();
    const roots = [...get().roots, { ...root, id }];
    set({ roots });
    persist(roots);
    return id;
  },

  removeRoot: (id) => {
    const roots = get().roots.filter((r) => r.id !== id);
    set({ roots });
    persist(roots);
  },

  renameRoot: (id, label) => {
    const roots = get().roots.map((r) => (r.id === id ? { ...r, label } : r));
    set({ roots });
    persist(roots);
  },
}));

// ── pure helpers (unit-tested) ───────────────────────────────────────────────

/// The deepest pinned **local** root that contains `filePath` (path-boundary
/// aware, so `/a/proj2` does not match a file under `/a/proj` — a plain
/// `startsWith` would). Returns the root's path, or `undefined` when no pinned
/// root contains the file. Feeds the W4 trace form's repo-root default and the
/// stack-trace resolver's candidate list (plan §3 item 6).
export function innermostLocalRoot(roots: InspectRoot[], filePath: string): string | undefined {
  let best: string | undefined;
  for (const r of roots) {
    if (r.source !== 'local' || r.path === undefined || r.path === '') continue;
    if (!containsPath(r.path, filePath)) continue;
    if (best === undefined || r.path.length > best.length) best = r.path;
  }
  return best;
}

/// True when `child` is `root` itself or lies beneath it, comparing whole path
/// segments (so `/a/proj` does not "contain" `/a/project/x`). Separator-agnostic
/// (`/` or `\`) and case-sensitive — desktop hosts here are Linux/macOS; a
/// Windows drift would only *miss* a root, degrading to the file-dir default.
export function containsPath(root: string, child: string): boolean {
  const r = root.replace(/[\\/]+$/, '');
  const c = child.replace(/[\\/]+$/, '');
  if (c === r) return true;
  return c.startsWith(r) && (c[r.length] === '/' || c[r.length] === '\\');
}
