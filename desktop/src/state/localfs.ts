import { invoke } from '../bridge';

/// Typed bridge to the Rust local-filesystem commands (localfs.rs) — the local
/// side of the two-pane SFTP transfer. Desktop-only; the plain-browser build has
/// no native core (the Files panel is SSH-only anyway).

export interface LocalEntry {
  name: string;
  path: string;
  is_dir: boolean;
  size: number;
}
export interface LocalListing {
  path: string;
  parent: string | null;
  entries: LocalEntry[];
  /// True when the directory held more than the listing cap (10 000 entries).
  truncated: boolean;
}

/// One entry of the Inspect tree's recursive name-index (`tree_index`): a
/// root-relative path and whether it is a directory.
export interface TreeIndexEntry {
  rel: string;
  is_dir: boolean;
}

/** The default local directory (the user's home). */
export function localHome(): Promise<string> {
  return invoke<string>('localfs_home');
}
/** List a local directory (empty / "~" → home). */
export function localList(path: string): Promise<LocalListing> {
  return invoke<LocalListing>('localfs_list', { path });
}
/** Bounded recursive name-index of a folder (Inspect tree filter; hidden files included, SKIP_DIRS not descended). */
export function treeIndex(path: string): Promise<{ entries: TreeIndexEntry[]; truncated: boolean }> {
  return invoke<{ entries: TreeIndexEntry[]; truncated: boolean }>('tree_index', { path });
}
/** Read a local file → raw bytes (for upload to remote; no base64 over IPC — §7 row 4). */
export function localRead(path: string): Promise<Uint8Array> {
  return invoke<Uint8Array>('localfs_read', { path });
}
/** Write raw bytes to a local path (for download from remote). */
export function localWrite(path: string, bytes: Uint8Array): Promise<void> {
  return invoke('localfs_write', { path, bytes });
}
/** mkdir -p locally (New Folder, directory-download destination). */
export function localMkdir(path: string): Promise<void> {
  return invoke('localfs_mkdir', { path });
}
/** Recursive delete (files and folders). */
export function localDelete(path: string): Promise<void> {
  return invoke('localfs_delete', { path });
}
export function localRename(from: string, to: string): Promise<void> {
  return invoke('localfs_rename', { from, to });
}
