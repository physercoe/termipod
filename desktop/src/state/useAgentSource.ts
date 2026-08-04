import { useMemo } from 'react';
import { hubAgentSource, type AgentEventSource } from './agentSource';
import { useSession } from './session';

/// Resolve the event source the Companion is bound to (vision-parity L1).
///
/// One implementation today, so resolution is "the bound hub client, if any"
/// and the hook is four lines. Deliberately NOT a React context: with a single
/// possible value a provider would be a selection mechanism with nothing to
/// select, and the shape of the real one depends on what a local source turns
/// out to need (plan L3 — a service handle, not a client). The second
/// implementation is what earns the context, and it arrives with a second
/// implementation to design it against.
///
/// Memoised on the client so a source is referentially stable across renders:
/// `followAgent` runs from a `useEffect` keyed on it, and a fresh object every
/// render would tear down and re-open the stream on every keystroke.
///
/// Lives apart from `agentSource.ts` so that module stays loadable under
/// `node --test` — the session store reaches the bridge and cannot.
export function useAgentSource(): AgentEventSource | null {
  const client = useSession((s) => s.client);
  return useMemo(() => (client === null ? null : hubAgentSource(client)), [client]);
}
