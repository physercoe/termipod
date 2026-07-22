/// Zero-knowledge vault crypto (ADR-055 M2.6b / D-3) — the Electron handler that
/// registers the nine `vault_*` commands, backed by the WASM build of the vault
/// crypto (`desktop/vault-wasm`, wasm-bindgen over the pure `vault-core`). Same
/// command names + string signatures as the Tauri commands, so `src/vault/
/// crypto.ts` drives them unchanged through the bridge. The crypto is COMPILED,
/// not reimplemented (D-3): the desktop seals/opens the SAME bundle the phone and
/// the Tauri build do, byte-for-byte.
///
/// The WASM module is the `wasm-pack build --target nodejs` output (a CJS module
/// that reads its `.wasm` synchronously). It is loaded lazily via a
/// computed-path dynamic import — so esbuild leaves it alone (the artifact is not
/// present at bundle time; it is built by the `vault-wasm` CI job and plumbed in
/// by M3 packaging) and a load failure surfaces as a rejected invoke rather than
/// a boot crash. `TERMIPOD_VAULT_WASM` overrides the path for packaging.
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import type { Handler } from './dispatch';

/// The wasm-bindgen exports (nodejs target). Loaded via a computed path, so this
/// is `any` at the type layer — the boundary is all strings and a thrown JsError
/// on failure (which propagates as the invoke rejection).
interface VaultWasm {
  vault_generate_key(): string;
  vault_seal(key: string, plaintext: string): string;
  vault_open(key: string, ciphertext: string): string;
  vault_generate_device(): string; // JSON: {"public_key","seed"}
  vault_wrap_for_device(key: string, devicePublic: string): string;
  vault_unwrap_device(seed: string, envelope: string): string;
  vault_wrap_for_recovery(key: string, code: string): string;
  vault_unwrap_recovery(code: string, envelope: string): string;
  vault_generate_recovery_code(): string;
}

// Resolved lazily (not a module const) so a packaged build's
// `TERMIPOD_VAULT_WASM`, which main.ts sets in `whenReady`, is honoured — a const
// would capture the env before that runs. Dev falls back to the sibling crate's
// wasm-pack output.
function wasmPath(): string {
  return (
    process.env.TERMIPOD_VAULT_WASM ??
    path.join(__dirname, '..', '..', 'vault-wasm', 'pkg', 'vault_wasm.js')
  );
}

let wasmP: Promise<VaultWasm> | null = null;
function loadVault(): Promise<VaultWasm> {
  // Computed-path dynamic import: opaque to esbuild, resolved at runtime. The
  // path MUST be turned into a file:// URL — Node's ESM loader rejects a bare
  // absolute path, and on Windows reads the drive letter as a URL scheme
  // (`import('D:\\…')` → ERR_UNSUPPORTED_ESM_URL_SCHEME, "protocol 'd:'"). Any
  // `vault_*` invoke (e.g. sync-down decrypt) is the first to trip it.
  if (wasmP === null) {
    wasmP = import(pathToFileURL(wasmPath()).href).then((m) => (m.default ?? m) as VaultWasm);
  }
  return wasmP;
}

const s = (v: unknown): string => String(v ?? '');

export const vaultHandlers: Record<string, Handler> = {
  vault_generate_key: async (): Promise<string> => (await loadVault()).vault_generate_key(),

  vault_seal: async (args): Promise<string> => (await loadVault()).vault_seal(s(args.key), s(args.plaintext)),

  vault_open: async (args): Promise<string> => (await loadVault()).vault_open(s(args.key), s(args.ciphertext)),

  // The WASM returns a JSON string; hand back the parsed { public_key, seed } the
  // frontend's DeviceKeys expects (matching the Tauri command's serde output).
  vault_generate_device: async (): Promise<unknown> => JSON.parse((await loadVault()).vault_generate_device()),

  vault_wrap_for_device: async (args): Promise<string> =>
    (await loadVault()).vault_wrap_for_device(s(args.key), s(args.devicePublic)),

  vault_unwrap_device: async (args): Promise<string> =>
    (await loadVault()).vault_unwrap_device(s(args.seed), s(args.envelope)),

  vault_wrap_for_recovery: async (args): Promise<string> =>
    (await loadVault()).vault_wrap_for_recovery(s(args.key), s(args.code)),

  vault_unwrap_recovery: async (args): Promise<string> =>
    (await loadVault()).vault_unwrap_recovery(s(args.code), s(args.envelope)),

  vault_generate_recovery_code: async (): Promise<string> => (await loadVault()).vault_generate_recovery_code(),
};
