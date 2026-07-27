import { invoke } from '../bridge';

/// Thin typed bridge to the Rust vault crypto (parity Phase 2b). Every call is
/// desktop-only (the browser build has no native core); the Vault surface gates
/// on isShell(). Byte shapes match the mobile vault_crypto.dart — see
/// src-tauri/src/vault.rs.

export interface DeviceKeys {
  public_key: string;
  seed: string;
}

export const vaultGenerateKey = (): Promise<string> => invoke<string>('vault_generate_key');

export const vaultSeal = (key: string, plaintext: string): Promise<string> =>
  invoke<string>('vault_seal', { key, plaintext });

export const vaultOpen = (key: string, ciphertext: string): Promise<string> =>
  invoke<string>('vault_open', { key, ciphertext });

export const vaultGenerateDevice = (): Promise<DeviceKeys> => invoke<DeviceKeys>('vault_generate_device');

export const vaultWrapForDevice = (key: string, devicePublic: string): Promise<string> =>
  invoke<string>('vault_wrap_for_device', { key, devicePublic });

export const vaultUnwrapDevice = (seed: string, envelope: string): Promise<string> =>
  invoke<string>('vault_unwrap_device', { seed, envelope });

export const vaultWrapForRecovery = (key: string, code: string): Promise<string> =>
  invoke<string>('vault_wrap_for_recovery', { key, code });

export const vaultUnwrapRecovery = (code: string, envelope: string): Promise<string> =>
  invoke<string>('vault_unwrap_recovery', { code, envelope });

export const vaultGenerateRecoveryCode = (): Promise<string> => invoke<string>('vault_generate_recovery_code');

// ===== env-secret envelope (ADR-056 D-3) =====

/// Seal resolved env-profile secrets to a target host's X25519 public key
/// (ADR-056 D-3), returning the envelope JSON stored on the spawn row. The
/// plaintext is canonicalized here — sorted keys, compact JSON — to match Go's
/// `json.Marshal(map[string]string)` (the host OPEN side) and the Dart
/// `SplayTreeMap` path, so all three implementations seal byte-identical
/// plaintext (the KAT interop contract). `hostPub` is the base64 X25519 key from
/// the target host's `capabilities.host_pubkey`.
export const vaultSealEnvSecret = (
  hostPub: string,
  teamId: string,
  hostId: string,
  profileId: string,
  secrets: Record<string, string>,
): Promise<string> => {
  const sorted: Record<string, string> = {};
  for (const k of Object.keys(secrets).sort()) sorted[k] = secrets[k];
  const plaintext = JSON.stringify(sorted);
  return invoke<string>('vault_seal_env_secret', { hostPub, teamId, hostId, profileId, plaintext });
};

/// The host-key trust short code (ADR-056 D-2): base32(SHA-256(pubkey)[:10]) in
/// four dash-separated groups of four. Shown in the trust dialog for the operator
/// to compare against the host console banner before secrets are sealed to it.
/// Identical to the Go (`envseal.Fingerprint`) and Dart (`VaultCrypto
/// .envFingerprint`) implementations.
export const vaultEnvFingerprint = (hostPub: string): Promise<string> =>
  invoke<string>('vault_env_fingerprint', { hostPub });
