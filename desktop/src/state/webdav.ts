import { isTauri } from '../platform';
import { activeAttachmentRoot, useAttachmentConfig } from './attachments';
import { useZoteroStorage } from './zoteroStorage';

/// Zotero-compatible WebDAV file sync for the Read-surface storage root (Tauri
/// only — the transfer + zip + hashing happen in the Rust core, `webdav.rs`).
///
/// Config: the base URL + username live in localStorage (non-secret), the
/// password in the OS keychain (`keychain_set/get`). All three are passed to the
/// Rust commands per call — nothing secret is cached in the webview. The server
/// layout is Zotero's own (`zotero/<KEY>.zip` + `<KEY>.prop`), so the same server
/// a user already syncs Zotero to works and files appear in both apps.

const LS_URL = 'termipod.webdav.url';
const LS_USER = 'termipod.webdav.user';
const KC_PASS = 'termipod.webdav.password';

export interface WebdavConfig {
  url: string;
  user: string;
}

export interface SyncReport {
  uploaded: number;
  downloaded: number;
  skipped: number;
  conflicts: number;
  downloadedKeys: string[];
  errors: string[];
}

async function invoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T> {
  const { invoke: inv } = await import('@tauri-apps/api/core');
  return inv<T>(cmd, args);
}

export function loadWebdavConfig(): WebdavConfig {
  try {
    return { url: localStorage.getItem(LS_URL) ?? '', user: localStorage.getItem(LS_USER) ?? '' };
  } catch {
    return { url: '', user: '' };
  }
}

export function saveWebdavConfig(url: string, user: string): void {
  try {
    localStorage.setItem(LS_URL, url.trim());
    localStorage.setItem(LS_USER, user);
  } catch {
    /* ignore */
  }
}

/// True once a URL is configured — the button/affordance can gate on this.
export function webdavConfigured(): boolean {
  return loadWebdavConfig().url !== '';
}

export async function getWebdavPassword(): Promise<string> {
  if (!isTauri()) return '';
  try {
    return (await invoke<string | null>('keychain_get', { key: KC_PASS })) ?? '';
  } catch {
    return '';
  }
}

export async function setWebdavPassword(pw: string): Promise<void> {
  if (!isTauri()) return;
  if (pw === '') {
    await invoke('keychain_delete', { key: KC_PASS });
  } else {
    await invoke('keychain_set', { key: KC_PASS, value: pw });
  }
}

/// Verify connectivity + write access. Resolves on success; rejects with the
/// server/auth error message.
export async function verifyWebdav(url: string, user: string, pass: string): Promise<void> {
  if (!isTauri()) throw new Error('WebDAV sync requires the desktop app');
  await invoke<string>('webdav_verify', { url: url.trim(), user, pass });
}

/// Two-way sync the active storage root against the configured WebDAV server.
/// Re-indexes a linked Zotero folder afterwards so freshly-downloaded files show.
export async function syncWebdav(): Promise<SyncReport> {
  if (!isTauri()) throw new Error('WebDAV sync requires the desktop app');
  const { url, user } = loadWebdavConfig();
  if (url === '') throw new Error('configure the WebDAV server first');

  let root = activeAttachmentRoot();
  if (root === null) {
    await useAttachmentConfig.getState().resolveDefault();
    root = activeAttachmentRoot();
  }
  if (root === null) throw new Error('no storage location — link a Zotero folder or add an attachment first');

  const pass = await getWebdavPassword();
  const report = await invoke<SyncReport>('webdav_sync', { root, url, user, pass });
  // Downloaded files only become resolvable once the folder index is refreshed;
  // reindex() is a no-op when no Zotero folder is linked (managed attachments
  // resolve by absolute path and need no index).
  if (report.downloaded > 0) {
    await useZoteroStorage.getState().reindex();
  }
  return report;
}
