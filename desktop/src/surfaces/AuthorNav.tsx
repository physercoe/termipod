import { useCallback, useEffect, useState, type MouseEvent as ReactMouseEvent } from 'react';
import { invoke } from '../bridge';
import { useT } from '../i18n';
import { Icon, type IconName } from '../ui/Icon';
import { isShell, revealPath } from '../platform';
import { fileToBody, kindForFile, useDocuments } from '../state/documents';
import { useWorkspace } from '../state/workspace';
import { writeDocToWorkspace } from '../state/workspaceFiles';
import { useSyncJob } from '../state/syncJob';
import { useTextPrompt } from '../ui/PromptModal';
import { WorkspaceSyncModal } from './WorkspaceSyncModal';

const DRAG_TYPE = 'application/x-termipod-doc';

/// The Author (J2) left nav: the on-disk **workspace folder** the director opens
/// to browse and edit its files. The open-documents list lives on the tab strip
/// now (W1 shell cleanup — the two were redundant); this pane is workspace-only,
/// and each file row that is currently open echoes its tab's kind icon + dirty ●
/// + active highlight so the tree is the one place that shows "what's open where".
/// The folder is listed by the Rust `workspace_list` command (read-only,
/// depth/entry-capped); clicking a text file reads it (`doc_read`) and opens it
/// as a document — or focuses it if already open. A draft tab dragged onto the
/// tree materializes it here.

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
  'mmd', 'dot', 'gv', 'nomnoml', // figure sources (mermaid / graphviz / nomnoml)
  'excalidraw', // sketch scenes (figure-plan Phase C)
]);

// A file row's kind icon, by extension — a pure lookup (unlike `kindForFile`,
// which content-sniffs `.json`). `.json` and anything unmapped-but-openable fall
// back to the generic document glyph; directories render their own chevron.
const EXT_ICON: Record<string, IconName> = {
  md: 'note', markdown: 'note', txt: 'file-text', log: 'file-text',
  drawio: 'diagram',
  canvas: 'canvas',
  csv: 'table', tsv: 'table',
  excalidraw: 'sketch',
  svg: 'image',
  mmd: 'figure', dot: 'figure', gv: 'figure', nomnoml: 'figure',
  js: 'code', ts: 'code', tsx: 'code', jsx: 'code', py: 'code', go: 'code', rs: 'code',
  sh: 'code', c: 'code', h: 'code', cpp: 'code', hpp: 'code', java: 'code', rb: 'code',
  php: 'code', sql: 'code', html: 'code', htm: 'code', css: 'code', xml: 'code',
  yml: 'code', yaml: 'code', toml: 'code', ini: 'code', json: 'file-text',
};
function iconForFile(name: string): IconName {
  return EXT_ICON[extOf(name)] ?? 'file-text';
}

// Which open documents (by their linked file path) are dirty / the active tab —
// so the workspace tree can echo the tab strip's markers on the file's row.
interface OpenMark {
  dirty: boolean;
  active: boolean;
}

function baseName(path: string): string {
  const parts = path.split(/[\\/]/);
  return parts[parts.length - 1] || path;
}

// The containing directory of a path (native separator preserved).
function parentDir(path: string): string {
  const i = Math.max(path.lastIndexOf('/'), path.lastIndexOf('\\'));
  return i > 0 ? path.slice(0, i) : path;
}

function extOf(path: string): string {
  return path.split('.').pop()?.toLowerCase() ?? '';
}

// A right-click target in the on-disk file tree. `root` = the blank area of the
// workspace section (the folder root itself) — a reduced menu (create only).
interface FileMenu {
  path: string;
  dir: boolean;
  root?: boolean;
  x: number;
  y: number;
}

