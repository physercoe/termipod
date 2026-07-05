import { useQuery } from '@tanstack/react-query';
import { useAgents, useHosts } from '../hub/queries';
import { str } from '../hub/types';
import { useSession } from '../state/session';

/// Persistent ambient monitor (plan §4) — fleet counters + governance backlog +
/// host connectivity, always in view.
export function StatusBar(): JSX.Element {
  const client = useSession((s) => s.client);
  const agents = useAgents().data ?? [];
  const hosts = useHosts().data ?? [];
  const attention =
    useQuery({
      queryKey: ['attention-count', client?.transport.teamId],
      enabled: client !== null,
      refetchInterval: 8000,
      queryFn: () => client!.listAttention(),
    }).data ?? [];

  const running = agents.filter((a) => str(a, 'status') === 'running').length;
  const paused = agents.filter((a) => str(a, 'status') === 'paused').length;

  return (
    <div className="statusbar">
      <span>{running} running</span>
      <span>{paused} paused</span>
      <span>{attention.length} need you</span>
      <span className="spacer" />
      <span>hosts {hosts.length}</span>
      <span>· WS3/4</span>
    </div>
  );
}
