import { useAgents, useAttention, useHosts } from '../hub/queries';
import { str } from '../hub/types';

/// Persistent ambient monitor (plan §4) — fleet counters + governance backlog +
/// host connectivity, always in view.
export function StatusBar(): JSX.Element {
  const agents = useAgents().data ?? [];
  const hosts = useHosts().data ?? [];
  const attention = (useAttention().data ?? []).filter((a) => (str(a, 'status') ?? 'open') === 'open');

  const running = agents.filter((a) => str(a, 'status') === 'running').length;
  const paused = agents.filter((a) => str(a, 'status') === 'paused').length;

  return (
    <div className="statusbar">
      <span>{running} running</span>
      <span>{paused} paused</span>
      <span>{attention.length} need you</span>
      <span className="spacer" />
      <span>hosts {hosts.length}</span>
      <span>· WS3–5</span>
    </div>
  );
}
