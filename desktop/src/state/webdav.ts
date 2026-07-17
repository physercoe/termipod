import { isTauri } from '../platform';
import { activeAttachmentRoot, useAttachmentConfig } from './attachments';
import { proxyForConnection } from './proxy';
import { secretDelete, secretGet, secretSet } from './persist';
import { useZoteroStorage } from './zoteroStorage';
import type { S3Config, SyncBackend } from './workspaceSync';

/// Zotero-compatible file sync for the Read-surface storage root (Tauri only — the
/// transfer + zip + hashing happen in the Rust core). Two backends, both writing
/// Zotero's own `zotero/<KEY>.zip` + `<KEY>.prop` layout:
/// - **WebDAV** (`webdav.rs`) — interoperates with the real Zotero apps (same
///   server, files appear in both).
/// - **S3** (`s3.rs` `s3_zotero_sync`) — the same object layout in an S3 bucket.
///   Zotero itself can't read S3, so this syncs attachments TermiPod-to-TermiPod
///   only (director choice); it's the cheaper/faster option when Zotero-app
///   interop isn't needed.
///
/// Config: non-secret fields in localStorage; the password / S3 secret in the OS
/// keychain (consolidated item, no extra macOS prompt). All are passed to the Rust
/// commands per call — nothing secret is cached in the webview.

const LS_URL = 'termipod.webdav.url';
const LS_USER = 'termipod.webdav.user';
const KC_PASS = 'termipod.webdav.password';
const LS_BACKEND = 'termipod.webdav.backend';
const LS_S3_ENDPOINT = 'termipod.webdav.s3.endpoint';
const LS_S3_REGION = 'termipod.webdav.s3.region';
const LS_S3_BUCKET = 'termipod.webdav.s3.bucket';
const LS_S3_PREFIX = 'termipod.webdav.s3.prefix';
const LS_S3_ACCESS = 'termipod.webdav.s3.accessKeyId';
const KC_S3_SECRET = 'termipod.webdav.s3.secret';

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
    // Routed through the consolidated secret store (single keychain item) so it
    // doesn't add its own macOS auth prompt.
    return (await secretGet(KC_PASS)) ?? '';
  } catch {
    return '';
  }
}

export async function setWebdavPassword(pw: string): Promise<void> {
  if (!isTauri()) return;
  if (pw === '') await secretDelete(KC_PASS);
  else await secretSet(KC_PASS, pw);
}

/// Verify connectivity + write access. Resolves on success; rejects with the
/// server/auth error message.
export async function verifyWebdav(url: string, user: string, pass: string): Promise<void> {
  if (!isTauri()) throw new Error('WebDAV sync requires the desktop app');
  await invoke<string>('webdav_verify', {
    url: url.trim(),
    user,
    pass,
    proxy: proxyForConnection('attachments') ?? null,
  });
}

// ── backend selection ────────────────────────────────────────────────────────
export function loadZoteroBackend(): SyncBackend {
  try {
    return localStorage.getItem(LS_BACKEND) === 's3' ? 's3' : 'webdav';
  } catch {
    return 'webdav';
  }
}

export function saveZoteroBackend(backend: SyncBackend): void {
  try {
    localStorage.setItem(LS_BACKEND, backend);
  } catch {
    /* ignore */
  }
}

// ── S3 backend (Zotero object layout in a bucket) ────────────────────────────
export function loadZoteroS3Config(): S3Config {
  try {
    return {
      endpoint: localStorage.getItem(LS_S3_ENDPOINT) ?? '',
      region: localStorage.getItem(LS_S3_REGION) ?? '',
      bucket: localStorage.getItem(LS_S3_BUCKET) ?? '',
      prefix: localStorage.getItem(LS_S3_PREFIX) ?? '',
      accessKeyId: localStorage.getItem(LS_S3_ACCESS) ?? '',
    };
  } catch {
    return { endpoint: '', region: '', bucket: '', prefix: '', accessKeyId: '' };
  }
}

export function saveZoteroS3Config(cfg: S3Config): void {
  try {
    localStorage.setItem(LS_S3_ENDPOINT, cfg.endpoint.trim());
    localStorage.setItem(LS_S3_REGION, cfg.region.trim());
    localStorage.setItem(LS_S3_BUCKET, cfg.bucket.trim());
    localStorage.setItem(LS_S3_PREFIX, cfg.prefix.trim());
    localStorage.setItem(LS_S3_ACCESS, cfg.accessKeyId.trim());
  } catch {
    /* ignore */
  }
}

export async function getZoteroS3Secret(): Promise<string> {
  if (!isTauri()) return '';
  try {
    return (await secretGet(KC_S3_SECRET)) ?? '';
  } catch {
    return '';
  }
}

export async function setZoteroS3Secret(secret: string): Promise<void> {
  if (!isTauri()) return;
  if (secret === '') await secretDelete(KC_S3_SECRET);
  else await secretSet(KC_S3_SECRET, secret);
}

/// Verify the S3 backend with the given form values (secret passed explicitly so
/// the modal can verify before persisting). Reuses the workspace S3 verify command.
export async function verifyZoteroS3(cfg: S3Config, secretKey: string): Promise<void> {
  if (!isTauri()) throw new Error('sync requires the desktop app');
  await invoke<string>('s3_sync_verify', {
    endpoint: cfg.endpoint.trim(),
    region: cfg.region.trim(),
    bucket: cfg.bucket.trim(),
    prefix: cfg.prefix.trim(),
    accessKey: cfg.accessKeyId.trim(),
    secretKey,
    proxy: proxyForConnection('attachments') ?? null,
  });
}

/// Two-way sync the active storage root against the configured backend (WebDAV or
/// S3). Re-indexes a linked Zotero folder afterwards so freshly-downloaded files
/// show.
export async function syncWebdav(): Promise<SyncReport> {
  if (!isTauri()) throw new Error('sync requires the desktop app');

  let root = activeAttachmentRoot();
  if (root === null) {
    await useAttachmentConfig.getState().resolveDefault();
    root = activeAttachmentRoot();
  }
  if (root === null) throw new Error('no storage location — link a Zotero folder or add an attachment first');

  let report: SyncReport;
  if (loadZoteroBackend() === 's3') {
    const cfg = loadZoteroS3Config();
    if (cfg.bucket === '') throw new Error('configure the S3 bucket first');
    const secretKey = await getZoteroS3Secret();
    report = await invoke<SyncReport>('s3_zotero_sync', {
      root,
      endpoint: cfg.endpoint.trim(),
      region: cfg.region.trim(),
      bucket: cfg.bucket.trim(),
      prefix: cfg.prefix.trim(),
      accessKey: cfg.accessKeyId.trim(),
      secretKey,
      proxy: proxyForConnection('attachments') ?? null,
    });
  } else {
    const { url, user } = loadWebdavConfig();
    if (url === '') throw new Error('configure the WebDAV server first');
    const pass = await getWebdavPassword();
    report = await invoke<SyncReport>('webdav_sync', {
      root,
      url,
      user,
      pass,
      proxy: proxyForConnection('attachments') ?? null,
    });
  }
  // Downloaded files only become resolvable once the folder index is refreshed;
  // reindex() is a no-op when no Zotero folder is linked (managed attachments
  // resolve by absolute path and need no index).
  if (report.downloaded > 0) {
    await useZoteroStorage.getState().reindex();
  }
  return report;
}
