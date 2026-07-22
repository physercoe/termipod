import { useCallback, useEffect, useRef, useState } from 'react';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import { hostOf, isShell, openBrowserWindow, openExternal } from '../platform';
import { invoke } from '../bridge';
import { proxyForConnection } from '../state/proxy';

/// The Read surface's in-app browser tab (director request: external links open
/// in a dedicated tab *inside* the app, not the OS browser).
///
/// The page is an Electron `<webview>` guest — a real top-level frame, so
/// `X-Frame-Options`/`frame-ancestors` do NOT apply and arXiv / publishers /
/// GitHub / Scholar actually load (the old sandboxed `<iframe>` was a bounce
/// page for nearly every site the reading workflow needs). All guest hardening —
/// isolated `persist:webtab` partition, no preload/bridge, popup + navigation
/// policy, permission + download control — is enforced main-side in
/// `electron/src/webtab.ts`; this component is only the chrome around the guest.
///
/// Navigation chrome drives the guest's REAL history (`goBack`/`goForward`/
/// `reload`/`loadURL`), enabled from `did-navigate`-time `canGoBack()`/
/// `canGoForward()`; the address bar and tab title reflect `did-navigate`/
/// `page-title-updated`. An empty initial URL renders the start state (an
/// autofocused address input) so a tab can be opened without a reference link.
///
/// See docs/plans/read-web-tabs-and-pdf-attachments.md.

interface WebviewEl extends HTMLElement {
  loadURL(url: string): Promise<void>;
  getURL(): string;
  getTitle(): string;
  goBack(): void;
  goForward(): void;
  reload(): void;
  stop(): void;
  canGoBack(): boolean;
  canGoForward(): boolean;
}

// `<webview>` is a host custom element; cast the tag so TS accepts the props we
// set (src/partition/allowpopups) without a global JSX.IntrinsicElements shim.
const Webview = 'webview' as unknown as React.FC<
  React.HTMLAttributes<HTMLElement> & {
    ref?: React.Ref<HTMLElement>;
    src?: string;
    partition?: string;
    allowpopups?: string;
    useragent?: string;
  }
>;

