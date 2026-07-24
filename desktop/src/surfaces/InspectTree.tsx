import { useEffect, useMemo, useRef, useState } from 'react';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import { kindForInspectFile } from '../state/inspect';
import { useInspectRoots, type InspectRoot } from '../state/inspectRoots';
import { localList, treeIndex, type LocalListing, type TreeIndexEntry } from '../state/localfs';
import type { PickResult } from './InspectOpen';

/// The Inspect (J3) **tree pane** — round-3 T1. A collapsible, resizable left
/// pane listing pinned roots and browsing them lazily: a directory click lists
/// that directory only (`localfs_list`), a file click opens it into a viewer tab
/// through the surface's existing `pick()` path (compare mode included). A
/// per-root name filter builds a bounded recursive index (`tree_index`) on first
/// keystroke.
///
/// T1 ships the `local` source; `remote`/`hub`/`github`/`hf` roots are later
/// wedges. Every listing/index is lazy and capped, and every cap is surfaced
/// (a muted "listing capped" row) rather than implying it read everything.

// Cosmetic mirror of the main-process `SKIP_DIRS` (electron fsutil.ts): heavy /
// generated folders are listed but tagged and never auto-expanded. Drift here
// only changes a badge, never behaviour (the walk's skip is authoritative).
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
// Join a root path with a POSIX-relative index path, keeping the root's own
// separator at the seam (internal '/' is fine for Node fs on every platform).
function joinRel(root: string, rel: string): string {
  const sep = root.includes('\\') && !root.includes('/') ? '\\' : '/';
  return `${root.replace(/[\\/]+$/, '')}${sep}${rel}`;
}

