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
