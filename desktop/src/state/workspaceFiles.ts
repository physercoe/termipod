import { bodyToFile, extForDoc, type Doc, type DocKind } from './documents';
import { invoke } from '../bridge';

/// Shared helpers for materializing Author documents into the on-disk workspace
/// folder and for enumerating that folder's files (the `@`-mention source). Tauri
/// is reached through a lazy `invoke` so the plain-browser build never imports the
/// native core at module load (mirrors AuthorSurface's helper).

interface RawNode {
  name: string;
  path: string;
  dir: boolean;
  children: RawNode[];
}

/// Base filename for a new document of each kind (`document.md`, `diagram.drawio`).
export const NEW_BASE: Record<DocKind, string> = {
  markdown: 'document',
  diagram: 'diagram',
  canvas: 'canvas',
  table: 'table',
  figure: 'figure',
};

function sep(dir: string): string {
  return dir.includes('\\') ? '\\' : '/';
}

/// A collision-free path for a new file at the workspace root: walks `base`,
/// `base-1`, … until a name not already present, so a second "New diagram" (or a
/// dropped draft) never overwrites the first.
export async function uniqueWorkspacePath(dir: string, base: string, ext: string): Promise<string> {
  let taken = new Set<string>();
  try {
    const nodes = await invoke<RawNode[]>('workspace_list', { path: dir });
    taken = new Set(nodes.map((n) => n.name.toLowerCase()));
  } catch {
    /* best-effort; the unique walk still avoids clobbering what we can see */
  }
  for (let i = 0; ; i += 1) {
    const name = `${base}${i === 0 ? '' : `-${i}`}.${ext}`;
    if (!taken.has(name.toLowerCase())) return `${dir}${sep(dir)}${name}`;
  }
}

/// Sanitize a document title into a filename stem (drop any extension + unsafe
/// chars), falling back to the kind's default base.
function baseFromTitle(title: string, kind: DocKind): string {
  const stem = title
    .trim()
    .replace(/\.[^.]+$/, '')
    .replace(/[^\w.-]+/g, '-')
    .replace(/^-+|-+$/g, '');
  return stem !== '' ? stem : NEW_BASE[kind];
}

/// Write a document's current body into the workspace folder under a fresh name
/// and return the path. Used to materialize an in-memory draft (drag-to-folder /
/// "Save to workspace"); the caller links it via `markSaved`.
export async function writeDocToWorkspace(dir: string, doc: Pick<Doc, 'kind' | 'title' | 'body' | 'spec'>): Promise<string> {
  const ext = extForDoc(doc);
  const path = await uniqueWorkspacePath(dir, baseFromTitle(doc.title, doc.kind), ext);
  await invoke('doc_write', { path, content: bodyToFile(doc.kind, doc.body, ext, 'Name') });
  return path;
}

export interface WorkspaceFile {
  /// Path relative to the workspace root (the `@`-mention label).
  rel: string;
  /// Absolute path (for reading the file on send).
  path: string;
}

/// Read a workspace file's text by absolute path (for `@`-mention context). The
/// caller clamps size; errors propagate so a bad mention can be skipped.
export async function readWorkspaceFile(path: string): Promise<string> {
  const res = await invoke<{ path: string; content: string }>('doc_read', { path });
  return res.content;
}

/// Flatten the workspace tree to its files (dirs dropped), each with a
/// root-relative label — the candidate list for `@`-mentions. Best-effort: an
/// unreadable folder yields an empty list rather than throwing.
export async function listWorkspaceFiles(dir: string): Promise<WorkspaceFile[]> {
  let nodes: RawNode[] = [];
  try {
    nodes = await invoke<RawNode[]>('workspace_list', { path: dir });
  } catch {
    return [];
  }
  const out: WorkspaceFile[] = [];
  const s = sep(dir);
  const walk = (list: RawNode[], prefix: string): void => {
    for (const n of list) {
      if (n.dir) walk(n.children, `${prefix}${n.name}${s}`);
      else out.push({ rel: `${prefix}${n.name}`, path: n.path });
    }
  };
  walk(nodes, '');
  return out;
}
