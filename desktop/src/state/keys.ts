import { invoke } from '@tauri-apps/api/core';
import { isTauri } from '../platform';
import { loadJson, newId, saveJson, secretDeleteMany, secretGet, secretSet } from './persist';

/// SSH key store (parity Phase 2a). `SshKeyMeta` mirrors the mobile
/// lib/providers/key_provider.dart key-for-key (vault-bundle parity). The
/// private key PEM lives in the OS keychain under `privatekey_<id>` and its
/// passphrase under `passphrase_<id>` — the mobile secure-storage patterns.
/// Import validates + introspects the PEM via the Rust `ssh_parse_key`.

export interface SshKeyMeta {
  id: string;
  name: string;
  type: string; // 'ed25519' | 'rsa-2048' | 'rsa-3072' | 'rsa-4096'
  publicKey: string | null;
  fingerprint: string | null;
  hasPassphrase: boolean;
  createdAt: string; // ISO-8601
  comment: string | null;
  source: 'generated' | 'imported';
}

const STORAGE_KEY = 'ssh_keys_meta';

function pkKey(id: string): string {
  return `privatekey_${id}`;
}
function passKey(id: string): string {
  return `passphrase_${id}`;
}

export function listKeys(): SshKeyMeta[] {
  return loadJson<SshKeyMeta[]>(STORAGE_KEY, []);
}

function persist(list: SshKeyMeta[]): void {
  saveJson(STORAGE_KEY, list);
}

interface ParsedKey {
  algorithm: string;
  public_openssh: string;
}

/** Map the OpenSSH algorithm name to the mobile `type` label. RSA bit-length
 * isn't recoverable from the parse, so — like the mobile importer — default to
 * rsa-4096. */
function typeFromAlgorithm(algo: string): string {
  if (algo.includes('ed25519')) return 'ed25519';
  if (algo.includes('rsa')) return 'rsa-4096';
  return algo;
}

/** Import a pasted private key: validate/introspect it, then store the meta +
 * secrets. Throws with the parse error if the key (or passphrase) is bad. */
export async function importKey(opts: {
  name: string;
  pem: string;
  passphrase?: string;
  comment?: string;
}): Promise<SshKeyMeta> {
  const passphrase = opts.passphrase ?? '';
  let algorithm = 'unknown';
  let publicKey: string | null = null;
  if (isTauri()) {
    const parsed = await invoke<ParsedKey>('ssh_parse_key', {
      pem: opts.pem,
      passphrase: passphrase !== '' ? passphrase : null,
    });
    algorithm = parsed.algorithm;
    publicKey = parsed.public_openssh;
  }
  const id = newId();
  const meta: SshKeyMeta = {
    id,
    name: opts.name,
    type: typeFromAlgorithm(algorithm),
    publicKey,
    fingerprint: null,
    hasPassphrase: passphrase !== '',
    createdAt: new Date().toISOString(),
    comment: opts.comment ?? null,
    source: 'imported',
  };
  await secretSet(pkKey(id), opts.pem);
  if (passphrase !== '') await secretSet(passKey(id), passphrase);
  persist([...listKeys(), meta]);
  return meta;
}

export async function deleteKey(id: string): Promise<void> {
  persist(listKeys().filter((k) => k.id !== id));
  await secretDeleteMany([pkKey(id), passKey(id)]);
}

/** The private-key PEM + passphrase for a saved key, for the connect flow. */
export async function getKeyMaterial(id: string): Promise<{ pem: string | null; passphrase: string | null }> {
  const pem = await secretGet(pkKey(id));
  const passphrase = await secretGet(passKey(id));
  return { pem, passphrase };
}
