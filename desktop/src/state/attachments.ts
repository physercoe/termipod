import { create } from 'zustand';
import { invoke } from '../bridge';
import { isShell } from '../platform';
import { useZoteroStorage } from './zoteroStorage';

/// Where user-added attachments are written (Tauri only). Resolution, per the
/// director's spec:
///   1. A linked Zotero `storage/` folder wins — new files land beside imports,
///      Zotero-style (`<key>/<file>`), so they travel with the library.
///   2. Else a user-set custom location (persisted here).
///   3. Else the app default, `<app-data>/storage` (resolved from the Rust core).
/// The chosen root is passed to `attachment_add`; the file's absolute path is
/// then stored on the reference and read back via `attachment_read`.

const LS_ROOT = 'termipod.attachments.customRoot';

interface RustPicked {
  path: string;
  name: string;
}

interface AttachmentConfigState {
  customRoot: string | null; // user override (persisted)
  defaultRoot: string | null; // <app-data>/storage, resolved at startup
  /** Fetch the app-default storage dir from the Rust core (idempotent). */
  resolveDefault: () => Promise<void>;
  /** Native folder pick to set a custom root. Returns an error message or null. */
  pickCustom: () => Promise<string | null>;
  /** Clear the override → fall back to the default (or a linked Zotero folder). */
  clearCustom: () => void;
}

function persist(p: string | null): void {
  try {
    if (p === null) localStorage.removeItem(LS_ROOT);
    else localStorage.setItem(LS_ROOT, p);
  } catch {
    /* ignore */
  }
}

function loadCustom(): string | null {
  try {
    return localStorage.getItem(LS_ROOT);
  } catch {
    return null;
  }
}

export const useAttachmentConfig = create<AttachmentConfigState>((set) => ({
  customRoot: loadCustom(),
  defaultRoot: null,

  resolveDefault: async () => {
    if (!isShell()) return;
    try {
      const dir = await invoke<string>('attachment_default_dir');
      set({ defaultRoot: dir });
    } catch {
      /* leave null — add will surface the error */
    }
  },

  pickCustom: async () => {
    if (!isShell()) return 'not supported';
    try {
      const dir = await invoke<string | null>('attachment_pick_dir');
      if (dir === null) return null; // cancelled
      persist(dir);
      set({ customRoot: dir });
      return null;
    } catch (e) {
      return e instanceof Error ? e.message : String(e);
    }
  },

  clearCustom: () => {
    persist(null);
    set({ customRoot: null });
  },
}));

/// The active write root: a linked Zotero storage folder wins, else the user
/// override, else the app default. Null only before the default resolves.
export function activeAttachmentRoot(): string | null {
  const zot = useZoteroStorage.getState().path;
  if (zot !== null && zot !== '') return zot;
  const cfg = useAttachmentConfig.getState();
  return cfg.customRoot ?? cfg.defaultRoot;
}

/// A human label for where the active root is (for Settings / the inspector).
export function activeRootLabel(): { kind: 'zotero' | 'custom' | 'default' | 'none'; path: string | null } {
  const zot = useZoteroStorage.getState().path;
  if (zot !== null && zot !== '') return { kind: 'zotero', path: zot };
  const cfg = useAttachmentConfig.getState();
  if (cfg.customRoot !== null && cfg.customRoot !== '') return { kind: 'custom', path: cfg.customRoot };
  if (cfg.defaultRoot !== null) return { kind: 'default', path: cfg.defaultRoot };
  return { kind: 'none', path: null };
}

/// Pick a file and copy it into the active root, returning the managed-attachment
/// coordinates to store on a reference. Null on cancel; throws with a message on
/// failure (no root, copy error, …).
export async function pickAndCopyAttachment(): Promise<
  { file: string; contentType?: string; key: string; path: string } | null
