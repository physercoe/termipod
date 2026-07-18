import { isTauri } from '../platform';
import { proxyForConnection } from './proxy';
import { secretDelete, secretGet, secretSet } from './persist';
import { invokeWithProgress, type SyncProgress } from './syncProgress';

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

const LS_BACKEND = 'termipod.workspacesync.backend';
const LS_URL = 'termipod.workspacesync.url';
const LS_USER = 'termipod.workspacesync.user';
const KC_PASS = 'termipod.workspacesync.password';
// S3 (and S3-compatible) backend config. Secret access key → keychain.
const LS_S3_ENDPOINT = 'termipod.workspacesync.s3.endpoint';
const LS_S3_REGION = 'termipod.workspacesync.s3.region';
const LS_S3_BUCKET = 'termipod.workspacesync.s3.bucket';
const LS_S3_PREFIX = 'termipod.workspacesync.s3.prefix';
const LS_S3_ACCESS = 'termipod.workspacesync.s3.accessKeyId';
const KC_S3_SECRET = 'termipod.workspacesync.s3.secret';

export type SyncBackend = 'webdav' | 's3';

export interface WorkspaceSyncConfig {
  url: string;
  user: string;
}

export interface S3Config {
  endpoint: string;
  region: string;
  bucket: string;
  prefix: string;
  accessKeyId: string;
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

/// Verify connectivity + auth against the configured WebDAV server. Resolves on
/// success; rejects with the server/auth error message.
export async function verifyWorkspaceSync(url: string, user: string, pass: string): Promise<void> {
  if (!isTauri()) throw new Error('workspace sync requires the desktop app');
  await invoke<string>('folder_webdav_verify', {
    url: url.trim(),
    user,
    pass,
    proxy: proxyForConnection('workspace') ?? null,
  });
}

// ── backend selection ───────────────────────────────────────────────────────
export function loadSyncBackend(): SyncBackend {
  try {
    return localStorage.getItem(LS_BACKEND) === 's3' ? 's3' : 'webdav';
  } catch {
    return 'webdav';
  }
}

export function saveSyncBackend(backend: SyncBackend): void {
  try {
    localStorage.setItem(LS_BACKEND, backend);
  } catch {
    /* ignore */
  }
}

// ── S3 backend ──────────────────────────────────────────────────────────────
export function loadS3Config(): S3Config {
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

export function saveS3Config(cfg: S3Config): void {
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

export async function getS3Secret(): Promise<string> {
  if (!isTauri()) return '';
  try {
    return (await secretGet(KC_S3_SECRET)) ?? '';
  } catch {
    return '';
  }
}

export async function setS3Secret(secret: string): Promise<void> {
  if (!isTauri()) return;
  if (secret === '') await secretDelete(KC_S3_SECRET);
  else await secretSet(KC_S3_SECRET, secret);
}

/// True once the active backend is configured enough to sync.
export function workspaceSyncConfiguredFor(backend: SyncBackend): boolean {
  return backend === 's3' ? loadS3Config().bucket !== '' : loadWorkspaceSyncConfig().url !== '';
}

/// Verify the S3 backend with the given form values (secret passed explicitly so
/// the modal can verify before persisting).
export async function verifyS3Sync(cfg: S3Config, secretKey: string): Promise<void> {
  if (!isTauri()) throw new Error('workspace sync requires the desktop app');
  await invoke<string>('s3_sync_verify', {
    endpoint: cfg.endpoint.trim(),
    region: cfg.region.trim(),
    bucket: cfg.bucket.trim(),
    prefix: cfg.prefix.trim(),
    accessKey: cfg.accessKeyId.trim(),
    secretKey,
    proxy: proxyForConnection('workspace') ?? null,
  });
}

/// Two-way, additive (never-delete) sync of `root` against the configured backend.
/// `onProgress` (optional) receives live N/M ticks for the status-bar chip.
export async function syncWorkspace(
  root: string,
  onProgress?: (p: SyncProgress) => void,
): Promise<FolderSyncReport> {
  if (!isTauri()) throw new Error('workspace sync requires the desktop app');
  if (loadSyncBackend() === 's3') {
    const cfg = loadS3Config();
    if (cfg.bucket === '') throw new Error('configure the S3 bucket first');
    const secretKey = await getS3Secret();
    return invokeWithProgress<FolderSyncReport>(
      's3_sync',
      {
        root,
        endpoint: cfg.endpoint.trim(),
        region: cfg.region.trim(),
        bucket: cfg.bucket.trim(),
        prefix: cfg.prefix.trim(),
        accessKey: cfg.accessKeyId.trim(),
        secretKey,
        proxy: proxyForConnection('workspace') ?? null,
      },
      onProgress,
    );
  }
  const { url, user } = loadWorkspaceSyncConfig();
  if (url === '') throw new Error('configure the sync server first');
  const pass = await getWorkspaceSyncPassword();
  return invokeWithProgress<FolderSyncReport>(
    'folder_webdav_sync',
    {
      root,
      url,
      user,
      pass,
      proxy: proxyForConnection('workspace') ?? null,
    },
    onProgress,
  );
}
