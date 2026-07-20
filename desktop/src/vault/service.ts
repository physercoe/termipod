import type { HubClient } from '../hub/client';
import { HubApiError } from '../hub/errors';
import { num, str } from '../hub/types';
import { isTauri } from '../platform';
import { secretDeleteMany, secretGet, secretSet } from '../state/persist';
import { assembleBundle, importBundle, loadVaultState, parseBundle, saveVaultState } from './bundle';
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
export type VaultErrorCode = 'noKey' | 'conflict' | 'empty' | 'noRecovery';

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
  if (isTauri()) {
    try {
      const { invoke } = await import('@tauri-apps/api/core');
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

/** Seal the current local data and push it up (optimistic-locked on the known
 * version). Throws a coded VaultError on a missing key or 409 version conflict. */
export async function syncUp(client: HubClient): Promise<number> {
  const vaultKey = await secretGet(KEY_VAULT);
  if (vaultKey === null) throw new VaultError('noKey');
  const bundle = JSON.stringify(await assembleBundle());
  const ciphertext = await vaultSeal(vaultKey, bundle);
  const state = loadVaultState();
  try {
    const res = await client.putVault(ciphertext, state.version, await machineName());
    const version = num(res, 'version') ?? state.version + 1;
    saveVaultState({ ...state, version });
    return version;
  } catch (e) {
    if (e instanceof HubApiError && e.status === 409) {
      throw new VaultError('conflict');
    }
    throw e;
  }
}

/** Pull the hub vault and merge it locally using the locally-held vault key. */
export async function syncDown(client: HubClient): Promise<number> {
  const vaultKey = await secretGet(KEY_VAULT);
  if (vaultKey === null) throw new VaultError('noKey');
  const v = await client.getVault();
  const ciphertext = str(v, 'ciphertext');
  if (ciphertext === undefined) throw new VaultError('empty');
  const bundle = await vaultOpen(vaultKey, ciphertext);
  await importBundle(parseBundle(bundle));
  const version = num(v, 'version') ?? 0;
  const state = loadVaultState();
  saveVaultState({ ...state, version });
  return version;
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
