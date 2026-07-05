import { useQuery } from '@tanstack/react-query';
import { str, type Entity } from '../hub/types';
import { useSession } from '../state/session';

function field(row: Entity, keys: string[]): string {
  for (const k of keys) {
    const v = str(row, k);
    if (v !== undefined && v !== '') return v;
  }
  return '';
}

/// The WS2 exit surface: the team audit feed (`GET /v1/teams/{team}/audit`)
/// rendered live-ish via a 5s TanStack-Query refetch — proving REST + tokens
/// end to end. Upgradable to a hub team-firehose SSE later (plan Open Q2).
export function AuditConsole(): JSX.Element {
  const client = useSession((s) => s.client);
  const query = useQuery({
    queryKey: ['audit', client?.transport.teamId],
    enabled: client !== null,
    refetchInterval: 5000,
    queryFn: () => client!.listAudit({ limit: 100 }),
  });

  if (query.isLoading) return <div className="region-pad">Loading audit…</div>;
  if (query.isError) {
    return <div className="region-pad error">{(query.error as Error).message}</div>;
  }
  const rows = query.data ?? [];

  return (
    <table>
      <thead>
        <tr>
          <th>Time</th>
          <th>Action</th>
          <th>Actor</th>
          <th>Target</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((row, i) => (
          <tr key={field(row, ['id']) || String(i)}>
            <td>{field(row, ['created_at', 'ts', 'timestamp'])}</td>
            <td>{field(row, ['action', 'kind', 'event', 'type'])}</td>
            <td>{field(row, ['actor', 'by', 'agent_handle', 'actor_id'])}</td>
            <td>{field(row, ['target', 'summary', 'ref', 'target_id'])}</td>
          </tr>
        ))}
        {rows.length === 0 && (
          <tr>
            <td colSpan={4}>No audit events.</td>
          </tr>
        )}
      </tbody>
    </table>
  );
}
