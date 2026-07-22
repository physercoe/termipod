import { useEffect, useMemo, useRef, useState, type MouseEvent as ReactMouseEvent } from 'react';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import { isShell } from '../platform';
import { useWorkbench } from '../state/workbench';
import {
  addGroup,
  connectionGroup,
  DEFAULT_GROUP,
  deleteConnection,
  listConnections,
  navGroups,
  removeGroup,
  renameGroup,
  setConnectionGroup,
  type Connection,
} from '../state/connections';
import { importSshConfig } from '../ssh/config';
import { sshDuplicate } from '../ssh/tauri';
import { useConfirm } from '../ui/ConfirmModal';
import { useTextPrompt } from '../ui/PromptModal';
import { ResizeHandle, usePanelWidth } from '../ui/ResizeHandle';
import { ConnectForm } from './ConnectForm';
import { ptyOpen } from './pty';
import { SessionView } from './SessionView';
import { useTerminals, type TermTab } from './store';

const NAV_FOLD_KEY = 'termipod.term.navFold';
const GROUP_FOLD_KEY = 'termipod.term.groupFold';

// A right-click target in the connections nav: a whole group, one connection, or
// blank space in the list (below/around the rows).
type NavMenu = {
  x: number;
  y: number;
  target: { kind: 'group'; name: string } | { kind: 'conn'; id: string } | { kind: 'blank' };
};

function msg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

