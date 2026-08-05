import { obj, str, type Entity } from '../hub/types.ts';

/// The engine family behind an agent — `backend.kind`, NOT `agent.kind`.
///
/// They differ for exactly the agents the Companion cares most about: a
/// steward's `kind` is its template (`steward.general`, `steward.project`) and
/// only `backend.kind` names the engine actually running. Mobile learned this
/// the same way and says so at `agent_compose.dart:158-161`; every engine-gated
/// affordance on either client has to read the same field or it silently
/// disables itself for stewards.
///
/// Returns undefined when the record hasn't loaded or carries no backend —
/// callers treat that as "engine unknown" and suppress engine-specific
/// affordances rather than guessing one.
export function agentEngine(agent: Entity | undefined): string | undefined {
  if (agent === undefined) return undefined;
  const backend = obj(agent, 'backend');
  if (backend === undefined) return undefined;
  const kind = str(backend, 'kind');
  return kind !== undefined && kind !== '' ? kind : undefined;
}
