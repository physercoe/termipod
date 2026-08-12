/// Local file pane for the two-pane transfer (ADR-055 M1.4) — port of
/// `src-tauri/src/localfs.rs`. Non-recursive listing (hidden files INCLUDED — an
/// SSH user wants `~/.ssh`) plus single-file byte read/write (raw bytes over IPC,
/// no base64 — ADR-055 §7 row 4), and the mkdir/delete/rename ops behind the
/// transfer panel's New Folder / Delete / Rename and directory download.
import { mkdir, readFile, readdir, rename, rm, stat, writeFile } from 'node:fs/promises';
import path from 'node:path';
import type { Handler } from './dispatch';
import { assertSafeLocalDelete, home, parentOrNull, searchTree, sortDirsFirst, walkNameIndex, type NameIndexEntry, type SearchHit } from './fsutil';

const MAX_ENTRIES = 10_000;

// Recursive name-index caps (Inspect tree filter, plan §3 item 5) — deeper and
// wider than `workspace_list`'s @-mention walk because inspection wants the
// whole project, and *includes* hidden files (a `.github/` or `.env.example` is
// an inspection target). Every cap is surfaced to the UI (`truncated`).
const TREE_INDEX_MAX_DEPTH = 12;
const TREE_INDEX_MAX_ENTRIES = 20_000;

// Field names mirror the serde output (`is_dir`, no rename) so the frontend's
// LocalListing/LocalEntry types read unchanged.
interface LocalEntry {
  name: string;
  path: string;
  is_dir: boolean;
  size: number;
  modified_ms: number | null;
  mode: number | null;
}
interface LocalListing {
  path: string;
  parent: string | null;
  entries: LocalEntry[];
  // True when the directory held more than MAX_ENTRIES (the listing is capped);
  // the tree pane surfaces this rather than implying it read everything.
  truncated: boolean;
}


export const localfsHandlers: Record<string, Handler> = {
  localfs_home: async (): Promise<string> => home(),

  localfs_list: async (args): Promise<LocalListing> => {
    const raw = String(args.path ?? '');
    const base = raw === '' || raw === '~' ? home() : raw;
    const baseStat = await stat(base).catch(() => null);
    if (baseStat === null || !baseStat.isDirectory()) throw new Error(`not a folder: ${base}`);

    const names = await readdir(base);
    const entries: LocalEntry[] = [];
    let truncated = false;
    for (const name of names) {
      if (entries.length >= MAX_ENTRIES) {
        truncated = true;
        break;
      }
      const full = path.join(base, name);
      const md = await stat(full).catch(() => null);
      const isDir = md?.isDirectory() ?? false;
      entries.push({
        name,
        path: full,
        is_dir: isDir,
        size: isDir ? 0 : (md?.size ?? 0),
        modified_ms: md !== null && Number.isFinite(md.mtimeMs) ? md.mtimeMs : null,
        mode: md !== null && Number.isInteger(md.mode) ? md.mode : null,
      });
    }
    sortDirsFirst(entries, (e) => e.is_dir, (e) => e.name);
    return { path: base, parent: parentOrNull(base), entries, truncated };
  },

  /// A bounded recursive **name index** of a folder, for the Inspect tree's
  /// filter box (plan §3 item 5). Unlike `workspace_list` this includes hidden
  /// files and uses inspection's own caps; it never descends `SKIP_DIRS` (they
  /// are listed as leaf-tagged nodes but not walked). Returns root-relative
  /// paths only — no bytes, no absolute paths — and a `truncated` flag when a
  /// cap was hit, so the UI never implies it indexed everything.
  tree_index: async (args): Promise<{ entries: NameIndexEntry[]; truncated: boolean }> => {
    return walkNameIndex(String(args.path ?? ''), TREE_INDEX_MAX_DEPTH, TREE_INDEX_MAX_ENTRIES);
  },

  /// Bounded recursive content search of a local root (Inspect T4a) — literal or
  /// regex, capped (≤500 hits / ≤20k files / ≤1 MB per file), binary + SKIP_DIRS
  /// skipped, every cap surfaced via `truncated`.
  tree_search: async (args): Promise<{ hits: SearchHit[]; truncated: boolean; scanned: number }> => {
    return searchTree(String(args.path ?? ''), {
      query: String(args.query ?? ''),
      regex: args.regex === true,
      caseSensitive: args.caseSensitive === true,
      includeSkip: args.includeSkip === true,
    });
  },

  localfs_read: async (args): Promise<Uint8Array> => {
    return await readFile(String(args.path ?? ''));
  },

  /// Does this path name a regular file on THIS machine?
  ///
  /// Added for the Rerun export (J8 Replay W4b-2): the export runs on whichever
  /// host owns the dataset's bytes and returns a path on that host, and "is
  /// that host this machine" has no reliable answer in the hub's metadata — but
  /// the local filesystem answers it directly. A boolean rather than a stat,
  /// because the caller needs the yes/no and nothing else; a missing path and an
  /// unreadable one are both "not here", which is the same decision either way.
  localfs_exists: async (args): Promise<boolean> => {
    try {
      return (await stat(String(args.path ?? ''))).isFile();
    } catch {
      return false;
    }
  },

  localfs_write: async (args): Promise<void> => {
    await writeFile(String(args.path ?? ''), (args.bytes ?? new Uint8Array()) as Uint8Array);
  },

  /// mkdir -p locally (New Folder + the directory-download destination).
  localfs_mkdir: async (args): Promise<void> => {
    await mkdir(String(args.path ?? ''), { recursive: true });
  },

  /// Recursive delete (files and folders) behind the panel's Delete — the
  /// renderer confirms with the user before invoking. `force` tolerates an entry
  /// that vanished between listing and delete (a list→delete race); the guard
  /// refuses a filesystem root or the home directory.
  localfs_delete: async (args): Promise<void> => {
    const target = String(args.path ?? '');
    assertSafeLocalDelete(target);
    await rm(target, { recursive: true, force: true });
  },

  localfs_rename: async (args): Promise<void> => {
    await rename(String(args.from ?? ''), String(args.to ?? ''));
  },
};
