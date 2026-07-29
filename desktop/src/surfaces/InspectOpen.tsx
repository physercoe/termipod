import { useCallback, useEffect, useMemo, useState } from 'react';
import { useT } from '../i18n';
import { Icon, type IconName } from '../ui/Icon';
import { kindForInspectFile, type ForgeRepo, type InspectKind, type InspectSource } from '../state/inspect';
import { useInspectRoots, type InspectRoot } from '../state/inspectRoots';
import { useWorkspace } from '../state/workspace';
import { listWorkspaceFiles, type WorkspaceFile } from '../state/workspaceFiles';
import { listConnections, type Connection } from '../state/connections';
import { sftpBrowse } from '../state/inspectSources';
import { localList } from '../state/localfs';
import type { Forge } from '../state/forge';
import { useSession } from '../state/session';
import type { Entity } from '../hub/types';
import type { SftpEntry } from '../ssh/native';
import { ForgeFilePicker } from './InspectForgePick';

/// The Inspect (J3) "open from…" picker — the W1 follow-on that opens files from
/// the Author **workspace**, a **remote** host over SFTP, a **hub** project's
/// docs, and (#460) any **pinned root** (local / remote / hub / github / hf —
/// the Compare menu's side-B-from-a-root path). One modal, four modes; each
/// resolves to a `PickResult` the surface turns into an inspector tab (content
/// is read lazily on activate — the picker only chooses metadata, except where
/// it already holds the bytes).

export type OpenMode = 'workspace' | 'remote' | 'hub' | 'roots';

/// A root to pin (round-3 T2) — the same browse dialog that opens a file can pin
/// the folder/project it is browsing as a tree root.
export type PinRoot = Omit<InspectRoot, 'id'>;

export interface PickResult {
  source: InspectSource;
  kind: InspectKind;
  title: string;
  path: string;
  hostId?: string;
  projectId?: string;
  /// The pinned forge snapshot, for a `github`/`hf` pick.
  repo?: ForgeRepo;
  /// A 1-based line to reveal after opening (a content-search hit).
  revealLine?: number;
}

// ── Entity field helpers (hub entities are untyped JSON maps) ────────────────
function str(e: Entity, k: string): string {
  const v = e[k];
  return typeof v === 'string' ? v : '';
}
function bool(e: Entity, k: string): boolean {
  return e[k] === true;
}

function baseName(p: string): string {
  const s = p.replace(/[\\/]+$/, '');
  const i = Math.max(s.lastIndexOf('/'), s.lastIndexOf('\\'));
  return i >= 0 ? s.slice(i + 1) : s;
}
function extOf(p: string): string {
  const b = baseName(p);
  const i = b.lastIndexOf('.');
  return i >= 0 ? b.slice(i + 1) : '';
}

// ── Workspace mode ───────────────────────────────────────────────────────────
function WorkspacePicker({ onPick }: { onPick: (r: PickResult) => void }): JSX.Element {
  const t = useT();
  const folder = useWorkspace((s) => s.folder);
  const [files, setFiles] = useState<WorkspaceFile[] | null>(null);
  const [q, setQ] = useState('');

  useEffect(() => {
    if (folder === null) {
      setFiles([]);
      return;
    }
    let cancelled = false;
    void listWorkspaceFiles(folder).then((f) => !cancelled && setFiles(f));
    return () => {
      cancelled = true;
    };
  }, [folder]);

  const shown = useMemo(() => {
    if (files === null) return [];
    const needle = q.trim().toLowerCase();
    return (needle === '' ? files : files.filter((f) => f.rel.toLowerCase().includes(needle))).slice(0, 500);
  }, [files, q]);

  if (folder === null) return <div className="inspect-modal-empty muted">{t('inspect.noWorkspace')}</div>;

  return (
    <>
      <input className="inspect-modal-search" placeholder={t('inspect.filter')} value={q} onChange={(e) => setQ(e.target.value)} autoFocus />
      <div className="inspect-modal-list">
        {files === null ? (
          <div className="muted region-pad">{t('inspect.loading')}</div>
        ) : shown.length === 0 ? (
          <div className="muted region-pad">{t('inspect.noFiles')}</div>
        ) : (
          shown.map((f) => (
            <button
              key={f.path}
              className="inspect-modal-row"
              onClick={() => onPick({ source: 'workspace', kind: kindForInspectFile(extOf(f.rel), ''), title: baseName(f.rel), path: f.path })}
            >
              <Icon name="file-text" size={14} />
              <span className="inspect-modal-row-name">{f.rel}</span>
            </button>
          ))
        )}
      </div>
    </>
  );
}

