import { useCallback, useEffect, useState } from 'react';
import { invoke } from '@tauri-apps/api/core';
import { useT } from '../i18n';
import { docKindIcon, Icon } from '../ui/Icon';
import { isTauri } from '../platform';
import { fileToBody, kindForFile, useDocuments } from '../state/documents';
import { useWorkspace } from '../state/workspace';
import { writeDocToWorkspace } from '../state/workspaceFiles';
import { WorkspaceSyncModal } from './WorkspaceSyncModal';

const DRAG_TYPE = 'application/x-termipod-doc';

/// The Author (J2) left nav: a file/workspace tree. Two sections — the currently
/// **open documents** (click to focus) and an on-disk **workspace folder** the
/// director opens to browse and open files as documents. The folder is listed by
/// the Rust `workspace_list` command (read-only, depth/entry-capped); clicking a
/// text file reads it (`doc_read`) and opens it as a document — or focuses it if
/// already open.

interface FileNode {
  name: string;
  path: string;
  dir: boolean;
  children: FileNode[];
}

// Files we can meaningfully open in a text editor. Others stay visible but inert
// (open them in the Read surface instead).
const TEXT_EXT = new Set([
  'md', 'markdown', 'txt', 'xml', 'svg', 'drawio', 'canvas', 'json', 'csv', 'tsv', 'log',
  'yml', 'yaml', 'toml', 'ini', 'html', 'htm', 'css', 'js', 'ts', 'tsx', 'jsx',
  'py', 'go', 'rs', 'sh', 'c', 'h', 'cpp', 'hpp', 'java', 'rb', 'php', 'sql',
]);

function baseName(path: string): string {
  const parts = path.split(/[\\/]/);
  return parts[parts.length - 1] || path;
}

function extOf(path: string): string {
  return path.split('.').pop()?.toLowerCase() ?? '';
}

