/// Partition allowlist + per-partition navigation policy for `<webview>` guests
/// (agent-transcript-redesign P0). Electron-free on purpose: the pure policy
/// lives here so the unit tests (`webtab_policy.test.ts`) can exercise the
/// whole matrix without booting Electron; `webtab.ts` consumes it when wiring
/// the real sessions and `will-attach-webview` / popup / navigation handlers.
///
/// Two partitions are allowed today:
///   - `persist:webtab` — the Read surface's browser tab. Persistent, any
///     http(s) origin, safe `target=_blank` stays in-tab (reading flow).
///   - `kimiweb` — embedded agent web UIs (`kimi web`). NON-persistent: the
///     bearer token rides the URL hash (`#token=…`), and a persistent partition
///     would keep it in guest history on disk; the token is re-captured at each
///     spawn anyway. Top-frame navigation is pinned to loopback origins, and
///     window-open never loads in-tab — safe links go to the OS browser.
///
/// Anything else (including the default session, where the `app://`/`drawio://`
/// scheme handlers and hub-CORS bearer injection live) is rejected at attach.

export interface PartitionPolicy {
  partition: string;
  /// Top-frame navigation predicate — enforced BOTH at the request layer
  /// (`onBeforeRequest`, which catches programmatic `loadURL` and redirects)
  /// and at `will-navigate`.
  allowTopFrame: (url: string) => boolean;
  /// `inline`: a safe http(s) popup becomes an in-tab navigation (webtab
  /// reading flow). `external`: nothing opens in-tab; safe schemes go to the
  /// OS browser (the most restrictive path — the kimiweb guest never leaves
  /// loopback, so an in-tab popup would be blocked by the nav policy anyway).
  windowOpen: 'inline' | 'external';
}

export const WEBTAB_PARTITION = 'persist:webtab';
export const KIMIWEB_PARTITION = 'kimiweb';

export function isHttpUrl(url: string): boolean {
  return /^https?:\/\//i.test(url);
}

/// Loopback-only http(s): 127.0.0.1 / localhost / [::1], any port. Hostname
/// comparison (not a string prefix) so `http://127.0.0.1.evil.com/` and
/// `http://169.254.169.254/` (cloud metadata) are NOT loopback.
export function isLoopbackHttpUrl(url: string): boolean {
  if (!isHttpUrl(url)) return false;
  try {
    const h = new URL(url).hostname.toLowerCase();
    return h === '127.0.0.1' || h === 'localhost' || h === '[::1]' || h === '::1';
  } catch {
    return false;
  }
}

export const PARTITION_POLICIES: readonly PartitionPolicy[] = [
  { partition: WEBTAB_PARTITION, allowTopFrame: isHttpUrl, windowOpen: 'inline' },
  { partition: KIMIWEB_PARTITION, allowTopFrame: isLoopbackHttpUrl, windowOpen: 'external' },
];

/// The allowlist lookup — `null` means the partition may not host a guest at
/// all (`will-attach-webview` rejects it).
export function partitionPolicy(partition: string): PartitionPolicy | null {
  return PARTITION_POLICIES.find((p) => p.partition === partition) ?? null;
}
