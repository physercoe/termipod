import type { ReactNode } from 'react';
import { useAgents, useAttention, useHosts } from '../hub/queries';
import { str } from '../hub/types';
import { useT } from '../i18n';
import { useSyncJob } from '../state/syncJob';
import { useZoteroSyncJob } from '../state/zoteroSyncJob';
import { Icon } from './Icon';

/// Persistent ambient monitor (plan §4) — fleet counters + governance backlog +
/// host connectivity, always in view. The `right` slot carries the session chrome
/// (profile switcher / connect + command palette) relocated here from the old
/// top titlebar, so the shell reclaims that whole row of vertical space.
///
/// The status bar is shell chrome — mounted once, visible on every tab — so it
/// also carries the **background sync** indicator for BOTH sync jobs (Author
/// workspace [[syncJob]] and Read/Zotero library [[zoteroSyncJob]]). The modals
/// that start a sync can be closed and the user can switch tabs, so this is the
/// only always-visible place to show "still syncing" / "sync failed".
export function StatusBar({ right }: { right?: ReactNode }): JSX.Element {
  const t = useT();
  const agents = useAgents().data ?? [];
  const hosts = useHosts().data ?? [];
  const attention = (useAttention().data ?? []).filter((a) => (str(a, 'status') ?? 'open') === 'open');

  const running = agents.filter((a) => str(a, 'status') === 'running').length;
  const paused = agents.filter((a) => str(a, 'status') === 'paused').length;

  const wsRunning = useSyncJob((s) => s.running);
  const wsError = useSyncJob((s) => s.error);
  const zoteroRunning = useZoteroSyncJob((s) => s.running);
  const zoteroError = useZoteroSyncJob((s) => s.error);

  // One chip PER job (Author workspace + Read/Zotero library) so both are
  // distinguishable when they run at once, and each background failure — which
  // would otherwise be lost once its modal is closed — is surfaced and dismissed
  // independently. Running chip spins; failed chip is a dismissable red button.
  const jobs = [
    {
      key: 'workspace',
      running: wsRunning,
      error: wsError,
      runLabel: t('status.syncingWorkspace'),
      failLabel: t('status.syncFailedWorkspace'),
      dismiss: (): void => useSyncJob.getState().dismiss(),
    },
    {
      key: 'library',
      running: zoteroRunning,
      error: zoteroError,
      runLabel: t('status.syncingLibrary'),
      failLabel: t('status.syncFailedLibrary'),
      dismiss: (): void => useZoteroSyncJob.getState().dismiss(),
    },
  ];

  return (
    <div className="statusbar">
      <span>{running} {t('status.running')}</span>
      <span>{paused} {t('status.paused')}</span>
      <span>{attention.length} {t('status.needYou')}</span>
      {jobs.map((j) =>
        j.running ? (
          <span key={j.key} className="statusbar-sync" title={j.runLabel}>
            <Icon name="cloud" size={13} /> {j.runLabel}
          </span>
        ) : j.error !== null ? (
          <button key={j.key} className="statusbar-sync err" title={j.error} onClick={j.dismiss}>
            <Icon name="cloud" size={13} /> {j.failLabel}
          </button>
        ) : null,
      )}
      <span className="spacer" />
      <span>{t('status.hosts')} {hosts.length}</span>
      {right !== undefined && <span className="statusbar-chrome">{right}</span>}
    </div>
  );
}