> {
  if (!isShell()) throw new Error('attachments require the desktop app');
  const picked = await invoke<RustPicked | null>('attachment_pick_file');
  if (picked === null) return null; // cancelled
  // Ensure a root is resolved (default may not have been fetched yet).
  let root = activeAttachmentRoot();
  if (root === null) {
    await useAttachmentConfig.getState().resolveDefault();
    root = activeAttachmentRoot();
  }
  if (root === null) throw new Error('no attachment storage location');
  const added = await invoke<{ key: string; file: string; path: string; contentType: string }>('attachment_add', {
    root,
    src: picked.path,
  });
  return { file: added.file, contentType: added.contentType, key: added.key, path: added.path };
}

// ---- note images (de-inlined) ---------------------------------------------
//
// Note screenshots/pastes used to be embedded as base64 data-URIs directly in the
// note markdown, which bloated the note string (and every localStorage/hub-sync
// payload). Instead we write the bytes as a managed attachment and reference it
// by a short, portable scheme — `termipod-att://<key>/<file>` — resolved back to
// a Blob at render time. Portable across devices (no absolute paths) and the
// image files ride WebDAV/hub file sync like any other attachment.

export const NOTE_ATT_SCHEME = 'termipod-att://';

interface NativeFile {
  bytes: Uint8Array; // raw bytes over IPC, no base64 (§7 row 4)
  mime: string;
}

/// Write image bytes into the active attachment root and return the short
/// `termipod-att://<key>/<file>` reference to embed in note markdown. Tauri only —
/// callers fall back to an inline data-URI in the browser build.
export async function writeNoteImage(base64: string, filename: string): Promise<string | null> {
  if (!isShell()) return null;
  let root = activeAttachmentRoot();
  if (root === null) {
    await useAttachmentConfig.getState().resolveDefault();
    root = activeAttachmentRoot();
  }
  if (root === null) return null;
  const added = await invoke<{ key: string; file: string }>('attachment_write_bytes', {
    root,
    filename,
    bytes: b64ToBytes(base64), // decode once here; raw bytes cross IPC (§7 row 4)
  });
  // A linked-Zotero root: refresh the index so the new file resolves immediately.
  if (useZoteroStorage.getState().path === root) {
    await useZoteroStorage.getState().reindex();
  }
  return `${NOTE_ATT_SCHEME}${added.key}/${added.file}`;
}

function b64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i += 1) out[i] = bin.charCodeAt(i);
  return out;
}

/// Resolve a `termipod-att://<key>/<file>` note-image reference to a Blob. Tries
/// a linked Zotero folder's index first, then the active root. Null if missing.
export async function loadNoteImage(ref: string): Promise<Blob | null> {
  if (!ref.startsWith(NOTE_ATT_SCHEME)) return null;
  const rest = ref.slice(NOTE_ATT_SCHEME.length);
  const slash = rest.indexOf('/');
  if (slash < 0) return null;
  const key = rest.slice(0, slash);
  const file = rest.slice(slash + 1);
  const k = `${key}/${file}`;
  const zs = useZoteroStorage.getState();

  // Browser build: the live File handle from the linked <input webkitdirectory>.
  const live = zs.files.get(k);
  if (live !== undefined) return live;
  if (!isShell()) return null;

  const tryRead = async (root: string, rel: string): Promise<Blob | null> => {
    try {
      const f = await invoke<NativeFile>('storage_read', { path: root, rel });
      return new Blob([f.bytes as BlobPart], { type: f.mime });
    } catch {
      return null;
    }
  };

  // Linked Zotero folder index (rel path under that root).
  const rel = zs.rels.get(k);
  if (rel !== undefined && zs.path !== null) {
    const b = await tryRead(zs.path, rel);
    if (b !== null) return b;
  }
  // Active attachment root (where note images are written).
  let root = activeAttachmentRoot();
  if (root === null) {
    await useAttachmentConfig.getState().resolveDefault();
    root = activeAttachmentRoot();
  }
  if (root !== null) return tryRead(root, k);
  return null;
}

/// Delete a managed attachment's bytes (its `<key>/` folder). No-op for Zotero
/// attachments (we never touch the user's Zotero library) or the browser build.
export async function deleteManagedAttachmentFile(path: string | undefined): Promise<void> {
  if (!isShell() || path === undefined || path === '') return;
  try {
    await invoke('attachment_delete', { path });
  } catch {
    /* best-effort; the pointer is dropped regardless */
  }
}
