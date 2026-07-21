/// Migration state read/write command family (ADR-055 M1) — the Electron
/// equivalent of `src-tauri/src/migration.rs`. Same command names
/// (`migration_read` / `migration_export`) and JSON contract, so
/// `src/migration/state.ts` drives them unchanged through the bridge.
///
/// The renderer's boot flow (`importStateIfFresh` before render, then
/// `armExport`) snapshots the migration-covered localStorage keys to
/// `<user-data>/migration/state-v1.json` and restores them into a fresh
/// Chromium profile. `migration_read` first looks at Electron's own `userData`
/// copy, then falls back to the **Tauri** install's app-data dir — the
/// one-time cross-install handoff (plan §5), pulled forward from M3 because it
/// is a read-only fallback and device-testable today (#353). The restore
/// imports into localStorage and the next `migration_export` writes Electron's
/// own copy, so the legacy path is consulted at most once.
import { app } from 'electron';
import os from 'node:os';
import path from 'node:path';
import { mkdir, readFile, rename, writeFile } from 'node:fs/promises';
import type { Handler } from './dispatch';

function stateDir(): string {
  return path.join(app.getPath('userData'), 'migration');
}
function statePath(): string {
  return path.join(stateDir(), 'state-v1.json');
}

/// Where the Tauri shell's egress (M0.2) writes, per platform — the Tauri
/// app-data dir keyed by the bundle identifier (`tauri.conf.json`). Mirrors
/// Tauri's `app_data_dir`: macOS `~/Library/Application Support/<id>`,
/// Windows `%APPDATA%\<id>`, Linux `${XDG_DATA_HOME:-~/.local/share}/<id>`.
/// (Electron's own `appData` would be wrong on Linux — it is XDG_CONFIG_HOME.)
function legacyStatePaths(): string[] {
  const id = 'app.termipod.desktop';
  if (process.platform === 'linux') {
    const dataHome = process.env.XDG_DATA_HOME ?? path.join(os.homedir(), '.local', 'share');
    return [path.join(dataHome, id, 'migration', 'state-v1.json')];
  }
  return [path.join(app.getPath('appData'), id, 'migration', 'state-v1.json')];
}

/// Crash-safe write: temp file + atomic rename, matching the Rust command.
async function writeAtomic(json: string): Promise<void> {
  const dir = stateDir();
  await mkdir(dir, { recursive: true });
  const finalPath = statePath();
  const tmp = `${finalPath}.tmp`;
  await writeFile(tmp, json, 'utf8');
  await rename(tmp, finalPath);
}

export const migrationHandlers: Record<string, Handler> = {
  migration_export: async (args: Record<string, unknown>) => {
    const json = typeof args.json === 'string' ? args.json : '';
    if (json === '') return;
    await writeAtomic(json);
  },

  migration_read: async (): Promise<string | null> => {
    for (const candidate of [statePath(), ...legacyStatePaths()]) {
      try {
        return await readFile(candidate, 'utf8');
      } catch {
        /* absent — try the next candidate */
      }
    }
    return null; // no snapshot anywhere — a genuine first boot
  },
};
