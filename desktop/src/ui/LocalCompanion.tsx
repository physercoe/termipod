import { useEffect, useRef, useState } from 'react';
import { FitAddon } from '@xterm/addon-fit';
import { Terminal as XTerm } from '@xterm/xterm';
import '@xterm/xterm/css/xterm.css';
import type { UnlistenFn } from '@tauri-apps/api/event';
import { useT } from '../i18n';
import { Icon } from './Icon';
import { isTauri } from '../platform';
import { useWorkspace } from '../state/workspace';
import { onPtyData, onPtyExit, ptyClose, ptyOpen, ptyResize, ptyStart, ptyWrite } from '../terminal/pty';

/// The **local** half of the AgentCompanion: an interactive terminal running an
/// engine CLI (default `claude`) on THIS machine, in a real PTY, with cwd = the
/// open Author workspace folder — so the local agent is a full interactive session
/// (edit, approve, @-mention files in its own TUI), not the former one-shot
/// print-mode call. Desktop-only; reuses the `pty.rs` bridge (ptyOpen documents
/// this exact "local agent CLI" use). Editing the command doesn't relaunch until
/// Restart, so a running agent isn't killed by a stray keystroke.

const CMD_KEY = 'termipod.localAgent.cmd';

export function LocalCompanion(): JSX.Element {
  const t = useT();
  const folder = useWorkspace((s) => s.folder);
  const [cmd, setCmd] = useState(() => localStorage.getItem(CMD_KEY) ?? 'claude');
  const [editCmd, setEditCmd] = useState(false);
  const [runNonce, setRunNonce] = useState(0);
  const [exited, setExited] = useState(false);
  const hostRef = useRef<HTMLDivElement>(null);
  // Current cmd/cwd are read at launch time (not baked into the effect deps) so
  // editing the command box doesn't tear down a live agent — only Restart does.
  const cmdRef = useRef(cmd);
  cmdRef.current = cmd;
  const folderRef = useRef(folder);
  folderRef.current = folder;
  const mountedRef = useRef(false);

  function saveCmd(v: string): void {
    setCmd(v);
    try {
      localStorage.setItem(CMD_KEY, v);
    } catch {
      /* ignore */
    }
  }

  // Relaunch when the workspace folder changes (the agent's cwd is stale
  // otherwise) or on an explicit Restart. Skip the initial folder value — the
  // launch effect below already spawns once for it, so bumping here too would
  // double-spawn on mount.
  useEffect(() => {
    if (!mountedRef.current) {
      mountedRef.current = true;
      return;
    }
    setRunNonce((n) => n + 1);
  }, [folder]);

  useEffect(() => {
    const host = hostRef.current;
    if (host === null || !isTauri()) return;
    setExited(false);
    const term = new XTerm({
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
      fontSize: 12,
      cursorBlink: true,
      convertEol: false,
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    try {
      fit.fit();
    } catch {
      /* host not laid out yet — the ResizeObserver will fit shortly */
    }

    let sessionId = '';
    let disposed = false;
    let unlistenData: UnlistenFn | null = null;
    let unlistenExit: UnlistenFn | null = null;
    const dataDisp = term.onData((d) => {
      if (sessionId !== '') void ptyWrite(sessionId, d);
    });

    void (async () => {
      const parts = cmdRef.current.trim().split(/\s+/).filter(Boolean);
      try {
        // Open BEFORE start, and attach listeners before start, so the first
        // output can't race ahead of the subscriber (the black-terminal bug).
        const opened = await ptyOpen({
          shell: parts[0],
          args: parts.slice(1),
          cwd: folderRef.current ?? undefined,
          cols: term.cols,
          rows: term.rows,
        });
        if (disposed) {
          void ptyClose(opened.id);
          return;
        }
        sessionId = opened.id;
        unlistenData = await onPtyData(opened.id, (bytes) => term.write(bytes));
        unlistenExit = await onPtyExit(opened.id, () => {
          if (!disposed) setExited(true);
        });
        await ptyStart(opened.id);
      } catch (e) {
        if (!disposed) term.write(`\r\n\x1b[31m${e instanceof Error ? e.message : String(e)}\x1b[0m\r\n`);
      }
    })();

    const ro = new ResizeObserver(() => {
      try {
        fit.fit();
      } catch {
        /* ignore */
      }
      if (sessionId !== '') void ptyResize(sessionId, term.cols, term.rows);
    });
    ro.observe(host);

    return () => {
      disposed = true;
      ro.disconnect();
      dataDisp.dispose();
      unlistenData?.();
      unlistenExit?.();
      if (sessionId !== '') void ptyClose(sessionId);
      term.dispose();
    };
  }, [runNonce]);

  if (!isTauri()) {
    return <div className="companion-empty muted">{t('companion.localDesktopOnly')}</div>;
  }

  return (
    <div className="companion-term-wrap">
      <div className="companion-local-cmd">
        {editCmd ? (
          <input
            className="companion-local-cmdin mono"
            value={cmd}
            autoFocus
            spellCheck={false}
            placeholder="claude"
            onChange={(e) => saveCmd(e.target.value)}
            onBlur={() => setEditCmd(false)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                setEditCmd(false);
                setRunNonce((n) => n + 1);
              }
            }}
          />
        ) : (
          <button className="companion-local-cmdbtn mono small" title={t('companion.localCmdHint')} onClick={() => setEditCmd(true)}>
            $ {cmd}
            {folder !== null && <span className="muted"> · {folder}</span>}
          </button>
        )}
        <span className="spacer" />
        <button
          className="companion-term-restart"
          title={t('companion.localRestart')}
          onClick={() => setRunNonce((n) => n + 1)}
        >
          <Icon name="refresh" size={14} />
        </button>
      </div>
      {folder === null && <div className="companion-term-hint muted small">{t('companion.localNoFolder')}</div>}
      <div className="companion-term" ref={hostRef} />
      {exited && (
        <div className="companion-term-exit muted small">
          {t('companion.localExited')}
          <button className="link-btn" onClick={() => setRunNonce((n) => n + 1)}>
            {t('companion.localRestart')}
          </button>
        </div>
      )}
    </div>
  );
}