// Persisted dock height/width (#319). Clamped loosely; the drag handler applies
// the live viewport-relative bounds.
function loadDockSize(key: string, fallback: number): number {
  const v = Number(localStorage.getItem(key));
  return Number.isFinite(v) && v >= 140 ? v : fallback;
}
function saveDockSize(key: string, n: number): void {
  try {
    localStorage.setItem(key, String(n));
  } catch {
    /* ignore */
  }
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
  const replaceSession = useTerminals((s) => s.replaceSession);
  const setActive = useTerminals((s) => s.setActive);
  const setOpen = useTerminals((s) => s.setOpen);
  const dockSide = useTerminals((s) => s.dockSide);
  const setDockSide = useTerminals((s) => s.setDockSide);

  const tauri = isShell();
  const [connecting, setConnecting] = useState(false);
  const [initialConnId, setInitialConnId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const cfgRef = useRef<HTMLInputElement>(null);
  // Dock size persists across launches (#319) like the nav width + dock side do.
  const [height, setHeight] = useState(() => loadDockSize('termipod.term.dockH', 340)); // bottom-dock height
  const [width, setWidth] = useState(() => loadDockSize('termipod.term.dockW', 480)); // right-dock width
  // Drag state for the dock resize edge — `axis` follows the dock side (top edge
  // for the bottom dock, left edge for the right dock).
  const dragRef = useRef<{ axis: 'y' | 'x'; start: number; startSize: number } | null>(null);

  // Surface-mode split layout (local — the panel never unmounts, so it persists
  // across job switches like the sessions do). `panes` is the set of session ids
  // tiled in the split; `orientation` is row (side-by-side) or column (stacked).
  const [panes, setPanes] = useState<string[]>([]);
  const [orientation, setOrientation] = useState<'row' | 'column'>('row');
  const [conns, setConns] = useState<Connection[]>(() => (tauri ? listConnections() : []));

  // Left-nav width (persisted, clamped) + fold state.
  const [navW, onNavResize] = usePanelWidth('termipod.term.navW', 210, 150, 420);
  const [navFold, setNavFold] = useState(() => localStorage.getItem(NAV_FOLD_KEY) === '1');

  // Group folding (keyed lowercase, matching the case-insensitive group identity)
  // + a bump to recompute the derived group list after a group-only mutation (a
  // new/renamed/deleted group that doesn't change `conns` on its own).
  const [foldedGroups, setFoldedGroups] = useState<Set<string>>(() => {
    try {
      return new Set(JSON.parse(localStorage.getItem(GROUP_FOLD_KEY) ?? '[]') as string[]);
    } catch {
      return new Set();
    }
  });
  const [groupBump, setGroupBump] = useState(0);
  const [navMenu, setNavMenu] = useState<NavMenu | null>(null);
  const { ask, node: promptNode } = useTextPrompt();
  const { ask: confirmAsk, node: confirmNode } = useConfirm();
  const groups = useMemo(() => (tauri ? navGroups(conns) : []), [conns, groupBump, tauri]);

  function refreshConns(): void {
    setConns(tauri ? listConnections() : []);
    setGroupBump((v) => v + 1);
  }
  function toggleGroupFold(name: string): void {
    setFoldedGroups((prev) => {
      const next = new Set(prev);
      const key = name.toLowerCase();
      if (next.has(key)) next.delete(key);
      else next.add(key);
      try {
        localStorage.setItem(GROUP_FOLD_KEY, JSON.stringify([...next]));
      } catch {
        /* ignore */
      }
      return next;
    });
  }
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

  // A vault sync-down / recovery-restore rewrites the saved connections straight
  // to localStorage (see vault/bundle.ts importBundle). This panel is
  // always-mounted (it owns live PTY/SSH sessions), so without this it would keep
  // showing the pre-sync list until an app restart. Re-read on the broadcast.
  useEffect(() => {
    if (!tauri) return;
    const onImported = (): void => refreshConns();
    window.addEventListener('termipod:vault-imported', onImported);
    return () => window.removeEventListener('termipod:vault-imported', onImported);
    // refreshConns is stable (defined in render, closes over stable setters).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tauri]);

  // Dismiss the "+" menu on an outside click.
  useEffect(() => {
    function onDoc(e: MouseEvent): void {
      if (addRef.current !== null && !addRef.current.contains(e.target as Node)) setAddMenu(false);
    }
    document.addEventListener('mousedown', onDoc);
    return () => document.removeEventListener('mousedown', onDoc);
  }, []);

  // Dismiss the nav context menu on any outside click, scroll, or Escape.
  useEffect(() => {
    if (navMenu === null) return;
    const close = (): void => setNavMenu(null);
    const onKey = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') setNavMenu(null);
    };
    window.addEventListener('click', close);
    window.addEventListener('scroll', close, true);
    window.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('click', close);
      window.removeEventListener('scroll', close, true);
      window.removeEventListener('keydown', onKey);
    };
  }, [navMenu]);

  // Nav context-menu actions. All refresh the local `conns` mirror afterwards.
  // `ask` is the in-app text prompt (window.prompt renders an unreliable native
  // `tauri.localhost` dialog in the webview — see useTextPrompt).
  async function promptNewGroup(assignId?: string): Promise<void> {
    setNavMenu(null);
    const name = await ask(t('term.newGroupPrompt'));
    if (name === null || name.trim() === '') return;
    addGroup(name.trim());
    if (assignId !== undefined) setConnectionGroup(assignId, name.trim());
    refreshConns();
  }
  async function promptRenameGroup(from: string): Promise<void> {
    setNavMenu(null);
    const to = await ask(t('term.renameGroupPrompt'), from);
    if (to === null || to.trim() === '' || to.trim() === from) return;
    renameGroup(from, to.trim());
    refreshConns();
  }
  async function doDeleteGroup(name: string): Promise<void> {
    setNavMenu(null);
    if (!(await confirmAsk({ message: t('term.confirmDeleteGroup').replace('{name}', name), danger: true })))
      return;
    removeGroup(name);
    refreshConns();
  }
  function doMoveToGroup(id: string, group: string): void {
    setNavMenu(null);
    setConnectionGroup(id, group);
    refreshConns();
  }
  async function doDeleteConnection(id: string): Promise<void> {
    setNavMenu(null);
    const name = conns.find((c) => c.id === id)?.name ?? '';
    if (!(await confirmAsk({ message: t('term.confirmDeleteConn').replace('{name}', name), danger: true })))
      return;
    try {
      await deleteConnection(id);
    } finally {
      refreshConns();
    }
  }

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
      const base = t.plural('term.importedConfig', res.count);
      setNotice(
        res.keysAdded > 0 ? `${base} · ${t.plural('term.importedKeys', res.keysAdded)}` : base,
      );
      setTimeout(() => setNotice(null), 4000);
    } catch (ex) {
      setError(msg(ex));
    }
  }

  function onConnected(sessionId: string, title: string, connId?: string): void {
    addTab({ kind: 'ssh', sessionId, title, connId });
    setConnecting(false);
  }

  // Reconnect a dead session (#319). A local shell rebinds in place (a fresh PTY
  // into the same tab, so its <Screen> stays mounted and just re-streams). An SSH
  // tab re-opens the connect flow for its saved connection (credentials may need
  // re-entry), producing a fresh tab.
  async function reconnect(tab: TermTab): Promise<void> {
    if (tab.kind === 'local') {
      try {
        const { id, shell } = await ptyOpen({ cols: 80, rows: 24 });
        replaceSession(tab.id, id, shell);
      } catch (e) {
        setError(msg(e));
      }
      return;
    }
    focusSession(tab.id);
    openConnect(tab.connId);
  }

  // Split the current view (#319): an SSH tab duplicates onto a FRESH channel
  // of the same backend connection (no re-auth — ssh.rs `ssh_duplicate`);
  // anything else spawns a new local shell. The whole group shares one
  // orientation in this round; a nested split tree is a follow-up.
  async function split(o: 'row' | 'column'): Promise<void> {
    setOrientation(o);
    const active = tabs.find((tb) => tb.id === activeId);
    const uiId = active?.kind === 'ssh' ? await duplicateSsh(active) : await newLocal();
    if (uiId !== null) setPanes((p) => (p.includes(uiId) ? p : [...p, uiId]));
  }

  // Split-duplicate an SSH tab: a second interactive shell on the SAME
  // connection, tiled beside the original and titled after it.
  async function duplicateSsh(tab: TermTab): Promise<string | null> {
    setError(null);
    try {
      const sessionId = await sshDuplicate(tab.sessionId, 80, 24);
      const uiId = addTab({ kind: 'ssh', sessionId, title: tab.title, connId: tab.connId });
      setConnecting(false);
      return uiId;
    } catch (e) {
      setError(msg(e));
      return null;
    }
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

  async function close(id: string): Promise<void> {
    // A live SSH session ends a remote shell (and any running process) when its
    // tab closes — confirm first. Local shells are cheap to respawn, so they
    // close immediately.
    const tab = tabs.find((tb) => tb.id === id);
    if (tab?.kind === 'ssh' && !(await confirmAsk({ message: t('term.confirmCloseSession'), danger: true })))
      return;
    setPanes((p) => p.filter((x) => x !== id));
    closeTab(id);
  }

  // Drag the dock's inner edge to resize it (dock mode only; clamped). The bottom
  // dock resizes its height from the top edge; the right dock its width from the
  // left edge.
  useEffect(() => {
    function onMove(e: MouseEvent): void {
      const d = dragRef.current;
      if (d === null) return;
      if (d.axis === 'y') {
        const next = d.startSize + (d.start - e.clientY);
        setHeight(Math.max(140, Math.min(next, window.innerHeight - 160)));
      } else {
        const next = d.startSize + (d.start - e.clientX);
        setWidth(Math.max(260, Math.min(next, window.innerWidth - 200)));
      }
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

  // Persist the dock size (debounced — the drag updates it on every mousemove).
  useEffect(() => {
    const id = setTimeout(() => saveDockSize('termipod.term.dockH', height), 250);
    return () => clearTimeout(id);
  }, [height]);
  useEffect(() => {
    const id = setTimeout(() => saveDockSize('termipod.term.dockW', width), 250);
    return () => clearTimeout(id);
  }, [width]);

  const visible = mode === 'surface' || open;
  const panelClass = `term-panel ${mode}${mode === 'dock' ? ` ${dockSide}` : ''}${visible ? '' : ' hidden'}`;
  const style = mode === 'dock' ? (dockSide === 'right' ? { width } : { height }) : undefined;
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
          onMouseDown={(e) =>
            (dragRef.current =
              dockSide === 'right'
                ? { axis: 'x', start: e.clientX, startSize: width }
                : { axis: 'y', start: e.clientY, startSize: height })
          }
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
        <div
          className="term-nav-list"
          onContextMenu={(e) => {
            // A row / group-header right-click sets its own menu first (bubbles up
            // to here); guard so blank space is the only thing that opens the
            // blank menu — never overwrite a more specific target.
            if ((e.target as HTMLElement).closest('.term-nav-item, .term-nav-group-head') !== null) return;
            e.preventDefault();
            setNavMenu({ x: e.clientX, y: e.clientY, target: { kind: 'blank' } });
          }}
        >
          {conns.length === 0 && groups.length <= 1 && (
            <div className="muted small term-nav-empty">{t('term.noSaved')}</div>
          )}
          {/* With only the default group, list connections flat (no header noise).
              Once a second group exists, show every group as a foldable section. */}
          {groups.length <= 1
            ? conns.map((c) => (
                <ConnRow
                  key={c.id}
                  c={c}
                  active={connecting && initialConnId === c.id}
                  onOpen={() => openConnect(c.id)}
                  onMenu={(e) => {
                    e.preventDefault();
                    setNavMenu({ x: e.clientX, y: e.clientY, target: { kind: 'conn', id: c.id } });
                  }}
                />
              ))
            : groups.map((g) => {
                const gl = g.toLowerCase();
                const gconns = conns.filter((c) => connectionGroup(c).toLowerCase() === gl);
                const folded = foldedGroups.has(gl);
                return (
                  <div key={gl} className="term-nav-group">
                    <button
                      className="term-nav-group-head"
                      onClick={() => toggleGroupFold(g)}
                      onContextMenu={(e) => {
                        e.preventDefault();
                        setNavMenu({ x: e.clientX, y: e.clientY, target: { kind: 'group', name: g } });
                      }}
                    >
                      <Icon name={folded ? 'chevron-right' : 'chevron-down'} size={12} />
                      <span className="term-nav-group-name">{g}</span>
                      <span className="spacer" />
                      <span className="muted small">{gconns.length}</span>
                    </button>
                    {!folded &&
                      gconns.map((c) => (
                        <ConnRow
                          key={c.id}
                          grouped
                          c={c}
                          active={connecting && initialConnId === c.id}
                          onOpen={() => openConnect(c.id)}
                          onMenu={(e) => {
                            e.preventDefault();
                            setNavMenu({ x: e.clientX, y: e.clientY, target: { kind: 'conn', id: c.id } });
                          }}
                        />
                      ))}
                  </div>
                );
              })}
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
                  {tab.unread === true && tab.id !== activeId && (
                    <span className="term-tab-unread" title={t('term.unread')} aria-label={t('term.unread')} />
                  )}
                </button>
                <button className="term-tab-x" title={t('term.closeTab')} onClick={() => void close(tab.id)}>
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
            <>
              <button
                className="term-dock-side"
                title={dockSide === 'right' ? t('term.dockBottom') : t('term.dockRight')}
                onClick={() => setDockSide(dockSide === 'right' ? 'bottom' : 'right')}
              >
                <Icon name={dockSide === 'right' ? 'dock-bottom' : 'dock-right'} size={14} />
              </button>
              <button className="term-dock-hide" title={t('term.hideDock')} onClick={() => setOpen(false)}>
                <Icon name={dockSide === 'right' ? 'chevron-right' : 'chevron-down'} size={15} />
              </button>
            </>
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
                      <SessionView tab={tab} onReconnect={() => void reconnect(tab)} />
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

      {navMenu !== null && (
        <NavContextMenu
          menu={navMenu}
          groups={groups}
          conns={conns}
          onNewConnection={() => {
            setNavMenu(null);
            openConnect();
          }}
          onImportConfig={() => {
            setNavMenu(null);
            cfgRef.current?.click();
          }}
          onNewGroup={promptNewGroup}
          onRenameGroup={promptRenameGroup}
          onDeleteGroup={doDeleteGroup}
          onMoveToGroup={doMoveToGroup}
          onEditConn={(id) => {
            setNavMenu(null);
            openConnect(id);
          }}
          onDeleteConn={(id) => void doDeleteConnection(id)}
        />
      )}
      {promptNode}
      {confirmNode}
    </div>
  );
}

// One connection row in the nav. Extracted so the flat and grouped renderings
// share it; `grouped` indents it under a group header.
function ConnRow({
  c,
  active,
  grouped,
  onOpen,
  onMenu,
}: {
  c: Connection;
  active: boolean;
  grouped?: boolean;
  onOpen: () => void;
  onMenu: (e: ReactMouseEvent) => void;
}): JSX.Element {
  return (
    <button
      className={`term-nav-item term-nav-conn${active ? ' active' : ''}${grouped ? ' grouped' : ''}`}
      title={`${c.username}@${c.host}:${c.port}`}
      onClick={onOpen}
      onContextMenu={onMenu}
    >
      <span className="term-tab-kind ssh" />
      {c.name}
    </button>
  );
}

// The nav right-click menu — blank space (new connection / new group / import),
// a group header menu (new / rename / delete), or a connection menu (edit /
// move-to-group / new group / delete).
function NavContextMenu({
  menu,
  groups,
  conns,
  onNewConnection,
  onImportConfig,
  onNewGroup,
  onRenameGroup,
  onDeleteGroup,
  onMoveToGroup,
  onEditConn,
  onDeleteConn,
}: {
  menu: NavMenu;
  groups: string[];
  conns: Connection[];
  onNewConnection: () => void;
  onImportConfig: () => void;
  onNewGroup: (assignId?: string) => void;
  onRenameGroup: (from: string) => void;
  onDeleteGroup: (name: string) => void;
  onMoveToGroup: (id: string, group: string) => void;
  onEditConn: (id: string) => void;
  onDeleteConn: (id: string) => void;
}): JSX.Element {
  const t = useT();
  const { target } = menu;
  const conn = target.kind === 'conn' ? conns.find((c) => c.id === target.id) : undefined;
  const curGroup = conn !== undefined ? connectionGroup(conn) : DEFAULT_GROUP;
  return (
    <div
      className="term-nav-ctxmenu"
      style={{ left: menu.x, top: menu.y }}
      onContextMenu={(e) => e.preventDefault()}
      onClick={(e) => e.stopPropagation()}
    >
      {target.kind === 'blank' ? (
        <>
          <button className="read-ctx-item" onClick={onNewConnection}>
            <Icon name="plus" size={14} /> {t('term.newConnection')}
          </button>
          <button className="read-ctx-item" onClick={() => onNewGroup()}>
            <Icon name="folder" size={14} /> {t('term.newGroup')}
          </button>
          <div className="read-ctx-sep" />
          <button className="read-ctx-item" onClick={onImportConfig}>
            <Icon name="external" size={14} /> {t('term.importConfig')}
          </button>
        </>
      ) : target.kind === 'group' ? (
        <>
          <button className="read-ctx-item" onClick={() => onNewGroup()}>
            <Icon name="plus" size={14} /> {t('term.newGroup')}
          </button>
          {target.name.toLowerCase() !== DEFAULT_GROUP && (
            <>
              <button className="read-ctx-item" onClick={() => onRenameGroup(target.name)}>
                <Icon name="pen" size={14} /> {t('term.renameGroup')}
              </button>
              <div className="read-ctx-sep" />
              <button className="read-ctx-item danger" onClick={() => onDeleteGroup(target.name)}>
                <Icon name="trash" size={14} /> {t('term.deleteGroup')}
              </button>
            </>
          )}
        </>
      ) : (
        <>
          <button className="read-ctx-item" onClick={() => onEditConn(target.id)}>
            <Icon name="pen" size={14} /> {t('term.editConnection')}
          </button>
          <div className="read-ctx-sep" />
          <div className="term-nav-ctx-label">{t('term.moveToGroup')}</div>
          {groups.map((g) => (
            <button
              key={g}
              className="read-ctx-item"
              disabled={g.toLowerCase() === curGroup.toLowerCase()}
              onClick={() => onMoveToGroup(target.id, g)}
            >
              <Icon name={g.toLowerCase() === curGroup.toLowerCase() ? 'check' : 'folder'} size={14} /> {g}
            </button>
          ))}
          <button className="read-ctx-item" onClick={() => onNewGroup(target.id)}>
            <Icon name="plus" size={14} /> {t('term.newGroup')}
          </button>
          <div className="read-ctx-sep" />
          <button className="read-ctx-item danger" onClick={() => onDeleteConn(target.id)}>
            <Icon name="trash" size={14} /> {t('term.delete')}
          </button>
        </>
      )}
    </div>
  );
}
