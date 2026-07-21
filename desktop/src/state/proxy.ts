import { create } from 'zustand';
import { invoke } from '../bridge';
import { isShell } from '../platform';

/// Shared HTTP-proxy config for every outbound connection TermiPod makes under
/// Tauri (Settings → Network). One proxy URL — a manual override, else the
/// system/env proxy auto-detected by the `system_proxy` Rust command (env vars,
/// Windows registry, macOS `scutil`) — plus a PER-CONNECTION toggle deciding
/// whether each connection routes through it. Every sync/update/hub/draw.io call
/// site reads `proxyForConnection(conn)` and passes the result to its Rust
/// command; `net::client_builder` on the Rust side turns `undefined`/`None` into
/// a genuinely direct connection (no silent env-proxy fallback).
///
/// Reuses the original About/Update proxy keys so a proxy the user already saved
/// there carries over verbatim.

export type ProxyConn = 'hub' | 'attachments' | 'workspace' | 'discovery' | 'update' | 'drawio';
export const PROXY_CONNS: ProxyConn[] = [
  'hub',
  'attachments',
  'workspace',
  'discovery',
  'update',
  'drawio',
];

const OVERRIDE_KEY = 'termipod.update.proxy'; // reused from the old About setting
const DETECTED_KEY = 'termipod.update.proxy.detected'; // reused detected cache
const useKey = (c: ProxyConn): string => `termipod.proxy.use.${c}`;

function readLS(key: string): string | null {
  try {
    const v = localStorage.getItem(key);
    return v !== null && v !== '' ? v : null;
  } catch {
    return null;
  }
}

function loadToggles(): Record<ProxyConn, boolean> {
  const out = {} as Record<ProxyConn, boolean>;
  for (const c of PROXY_CONNS) {
    // Default ON: a proxy the user configures should take effect everywhere
    // until they opt a specific connection out ('0').
    out[c] = readLS(useKey(c)) !== '0';
  }
  return out;
}

interface ProxyState {
  /// Manual override the user typed; '' = use the auto-detected value.
  override: string;
  /// What `system_proxy` detected (env / Windows / macOS), for display + as the
  /// effective proxy when no manual override is set.
  detected: string | null;
  /// Per-connection "route through the proxy" switches.
  use: Record<ProxyConn, boolean>;
  setOverride: (v: string) => void;
  setUse: (c: ProxyConn, on: boolean) => void;
  /// Fetch + cache the system/env proxy (idempotent; call at startup and when the
  /// Network tab mounts). No-op in the browser build.
  resolveDetected: () => Promise<void>;
}

export const useProxy = create<ProxyState>((set, get) => ({
  override: readLS(OVERRIDE_KEY) ?? '',
  detected: readLS(DETECTED_KEY),
  use: loadToggles(),
  setOverride: (v) => {
    const trimmed = v.trim();
    try {
      if (trimmed !== '') localStorage.setItem(OVERRIDE_KEY, trimmed);
      else localStorage.removeItem(OVERRIDE_KEY);
    } catch {
      /* storage unavailable — state still drives the UI */
    }
    set({ override: v });
  },
  setUse: (c, on) => {
    try {
      localStorage.setItem(useKey(c), on ? '1' : '0');
    } catch {
      /* ignore */
    }
    set({ use: { ...get().use, [c]: on } });
  },
  resolveDetected: async () => {
    if (!isShell()) return;
    try {
      const p = await invoke<string | null>('system_proxy');
      const val = p !== null && p !== '' ? p : null;
      try {
        if (val !== null) localStorage.setItem(DETECTED_KEY, val);
        else localStorage.removeItem(DETECTED_KEY);
      } catch {
        /* ignore */
      }
      set({ detected: val });
    } catch {
      set({ detected: null });
    }
  },
}));

/// The effective proxy URL (manual override wins, else the detected system/env
/// proxy), or undefined for a direct connection.
export function effectiveProxy(): string | undefined {
  const s = useProxy.getState();
  const o = s.override.trim();
  if (o !== '') return o;
  return s.detected ?? undefined;
}

/// The proxy a given connection should use right now, honouring its toggle.
/// undefined = connect directly. Call this at each network call site.
export function proxyForConnection(conn: ProxyConn): string | undefined {
  if (!useProxy.getState().use[conn]) return undefined;
  return effectiveProxy();
}