export function AuthorNav(): JSX.Element {
  const t = useT();
  const docs = useDocuments((s) => s.docs);
  const activeId = useDocuments((s) => s.activeId);
  const setActive = useDocuments((s) => s.setActive);
  const create = useDocuments((s) => s.create);
  const remove = useDocuments((s) => s.remove);
  const update = useDocuments((s) => s.update);
  const markSaved = useDocuments((s) => s.markSaved);
  const folder = useWorkspace((s) => s.folder);
  const setFolder = useWorkspace((s) => s.setFolder);
  const touch = useWorkspace((s) => s.touch);
  const rev = useWorkspace((s) => s.rev);
  const [nodes, setNodes] = useState<FileNode[]>([]);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [showSync, setShowSync] = useState(false);
  const [menu, setMenu] = useState<{ id: string; x: number; y: number } | null>(null);
  const [renaming, setRenaming] = useState<{ id: string; value: string } | null>(null);
  const [dropActive, setDropActive] = useState(false);
  const tauri = isTauri();

  // Materialize an in-memory draft into the workspace folder (drag-to-folder or
  // the "Save to workspace" menu item), then link it so Save round-trips to disk.
  async function saveDraftToWorkspace(id: string): Promise<void> {
    if (folder === null) return;
    const d = docs.find((x) => x.id === id);
    if (d === undefined || d.filePath !== undefined) return;
    try {
      const path = await writeDocToWorkspace(folder, d);
      markSaved(id, path, baseName(path));
      touch();
    } catch {
      /* permissions / race — leave the draft in memory */
    }
  }

  const refresh = useCallback(
    async (f: string | null): Promise<void> => {
      if (f === null || !tauri) {
        setNodes([]);
        return;
      }
      setLoading(true);
      setErr(null);
      try {
        setNodes(await invoke<FileNode[]>('workspace_list', { path: f }));
      } catch (e) {
        setErr(e instanceof Error ? e.message : String(e));
        setNodes([]);
      } finally {
        setLoading(false);
      }
    },
    [tauri],
  );

  // Re-list on folder change and whenever the folder is `touch`ed (a new document
  // was materialized into it from the toolbar), so new files appear immediately.
  useEffect(() => {
    void refresh(folder);
  }, [folder, rev, refresh]);

  async function pick(): Promise<void> {
    if (!tauri) return;
    try {
      const f = await invoke<string | null>('workspace_pick_folder');
      if (f !== null) setFolder(f);
    } catch {
      /* cancelled / ignore */
    }
  }

  async function openFile(path: string): Promise<void> {
    const existing = docs.find((d) => d.filePath === path);
    if (existing !== undefined) {
      setActive(existing.id);
      return;
    }
    if (!TEXT_EXT.has(extOf(path))) return; // binary/unsupported — inert
    try {
      const res = await invoke<{ path: string; content: string }>('doc_read', { path });
      const ext = extOf(path); // .canvas→canvas · .csv→table · .drawio→diagram · .json→sniff
      const kind = kindForFile(ext, res.content);
      create(kind, { title: baseName(path), body: fileToBody(kind, res.content, ext, t('table.colName')), filePath: path });
    } catch {
      /* unreadable/binary — ignore */
    }
  }

  return (
    <div className="author-nav">
      <div className="author-nav-sec">
        <div className="author-nav-head">{t('author.navOpen')}</div>
        {docs.length === 0 && <div className="muted small author-nav-empty">{t('author.navNoOpen')}</div>}
        {docs.map((d) => {
          const draft = d.filePath === undefined;
          if (renaming !== null && renaming.id === d.id) {
            return (
              <input
                key={d.id}
                className="author-nav-rename"
                autoFocus
                value={renaming.value}
                onChange={(e) => setRenaming({ id: d.id, value: e.target.value })}
                onBlur={() => {
                  if (renaming.value.trim() !== '') update(d.id, { title: renaming.value.trim() });
                  setRenaming(null);
                }}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') e.currentTarget.blur();
                  if (e.key === 'Escape') setRenaming(null);
                }}
              />
            );
          }
          return (
            <button
              key={d.id}
              className={`author-nav-doc${activeId === d.id ? ' active' : ''}${draft ? ' draft' : ''}`}
              title={draft ? t('author.navDraftHint') : d.filePath}
              draggable={draft}
              onDragStart={(e) => {
                e.dataTransfer.setData(DRAG_TYPE, d.id);
                e.dataTransfer.effectAllowed = 'copy';
              }}
              onClick={() => setActive(d.id)}
              onContextMenu={(e) => {
                e.preventDefault();
                setMenu({ id: d.id, x: e.clientX, y: e.clientY });
              }}
            >
              <Icon name={docKindIcon(d.kind)} size={14} className="author-nav-kind" />
              <span className="author-nav-name">
                {d.dirty === true ? '● ' : ''}
                {d.title !== '' ? d.title : t('author.untitled')}
              </span>
              {draft && <span className="author-nav-badge muted" title={t('author.navDraftHint')}>{t('author.navDraft')}</span>}
            </button>
          );
        })}
      </div>

      <div
        className={`author-nav-sec grow${dropActive ? ' drop-active' : ''}`}
        onDragOver={(e) => {
          // Only a draft drag (our type) is a valid drop; folder must be open.
          if (folder !== null && Array.from(e.dataTransfer.types).includes(DRAG_TYPE)) {
            e.preventDefault();
            e.dataTransfer.dropEffect = 'copy';
            setDropActive(true);
          }
        }}
        onDragLeave={(e) => {
          // Ignore leaves into descendant elements (relatedTarget still inside).
          if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDropActive(false);
        }}
        onDrop={(e) => {
          const id = e.dataTransfer.getData(DRAG_TYPE);
          setDropActive(false);
          if (id !== '') {
            e.preventDefault();
            void saveDraftToWorkspace(id);
          }
        }}
      >
        <div className="author-nav-head">
          {t('author.navFiles')}
          <span className="spacer" />
          {folder !== null && (
            <button className="author-nav-icon" title={t('author.navRefresh')} onClick={() => void refresh(folder)}>
              <Icon name="refresh" size={15} />
            </button>
          )}
          {tauri && folder !== null && (
            <button className="author-nav-icon" title={t('author.navSync')} onClick={() => setShowSync(true)}>
              <Icon name="cloud" size={15} />
            </button>
          )}
          {tauri && (
            <button className="author-nav-icon" title={t('author.navOpenFolder')} onClick={() => void pick()}>
              <Icon name="folder" size={15} />
            </button>
          )}
          {folder !== null && (
            <button className="author-nav-icon" title={t('author.navCloseFolder')} onClick={() => setFolder(null)}>
              <Icon name="close" size={15} />
            </button>
          )}
        </div>
        {!tauri && <div className="muted small author-nav-empty">{t('author.navDesktopOnly')}</div>}
        {tauri && folder === null && <div className="muted small author-nav-empty">{t('author.navPickHint')}</div>}
        {folder !== null && (
          <div className="author-nav-root mono small" title={folder}>
            {baseName(folder)}
          </div>
        )}
        {loading && <div className="muted small author-nav-empty">{t('author.navLoading')}</div>}
        {err !== null && <div className="error small author-nav-empty">{err}</div>}
        {nodes.map((n) => (
          <TreeNode key={n.path} node={n} depth={0} onOpen={openFile} />
        ))}
      </div>

      {showSync && (
        <WorkspaceSyncModal
          root={folder}
          onClose={() => setShowSync(false)}
          onSynced={() => void refresh(folder)}
        />
      )}

      {menu !== null &&
        (() => {
          const d = docs.find((x) => x.id === menu.id);
          if (d === undefined) return null;
          const draft = d.filePath === undefined;
          return (
            <>
              <div className="context-backdrop" onMouseDown={() => setMenu(null)} onContextMenu={(e) => e.preventDefault()} />
              <div className="context-menu" style={{ left: menu.x, top: menu.y }} onMouseDown={(e) => e.stopPropagation()}>
                {draft && folder !== null && (
                  <button
                    onClick={() => {
                      void saveDraftToWorkspace(menu.id);
                      setMenu(null);
                    }}
                  >
                    {t('author.navSaveToWorkspace')}
                  </button>
                )}
                <button
                  onClick={() => {
                    setRenaming({ id: menu.id, value: d.title });
                    setMenu(null);
                  }}
                >
                  {t('author.navRename')}
                </button>
                <button
                  onClick={() => {
                    remove(menu.id);
                    setMenu(null);
                  }}
                >
                  {t('author.navClose')}
                </button>
              </div>
            </>
          );
        })()}
    </div>
  );
}

function TreeNode({
  node,
  depth,
  onOpen,
}: {
  node: FileNode;
  depth: number;
  onOpen: (path: string) => void;
}): JSX.Element {
  const [open, setOpen] = useState(depth < 1); // top-level dirs expanded by default
  const pad = { paddingLeft: 6 + depth * 12 };
  if (node.dir) {
    return (
      <div>
        <button className="author-nav-item dir" style={pad} onClick={() => setOpen((o) => !o)}>
          <Icon name={open ? 'chevron-down' : 'chevron-right'} size={13} className="author-nav-tw" />
          {node.name}
        </button>
        {open && node.children.map((c) => <TreeNode key={c.path} node={c} depth={depth + 1} onOpen={onOpen} />)}
      </div>
    );
  }
  const openable = TEXT_EXT.has(extOf(node.name));
  return (
    <button
      className={`author-nav-item file${openable ? '' : ' inert'}`}
      style={pad}
      title={node.path}
      onClick={() => onOpen(node.path)}
    >
      <span className="author-nav-tw" />
      {node.name}
    </button>
  );
}