export function AuthorNav({ onFold }: { onFold?: () => void }): JSX.Element {
  const t = useT();
  const { ask, node: promptNode } = useTextPrompt();
  const docs = useDocuments((s) => s.docs);
  const activeId = useDocuments((s) => s.activeId);
  const setActive = useDocuments((s) => s.setActive);
  const create = useDocuments((s) => s.create);
  const update = useDocuments((s) => s.update);
  const markSaved = useDocuments((s) => s.markSaved);
  const folder = useWorkspace((s) => s.folder);
  const setFolder = useWorkspace((s) => s.setFolder);
  const touch = useWorkspace((s) => s.touch);
  const rev = useWorkspace((s) => s.rev);
  const syncRunning = useSyncJob((s) => s.running);
  const [nodes, setNodes] = useState<FileNode[]>([]);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [showSync, setShowSync] = useState(false);
  const [dropActive, setDropActive] = useState(false);
  // On-disk file-tree right-click menu + its two-step delete confirm.
  const [fileMenu, setFileMenu] = useState<FileMenu | null>(null);
  const [fileConfirmDelete, setFileConfirmDelete] = useState(false);
  const tauri = isShell();

  // Path → open-marker for every open, file-linked document, so a workspace row
  // whose path matches renders emphasized (dirty ● / active highlight) like its
  // tab. Drafts (no filePath) aren't in the tree, so they don't appear here.
  const openByPath = new Map<string, OpenMark>();
  for (const d of docs) {
    if (d.filePath !== undefined) openByPath.set(d.filePath, { dirty: d.dirty === true, active: activeId === d.id });
  }

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

  // Watch for EXTERNAL changes (a file added/edited outside the app never
  // appeared until a manual refresh, #322). src-tauri exposes no fs-watch, so
  // poll the same depth/entry-capped `workspace_list` on a slow interval and
  // swap the tree only when it actually changed — cheap, quiet (no loading
  // spinner), and expanded dirs keep their open state since rows key by path.
  useEffect(() => {
    if (folder === null || !tauri) return;
    const id = setInterval(() => {
      if (document.visibilityState === 'hidden') return;
      void (async () => {
        try {
          const next = await invoke<FileNode[]>('workspace_list', { path: folder });
          setNodes((cur) => (JSON.stringify(cur) === JSON.stringify(next) ? cur : next));
        } catch {
          /* folder unplugged / transient error — keep showing the last tree */
        }
      })();
    }, 4000);
    return () => clearInterval(id);
  }, [folder, tauri]);

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
      const ext = extOf(path); // .canvas→canvas · .csv→table · .drawio→diagram · .mmd/.dot/.json→figure/sniff
      const { kind, spec } = kindForFile(ext, res.content);
      create(kind, {
        title: baseName(path),
        body: fileToBody(kind, res.content, ext, t('table.colName')),
        filePath: path,
        spec,
      });
    } catch {
      /* unreadable/binary — ignore */
    }
  }

  // Dismiss the file-tree context menu on an outside click, scroll, or Escape.
  useEffect(() => {
    if (fileMenu === null) return;
    const close = (): void => {
      setFileMenu(null);
      setFileConfirmDelete(false);
    };
    const onKey = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') close();
    };
    window.addEventListener('click', close);
    window.addEventListener('scroll', close, true);
    window.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('click', close);
      window.removeEventListener('scroll', close, true);
      window.removeEventListener('keydown', onKey);
    };
  }, [fileMenu]);

  // --- File-tree operations (Rust workspacefs commands). All refresh the tree
  // (touch → re-list) and surface any error inline. Open documents whose file was
  // renamed/moved are re-pointed so Save keeps round-tripping to the right path. ---
  function opFailed(e: unknown): void {
    setErr(t('author.fOpFailed').replace('{err}', e instanceof Error ? e.message : String(e)));
  }
  function repointDoc(oldPath: string, newPath: string): void {
    const d = docs.find((x) => x.filePath === oldPath);
    if (d !== undefined) update(d.id, { filePath: newPath, title: baseName(newPath) });
  }
  async function newInDir(dir: boolean, path: string, folderKind: boolean): Promise<void> {
    setFileMenu(null);
    const base = dir ? path : parentDir(path);
    const name = await ask(folderKind ? t('author.fNewFolderPrompt') : t('author.fNewFilePrompt'));
    if (name === null || name.trim() === '') return;
    try {
      const created = await invoke<string>(folderKind ? 'workspace_new_folder' : 'workspace_new_file', {
        dir: base,
        name: name.trim(),
      });
      touch();
      if (!folderKind) void openFile(created);
    } catch (e) {
      opFailed(e);
    }
  }
  async function renameEntry(path: string): Promise<void> {
    setFileMenu(null);
    const name = await ask(t('author.fRenamePrompt'), baseName(path));
    if (name === null || name.trim() === '' || name.trim() === baseName(path)) return;
    try {
      const next = await invoke<string>('workspace_rename', { path, name: name.trim() });
      repointDoc(path, next);
      touch();
    } catch (e) {
      opFailed(e);
    }
  }
  async function moveOrCopy(path: string, copy: boolean): Promise<void> {
    setFileMenu(null);
    try {
      const destDir = await invoke<string | null>('workspace_pick_folder');
      if (destDir === null) return;
      const next = await invoke<string>(copy ? 'workspace_copy' : 'workspace_move', { src: path, destDir });
      if (!copy) repointDoc(path, next);
      touch();
    } catch (e) {
      opFailed(e);
    }
  }
  async function deleteEntry(path: string): Promise<void> {
    setFileMenu(null);
    setFileConfirmDelete(false);
    try {
      await invoke('workspace_delete', { path });
      touch();
    } catch (e) {
      opFailed(e);
    }
  }
  function copyPath(path: string): void {
    setFileMenu(null);
    try {
      void navigator.clipboard?.writeText(path);
    } catch {
      /* clipboard may be unavailable */
    }
  }

  return (
    <div className="author-nav">
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
        onContextMenu={(e) => {
          // Blank-space menu (new file/folder in the workspace root). Skip when the
          // right-click landed on a tree row or a header control — those have their
          // own handling (tree rows stopPropagation; headers should do nothing).
          if (folder === null || !tauri) return;
          if ((e.target as HTMLElement).closest('button, input, .author-nav-item') !== null) return;
          e.preventDefault();
          setFileConfirmDelete(false);
          setFileMenu({ path: folder, dir: true, root: true, x: e.clientX, y: e.clientY });
        }}
      >
        <div className="author-nav-head">
          {onFold !== undefined && (
            <button className="author-nav-icon" title={t('author.navFold')} onClick={onFold}>
              <Icon name="chevron-left" size={15} />
            </button>
          )}
          {t('author.navFiles')}
          <span className="spacer" />
          {folder !== null && (
            <button className="author-nav-icon" title={t('author.navRefresh')} onClick={() => void refresh(folder)}>
              <Icon name="refresh" size={15} />
            </button>
          )}
          {tauri && folder !== null && (
            <button
              className={`author-nav-icon${syncRunning ? ' syncing' : ''}`}
              title={syncRunning ? t('author.syncInProgress') : t('author.navSync')}
              onClick={() => setShowSync(true)}
            >
              <Icon name={syncRunning ? 'refresh' : 'cloud'} size={15} />
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
          <TreeNode
            key={n.path}
            node={n}
            depth={0}
            openByPath={openByPath}
            onOpen={openFile}
            onContext={(node, e) => {
              e.preventDefault();
              e.stopPropagation(); // don't also trigger the section's blank-space menu
              setFileConfirmDelete(false);
              setFileMenu({ path: node.path, dir: node.dir, x: e.clientX, y: e.clientY });
            }}
          />
        ))}
      </div>

      {showSync && <WorkspaceSyncModal root={folder} onClose={() => setShowSync(false)} />}

      {fileMenu !== null && (
        <>
          <div
            className="context-backdrop"
            onMouseDown={() => {
              setFileMenu(null);
              setFileConfirmDelete(false);
            }}
            onContextMenu={(e) => e.preventDefault()}
          />
          <div
            className="context-menu"
            style={{ left: fileMenu.x, top: fileMenu.y }}
            onMouseDown={(e) => e.stopPropagation()}
          >
            <button onClick={() => void newInDir(fileMenu.dir, fileMenu.path, false)}>{t('author.fNewFile')}</button>
            <button onClick={() => void newInDir(fileMenu.dir, fileMenu.path, true)}>{t('author.fNewFolder')}</button>
            <div className="context-menu-sep" />
            <button onClick={() => copyPath(fileMenu.path)}>{t('author.fCopyPath')}</button>
            {tauri && <button onClick={() => { revealPath(fileMenu.path); setFileMenu(null); }}>{t('author.fReveal')}</button>}
            {/* The whole-workspace (blank-space) menu stops at "create + reveal" —
                rename/move/copy/delete of the workspace root itself would be a foot-gun. */}
            {fileMenu.root !== true && (
              <>
                <div className="context-menu-sep" />
                <button onClick={() => void renameEntry(fileMenu.path)}>{t('author.fRename')}</button>
                <button onClick={() => void moveOrCopy(fileMenu.path, false)}>{t('author.fMove')}</button>
                <button onClick={() => void moveOrCopy(fileMenu.path, true)}>{t('author.fCopy')}</button>
                <div className="context-menu-sep" />
                {fileConfirmDelete ? (
                  <button className="danger" onClick={() => void deleteEntry(fileMenu.path)}>
                    {t('author.fDeleteConfirm')}
                  </button>
                ) : (
                  <button
                    className="danger"
                    onClick={(e) => {
                      e.stopPropagation();
                      setFileConfirmDelete(true);
                    }}
                  >
                    {t('author.fDelete')}
                  </button>
                )}
              </>
            )}
          </div>
        </>
      )}
      {promptNode}
    </div>
  );
}

