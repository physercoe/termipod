import { create } from 'zustand';
import { isTauri } from '../platform';
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

async function invoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T> {
  const { invoke: inv } = await import('@tauri-apps/api/core');
  return inv<T>(cmd, args);
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
    if (!isTauri()) return;
    try {
      const dir = await invoke<string>('attachment_default_dir');
      set({ defaultRoot: dir });
    } catch {
      /* leave null — add will surface the error */
    }
  },

  pickCustom: async () => {
    if (!isTauri()) return 'not supported';
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
  if (!isTauri()) throw new Error('attachments require the desktop app');
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

/// Delete a managed attachment's bytes (its `<key>/` folder). No-op for Zotero
/// attachments (we never touch the user's Zotero library) or the browser build.
export async function deleteManagedAttachmentFile(path: string | undefined): Promise<void> {
  if (!isTauri() || path === undefined || path === '') return;
  try {
    await invoke('attachment_delete', { path });
  } catch {
    /* best-effort; the pointer is dropped regardless */
  }
}
