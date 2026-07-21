/// Migration data egress (ADR-055, plan M0).
///
/// localStorage is bound to the webview profile and will NOT follow the app
/// into Electron's Chromium profile (ADR-055 D-5). So we snapshot every
/// `termipod.*` key — plus the older unprefixed feature keys below — to
/// `<app-data>/migration/state-v1.json` under a native shell; Electron's first
/// boot re-imports it (M1), and it doubles as a free local backup. In the
/// plain browser build every function here is a no-op.
import { invoke, isShell } from '../bridge';

const PREFIX = 'termipod.';
// Feature stores whose keys predate the termipod.* convention and were never
// renamed (renaming would strand existing installs' data). These hold the hub
// connection profile, SSH/vault metadata, and voice settings — exactly the
// state a cross-install handoff must carry or the user re-enters it (#353).
const EXTRA_KEYS = [
  'hub_profiles',
  'hub_active_profile',
  'connections',
  'connection_groups',
  'ssh_keys_meta',
  'vault_items',
  'vault_state',
  'voice.model',
] as const;

function isSnapshotKey(k: string): boolean {
  return k.startsWith(PREFIX) || (EXTRA_KEYS as readonly string[]).includes(k);
}

const VERSION = 1;

interface StateSnapshot {
  version: number;
  exportedAt: string;
  data: Record<string, string>;
}

/// Every snapshot-covered entry currently in localStorage.
function collect(): Record<string, string> {
  const out: Record<string, string> = {};
  for (let i = 0; i < localStorage.length; i += 1) {
    const k = localStorage.key(i);
    if (k !== null && isSnapshotKey(k)) {
      const v = localStorage.getItem(k);
      if (v !== null) out[k] = v;
    }
  }
  return out;
}

/// Write the current snapshot to disk. Native shell only; best-effort — a
/// backup path must never block or crash the app.
export async function exportState(): Promise<void> {
  if (!isShell()) return;
  const snap: StateSnapshot = {
    version: VERSION,
    exportedAt: new Date().toISOString(),
    data: collect(),
  };
  try {
    await invoke('migration_export', { json: JSON.stringify(snap) });
  } catch {
    /* best effort */
  }
}

let timer: ReturnType<typeof setTimeout> | null = null;
/// Debounced export — coalesces a burst of localStorage writes into one disk
/// write.
export function scheduleExport(delayMs = 2000): void {
  if (!isShell()) return;
  if (timer !== null) clearTimeout(timer);
  timer = setTimeout(() => {
    timer = null;
    void exportState();
  }, delayMs);
}

/// Restore a snapshot into localStorage ONLY when this profile has no
/// snapshot-covered keys yet — i.e. a fresh Chromium profile on Electron's
/// first boot. Under Tauri (keys already present) it returns before any disk
/// read, so it is safe — and cheap — to await on every boot. Must run before
/// the app reads localStorage.
export async function importStateIfFresh(): Promise<void> {
  if (!isShell()) return;
  if (Object.keys(collect()).length > 0) return; // profile already populated
  try {
    const raw = await invoke<string | null>('migration_read');
    if (raw === null || raw === undefined || raw === '') return;
    const snap = JSON.parse(raw) as Partial<StateSnapshot>;
    if (snap.data === undefined) return;
    for (const [k, v] of Object.entries(snap.data)) {
      if (isSnapshotKey(k) && typeof v === 'string' && localStorage.getItem(k) === null) {
        localStorage.setItem(k, v);
      }
    }
  } catch {
    /* best effort */
  }
}

/// Arm the ongoing export: one snapshot shortly after boot, then an immediate
/// flush whenever the app is backgrounded or closed (captures the freshest
/// state for the next boot / the Electron cutover). Call once, after render.
/// The flush must be direct, not debounced — a `setTimeout` scheduled during
/// `pagehide` never fires in a closing webview, so a timer-based flush would
/// silently drop the final snapshot on quit.
export function armExport(): void {
  if (!isShell()) return;
  scheduleExport(1000);
  const flush = (): void => {
    if (timer !== null) {
      clearTimeout(timer);
      timer = null;
    }
    void exportState();
  };
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'hidden') flush();
  });
  window.addEventListener('pagehide', flush);
}