export function normalizeUrl(raw: string): string {
  const s = raw.trim();
  if (s === '') return s;
  if (/^https?:\/\//i.test(s)) return s;
  if (/^[\w.-]+\.[a-z]{2,}(\/|$|\?|#)/i.test(s)) return `https://${s}`;
  return s;
}

export function BrowserView({
  initialUrl,
  onTitle,
}: {
  initialUrl: string;
  onTitle?: (title: string) => void;
}): JSX.Element {
  const t = useT();
  const viewRef = useRef<WebviewEl | null>(null);
  const addrRef = useRef<HTMLInputElement | null>(null);
  // The URL the guest is currently on (drives the address bar); starts at
  // `initialUrl` (empty → the start state, no guest navigation yet).
  const [current, setCurrent] = useState(initialUrl);
  const [address, setAddress] = useState(initialUrl);
  const [started, setStarted] = useState(initialUrl !== '');
  const [canBack, setCanBack] = useState(false);
  const [canFwd, setCanFwd] = useState(false);
  // A real load failure (DNS / offline / TLS) — the replacement for the old
  // frame-refused panel, now only for genuine failures.
  const [loadError, setLoadError] = useState<{ code: number; desc: string; url: string } | null>(null);

  // Navigate the guest imperatively (never via the `src` prop — that would
  // remount/reset the guest on every address change).
  const navigate = useCallback((raw: string): void => {
    const u = normalizeUrl(raw);
    if (u === '') return;
    setStarted(true);
    setLoadError(null);
    setCurrent(u);
    const v = viewRef.current;
    if (v !== null) void v.loadURL(u).catch(() => undefined);
  }, []);

  // Reflect the guest's real navigation state after a (best-effort) settle.
  const syncNavState = useCallback((): void => {
    const v = viewRef.current;
    if (v === null) return;
    try {
      setCanBack(v.canGoBack());
      setCanFwd(v.canGoForward());
    } catch {
      /* guest not attached yet — ignore */
    }
  }, []);

  // Wire the guest's events → chrome. Runs once (the element is stable per tab).
  useEffect(() => {
    const v = viewRef.current;
    if (v === null) return;
    const onNavigate = (e: Event): void => {
      const url = (e as unknown as { url?: string }).url;
      if (typeof url === 'string' && url !== '' && url !== 'about:blank') {
        setCurrent(url);
        setAddress(url);
        setStarted(true);
      }
      setLoadError(null);
      syncNavState();
    };
    const onTitleUpdate = (e: Event): void => {
      const title = (e as unknown as { title?: string }).title;
      if (typeof title === 'string' && title !== '') onTitle?.(title);
    };
    const onFail = (e: Event): void => {
      const ev = e as unknown as { errorCode?: number; errorDescription?: string; validatedURL?: string; isMainFrame?: boolean };
      // -3 is ERR_ABORTED (a superseded navigation), not a real failure; and a
      // sub-frame failure must not blank the whole page.
      if (ev.errorCode === -3 || ev.isMainFrame === false) return;
      setLoadError({ code: ev.errorCode ?? 0, desc: ev.errorDescription ?? '', url: ev.validatedURL ?? current });
    };
    v.addEventListener('did-navigate', onNavigate);
    v.addEventListener('did-navigate-in-page', onNavigate);
    v.addEventListener('page-title-updated', onTitleUpdate);
    v.addEventListener('did-stop-loading', syncNavState);
    v.addEventListener('did-fail-load', onFail);
    return () => {
      v.removeEventListener('did-navigate', onNavigate);
      v.removeEventListener('did-navigate-in-page', onNavigate);
      v.removeEventListener('page-title-updated', onTitleUpdate);
      v.removeEventListener('did-stop-loading', syncNavState);
      v.removeEventListener('did-fail-load', onFail);
    };
  }, [onTitle, syncNavState, current]);

  // Push the app's effective proxy to the webtab session before the first load
  // (the session default is system-proxy; this applies a manual override).
  useEffect(() => {
    if (!isShell()) return;
    void invoke('webtab_set_proxy', { proxy: proxyForConnection('webtab') ?? null }).catch(() => undefined);
  }, []);

  // Ctrl/Cmd+L focuses the address bar (the one browser shortcut worth stealing;
  // it collides with nothing in the app's map).
  useEffect(() => {
    const onKey = (e: KeyboardEvent): void => {
      if ((e.ctrlKey || e.metaKey) && (e.key === 'l' || e.key === 'L')) {
        e.preventDefault();
        addrRef.current?.focus();
        addrRef.current?.select();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  return (
    <div className="browser-view">
      <div className="browser-bar">
        <button className="browser-nav" disabled={!canBack} title={t('read.browserBack')} onClick={() => viewRef.current?.goBack()}>
          <Icon name="chevron-left" />
        </button>
        <button className="browser-nav" disabled={!canFwd} title={t('read.browserForward')} onClick={() => viewRef.current?.goForward()}>
          <Icon name="chevron-right" />
        </button>
        <button className="browser-nav" title={t('read.browserReload')} onClick={() => viewRef.current?.reload()}>
          <Icon name="refresh" />
        </button>
        <input
          ref={addrRef}
          className="browser-address"
          value={address}
          spellCheck={false}
          placeholder={t('read.browserAddressPlaceholder')}
          onChange={(e) => setAddress(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') navigate(address);
          }}
        />
        <button className="browser-nav" title={t('read.openInWindow')} onClick={() => openBrowserWindow(current)} disabled={current === ''}>
          <Icon name="expand" />
        </button>
        <button className="browser-nav" title={t('read.openExternal')} onClick={() => openExternal(current)} disabled={current === ''}>
          <Icon name="external" />
        </button>
      </div>
      <div className="browser-frame-wrap">
        {/* The guest is always mounted (stable per tab); `src` is the constant
            initial URL — later navigation goes through loadURL, never `src`. */}
        <Webview
          ref={viewRef as unknown as React.Ref<HTMLElement>}
          className="browser-webview"
          src={initialUrl !== '' ? initialUrl : 'about:blank'}
          partition="persist:webtab"
          allowpopups="true"
        />
        {!started && (
          <div className="browser-start">
            <Icon name="globe" size={28} />
            <p className="muted">{t('read.browserStartHint')}</p>
            <input
              className="browser-address browser-start-address"
              value={address}
              spellCheck={false}
              autoFocus
              placeholder={t('read.browserAddressPlaceholder')}
              onChange={(e) => setAddress(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') navigate(address);
              }}
            />
          </div>
        )}
        {loadError !== null && (
          <div className="browser-error">
            <Icon name="alert" size={22} />
            <p className="muted">{t('read.browserLoadFailed').replace('{host}', hostOf(loadError.url))}</p>
            {loadError.desc !== '' && <p className="muted small mono">{loadError.desc}</p>}
            <div className="browser-error-actions">
              <button className="browser-nav" onClick={() => navigate(loadError.url)}>
                <Icon name="refresh" /> {t('read.browserRetry')}
              </button>
              <button className="browser-nav" onClick={() => openExternal(loadError.url)}>
                <Icon name="external" /> {t('read.openExternal')}
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
