import { useEffect, useRef, useState } from 'react';
import { FitAddon } from '@xterm/addon-fit';
import { SearchAddon } from '@xterm/addon-search';
import { SerializeAddon } from '@xterm/addon-serialize';
import { Terminal as XTerm } from '@xterm/xterm';
import '@xterm/xterm/css/xterm.css';
import { useT } from '../i18n';
import {
  onSessionData,
  onSessionExit,
  sessionClose,
  sessionResize,
  sessionStart,
  sessionWrite,
  type TermKind,
} from './backend';
import { ShellIntegration, shellIntegrationScript, type CommandBlock } from './osc133';

interface Props {
  kind: TermKind;
  sessionId: string;
  /** Auto-inject shell integration shortly after mount (local shells). */
  autoIntegrate?: boolean;
  /** Whether the OSC-133 (bash/zsh) integration can run in this shell at all —
   *  false for cmd.exe / PowerShell, so the "Enable blocks" button is hidden and
   *  no script is ever injected. Defaults true (SSH / POSIX). */
  canIntegrate?: boolean;
}

function fmtDuration(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)}ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  const m = Math.floor(ms / 60_000);
  const s = Math.round((ms % 60_000) / 1000);
  return `${m}m${s.toString().padStart(2, '0')}s`;
}

/// A live xterm.js screen bound to one session (SSH or local), with the fit +
/// search + serialize addons and OSC 133 command-block tracking (no WebGL — see
/// the renderer note in the mount effect). The
/// effect depends on `sessionId` ALONE: its cleanup calls sessionClose(), so
/// re-running it on a parent re-render would tear the session down (the black-
/// terminal / dead-input bug fixed in desktop-v0.3.10). Unmount closes the
/// session — which now only happens when the owning tab is closed, since the dock
/// hides inactive tabs via CSS rather than unmounting them.
export function Screen({ kind, sessionId, autoIntegrate = false, canIntegrate = true }: Props): JSX.Element {
  const t = useT();
  const ref = useRef<HTMLDivElement>(null);
  const termRef = useRef<XTerm | null>(null);
  const searchRef = useRef<SearchAddon | null>(null);
  const integRef = useRef<ShellIntegration | null>(null);
  const navCursor = useRef(0);
  const [blocks, setBlocks] = useState<CommandBlock[]>([]);
  const [showList, setShowList] = useState(false);
  const [query, setQuery] = useState('');
  const [integrated, setIntegrated] = useState(false);

  useEffect(() => {
    const el = ref.current;
    if (el === null) return;
    let disposed = false;
    const term = new XTerm({
      cursorBlink: true,
      fontFamily: 'ui-monospace, "SF Mono", "JetBrains Mono", Menlo, monospace',
      fontSize: 13,
      theme: { background: '#0a0c10', foreground: '#e6edf3' },
    });
    const fit = new FitAddon();
    const search = new SearchAddon();
    const serialize = new SerializeAddon();
    term.loadAddon(fit);
    term.loadAddon(search);
    term.loadAddon(serialize);
    term.open(el);
    // NOTE: intentionally NO WebGL renderer. @xterm/addon-webgl on Windows
    // WebView2 (ANGLE/GL backend) renders a black screen and can wedge the GPU
    // process, freezing the app (director report, v0.3.11 Windows). xterm's
    // default DOM renderer is reliable across all WebView backends; GPU raster
    // waits for a WebView2-safe path.
    termRef.current = term;
    searchRef.current = search;
    integRef.current = new ShellIntegration(term, (b) => {
      if (!disposed) setBlocks(b);
    });

    // Re-fit + repaint whenever the container has a real size (initial open,
    // window/panel resize, and — critically — when un-hidden after a tab switch:
    // while `display:none` the element is 0×0 and xterm can't lay out, so the
    // buffered prompt reads as a black screen until we re-fit and refresh).
    let lastCols = 0;
    let lastRows = 0;
    let wasVisible = false;
    const refit = (): void => {
      if (disposed || el.clientWidth === 0 || el.clientHeight === 0) {
        wasVisible = false;
        return;
      }
      try {
        fit.fit();
      } catch {
        return;
      }
      if (term.cols !== lastCols || term.rows !== lastRows) {
        lastCols = term.cols;
        lastRows = term.rows;
        void sessionResize(kind, sessionId, term.cols, term.rows);
      }
      term.refresh(0, term.rows - 1);
      if (!wasVisible) {
        wasVisible = true;
        term.focus();
      }
    };
    refit();

    const onData = term.onData((s) => void sessionWrite(kind, sessionId, s));
    const ro = new ResizeObserver(() => refit());
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

    // Local shells: enable command blocks automatically once the shell is ready.
    let autoTimer: ReturnType<typeof setTimeout> | undefined;
    if (autoIntegrate) {
      autoTimer = setTimeout(() => {
        if (disposed) return;
        void sessionWrite(kind, sessionId, ` ${shellIntegrationScript}\r`);
        setIntegrated(true);
      }, 500);
    }

    return () => {
      disposed = true;
      if (autoTimer !== undefined) clearTimeout(autoTimer);
      ro.disconnect();
      onData.dispose();
      integRef.current?.dispose();
      integRef.current = null;
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

  function enableIntegration(): void {
    if (integrated) return;
    // Leading space so shells with HISTCONTROL=ignorespace don't save the line.
    void sessionWrite(kind, sessionId, ` ${shellIntegrationScript}\r`);
    setIntegrated(true);
  }

  function scrollToBlock(block: CommandBlock): void {
    const term = termRef.current;
    const line = block.promptMarker?.line;
    if (term === null || line === undefined) return;
    term.scrollLines(line - term.buffer.active.viewportY);
  }

  function jump(delta: number): void {
    if (blocks.length === 0) return;
    navCursor.current = Math.max(0, Math.min(blocks.length - 1, navCursor.current + delta));
    scrollToBlock(blocks[navCursor.current]);
  }

  function runSearch(dir: 1 | -1): void {
    const q = query.trim();
    if (q === '' || searchRef.current === null) return;
    if (dir === 1) searchRef.current.findNext(q);
    else searchRef.current.findPrevious(q);
  }

  async function copyOutput(block: CommandBlock): Promise<void> {
    const text = integRef.current?.getOutput(block) ?? '';
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      /* clipboard blocked — no-op */
    }
  }

  const recent = blocks.slice(-14);

  return (
    <div className="term-screen-wrap">
      <div className="term-nav">
        <input
          className="term-search"
          placeholder={t('term.search')}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') runSearch(e.shiftKey ? -1 : 1);
          }}
        />
        <button className="term-nav-btn" title={t('term.searchPrev')} onClick={() => runSearch(-1)}>
          ↑
        </button>
        <button className="term-nav-btn" title={t('term.searchNext')} onClick={() => runSearch(1)}>
          ↓
        </button>
        <span className="term-nav-sep" />
        <button className="term-nav-btn" title={t('term.prevCmd')} onClick={() => jump(-1)} disabled={blocks.length === 0}>
          ⌃
        </button>
        <button className="term-nav-btn" title={t('term.nextCmd')} onClick={() => jump(1)} disabled={blocks.length === 0}>
          ⌄
        </button>
        <button
          className={showList ? 'term-nav-btn active' : 'term-nav-btn'}
          onClick={() => setShowList((v) => !v)}
          title={t('term.blocks')}
        >
          ⌗ {blocks.length}
        </button>
        <span className="spacer" />
        {canIntegrate && !integrated && (
          <button className="term-nav-btn" onClick={enableIntegration} title={t('term.enableBlocksHint')}>
            {t('term.enableBlocks')}
          </button>
        )}
      </div>
      {showList && (
        <div className="term-blocks">
          {recent.length === 0 && <span className="muted small">{t('term.noBlocks')}</span>}
          {recent.map((b) => {
            const state = b.running ? 'run' : b.exitCode === 0 ? 'ok' : b.exitCode === null ? 'na' : 'err';
            const dur = b.endedAt !== null ? fmtDuration(b.endedAt - b.startedAt) : b.running ? '…' : '';
            return (
              <div key={b.id} className="term-block" onClick={() => scrollToBlock(b)}>
                <span className={`term-block-dot ${state}`} />
                <span className="term-block-cmd">{b.command || t('term.command')}</span>
                {dur !== '' && <span className="term-block-dur">{dur}</span>}
                {b.exitCode !== null && b.exitCode !== 0 && <span className="term-block-code">{b.exitCode}</span>}
                <button
                  className="term-block-copy"
                  title={t('term.copyOutput')}
                  onClick={(e) => {
                    e.stopPropagation();
                    void copyOutput(b);
                  }}
                >
                  ⧉
                </button>
              </div>
            );
          })}
        </div>
      )}
      <div className="term-screen" ref={ref} />
    </div>
  );
}
