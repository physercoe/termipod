import { listConnections, type Connection } from '../state/connections';
import { listKeys, type SshKeyMeta } from '../state/keys';
import { loadJson, saveJson, secretGet, secretSet } from '../state/persist';

/// The plaintext vault bundle (parity Phase 2b). Shape matches the mobile
/// `_assembleBundle` (vault_service.dart) so a desktop and a phone seal/open the
/// same JSON: connections array + an sshKeys object (meta + PEMs + passphrases)
/// + a flat passwords map. Secrets are gathered from the OS keychain here and
/// re-scattered back to it on import.

export interface VaultBundle {
  connections: Connection[];
  sshKeys: {
    meta: SshKeyMeta[];
    privateKeys: Record<string, string>;
    passphrases: Record<string, string>;
  };
  passwords: Record<string, string>;
}

export async function assembleBundle(): Promise<VaultBundle> {
  const connections = listConnections();
  const meta = listKeys();

  const privateKeys: Record<string, string> = {};
  const passphrases: Record<string, string> = {};
  for (const k of meta) {
    const pk = await secretGet(`privatekey_${k.id}`);
    if (pk !== null) privateKeys[k.id] = pk;
    const pass = await secretGet(`passphrase_${k.id}`);
    if (pass !== null) passphrases[k.id] = pass;
  }

  const passwords: Record<string, string> = {};
  for (const c of connections) {
    const pw = await secretGet(`password_${c.id}`);
    if (pw !== null) passwords[c.id] = pw;
    const jump = await secretGet(`password_${c.id}_jump`);
    if (jump !== null) passwords[`${c.id}_jump`] = jump;
  }

  return { connections, sshKeys: { meta, privateKeys, passphrases }, passwords };
}

/** Merge a decrypted bundle into local storage + the keychain (restore/sync
 * down). Overwrites the connection and key lists wholesale — the vault is the
 * source of truth on a pull. */
export async function importBundle(bundle: VaultBundle): Promise<void> {
  if (Array.isArray(bundle.connections)) saveJson('connections', bundle.connections);
  const ssh = bundle.sshKeys;
  if (ssh !== undefined && ssh !== null) {
    if (Array.isArray(ssh.meta)) saveJson('ssh_keys_meta', ssh.meta);
    for (const [id, pem] of Object.entries(ssh.privateKeys ?? {})) await secretSet(`privatekey_${id}`, pem);
    for (const [id, pass] of Object.entries(ssh.passphrases ?? {})) await secretSet(`passphrase_${id}`, pass);
  }
  for (const [k, v] of Object.entries(bundle.passwords ?? {})) await secretSet(`password_${k}`, v);
}

export function readBundleJson(): Promise<string> {
  return assembleBundle().then((b) => JSON.stringify(b));
}

export function parseBundle(json: string): VaultBundle {
  const b = JSON.parse(json) as Partial<VaultBundle>;
  return {
    connections: Array.isArray(b.connections) ? b.connections : [],
    sshKeys: {
      meta: Array.isArray(b.sshKeys?.meta) ? b.sshKeys!.meta : [],
      privateKeys: b.sshKeys?.privateKeys ?? {},
      passphrases: b.sshKeys?.passphrases ?? {},
    },
    passwords: b.passwords ?? {},
  };
}

/** Non-secret local state that reflects vault identity/version (kept in
 * localStorage; the actual vault key + device seed live in the keychain). */
export interface VaultLocalState {
  version: number;
  deviceId: string | null;
  enrolled: boolean;
}

export function loadVaultState(): VaultLocalState {
  return loadJson<VaultLocalState>('vault_state', { version: 0, deviceId: null, enrolled: false });
}
export function saveVaultState(s: VaultLocalState): void {
  saveJson('vault_state', s);
}