// ── Remote (SFTP) mode ───────────────────────────────────────────────────────
function RemotePicker({ onPick, onPinRoot }: { onPick: (r: PickResult) => void; onPinRoot?: (r: PinRoot) => void }): JSX.Element {
  const t = useT();
  const conns = useMemo(() => listConnections(), []);
  const [connId, setConnId] = useState<string | null>(null);
  const [cwd, setCwd] = useState('.');
  const [entries, setEntries] = useState<SftpEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  // The SFTP session is cached per connection (state/inspectSources) so a picked
  // file's tab reuses it for its read + later re-reads — so the picker does NOT
  // close it on unmount (that would race the tab's first read). It lingers until
  // the app closes, which is the intended one-session-per-host behaviour.

  useEffect(() => {
    if (connId === null) return;
    let cancelled = false;
    setEntries(null);
    setError(null);
    void sftpBrowse(connId, cwd)
      .then((e) => !cancelled && setEntries(e))
      .catch((err: unknown) => !cancelled && setError(err instanceof Error ? err.message : String(err)));
    return () => {
      cancelled = true;
    };
  }, [connId, cwd]);

  function child(name: string): string {
    return cwd === '.' ? name : `${cwd.replace(/\/+$/, '')}/${name}`;
  }
  function parent(): string {
    const trimmed = cwd.replace(/\/+$/, '');
    const i = trimmed.lastIndexOf('/');
    return i > 0 ? trimmed.slice(0, i) : '.';
  }

  if (connId === null) {
    return (
      <div className="inspect-modal-list">
        {conns.length === 0 ? (
          <div className="muted region-pad">{t('inspect.noConns')}</div>
        ) : (
          conns.map((c: Connection) => (
            <button key={c.id} className="inspect-modal-row" onClick={() => (setCwd('.'), setConnId(c.id))}>
              <Icon name="terminal" size={14} />
              <span className="inspect-modal-row-name">{c.name}</span>
              <span className="muted small">
                {c.username}@{c.host}
              </span>
            </button>
          ))
        )}
      </div>
    );
  }

  const sorted = (entries ?? []).slice().sort((a, b) => Number(b.is_dir) - Number(a.is_dir) || a.name.localeCompare(b.name));
  return (
    <>
      <div className="inspect-modal-crumbs mono small">
        <button className="link-btn" onClick={() => (setConnId(null), setEntries(null))}>
          {listConnections().find((c) => c.id === connId)?.name ?? t('inspect.remote')}
        </button>
        <span className="muted"> : {cwd}</span>
        {onPinRoot !== undefined && (
          <>
            <span className="spacer" />
            <button
              className="link-btn small"
              onClick={() => onPinRoot({ source: 'remote', hostId: connId, path: cwd, label: `${listConnections().find((c) => c.id === connId)?.name ?? t('inspect.remote')}:${cwd}` })}
            >
              <Icon name="plus" size={12} /> {t('inspect.pinFolderAsRoot')}
            </button>
          </>
        )}
      </div>
      <div className="inspect-modal-list">
        {error !== null ? (
          <div className="inspect-error region-pad">
            <Icon name="alert" size={16} /> {error}
          </div>
        ) : entries === null ? (
          <div className="muted region-pad">{t('inspect.loading')}</div>
        ) : (
          <>
            {cwd !== '.' && (
              <button className="inspect-modal-row" onClick={() => setCwd(parent())}>
                <Icon name="chevron-up" size={14} />
                <span className="inspect-modal-row-name">..</span>
              </button>
            )}
            {sorted.map((e) =>
              e.is_dir ? (
                <button key={e.name} className="inspect-modal-row" onClick={() => setCwd(child(e.name))}>
                  <Icon name="folder" size={14} />
                  <span className="inspect-modal-row-name">{e.name}</span>
                </button>
              ) : (
                <button
                  key={e.name}
                  className="inspect-modal-row"
                  onClick={() =>
                    onPick({ source: 'remote', kind: kindForInspectFile(extOf(e.name), ''), title: e.name, path: child(e.name), hostId: connId })
                  }
                >
                  <Icon name="file-text" size={14} />
                  <span className="inspect-modal-row-name">{e.name}</span>
                </button>
              ),
            )}
          </>
        )}
      </div>
    </>
  );
}

