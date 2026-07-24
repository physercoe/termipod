import { invoke } from '../bridge';

/// A session web-panel kind (agent-transcript-redesign P0, decision §7.2 —
/// registry-shaped internals, kimi-scoped UI). Each row is one embeddable agent
/// web UI; the SessionView sub-tab switcher renders one tab per row.
///
/// Adding another agent web UI later is: one row here + its partition in the
/// main-process allowlist (`electron/src/webtab_policy.ts`) + a spawn manager
/// behind the start/stop pair (see `electron/src/kimiweb.ts` for the shape).
export interface WebPanelDef {
  /// Stable id — the SessionView sub-view key is `web:${id}`.
  id: string;
  /// i18n key for the sub-tab label.
  labelKey: string;
  /// i18n key for the "external UI" affordance strip (plan caveat (b): this is
  /// a PARALLEL UI, not a data path — hub events / attention / team features
  /// do not see what happens inside the guest).
  noticeKey: string;
  /// The `<webview>` partition — must be allowlisted main-side, else the
  /// `will-attach-webview` guard refuses the guest.
  partition: string;
  /// Start (or reuse) the backing server; resolves with the embed URL.
  start: () => Promise<{ url: string }>;
  /// Release this panel's hold on the shared server (refcounted main-side —
  /// the server dies when the last panel closes).
  stop: () => Promise<void>;
}

/// The one P0 row. Caveat (a) lives here by design: kimi-web is an SPA with NO
/// per-session deep link (the URL hash carries the bearer token, not routes),
/// so the panel always opens kimi's last-active session and the user switches
/// sessions in kimi's own sidebar — we cannot deep-link the termipod session
/// the panel sits in.
export const webPanels: readonly WebPanelDef[] = [
  {
    id: 'kimi',
    labelKey: 'webpanel.kimi',
    noticeKey: 'webpanel.externalNotice',
    partition: 'kimiweb',
    start: () => invoke<{ url: string }>('kimiweb_start'),
    stop: () => invoke<void>('kimiweb_stop'),
  },
];

export function webPanelById(id: string): WebPanelDef | undefined {
  return webPanels.find((p) => p.id === id);
}
