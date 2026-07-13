import { useState } from 'react';
import { useT } from '../i18n';
import { FileTransferPanel } from '../surfaces/FileTransferPanel';
import { TmuxPanel } from '../surfaces/TmuxPanel';
import { isPosixShell } from './osc133';
import { Screen } from './Screen';
import type { TermTab } from './store';

/// One dock tab's content. SSH tabs keep the terminal / tmux / files sub-switcher
/// (parity with the old modal); local shells show only the terminal (tmux control
/// and SFTP both ride an SSH session, so they don't apply). The `<Screen>` stays
/// mounted across sub-view switches — hiding it (not unmounting) keeps the session
/// alive.
export function SessionView({ tab }: { tab: TermTab }): JSX.Element {
  const t = useT();
  const [view, setView] = useState<'term' | 'tmux' | 'files'>('term');
  const isSsh = tab.kind === 'ssh';
  // OSC-133 blocks ride a bash/zsh script; only offer/auto-run it where that
  // shell can parse it. SSH (remote, shell kind unknown → assumed POSIX) keeps
  // manual integration; a local cmd.exe / PowerShell gets neither. An agent tab
  // never integrates — its TUI owns the screen, and injecting the script would
  // type it straight into the agent's prompt.
  const canIntegrate = tab.agent !== true && (isSsh || isPosixShell(tab.shell));

  return (
    <div className="session-view">
      {isSsh && (
        <div className="session-subtabs">
          <button className={view === 'term' ? 'tab active' : 'tab'} onClick={() => setView('term')}>
            {t('term.terminal')}
          </button>
          <button className={view === 'tmux' ? 'tab active' : 'tab'} onClick={() => setView('tmux')}>
            {t('term.tmux')}
          </button>
          <button className={view === 'files' ? 'tab active' : 'tab'} onClick={() => setView('files')}>
            {t('term.files')}
          </button>
        </div>
      )}
      <div className="session-body">
        <div className={view === 'term' ? 'term-view' : 'term-view hidden'}>
          <Screen
            kind={tab.kind}
            sessionId={tab.sessionId}
            autoIntegrate={tab.kind === 'local' && canIntegrate}
            canIntegrate={canIntegrate}
          />
        </div>
        {isSsh && view === 'tmux' && <TmuxPanel sessionId={tab.sessionId} />}
        {isSsh && view === 'files' && <FileTransferPanel sessionId={tab.sessionId} />}
      </div>
    </div>
  );
}
