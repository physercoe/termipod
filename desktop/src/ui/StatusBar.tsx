import { useAgents, useAttention, useHosts } from '../hub/queries';
import { str } from '../hub/types';
import { useT } from '../i18n';

/// Persistent ambient monitor (plan §4) — fleet counters + governance backlog +
/// host connectivity, always in view.
export function StatusBar(): JSX.Element {
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
    </div>
  );
}