// ── Hub project-doc mode ─────────────────────────────────────────────────────
function HubPicker({ onPick, onPinRoot }: { onPick: (r: PickResult) => void; onPinRoot?: (r: PinRoot) => void }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const [projects, setProjects] = useState<Entity[] | null>(null);
  const [project, setProject] = useState<Entity | null>(null);
  const [docs, setDocs] = useState<Entity[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (client === null) return;
    let cancelled = false;
    void client
      .listProjects()
      .then((p) => !cancelled && setProjects(p))
      .catch((e: unknown) => !cancelled && setError(e instanceof Error ? e.message : String(e)));
    return () => {
      cancelled = true;
    };
  }, [client]);

  useEffect(() => {
    if (client === null || project === null) return;
    const pid = str(project, 'id');
    let cancelled = false;
    setDocs(null);
    setError(null);
    void client
      .listProjectDocs(pid)
      .then((d) => !cancelled && setDocs(d.filter((x) => !bool(x, 'is_dir'))))
      .catch((e: unknown) => !cancelled && setError(e instanceof Error ? e.message : String(e)));
    return () => {
      cancelled = true;
    };
  }, [client, project]);

  if (client === null) return <div className="inspect-modal-empty muted">{t('inspect.noHub')}</div>;
  if (error !== null)
    return (
      <div className="inspect-error region-pad">
        <Icon name="alert" size={16} /> {error}
      </div>
    );

  if (project === null) {
    return (
      <div className="inspect-modal-list">
        {projects === null ? (
          <div className="muted region-pad">{t('inspect.loading')}</div>
        ) : projects.length === 0 ? (
          <div className="muted region-pad">{t('inspect.noProjects')}</div>
        ) : (
          projects.map((p) => (
            <button key={str(p, 'id')} className="inspect-modal-row" onClick={() => setProject(p)}>
              <Icon name="folder" size={14} />
              <span className="inspect-modal-row-name">{str(p, 'name') || str(p, 'id')}</span>
            </button>
          ))
        )}
      </div>
    );
  }

  const pid = str(project, 'id');
  return (
    <>
      <div className="inspect-modal-crumbs small">
        <button className="link-btn" onClick={() => (setProject(null), setDocs(null))}>
          {str(project, 'name') || pid}
        </button>
        {onPinRoot !== undefined && (
          <>
            <span className="spacer" />
            <button className="link-btn small" onClick={() => onPinRoot({ source: 'hub', projectId: pid, label: str(project, 'name') || pid })}>
              <Icon name="plus" size={12} /> {t('inspect.pinProjectAsRoot')}
            </button>
          </>
        )}
      </div>
      <div className="inspect-modal-list">
        {docs === null ? (
          <div className="muted region-pad">{t('inspect.loading')}</div>
        ) : docs.length === 0 ? (
          <div className="muted region-pad">{t('inspect.noDocs')}</div>
        ) : (
          docs.map((d) => {
            const path = str(d, 'path');
            return (
              <button
                key={path}
                className="inspect-modal-row"
                onClick={() => onPick({ source: 'hub', kind: kindForInspectFile(extOf(path), ''), title: baseName(path), path, projectId: pid })}
              >
                <Icon name="file-text" size={14} />
                <span className="inspect-modal-row-name">{path}</span>
              </button>
            );
          })
        )}
      </div>
    </>
  );
}

// ── Pinned-roots mode (#460) ─────────────────────────────────────────────────
// Side B of a compare straight from the user's pinned Inspect roots: pick a
// root, then browse it. local/remote browse lazily per directory (the one-shot
// dialog shape of the tree pane's lazy expand); hub + github/hf arrive in one
// fetch and filter flat — the same data, and the same caps, as the tree pane.

function rootSourceIcon(source: InspectRoot['source']): IconName {
  if (source === 'remote') return 'terminal';
  if (source === 'hub') return 'cloud';
  if (source === 'github') return 'git-branch';
  if (source === 'hf') return 'sliders';
  return 'folder';
}

interface DirEntry {
  name: string;
  path: string;
  is_dir: boolean;
}
interface DirListResult {
  entries: DirEntry[];
  truncated: boolean;
}

