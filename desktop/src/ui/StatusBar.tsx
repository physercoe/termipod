import type { ReactNode } from 'react';
import { useAgents, useAttention, useHosts } from '../hub/queries';
import { str } from '../hub/types';
import { useT } from '../i18n';

/// Persistent ambient monitor (plan §4) — fleet counters + governance backlog +
/// host connectivity, always in view. The `right` slot carries the session chrome
/// (profile switcher / connect + command palette) relocated here from the old
/// top titlebar, so the shell reclaims that whole row of vertical space.
export function StatusBar({ right }: { right?: ReactNode }): JSX.Element {
  const t = useT();
  const agents = useAgents().data ?? [];
  const hosts = useHosts().data ?? [];
  const attention = (useAttention().data ?? []).filter((a) => (str(a, 'status') ?? 'open') === 'open');

  const running = agents.filter((a) => str(a, 'status') === 'running').length;
  const paused = agents.filter((a) => str(a, 'status') === 'paused').length;

  return (
    <div className="statusbar">
      <span>{running} {t('status.running')}</span>
      <span>{paused} {t('status.paused')}</span>
      <span>{attention.length} {t('status.needYou')}</span>
      <span className="spacer" />
      <span>{t('status.hosts')} {hosts.length}</span>
      {right !== undefined && <span className="statusbar-chrome">{right}</span>}
    </div>
  );
}
