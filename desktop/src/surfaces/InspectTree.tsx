import { useEffect, useMemo, useRef, useState } from 'react';
import { useT } from '../i18n';
import { Icon, type IconName } from '../ui/Icon';
import { kindForInspectFile } from '../state/inspect';
import { useInspectRoots, type InspectRoot } from '../state/inspectRoots';
import { foldHubDocs, nodeMatches, type TreeNode } from '../state/inspectTree';
import { localList, treeIndex, type TreeIndexEntry } from '../state/localfs';
import { sftpBrowse } from '../state/inspectSources';
import { fetchForgeTree } from '../state/forge';
import { useSession } from '../state/session';
import { isShell } from '../platform';
import type { PickResult } from './InspectOpen';

/// The Inspect (J3) **tree pane** — round-3 T1 (local) + T2 (remote SFTP, hub
/// project). One node model over three roots: a local folder (`localfs_list`,
/// lazy per dir), a remote directory (`sftpBrowse`, lazy per dir, one cached SSH
/// session per host), and a hub project's `docs_root` (one flat fetch folded
/// into the same tree). A file click opens it into a viewer tab through the
/// surface's `pick()` path (compare mode included). Per-root name filter:
/// recursive index (local), loaded-nodes-only (remote — cap discipline), or the
/// full flat list (hub — exact for free).
///
/// GitHub/HF roots are the T3 wedge. Every listing/index is lazy and capped, and
/// every cap is surfaced rather than implying it read everything.

// Cosmetic mirror of the main-process `SKIP_DIRS` (electron fsutil.ts): heavy /
// generated folders are tagged (local trees list them; drift here only changes a
// badge, never behaviour — the walk's skip is authoritative).
const HEAVY_DIRS = new Set([
  'node_modules', '.git', 'target', 'dist', 'build', '.next', '.venv', 'venv',
  '__pycache__', '.cache', '.idea', '.vscode', '.svn', '.hg',
]);

