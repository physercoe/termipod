import type { HubConfig } from './config';
import { streamSse, type SseHandle, type SseOptions } from './sse';
import { HubTransport } from './transport';
import type { Entity, HubInfo } from './types';

function asArray(v: unknown): Entity[] {
  return Array.isArray(v) ? (v as Entity[]) : [];
}

/** One multimodal attachment on an input turn — `data` is RAW base64. */
export interface WireAttachment {
  mime_type: string;
  data: string;
  filename?: string;
}
export interface InputAttachments {
  images?: WireAttachment[];
  pdfs?: WireAttachment[];
  audios?: WireAttachment[];
  videos?: WireAttachment[];
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
  getSession(id: string): Promise<Entity> {
    return this.transport.get(this.transport.team(`/sessions/${id}`)) as Promise<Entity>;
  }
  /** The session-scoped run digest (ADR-038 §5) — same wire shape as the agent
   * digest, rolled up across the session's agents. Renders in `RunReport`. */
  getSessionDigest(id: string): Promise<Entity> {
    return this.transport.get(this.transport.team(`/sessions/${id}/digest`)) as Promise<Entity>;
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
  /** Send director text (+ optional multimodal attachments) into an agent. Flat
   * `{kind:'text', body, images?, pdfs?, audios?, videos?}` per the hub
   * (handlers_agent_input.go). Each attachment's `data` is RAW base64 (no
   * `data:` prefix); the hub base64-decodes it to enforce size caps. Modalities
   * an engine doesn't support are strip-and-warned hub-side, not rejected. */
  postAgentInput(id: string, body: string, att?: InputAttachments): Promise<unknown> {
    return this.transport.post(this.transport.team(`/agents/${id}/input`), { kind: 'text', body, ...att });
  }

  // --- projects / tasks (WS6) ---
  async listProjects(): Promise<Entity[]> {
    const out = await this.transport.get(this.transport.team('/projects'));
    return asArray(out);
  }
  getProject(id: string): Promise<Entity> {
    return this.transport.get(this.transport.team(`/projects/${id}`)) as Promise<Entity>;
  }
  /** Composed project read — phase + phases + active-phase deliverables + counts. */
  getProjectOverview(id: string): Promise<Entity> {
    return this.transport.get(this.transport.team(`/projects/${id}/overview`)) as Promise<Entity>;
  }
  async listTasks(projectId: string): Promise<Entity[]> {
    const out = await this.transport.get(this.transport.team(`/projects/${projectId}/tasks`));
    return asArray(out);
  }
  /** Patch a task — status / title / assignee / priority (ADR-029; `handlePatchTask`). */
  patchTask(projectId: string, taskId: string, patch: Record<string, unknown>): Promise<unknown> {
    return this.transport.patch(this.transport.team(`/projects/${projectId}/tasks/${taskId}`), patch);
  }
  /** Create a task (direct write — `handleCreateTask`). `title` required;
   * `priority` ∈ low|med|high|urgent; `status` defaults todo hub-side. */
  createTask(
    projectId: string,
    body: { title: string; body_md?: string; status?: string; priority?: string },
  ): Promise<Entity> {
    return this.transport.post(this.transport.team(`/projects/${projectId}/tasks`), body) as Promise<Entity>;
  }
  /** Runs are team-scoped, filtered by `?project=` (NOT nested under the
   * project path — `GET /v1/teams/{team}/runs`, `handleListRuns`). */
  async listRuns(projectId: string): Promise<Entity[]> {
    const out = await this.transport.get(this.transport.team('/runs'), { project: projectId });
    return asArray(out);
  }
  /** Plans are likewise team-scoped with a `?project=` filter (`handleListPlans`). */
  async listPlans(projectId: string): Promise<Entity[]> {
    const out = await this.transport.get(this.transport.team('/plans'), { project: projectId });
    return asArray(out);
  }

  // --- team governance (WS7) ---
  /** Team policy as raw YAML text (`GET /policy` serves `application/yaml`, so
   * this must not JSON-parse). Empty string when no policy file exists. */
  getPolicyText(): Promise<string> {
    return this.transport.getText(this.transport.team('/policy'));
  }
  async listPrincipals(): Promise<Entity[]> {
    const out = await this.transport.get(this.transport.team('/principals'));
    return asArray(out);
  }
  /** Replace the team policy document (`PUT /policy`) — raw YAML body. */
  putPolicy(yamlText: string): Promise<unknown> {
    return this.transport.putText(this.transport.team('/policy'), yamlText);
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
  /** Rotate a team's owner token — returns the freshly-minted token. */
  adminRotateTeamToken(team: string): Promise<Entity> {
    return this.transport.post(`/v1/admin/teams/${team}/rotate-token`, {}) as Promise<Entity>;
  }
  /** Rotate the hub host token (upkeep). */
  adminRotateHostTokens(reason?: string): Promise<Entity> {
    return this.transport.post('/v1/admin/tokens/rotate', reason ? { reason } : {}) as Promise<Entity>;
  }
  /** VACUUM the hub database — returns bytes before/after/reclaimed. */
  adminDBVacuum(): Promise<Entity> {
    return this.transport.post('/v1/admin/db/vacuum', {}) as Promise<Entity>;
  }

  // --- zero-knowledge vault (Phase 2b, ADR-052 D-3/D-4) ---
  /** The sealed vault blob; throws HubApiError(404) when no vault exists. */
  getVault(): Promise<Entity> {
    return this.transport.get(this.transport.team('/vault')) as Promise<Entity>;
  }
  /** Push a sealed vault. `baseVersion` 0 creates; else optimistic-locks on the
   * current version (409 on conflict). */
  putVault(ciphertext: string, baseVersion: number): Promise<Entity> {
    return this.transport.put(this.transport.team('/vault'), { ciphertext, base_version: baseVersion }) as Promise<Entity>;
  }
  getVaultRecovery(): Promise<Entity> {
    return this.transport.get(this.transport.team('/vault/recovery')) as Promise<Entity>;
  }
  setVaultRecovery(envelope: string, hint?: string): Promise<Entity> {
    const body = hint !== undefined ? { recovery_envelope: envelope, recovery_hint: hint } : { recovery_envelope: envelope };
    return this.transport.put(this.transport.team('/vault/recovery'), body) as Promise<Entity>;
  }
  async listVaultDevices(): Promise<Entity[]> {
    const out = (await this.transport.get(this.transport.team('/vault/devices'))) as Entity;
    return asArray(out?.devices);
  }
  putVaultDevice(
    deviceId: string,
    body: { device_name?: string; public_key?: string; wrapped_key?: string },
  ): Promise<Entity> {
    return this.transport.put(this.transport.team(`/vault/devices/${deviceId}`), body) as Promise<Entity>;
  }

  // --- channels (chat, Phase 4) ---
  async listChannels(): Promise<Entity[]> {
    return asArray(await this.transport.get(this.transport.team('/channels')));
  }
  /** Recent channel events, newest-last after sorting on ts (`limit` default 100
   * hub-side). */
  async listChannelEvents(channel: string, opts: { limit?: number; since?: string } = {}): Promise<Entity[]> {
    return asArray(
      await this.transport.get(this.transport.team(`/channels/${channel}/events`), {
        limit: opts.limit !== undefined ? String(opts.limit) : undefined,
        since: opts.since,
      }),
    );
  }
  /** Post a director chat message (`type:"message"`, one text part). */
  postChannelMessage(channel: string, text: string): Promise<unknown> {
    return this.transport.post(this.transport.team(`/channels/${channel}/events`), {
      type: 'message',
      parts: [{ kind: 'text', text }],
    });
  }

  // --- live streams ---
  streamAgent(agentId: string, opts: SseOptions): SseHandle {
    return streamSse(this.cfg, this.transport.team(`/agents/${agentId}/stream`), opts);
  }
  streamChannel(channel: string, opts: SseOptions): SseHandle {
    return streamSse(this.cfg, this.transport.team(`/channels/${channel}/stream`), opts);
  }
}
