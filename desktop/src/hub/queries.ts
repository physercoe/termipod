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

/// The agent-family registry — engine capabilities as DATA (ADR-010): which
/// driving modes a family supports and which prompt modalities it accepts on
/// each. Not polled: it changes only when an operator edits a family, and the
/// composers that gate on it would rather be a refresh behind than re-fetch a
/// static list every few seconds. `['agent-families']` is the key AgentSpawn
/// and AdminCockpit already use, so all four share one request.
export function useAgentFamilies(): UseQueryResult<Entity[]> {
  const client = useSession((s) => s.client);
  return useQuery({
    queryKey: ['agent-families'],
    enabled: client !== null,
    queryFn: () => client!.listAgentFamilies(),
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

export function useProjects(): UseQueryResult<Entity[]> {
  const client = useSession((s) => s.client);
  return useQuery({
    queryKey: ['projects', client?.transport.teamId],
    enabled: client !== null,
    refetchInterval: 15000,
    queryFn: () => client!.listProjects(),
  });
}

/// Team environment profiles (env-profiles plan) — the reusable
/// {setup_script + env_vars + secret_refs + net_policy} bundles a spawn attaches.
/// Feeds the spawn-sheet picker and (E2b-2) the management surface.
export function useEnvProfiles(): UseQueryResult<Entity[]> {
  const client = useSession((s) => s.client);
  return useQuery({
    queryKey: ['env-profiles', client?.transport.teamId],
    enabled: client !== null,
    queryFn: () => client!.listEnvProfiles(),
  });
}

/// Team-scope insights — ONE call feeds the whole Projects nav with each
/// project's phase-weighted progress, open-AC count, phase, and open-attention
/// rollup (`by_project[]`, populated only on team scope — `handlers_insights.go`).
/// Mirrors mobile, which derives its project-card lines from the same aggregate
/// rather than N per-project reads.
export function useProjectInsights(): UseQueryResult<Entity> {
  const client = useSession((s) => s.client);
  const teamId = client?.transport.teamId;
  return useQuery({
    queryKey: ['insights', 'team', teamId],
    enabled: client !== null && teamId !== undefined && teamId !== '',
    refetchInterval: 20000,
    queryFn: () => client!.getInsights({ team_id: teamId! }),
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
