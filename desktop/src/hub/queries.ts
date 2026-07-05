import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { useSession } from '../state/session';
import type { Entity } from './types';

/// Shared fleet queries — TanStack Query dedupes by key, so Navigator and the
/// status bar reuse one in-flight request. Polled (5s) since the fleet REST
/// surfaces aren't SSE (plan §4 / Open Q2).
export function useAgents(): UseQueryResult<Entity[]> {
  const client = useSession((s) => s.client);
  return useQuery({
    queryKey: ['agents', client?.transport.teamId],
    enabled: client !== null,
    refetchInterval: 5000,
    queryFn: () => client!.listAgents(),
  });
}

export function useHosts(): UseQueryResult<Entity[]> {
  const client = useSession((s) => s.client);
  return useQuery({
    queryKey: ['hosts', client?.transport.teamId],
    enabled: client !== null,
    refetchInterval: 15000,
    queryFn: () => client!.listHosts(),
  });
}

/// The approvals queue — shared by the dock (WS5) and the status-bar counter.
export function useAttention(): UseQueryResult<Entity[]> {
  const client = useSession((s) => s.client);
  return useQuery({
    queryKey: ['attention', client?.transport.teamId],
    enabled: client !== null,
    refetchInterval: 6000,
    queryFn: () => client!.listAttention(),
  });
}