async function localDirList(dir: string): Promise<DirListResult> {
  const l = await localList(dir);
  return { entries: l.entries.map((e) => ({ name: e.name, path: e.path, is_dir: e.is_dir })), truncated: l.truncated };
}
async function remoteDirList(hostId: string, dir: string): Promise<DirListResult> {
  const es = await sftpBrowse(hostId, dir);
  return { entries: es.map((e) => ({ name: e.name, path: dir === '.' || dir === '' ? e.name : `${dir.replace(/\/+$/, '')}/${e.name}`, is_dir: e.is_dir })), truncated: false };
}
function parentDirOf(dir: string): string {
  const trimmed = dir.replace(/[\\/]+$/, '');
  const i = Math.max(trimmed.lastIndexOf('/'), trimmed.lastIndexOf('\\'));
  return i > 0 ? trimmed.slice(0, i) : '.';
}

/// A lazy per-directory browser over an injectable listing function — one shape
/// for local roots (`localfs_list`) and remote roots (`sftpBrowse`).
function DirBrowser({
  startDir,
  listDir,
  onPick,
}: {
  startDir: string;
  listDir: (dir: string) => Promise<DirListResult>;
  onPick: (path: string, name: string) => void;
}): JSX.Element {
  const t = useT();
  const [cwd, setCwd] = useState(startDir);
  const [listing, setListing] = useState<DirListResult | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setListing(null);
    setErr(null);
    listDir(cwd)
      .then((r) => !cancelled && setListing(r))
      .catch((e: unknown) => !cancelled && setErr(e instanceof Error ? e.message : String(e)));
    return () => {
      cancelled = true;
    };
  }, [cwd, listDir]);

  const sorted = (listing?.entries ?? []).slice().sort((a, b) => Number(b.is_dir) - Number(a.is_dir) || a.name.localeCompare(b.name));
  const canUp = cwd !== '.' && cwd !== '/' && parentDirOf(cwd) !== cwd;
  return (
    <>
      <div className="inspect-modal-crumbs mono small">
        <span className="muted">{cwd}</span>
      </div>
      <div className="inspect-modal-list">
        {err !== null ? (
          <div className="inspect-error region-pad">
            <Icon name="alert" size={16} /> {err}
          </div>
        ) : listing === null ? (
          <div className="muted region-pad">{t('inspect.loading')}</div>
        ) : (
          <>
            {canUp && (
              <button className="inspect-modal-row" onClick={() => setCwd(parentDirOf(cwd))}>
                <Icon name="chevron-up" size={14} />
                <span className="inspect-modal-row-name">..</span>
              </button>
            )}
            {sorted.map((e) =>
              e.is_dir ? (
                <button key={e.path} className="inspect-modal-row" onClick={() => setCwd(e.path)}>
                  <Icon name="folder" size={14} />
                  <span className="inspect-modal-row-name">{e.name}</span>
                </button>
              ) : (
                <button key={e.path} className="inspect-modal-row" onClick={() => onPick(e.path, e.name)}>
                  <Icon name="file-text" size={14} />
                  <span className="inspect-modal-row-name">{e.name}</span>
                </button>
              ),
            )}
            {listing.truncated && <div className="muted small region-pad">{t('inspect.listingCapped')}</div>}
          </>
        )}
      </div>
    </>
  );
}

/// A hub root's docs: one flat fetch, filtered client-side (the HubPicker's
/// second phase with the project already chosen by the root).
function HubDocsBrowser({ projectId, onPick }: { projectId: string; onPick: (r: PickResult) => void }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const [docs, setDocs] = useState<Entity[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [q, setQ] = useState('');

  useEffect(() => {
    if (client === null) return;
    let cancelled = false;
    void client
      .listProjectDocs(projectId)
      .then((d) => !cancelled && setDocs(d.filter((x) => !bool(x, 'is_dir'))))
      .catch((e: unknown) => !cancelled && setErr(e instanceof Error ? e.message : String(e)));
    return () => {
      cancelled = true;
    };
  }, [client, projectId]);

  const shown = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return (docs ?? [])
      .map((d) => str(d, 'path'))
      .filter((p) => needle === '' || p.toLowerCase().includes(needle))
      .slice(0, 500);
  }, [docs, q]);

  if (client === null) return <div className="inspect-modal-empty muted">{t('inspect.noHub')}</div>;
  return (
    <>
      <input className="inspect-modal-search" placeholder={t('inspect.filter')} value={q} onChange={(e) => setQ(e.target.value)} autoFocus />
      <div className="inspect-modal-list">
        {err !== null ? (
          <div className="inspect-error region-pad">
            <Icon name="alert" size={16} /> {err}
          </div>
        ) : docs === null ? (
          <div className="muted region-pad">{t('inspect.loading')}</div>
        ) : shown.length === 0 ? (
          <div className="muted region-pad">{t('inspect.noMatches')}</div>
        ) : (
          shown.map((p) => (
            <button
              key={p}
              className="inspect-modal-row"
              onClick={() => onPick({ source: 'hub', kind: kindForInspectFile(extOf(p), ''), title: baseName(p), path: p, projectId })}
            >
              <Icon name="file-text" size={14} />
              <span className="inspect-modal-row-name">{p}</span>
            </button>
          ))
        )}
      </div>
    </>
  );
}