function TreeNode({
  node,
  depth,
  openByPath,
  onOpen,
  onContext,
}: {
  node: FileNode;
  depth: number;
  openByPath: Map<string, OpenMark>;
  onOpen: (path: string) => void;
  onContext: (node: FileNode, e: ReactMouseEvent) => void;
}): JSX.Element {
  const [open, setOpen] = useState(depth < 1); // top-level dirs expanded by default
  const pad = { paddingLeft: 6 + depth * 12 };
  if (node.dir) {
    return (
      <div>
        <button
          className="author-nav-item dir"
          style={pad}
          onClick={() => setOpen((o) => !o)}
          onContextMenu={(e) => onContext(node, e)}
        >
          <Icon name={open ? 'chevron-down' : 'chevron-right'} size={13} className="author-nav-tw" />
          {node.name}
        </button>
        {open &&
          node.children.map((c) => (
            <TreeNode key={c.path} node={c} depth={depth + 1} openByPath={openByPath} onOpen={onOpen} onContext={onContext} />
          ))}
      </div>
    );
  }
  const openable = TEXT_EXT.has(extOf(node.name));
  const mark = openByPath.get(node.path);
  const cls =
    `author-nav-item file${openable ? '' : ' inert'}` +
    (mark !== undefined ? ' open' : '') +
    (mark?.active === true ? ' active' : '');
  return (
    <button
      className={cls}
      style={pad}
      title={node.path}
      onClick={() => onOpen(node.path)}
      onContextMenu={(e) => onContext(node, e)}
    >
      <Icon name={iconForFile(node.name)} size={13} className="author-nav-kind" />
      <span className="author-nav-name">
        {mark?.dirty === true ? '● ' : ''}
        {node.name}
      </span>
    </button>
  );
}
