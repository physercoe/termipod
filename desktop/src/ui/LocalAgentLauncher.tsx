import { useState } from 'react';
import { useT } from '../i18n';
import { Icon } from './Icon';
import { isShell } from '../platform';
import { useWorkspace } from '../state/workspace';
import { ptyOpen } from '../terminal/pty';
import { useTerminals } from '../terminal/store';

/// Launch a local engine CLI (default `claude`) as an interactive session in the
/// shared **terminal dock** — cwd = the open Author workspace — instead of an
/// embedded panel terminal. The dock's `<Screen>` owns robust fit/resize, so wide
/// agent TUIs (e.g. kimi) fill the width and re-flow with the window rather than
/// truncating, and the session is reachable from any tab via the status-bar
/// terminal chip.
///
/// This supersedes the former in-panel embed (`LocalCompanion`). A local
/// *structured-protocol* driver that spawns the CLI in a machine-readable mode
/// (claude `--output-format stream-json`; ACP for others) and renders it as chat
/// is the tracked follow-up — this half is the raw-terminal route.

const CMD_KEY = 'termipod.localAgent.cmd';

export function LocalAgentLauncher(): JSX.Element {
  const t = useT();
  const folder = useWorkspace((s) => s.folder);
  const addTab = useTerminals((s) => s.addTab);
  const [cmd, setCmd] = useState(() => localStorage.getItem(CMD_KEY) ?? 'claude');
  const [error, setError] = useState<string | null>(null);

  function saveCmd(v: string): void {
    setCmd(v);
    try {
      localStorage.setItem(CMD_KEY, v);
    } catch {
      /* ignore */
    }
  }

  // Spawn the CLI in a PTY (opened, not yet started) and hand it to the dock as an
  // agent tab; the dock's <Screen> attaches its listeners then starts streaming
  // (avoids the first-prompt race) and fits to the pane. addTab opens the dock.
  async function launch(): Promise<void> {
    setError(null);
    const parts = cmd.trim().split(/\s+/).filter(Boolean);
    if (parts.length === 0) return;
    try {
      const { id, shell } = await ptyOpen({
        shell: parts[0],
        args: parts.slice(1),
        cwd: folder ?? undefined,
        cols: 80,
        rows: 24,
      });
      addTab({ kind: 'local', sessionId: id, shell, title: parts[0], agent: true });
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  if (!isShell()) {
    return <div className="companion-empty muted">{t('companion.localDesktopOnly')}</div>;
  }

  return (
    <div className="companion-launch">
      <p className="muted small">{t('companion.localLaunchBlurb')}</p>
      <div className="companion-launch-row">
        <input
          className="companion-local-cmdin mono"
          value={cmd}
          spellCheck={false}
          placeholder="claude"
          onChange={(e) => saveCmd(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') void launch();
          }}
        />
        <button className="primary companion-launch-btn" onClick={() => void launch()}>
          <Icon name="terminal" size={14} /> {t('companion.localLaunch')}
        </button>
      </div>
      {folder !== null ? (
        <p className="muted small mono companion-launch-cwd">{t('companion.localCwd').replace('{dir}', folder)}</p>
      ) : (
        <p className="muted small">{t('companion.localNoFolder')}</p>
      )}
      {error !== null && <div className="error small">{error}</div>}
    </div>
  );
}
