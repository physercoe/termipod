import { useMemo } from 'react';
import { invoke, isShell, listen } from '../bridge';
import { hubAgentSource, type AgentEventSource } from './agentSource';
import { localAgentSource, type LocalAgentBackend } from './localAgentSource';
import { useSession } from './session';

/// Ids minted by the local agent service. The prefix is the routing key: a
/// session id says which producer owns it, so nothing has to carry a parallel
/// "which source" flag that could disagree with the id beside it.
export const LOCAL_SESSION_PREFIX = 'local-';

export function isLocalSessionId(id: string | undefined): boolean {
  return id !== undefined && id.startsWith(LOCAL_SESSION_PREFIX);
}

/// The bridge, as the local source consumes it. Built once: `localAgentSource`
/// holds a watch refcount, and a fresh backend per render would hand each
/// subscription its own count and unwatch the window while views are still open.
const bridgeBackend: LocalAgentBackend = {
  invoke: <T,>(cmd: string, args?: Record<string, unknown>): Promise<T> => invoke<T>(cmd, args),
  listen: <T,>(event: string, cb: (ev: { payload: T }) => void): Promise<() => void> =>
    listen<T>(event, (e) => cb({ payload: e.payload })),
};

/// Resolve the event source for an agent (vision-parity L1, completed by L3a).
///
/// L1 shipped this as four lines returning the hub client, and said why: with
/// one implementation a selector has nothing to select, and "the shape of the
/// real one depends on what a local source turns out to need". It needed an
/// argument. A local session and a hub agent are both just an id to every
/// caller, so resolution keys on the id rather than on an ambient mode — which
/// means a Companion showing a local session and a dock listing hub attention
/// can coexist without a global switch deciding for both.
///
/// Called with no id — as the attention cards do — this stays what it was: the
/// hub source, or null when no hub is bound. That is correct rather than
/// convenient, because the attention table those callers read is a hub concept
/// and a local source deliberately does not have one.
///
/// Memoised so a source is referentially stable across renders: `followAgent`
/// runs from a `useEffect` keyed on it, and a fresh object every render would
/// tear down and re-open the stream on every keystroke.
export function useAgentSource(agentId?: string): AgentEventSource | null {
  const client = useSession((s) => s.client);
  const local = isLocalSessionId(agentId);
  return useMemo(() => {
    // A local id can only be served in a shell — the service lives in Electron
    // main. In the browser build there is nothing behind the bridge, so this
    // resolves to no source and the panel degrades to its empty state rather
    // than throwing on the first invoke.
    if (local) return isShell() ? localAgentSource(bridgeBackend) : null;
    return client === null ? null : hubAgentSource(client);
  }, [client, local]);
}
