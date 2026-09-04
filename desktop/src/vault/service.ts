import type { HubClient } from '../hub/client';
import { invoke } from '../bridge';
import { HubApiError } from '../hub/errors';
import { num, str } from '../hub/types';
import { isShell } from '../platform';
import { secretDeleteMany, secretGet, secretSet } from '../state/persist';
import { assembleBundle, importBundle, loadVaultState, parseBundle, saveVaultState } from './bundle';
import {
  canonicalVaultValue,
  mergeVaultBundles,
  vaultReviewProjection,
  type VaultChange,
  type VaultResolutions,
  type VaultSyncDirection,
} from './merge';
import {
  vaultGenerateDevice,
  vaultGenerateKey,
  vaultGenerateRecoveryCode,
  vaultOpen,
  vaultSeal,
  vaultUnwrapRecovery,
  vaultWrapForDevice,
  vaultWrapForRecovery,
} from './crypto';

/// Vault orchestration (parity Phase 2b). The vault key + device seed live in
/// the OS keychain (`vault_key`, `vault_device_seed`); non-secret version/device
/// identity is in localStorage (loadVaultState). All hub I/O goes through the
/// authenticated HubClient. Cross-device Rust↔Dart interop is UNVERIFIED — this
/// is experimental until confirmed against a phone.

const KEY_VAULT = 'vault_key';
const KEY_SEED = 'vault_device_seed';

/// Stable error codes for the sync flows — the service layer has no t(), so it
/// throws coded errors and the UI maps code → localized message at the catch
/// site (#320). Keep the codes stable: they are the i18n key suffixes.
export type VaultErrorCode = 'noKey' | 'conflict' | 'empty' | 'noRecovery' | 'stalePreview';

export class VaultError extends Error {
  constructor(readonly code: VaultErrorCode) {
    super(code);
    this.name = 'VaultError';
  }
}

function randomDeviceId(): string {
  const b = new Uint8Array(12);
  crypto.getRandomValues(b);
  return Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
}

/// OS-platform label ("MacIntel", "Win32", …) — the always-available fallback
/// when the real hostname can't be resolved.
function platformLabel(): string {
  return typeof navigator !== 'undefined' && navigator.platform ? navigator.platform : 'desktop';
}

/// A human machine name for the vault status + device enrollment. Prefers the OS
/// hostname (via the Rust `system_hostname` command, resolved once + cached) so
/// two of the director's Macs read as "mac-studio" vs "macbook" rather than both
/// "MacIntel"; falls back to the platform label off-desktop or on any failure.
let cachedMachine: string | null = null;
export async function machineName(): Promise<string> {
  if (cachedMachine !== null) return cachedMachine;
  let name = '';
  if (isShell()) {
    try {
      name = (await invoke<string | null>('system_hostname')) ?? '';
    } catch {
      /* fall through to the platform label */
    }
  }
  if (name.trim() === '') name = platformLabel();
  cachedMachine = name;
  return name;
}

export interface VaultStatus {
  exists: boolean; // a vault blob is present at the hub
  version: number; // hub version (0 if none)
  hasLocalKey: boolean; // this device holds the vault key
  enrolled: boolean; // this device is enrolled
  updatedAt: string | null; // hub's last-push time (ISO), null when no vault
  lastDevice: string | null; // machine that last pushed, null when unknown
  thisDevice: string; // this machine's name (hostname or platform label)
}

/// Query key for the vault status, shared by the Settings panel (which reads it)
/// and the app shell (which prefetches it on connect). Scoped by team so a hub
/// switch doesn't show stale status. Prefetching hides the OS-keychain latency
/// that otherwise makes the status "pop in" a beat after Settings opens.
export function vaultStatusKey(client: HubClient | null): readonly [string, string] {
  return ['vault-status', client !== null ? client.transport.teamId : 'none'] as const;
}

export async function vaultStatus(client: HubClient): Promise<VaultStatus> {
  const local = loadVaultState();
  const hasLocalKey = (await secretGet(KEY_VAULT)) !== null;
  const thisDevice = await machineName();
  try {
    const v = await client.getVault();
    return {
      exists: true,
      version: num(v, 'version') ?? local.version,
      hasLocalKey,
      enrolled: local.enrolled,
      updatedAt: str(v, 'updated_at') ?? null,
      lastDevice: str(v, 'last_device') ?? null,
      thisDevice,
    };
  } catch (e) {
    if (e instanceof HubApiError && e.status === 404) {
      return { exists: false, version: 0, hasLocalKey, enrolled: false, updatedAt: null, lastDevice: null, thisDevice };
    }
    throw e;
  }
}

