import { useEffect, useRef, useState } from 'react';
import { FitAddon } from '@xterm/addon-fit';
import { SearchAddon } from '@xterm/addon-search';
import { Terminal as XTerm } from '@xterm/xterm';
import '@xterm/xterm/css/xterm.css';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
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
export function Screen({ kind, sessionId }: Props): JSX.Element {
  const t = useT();
  const ref = useRef<HTMLDivElement>(null);
  const termRef = useRef<XTerm | null>(null);
  const searchRef = useRef<SearchAddon | null>(null);
  const findInputRef = useRef<HTMLInputElement>(null);
  const [showFind, setShowFind] = useState(false);
  const [query, setQuery] = useState('');

  useEffect(() => {
    const el = ref.current;
    if (el === null) return;
    let disposed = false;
    const term = new XTerm({
      cursorBlink: true,
      // Modern, professional monospace stack — prefer ligature-friendly coding
      // faces, fall back through the OS defaults. Slightly airier metrics than
      // xterm's defaults so long sessions read cleanly.
      fontFamily:
        '"JetBrains Mono Variable", "JetBrains Mono", "Cascadia Code", "SF Mono", ui-monospace, "Menlo", "Consolas", "DejaVu Sans Mono", monospace',
      fontSize: 13,
      fontWeight: 400,
      fontWeightBold: 600,
      lineHeight: 1.25,
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
    term.open(el);
    // Ctrl/Cmd+F opens the floating find bar (VS Code terminal idiom) instead of
    // passing to the shell's readline forward-char. Returning false stops xterm
    // from forwarding the key to the pty.
    term.attachCustomKeyEventHandler((e) => {
      if (e.type === 'keydown' && (e.ctrlKey || e.metaKey) && !e.altKey && e.key.toLowerCase() === 'f') {
        if (!disposed) setShowFind(true);
        return false;
      }
      return true;
    });
    // NOTE: intentionally NO WebGL renderer. @xterm/addon-webgl on Windows
    // WebView2 (ANGLE/GL backend) renders a black screen and can wedge the GPU
    // process, freezing the app (director report, v0.3.11 Windows). xterm's
    // default DOM renderer is reliable across all WebView backends; GPU raster
    // waits for a WebView2-safe path.
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

    const unlistenP = onSessionData(kind, sessionId, (b) => term.write(b));
    const exitP = onSessionExit(kind, sessionId, () => {
      if (!disposed) term.write('\r\n\x1b[2m[process exited]\x1b[0m\r\n');
    });
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

      <div className="term-screen" ref={ref} />
    </div>
  );
}
