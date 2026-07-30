import type { ReactNode } from 'react';
import { useAgents, useAttention, useHosts } from '../hub/queries';
import { str } from '../hub/types';
import { useT } from '../i18n';
import { isShell } from '../platform';
import { GLOBAL_ORIGIN, useAnnotation } from '../state/annotation';
import { useAssistant } from '../state/assistant';
import { useSyncJob } from '../state/syncJob';
import { useUiContext } from '../state/uiContext';
import { useTerminals } from '../terminal/store';
import { useWorkbench } from '../state/workbench';
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
  const wsProgress = useSyncJob((s) => s.progress);
  const zoteroRunning = useZoteroSyncJob((s) => s.running);
  const zoteroError = useZoteroSyncJob((s) => s.error);
  const zoteroProgress = useZoteroSyncJob((s) => s.progress);

  // Live terminal sessions + a toggle for the dock, so terminals are reachable
  // (and their count is visible) from any tab — not only the Terminal surface.
  const termCount = useTerminals((s) => s.tabs.length);
  const termOpen = useTerminals((s) => s.open);
  const toggleTerm = useTerminals((s) => s.toggle);
  const onTerminalSurface = useWorkbench((s) => s.job === 'terminal');
  const showTermChip = isShell() && !onTerminalSurface;

  // The assistant dock toggle — relocated from the activity rail to a status-bar
  // chip (#460): the rail stays purely job navigation, and the dock's ambient
  // open/closed state reads naturally next to the terminal chip.
  const assistantOpen = useAssistant((s) => s.open);
  const toggleAssistant = useAssistant((s) => s.toggle);

  // D2.1: the GLOBAL "Ask agent" annotate trigger (plan §3.4 step 2) — arms
  // the overlay with no companion origin so the gesture is reachable from
  // anywhere, including the embedded kimi-web loop (the SPA takes no injected
  // buttons). Same gate as the companion's compose-box button: sharing
  // toggle on + native shell. The chip reads active while the overlay is
  // armed, mirroring the dock chips' open state.
  const sharing = useUiContext((s) => s.enabled);
  const annotArmed = useAnnotation((s) => s.phase !== 'idle');

  // One chip PER job (Author workspace + Read/Zotero library) so both are
  // distinguishable when they run at once, and each background failure — which
  // would otherwise be lost once its modal is closed — is surfaced and dismissed
  // independently. Running chip spins; failed chip is a dismissable red button.
  // `N/M` when the running sync has reported progress (M = files to transfer for
  // the workspace backends, keys processed for the Zotero ones — see SyncProgress).
  const fmt = (p: { done: number; total: number } | null): string =>
    p !== null && p.total > 0 ? ` ${p.done}/${p.total}` : '';
  const jobs = [
    {
      key: 'workspace',
      running: wsRunning,
      error: wsError,
      runLabel: t('status.syncingWorkspace') + fmt(wsProgress),
      failLabel: t('status.syncFailedWorkspace'),
      dismiss: (): void => useSyncJob.getState().dismiss(),
    },
    {
      key: 'library',
      running: zoteroRunning,
      error: zoteroError,
      runLabel: t('status.syncingLibrary') + fmt(zoteroProgress),
      failLabel: t('status.syncFailedLibrary'),
      dismiss: (): void => useZoteroSyncJob.getState().dismiss(),
    },
  ];

  return (
    <div className="statusbar" role="status" aria-live="polite">
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
      {isShell() && (
        <button
          className={`statusbar-term${assistantOpen ? ' active' : ''}`}
          title={t('assistant.toggleHint')}
          aria-pressed={assistantOpen}
          onClick={() => toggleAssistant()}
        >
          <Icon name="globe" size={13} /> {t('assistant.title')}
        </button>
      )}
      {isShell() && sharing && (
        <button
          className={`statusbar-term statusbar-annotate${annotArmed ? ' active' : ''}`}
          title={t('annotate.ask')}
          aria-label={t('annotate.ask')}
          aria-pressed={annotArmed}
          onClick={() => useAnnotation.getState().arm(GLOBAL_ORIGIN)}
        >
          <Icon name="crosshair" size={13} /> {t('annotate.chip')}
        </button>
      )}
      {showTermChip && (
        <button
          className={`statusbar-term${termOpen ? ' active' : ''}`}
          title={termCount > 0 ? t('status.terminalsOpen') : t('status.terminalsNew')}
          onClick={() => toggleTerm()}
        >
          <Icon name="terminal" size={13} /> {termCount}
        </button>
      )}
      <span>{t('status.hosts')} {hosts.length}</span>
      {right !== undefined && <span className="statusbar-chrome">{right}</span>}
    </div>
  );
}
