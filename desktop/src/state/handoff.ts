/// Updater handoff (ADR-055 plan §2.3 / §5) — the frontend half of
/// `src-tauri/handoff.rs`.
///
/// At the M3 cutover the final Tauri release surfaces the successor Electron
/// installer as a normal "a new version is available — Download" prompt (the
/// Tauri auto-updater can't install a foreign installer format in place). This
/// checks a small `handoff.json` published beside `latest.json`; it is absent
/// today, so `checkHandoff()` returns null and the update UI is unchanged.
import { invoke } from '../bridge';
import { platformOs, shellKind } from '../platform';
import { proxyForConnection } from './proxy';

const MANIFEST_URL = 'https://github.com/physercoe/termipod/releases/latest/download/handoff.json';

export interface Handoff {
  version: string;
  notes?: string;
  url: string;
}

interface HandoffManifest {
  version?: string;
  notes?: string;
  /** OS key (`windows` | `macos` | `linux`, matching `platform_os`) → installer URL. */
  platforms?: Record<string, string>;
}

/// Whether a successor (Electron) build is published for this OS. Only the
/// Tauri shell needs this — it is the build being handed OFF; Electron is itself
/// the successor and the browser build doesn't self-update. Returns null when no
/// manifest is published (the steady state until M3) or on any failure — the
/// caller then falls back to the normal update check.
export async function checkHandoff(): Promise<Handoff | null> {
  if (shellKind() !== 'tauri') return null;
  try {
    const raw = await invoke<string | null>('handoff_check', {
      url: MANIFEST_URL,
      proxy: proxyForConnection('update') ?? null,
    });
    if (raw === null || raw === undefined || raw === '') return null;
    const m = JSON.parse(raw) as HandoffManifest;
    if (typeof m.version !== 'string' || m.version === '') return null;
    const os = await platformOs(); // 'windows' | 'macos' | 'linux'
    const url = m.platforms?.[os];
    if (typeof url !== 'string' || url === '') return null;
    return { version: m.version, notes: m.notes, url };
  } catch {
    return null;
  }
}