/** Enroll this device's public key (wrapping the vault key to itself so a later
 * pull can recover it from the device row). */
async function enrollThisDevice(client: HubClient, vaultKey: string): Promise<string> {
  const { public_key, seed } = await vaultGenerateDevice();
  await secretSet(KEY_SEED, seed);
  const deviceId = randomDeviceId();
  const wrapped = await vaultWrapForDevice(vaultKey, public_key);
  await client.putVaultDevice(deviceId, { device_name: await machineName(), public_key, wrapped_key: wrapped });
  return deviceId;
}

/** Create a brand-new vault from this device's current data. Returns the
 * one-time recovery code to show the user. */
export async function createVault(client: HubClient, hint?: string): Promise<string> {
  const vaultKey = await vaultGenerateKey();
  const bundle = JSON.stringify(await assembleBundle());
  const ciphertext = await vaultSeal(vaultKey, bundle);
  const res = await client.putVault(ciphertext, 0, await machineName());
  const version = num(res, 'version') ?? 1;

  const deviceId = await enrollThisDevice(client, vaultKey);

  const code = await vaultGenerateRecoveryCode();
  const recoveryEnvelope = await vaultWrapForRecovery(vaultKey, code);
  await client.setVaultRecovery(recoveryEnvelope, hint);

  await secretSet(KEY_VAULT, vaultKey);
  saveVaultState({ version, deviceId, enrolled: true });
  return code;
}

export interface VaultSyncReview {
  expectedVersion: number;
  expectedLocalFingerprint: string;
  resolutions: VaultResolutions;
}

/** Merge the current Hub snapshot with local data, then seal and push the
 * resolved bundle. Both reviewed inputs are checked before the atomic PUT, so
 * no stale or unreviewed ciphertext is submitted. */
export async function syncUp(client: HubClient, review?: VaultSyncReview): Promise<number> {
  const vaultKey = await secretGet(KEY_VAULT);
  if (vaultKey === null) throw new VaultError('noKey');
  const remote = await openRemoteBundle(client, vaultKey);
  if (review !== undefined && remote.version !== review.expectedVersion) throw new VaultError('stalePreview');
  const local = await assembleBundle();
  if (
    review !== undefined
    && await localBundleFingerprint(local, vaultKey) !== review.expectedLocalFingerprint
  ) {
    throw new VaultError('stalePreview');
  }
  const merged = mergeVaultBundles(local, remote.bundle, review?.resolutions, 'up');
  const ciphertext = await vaultSeal(vaultKey, JSON.stringify(merged.bundle));
  const state = loadVaultState();
  try {
    const res = await client.putVault(ciphertext, remote.version, await machineName());
    const version = num(res, 'version') ?? remote.version + 1;
    // The upload is also a bidirectional reconciliation: retain the exact
    // merged snapshot locally so Hub-only entries become immediately usable.
    await importBundle(merged.bundle);
    // Advance local version state only after the merged data was imported. If
    // that write fails, the device must not claim it fully applied this version.
    saveVaultState({ ...state, version });
    return version;
  } catch (e) {
    if (e instanceof HubApiError && e.status === 409) {
      throw new VaultError('conflict');
    }
    throw e;
  }
}

export interface VaultSyncPreview {
  direction: VaultSyncDirection;
  version: number;
  localFingerprint: string;
  updatedAt: string | null;
  lastDevice: string | null;
  changes: VaultChange[];
}

async function localBundleFingerprint(
  bundle: Awaited<ReturnType<typeof assembleBundle>>,
  vaultKey: string,
): Promise<string> {
  const hmacKey = await crypto.subtle.importKey(
    'raw',
    new TextEncoder().encode(vaultKey),
    { name: 'HMAC', hash: 'SHA-256' },
    false,
    ['sign'],
  );
  const digest = await crypto.subtle.sign(
    'HMAC',
    hmacKey,
    new TextEncoder().encode(canonicalVaultValue(vaultReviewProjection(bundle))),
  );
  return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, '0')).join('');
}

