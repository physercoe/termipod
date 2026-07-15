import { useState } from 'react';
import { useT } from '../i18n';
import { FileTransferPanel } from '../surfaces/FileTransferPanel';
import { Screen } from './Screen';
import type { TermTab } from './store';

/// One terminal tab's content. SSH tabs keep a terminal / files sub-switcher (SFTP
/// rides the SSH session); local shells show only the terminal. The `<Screen>`
/// stays mounted across sub-view switches — hiding it (not unmounting) keeps the
/// session alive. (tmux control was removed on desktop — redundant with native
/// panes; the director drives shells directly.)
export function SessionView({ tab }: { tab: TermTab }): JSX.Element {
  const t = useT();
  const [view, setView] = useState<'term' | 'files'>('term');
  const isSsh = tab.kind === 'ssh';

  return (
    <div className="session-view">
      {isSsh && (
        <div className="session-subtabs">
          <button className={view === 'term' ? 'tab active' : 'tab'} onClick={() => setView('term')}>
            {t('term.terminal')}
          </button>
          <button className={view === 'files' ? 'tab active' : 'tab'} onClick={() => setView('files')}>
            {t('term.files')}
          </button>
        </div>
      )}
      <div className="session-body">
        <div className={view === 'term' ? 'term-view' : 'term-view hidden'}>
          <Screen kind={tab.kind} sessionId={tab.sessionId} />
        </div>
        {isSsh && view === 'files' && <FileTransferPanel sessionId={tab.sessionId} />}
      </div>
    </div>
  );
}
