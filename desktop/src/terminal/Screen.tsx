import { useEffect, useRef, useState } from 'react';
import { FitAddon } from '@xterm/addon-fit';
import { SearchAddon } from '@xterm/addon-search';
import { WebLinksAddon } from '@xterm/addon-web-links';
import { Terminal as XTerm } from '@xterm/xterm';
import '@xterm/xterm/css/xterm.css';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import { isWindows, openExternal, shellKind, windowsBuildNumber } from '../platform';

// Persisted terminal font size (Ctrl/Cmd +/-/0 zoom, #319). Shared across all
// screens so zoom is a global preference, clamped to a sane, legible range.
const FONT_KEY = 'termipod.term.fontSize';
const FONT_MIN = 8;
const FONT_MAX = 28;
const FONT_DEFAULT = 13;
function loadFontSize(): number {
  try {
    const n = Number(localStorage.getItem(FONT_KEY));
    if (Number.isFinite(n) && n >= FONT_MIN && n <= FONT_MAX) return n;
  } catch {
    /* ignore */
  }
  return FONT_DEFAULT;
}
function saveFontSize(n: number): void {
  try {
    localStorage.setItem(FONT_KEY, String(n));
  } catch {
    /* ignore */
  }
}
import {
  onSessionData,
  onSessionExit,
  sessionClose,
  sessionResize,
  sessionStart,
  sessionWrite,
  type TermKind,
} from './backend';

interface Props {
  kind: TermKind;
  sessionId: string;
  /** Offer a Reconnect affordance when the session dies (#319). */
  onReconnect?: () => void;
  /** Called when the session emits output — drives the tab unread dot (#319). */
  onActivity?: () => void;
}

