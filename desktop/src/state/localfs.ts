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
}

/** The default local directory (the user's home). */
export function localHome(): Promise<string> {
  return invoke<string>('localfs_home');
}
/** List a local directory (empty / "~" → home). */
export function localList(path: string): Promise<LocalListing> {
  return invoke<LocalListing>('localfs_list', { path });
}
/** Read a local file → base64 bytes (for upload to remote). */
export function localRead(path: string): Promise<string> {
  return invoke<string>('localfs_read', { path });
}
/** Write base64 bytes to a local path (for download from remote). */
export function localWrite(path: string, dataB64: string): Promise<void> {
  return invoke('localfs_write', { path, dataB64 });
}
