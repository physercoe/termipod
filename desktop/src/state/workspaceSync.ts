import { isTauri } from '../platform';
import { secretDelete, secretGet, secretSet } from './persist';

/// WebDAV sync for the Author **workspace** folder — an Obsidian-vault–style
/// recursive tree mirror (Tauri only; the transfer runs in the Rust core,
/// `foldersync.rs`). This is deliberately *separate* from the Read surface's
/// Zotero WebDAV config (`webdav.ts`): a research library and a notes vault are
/// different remotes with different on-server layouts. Named "workspace sync",
/// not "vault sync", because "Vault" already denotes the secrets manager in this
/// app (glossary) — the Obsidian-vault framing lives only in help text.
///
/// Config: base URL + username in localStorage (non-secret); the password in the
/// consolidated OS-keychain item (via `persist`, so it adds no extra macOS auth
/// prompt). All three are passed to the Rust commands per call — nothing secret
/// is cached in the webview. The tree is mirrored verbatim under the base URL (no
/// subcollection), so a WebDAV endpoint that already holds an Obsidian vault
/// imports straight in.

const LS_URL = 'termipod.workspacesync.url';
const LS_USER = 'termipod.workspacesync.user';
const KC_PASS = 'termipod.workspacesync.password';

export interface WorkspaceSyncConfig {
  url: string;
  user: string;
}

export interface FolderSyncReport {
  uploaded: number;
  downloaded: number;
  skipped: number;
  conflicts: number;
  errors: string[];
}

async function invoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T> {
  const { invoke: inv } = await import('@tauri-apps/api/core');
  return inv<T>(cmd, args);
}

export function loadWorkspaceSyncConfig(): WorkspaceSyncConfig {
  try {
    return { url: localStorage.getItem(LS_URL) ?? '', user: localStorage.getItem(LS_USER) ?? '' };
  } catch {
    return { url: '', user: '' };
  }
}

export function saveWorkspaceSyncConfig(url: string, user: string): void {
  try {
    localStorage.setItem(LS_URL, url.trim());
    localStorage.setItem(LS_USER, user);
  } catch {
    /* ignore */
  }
}

/// True once a URL is configured — an affordance can gate on this.
export function workspaceSyncConfigured(): boolean {
  return loadWorkspaceSyncConfig().url !== '';
}

export async function getWorkspaceSyncPassword(): Promise<string> {
  if (!isTauri()) return '';
  try {
    return (await secretGet(KC_PASS)) ?? '';
  } catch {
    return '';
  }
}

export async function setWorkspaceSyncPassword(pw: string): Promise<void> {
  if (!isTauri()) return;
  if (pw === '') await secretDelete(KC_PASS);
  else await secretSet(KC_PASS, pw);
}

/// Verify connectivity + auth against the configured server. Resolves on success;
/// rejects with the server/auth error message.
export async function verifyWorkspaceSync(url: string, user: string, pass: string): Promise<void> {
  if (!isTauri()) throw new Error('workspace sync requires the desktop app');
  await invoke<string>('folder_webdav_verify', { url: url.trim(), user, pass });
}

/// Two-way, additive (never-delete) sync of `root` against the configured server.
export async function syncWorkspace(root: string): Promise<FolderSyncReport> {
  if (!isTauri()) throw new Error('workspace sync requires the desktop app');
  const { url, user } = loadWorkspaceSyncConfig();
  if (url === '') throw new Error('configure the sync server first');
  const pass = await getWorkspaceSyncPassword();
  return invoke<FolderSyncReport>('folder_webdav_sync', { root, url, user, pass });
}
