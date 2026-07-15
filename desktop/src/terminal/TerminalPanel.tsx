import { useEffect, useRef, useState } from 'react';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import { isTauri } from '../platform';
import { useWorkbench } from '../state/workbench';
import { listConnections, type Connection } from '../state/connections';
import { importSshConfig } from '../ssh/config';
import { ResizeHandle, usePanelWidth } from '../ui/ResizeHandle';
import { ConnectForm } from './ConnectForm';
import { ptyOpen } from './pty';
import { SessionView } from './SessionView';
import { useTerminals } from './store';

const NAV_FOLD_KEY = 'termipod.term.navFold';

function msg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/// The persistent terminal panel (professional-terminal, → ADR-053). Mounted once
/// inside the workbench main for the app's lifetime — never unmounted, because
/// unmounting a `<Screen>` closes its PTY/SSH session. It renders in one of two
/// modes, switched purely by CSS so no `<Screen>` is ever re-parented:
///
///   • **surface** (the `terminal` job is active) — a full job surface: a left
///     nav listing saved SSH connections (resizable + foldable) and a main column
///     whose head is a tab strip of the open sessions over a split-able pane area.
///   • **dock** (any other job) — the compact bottom strip of old, shown only
///     when toggled (Ctrl+`); absolutely positioned so it overlays the active
///     surface without disturbing it.
///
/// Sessions and connections are deliberately *different categories* (director
/// feedback): live sessions are the tabs in the head; saved connections are the
/// left nav. The store (`useTerminals`) owns the sessions; this panel is a view.
export function TerminalPanel(): JSX.Element {
  const t = useT();
  const job = useWorkbench((s) => s.job);
  const open = useTerminals((s) => s.open);
  const tabs = useTerminals((s) => s.tabs);
  const activeId = useTerminals((s) => s.activeId);
  const addTab = useTerminals((s) => s.addTab);
  const closeTab = useTerminals((s) => s.closeTab);
  const setActive = useTerminals((s) => s.setActive);
  const setOpen = useTerminals((s) => s.setOpen);

  const tauri = isTauri();
  const [connecting, setConnecting] = useState(false);
  const [initialConnId, setInitialConnId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const cfgRef = useRef<HTMLInputElement>(null);
  const [height, setHeight] = useState(340);
  const dragRef = useRef<{ startY: number; startH: number } | null>(null);

  // Surface-mode split layout (local — the panel never unmounts, so it persists
  // across job switches like the sessions do). `panes` is the set of session ids
  // tiled in the split; `orientation` is row (side-by-side) or column (stacked).
  const [panes, setPanes] = useState<string[]>([]);
  const [orientation, setOrientation] = useState<'row' | 'column'>('row');
  const [conns, setConns] = useState<Connection[]>(() => (tauri ? listConnections() : []));

  // Left-nav width (persisted, clamped) + fold state.
  const [navW, onNavResize] = usePanelWidth('termipod.term.navW', 210, 150, 420);
  const [navFold, setNavFold] = useState(() => localStorage.getItem(NAV_FOLD_KEY) === '1');
  function toggleFold(): void {
    setNavFold((v) => {
      const n = !v;
      try {
        localStorage.setItem(NAV_FOLD_KEY, n ? '1' : '0');
      } catch {
        /* ignore */
      }
      return n;
    });
  }

  // "+" new-session menu (local shell / SSH).
  const [addMenu, setAddMenu] = useState(false);
  const addRef = useRef<HTMLDivElement>(null);

  const mode: 'surface' | 'dock' = job === 'terminal' ? 'surface' : 'dock';

  // Refresh the saved-connections nav when the connect form closes (a new one may
  // have been added there).
  useEffect(() => {
    if (!connecting && tauri) setConns(listConnections());
  }, [connecting, tauri]);

  // Dismiss the "+" menu on an outside click.
  useEffect(() => {
    function onDoc(e: MouseEvent): void {
      if (addRef.current !== null && !addRef.current.contains(e.target as Node)) setAddMenu(false);
    }
    document.addEventListener('mousedown', onDoc);
    return () => document.removeEventListener('mousedown', onDoc);
  }, []);

  // Seed / prune the split against the live session list. When nothing is tiled
  // yet, fall back to the active session so the surface is never blank with open
  // tabs; drop ids whose session has closed.
  useEffect(() => {
    setPanes((prev) => {
      const live = prev.filter((id) => tabs.some((tb) => tb.id === id));
      if (live.length > 0) return live.length === prev.length ? prev : live;
      return activeId !== null ? [activeId] : [];
    });
  }, [tabs, activeId]);

  const paneShown = (id: string): boolean =>
    mode === 'surface' ? panes.includes(id) : !connecting && id === activeId;

  async function newLocal(): Promise<string | null> {
    setError(null);
    setAddMenu(false);
    try {
      const { id, shell } = await ptyOpen({ cols: 80, rows: 24 });
      const uiId = addTab({ kind: 'local', sessionId: id, shell, title: t('term.localShell') });
      setConnecting(false);
      return uiId;
    } catch (e) {
      setError(msg(e));
      return null;
    }
  }

  function openConnect(connId?: string): void {
    setInitialConnId(connId ?? null);
    setConnecting(true);
    setAddMenu(false);
  }

  // Import an OpenSSH client config (~/.ssh/config) into saved connections. Read
  // in the web layer via the File API (no fs plugin); re-import updates in place.
  async function onImportConfig(e: React.ChangeEvent<HTMLInputElement>): Promise<void> {
    const file = e.target.files?.[0];
    e.target.value = ''; // allow re-picking the same file
    if (file === undefined) return;
    setError(null);
    try {
      const res = await importSshConfig(await file.text(), listConnections());
      setConns(listConnections());
      const base = t('term.importedConfig').replace('{n}', String(res.count));
      setNotice(
        res.keysAdded > 0 ? `${base} · ${t('term.importedKeys').replace('{n}', String(res.keysAdded))}` : base,
      );
      setTimeout(() => setNotice(null), 4000);
    } catch (ex) {
      setError(msg(ex));
    }
  }

  function onConnected(sessionId: string, title: string): void {
    addTab({ kind: 'ssh', sessionId, title });
    setConnecting(false);
  }

  // Split the current view: spawn a fresh local shell into a new tile in the
  // chosen orientation (the whole group shares one orientation in this round; a
  // nested split tree is a follow-up).
  async function split(o: 'row' | 'column'): Promise<void> {
    setOrientation(o);
    const uiId = await newLocal();
    if (uiId !== null) setPanes((p) => (p.includes(uiId) ? p : [...p, uiId]));
  }

  // Show a session as the sole tile (tab click) — or just focus it if it's
  // already tiled.
  function focusSession(id: string): void {
    setActive(id);
    setConnecting(false);
    if (mode === 'surface') setPanes((p) => (p.includes(id) ? p : [id]));
  }

  function closePaneAt(id: string): void {
    setPanes((p) => p.filter((x) => x !== id));
  }

  function close(id: string): void {
    setPanes((p) => p.filter((x) => x !== id));
    closeTab(id);
  }

  // Drag the top edge to resize the dock height (dock mode only; clamped).
  useEffect(() => {
    function onMove(e: MouseEvent): void {
      const d = dragRef.current;
      if (d === null) return;
      const next = d.startH + (d.startY - e.clientY);
      setHeight(Math.max(140, Math.min(next, window.innerHeight - 160)));
    }
    function onUp(): void {
      dragRef.current = null;
    }
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
    return () => {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };
  }, []);

  const visible = mode === 'surface' || open;
  const panelClass = `term-panel ${mode}${visible ? '' : ' hidden'}`;
  const style = mode === 'dock' ? { height } : undefined;
  const navStyle = mode === 'surface' && !navFold ? { width: navW } : undefined;

  const addMenuEl = (
    <div className="term-add" ref={addRef}>
      <button
        className="term-add-btn"
        title={t('term.newSession')}
        disabled={!tauri}
        onClick={() => setAddMenu((v) => !v)}
      >
        <Icon name="plus" size={14} />
      </button>
      {addMenu && (
        <div className="term-add-menu">
          <button onClick={() => void newLocal()}>{t('term.localShell')}</button>
          <button onClick={() => openConnect()}>{t('term.ssh')}</button>
        </div>
      )}
    </div>
  );

  return (
    <div className={panelClass} style={style}>
      {mode === 'dock' && (
        <div
          className="term-dock-resize"
          onMouseDown={(e) => (dragRef.current = { startY: e.clientY, startH: height })}
        />
      )}

      {/* Left nav — saved connections/hosts (surface mode; CSS-hidden in dock).
          Always rendered so the pane area keeps a stable DOM position and its
          <Screen>s are never re-parented. */}
      <aside className={`term-nav${navFold ? ' folded' : ''}`} style={navStyle}>
        <div className="term-nav-head">
          <span>{t('term.navConnections')}</span>
          <button className="term-nav-fold" title={t('term.foldNav')} onClick={toggleFold}>
            <Icon name="chevron-left" size={14} />
          </button>
        </div>
        <div className="term-nav-actions">
          <button className="term-nav-new" disabled={!tauri} onClick={() => openConnect()}>
            <Icon name="plus" size={13} />
            {t('term.newConnection')}
          </button>
          <button
            className="term-nav-import"
            disabled={!tauri}
            aria-label={t('term.importConfig')}
            title={t('term.importConfigHint')}
            onClick={() => cfgRef.current?.click()}
          >
            <Icon name="external" size={13} />
          </button>
          {/* No `accept` filter — the OpenSSH config file is literally named
              `config` with no extension, which an extension filter would hide;
              the user picks it from wherever it lives via the native dialog. */}
          <input ref={cfgRef} type="file" hidden onChange={(e) => void onImportConfig(e)} />
        </div>
        {notice !== null && <div className="muted small term-nav-notice">{notice}</div>}
        {error !== null && <div className="error small term-nav-notice">{error}</div>}
        <div className="term-nav-list">
          {conns.length === 0 && <div className="muted small term-nav-empty">{t('term.noSaved')}</div>}
          {conns.map((c) => (
            <button
              key={c.id}
              className={`term-nav-item term-nav-conn${
                connecting && initialConnId === c.id ? ' active' : ''
              }`}
              title={`${c.username}@${c.host}:${c.port}`}
              onClick={() => openConnect(c.id)}
            >
              <span className="term-tab-kind ssh" />
              {c.name}
            </button>
          ))}
        </div>
      </aside>
      {mode === 'surface' && !navFold && <ResizeHandle onResize={onNavResize} />}

      <div className="term-main">
        <div className="term-head">
          {mode === 'surface' && (
            <button
              className="term-nav-reveal"
              title={t('term.foldNav')}
              onClick={toggleFold}
            >
              <Icon name={navFold ? 'chevron-right' : 'chevron-left'} size={14} />
            </button>
          )}

          <div className="term-tabs">
            {tabs.length === 0 && <span className="muted small term-tabs-empty">{t('term.navNoSessions')}</span>}
            {tabs.map((tab) => (
              <div key={tab.id} className={!connecting && tab.id === activeId ? 'term-tab active' : 'term-tab'}>
                <button className="term-tab-pick" title={tab.title} onClick={() => focusSession(tab.id)}>
                  <span className={`term-tab-kind ${tab.kind}`} />
                  {tab.title}
                </button>
                <button className="term-tab-x" title={t('term.closeTab')} onClick={() => close(tab.id)}>
                  <Icon name="close" size={13} />
                </button>
              </div>
            ))}
          </div>

          {addMenuEl}
          <span className="spacer" />

          {mode === 'surface' && tauri && (
            <span className="term-split-ctl">
              <button
                className={orientation === 'row' ? 'active' : ''}
                title={t('term.splitRight')}
                onClick={() => void split('row')}
                disabled={activeId === null}
              >
                <Icon name="split-h" size={14} />
              </button>
              <button
                className={orientation === 'column' ? 'active' : ''}
                title={t('term.splitDown')}
                onClick={() => void split('column')}
                disabled={activeId === null}
              >
                <Icon name="split-v" size={14} />
              </button>
            </span>
          )}

          {mode === 'dock' && (
            <button className="term-dock-hide" title={t('term.hideDock')} onClick={() => setOpen(false)}>
              <Icon name="chevron-down" />
            </button>
          )}
        </div>

        <div className={`term-panes ${orientation}`}>
          {!tauri ? (
            <div className="term-banner">{t('term.desktopOnly')}</div>
          ) : (
            <>
              {tabs.map((tab) => {
                const shown = paneShown(tab.id);
                return (
                  <div
                    key={tab.id}
                    className={`term-pane${shown ? '' : ' hidden'}${
                      shown && mode === 'surface' && tab.id === activeId && panes.length > 1 ? ' focused' : ''
                    }`}
                    onMouseDown={() => mode === 'surface' && setActive(tab.id)}
                  >
                    {mode === 'surface' && panes.length > 1 && (
                      <div className="term-pane-head">
                        <span className={`term-tab-kind ${tab.kind}`} />
                        <span className="term-pane-title">{tab.title}</span>
                        <span className="spacer" />
                        <button
                          className="term-pane-x"
                          title={t('term.closePane')}
                          onClick={() => closePaneAt(tab.id)}
                        >
                          <Icon name="close" size={12} />
                        </button>
                      </div>
                    )}
                    <div className="term-pane-body">
                      <SessionView tab={tab} />
                    </div>
                  </div>
                );
              })}

              {connecting && (
                <div className="term-pane term-pane-overlay">
                  {/* Keyed on the picked connection so selecting a different host
                      in the nav remounts the form with that host prefilled (a bare
                      prop change wouldn't re-run the mount-time prefill). */}
                  <ConnectForm
                    key={initialConnId ?? '__new__'}
                    initialConnId={initialConnId ?? undefined}
                    onConnected={onConnected}
                    onCancel={() => setConnecting(false)}
                  />
                </div>
              )}

              {!connecting && tabs.length === 0 && (
                <div className="term-empty">
                  <p className="muted">{t('term.emptyHint')}</p>
                  {error !== null && <div className="error">{error}</div>}
                  <div className="term-empty-actions">
                    <button className="primary" onClick={() => void newLocal()}>
                      + {t('term.localShell')}
                    </button>
                    <button onClick={() => openConnect()}>+ {t('term.ssh')}</button>
                  </div>
                </div>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}
