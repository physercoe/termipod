import { useEffect, useRef, useState } from 'react';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import { isTauri } from '../platform';
import { useWorkbench } from '../state/workbench';
import { listConnections, type Connection } from '../state/connections';
import { ConnectForm } from './ConnectForm';
import { ptyOpen } from './pty';
import { SessionView } from './SessionView';
import { useTerminals } from './store';
import { useWorkspace } from '../state/workspace';

const AGENT_CMD_KEY = 'termipod.localAgent.termCmd';

function msg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/// The persistent terminal panel (professional-terminal, → ADR-053). Mounted once
/// inside the workbench main for the app's lifetime — never unmounted, because
/// unmounting a `<Screen>` closes its PTY/SSH session. It renders in one of two
/// modes, switched purely by CSS so no `<Screen>` is ever re-parented:
///
///   • **surface** (the `terminal` job is active) — a full job surface: a left
///     nav (open sessions + saved SSH connections + spawn buttons) and a split
///     pane area that tiles several live sessions side-by-side (row) or stacked
///     (column).
///   • **dock** (any other job) — the compact bottom strip of old, shown only
///     when toggled (Ctrl+`); absolutely positioned so it overlays the active
///     surface without disturbing it.
///
/// The store (`useTerminals`) owns the sessions; this panel is a view over them,
/// so both modes read the same `tabs`. The split layout is local state and, since
/// the panel never unmounts, it survives job switches like the sessions do.
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
  const folder = useWorkspace((s) => s.folder);
  const [connecting, setConnecting] = useState(false);
  const [agentForm, setAgentForm] = useState(false);
  const [agentCmd, setAgentCmd] = useState(() => localStorage.getItem(AGENT_CMD_KEY) ?? 'claude');
  const [error, setError] = useState<string | null>(null);
  const [height, setHeight] = useState(340);
  const dragRef = useRef<{ startY: number; startH: number } | null>(null);

  // Surface-mode split layout (local — the panel never unmounts, so it persists
  // across job switches like the sessions do). `panes` is the set of session ids
  // tiled in the split; `orientation` is row (side-by-side) or column (stacked).
  const [panes, setPanes] = useState<string[]>([]);
  const [orientation, setOrientation] = useState<'row' | 'column'>('row');
  const [conns, setConns] = useState<Connection[]>(() => (tauri ? listConnections() : []));

  const mode: 'surface' | 'dock' = job === 'terminal' ? 'surface' : 'dock';

  // Refresh the saved-connections nav when the connect form closes (a new one may
  // have been added there).
  useEffect(() => {
    if (!connecting && tauri) setConns(listConnections());
  }, [connecting, tauri]);

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
    mode === 'surface' ? panes.includes(id) : !connecting && !agentForm && id === activeId;

  async function newLocal(): Promise<string | null> {
    setError(null);
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

  // Open a local *agent* CLI in a PTY (ConPTY on Windows) — the agent's full,
  // native interactive TUI, run in the open workspace folder. The engine binary +
  // flags are split into argv (no shell string → no injection); pty.rs routes an
  // npm shim through cmd.exe on Windows. The tab is marked `agent` so no OSC-133
  // shell-integration script is ever injected into the agent's prompt.
  async function newAgent(): Promise<void> {
    setError(null);
    const parts = agentCmd.trim().split(/\s+/).filter(Boolean);
    if (parts.length === 0) return;
    try {
      localStorage.setItem(AGENT_CMD_KEY, agentCmd.trim());
    } catch {
      /* ignore */
    }
    try {
      const { id, shell } = await ptyOpen({
        shell: parts[0],
        args: parts.slice(1),
        cwd: folder ?? undefined,
        cols: 80,
        rows: 24,
      });
      addTab({ kind: 'local', sessionId: id, shell, title: parts[0], agent: true });
      setAgentForm(false);
    } catch (e) {
      setError(msg(e));
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

  // Show a session as the sole tile (nav click) — or just focus it if it's
  // already tiled.
  function focusSession(id: string): void {
    setActive(id);
    setConnecting(false);
    setAgentForm(false);
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

  const spawnButtons = (
    <>
      <button onClick={() => void newLocal()} disabled={!tauri} title={t('term.newLocalHint')}>
        <Icon name="plus" size={13} />
        {t('term.localShell')}
      </button>
      <button
        className={connecting ? 'active' : ''}
        onClick={() => {
          setConnecting(true);
          setAgentForm(false);
        }}
        disabled={!tauri}
      >
        <Icon name="plus" size={13} />
        {t('term.ssh')}
      </button>
      <button
        className={agentForm ? 'active' : ''}
        onClick={() => {
          setAgentForm(true);
          setConnecting(false);
        }}
        disabled={!tauri}
        title={t('term.newAgentHint')}
      >
        <Icon name="plus" size={13} />
        {t('term.agent')}
      </button>
    </>
  );

  return (
    <div className={panelClass} style={style}>
      {mode === 'dock' && (
        <div
          className="term-dock-resize"
          onMouseDown={(e) => (dragRef.current = { startY: e.clientY, startH: height })}
        />
      )}

      {/* Left nav — always rendered (CSS-hidden in dock mode) so the pane area
          keeps a stable DOM position and its <Screen>s are never re-parented. */}
      <aside className="term-nav">
        <div className="term-nav-head">{t('job.terminal')}</div>
        <div className="term-nav-actions">{spawnButtons}</div>
        <div className="term-nav-section">{t('term.navSessions')}</div>
        <div className="term-nav-list">
          {tabs.length === 0 && <div className="muted small term-nav-empty">{t('term.navNoSessions')}</div>}
          {tabs.map((tab) => (
            <div key={tab.id} className={tab.id === activeId ? 'term-nav-item active' : 'term-nav-item'}>
              <button className="term-nav-pick" title={tab.title} onClick={() => focusSession(tab.id)}>
                <span className={`term-tab-kind ${tab.kind}`} />
                {tab.title}
              </button>
              <button className="term-nav-x" title={t('term.closeTab')} onClick={() => close(tab.id)}>
                <Icon name="close" size={12} />
              </button>
            </div>
          ))}
        </div>
        {tauri && conns.length > 0 && (
          <>
            <div className="term-nav-section">{t('term.navConnections')}</div>
            <div className="term-nav-list">
              {conns.map((c) => (
                <button
                  key={c.id}
                  className="term-nav-item term-nav-conn"
                  title={`${c.username}@${c.host}:${c.port}`}
                  onClick={() => {
                    setConnecting(true);
                    setAgentForm(false);
                  }}
                >
                  <span className="term-tab-kind ssh" />
                  {c.name}
                </button>
              ))}
            </div>
          </>
        )}
      </aside>

      <div className="term-main">
        <div className="term-head">
          {mode === 'dock' ? (
            <div className="term-tabs">
              {tabs.map((tab) => (
                <div key={tab.id} className={!connecting && tab.id === activeId ? 'term-tab active' : 'term-tab'}>
                  <button
                    className="term-tab-pick"
                    onClick={() => {
                      setActive(tab.id);
                      setConnecting(false);
                    }}
                  >
                    <span className={`term-tab-kind ${tab.kind}`} />
                    {tab.title}
                  </button>
                  <button className="term-tab-x" title={t('term.closeTab')} onClick={() => close(tab.id)}>
                    <Icon name="close" size={13} />
                  </button>
                </div>
              ))}
            </div>
          ) : (
            <span className="term-head-title">{activeTitle(tabs, activeId, t('term.terminal'))}</span>
          )}

          {mode === 'dock' && <span className="term-dock-add">{spawnButtons}</span>}
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
                  <ConnectForm onConnected={onConnected} onCancel={() => setConnecting(false)} />
                </div>
              )}
              {agentForm && (
                <div className="term-pane term-pane-overlay">
                  <div className="agent-form">
                    <div className="agent-form-title">{t('term.openAgent')}</div>
                    <label className="agent-form-row">
                      <span>{t('term.agentCmd')}</span>
                      <input
                        className="mono"
                        value={agentCmd}
                        autoFocus
                        spellCheck={false}
                        placeholder="claude"
                        onChange={(e) => setAgentCmd(e.target.value)}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter') void newAgent();
                        }}
                      />
                    </label>
                    <div className="muted small mono agent-form-cwd">
                      {folder !== null ? t('term.agentCwd').replace('{dir}', folder) : t('term.agentCwdNone')}
                    </div>
                    <div className="agent-form-actions">
                      <button className="primary" onClick={() => void newAgent()}>
                        {t('term.openAgent')}
                      </button>
                      <button onClick={() => setAgentForm(false)}>{t('term.agentCancel')}</button>
                    </div>
                    {error !== null && <div className="error">{error}</div>}
                    <div className="muted small">{t('term.agentCmdHint')}</div>
                  </div>
                </div>
              )}

              {!connecting && !agentForm && tabs.length === 0 && (
                <div className="term-empty">
                  <p className="muted">{t('term.emptyHint')}</p>
                  {error !== null && <div className="error">{error}</div>}
                  <div className="term-empty-actions">
                    <button className="primary" onClick={() => void newLocal()}>
                      + {t('term.localShell')}
                    </button>
                    <button onClick={() => setConnecting(true)}>+ {t('term.ssh')}</button>
                    <button onClick={() => setAgentForm(true)}>+ {t('term.agent')}</button>
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

function activeTitle(
  tabs: { id: string; title: string }[],
  activeId: string | null,
  fallback: string,
): string {
  const a = tabs.find((tb) => tb.id === activeId);
  return a?.title ?? fallback;
}
