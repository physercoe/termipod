import { useEffect, useMemo, useState } from 'react';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import { kindForInspectFile, type InspectKind, type InspectSource } from '../state/inspect';
import type { InspectRoot } from '../state/inspectRoots';
import { useWorkspace } from '../state/workspace';
import { listWorkspaceFiles, type WorkspaceFile } from '../state/workspaceFiles';
import { listConnections, type Connection } from '../state/connections';
import { sftpBrowse } from '../state/inspectSources';
import { useSession } from '../state/session';
import type { Entity } from '../hub/types';
import type { SftpEntry } from '../ssh/native';

/// The Inspect (J3) "open from…" picker — the W1 follow-on that opens files from
/// the Author **workspace**, a **remote** host over SFTP, and a **hub** project's
/// docs. One modal, three modes; each resolves to a `PickResult` the surface
/// turns into an inspector tab (content is read lazily on activate — the picker
/// only chooses metadata, except where it already holds the bytes).

export type OpenMode = 'workspace' | 'remote' | 'hub';

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
  const title = mode === 'workspace' ? t('inspect.fromWorkspace') : mode === 'remote' ? t('inspect.fromRemote') : t('inspect.fromHub');
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
        ) : (
          <HubPicker onPick={onPick} onPinRoot={onPinRoot} />
        )}
      </div>
    </div>
  );
}
