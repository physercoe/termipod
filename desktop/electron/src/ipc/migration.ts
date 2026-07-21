/// Migration state read/write command family (ADR-055 M1) — the Electron
/// equivalent of `src-tauri/src/migration.rs`. Same command names
/// (`migration_read` / `migration_export`) and JSON contract, so
/// `src/migration/state.ts` drives them unchanged through the bridge.
///
/// The renderer's boot flow (`importStateIfFresh` before render, then
/// `armExport`) snapshots every `termipod.*` localStorage key to
/// `<user-data>/migration/state-v1.json` and restores it into a fresh Chromium
/// profile. Here that path is Electron's own `userData` — enough for dual-shell
/// parity testing (plan §8). The one-time cross-install handoff (reading the
/// **Tauri** app-data dir the old shell wrote to) is an M3 cutover concern
/// (plan §5) and is deliberately not wired yet.
import { app } from 'electron';
import path from 'node:path';
import { mkdir, readFile, rename, writeFile } from 'node:fs/promises';
import type { Handler } from './dispatch';

function stateDir(): string {
  return path.join(app.getPath('userData'), 'migration');
}
function statePath(): string {
  return path.join(stateDir(), 'state-v1.json');
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
    try {
      return await readFile(statePath(), 'utf8');
    } catch {
      return null; // absent on a first boot — the common case
    }
  },
};
