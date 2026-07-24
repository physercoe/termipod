/// Source-agnostic tree model for the Inspect (J3) tree pane (round-3 T2). The
/// pane browses three kinds of root — a local folder, a remote SFTP directory,
/// and a hub project's `docs_root` — through one node type. Local and remote
/// list one directory per expand (lazy IPC / SFTP); the hub source hands back a
/// single flat file list, which this module folds into the same node shape so
/// the pane renders all three identically.
///
/// Kept pure (no store / no IPC) so the fold + filter logic is unit-tested.

/// One node of a browsable tree. `key` is the source-specific identifier the
/// pane uses to expand a directory or open a file: an absolute path (local), a
/// remote path (SFTP), or a `docs_root`-relative path (hub).
export interface TreeNode {
  name: string;
  key: string;
  is_dir: boolean;
}

// ── path helpers over '/'-joined (hub) paths ─────────────────────────────────
function baseSeg(p: string): string {
  const i = p.lastIndexOf('/');
  return i >= 0 ? p.slice(i + 1) : p;
}
function parentSeg(p: string): string {
  const i = p.lastIndexOf('/');
  return i >= 0 ? p.slice(0, i) : '';
}

/// Fold a hub project's flat doc list (`[{ path, is_dir }]`, where dir rows may
/// or may not be present) into a parent→children map plus the flat list of file
/// nodes (for the exact name filter). Missing ancestor directories are
/// synthesized, so a `weights/model.safetensors` entry with no explicit
/// `weights` row still nests correctly. Root-level children live under key `''`.
/// Each child array is sorted dirs-first, then case-insensitive by name — the
/// ordering every other listing uses.
export function foldHubDocs(docs: Array<{ path: string; is_dir: boolean }>): { children: Map<string, TreeNode[]>; files: TreeNode[] } {
  const children = new Map<string, TreeNode[]>();
  const seen = new Set<string>(); // node keys already inserted (dedupe)
  const files: TreeNode[] = [];

  const add = (key: string, isDir: boolean): void => {
    const norm = key.replace(/^\/+/, '').replace(/\/+$/, '');
    if (norm === '' || seen.has(norm)) return;
    // Ensure every ancestor directory exists first (so nesting is complete even
    // when the payload omits intermediate dir rows).
    const parent = parentSeg(norm);
    if (parent !== '') add(parent, true);
    seen.add(norm);
    const node: TreeNode = { name: baseSeg(norm), key: norm, is_dir: isDir };
    const bucket = children.get(parent);
    if (bucket === undefined) children.set(parent, [node]);
    else bucket.push(node);
    if (!isDir) files.push(node);
  };

  for (const d of docs) add(d.path, d.is_dir === true);

  for (const bucket of children.values()) {
    bucket.sort((a, b) => Number(b.is_dir) - Number(a.is_dir) || a.name.toLowerCase().localeCompare(b.name.toLowerCase()));
  }
  return { children, files };
}

/// Case-insensitive substring match of a node against a lowercased query, over
/// both its display name and full key (so a path fragment like `src/main`
/// matches). Shared by the remote "loaded nodes" and hub "flat" filters.
export function nodeMatches(node: TreeNode, qLower: string): boolean {
  return node.name.toLowerCase().includes(qLower) || node.key.toLowerCase().includes(qLower);
}
