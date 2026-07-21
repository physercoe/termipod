/// Secret storage (ADR-055 M1.3) — the Electron replacement for
/// `src-tauri/src/keychain.rs`. Same command names (`keychain_get` / `set` /
/// `delete` / `keychain_is_windows`), so `src/state/persist.ts` drives it
/// unchanged through the bridge.
///
/// ENGINE = Electron `safeStorage`: values are encrypted with an OS-protected key
/// and the ciphertext is kept in ONE app-data file. This deletes two costs the
/// Tauri keyring imposed (plan §7 row 8): the per-item macOS auth prompt (there
/// is no OS keychain item per secret — persist.ts already consolidates into one
/// document, and even that is now a file entry) and the Windows Credential
/// Manager 2560-byte cap (a file has no cap, so `keychain_is_windows` returns
/// false and persist.ts never chunks).
///
/// ONE-TIME READER = `@napi-rs/keyring`: on first boot we read the existing
/// consolidated secret document (`secretstore.v1`, reassembling the Windows
/// `#i` chunks) out of the OS keychain the Tauri build wrote to, and fold it into
/// the safeStorage store — so an upgrading user keeps their SSH keys / passphrases
/// / vault material with no re-auth. If the read fails (native module or store
/// unavailable, or macOS denies a cross-app read), migration is skipped and the
/// user re-enters secrets — a graceful degrade, not a crash.
import { app, safeStorage } from 'electron';
import path from 'node:path';
import { mkdir, readFile, rename, writeFile } from 'node:fs/promises';
import type { Handler } from './dispatch';

const SERVICE = 'app.termipod.desktop'; // must match keychain.rs SERVICE
const CONSOLIDATED_KEY = 'secretstore.v1'; // must match persist.ts STORE_KEY
const DOC_TAG = ' doc:'; // persist.ts chunk manifest sentinel

function storeDir(): string {
  return path.join(app.getPath('userData'), 'secrets');
}
function storePath(): string {
  return path.join(storeDir(), 'secretstore-v1.json');
}

// key → base64(safeStorage ciphertext). Loaded once, authoritative for the run.
let store: Record<string, string> | null = null;

async function loadStore(): Promise<Record<string, string>> {
  if (store !== null) return store;
  try {
    store = JSON.parse(await readFile(storePath(), 'utf8')) as Record<string, string>;
  } catch {
    store = {};
  }
  return store;
}

async function saveStore(): Promise<void> {
  const dir = storeDir();
  await mkdir(dir, { recursive: true });
  const finalPath = storePath();
  const tmp = `${finalPath}.tmp`;
  await writeFile(tmp, JSON.stringify(store ?? {}), 'utf8');
  await rename(tmp, finalPath);
}

function encrypt(value: string): string {
  return safeStorage.encryptString(value).toString('base64');
}
function decrypt(b64: string): string {
  return safeStorage.decryptString(Buffer.from(b64, 'base64'));
}

// ── one-time migration from the Tauri OS-keychain document ──────────────────
let migrationP: Promise<void> | null = null;

/// Kick off the one-time read of the Tauri-written secret document. Fire-and-
/// forget from `whenReady`; the command handlers await it before serving, so the
/// window paints immediately and only the first secret access waits (behind a
/// possible one-time macOS prompt).
export function startKeychainMigration(): void {
  if (migrationP === null) migrationP = migrateOnce();
}

async function migrateOnce(): Promise<void> {
  const s = await loadStore();
  if (s[CONSOLIDATED_KEY] !== undefined) return; // already migrated / has data
  try {
    // Dynamic + external so a native-module load failure can't crash startup.
    const { AsyncEntry } = await import('@napi-rs/keyring');
    const read = async (account: string): Promise<string | null> => {
      try {
        return (await new AsyncEntry(SERVICE, account).getPassword()) ?? null;
      } catch {
        return null;
      }
    };
    const head = await read(CONSOLIDATED_KEY);
    if (head === null) return; // nothing to migrate

    let doc = head;
    if (head.startsWith(DOC_TAG)) {
      // Windows: reassemble the `secretstore.v1#i` chunks into the document.
      const n = Number.parseInt(head.slice(DOC_TAG.length), 10);
      if (!Number.isFinite(n)) return;
      let out = '';
      for (let i = 0; i < n; i += 1) {
        const part = await read(`${CONSOLIDATED_KEY}#${i}`);
        if (part === null) return; // partial — leave unmigrated
        out += part;
      }
      doc = out;
    }
    s[CONSOLIDATED_KEY] = encrypt(doc);
    await saveStore();
  } catch {
    /* @napi-rs/keyring unavailable — user re-enters secrets (graceful degrade) */
  }
}

async function ready(): Promise<Record<string, string>> {
  if (migrationP !== null) await migrationP;
  return loadStore();
}

export const keychainHandlers: Record<string, Handler> = {
  // File store, no Credential Manager byte cap → persist.ts must not chunk.
  keychain_is_windows: () => false,

  keychain_get: async (args): Promise<string | null> => {
    const key = String(args.key ?? '');
    const s = await ready();
    const b = s[key];
    return b === undefined ? null : decrypt(b);
  },

  keychain_set: async (args): Promise<void> => {
    const key = String(args.key ?? '');
    const value = String(args.value ?? '');
    const s = await ready();
    s[key] = encrypt(value);
    await saveStore();
  },

  keychain_delete: async (args): Promise<void> => {
    const key = String(args.key ?? '');
    const s = await ready();
    if (key in s) {
      delete s[key];
      await saveStore();
    }
  },
};
