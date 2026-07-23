import { useState } from 'react';
import { useT } from '../i18n';
import { isShell } from '../platform';
import { FileTransferPanel } from '../surfaces/FileTransferPanel';
import { WebPanel } from '../surfaces/WebPanel';
import { webPanels } from '../ui/webPanels';
import { Screen } from './Screen';
import { useTerminals, type TermTab } from './store';

/// The session-area sub-view kinds: terminal | files (SSH only) | one `web:<id>`
/// per registered web panel (agent-transcript-redesign P0 — the panel type is
/// now extensible; another agent web UI is one registry row later).
type SubView = 'term' | 'files' | `web:${string}`;

/// One terminal tab's content. SSH tabs keep a terminal / files sub-switcher (SFTP
/// rides the SSH session); local shells show only the terminal. The `<Screen>`
/// stays mounted across sub-view switches — hiding it (not unmounting) keeps the
/// session alive. (tmux control was removed on desktop — redundant with native
/// panes; the director drives shells directly.)
///
/// Web panels (P0) join the switcher for local AND ssh tabs: the backing server
/// (`kimi web`) spawns locally either way — P0 is local-first; the remote
/// SSH-forward wedge is a follow-up (decision §7.1). The tabs are offered
/// unconditionally (no availability probe just to hide a tab): if the kimi
/// binary is missing the panel shows its own error + retry — the simpler honest
/// option. A mounted web panel stays mounted (hidden) across sub-view switches
/// so the guest's SPA state survives, exactly like the Screen.
export function SessionView({ tab, onReconnect }: { tab: TermTab; onReconnect?: () => void }): JSX.Element {
  const t = useT();
  const [view, setView] = useState<SubView>('term');
  // Web sub-views mounted at least once (kept alive while hidden).
  const [mountedWeb, setMountedWeb] = useState<string[]>([]);
  const isSsh = tab.kind === 'ssh';
  const showWeb = isShell(); // web panels need the native shell (spawn + webview)
  const markActivity = useTerminals((s) => s.markActivity);

  function open(v: SubView): void {
    setView(v);
    if (v.startsWith('web:')) setMountedWeb((m) => (m.includes(v) ? m : [...m, v]));
  }

  return (
    <div className="session-view">
      {(isSsh || showWeb) && (
        <div className="session-subtabs">
          <button className={view === 'term' ? 'tab active' : 'tab'} onClick={() => open('term')}>
            {t('term.terminal')}
          </button>
          {isSsh && (
            <button className={view === 'files' ? 'tab active' : 'tab'} onClick={() => open('files')}>
              {t('term.files')}
            </button>
          )}
          {showWeb &&
            webPanels.map((p) => (
              <button
                key={p.id}
                className={view === `web:${p.id}` ? 'tab active' : 'tab'}
                onClick={() => open(`web:${p.id}`)}
              >
                {t(p.labelKey)}
              </button>
            ))}
        </div>
      )}
      <div className="session-body">
        <div className={view === 'term' ? 'term-view' : 'term-view hidden'}>
          <Screen
            kind={tab.kind}
            sessionId={tab.sessionId}
            onReconnect={onReconnect}
            onActivity={() => markActivity(tab.id)}
          />
        </div>
        {isSsh && view === 'files' && <FileTransferPanel sessionId={tab.sessionId} />}
        {showWeb &&
          webPanels.map((p) =>
            mountedWeb.includes(`web:${p.id}`) ? (
              <div key={p.id} className={view === `web:${p.id}` ? 'web-panel-wrap' : 'web-panel-wrap hidden'}>
                <WebPanel panel={p} />
              </div>
            ) : null,
          )}
      </div>
    </div>
  );
}
