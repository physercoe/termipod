import { useEffect, useRef, useState } from 'react';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import { isTauri } from '../platform';
import { ConnectForm } from './ConnectForm';
import { ptyOpen } from './pty';
import { SessionView } from './SessionView';
import { useTerminals } from './store';
import { useWorkspace } from '../state/workspace';

const AGENT_CMD_KEY = 'termipod.localAgent.termCmd';

function msg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/// The persistent terminal dock (professional-terminal, → ADR-053). Mounted once
/// at the shell's bottom for the app's lifetime and shown/hidden via a class, so
/// live sessions and scrollback survive toggling (Ctrl+`) and tab switches. Tabs
/// are local shells (pty.rs) and SSH sessions (ssh.rs / ConnectForm); all tab
/// panes stay mounted, with inactive/hidden ones parked behind `display:none` —
/// never unmounted, since unmounting a `<Screen>` closes its session.
export function TerminalDock(): JSX.Element {
  const t = useT();
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

  // Drag the top edge to resize the dock height (clamped to a sane band).
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

  async function newLocal(): Promise<void> {
    setError(null);
    try {
      const { id, shell } = await ptyOpen({ cols: 80, rows: 24 });
      addTab({ kind: 'local', sessionId: id, shell, title: t('term.localShell') });
      setConnecting(false);
    } catch (e) {
      setError(msg(e));
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

  return (
    <div className={open ? 'term-dock' : 'term-dock hidden'} style={{ height }}>
      <div className="term-dock-resize" onMouseDown={(e) => (dragRef.current = { startY: e.clientY, startH: height })} />
      <div className="term-dock-head">
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
              <button className="term-tab-x" title={t('term.closeTab')} onClick={() => closeTab(tab.id)}>
                <Icon name="close" size={13} />
              </button>
            </div>
          ))}
        </div>
        <span className="term-dock-add">
          <button onClick={() => void newLocal()} disabled={!tauri} title={t('term.newLocalHint')}>
            <Icon name="plus" size={13} />
            {t('term.localShell')}
          </button>
          <button className={connecting ? 'active' : ''} onClick={() => setConnecting(true)} disabled={!tauri}>
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
        </span>
        <span className="spacer" />
        <button className="term-dock-hide" title={t('term.hideDock')} onClick={() => setOpen(false)}>
          <Icon name="chevron-down" />
        </button>
      </div>

      <div className="term-dock-body">
        {!tauri ? (
          <div className="term-banner">{t('term.desktopOnly')}</div>
        ) : (
          <>
            {tabs.map((tab) => (
              <div
                key={tab.id}
                className={!connecting && !agentForm && tab.id === activeId ? 'dock-pane' : 'dock-pane hidden'}
              >
                <SessionView tab={tab} />
              </div>
            ))}
            {connecting && (
              <div className="dock-pane">
                <ConnectForm onConnected={onConnected} onCancel={() => setConnecting(false)} />
              </div>
            )}
            {agentForm && (
              <div className="dock-pane">
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
  );
}