// ── one lazily-listed directory's children ───────────────────────────────────
function DirChildren({
  dir,
  depth,
  listings,
  expanded,
  loading,
  errors,
  onToggleDir,
  onOpenFile,
}: {
  dir: string;
  depth: number;
  listings: Record<string, LocalListing>;
  expanded: Set<string>;
  loading: Set<string>;
  errors: Record<string, string>;
  onToggleDir: (path: string) => void;
  onOpenFile: (path: string, name: string) => void;
}): JSX.Element {
  const t = useT();
  const listing = listings[dir];
  const pad = { paddingLeft: `${8 + depth * 14}px` };
  if (errors[dir] !== undefined) {
    return (
      <div className="inspect-tree-msg err" style={pad}>
        <Icon name="alert" size={13} /> {errors[dir]}
      </div>
    );
  }
  if (listing === undefined) {
    return (
      <div className="inspect-tree-msg muted" style={pad}>
        {loading.has(dir) ? t('inspect.loading') : '…'}
      </div>
    );
  }
  return (
    <>
      {listing.entries.map((e) =>
        e.is_dir ? (
          <div key={e.path}>
            <button className="inspect-tree-row" style={pad} onClick={() => onToggleDir(e.path)} title={e.path}>
              <Icon name={expanded.has(e.path) ? 'chevron-down' : 'chevron-right'} size={12} />
              <Icon name="folder" size={13} />
              <span className="inspect-tree-name">{e.name}</span>
              {HEAVY_DIRS.has(e.name) && <span className="inspect-tree-tag small muted">{t('inspect.heavyDir')}</span>}
            </button>
            {expanded.has(e.path) && (
              <DirChildren
                dir={e.path}
                depth={depth + 1}
                listings={listings}
                expanded={expanded}
                loading={loading}
                errors={errors}
                onToggleDir={onToggleDir}
                onOpenFile={onOpenFile}
              />
            )}
          </div>
        ) : (
          <button key={e.path} className="inspect-tree-row file" style={pad} onClick={() => onOpenFile(e.path, e.name)} title={e.path}>
            <span className="inspect-tree-spacer" />
            <Icon name="file-text" size={13} />
            <span className="inspect-tree-name">{e.name}</span>
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

  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [listings, setListings] = useState<Record<string, LocalListing>>({});
  const [loading, setLoading] = useState<Set<string>>(new Set());
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [filters, setFilters] = useState<Record<string, string>>({});
  const [indexes, setIndexes] = useState<Record<string, { entries: TreeIndexEntry[]; truncated: boolean }>>({});
  const [indexBusy, setIndexBusy] = useState<Set<string>>(new Set());
  const [renaming, setRenaming] = useState<string | null>(null);
  const seenRoots = useRef<Set<string>>(new Set());

  function loadDir(dir: string): void {
    setLoading((s) => new Set(s).add(dir));
    setErrors((e) => {
      const n = { ...e };
      delete n[dir];
      return n;
    });
    void localList(dir)
      .then((l) => setListings((m) => ({ ...m, [dir]: l })))
      .catch((err: unknown) => setErrors((e) => ({ ...e, [dir]: err instanceof Error ? err.message : String(err) })))
      .finally(() =>
        setLoading((s) => {
          const n = new Set(s);
          n.delete(dir);
          return n;
        }),
      );
  }

  function toggleDir(dir: string): void {
    setExpanded((s) => {
      const n = new Set(s);
      if (n.has(dir)) n.delete(dir);
      else {
        n.add(dir);
        if (listings[dir] === undefined && !loading.has(dir)) loadDir(dir);
      }
      return n;
    });
  }

  // Auto-expand a genuinely new local root once (a user's later collapse sticks).
  useEffect(() => {
    for (const r of roots) {
      if (r.source !== 'local' || r.path === undefined || seenRoots.current.has(r.id)) continue;
      seenRoots.current.add(r.id);
      const dir = r.path;
      setExpanded((s) => new Set(s).add(dir));
      if (listings[dir] === undefined) loadDir(dir);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [roots]);

  function refreshRoot(root: InspectRoot): void {
    if (root.path === undefined) return;
    const prefix = root.path;
    const within = (p: string): boolean => p === prefix || p.startsWith(prefix.replace(/[\\/]+$/, '') + '/') || p.startsWith(prefix.replace(/[\\/]+$/, '') + '\\');
    setListings((m) => {
      const n = { ...m };
      for (const k of Object.keys(n)) if (within(k)) delete n[k];
      return n;
    });
    setExpanded((s) => {
      const n = new Set([...s].filter((p) => !within(p) || p === prefix));
      return n;
    });
    setIndexes((m) => {
      const n = { ...m };
      delete n[root.id];
      return n;
    });
    // Re-list the root itself so the pane doesn't blank on refresh.
    if (expanded.has(prefix)) loadDir(prefix);
  }

  function buildIndex(root: InspectRoot): void {
    if (root.path === undefined || indexes[root.id] !== undefined || indexBusy.has(root.id)) return;
    setIndexBusy((s) => new Set(s).add(root.id));
    void treeIndex(root.path)
      .then((idx) => setIndexes((m) => ({ ...m, [root.id]: idx })))
      .catch((err: unknown) => setErrors((e) => ({ ...e, [`idx:${root.id}`]: err instanceof Error ? err.message : String(err) })))
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
    if (q.trim() !== '') buildIndex(root);
  }

  // T1 roots are all `local`; a tree file opens like a workspace file (its
  // absolute path is read lazily on activate). Remote/hub/forge sources arrive
  // with their own arms in later wedges.
  function openFile(absPath: string, name: string): void {
    onPick({ source: 'local', kind: kindForInspectFile(extOf(name), ''), title: name, path: absPath });
  }

  return (
    <aside className="inspect-tree" style={{ width: `${width}px` }} aria-label={t('inspect.tree')}>
      <div className="inspect-tree-head">
        <span className="inspect-tree-title small muted">{t('inspect.roots')}</span>
        <span className="spacer" />
        <button className="icon-btn" title={t('inspect.addFolder')} onClick={onAddFolder}>
          <Icon name="plus" size={14} />
        </button>
        <button className="icon-btn" title={t('nav.collapse')} onClick={onClose}>
          <Icon name="sidebar" size={14} />
        </button>
      </div>
      <div className="inspect-tree-body">
        {roots.map((root) => {
          const q = (filters[root.id] ?? '').trim().toLowerCase();
          const idx = indexes[root.id];
          const idxErr = errors[`idx:${root.id}`];
          return (
            <div key={root.id} className="inspect-tree-root">
              <div className="inspect-tree-rootrow">
                <button className="inspect-tree-roottoggle" onClick={() => root.path !== undefined && toggleDir(root.path)} title={root.path}>
                  <Icon name={root.path !== undefined && expanded.has(root.path) ? 'chevron-down' : 'chevron-right'} size={12} />
                  <Icon name="folder" size={13} />
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
                <FilterResults q={q} idx={idx} busy={indexBusy.has(root.id)} err={idxErr} rootPath={root.path ?? ''} onOpenFile={openFile} />
              ) : (
                root.path !== undefined &&
                expanded.has(root.path) && (
                  <DirChildren
                    dir={root.path}
                    depth={1}
                    listings={listings}
                    expanded={expanded}
                    loading={loading}
                    errors={errors}
                    onToggleDir={toggleDir}
                    onOpenFile={openFile}
                  />
                )
              )}
            </div>
          );
        })}
      </div>
    </aside>
  );
}

// ── filtered flat results (name index) ───────────────────────────────────────
function FilterResults({
  q,
  idx,
  busy,
  err,
  rootPath,
  onOpenFile,
}: {
  q: string;
  idx: { entries: TreeIndexEntry[]; truncated: boolean } | undefined;
  busy: boolean;
  err: string | undefined;
  rootPath: string;
  onOpenFile: (absPath: string, name: string) => void;
}): JSX.Element {
  const t = useT();
  const CAP = 500;
  const matches = useMemo(() => {
    if (idx === undefined) return [];
    return idx.entries.filter((e) => e.rel.toLowerCase().includes(q)).slice(0, CAP);
  }, [idx, q]);

  if (err !== undefined)
    return (
      <div className="inspect-tree-msg err" style={{ paddingLeft: '8px' }}>
        <Icon name="alert" size={13} /> {err}
      </div>
    );
  if (idx === undefined) return <div className="inspect-tree-msg muted" style={{ paddingLeft: '8px' }}>{busy ? t('inspect.indexing') : '…'}</div>;
  if (matches.length === 0) return <div className="inspect-tree-msg muted" style={{ paddingLeft: '8px' }}>{t('inspect.noMatches')}</div>;

  return (
    <>
      {matches.map((e) =>
        e.is_dir ? (
          <div key={e.rel} className="inspect-tree-row dir-match" title={e.rel}>
            <Icon name="folder" size={13} />
            <span className="inspect-tree-name muted">{e.rel}</span>
          </div>
        ) : (
          <button key={e.rel} className="inspect-tree-row file" onClick={() => onOpenFile(joinRel(rootPath, e.rel), baseName(e.rel))} title={e.rel}>
            <Icon name="file-text" size={13} />
            <span className="inspect-tree-name">{e.rel}</span>
          </button>
        ),
      )}
      {(idx.truncated || matches.length >= CAP) && <div className="inspect-tree-msg muted" style={{ paddingLeft: '8px' }}>{t('inspect.searchCapped')}</div>}
    </>
  );
}
