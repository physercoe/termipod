import { useCallback, useEffect, useRef, useState } from 'react';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import { isShell } from '../platform';
import type { WebPanelDef } from '../ui/webPanels';

/// A session web panel (agent-transcript-redesign P0): an embedded agent web UI
/// (`kimi web`) in a `<webview>` guest. This component is only the chrome; all
/// hardening (loopback-pinned navigation, no preload/bridge, popup policy) is
/// enforced main-side per the panel's partition (electron/src/webtab.ts +
/// webtab_policy.ts), and the backing server lifecycle is the main-process
/// manager's (electron/src/kimiweb.ts).
///
/// Two honest caveats from the plan shape the UI:
///   (a) kimi-web has NO per-session deep link — the guest opens kimi's
///       last-active session and the user switches in kimi's own sidebar;
///   (b) this is a parallel UI, not a data path — hence the always-visible
///       "external UI" notice strip.

interface WebviewEl extends HTMLElement {
  loadURL(url: string): Promise<void>;
  getURL(): string;
}

// `<webview>` is a host custom element; cast the tag so TS accepts the props we
// set (src/partition) without a global JSX.IntrinsicElements shim — same pattern
// as surfaces/BrowserView.tsx.
const Webview = 'webview' as unknown as React.FC<
  React.HTMLAttributes<HTMLElement> & {
    ref?: React.Ref<HTMLElement>;
    src?: string;
    partition?: string;
  }
>;

type Phase = 'starting' | 'ready' | 'error';

export function WebPanel({ panel }: { panel: WebPanelDef }): JSX.Element {
  const t = useT();
  const viewRef = useRef<WebviewEl | null>(null);
  const [phase, setPhase] = useState<Phase>('starting');
  const [url, setUrl] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  // Bump to re-attempt a failed start (the Retry button).
  const [attempt, setAttempt] = useState(0);

  // Start the backing server and mount the guest at the returned embed URL.
  // Failures (binary missing, token never printed, early exit) land in the
  // inline error state with a retry (a null error message renders the static
  // no-shell text, which is why the effect never needs `t`).
  //
  // Every start() is paired with a stop() in THIS effect's cleanup — the
  // main-side refcount counts each start (including failed ones), so a
  // stop-per-unmount-only pairing would leak a hold per Retry and the shared
  // server would outlive the last panel. The manager serializes the
  // stop→start replay a re-run produces, so pairing here is race-free.
  const shell = isShell();
  useEffect(() => {
    if (!shell) {
      // The browser degrade build has no native shell to spawn from.
      setPhase('error');
      return;
    }
    let alive = true;
    setPhase('starting');
    setError(null);
    panel.start().then(
      ({ url: u }) => {
        if (!alive) return;
        setUrl(u);
        setPhase('ready');
      },
      (e: unknown) => {
        if (!alive) return;
        setError(e instanceof Error ? e.message : String(e));
        setPhase('error');
      },
    );
    return () => {
      alive = false;
      void panel.stop().catch(() => undefined);
    };
  }, [shell, panel, attempt]);

  // A main-frame load failure inside the guest (the server died mid-session —
  // e.g. a stray Ctrl+C) drops back to the error state; Retry re-invokes
  // `start`, which respawns when the manager saw the child exit.
  useEffect(() => {
    const v = viewRef.current;
    if (v === null || phase !== 'ready') return;
    const onFail = (e: Event): void => {
      const ev = e as unknown as { errorCode?: number; errorDescription?: string; isMainFrame?: boolean };
      // -3 is ERR_ABORTED (a superseded navigation), not a real failure; and a
      // sub-frame failure must not blank the whole panel.
      if (ev.errorCode === -3 || ev.isMainFrame === false) return;
      setError(ev.errorDescription ?? t('webpanel.loadFailed'));
      setPhase('error');
    };
    v.addEventListener('did-fail-load', onFail);
    return () => v.removeEventListener('did-fail-load', onFail);
  }, [phase, t]);

  const retry = useCallback(() => setAttempt((n) => n + 1), []);

  return (
    <div className="web-panel">
      <div className="web-panel-notice muted small">
        <Icon name="globe" size={12} />
        <span>{t(panel.noticeKey)}</span>
      </div>
      {phase === 'ready' && url !== null && (
        <Webview ref={viewRef as unknown as React.Ref<HTMLElement>} className="web-panel-guest" src={url} partition={panel.partition} />
      )}
      {phase === 'starting' && (
        <div className="web-panel-state muted small">
          <p>{t('webpanel.starting')}</p>
        </div>
      )}
      {phase === 'error' && (
        <div className="web-panel-state">
          <Icon name="alert" size={18} />
          <p className="error small">{error ?? t('webpanel.noShell')}</p>
          {shell && (
            <button className="web-panel-retry" onClick={retry}>
              <Icon name="refresh" size={13} /> {t('webpanel.retry')}
            </button>
          )}
        </div>
      )}
    </div>
  );
}
