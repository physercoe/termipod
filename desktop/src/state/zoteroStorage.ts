import { create } from 'zustand';
import { isTauri } from '../platform';

/// Index of a user-linked Zotero `storage/` folder so the Read surface can open
/// a reference's PDF locally — the bytes never leave the device.
///
/// Two modes, chosen by build target:
/// - **Tauri** — the folder is picked via a native dialog (Rust
///   `storage_pick_folder`) and its absolute **path** is persisted
///   (localStorage) + re-indexed on startup (`storage_reindex`), so the link
///   SURVIVES a restart (director report: the link was lost on reopen). File
///   bytes are read on demand through `storage_read`.
/// - **Browser** — no native core, so the picker is `<input webkitdirectory>`
///   giving live `File` handles. Session-only: a `File` can't be persisted across
///   a reload, so the folder is re-linked each session.
///
/// Zotero lays files out as `storage/<attachment-key>/<filename>`; the index is
/// keyed by `<attachment-key>/<filename>` (matching a Reference's
/// `zoteroStorage`). In Tauri mode the value is the file's path relative to the
/// linked root (`rel`), which `storage_read` reopens.

const LS_PATH = 'termipod.zotero.storagePath';

interface RustEntry {
  key: string;
  file: string;
  rel: string;
}
interface RustIndex {
  path: string;
  folderName: string;
  entries: RustEntry[];
}
interface RustFile {
  base64: string;
  mime: string;
}

interface ZoteroStorageState {
  folderName: string | null;
  count: number;
  path: string | null; // tauri: absolute linked-folder path (persisted)
  rels: Map<string, string>; // tauri: "key/file" -> path relative to root
  files: Map<string, File>; // browser: "key/file" -> live File handle
  /**
   * Native folder pick (Tauri). `start` seeds the dialog's initial directory so it
   * opens at the current real storage location rather than whatever folder another
   * tab last browsed (the OS reuses one app-global last-used dir otherwise).
   * Returns an error message, or null on success/cancel.
   */
  linkNative: (start?: string) => Promise<string | null>;
  /** Re-index the persisted path on startup (Tauri). No-op with nothing saved. */
  reindex: () => Promise<void>;
  /** Browser fallback — index a `<input webkitdirectory>` FileList (session-only). */
  linkFolder: (list: FileList) => void;
  clear: () => void;
}

async function invoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T> {
  const { invoke: inv } = await import('@tauri-apps/api/core');
  return inv<T>(cmd, args);
}

function persistPath(p: string | null): void {
  try {
    if (p === null) localStorage.removeItem(LS_PATH);
    else localStorage.setItem(LS_PATH, p);
  } catch {
    /* ignore */
  }
}

function relsFrom(idx: RustIndex): Map<string, string> {
  const rels = new Map<string, string>();
  for (const e of idx.entries) rels.set(`${e.key}/${e.file}`, e.rel);
  return rels;
}

export const useZoteroStorage = create<ZoteroStorageState>((set) => ({
  folderName: null,
  count: 0,
  path: null,
  rels: new Map(),
  files: new Map(),

  linkNative: async (start) => {
    try {
      const idx = await invoke<RustIndex | null>('storage_pick_folder', { start: start ?? null });
      if (idx === null) return null; // user cancelled
      const rels = relsFrom(idx);
      persistPath(idx.path);
      set({ path: idx.path, folderName: idx.folderName, rels, count: rels.size });
      return null;
    } catch (e) {
      return e instanceof Error ? e.message : String(e);
    }
  },

  reindex: async () => {
    let saved: string | null = null;
    try {
      saved = localStorage.getItem(LS_PATH);
    } catch {
      /* ignore */
    }
    if (saved === null || saved === '') return;
    try {
      const idx = await invoke<RustIndex>('storage_reindex', { path: saved });
      const rels = relsFrom(idx);
      set({ path: idx.path, folderName: idx.folderName, rels, count: rels.size });
    } catch {
      // Folder moved/removed — drop the stale path so the UI prompts a re-link.
      persistPath(null);
      set({ path: null, folderName: null, rels: new Map(), count: 0 });
    }
  },

  linkFolder: (list) => {
    const files = new Map<string, File>();
    let root: string | null = null;
    for (const f of Array.from(list)) {
      // webkitRelativePath e.g. "storage/VLABQTMC/Paper.pdf" — key by the last two
      // segments (<key>/<filename>) regardless of the picked folder's own name.
      const rel = (f as File & { webkitRelativePath?: string }).webkitRelativePath ?? f.name;
      const parts = rel.split('/').filter((p) => p !== '');
      if (root === null && parts.length > 0) root = parts[0];
      if (parts.length >= 2) {
        files.set(`${parts[parts.length - 2]}/${parts[parts.length - 1]}`, f);
      }
    }
    set({ files, folderName: root, count: files.size });
  },

  clear: () => {
    persistPath(null);
    set({ folderName: null, count: 0, path: null, rels: new Map(), files: new Map() });
  },
}));

// An attachment to resolve: a Zotero-indexed one (`key`/`file`) or a user-managed
// one carrying its own absolute `path`.
type AttRef = { key?: string; file: string; path?: string } | undefined;
type Resolvable = Pick<ZoteroStorageState, 'rels' | 'files' | 'path'>;

/// True if the attachment is resolvable. A managed attachment (absolute `path`)
/// is self-resolving; a Zotero one must be present in the linked folder.
export function hasAttachment(state: Pick<ZoteroStorageState, 'rels' | 'files'>, att: AttRef): boolean {
  if (att === undefined) return false;
  if (att.path !== undefined && att.path !== '') return true;
  if (att.key === undefined) return false;
  const k = `${att.key}/${att.file}`;
  return state.rels.has(k) || state.files.has(k);
}

function b64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i += 1) out[i] = bin.charCodeAt(i);
  return out;
}

/// Resolve an attachment to a Blob (async). Browser mode returns the live File;
/// Tauri mode reads the bytes through the Rust core. Null when the folder isn't
/// linked or the file is missing.
export async function loadAttachmentBlob(state: Resolvable, att: AttRef): Promise<Blob | null> {
  if (att === undefined) return null;
  // Managed attachment — read its absolute path through the Rust core (Tauri).
  if (att.path !== undefined && att.path !== '' && isTauri()) {
    try {
      const f = await invoke<RustFile>('attachment_read', { path: att.path });
      const bytes = b64ToBytes(f.base64);
      return new Blob([bytes.buffer as ArrayBuffer], { type: f.mime });
    } catch {
      return null;
    }
  }
  if (att.key === undefined) return null;
  const k = `${att.key}/${att.file}`;
  const file = state.files.get(k);
  if (file !== undefined) return file;
  const rel = state.rels.get(k);
  if (rel !== undefined && state.path !== null && isTauri()) {
    try {
      const f = await invoke<RustFile>('storage_read', { path: state.path, rel });
      // `.buffer` is a plain ArrayBuffer here (the view is created full-size over
      // a fresh buffer); the cast silences the DOM lib's SharedArrayBuffer union.
      const bytes = b64ToBytes(f.base64);
      return new Blob([bytes.buffer as ArrayBuffer], { type: f.mime });
    } catch {
      return null;
    }
  }
  return null;
}
