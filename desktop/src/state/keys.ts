import { invoke } from '@tauri-apps/api/core';
import { isTauri } from '../platform';
import { loadJson, newId, saveJson, secretDeleteMany, secretGet, secretSetMany } from './persist';

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
  fingerprint: string;
}

/// A keypair generated in the Rust core (`ssh_generate_key`, #320): the OpenSSH
/// PEM (passphrase-encrypted when one was given) plus its public half and
/// SHA-256 fingerprint. The PEM is stored in the OS keychain exactly like an
/// imported key.
interface GeneratedKey {
  algorithm: string;
  public_openssh: string;
  fingerprint: string;
  pem: string;
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
  let fingerprint: string | null = null;
  if (isTauri()) {
    const parsed = await invoke<ParsedKey>('ssh_parse_key', {
      pem: opts.pem,
      passphrase: passphrase !== '' ? passphrase : null,
    });
    algorithm = parsed.algorithm;
    publicKey = parsed.public_openssh;
    fingerprint = parsed.fingerprint;
  }
  const id = newId();
  const meta: SshKeyMeta = {
    id,
    name: opts.name,
    type: typeFromAlgorithm(algorithm),
    publicKey,
    fingerprint,
    hasPassphrase: passphrase !== '',
    createdAt: new Date().toISOString(),
    comment: opts.comment ?? null,
    source: 'imported',
  };
  // One batched write: the consolidated keychain store re-seals the whole
  // document per flush, so two sequential secretSet calls would prompt twice
  // on macOS (#320 review).
  await secretSetMany({ [pkKey(id)]: opts.pem, ...(passphrase !== '' && { [passKey(id)]: passphrase }) });
  persist([...listKeys(), meta]);
  return meta;
}

/// Generate an ed25519 keypair in-app (#320): the Rust core does the keygen and
/// hands back the PEM + public key + fingerprint; storage then mirrors the
/// import path key-for-key (PEM and passphrase into the OS keychain).
export async function generateKey(opts: {
  name: string;
  passphrase?: string;
  comment?: string;
}): Promise<SshKeyMeta> {
  const passphrase = opts.passphrase ?? '';
  if (!isTauri()) throw new Error('key generation requires the desktop app');
  const gen = await invoke<GeneratedKey>('ssh_generate_key', {
    passphrase: passphrase !== '' ? passphrase : null,
  });
  const id = newId();
  const meta: SshKeyMeta = {
    id,
    name: opts.name,
    type: typeFromAlgorithm(gen.algorithm),
    publicKey: gen.public_openssh,
    fingerprint: gen.fingerprint,
    hasPassphrase: passphrase !== '',
    createdAt: new Date().toISOString(),
    comment: opts.comment ?? null,
    source: 'generated',
  };
  // Batched like importKey above — one keychain flush, one prompt.
  await secretSetMany({ [pkKey(id)]: gen.pem, ...(passphrase !== '' && { [passKey(id)]: passphrase }) });
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
