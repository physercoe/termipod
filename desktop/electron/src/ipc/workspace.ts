/// Author file tree: pick + recursive list (ADR-055 M1.4, port of
/// `workspace.rs`) and the mutations create/rename/delete/move/copy (port of
/// `workspacefs.rs`). Names for create/rename are validated to a single path
/// segment; move/copy refuse to descend a folder into itself.
import { readdir, rename, rm, stat, writeFile, mkdir } from 'node:fs/promises';
import path from 'node:path';
import type { Ctx, Handler } from './dispatch';
import { openDialog } from './dialogs';
import { copyRecursive, isWithin, sortDirsFirst } from './fsutil';

interface FileNode {
  name: string;
  path: string;
  dir: boolean;
  children: FileNode[];
}

const SKIP_DIRS = new Set([
  'node_modules', '.git', 'target', 'dist', 'build', '.next', '.venv', 'venv',
  '__pycache__', '.cache', '.idea', '.vscode', '.svn', '.hg',
]);
const MAX_DEPTH = 8;
const MAX_ENTRIES = 5000;

async function isDir(p: string): Promise<boolean> {
  return (await stat(p).catch(() => null))?.isDirectory() ?? false;
}

async function listDir(dir: string, depth: number, count: { n: number }): Promise<FileNode[]> {
  if (depth >= MAX_DEPTH) return [];
  const names = await readdir(dir).catch(() => [] as string[]);
  const picked: Array<{ isDir: boolean; name: string; path: string }> = [];
  for (const name of names) {
    if (count.n >= MAX_ENTRIES) break;
    if (name.startsWith('.')) continue;
    const p = path.join(dir, name);
    const d = await isDir(p);
    if (d && SKIP_DIRS.has(name)) continue;
    count.n += 1;
    picked.push({ isDir: d, name, path: p });
  }
  sortDirsFirst(picked, (e) => e.isDir, (e) => e.name);
  const out: FileNode[] = [];
  for (const e of picked) {
    const children = e.isDir ? await listDir(e.path, depth + 1, count) : [];
    out.push({ name: e.name, path: e.path, dir: e.isDir, children });
  }
  return out;
}

/// A bare filename: non-empty, a single path segment (port of `bare_name`).
function bareName(name: string): string {
  const n = name.trim();
  if (n === '') throw new Error('empty name');
  if (n === '.' || n === '..' || n.includes('/') || n.includes('\\')) {
    throw new Error('name must be a single path segment');
  }
  return n;
}

async function exists(p: string): Promise<boolean> {
  return (await stat(p).catch(() => null)) !== null;
}

export const workspaceHandlers: Record<string, Handler> = {
  workspace_pick_folder: async (_args, ctx: Ctx): Promise<string | null> => {
    const res = await openDialog(ctx.win, { properties: ['openDirectory'] });
    if (res.canceled || res.filePaths.length === 0) return null;
    return res.filePaths[0];
  },

  workspace_list: async (args): Promise<FileNode[]> => {
    const root = String(args.path ?? '');
    if (!(await isDir(root))) throw new Error(`not a folder: ${root}`);
    return listDir(root, 0, { n: 0 });
  },

  workspace_new_file: async (args): Promise<string> => {
    const dir = String(args.dir ?? '');
    const name = bareName(String(args.name ?? ''));
    if (!(await isDir(dir))) throw new Error(`not a folder: ${dir}`);
    const target = path.join(dir, name);
    if (await exists(target)) throw new Error(`already exists: ${target}`);
    await writeFile(target, '');
    return target;
  },

  workspace_new_folder: async (args): Promise<string> => {
    const dir = String(args.dir ?? '');
    const name = bareName(String(args.name ?? ''));
    if (!(await isDir(dir))) throw new Error(`not a folder: ${dir}`);
    const target = path.join(dir, name);
    if (await exists(target)) throw new Error(`already exists: ${target}`);
    await mkdir(target);
    return target;
  },

  workspace_rename: async (args): Promise<string> => {
    const src = String(args.path ?? '');
    const name = bareName(String(args.name ?? ''));
    if (!(await exists(src))) throw new Error(`not found: ${src}`);
    const parent = path.dirname(src);
    const target = path.join(parent, name);
    if (target === src) return src;
    if (await exists(target)) throw new Error(`already exists: ${target}`);
    await rename(src, target);
    return target;
  },

  workspace_delete: async (args): Promise<void> => {
    const p = String(args.path ?? '');
    if (!(await exists(p))) throw new Error(`not found: ${p}`);
    await rm(p, { recursive: true, force: true });
  },

  workspace_move: async (args): Promise<string> => {
    const from = String(args.src ?? '');
    const destDir = String(args.destDir ?? '');
    if (!(await exists(from))) throw new Error(`not found: ${from}`);
    if (!(await isDir(destDir))) throw new Error(`not a folder: ${destDir}`);
    if ((await isDir(from)) && (await isWithin(destDir, from))) {
      throw new Error('cannot move a folder into itself');
    }
    const target = path.join(destDir, path.basename(from));
    if (target === from) return from;
    if (await exists(target)) throw new Error(`already exists: ${target}`);
    // rename works within one filesystem; fall back to copy+delete across devices.
    try {
      await rename(from, target);
    } catch {
      await copyRecursive(from, target);
      await rm(from, { recursive: true, force: true });
    }
    return target;
  },

  workspace_copy: async (args): Promise<string> => {
    const from = String(args.src ?? '');
    const destDir = String(args.destDir ?? '');
    if (!(await exists(from))) throw new Error(`not found: ${from}`);
    if (!(await isDir(destDir))) throw new Error(`not a folder: ${destDir}`);
    if ((await isDir(from)) && (await isWithin(destDir, from))) {
      throw new Error('cannot copy a folder into itself');
    }
    const target = path.join(destDir, path.basename(from));
    if (await exists(target)) throw new Error(`already exists: ${target}`);
    await copyRecursive(from, target);
    return target;
  },
};
