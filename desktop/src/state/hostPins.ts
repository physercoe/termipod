import { loadJson, saveJson } from './persist';

/// Pinned host env-keys (ADR-056 D-2). Maps a host id to the base64 X25519
/// public key the operator has explicitly trusted (after comparing its
/// fingerprint short code against the host's console banner). Secrets are only
/// ever sealed to a pinned key; a later key that differs from the pin is a hard
/// mismatch (a possible hub substitution — the hub relays pubkeys but must not be
/// able to make itself a recipient).
///
/// The pins live in localStorage locally, but they are ALSO carried inside the
/// zero-knowledge vault bundle (see vault/bundle.ts) so the trust decision syncs
/// across the director's devices hub-blind — the hub never sees the map.

const KEY = 'host_pins';

/** host_id → base64 X25519 public key. */
export type HostPins = Record<string, string>;

export function listHostPins(): HostPins {
  return loadJson<HostPins>(KEY, {});
}

/** The pinned key for a host, or null when the host has never been trusted. */
export function getHostPin(hostId: string): string | null {
  return listHostPins()[hostId] ?? null;
}

/** Trust a host's key (first sight or a deliberate re-trust after a key change). */
export function pinHostKey(hostId: string, pubkey: string): void {
  const pins = listHostPins();
  pins[hostId] = pubkey;
  saveJson(KEY, pins);
}

export function unpinHostKey(hostId: string): void {
  const pins = listHostPins();
  if (hostId in pins) {
    delete pins[hostId];
    saveJson(KEY, pins);
  }
}

/** Replace the whole pin map (a vault sync-down: the vault is the source of
 * truth for trust decisions across devices). */
export function replaceHostPins(pins: HostPins): void {
  saveJson(KEY, pins);
}
