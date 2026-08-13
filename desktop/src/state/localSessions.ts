import { useMutation, useQuery, useQueryClient, type UseMutationResult, type UseQueryResult } from '@tanstack/react-query';
import { invoke, isShell } from '../bridge';
import type { Entity } from '../hub/types.ts';

/// Local agent sessions — the client half of the Electron-main service
/// (vision-parity L3a).
///
/// These are *not* hub agents. There is no team, no host, no spawn row and no
/// attention table; a session is a claude child this app is running, and the
/// only thing it shares with a hub agent is the event vocabulary the Companion
/// renders. That is D-7 working as intended.

export type ToolPosture = 'converse' | 'read_local' | 'unrestricted';

export interface LocalSession {
  id: string;
  family: string;
  cwd: string;
  posture: ToolPosture;
  model?: string;
  status: 'running' | 'stopped';
  created_at: string;
  engine_session_id?: string;
}

/// Which families this build can drive locally, and therefore whether the
/// "new local session" affordance has anything to offer. An empty list is the
/// honest answer in the browser build and on a machine whose registry failed
/// to load — the surface says so rather than showing a button that throws.
///
/// Rows carry the `prompt_*` maps, so `promptCapabilities` resolves a local
/// session's modalities from the same shape it reads for a hub agent.
export function useLocalFamilies(): UseQueryResult<Entity[]> {
  return useQuery({
    queryKey: ['localagent', 'families'],
    enabled: isShell(),
    // The registry is generated at build time and read once; polling it would
    // re-read a file that cannot change while the app runs.
    staleTime: Infinity,
    queryFn: async () => (await invoke<{ families: Entity[] }>('localagent_families')).families,
  });
}

export function useLocalSessions(): UseQueryResult<LocalSession[]> {
  return useQuery({
    queryKey: ['localagent', 'sessions'],
    enabled: isShell(),
    // Sessions change when the user starts or stops one, and when a child
    // exits on its own — that last one has no client-side trigger, which is
    // why this polls at all.
    refetchInterval: 5000,
    queryFn: async () => (await invoke<{ sessions: LocalSession[] }>('localagent_list')).sessions,
  });
}

export interface CreateLocalSessionVars {
  cwd: string;
  family?: string;
  posture?: ToolPosture;
  model?: string;
}

export function useCreateLocalSession(): UseMutationResult<LocalSession, Error, CreateLocalSessionVars> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: CreateLocalSessionVars) => invoke<LocalSession>('localagent_create', { ...vars }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['localagent', 'sessions'] });
    },
  });
}

export function useStopLocalSession(): UseMutationResult<unknown, Error, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => invoke('localagent_stop', { session_id: id }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['localagent', 'sessions'] });
    },
  });
}

/// Render a local session as the picker's row shape.
///
/// The Companion's picker reads hub `Entity` maps, and a local session is not
/// one — so rather than teach the picker a second shape, a session is projected
/// onto the three fields it reads. `kind` carries the family so the row reads
/// the same as a hub agent's, and the cwd's last segment is the handle, because
/// "which directory is this session working in" is the only thing that
/// distinguishes two local sessions at a glance.
export function localSessionRow(s: LocalSession): Entity {
  const leaf = s.cwd.replace(/[/\\]+$/, '').split(/[/\\]/).pop() ?? s.cwd;
  return {
    id: s.id,
    handle: leaf === '' ? s.cwd : leaf,
    kind: s.family,
    status: s.status,
    local: true,
  };
}
