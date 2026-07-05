import type { HubConfig } from './config';
import { streamSse, type SseHandle, type SseOptions } from './sse';
import { HubTransport } from './transport';
import type { Entity, HubInfo } from './types';

function asArray(v: unknown): Entity[] {
  return Array.isArray(v) ? (v as Entity[]) : [];
}

/// Typed facade over the hub REST + SSE API — the web analogue of
/// hub_client.dart. Subclient methods are grouped inline; paths verified against
/// hub/internal/server/server.go. Note `hub/stats` is NOT team-scoped.
export class HubClient {
  readonly transport: HubTransport;
  constructor(private readonly cfg: HubConfig) {
    this.transport = new HubTransport(cfg);
  }

  // --- system ---
  probe(): Promise<HubInfo> {
    return this.transport.probe() as Promise<HubInfo>;
  }
  getInfo(): Promise<HubInfo> {
    return this.transport.get('/v1/_info') as Promise<HubInfo>;
  }
  getHubStats(): Promise<Entity> {
    return this.transport.get('/v1/hub/stats') as Promise<Entity>;
  }

  // --- audit / activity console (read-only surface, WS2 exit) ---
  async listAudit(params: { limit?: number; before?: string } = {}): Promise<Entity[]> {
    const out = await this.transport.get(this.transport.team('/audit'), {
      limit: params.limit !== undefined ? String(params.limit) : undefined,
      before: params.before,
    });
    return asArray(out);
  }

  // --- attention (approvals) ---
  async listAttention(status?: string): Promise<Entity[]> {
    const out = await this.transport.get(this.transport.team('/attention'), { status });
    return asArray(out);
  }
  decideAttention(id: string, decision: string, extra: Record<string, unknown> = {}): Promise<unknown> {
    return this.transport.post(this.transport.team(`/attention/${id}/decide`), { decision, ...extra });
  }

  // --- fleet ---
  async listAgents(): Promise<Entity[]> {
    const out = await this.transport.get(this.transport.team('/agents'));
    return asArray(out);
  }
  async listHosts(): Promise<Entity[]> {
    const out = await this.transport.get(this.transport.team('/hosts'));
    return asArray(out);
  }
  async listSessions(): Promise<Entity[]> {
    const out = await this.transport.get(this.transport.team('/sessions'));
    return asArray(out);
  }

  // --- agent detail + lifecycle (WS3) ---
  getAgent(id: string): Promise<Entity> {
    return this.transport.get(this.transport.team(`/agents/${id}`)) as Promise<Entity>;
  }
  pauseAgent(id: string): Promise<unknown> {
    return this.transport.post(this.transport.team(`/agents/${id}/pause`), {});
  }
  resumeAgent(id: string): Promise<unknown> {
    return this.transport.post(this.transport.team(`/agents/${id}/resume`), {});
  }
  stopAgent(id: string): Promise<unknown> {
    return this.transport.post(this.transport.team(`/agents/${id}/stop`), {});
  }
  terminateAgent(id: string): Promise<unknown> {
    return this.transport.post(this.transport.team(`/agents/${id}/terminate`), {});
  }
  archiveAgent(id: string): Promise<unknown> {
    return this.transport.delete(this.transport.team(`/agents/${id}`));
  }

  // --- transcript (WS4) ---
  /** Backfill recent events (`tail` = last N, newest last after the hub's order). */
  async listAgentEvents(id: string, opts: { tail?: number; since?: string } = {}): Promise<Entity[]> {
    const out = await this.transport.get(this.transport.team(`/agents/${id}/events`), {
      tail: opts.tail !== undefined ? String(opts.tail) : undefined,
      since: opts.since,
    });
    return asArray(out);
  }
  getAgentDigest(id: string): Promise<Entity> {
    return this.transport.get(this.transport.team(`/agents/${id}/digest`)) as Promise<Entity>;
  }
  /** Send director text into an agent (flat `{kind:'text', body}` per the hub). */
  postAgentInput(id: string, body: string): Promise<unknown> {
    return this.transport.post(this.transport.team(`/agents/${id}/input`), { kind: 'text', body });
  }

  // --- projects / tasks (WS6) ---
  async listProjects(): Promise<Entity[]> {
    const out = await this.transport.get(this.transport.team('/projects'));
    return asArray(out);
  }
  getProject(id: string): Promise<Entity> {
    return this.transport.get(this.transport.team(`/projects/${id}`)) as Promise<Entity>;
  }
  async listTasks(projectId: string): Promise<Entity[]> {
    const out = await this.transport.get(this.transport.team(`/projects/${projectId}/tasks`));
    return asArray(out);
  }

  // --- team governance (WS7) ---
  /** Team policy (`GET /policy`) — the governance policy document. */
  getPolicy(): Promise<Entity> {
    return this.transport.get(this.transport.team('/policy')) as Promise<Entity>;
  }
  async listPrincipals(): Promise<Entity[]> {
    const out = await this.transport.get(this.transport.team('/principals'));
    return asArray(out);
  }

  // --- operator admin (WS7, cross-team; may 403 for non-operator tokens) ---
  async adminListHosts(): Promise<Entity[]> {
    return asArray(await this.transport.get('/v1/admin/hosts'));
  }
  adminHostAction(host: string, action: 'ping' | 'restart' | 'shutdown' | 'update'): Promise<unknown> {
    return this.transport.post(`/v1/admin/hosts/${host}/${action}`, {});
  }
  async adminListAgents(): Promise<Entity[]> {
    return asArray(await this.transport.get('/v1/admin/agents'));
  }
  adminKillAgent(agent: string): Promise<unknown> {
    return this.transport.post(`/v1/admin/agents/${agent}/kill`, {});
  }
  async adminListTeams(): Promise<Entity[]> {
    return asArray(await this.transport.get('/v1/admin/teams'));
  }

  // --- live streams ---
  streamAgent(agentId: string, opts: SseOptions): SseHandle {
    return streamSse(this.cfg, this.transport.team(`/agents/${agentId}/stream`), opts);
  }
  streamChannel(channel: string, opts: SseOptions): SseHandle {
    return streamSse(this.cfg, this.transport.team(`/channels/${channel}/stream`), opts);
  }
}
