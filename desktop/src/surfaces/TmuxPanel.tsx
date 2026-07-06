import { useCallback, useEffect, useState } from 'react';
import { sshExec } from '../ssh/tauri';
import { TmuxCmd } from '../tmux/commands';
import { parsePanes, parseSessions, parseWindows, type TmuxPane, type TmuxSession, type TmuxWindow } from '../tmux/parser';
import { useT } from '../i18n';

/// tmux management panel (parity — mobile terminal/widgets tmux dialogs). Drives
/// the SSH exec channel (`sshExec`) to browse sessions → windows → panes and run
/// management ops (new/kill/rename/split/send-keys/capture). The live pane render
/// stays in the xterm PTY; this is the structural control surface beside it.
function msg(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

export function TmuxPanel({ sessionId }: { sessionId: string }): JSX.Element {
  const t = useT();
  const [sessions, setSessions] = useState<TmuxSession[]>([]);
  const [sel, setSel] = useState<string | null>(null);
  const [windows, setWindows] = useState<TmuxWindow[]>([]);
  const [selWin, setSelWin] = useState<number | null>(null);
  const [panes, setPanes] = useState<TmuxPane[]>([]);
  const [capture, setCapture] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const run = useCallback(async (cmd: string): Promise<string | null> => {
    setErr(null);
    try {
      return await sshExec(sessionId, cmd);
    } catch (e) {
      setErr(msg(e));
      return null;
    }
  }, [sessionId]);

  const loadSessions = useCallback(async (): Promise<void> => {
    setBusy(true);
    const out = await run(TmuxCmd.listSessions());
    setBusy(false);
    if (out !== null) setSessions(parseSessions(out));
  }, [run]);

  const loadWindows = useCallback(async (session: string): Promise<void> => {
    const out = await run(TmuxCmd.listWindows(session));
    if (out !== null) setWindows(parseWindows(out));
  }, [run]);

  const loadPanes = useCallback(async (session: string, win: number): Promise<void> => {
    const out = await run(TmuxCmd.listPanes(session, win));
    if (out !== null) setPanes(parsePanes(out));
  }, [run]);

  useEffect(() => {
    void loadSessions();
  }, [loadSessions]);

  useEffect(() => {
    if (sel !== null) void loadWindows(sel);
    else setWindows([]);
    setSelWin(null);
    setPanes([]);
  }, [sel, loadWindows]);

  useEffect(() => {
    if (sel !== null && selWin !== null) void loadPanes(sel, selWin);
    else setPanes([]);
  }, [sel, selWin, loadPanes]);

  async function act(cmd: string, refresh: () => Promise<void>): Promise<void> {
    const out = await run(cmd);
    if (out !== null) await refresh();
  }

  return (
    <div className="tmux-panel">
      <div className="tmux-col">
        <div className="tmux-col-head">
          <strong>{t('tmux.sessions')}</strong>
          <span className="spacer" />
          <button
            disabled={busy}
            onClick={() =>
              void (async () => {
                const name = window.prompt(t('tmux.newSessionName'));
                if (name !== null && name.trim() !== '') await act(TmuxCmd.newSession(name.trim()), loadSessions);
              })()
            }
          >
            + {t('tmux.new')}
          </button>
          <button disabled={busy} onClick={() => void loadSessions()}>
            {t('tmux.refresh')}
          </button>
        </div>
        {sessions.map((s) => (
          <div key={s.name} className={s.name === sel ? 'tmux-row active' : 'tmux-row'}>
            <button className="tmux-pick" onClick={() => setSel(s.name)}>
              <span className="tmux-name">{s.name}</span>
              <span className="muted small">
                {s.windows}w{s.attached ? ` · ${t('tmux.attached')}` : ''}
              </span>
            </button>
            <button
              className="link-btn"
              onClick={() =>
                void (async () => {
                  const to = window.prompt(t('tmux.renameTo'), s.name);
                  if (to !== null && to.trim() !== '') await act(TmuxCmd.renameSession(s.name, to.trim()), loadSessions);
                })()
              }
            >
              {t('tmux.rename')}
            </button>
            <button className="link-btn danger" onClick={() => void act(TmuxCmd.killSession(s.name), loadSessions)}>
              {t('tmux.kill')}
            </button>
          </div>
        ))}
        {sessions.length === 0 && !busy && <div className="muted small region-pad">{t('tmux.none')}</div>}
      </div>

      <div className="tmux-col">
        <div className="tmux-col-head">
          <strong>{t('tmux.windows')}</strong>
          <span className="spacer" />
          {sel !== null && (
            <button onClick={() => void act(TmuxCmd.newWindow(sel), () => loadWindows(sel))}>+ {t('tmux.new')}</button>
          )}
        </div>
        {windows.map((w) => (
          <div key={w.index} className={w.index === selWin ? 'tmux-row active' : 'tmux-row'}>
            <button className="tmux-pick" onClick={() => setSelWin(w.index)}>
              <span className="tmux-name">
                {w.index}: {w.name}
              </span>
              <span className="muted small">
                {w.panes}p{w.active ? ` · ${t('tmux.active')}` : ''}
              </span>
            </button>
            {sel !== null && (
              <button className="link-btn danger" onClick={() => void act(TmuxCmd.killWindow(sel, w.index), () => loadWindows(sel))}>
                {t('tmux.kill')}
              </button>
            )}
          </div>
        ))}
        {sel !== null && windows.length === 0 && <div className="muted small region-pad">{t('tmux.none')}</div>}
      </div>

      <div className="tmux-col">
        <div className="tmux-col-head">
          <strong>{t('tmux.panes')}</strong>
          <span className="spacer" />
          {sel !== null && selWin !== null && panes.length > 0 && (
            <>
              <button onClick={() => void act(TmuxCmd.splitWindow(sel, selWin, panes[0].index, true), () => loadPanes(sel, selWin))}>
                {t('tmux.splitH')}
              </button>
              <button onClick={() => void act(TmuxCmd.splitWindow(sel, selWin, panes[0].index, false), () => loadPanes(sel, selWin))}>
                {t('tmux.splitV')}
              </button>
            </>
          )}
        </div>
        {panes.map((p) => (
          <div key={p.index} className="tmux-row">
            <div className="tmux-pick">
              <span className="tmux-name">
                {p.index}: {p.command}
              </span>
              <span className="muted small">
                {p.width}×{p.height}
                {p.active ? ` · ${t('tmux.active')}` : ''}
              </span>
            </div>
            {sel !== null && selWin !== null && (
              <>
                <button
                  className="link-btn"
                  onClick={() =>
                    void (async () => {
                      const out = await run(TmuxCmd.capturePane(sel, selWin, p.index));
                      if (out !== null) setCapture(out);
                    })()
                  }
                >
                  {t('tmux.capture')}
                </button>
                <button
                  className="link-btn"
                  onClick={() =>
                    void (async () => {
                      const keys = window.prompt(t('tmux.sendKeysPrompt'));
                      if (keys !== null && keys !== '') await run(TmuxCmd.sendKeys(sel, selWin, p.index, keys));
                    })()
                  }
                >
                  {t('tmux.sendKeys')}
                </button>
                <button className="link-btn danger" onClick={() => void act(TmuxCmd.killPane(sel, selWin, p.index), () => loadPanes(sel, selWin))}>
                  {t('tmux.kill')}
                </button>
              </>
            )}
          </div>
        ))}
        {sel !== null && selWin !== null && panes.length === 0 && <div className="muted small region-pad">{t('tmux.none')}</div>}
      </div>

      {err !== null && <div className="error tmux-err">{err}</div>}
      {capture !== null && (
        <div className="palette-backdrop" onMouseDown={() => setCapture(null)}>
          <div className="task-detail" onMouseDown={(e) => e.stopPropagation()}>
            <div className="admin-tabs">
              <strong>{t('tmux.capture')}</strong>
              <span className="spacer" />
              <button onClick={() => setCapture(null)}>{t('admin.close')}</button>
            </div>
            <div className="region-pad scroll">
              <pre className="ev-mono">{capture}</pre>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
