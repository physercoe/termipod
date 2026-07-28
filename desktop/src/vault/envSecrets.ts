import type { Entity } from '../hub/types';
import { obj, str } from '../hub/types';
import { getHostPin, pinHostKey } from '../state/hostPins';
import { getItemSecret } from '../state/vaultItems';
import { vaultEnvFingerprint, vaultSealEnvSecret } from './crypto';

/// Client-side glue that turns an env profile's `secret_refs` into the opaque
/// `env_secret_envelope` a spawn carries (ADR-056 E3b-3): resolve the referenced
/// values from the local vault, confirm the target host key is trusted (D-2),
/// then seal to that key (D-3). The hub only ever sees the ciphertext.

/** A profile secret ref — a pointer into the vault, never a value (matches the
 * hub `secretRef` shape). `vault_item` is `"<itemId>"` (→ the item's `content`
 * secret slot) or `"<itemId>:<slot>"` for an explicit slot. */
export interface SecretRef {
  key: string;
  vault_item: string;
}

export type EnvSecretCode = 'unresolved' | 'noHostKey' | 'untrusted';

/** A coded failure the spawn UI maps to a localized message (the vault layer has
 * no t()). `detail` carries the offending key/host for the message. */
export class EnvSecretError extends Error {
  constructor(
    readonly code: EnvSecretCode,
    readonly detail?: string,
  ) {
    super(code);
    this.name = 'EnvSecretError';
  }
}

/** Read a profile entity's `secret_refs` as a typed list (empty when none). */
export function secretRefsOf(profile: Entity | undefined): SecretRef[] {
  if (profile === undefined) return [];
  const raw = profile.secret_refs;
  if (!Array.isArray(raw)) return [];
  const out: SecretRef[] = [];
  for (const r of raw) {
    if (typeof r !== 'object' || r === null) continue;
    const key = str(r as Entity, 'key');
    const vaultItem = str(r as Entity, 'vault_item');
    if (key !== undefined && key !== '' && vaultItem !== undefined && vaultItem !== '') {
      out.push({ key, vault_item: vaultItem });
    }
  }
  return out;
}

/** Resolve each secret ref to its value from the local vault items. Throws
 * `unresolved` (naming the KEY) when a referenced item/slot holds no value on
 * this device — the caller can't seal a secret it doesn't have. */
export async function resolveSecretRefs(refs: SecretRef[]): Promise<Record<string, string>> {
  const out: Record<string, string> = {};
  for (const ref of refs) {
    const sep = ref.vault_item.indexOf(':');
    const itemId = sep === -1 ? ref.vault_item : ref.vault_item.slice(0, sep);
    const slot = sep === -1 ? 'content' : ref.vault_item.slice(sep + 1);
    const value = await getItemSecret(itemId, slot);
    if (value === '') throw new EnvSecretError('unresolved', ref.key);
    out[ref.key] = value;
  }
  return out;
}

/** The trust state of a target host's env key, for the spawn flow to act on. */
export interface HostKeyInfo {
  hostId: string;
  pubkey: string; // base64 X25519
  fingerprint: string; // the D-2 short code
  trusted: boolean; // a matching pin already exists
  /** Set when a pin exists but the host now advertises a DIFFERENT key: the
   * previously-trusted key's fingerprint. Either a deliberate host re-key
   * (`--rekey`) or a substitution — the re-trust dialog shows both codes and
   * the operator decides. Never sealed to until re-pinned (D-2). */
  changedFrom?: string;
}

/** Classify a host's env key from its capabilities. Throws `noHostKey` when the
 * host advertises none (it can't receive secrets — a headless/old host). A pin
 * that differs from the advertised key comes back as `trusted: false` +
 * `changedFrom` so the spawn flow can offer the deliberate re-trust step D-2
 * requires (a host `--rekey` is legitimate; only the operator can tell it from
 * a substitution, by comparing codes against the host console). */
export async function inspectHostKey(host: Entity, hostId: string): Promise<HostKeyInfo> {
  const caps = obj(host, 'capabilities') ?? {};
  const pubkey = str(caps, 'host_pubkey');
  if (pubkey === undefined || pubkey === '') throw new EnvSecretError('noHostKey', hostId);
  const pinned = getHostPin(hostId);
  const fingerprint = await vaultEnvFingerprint(pubkey);
  if (pinned !== null && pinned !== pubkey) {
    return { hostId, pubkey, fingerprint, trusted: false, changedFrom: await vaultEnvFingerprint(pinned) };
  }
  return { hostId, pubkey, fingerprint, trusted: pinned === pubkey };
}

/** Trust a host key the operator confirmed in the dialog (pins it + syncs on the
 * next vault push). */
export function trustHostKey(hostId: string, pubkey: string): void {
  pinHostKey(hostId, pubkey);
}

/** Seal the resolved secrets to a trusted host key, returning the envelope JSON
 * for the spawn body. The caller must have pinned the key first (via the trust
 * dialog); this re-reads the pin to fail closed if it isn't there. */
export async function sealEnvSecrets(
  secrets: Record<string, string>,
  info: HostKeyInfo,
  teamId: string,
  profileId: string,
): Promise<string> {
  if (getHostPin(info.hostId) !== info.pubkey) throw new EnvSecretError('untrusted', info.hostId);
  return vaultSealEnvSecret(info.pubkey, teamId, info.hostId, profileId, secrets);
}