function RootsPicker({ onPick }: { onPick: (r: PickResult) => void }): JSX.Element {
  const t = useT();
  const roots = useInspectRoots((s) => s.roots);
  const [root, setRoot] = useState<InspectRoot | null>(null);

  // Stable listing functions for DirBrowser's effect deps (an inline arrow
  // would re-fire the listing on every render).
  const listLocal = useCallback((dir: string) => localDirList(dir), []);
  const listRemote = useCallback((dir: string) => remoteDirList(root?.hostId ?? '', dir), [root?.hostId]);

  if (root !== null) {
    return (
      <>
        <div className="inspect-modal-crumbs small">
          <button className="link-btn" onClick={() => setRoot(null)}>
            {t('inspect.roots')}
          </button>
          <span className="muted"> / {root.label}</span>
        </div>
        {root.source === 'local' && root.path !== undefined ? (
          <DirBrowser startDir={root.path} listDir={listLocal} onPick={(path, name) => onPick({ source: 'local', kind: kindForInspectFile(extOf(name), ''), title: name, path })} />
        ) : root.source === 'remote' ? (
          <DirBrowser
            startDir={root.path ?? '.'}
            listDir={listRemote}
            onPick={(path, name) => onPick({ source: 'remote', kind: kindForInspectFile(extOf(name), ''), title: name, path, hostId: root.hostId })}
          />
        ) : root.source === 'hub' ? (
          <HubDocsBrowser projectId={root.projectId ?? ''} onPick={onPick} />
        ) : root.repo !== undefined ? (
          <ForgeFilePicker forge={root.source as Forge} repo={root.repo} onPick={onPick} />
        ) : (
          <div className="inspect-modal-empty muted">{t('inspect.noRoots')}</div>
        )}
      </>
    );
  }
  return (
    <div className="inspect-modal-list">
      {roots.length === 0 ? (
        <div className="muted region-pad">{t('inspect.noRoots')}</div>
      ) : (
        roots.map((r) => (
          <button key={r.id} className="inspect-modal-row" onClick={() => setRoot(r)}>
            <Icon name={rootSourceIcon(r.source)} size={14} />
            <span className="inspect-modal-row-name">{r.label}</span>
          </button>
        ))
      )}
    </div>
  );
}

export function InspectOpenDialog({
  mode,
  onClose,
  onPick,
  onPinRoot,
}: {
  mode: OpenMode;
  onClose: () => void;
  onPick: (r: PickResult) => void;
  onPinRoot?: (r: PinRoot) => void;
}): JSX.Element {
  const t = useT();
  const title =
    mode === 'workspace'
      ? t('inspect.fromWorkspace')
      : mode === 'remote'
        ? t('inspect.fromRemote')
        : mode === 'hub'
          ? t('inspect.fromHub')
          : t('inspect.fromRoots');
  return (
    <div className="inspect-modal-backdrop" onClick={onClose}>
      <div className="inspect-modal" role="dialog" aria-label={title} onClick={(e) => e.stopPropagation()}>
        <div className="inspect-modal-head">
          <span className="inspect-modal-title">{title}</span>
          <span className="spacer" />
          <button className="icon-btn" title={t('inspect.close')} onClick={onClose}>
            <Icon name="close" size={15} />
          </button>
        </div>
        {mode === 'workspace' ? (
          <WorkspacePicker onPick={onPick} />
        ) : mode === 'remote' ? (
          <RemotePicker onPick={onPick} onPinRoot={onPinRoot} />
        ) : mode === 'hub' ? (
          <HubPicker onPick={onPick} onPinRoot={onPinRoot} />
        ) : (
          <RootsPicker onPick={onPick} />
        )}
      </div>
    </div>
  );
}
