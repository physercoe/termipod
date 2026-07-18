import type { IconName } from '../ui/Icon';
import { secretGet, secretSet } from './persist';
import { loadWebdavConfig, loadZoteroBackend, loadZoteroS3Config } from './webdav';
import { loadS3Config, loadSyncBackend, loadWorkspaceSyncConfig } from './workspaceSync';
import { getVoiceModel } from '../voice/settings';

/// TermiPod's own integration config + secrets, gathered in one place so they can
/// be (a) surfaced/managed in the Vault's "TermiPod" tab and (b) sealed into the
/// synced vault bundle — so setting up a new machine restores the WebDAV/S3 sync
/// endpoints and the voice API key along with everything else.
///
/// The four integrations (each secret already lives in the consolidated keychain
/// item via `persist`):
///   • Read storage — Zotero-compatible WebDAV (webdav.ts)
///   • Author workspace — WebDAV (workspaceSync.ts)
///   • Author workspace — S3 / S3-compatible (workspaceSync.ts / s3.rs)
///   • Voice input — DashScope realtime ASR (voice/settings.ts)

/// Non-secret config that seals into / restores from the vault. Snapshotting the
/// raw localStorage strings keeps it trivially forward-compatible (e.g. `voice.model`
/// is a JSON string written by saveJson — copied verbatim, restored verbatim).
export const APP_CONFIG_KEYS = [
  'termipod.webdav.url',
  'termipod.webdav.user',
  // Read-storage (Zotero) backend selection + S3 config — sealed so a new machine
  // restores the Read S3 sync target too (secret in APP_SECRET_KEYS below).
  'termipod.webdav.backend',
  'termipod.webdav.s3.endpoint',
  'termipod.webdav.s3.region',
  'termipod.webdav.s3.bucket',
  'termipod.webdav.s3.prefix',
  'termipod.webdav.s3.accessKeyId',
  'termipod.workspacesync.backend',
  'termipod.workspacesync.url',
  'termipod.workspacesync.user',
  'termipod.workspacesync.s3.endpoint',
  'termipod.workspacesync.s3.region',
  'termipod.workspacesync.s3.bucket',
  'termipod.workspacesync.s3.prefix',
  'termipod.workspacesync.s3.accessKeyId',
  'voice.model',
] as const;

/// Keychain slots for the integration secrets.
export const APP_SECRET_KEYS = [
  'termipod.webdav.password',
  'termipod.webdav.s3.secret',
  'termipod.workspacesync.password',
  'termipod.workspacesync.s3.secret',
  'voice_dashscope_api_key',
] as const;

// ── UI descriptors (Vault → TermiPod tab) ───────────────────────────────────
export interface AppInfoRow {
  labelKey: string;
  value: string;
}
export interface AppSecretRef {
  slot: string;
  labelKey: string;
}
export interface AppIntegration {
  id: string;
  titleKey: string;
  icon: IconName;
  info: AppInfoRow[];
  secrets: AppSecretRef[];
}

/// The integrations with their current on-device config values, for display. Only
/// non-empty info rows are shown by the renderer.
export function listAppIntegrations(): AppIntegration[] {
  const wd = loadWebdavConfig();
  const readBackend = loadZoteroBackend();
  const readS3 = loadZoteroS3Config();
  const ws = loadWorkspaceSyncConfig();
  const s3 = loadS3Config();
  const backend = loadSyncBackend();
  const model = getVoiceModel();
  return [
    {
      id: 'read-webdav',
      titleKey: 'vault.tpReadWebdav',
      icon: 'cloud',
      info: [
        { labelKey: 'vault.tpBackend', value: readBackend },
        { labelKey: 'read.webdavUrl', value: wd.url },
        { labelKey: 'read.webdavUser', value: wd.user },
      ],
      secrets: [{ slot: 'termipod.webdav.password', labelKey: 'read.webdavPass' }],
    },
    {
      id: 'read-s3',
      titleKey: 'vault.tpReadS3',
      icon: 'cloud',
      info: [
        { labelKey: 'author.s3Endpoint', value: readS3.endpoint },
        { labelKey: 'author.s3Region', value: readS3.region },
        { labelKey: 'author.s3Bucket', value: readS3.bucket },
        { labelKey: 'author.s3Prefix', value: readS3.prefix },
        { labelKey: 'author.s3Access', value: readS3.accessKeyId },
      ],
      secrets: [{ slot: 'termipod.webdav.s3.secret', labelKey: 'author.s3Secret' }],
    },
    {
      id: 'workspace-webdav',
      titleKey: 'vault.tpWsWebdav',
      icon: 'cloud',
      info: [
        { labelKey: 'vault.tpBackend', value: backend },
        { labelKey: 'read.webdavUrl', value: ws.url },
        { labelKey: 'read.webdavUser', value: ws.user },
      ],
      secrets: [{ slot: 'termipod.workspacesync.password', labelKey: 'read.webdavPass' }],
    },
    {
      id: 'workspace-s3',
      titleKey: 'vault.tpWsS3',
      icon: 'cloud',
      info: [
        { labelKey: 'author.s3Endpoint', value: s3.endpoint },
        { labelKey: 'author.s3Region', value: s3.region },
        { labelKey: 'author.s3Bucket', value: s3.bucket },
        { labelKey: 'author.s3Prefix', value: s3.prefix },
        { labelKey: 'author.s3Access', value: s3.accessKeyId },
      ],
      secrets: [{ slot: 'termipod.workspacesync.s3.secret', labelKey: 'author.s3Secret' }],
    },
    {
      id: 'voice',
      titleKey: 'vault.tpVoice',
      icon: 'music',
      info: [{ labelKey: 'vault.tpModel', value: model }],
      secrets: [{ slot: 'voice_dashscope_api_key', labelKey: 'vault.tpApiKey' }],
    },
  ];
}

// ── sync export / import (used by vault/bundle.ts) ──────────────────────────
export interface AppIntegrationsExport {
  config: Record<string, string>;
  secrets: Record<string, string>;
}

export async function exportAppIntegrations(): Promise<AppIntegrationsExport> {
  const config: Record<string, string> = {};
  for (const k of APP_CONFIG_KEYS) {
    try {
      const v = localStorage.getItem(k);
      if (v !== null) config[k] = v;
    } catch {
      /* ignore */
    }
  }
  const secrets: Record<string, string> = {};
  for (const k of APP_SECRET_KEYS) {
    const v = await secretGet(k);
    if (v !== null && v !== '') secrets[k] = v;
  }
  return { config, secrets };
}

/// Restore integration config + secrets pulled from the vault. Only keys the
/// bundle carries are touched, so an older/mobile bundle that omits them leaves
/// the local config intact.
export async function importAppIntegrations(
  config: Record<string, string> | undefined,
  secrets: Record<string, string> | undefined,
): Promise<void> {
  for (const [k, v] of Object.entries(config ?? {})) {
    if ((APP_CONFIG_KEYS as readonly string[]).includes(k)) {
      try {
        localStorage.setItem(k, v);
      } catch {
        /* ignore */
      }
    }
  }
  for (const [k, v] of Object.entries(secrets ?? {})) {
    if ((APP_SECRET_KEYS as readonly string[]).includes(k)) await secretSet(k, v);
  }
}
