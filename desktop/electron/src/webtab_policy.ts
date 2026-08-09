/// Partition allowlist + per-partition navigation policy for `<webview>` guests
/// (agent-transcript-redesign P0). Electron-free on purpose: the pure policy
/// lives here so the unit tests (`webtab_policy.test.ts`) can exercise the
/// whole matrix without booting Electron; `webtab.ts` consumes it when wiring
/// the real sessions and `will-attach-webview` / popup / navigation handlers.
///
/// Two partitions are allowed today:
///   - `persist:webtab` — the Read surface's browser tab. Persistent, any
///     http(s) origin, safe `target=_blank` stays in-tab (reading flow).
///   - `rerunweb` — the Rerun viewer companion (J8 Replay W4). NON-persistent:
///     it hosts a locally-served recording and has nothing worth keeping on
///     disk between sessions. Same posture as kimiweb — loopback-pinned top
///     frame, window-open never in-tab — because it is the same kind of guest:
///     a third-party web UI we start ourselves and point at a local server.
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
  /// Agent browser-bridge capability (docs/plans/desktop-agent-browser-bridge.md):
  /// `full` — read + action tools (action tools land in W2); `read` — read
  /// tools only, action tools refuse with PARTITION_READ_ONLY; `none` — the
  /// partition never enters the bridge registry. Every partition chooses
  /// deliberately — a future partition must set this explicitly instead of
  /// being silently included or excluded. kimiweb is `read` by design: it
  /// hosts an agent chat UI, so action-driving it would let one bridge-enabled
  /// agent submit prompts into another agent's session with user authority.
  bridge: 'full' | 'read' | 'none';
}

export const WEBTAB_PARTITION = 'persist:webtab';
export const KIMIWEB_PARTITION = 'kimiweb';
export const RERUNWEB_PARTITION = 'rerunweb';

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
  { partition: WEBTAB_PARTITION, allowTopFrame: isHttpUrl, windowOpen: 'inline', bridge: 'full' },
  { partition: KIMIWEB_PARTITION, allowTopFrame: isLoopbackHttpUrl, windowOpen: 'external', bridge: 'read' },
  // Deliberately identical to kimiweb rather than looser. The rerun viewer is
  // served from the same machine that holds the recording, so it never needs a
  // remote origin, and a new partition must not relax the policy the existing
  // ones set (the plan's partition-discipline anchor). Bridge capability is
  // 'read' like kimiweb: agents may inspect the episode viewer (useful for
  // J8-style episode debugging) but never action-drive it.
  { partition: RERUNWEB_PARTITION, allowTopFrame: isLoopbackHttpUrl, windowOpen: 'external', bridge: 'read' },
];

/// The allowlist lookup — `null` means the partition may not host a guest at
/// all (`will-attach-webview` rejects it).
export function partitionPolicy(partition: string): PartitionPolicy | null {
  return PARTITION_POLICIES.find((p) => p.partition === partition) ?? null;
}

// ── Guest context-menu template (pure) ───────────────────────────────────────
// A `<webview>` guest's right-click fires its `context-menu` event in the MAIN
// process, NOT in the renderer DOM that the app's own menu listener
// (src/nativeContextMenu.ts) watches — a guest is a separate WebContents in its
// own process, so nothing reachable from the host DOM ever sees it. Without a
// main-side handler, kimiweb + the Read web tab had no context menu at all (no
// Copy/Paste). webtab.ts wires the event to a native menu built from this pure
// descriptor, whose item set / ordering / enabled-state is exercised by
// webtab_policy.test.ts without booting Electron.

export type GuestMenuAction =
  | 'back'
  | 'forward'
  | 'reload'
  | 'openLink'
  | 'copyLink'
  | 'copyImage'
  | 'cut'
  | 'copy'
  | 'paste'
  | 'selectAll';

export type GuestMenuItem = { action: GuestMenuAction; enabled: boolean } | 'separator';

/// The context relevant to building a guest menu, distilled from Electron's
/// `ContextMenuParams`. `linkURL` is already emptied by the caller when the URL
/// isn't a safe external, so this pure logic needn't know the scheme rules.
export interface GuestMenuContext {
  canGoBack: boolean;
  canGoForward: boolean;
  linkURL: string;
  isImage: boolean;
  isEditable: boolean;
  selectionText: string;
  canCut: boolean;
  canCopy: boolean;
  canPaste: boolean;
  canSelectAll: boolean;
}

/// Build the ordered guest context-menu descriptor. Navigation is always present
/// so right-clicking page whitespace still behaves like a browser; contextual
/// link, image and edit actions follow it.
export function buildGuestMenuTemplate(ctx: GuestMenuContext): GuestMenuItem[] {
  const out: GuestMenuItem[] = [
    { action: 'back', enabled: ctx.canGoBack },
    { action: 'forward', enabled: ctx.canGoForward },
    { action: 'reload', enabled: true },
  ];
  const sep = (): void => {
    if (out.length > 0) out.push('separator');
  };

  if (ctx.linkURL !== '') {
    sep();
    out.push({ action: 'openLink', enabled: true }, { action: 'copyLink', enabled: true });
  }
  if (ctx.isImage) {
    sep();
    out.push({ action: 'copyImage', enabled: true });
  }
  if (ctx.isEditable) {
    sep();
    out.push(
      { action: 'cut', enabled: ctx.canCut },
      { action: 'copy', enabled: ctx.canCopy },
      { action: 'paste', enabled: ctx.canPaste },
      'separator',
      { action: 'selectAll', enabled: ctx.canSelectAll },
    );
  } else if (ctx.selectionText !== '') {
    sep();
    out.push({ action: 'copy', enabled: true }, 'separator', { action: 'selectAll', enabled: true });
  }
  return out;
}