function baseName(p: string): string {
  const s = p.replace(/[\\/]+$/, '');
  const i = Math.max(s.lastIndexOf('/'), s.lastIndexOf('\\'));
  return i >= 0 ? s.slice(i + 1) : s;
}
function extOf(name: string): string {
  const i = name.lastIndexOf('.');
  return i >= 0 ? name.slice(i + 1) : '';
}
// Join a remote directory with a child name (mirrors RemotePicker's `child`).
function joinRemote(dir: string, name: string): string {
  return dir === '.' || dir === '' ? name : `${dir.replace(/\/+$/, '')}/${name}`;
}
function msg(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
function sourceIcon(source: InspectRoot['source']): IconName {
  if (source === 'remote') return 'terminal';
  if (source === 'hub') return 'cloud';
  if (source === 'github') return 'git-branch';
  if (source === 'hf') return 'sliders';
  return 'folder';
}
// Sources whose whole tree arrives in one fetch and is folded client-side (hub
// docs, forge trees) rather than listed one directory per expand.
function isFolded(source: InspectRoot['source']): boolean {
  return source === 'hub' || source === 'github' || source === 'hf';
}

interface Listing {
  nodes: TreeNode[];
  truncated: boolean;
}
type Folded = { children: Map<string, TreeNode[]>; files: TreeNode[]; truncated: boolean };

// ── one directory's rendered children (recursive) ────────────────────────────
interface BranchCtx {
  ckOf: (key: string) => string;
  listings: Record<string, Listing>;
  expanded: Set<string>;
  loading: Set<string>;
  errors: Record<string, string>;
  onToggle: (key: string) => void;
  onOpen: (node: TreeNode) => void;
  onRetry: (key: string) => void;
}

function Branch({ nodeKey, depth, ctx }: { nodeKey: string; depth: number; ctx: BranchCtx }): JSX.Element {
  const t = useT();
  const ck = ctx.ckOf(nodeKey);
  const listing = ctx.listings[ck];
  const pad = { paddingLeft: `${8 + depth * 14}px` };

  if (ctx.errors[ck] !== undefined) {
    return (
      <div className="inspect-tree-msg err" style={pad}>
        <Icon name="alert" size={13} />
        <span className="inspect-tree-name">{ctx.errors[ck]}</span>
        <button className="link-btn small" onClick={() => ctx.onRetry(nodeKey)}>
          {t('inspect.retry')}
        </button>
      </div>
    );
  }
  if (listing === undefined) {
    return (
      <div className="inspect-tree-msg muted" style={pad}>
        {ctx.loading.has(ck) ? t('inspect.loading') : '…'}
      </div>
    );
  }
  return (
    <>
      {listing.nodes.map((n) =>
        n.is_dir ? (
          <div key={n.key}>
            <button className="inspect-tree-row" style={pad} onClick={() => ctx.onToggle(n.key)} title={n.key}>
              <Icon name={ctx.expanded.has(ctx.ckOf(n.key)) ? 'chevron-down' : 'chevron-right'} size={12} />
              <Icon name="folder" size={13} />
              <span className="inspect-tree-name">{n.name}</span>
              {HEAVY_DIRS.has(n.name) && <span className="inspect-tree-tag small muted">{t('inspect.heavyDir')}</span>}
            </button>
            {ctx.expanded.has(ctx.ckOf(n.key)) && <Branch nodeKey={n.key} depth={depth + 1} ctx={ctx} />}
          </div>
        ) : (
          <button key={n.key} className="inspect-tree-row file" style={pad} onClick={() => ctx.onOpen(n)} title={n.key}>
            <span className="inspect-tree-spacer" />
            <Icon name="file-text" size={13} />
            <span className="inspect-tree-name">{n.name}</span>
          </button>
        ),
      )}
      {listing.truncated && (
        <div className="inspect-tree-msg muted" style={{ paddingLeft: `${8 + (depth + 1) * 14}px` }}>
          {t('inspect.listingCapped')}
        </div>
      )}
    </>
  );
}

export function InspectTree({
  width,
  onPick,
  onAddFolder,
  onClose,
}: {
  width: number;
  onPick: (r: PickResult) => void;
  onAddFolder: () => void;
  onClose: () => void;
}): JSX.Element {
  const t = useT();
  const roots = useInspectRoots((s) => s.roots);
  const removeRoot = useInspectRoots((s) => s.removeRoot);
  const renameRoot = useInspectRoots((s) => s.renameRoot);
  const client = useSession((s) => s.client);

  // Caches namespaced by `ck(rootId, nodeKey)` — remote paths / hub paths can
  // collide across roots, so the rootId prefix keeps them apart (and makes
  // refresh a single prefix-drop).
  const [listings, setListings] = useState<Record<string, Listing>>({});
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState<Set<string>>(new Set());
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [filters, setFilters] = useState<Record<string, string>>({});
  const [indexes, setIndexes] = useState<Record<string, { entries: TreeIndexEntry[]; truncated: boolean }>>({});
  const [indexBusy, setIndexBusy] = useState<Set<string>>(new Set());
  const [foldedFiles, setFoldedFiles] = useState<Record<string, TreeNode[]>>({});
  const [renaming, setRenaming] = useState<string | null>(null);
  const foldedPromise = useRef<Record<string, Promise<Folded>>>({});
  const seenRoots = useRef<Set<string>>(new Set());
  const mounted = useRef(false);

  const ck = (rootId: string, key: string): string => `${rootId} ${key}`;
  const rootKey = (root: InspectRoot): string => (isFolded(root.source) ? '' : (root.path ?? ''));

  // One fetch → folded tree, memoized per root: hub docs, or a forge repo tree at
  // its pinned SHA (both land in the same TreeNode shape via `foldHubDocs`).
  function ensureFolded(root: InspectRoot): Promise<Folded> {
    let p = foldedPromise.current[root.id];
    if (p === undefined) {
      if (root.source === 'hub') {
        p =
          client === null
            ? Promise.reject(new Error(t('inspect.noHub')))
            : client
                .listProjectDocs(root.projectId ?? '')
                .then((docs) => ({ ...foldHubDocs(docs.map((d) => ({ path: typeof d.path === 'string' ? d.path : '', is_dir: d.is_dir === true }))), truncated: false }));
      } else if (root.source === 'github' || root.source === 'hf') {
        p =
          root.repo === undefined
            ? Promise.reject(new Error('forge root is missing its repo snapshot'))
            : fetchForgeTree(root.source, root.repo).then((r) => ({ ...foldHubDocs(r.entries), truncated: r.truncated }));
      } else {
        p = Promise.reject(new Error(`inspect: source '${root.source}' is not a folded tree`));
      }
      foldedPromise.current[root.id] = p;
    }
    void p.then((folded) => setFoldedFiles((m) => ({ ...m, [root.id]: folded.files }))).catch(() => undefined);
    return p;
  }

  async function fetchChildren(root: InspectRoot, nodeKey: string): Promise<Listing> {
    if (root.source === 'local') {
      const l = await localList(nodeKey);
      return { nodes: l.entries.map((e) => ({ name: e.name, key: e.path, is_dir: e.is_dir })), truncated: l.truncated };
    }
    if (root.source === 'remote') {
      const es = await sftpBrowse(root.hostId ?? '', nodeKey);
      const sorted = es.slice().sort((a, b) => Number(b.is_dir) - Number(a.is_dir) || a.name.localeCompare(b.name));
      return { nodes: sorted.map((e) => ({ name: e.name, key: joinRemote(nodeKey, e.name), is_dir: e.is_dir })), truncated: false };
    }
    const folded = await ensureFolded(root);
    // Surface a truncated forge tree at the root level (the whole tree was capped).
    return { nodes: folded.children.get(nodeKey) ?? [], truncated: nodeKey === rootKey(root) ? folded.truncated : false };
  }

  function loadDir(root: InspectRoot, nodeKey: string): void {
    const k = ck(root.id, nodeKey);
    setLoading((s) => new Set(s).add(k));
    setErrors((e) => {
      const n = { ...e };
      delete n[k];
      return n;
    });
    void fetchChildren(root, nodeKey)
      .then((res) => setListings((m) => ({ ...m, [k]: res })))
      .catch((err: unknown) => setErrors((e) => ({ ...e, [k]: msg(err) })))
      .finally(() =>
        setLoading((s) => {
          const n = new Set(s);
          n.delete(k);
          return n;
        }),
      );
  }

  function toggleDir(root: InspectRoot, nodeKey: string): void {
    const k = ck(root.id, nodeKey);
    setExpanded((s) => {
      const n = new Set(s);
      if (n.has(k)) n.delete(k);
      else {
        n.add(k);
        if (listings[k] === undefined && !loading.has(k)) loadDir(root, nodeKey);
      }
      return n;
    });
  }

  // Auto-expand a genuinely new root once. On the first mount, only cheap local
  // roots auto-open (never auto-connect a persisted remote root or auto-fetch a
  // hub project on boot); roots added *during* the session open regardless of
  // source (the user just pinned them). A later user collapse always sticks.
  useEffect(() => {
    for (const r of roots) {
      if (seenRoots.current.has(r.id)) continue;
      seenRoots.current.add(r.id);
      if (!(mounted.current || r.source === 'local')) continue;
      const key = rootKey(r);
      setExpanded((s) => new Set(s).add(ck(r.id, key)));
      if (listings[ck(r.id, key)] === undefined) loadDir(r, key);
    }
    mounted.current = true;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [roots]);

  function refreshRoot(root: InspectRoot): void {
    const prefix = `${root.id} `;
    const drop = (obj: Record<string, unknown>): void => {
      for (const kk of Object.keys(obj)) if (kk.startsWith(prefix)) delete obj[kk];
    };
    setListings((m) => {
      const n = { ...m };
      drop(n);
      return n;
    });
    setErrors((m) => {
      const n = { ...m };
      drop(n);
      return n;
    });
    delete foldedPromise.current[root.id];
    setIndexes((m) => {
      const n = { ...m };
      delete n[root.id];
      return n;
    });
    setFoldedFiles((m) => {
      const n = { ...m };
      delete n[root.id];
      return n;
    });
    // Re-list the root itself so the pane doesn't blank on refresh.
    const key = rootKey(root);
    if (expanded.has(ck(root.id, key))) loadDir(root, key);
  }

  function buildIndex(root: InspectRoot): void {
    if (root.source !== 'local' || root.path === undefined || indexes[root.id] !== undefined || indexBusy.has(root.id)) return;
    setIndexBusy((s) => new Set(s).add(root.id));
    void treeIndex(root.path)
      .then((idx) => setIndexes((m) => ({ ...m, [root.id]: idx })))
      .catch((err: unknown) => setErrors((e) => ({ ...e, [`idx:${root.id}`]: msg(err) })))
      .finally(() =>
        setIndexBusy((s) => {
          const n = new Set(s);
          n.delete(root.id);
          return n;
        }),
      );
  }

  function onFilterChange(root: InspectRoot, q: string): void {
    setFilters((f) => ({ ...f, [root.id]: q }));
    if (q.trim() === '') return;
    if (root.source === 'local') buildIndex(root);
    else if (isFolded(root.source)) void ensureFolded(root); // load the flat list for the exact filter
  }

  function openFile(root: InspectRoot, node: TreeNode): void {
    const kind = kindForInspectFile(extOf(node.name), '');
    if (root.source === 'remote') onPick({ source: 'remote', kind, title: node.name, path: node.key, hostId: root.hostId });
    else if (root.source === 'github' || root.source === 'hf') onPick({ source: root.source, kind, title: baseName(node.key), path: node.key, repo: root.repo });
    else if (root.source === 'hub') onPick({ source: 'hub', kind, title: baseName(node.key), path: node.key, projectId: root.projectId });
    else onPick({ source: 'local', kind, title: node.name, path: node.key });
  }

  // Remote "loaded nodes only" filter: matching file nodes across every already-
  // expanded directory of this root (no remote recursive walk — cap discipline).
  function loadedMatches(root: InspectRoot, qLower: string): TreeNode[] {
    const prefix = `${root.id} `;
    const out: TreeNode[] = [];
    const seen = new Set<string>();
    for (const [key, l] of Object.entries(listings)) {
      if (!key.startsWith(prefix)) continue;
      for (const n of l.nodes) if (!n.is_dir && !seen.has(n.key) && nodeMatches(n, qLower)) (seen.add(n.key), out.push(n));
    }
    return out.slice(0, 500);
  }

  return (
    <aside className="inspect-tree" style={{ width: `${width}px` }} aria-label={t('inspect.tree')}>
      <div className="inspect-tree-head">
        <span className="inspect-tree-title small muted">{t('inspect.roots')}</span>
        <span className="spacer" />
        {isShell() && (
          <button className="icon-btn" title={t('inspect.addFolder')} onClick={onAddFolder}>
            <Icon name="plus" size={14} />
          </button>
        )}
        <button className="icon-btn" title={t('nav.collapse')} onClick={onClose}>
          <Icon name="sidebar" size={14} />
        </button>
      </div>
      <div className="inspect-tree-body">
        {roots.map((root) => {
          const q = (filters[root.id] ?? '').trim().toLowerCase();
          const key = rootKey(root);
          const ctx: BranchCtx = {
            ckOf: (k) => ck(root.id, k),
            listings,
            expanded,
            loading,
            errors,
            onToggle: (k) => toggleDir(root, k),
            onOpen: (n) => openFile(root, n),
            onRetry: (k) => loadDir(root, k),
          };
          return (
            <div key={root.id} className="inspect-tree-root">
              <div className="inspect-tree-rootrow">
                <button className="inspect-tree-roottoggle" onClick={() => toggleDir(root, key)} title={root.path ?? root.label}>
                  <Icon name={expanded.has(ck(root.id, key)) ? 'chevron-down' : 'chevron-right'} size={12} />
                  <Icon name={sourceIcon(root.source)} size={13} />
                  {renaming === root.id ? (
                    <input
                      className="inspect-tree-rename"
                      defaultValue={root.label}
                      autoFocus
                      onClick={(e) => e.stopPropagation()}
                      onBlur={(e) => {
                        const v = e.target.value.trim();
                        if (v !== '') renameRoot(root.id, v);
                        setRenaming(null);
                      }}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter') (e.target as HTMLInputElement).blur();
                        else if (e.key === 'Escape') setRenaming(null);
                      }}
                    />
                  ) : (
                    <span className="inspect-tree-name strong">{root.label}</span>
                  )}
                </button>
                <div className="inspect-tree-rootactions">
                  <button className="icon-btn sm" title={t('inspect.rename')} onClick={() => setRenaming(root.id)}>
                    <Icon name="pen" size={12} />
                  </button>
                  <button className="icon-btn sm" title={t('inspect.refresh')} onClick={() => refreshRoot(root)}>
                    <Icon name="refresh" size={12} />
                  </button>
                  <button className="icon-btn sm" title={t('inspect.remove')} onClick={() => removeRoot(root.id)}>
                    <Icon name="close" size={12} />
                  </button>
                </div>
              </div>
              <input
                className="inspect-tree-filter"
                placeholder={t('inspect.filterInTree')}
                value={filters[root.id] ?? ''}
                onChange={(e) => onFilterChange(root, e.target.value)}
              />
              {q !== '' ? (
                <FilterResults
                  root={root}
                  q={q}
                  index={indexes[root.id]}
                  indexBusy={indexBusy.has(root.id)}
                  indexErr={errors[`idx:${root.id}`]}
                  foldedFiles={foldedFiles[root.id]}
                  loadedMatches={loadedMatches}
                  onOpen={(n) => openFile(root, n)}
                />
              ) : (
                expanded.has(ck(root.id, key)) && <Branch nodeKey={key} depth={1} ctx={ctx} />
              )}
            </div>
          );
        })}
        {roots.length === 0 && <div className="inspect-tree-msg muted" style={{ paddingLeft: '8px' }}>{t('inspect.noRoots')}</div>}
      </div>
    </aside>
  );
}