/// A live xterm.js screen bound to one session (SSH or local), with the fit +
/// search addons (no WebGL — see the renderer note in the mount effect). The
/// effect depends on `sessionId` ALONE: its cleanup calls sessionClose(), so
/// re-running it on a parent re-render would tear the session down (the
/// black-terminal / dead-input bug fixed in desktop-v0.3.10). Unmount closes the
/// session — which now only happens when the owning tab is closed, since inactive
/// tabs are hidden via CSS, not unmounted.
///
/// Chrome is intentionally minimal (director feedback, v0.3.46): no persistent
/// toolbar stealing vertical space. Find rides a floating bar toggled with
/// Ctrl/Cmd+F (VS Code idiom). (Command-block tracking was removed in v0.3.49 —
/// the OSC-133 integration was unreliable.)
export function Screen({ kind, sessionId, onReconnect, onActivity }: Props): JSX.Element {
  const t = useT();
  const ref = useRef<HTMLDivElement>(null);
  // Latest onActivity, read from inside the session effect without adding it to
  // the effect deps (which key on sessionId alone — see the effect note).
  const onActivityRef = useRef(onActivity);
  onActivityRef.current = onActivity;
  const termRef = useRef<XTerm | null>(null);
  const searchRef = useRef<SearchAddon | null>(null);
  const findInputRef = useRef<HTMLInputElement>(null);
  const [showFind, setShowFind] = useState(false);
  const [menu, setMenu] = useState<{ x: number; y: number } | null>(null);
  const [matchInfo, setMatchInfo] = useState<{ index: number; count: number } | null>(null);
  const [query, setQuery] = useState('');
  // Dead-session state: null while live; set (with the exit code, if any) once the
  // shell/channel closes so a Reconnect banner can appear over the frozen buffer.
  const [exited, setExited] = useState<{ code: number | null } | null>(null);

  useEffect(() => {
    const el = ref.current;
    if (el === null) return;
    let disposed = false;
    // A reconnect rebinds this same <Screen> to a new sessionId — clear any dead
    // banner from the prior session.
    setExited(null);
    const term = new XTerm({
      cursorBlink: true,
      // Modern, professional monospace stack — prefer ligature-friendly coding
      // faces, fall back through the OS defaults. Slightly airier metrics than
      // xterm's defaults so long sessions read cleanly.
      fontFamily:
        '"JetBrains Mono Variable", "JetBrains Mono", "Cascadia Code", "SF Mono", ui-monospace, "Menlo", "Consolas", "DejaVu Sans Mono", monospace',
      fontSize: loadFontSize(),
      fontWeight: 400,
      fontWeightBold: 600,
      lineHeight: 1.25,
      // 10k lines of scrollback (xterm default is 1000) — long build logs and
      // agent transcripts scroll back much further before the head is dropped.
      scrollback: 10000,
      // letterSpacing MUST stay an integer. xterm's DOM renderer applies the raw
      // value as real CSS `letter-spacing` per character, but computes the layout
      // cell width with `Math.round(letterSpacing)`. A fractional value (e.g. 0.2)
      // rounds to 0 for layout yet still nudges every glyph right, so the drift
      // accumulates across a wide row and pushes the rightmost columns off the grid
      // — the kimi right-edge truncation. 0 keeps render and layout in agreement.
      letterSpacing: 0,
      cursorStyle: 'bar',
      theme: {
        background: '#0d1117',
        foreground: '#e6edf3',
        cursor: '#58a6ff',
        cursorAccent: '#0d1117',
        selectionBackground: '#2d4a72',
      },
    });
    const fit = new FitAddon();
    const search = new SearchAddon();
    term.loadAddon(fit);
    term.loadAddon(search);
    // Live match count for the find bar ("3 / 17"); resultIndex is -1 when there
    // are no matches.
    search.onDidChangeResults(({ resultIndex, resultCount }) => {
      if (!disposed) setMatchInfo(resultCount > 0 ? { index: resultIndex + 1, count: resultCount } : { index: 0, count: 0 });
    });
    // Clickable URLs — opened in the OS browser (never the app webview, which
    // would strand the single-page shell), matching openExternal everywhere else.
    term.loadAddon(new WebLinksAddon((_e, uri) => openExternal(uri)));
    term.open(el);

    // GPU renderer ladder (#333). xterm's default DOM renderer paints every row as
    // <div>/<span> — the slowest path, and costly for full-screen TUIs (vim/htop),
    // output bursts, and 10k-scrollback resize reflows. Upgrade per platform, best
    // effort, with self-healing fall-back and lazy imports (addons stay out of the
    // main chunk):
    //   • macOS / Linux → WebGL (GPU texture-atlas glyphs); on context loss, drop
    //     to canvas so a lost GPU context never freezes on a blank canvas.
    //   • Windows        → WebGL under Electron (Chromium/ANGLE is fine), canvas
    //     only under Tauri. WebView2's ANGLE black-screened WebGL and could wedge
    //     the GPU process (v0.3.11), so the Tauri shell stays canvas-only; Electron
    //     is a different Chromium and doesn't hit that bug (plan M4 §6/§7 #9). The
    //     OS gate is the Rust `platform_os` (compile-time exact), not a spoofable
    //     navigator.userAgent; the shell gate is the injected-globals `shellKind()`.
    //   • any failure    → the DOM renderer remains (xterm's default) — always works.
    // Renderer choice is renderer-agnostic to selection/find/links/fit/scrollbar and
    // to the integer-letterSpacing note below (0 is correct in every renderer).
    const loadCanvas = async (): Promise<void> => {
      if (disposed) return;
      try {
        const { CanvasAddon } = await import('@xterm/addon-canvas');
        if (!disposed) term.loadAddon(new CanvasAddon());
      } catch {
        /* DOM renderer stays */
      }
    };
    void (async () => {
      const win = await isWindows();
      if (disposed) return;
      if (win) {
        // Tell xterm the pty is ConPTY (the desktop terminal is portable-pty →
        // ConPTY on Windows). ConPTY does its own line wrapping and, when the
        // viewport grows, adds blank rows at the BOTTOM rather than pulling
        // scrollback back into view — so without this flag xterm's own reflow
        // fights ConPTY and a repainting TUI's intermediate frames pile up in the
        // scrollback (director report: "scroll up to see the intermediate drawing
        // content"). Passing the build number lets xterm keep native reflow on for
        // builds ≥ 21376 (where ConPTY emits proper wrap sequences) and fall back
        // to the heuristic below it; when the build is unknown, `{ backend }` alone
        // still fixes the duplicate-scrollback bug (reflow off, as legacy
        // windowsMode did). This is independent of the renderer choice below.
        const build = await windowsBuildNumber();
        if (disposed) return;
        term.options.windowsPty = build !== null ? { backend: 'conpty', buildNumber: build } : { backend: 'conpty' };
      }
      // WebGL everywhere except Windows-under-Tauri (see the ladder note above).
      const tryWebgl = !win || shellKind() === 'electron';
      if (tryWebgl) {
        try {
          const { WebglAddon } = await import('@xterm/addon-webgl');
          if (disposed) return;
          const webgl = new WebglAddon();
          webgl.onContextLoss(() => {
            webgl.dispose();
            void loadCanvas();
          });
          term.loadAddon(webgl);
          return;
        } catch {
          /* WebGL unavailable (driver/GPU) — fall through to canvas */
        }
      }
      await loadCanvas();
    })();
    // Apply the current font size (Ctrl/Cmd +/-/0), refit, and persist.
    const zoom = (next: number): void => {
      const size = Math.max(FONT_MIN, Math.min(FONT_MAX, next));
      if (size === term.options.fontSize) return;
      term.options.fontSize = size;
      saveFontSize(size);
      applyFit(true);
    };
    // Ctrl/Cmd+F opens the floating find bar (VS Code terminal idiom) instead of
    // passing to the shell's readline forward-char. Ctrl/Cmd +/-/0 zoom the font.
    // Returning false stops xterm from forwarding the key to the pty.
    term.attachCustomKeyEventHandler((e) => {
      if (e.type !== 'keydown') return true;
      const mod = e.ctrlKey || e.metaKey;
      if (mod && !e.altKey && e.key.toLowerCase() === 'f') {
        if (!disposed) setShowFind(true);
        return false;
      }
      if (mod && !e.altKey && (e.key === '=' || e.key === '+')) {
        zoom((term.options.fontSize ?? FONT_DEFAULT) + 1);
        return false;
      }
      if (mod && !e.altKey && (e.key === '-' || e.key === '_')) {
        zoom((term.options.fontSize ?? FONT_DEFAULT) - 1);
        return false;
      }
      if (mod && !e.altKey && e.key === '0') {
        zoom(FONT_DEFAULT);
        return false;
      }
      return true;
    });
    // (Renderer selection happens right after term.open above — WebGL on
    // macOS/Linux, canvas on Windows, DOM fallback everywhere. WebGL is gated OFF
    // Windows because it black-screened WebView2/ANGLE in v0.3.11.)
    termRef.current = term;
    searchRef.current = search;

    // Re-fit + repaint whenever the container has a real size (initial open,
    // window/panel resize, and — critically — when un-hidden after a tab switch:
    // while `display:none` the element is 0×0 and xterm can't lay out, so the
    // buffered prompt reads as a black screen until we re-fit and refresh).
    let lastCols = 0; // last geometry we handed the PTY (SIGWINCH target)
    let lastRows = 0;
    let lastW = -1; // last observed .term-screen size we fit to (loop guard)
    let lastH = -1;
    let wasVisible = false;
    let rafId = 0;
    let ptyTimer: ReturnType<typeof setTimeout> | undefined;

    const applyFit = (force: boolean): void => {
      if (disposed || el.clientWidth === 0 || el.clientHeight === 0) {
        wasVisible = false;
        return;
      }
      // `fit.fit()` only calls `term.resize()` when the proposed column count
      // changes — and it's `resize()` that makes xterm re-measure the character
      // cell. So when an async web font swaps in *wider* glyphs but the fallback
      // metric happens to yield the same column count, xterm keeps the stale
      // (narrower) cell width, over-counts columns, and the rightmost ones spill
      // under the scrollbar. Force a re-measure via a fontFamily nudge (public API)
      // so the next fit proposes honest columns.
      if (force) {
        try {
          const ff = term.options.fontFamily;
          term.options.fontFamily = `${ff}, monospace`;
          term.options.fontFamily = ff;
        } catch {
          /* option setter unavailable — fall through to a plain fit */
        }
      }
      try {
        fit.fit(); // when the dimensions change this resizes + renders on its own
      } catch {
        return;
      }
      if (term.cols !== lastCols || term.rows !== lastRows) {
        const c = term.cols;
        const r = term.rows;
        lastCols = c;
        lastRows = r;
        // Debounce ONLY the PTY winsize: a full-screen TUI (kimi, vim) repaints on
        // every SIGWINCH, so the shell should learn the final size once — xterm has
        // already reflowed visually via fit.fit() above.
        if (ptyTimer !== undefined) clearTimeout(ptyTimer);
        ptyTimer = setTimeout(() => {
          ptyTimer = undefined;
          if (!disposed) void sessionResize(kind, sessionId, c, r);
        }, 150);
      }
      // Full repaint ONLY when the pane was just revealed (0×0 while display:none →
      // xterm can't lay out, so the buffered prompt reads as black until we refit +
      // refresh). On a plain resize fit.fit() already re-rendered — an extra
      // full-screen refresh here is what strobed the pane during a resize.
      if (!wasVisible) {
        wasVisible = true;
        term.refresh(0, term.rows - 1);
        term.focus();
      }
      // Record the box we just fit to, from EVERY path (initial call, settle
      // timers, fonts.ready, and the observer). Without this a direct applyFit
      // leaves lastW/lastH stale, so the next ResizeObserver callback for the same
      // box slips past its guard and re-fits — one wasted full-scrollback reflow,
      // and the seed of the multi-second "content slides right" redraw when a burst
      // of them stacks up. Keeping the guard authoritative here collapses a
      // dock-side switch to a single reflow.
      lastW = Math.round(el.clientWidth);
      lastH = Math.round(el.clientHeight);
    };

    // Coalesce a burst of ResizeObserver callbacks into one fit per frame.
    let pendingForce = false;
    const scheduleFit = (force = false): void => {
      if (force) pendingForce = true;
      if (rafId !== 0) return;
      rafId = requestAnimationFrame(() => {
        rafId = 0;
        const f = pendingForce;
        pendingForce = false;
        applyFit(f);
      });
    };

    applyFit(false);
    // The terminal font (JetBrains Mono) is an async web font: the first fit runs
    // with a fallback metric. Force a re-measure + re-fit once the real font is
    // ready, plus a couple of delayed re-fits to catch late layout settling (dock
    // open animation, first pane reveal).
    void document.fonts?.ready.then(() => scheduleFit(true));
    const settle1 = setTimeout(() => scheduleFit(true), 120);
    const settle2 = setTimeout(() => scheduleFit(false), 450);

    const onData = term.onData((s) => void sessionWrite(kind, sessionId, s));
    // Loop guard: only refit when `.term-screen`'s rounded box ACTUALLY changed.
    // xterm's resize mutates elements INSIDE this box, never the box itself, so any
    // callback reporting the same size is spurious (sub-pixel jitter at fractional
    // Windows display scaling, or a fit→observe echo) — refitting on it re-wraps the
    // whole scrollback for nothing and can self-perpetuate into a multi-second
    // redraw storm. Ignore it.
    const ro = new ResizeObserver(() => {
      // Compare against `clientWidth/Height` (the same metric applyFit records),
      // NOT the entry's contentRect — contentRect excludes `.term-screen`'s padding
      // while clientWidth includes it, so mixing the two would leave the guard
      // permanently mismatched and defeat the loop suppression.
      const w = Math.round(el.clientWidth);
      const h = Math.round(el.clientHeight);
      if (w === lastW && h === lastH) return;
      scheduleFit();
    });
    ro.observe(el);

    const unlistenP = onSessionData(kind, sessionId, (b) => {
      term.write(b);
      onActivityRef.current?.();
    });
    const exitP = onSessionExit(kind, sessionId, (code) => {
      if (disposed) return;
      const tag = code === null ? '[process exited]' : `[process exited: ${code}]`;
      term.write(`\r\n\x1b[2m${tag}\x1b[0m\r\n`);
      setExited({ code });
    });
    // Copy-on-select (Termius/iTerm idiom): once a drag-selection settles, put it on
    // the clipboard so a plain select is enough to copy (right-click paste / Ctrl+V
    // then round-trips it). Only fires for a non-empty selection.
    const onMouseUp = (): void => {
      const sel = term.getSelection();
      if (sel !== '') void navigator.clipboard.writeText(sel).catch(() => undefined);
    };
    el.addEventListener('mouseup', onMouseUp);
    // Only start streaming once BOTH listeners are registered — a local shell
    // prints its prompt within microseconds of spawning, and emitting before the
    // subscriber attaches drops it (the black-local-shell bug). SSH is a no-op.
    void Promise.all([unlistenP, exitP]).then(() => {
      if (!disposed) void sessionStart(kind, sessionId);
    });
    term.focus();

    return () => {
      disposed = true;
      clearTimeout(settle1);
      clearTimeout(settle2);
      if (ptyTimer !== undefined) clearTimeout(ptyTimer);
      if (rafId !== 0) cancelAnimationFrame(rafId);
      ro.disconnect();
      el.removeEventListener('mouseup', onMouseUp);
      onData.dispose();
      void unlistenP.then((u) => u());
      void exitP.then((u) => u());
      void sessionClose(kind, sessionId);
      term.dispose();
      termRef.current = null;
      searchRef.current = null;
    };
    // sessionId ALONE — see the doc comment. Re-running closes the session.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId]);

  // Focus the find field when the bar opens.
  useEffect(() => {
    if (showFind) findInputRef.current?.focus();
  }, [showFind]);

  function closeFind(): void {
    setShowFind(false);
    setMatchInfo(null);
    termRef.current?.focus();
  }

  // Right-click actions on the terminal screen (#319). Copy the selection, paste
  // clipboard text into the pty, select the whole buffer, or clear scrollback.
  function copySelection(): void {
    const sel = termRef.current?.getSelection() ?? '';
    if (sel !== '') void navigator.clipboard.writeText(sel).catch(() => {});
    setMenu(null);
  }
  function pasteClipboard(): void {
    void navigator.clipboard
      .readText()
      .then((text) => {
        if (text !== '') void sessionWrite(kind, sessionId, text);
      })
      .catch(() => {});
    setMenu(null);
    termRef.current?.focus();
  }
  function selectAll(): void {
    termRef.current?.selectAll();
    setMenu(null);
  }
  function clearScreen(): void {
    termRef.current?.clear();
    setMenu(null);
    termRef.current?.focus();
  }

  function runSearch(dir: 1 | -1): void {
    const q = query.trim();
    if (q === '' || searchRef.current === null) return;
    if (dir === 1) searchRef.current.findNext(q);
    else searchRef.current.findPrevious(q);
  }

  return (
    <div className="term-screen-wrap">
      {showFind && (
        <div className="term-find">
          <input
            ref={findInputRef}
            className="term-find-input"
            placeholder={t('term.search')}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') runSearch(e.shiftKey ? -1 : 1);
              else if (e.key === 'Escape') closeFind();
            }}
          />
          {matchInfo !== null && (
            <span className="term-find-count muted small">
              {matchInfo.count === 0 ? t('term.noMatches') : `${matchInfo.index}/${matchInfo.count}`}
            </span>
          )}
          <button className="term-find-btn" title={t('term.searchPrev')} onClick={() => runSearch(-1)}>
            <Icon name="chevron-up" size={14} />
          </button>
          <button className="term-find-btn" title={t('term.searchNext')} onClick={() => runSearch(1)}>
            <Icon name="chevron-down" size={14} />
          </button>
          <button className="term-find-btn" title={t('common.cancel')} onClick={closeFind}>
            <Icon name="close" size={14} />
          </button>
        </div>
      )}

      <div
        className="term-screen"
        ref={ref}
        onContextMenu={(e) => {
          e.preventDefault();
          setMenu({ x: e.clientX, y: e.clientY });
        }}
      />

      {exited !== null && (
        <div className="term-dead" role="status">
          <span className="term-dead-msg">
            {exited.code === null || exited.code === 0
              ? t('term.sessionEnded')
              : t('term.sessionEndedCode').replace('{code}', String(exited.code))}
          </span>
          {onReconnect !== undefined && (
            <button className="term-dead-reconnect primary small" onClick={onReconnect}>
              <Icon name="refresh" size={13} /> {t('term.reconnect')}
            </button>
          )}
        </div>
      )}

      {menu !== null && (
        <>
          <div className="term-ctxmenu-backdrop" onMouseDown={() => setMenu(null)} onContextMenu={(e) => { e.preventDefault(); setMenu(null); }} />
          <div className="term-nav-ctxmenu" style={{ left: menu.x, top: menu.y }} role="menu">
            <button role="menuitem" disabled={(termRef.current?.getSelection() ?? '') === ''} onClick={copySelection}>
              {t('term.copy')}
            </button>
            <button role="menuitem" onClick={pasteClipboard}>
              {t('term.paste')}
            </button>
            <button role="menuitem" onClick={selectAll}>
              {t('term.selectAll')}
            </button>
            <button role="menuitem" onClick={clearScreen}>
              {t('term.clear')}
            </button>
          </div>
        </>
      )}
    </div>
  );
}
