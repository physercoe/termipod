import { useCallback, useEffect, useState } from 'react';
import { invoke } from '@tauri-apps/api/core';
import { useT } from '../i18n';
import { docKindIcon, Icon } from '../ui/Icon';
import { isTauri } from '../platform';
import { fileToBody, kindForFile, useDocuments } from '../state/documents';
import { useWorkspace } from '../state/workspace';
import { WorkspaceSyncModal } from './WorkspaceSyncModal';

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
  const folder = useWorkspace((s) => s.folder);
  const setFolder = useWorkspace((s) => s.setFolder);
  const [nodes, setNodes] = useState<FileNode[]>([]);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [showSync, setShowSync] = useState(false);
  const tauri = isTauri();

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

  useEffect(() => {
    void refresh(folder);
  }, [folder, refresh]);

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
        {docs.map((d) => (
          <button
            key={d.id}
            className={`author-nav-doc${activeId === d.id ? ' active' : ''}`}
            title={d.filePath ?? d.title}
            onClick={() => setActive(d.id)}
          >
            <Icon name={docKindIcon(d.kind)} size={14} className="author-nav-kind" />
            <span className="author-nav-name">
              {d.dirty === true ? '● ' : ''}
              {d.title !== '' ? d.title : t('author.untitled')}
            </span>
          </button>
        ))}
      </div>

      <div className="author-nav-sec grow">
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