async function openRemoteBundle(client: HubClient, vaultKey: string): Promise<{
  version: number;
  updatedAt: string | null;
  lastDevice: string | null;
  bundle: ReturnType<typeof parseBundle>;
}> {
  const v = await client.getVault();
  const ciphertext = str(v, 'ciphertext');
  if (ciphertext === undefined) throw new VaultError('empty');
  const plaintext = await vaultOpen(vaultKey, ciphertext);
  return {
    version: num(v, 'version') ?? 0,
    updatedAt: str(v, 'updated_at') ?? null,
    lastDevice: str(v, 'last_device') ?? null,
    bundle: parseBundle(plaintext),
  };
}

/** Decrypt and compare the hub vault without mutating local state. The result
 * is secret-free and safe to hold in React state for the review dialog. */
async function previewSync(client: HubClient, direction: VaultSyncDirection): Promise<VaultSyncPreview> {
  const vaultKey = await secretGet(KEY_VAULT);
  if (vaultKey === null) throw new VaultError('noKey');
  const remote = await openRemoteBundle(client, vaultKey);
  const local = await assembleBundle();
  const { changes } = mergeVaultBundles(local, remote.bundle, {}, direction);
  return {
    direction,
    version: remote.version,
    localFingerprint: await localBundleFingerprint(local, vaultKey),
    updatedAt: remote.updatedAt,
    lastDevice: remote.lastDevice,
    changes,
  };
}

/** Compare local and Hub data without mutating either side before sync-up. */
export function previewSyncUp(client: HubClient): Promise<VaultSyncPreview> {
  return previewSync(client, 'up');
}

/** Compare local and Hub data without mutating either side before sync-down. */
export function previewSyncDown(client: HubClient): Promise<VaultSyncPreview> {
  return previewSync(client, 'down');
}

/** Pull and merge the hub vault. The conservative defaults preserve one-sided
 * records and use trustworthy edit clocks, while reviewed resolutions can
 * explicitly choose either side (including an absent side to delete locally).
 * Both snapshots are pinned so apply cannot act on unreviewed data. */
export async function syncDown(
  client: HubClient,
  review?: VaultSyncReview,
): Promise<number> {
  const vaultKey = await secretGet(KEY_VAULT);
  if (vaultKey === null) throw new VaultError('noKey');
  const remote = await openRemoteBundle(client, vaultKey);
  if (review !== undefined && remote.version !== review.expectedVersion) throw new VaultError('stalePreview');
  const local = await assembleBundle();
  if (
    review !== undefined
    && await localBundleFingerprint(local, vaultKey) !== review.expectedLocalFingerprint
  ) {
    throw new VaultError('stalePreview');
  }
  const merged = mergeVaultBundles(local, remote.bundle, review?.resolutions, 'down');
  await importBundle(merged.bundle);
  const state = loadVaultState();
  saveVaultState({ ...state, version: remote.version });
  return remote.version;
}

/** Restore a vault onto this device from a recovery code: unwrap the vault key,
 * open + import the bundle, store the key, and enroll this device for future
 * syncs. */
export async function restoreWithRecovery(client: HubClient, code: string): Promise<void> {
  const v = await client.getVault();
  const ciphertext = str(v, 'ciphertext');
  if (ciphertext === undefined) throw new VaultError('empty');
  const rec = await client.getVaultRecovery();
  const envelope = str(rec, 'recovery_envelope');
  if (envelope === undefined) throw new VaultError('noRecovery');

  const vaultKey = await vaultUnwrapRecovery(code, envelope);
  const bundle = await vaultOpen(vaultKey, ciphertext);
  await importBundle(parseBundle(bundle));

  await secretSet(KEY_VAULT, vaultKey);
  const deviceId = await enrollThisDevice(client, vaultKey);
  saveVaultState({ version: num(v, 'version') ?? 0, deviceId, enrolled: true });
}

/** Forget this device's vault material (leaves the hub vault untouched). */
export async function forgetLocalVault(): Promise<void> {
  await secretDeleteMany([KEY_VAULT, KEY_SEED]);
  saveVaultState({ version: 0, deviceId: null, enrolled: false });
}