// ── filtered flat results (per-source filter mode) ───────────────────────────
function FilterResults({
  root,
  q,
  index,
  indexBusy,
  indexErr,
  foldedFiles,
  loadedMatches,
  onOpen,
}: {
  root: InspectRoot;
  q: string;
  index: { entries: TreeIndexEntry[]; truncated: boolean } | undefined;
  indexBusy: boolean;
  indexErr: string | undefined;
  foldedFiles: TreeNode[] | undefined;
  loadedMatches: (root: InspectRoot, qLower: string) => TreeNode[];
  onOpen: (node: TreeNode) => void;
}): JSX.Element {
  const t = useT();
  const CAP = 500;
  const rootPath = root.path ?? '';

  // Local: recursive index; remote: loaded nodes only; hub/forge: full flat list.
  const results = useMemo<{ rows: TreeNode[]; capped: boolean; pending: boolean }>(() => {
    if (root.source === 'local') {
      if (index === undefined) return { rows: [], capped: false, pending: true };
      const rows = index.entries
        .filter((e) => !e.is_dir && e.rel.toLowerCase().includes(q))
        .slice(0, CAP)
        .map((e) => ({ name: e.rel.slice(e.rel.lastIndexOf('/') + 1), key: `${rootPath.replace(/[\\/]+$/, '')}/${e.rel}`, is_dir: false }));
      return { rows, capped: index.truncated || rows.length >= CAP, pending: false };
    }
    if (root.source === 'hub' || root.source === 'github' || root.source === 'hf') {
      if (foldedFiles === undefined) return { rows: [], capped: false, pending: true };
      const rows = foldedFiles.filter((n) => nodeMatches(n, q)).slice(0, CAP);
      return { rows, capped: rows.length >= CAP, pending: false };
    }
    const rows = loadedMatches(root, q);
    return { rows, capped: rows.length >= CAP, pending: false };
  }, [root, q, index, foldedFiles, loadedMatches, rootPath]);

  if (root.source === 'local' && indexErr !== undefined)
    return (
      <div className="inspect-tree-msg err" style={{ paddingLeft: '8px' }}>
        <Icon name="alert" size={13} /> {indexErr}
      </div>
    );
  if (results.pending) return <div className="inspect-tree-msg muted" style={{ paddingLeft: '8px' }}>{indexBusy ? t('inspect.indexing') : '…'}</div>;
  if (results.rows.length === 0)
    return (
      <div className="inspect-tree-msg muted" style={{ paddingLeft: '8px' }}>
        {root.source === 'remote' ? t('inspect.noMatchesLoaded') : t('inspect.noMatches')}
      </div>
    );

  // Display the path fragment that helps locate a match: the repo/project-
  // relative path for hub + forge (`key` already is), the root-relative path for
  // local (its `key` is absolute), the basename for remote (only loaded folders).
  const label = (n: TreeNode): string => {
    if (root.source === 'hub' || root.source === 'github' || root.source === 'hf') return n.key;
    if (root.source === 'local') {
      const base = rootPath.replace(/[\\/]+$/, '');
      return n.key.startsWith(base) ? n.key.slice(base.length).replace(/^[\\/]/, '') : n.key;
    }
    return n.name;
  };

  return (
    <>
      {results.rows.map((n) => (
        <button key={n.key} className="inspect-tree-row file" onClick={() => onOpen(n)} title={n.key}>
          <Icon name="file-text" size={13} />
          <span className="inspect-tree-name">{label(n)}</span>
        </button>
      ))}
      {results.capped && <div className="inspect-tree-msg muted" style={{ paddingLeft: '8px' }}>{t('inspect.searchCapped')}</div>}
    </>
  );
}
