import type { HubConfig } from './config';
import { streamSse, type SseHandle, type SseOptions } from './sse';
import { HubTransport, type Json } from './transport';
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
  async listAudit(params: { limit?: number; before?: string; project_id?: string } = {}): Promise<Entity[]> {
    const out = await this.transport.get(this.transport.team('/audit'), {
      limit: params.limit !== undefined ? String(params.limit) : undefined,
      before: params.before,
      project_id: params.project_id,
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
  /** List agents (`handleListAgents`). Optional filters: `project_id` (agents on
   * one project — note it's `project_id`, NOT `project`), `host_id`, `status`,
   * and `include_terminated`/`include_archived` to surface stopped rows (default
   * hides terminated/failed/crashed/archived). */
  async listAgents(
    params: {
      project_id?: string;
      host_id?: string;
      status?: string;
      include_terminated?: boolean;
      include_archived?: boolean;
    } = {},
  ): Promise<Entity[]> {
    const out = await this.transport.get(this.transport.team('/agents'), {
      project_id: params.project_id,
      host_id: params.host_id,
      status: params.status,
      include_terminated: params.include_terminated === true ? '1' : undefined,
      include_archived: params.include_archived === true ? '1' : undefined,
    });
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
  /** Rename a session (handlePatchSession — the only editable field is `title`;
   * '' clears it back to untitled). Returns 204, no body. */
  renameSession(id: string, title: string): Promise<unknown> {
    return this.transport.patch(this.transport.team(`/sessions/${id}`), { title });
  }
  /** The session-scoped run digest (ADR-038 §5) — same wire shape as the agent
   * digest, rolled up across the session's agents. Renders in `RunReport`. */
  getSessionDigest(id: string): Promise<Entity> {
    return this.transport.get(this.transport.team(`/sessions/${id}/digest`)) as Promise<Entity>;
  }

  // --- reference library (ADR-053) ---
  /** List the team's references (the hub-owned library shared with agents).
   *  Filters mirror the REST handler: collection / tag / source / q. */
  async listReferences(
    params: { collection?: string; tag?: string; source?: string; q?: string } = {},
  ): Promise<Entity[]> {
    const out = await this.transport.get(this.transport.team('/references'), {
      collection: params.collection,
      tag: params.tag,
      source: params.source,
      q: params.q,
    });
    return asArray(out);
  }
  getReference(id: string): Promise<Entity> {
    return this.transport.get(this.transport.team(`/references/${id}`)) as Promise<Entity>;
  }
  createReference(body: Json): Promise<Entity> {
    return this.transport.post(this.transport.team('/references'), body) as Promise<Entity>;
  }
  updateReference(id: string, patch: Json): Promise<Entity> {
    return this.transport.patch(this.transport.team(`/references/${id}`), patch) as Promise<Entity>;
  }
  deleteReference(id: string): Promise<Json> {
    return this.transport.delete(this.transport.team(`/references/${id}`));
  }

  // --- reference annotations (ADR-053 companion, migration 0064) ---
  /** PDF annotations are child records of a reference; agents reach the same
   *  store via the `reference_annotation_*` MCP tools. The whole tree is nested
   *  under the parent reference, so update/delete carry the ref id too. */
  async listAnnotations(refId: string): Promise<Entity[]> {
    const out = await this.transport.get(this.transport.team(`/references/${refId}/annotations`));
    return asArray(out);
  }
  createAnnotation(refId: string, body: Json): Promise<Entity> {
    return this.transport.post(this.transport.team(`/references/${refId}/annotations`), body) as Promise<Entity>;
  }
  updateAnnotation(refId: string, annId: string, patch: Json): Promise<Entity> {
    return this.transport.patch(
      this.transport.team(`/references/${refId}/annotations/${annId}`),
      patch,
    ) as Promise<Entity>;
  }
  deleteAnnotation(refId: string, annId: string): Promise<Json> {
    return this.transport.delete(this.transport.team(`/references/${refId}/annotations/${annId}`));
  }

  /** Spawn the project's bound domain steward (`handleStartProject`) — direct
   * principal action, materialize-then-start (ADR-046). */
  startProject(id: string): Promise<Entity> {
    return this.transport.post(this.transport.team(`/projects/${id}/start`), {}) as Promise<Entity>;
  }
  /** PATCH a project's mutable fields (`handleUpdateProject`) — e.g. bind a
   * steward via `on_create_template_id` so the project becomes startable.
   * Returns the updated project. */
  updateProject(id: string, patch: Record<string, unknown>): Promise<Entity> {
    return this.transport.patch(this.transport.team(`/projects/${id}`), patch) as Promise<Entity>;
  }

  // --- agent spawn (Phase 4 / F3) ---
  /** Spawn an agent (`handleSpawn`, self-governing). Returns `{status, agent_id}`
   * on immediate spawn, or `202 {status:"pending_approval", attention_id}` when
   * policy requires approval — that item then appears in the Attention dock.
   * `child_handle` / `kind` / `host_id` required. */
  spawnAgent(body: {
    child_handle: string;
    kind: string;
    host_id: string;
    project_id?: string;
    // Link the spawn to an existing task (ADR-029 D-2). Mutually exclusive with
    // `task` below — the hub 4xxs if both are set. The task's status then flips
    // todo→in_progress via the existing derivation, not a client PATCH.
    task_id?: string;
    task?: { title: string; body_md?: string };
  }): Promise<Entity> {
    return this.transport.post(this.transport.team('/agents/spawn'), body) as Promise<Entity>;
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
  /** Backfill events (handlers_agent_events.go). `session` scopes the query to a
   * whole session across its respawned agents (when set, the URL agent is ignored
   * and the query runs by session_id, ordered on the dense `session_ordinal` —
   * ADR-042; parity — mobile listAgentEvents `sessionId`). Cursors:
   * - `tail` = the newest N. NB the hub gates newest-first on `tail=true` + a
   *   `limit`; a bare `tail=<number>` reads as false and silently serves the
   *   OLDEST page — so we send the flag + the count separately.
   * - `beforeOrdinal`/`afterOrdinal` = the session-scoped random-access window
   *   (`session_ordinal < / > n`) — the load-older / jump-around-anchor cursors
   *   the Insight navigator uses to reach a turn outside the loaded tail.
   * - `since` = incremental `seq > n` (per-agent); `limit` for the cursor pagers. */
  async listAgentEvents(
    id: string,
    opts: { tail?: number; since?: string; session?: string; beforeOrdinal?: number; afterOrdinal?: number; limit?: number } = {},
  ): Promise<Entity[]> {
    const q: Record<string, string | undefined> = {
      since: opts.since,
      session: opts.session,
      before_ordinal: opts.beforeOrdinal !== undefined ? String(opts.beforeOrdinal) : undefined,
      after_ordinal: opts.afterOrdinal !== undefined ? String(opts.afterOrdinal) : undefined,
    };
    if (opts.tail !== undefined) {
      q.tail = 'true';
      q.limit = String(opts.tail);
    } else if (opts.limit !== undefined) {
      q.limit = String(opts.limit);
    }
    const out = await this.transport.get(this.transport.team(`/agents/${id}/events`), q);
    return asArray(out);
  }
  getAgentDigest(id: string): Promise<Entity> {
    return this.transport.get(this.transport.team(`/agents/${id}/digest`)) as Promise<Entity>;
  }
  /** The per-turn index (`handleListAgentTurns`) — one row per turn with
   * `start_seq`/`start_ordinal`/`status`/`duration_ms`/`tool_count`/`error_count`.
   * Backs the Insight-mode Turns navigator (jump-to-turn by start_seq). */
  async listAgentTurns(id: string, opts: { after?: string; limit?: number } = {}): Promise<Entity[]> {
    const out = (await this.transport.get(this.transport.team(`/agents/${id}/turns`), {
      after: opts.after,
      limit: opts.limit !== undefined ? String(opts.limit) : undefined,
    })) as Entity;
    return asArray(out?.turns);
  }
  /** Session-scoped turn index (`handleListSessionTurns` → `{session_id,
   * agent_ids, turns}`) — the ts-ordered UNION of the session's agents' turns,
   * so a resumed session's navigator shows every turn, not just the current
   * agent's. Each row carries `start_ordinal` (the dense `session_ordinal`
   * anchor), which the transcript jumps by (per-agent `start_seq` collides
   * across the resume). Cursor is `after_ts`. */
  async listSessionTurns(sessionId: string, opts: { afterTs?: string; limit?: number } = {}): Promise<Entity[]> {
    const out = (await this.transport.get(this.transport.team(`/sessions/${sessionId}/turns`), {
      after_ts: opts.afterTs,
      limit: opts.limit !== undefined ? String(opts.limit) : undefined,
    })) as Entity;
    return asArray(out?.turns);
  }
  /** Send director text (+ optional multimodal attachments) into an agent. Flat
   * `{kind:'text', body, images?, pdfs?, audios?, videos?}` per the hub
   * (handlers_agent_input.go). Each attachment's `data` is RAW base64 (no
   * `data:` prefix); the hub base64-decodes it to enforce size caps. Modalities
   * an engine doesn't support are strip-and-warned hub-side, not rejected.
   * `raw: true` (mobile v1.0.707) marks a slash-command body so the hub skips
   * the principal-directive envelope and the engine receives it verbatim. */
  postAgentInput(id: string, body: string, att?: InputAttachments, raw?: boolean): Promise<unknown> {
    return this.transport.post(this.transport.team(`/agents/${id}/input`), {
      kind: 'text',
      body,
      ...att,
      ...(raw === true ? { raw: true } : {}),
    });
  }
  /** Interrupt the agent's current turn (parity — mobile agents_api `_cancel`:
   * `postAgentInput(kind:'cancel')`). Lands in agent_events as a `producer:'user'`
   * cancel input the driver acts on — distinct from the `/stop` lifecycle, which
   * is the RESUMABLE KILL: it pauses the session and drops the agent from the
   * live list, which reads to the director as "archived", not "interrupt this
   * turn". The composer's stop-while-generating must cancel, not kill. */
  cancelAgentInput(id: string, reason?: string): Promise<unknown> {
    return this.transport.post(this.transport.team(`/agents/${id}/input`), {
      kind: 'cancel',
      ...(reason !== undefined && reason !== '' ? { reason } : {}),
    });
  }

  // --- projects / tasks (WS6) ---
  async listProjects(): Promise<Entity[]> {
    const out = await this.transport.get(this.transport.team('/projects'));
    return asArray(out);
  }
  getProject(id: string): Promise<Entity> {
    return this.transport.get(this.transport.team(`/projects/${id}`)) as Promise<Entity>;
  }
  /** Create a project (direct POST — `handleCreateProject`; a principal is
   * admitted, agents must `propose(kind="project.create")`). `name` required;
   * `kind` ∈ goal|standing. */
  createProject(body: {
    name: string;
    kind?: string;
    goal?: string;
    config_yaml?: string;
    docs_root?: string;
  }): Promise<Entity> {
    return this.transport.post(this.transport.team('/projects'), body) as Promise<Entity>;
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
  /** Launch a run (`handleCreateRun`, direct write). `project_id` required;
   * status is created `pending`. */
  createRun(body: { project_id: string; agent_id?: string; config_json?: unknown; seed?: number }): Promise<Entity> {
    return this.transport.post(this.transport.team('/runs'), body) as Promise<Entity>;
  }
  /** Author a plan (`handleCreatePlan`, direct write). `project_id` required;
   * status is created `draft`. `spec_json` is the plan body. */
  createPlan(body: { project_id: string; template_id?: string; version?: number; spec_json?: unknown }): Promise<Entity> {
    return this.transport.post(this.transport.team('/plans'), body) as Promise<Entity>;
  }
  /** One plan (`handleGetPlan`) — status, version, spec_json, timestamps. */
  getPlan(id: string): Promise<Entity> {
    return this.transport.get(this.transport.team(`/plans/${id}`)) as Promise<Entity>;
  }
  /** Edit a plan (`handleUpdatePlan`, `PATCH …/plans/{id}`, 204). `status` ∈
   * draft|ready|running|completed|failed|cancelled; `spec_json` replaces the body. */
  updatePlan(id: string, patch: { status?: string; spec_json?: unknown }): Promise<unknown> {
    return this.transport.patch(this.transport.team(`/plans/${id}`), patch);
  }
  /** A plan's steps (`handleListPlanSteps`, bare array). */
  async listPlanSteps(planId: string): Promise<Entity[]> {
    return asArray(await this.transport.get(this.transport.team(`/plans/${planId}/steps`)));
  }
  /** Edit a plan step (`handleUpdatePlanStep`, `PATCH …/plans/{id}/steps/{step}`).
   * `status` ∈ pending|running|completed|failed|blocked|skipped. */
  updatePlanStep(
    planId: string,
    stepId: string,
    patch: { status?: string; started_at?: string; completed_at?: string; agent_id?: string; input_refs_json?: unknown; output_refs_json?: unknown },
  ): Promise<unknown> {
    return this.transport.patch(this.transport.team(`/plans/${planId}/steps/${stepId}`), patch);
  }
  /** A single run (`handleGetRun`) — `runOut`: status, config_json, seed,
   * started_at, finished_at, agent_id, trackio refs, parent_run_id. */
  getRun(id: string): Promise<Entity> {
    return this.transport.get(this.transport.team(`/runs/${id}`)) as Promise<Entity>;
  }
  /** The run's hyperparameter config envelope (`GET …/runs/{id}/config` →
   * `{config, updated_at}`). */
  getRunConfig(id: string): Promise<Entity> {
    return this.transport.get(this.transport.team(`/runs/${id}/config`)) as Promise<Entity>;
  }
  /** Content-addressed run/project outputs (`handleListArtifacts`, bare array).
   * Filter by `project` and/or `run` (note: `run`, not `run_id`). */
  async listArtifacts(params: { project?: string; run?: string; kind?: string } = {}): Promise<Entity[]> {
    return asArray(await this.transport.get(this.transport.team('/artifacts'), params));
  }
  /** Edit a run (`handleUpdateRun`, `PATCH …/runs/{id}`). All fields optional;
   * `status` ∈ pending|running|completed|failed|cancelled. Setting
   * `trackio_run_uri` without a host auto-derives it from the agent. */
  updateRun(
    id: string,
    patch: {
      status?: string;
      config_json?: unknown;
      seed?: number;
      agent_id?: string;
      started_at?: string;
      finished_at?: string;
      trackio_host_id?: string;
      trackio_run_uri?: string;
      parent_run_id?: string;
    },
  ): Promise<Entity> {
    return this.transport.patch(this.transport.team(`/runs/${id}`), patch) as Promise<Entity>;
  }
  /** Scalar training metrics (`handleGetRunMetrics`, bare array). Each row:
   * `{name, points:[{step,value}…], sample_count, last_step?, last_value?, updated_at}`. */
  async getRunMetrics(id: string): Promise<Entity[]> {
    return asArray(await this.transport.get(this.transport.team(`/runs/${id}/metrics`)));
  }
  /** GPU/CPU system metrics (`handleGetRunSystemMetrics`, same shape as /metrics). */
  async getRunSystemMetrics(id: string): Promise<Entity[]> {
    return asArray(await this.transport.get(this.transport.team(`/runs/${id}/system_metrics`)));
  }
  /** Logged run images (`handleGetRunImages`, bare array). Each row:
   * `{id, metric_name, step, blob_sha, caption?, created_at}`; fetch bytes via
   * `getBlobDataUrl(blob_sha)`. Optional `metric` filter. */
  async getRunImages(id: string, metric?: string): Promise<Entity[]> {
    return asArray(await this.transport.get(this.transport.team(`/runs/${id}/images`), { metric }));
  }
  /** Logged histograms (`handleGetRunHistograms`, bare array). Each row:
   * `{name, step, buckets, updated_at}`. Optional `metric` filter. */
  async getRunHistograms(id: string, metric?: string): Promise<Entity[]> {
    return asArray(await this.transport.get(this.transport.team(`/runs/${id}/histograms`), { metric }));
  }

  // --- deliverables + ratify (Phase 4) ---
  /** Deliverables for a project (`handleListDeliverables` → `{items:[…]}`).
   * `include=components` populates each deliverable's components array. */
  async listDeliverables(projectId: string, opts: { phase?: string; include?: 'components' } = {}): Promise<Entity[]> {
    const out = (await this.transport.get(this.transport.team(`/projects/${projectId}/deliverables`), {
      phase: opts.phase,
      include: opts.include,
    })) as Entity;
    return asArray(out?.items);
  }
  /** Ratify a deliverable (`POST …/ratify`, direct — any team token, records
   * `ratified_by_actor`). 409 if already ratified or phase gate blocks. */
  ratifyDeliverable(projectId: string, deliverableId: string, rationale?: string): Promise<Entity> {
    return this.transport.post(
      this.transport.team(`/projects/${projectId}/deliverables/${deliverableId}/ratify`),
      rationale !== undefined ? { rationale } : {},
    ) as Promise<Entity>;
  }
  /** Revert a ratified deliverable back to draft (`POST …/unratify`). */
  unratifyDeliverable(projectId: string, deliverableId: string): Promise<Entity> {
    return this.transport.post(
      this.transport.team(`/projects/${projectId}/deliverables/${deliverableId}/unratify`),
      {},
    ) as Promise<Entity>;
  }
  /** Move a deliverable's state (`PATCH …` — draft|in-review ONLY; `ratified`
   * is rejected, use ratify). */
  patchDeliverable(projectId: string, deliverableId: string, patch: { ratification_state?: string }): Promise<Entity> {
    return this.transport.patch(
      this.transport.team(`/projects/${projectId}/deliverables/${deliverableId}`),
      patch,
    ) as Promise<Entity>;
  }

  /** One deliverable with its components (`handleGetDeliverable` — always
   * includes components: `{kind: document|artifact|run|commit, ref_id, required}`). */
  getDeliverable(projectId: string, deliverableId: string): Promise<Entity> {
    return this.transport.get(this.transport.team(`/projects/${projectId}/deliverables/${deliverableId}`)) as Promise<Entity>;
  }
  /** Send a deliverable back for revision (`POST …/send-back`, `handleSendBack`).
   * `note` required; moves state to `in-review` and raises a `revision_requested`
   * attention item. 409 if the deliverable is currently ratified (unratify first),
   * 422 if an annotation id doesn't belong to a component document. */
  sendBackDeliverable(projectId: string, deliverableId: string, body: { note: string; annotation_ids?: string[] }): Promise<Entity> {
    return this.transport.post(
      this.transport.team(`/projects/${projectId}/deliverables/${deliverableId}/send-back`),
      body,
    ) as Promise<Entity>;
  }

  // --- acceptance criteria (Phase 4 / parity) ---
  /** A project's acceptance criteria (`handleListCriteria` → `{items:[…]}`).
   * Each: `{id, phase, kind: text|metric|gate, body, state: pending|met|failed|
   * waived, deliverable_id, required, ord}`. Filter by `phase`/`deliverable_id`. */
  async listCriteria(projectId: string, opts: { phase?: string; deliverable_id?: string } = {}): Promise<Entity[]> {
    const out = (await this.transport.get(this.transport.team(`/projects/${projectId}/criteria`), {
      phase: opts.phase,
      deliverable_id: opts.deliverable_id,
    })) as Entity;
    return asArray(out?.items);
  }
  /** Act on a criterion (`POST …/criteria/{id}/{action}`, action ∈ mark-met |
   * mark-failed | waive). Body carries optional evidence_ref / reason. */
  criterionAction(
    projectId: string,
    criterionId: string,
    action: 'mark-met' | 'mark-failed' | 'waive',
    body: { evidence_ref?: string; reason?: string } = {},
  ): Promise<Entity> {
    return this.transport.post(
      this.transport.team(`/projects/${projectId}/criteria/${criterionId}/${action}`),
      body,
    ) as Promise<Entity>;
  }
  /** Create an acceptance criterion (`handleCreateCriterion`, direct write, 201).
   * `phase` + `kind` (text|metric|gate) required; `body` is a free-form object
   * (text → {description}; metric → {metric, operator, threshold}). Optional
   * `deliverable_id` (must exist in the project), `required` (default true), `ord`. */
  createCriterion(
    projectId: string,
    body: { phase: string; kind: 'text' | 'metric' | 'gate'; body?: Record<string, unknown>; deliverable_id?: string; required?: boolean; ord?: number },
  ): Promise<Entity> {
    return this.transport.post(this.transport.team(`/projects/${projectId}/criteria`), body) as Promise<Entity>;
  }

  // --- project docs_root files (parity Files tab) ---
  /** The project's `docs_root` filesystem tree (`handleListProjectDocs` →
   * `[{path, is_dir, size, mod_time}]`, metadata only). Distinct from the DB
   * `documents` entity. */
  async listProjectDocs(projectId: string): Promise<Entity[]> {
    return asArray(await this.transport.get(this.transport.team(`/projects/${projectId}/docs`)));
  }
  /** Raw text of one `docs_root` file (`handleGetProjectDoc` returns the file
   * bytes inline with a by-extension content-type). */
  getProjectDocText(projectId: string, path: string): Promise<string> {
    const enc = path.split('/').map(encodeURIComponent).join('/');
    return this.transport.getText(this.transport.team(`/projects/${projectId}/docs/${enc}`));
  }

  // --- documents (Phase 4) ---
  /** DB-row documents (`handleListDocuments`). List rows omit `content_inline`. */
  async listDocuments(projectId?: string, kind?: string): Promise<Entity[]> {
    const out = await this.transport.get(this.transport.team('/documents'), { project: projectId, kind });
    return asArray(out);
  }
  /** One document with `content_inline` (markdown body) when authored inline;
   * else `artifact_id` points at a blob-backed artifact (`handleGetDocument`). */
  getDocument(id: string): Promise<Entity> {
    return this.transport.get(this.transport.team(`/documents/${id}`)) as Promise<Entity>;
  }
  /** Compose a document (`handleCreateDocument`, direct write, 201). `project_id`,
   * `kind`, `title` required; plain-markdown kinds are memo|draft|report|review|
   * sample. Exactly one of `content_inline` (≤256 KiB) / `artifact_id`. Pass
   * `prev_version_id` to author a new version of an existing document (the edit
   * mechanism — documents are versioned, there is no whole-document PATCH). */
  createDocument(body: {
    project_id: string;
    kind: string;
    title: string;
    content_inline?: string;
    artifact_id?: string;
    schema_id?: string;
    prev_version_id?: string;
  }): Promise<Entity> {
    return this.transport.post(this.transport.team('/documents'), body) as Promise<Entity>;
  }

  // --- insights analytics (Phase 4, ADR-038/039/041) ---
  /** The insights aggregator (`handleInsights`, NOT team-scoped by path — scopes
   * to the token's team). Pass EXACTLY ONE scope key
   * (project_id|team_id|agent_id|engine|host_id) + optional RFC3339 since/until. */
  getInsights(scope: {
    project_id?: string;
    team_id?: string;
    agent_id?: string;
    engine?: string;
    host_id?: string;
    kind?: string;
    since?: string;
    until?: string;
  }): Promise<Entity> {
    return this.transport.get('/v1/insights', scope as Record<string, string | undefined>) as Promise<Entity>;
  }

  // --- search (Phase 4) ---
  /** Full-text search over event text parts (`handleSearch`, `/v1/search`, NOT
   * team-scoped by path — token-scoped; 403 for a teamless token). Returns
   * `{id, received_ts, channel_id, type, from_id, parts}` rows. */
  async searchEvents(q: string, limit = 50): Promise<Entity[]> {
    const out = await this.transport.get('/v1/search', { q, limit: String(limit) });
    return asArray(out);
  }

  // --- governance depth: templates + agent families (read + write, Phase 5) ---
  async listTemplates(category?: string): Promise<Entity[]> {
    return asArray(await this.transport.get(this.transport.team('/templates'), { category }));
  }
  /** Raw bytes of one template (`handleGetTemplate` — YAML/markdown/JSON served
   * verbatim). `merge=1` folds the per-team overlay over the bundled default. */
  getTemplateText(category: string, name: string, merge = false): Promise<string> {
    return this.transport.getText(
      this.transport.team(`/templates/${encodeURIComponent(category)}/${encodeURIComponent(name)}`) + (merge ? '?merge=1' : ''),
    );
  }
  /** Create/overwrite a template (`handlePutTemplate`, raw body ≤1 MiB, writes a
   * per-team overlay file). 403 if an agent edits its own kind's template
   * (principal/director bypass). */
  putTemplate(category: string, name: string, text: string): Promise<unknown> {
    return this.transport.putText(this.transport.team(`/templates/${encodeURIComponent(category)}/${encodeURIComponent(name)}`), text);
  }
  /** Remove a template overlay (`handleDeleteTemplate`; falls back to bundled). */
  deleteTemplate(category: string, name: string): Promise<unknown> {
    return this.transport.delete(this.transport.team(`/templates/${encodeURIComponent(category)}/${encodeURIComponent(name)}`));
  }
  /** Rename a template (`handleRenameTemplate`, `PATCH` body `{new_name}`). */
  renameTemplate(category: string, name: string, newName: string): Promise<unknown> {
    return this.transport.patch(this.transport.team(`/templates/${encodeURIComponent(category)}/${encodeURIComponent(name)}`), { new_name: newName });
  }
  async listAgentFamilies(): Promise<Entity[]> {
    return asArray(await this.transport.get(this.transport.team('/agent-families')));
  }
  /** Raw YAML of one agent family (`handleGetAgentFamily`, single Family record). */
  getAgentFamilyText(family: string): Promise<string> {
    return this.transport.getText(this.transport.team(`/agent-families/${encodeURIComponent(family)}`));
  }
  /** Create/overwrite an agent family (`handlePutAgentFamily`, raw YAML ≤8 KiB,
   * strict-parsed; body `family` must equal the path). Writes an overlay file. */
  putAgentFamily(family: string, yamlText: string): Promise<unknown> {
    return this.transport.putText(this.transport.team(`/agent-families/${encodeURIComponent(family)}`), yamlText);
  }
  /** Remove an agent-family overlay (`handleDeleteAgentFamily`; 409 if the family
   * is embedded-only — write an override instead). */
  deleteAgentFamily(family: string): Promise<unknown> {
    return this.transport.delete(this.transport.team(`/agent-families/${encodeURIComponent(family)}`));
  }

  // --- content-addressed blob bytes (parity — run media, doc/artifact viewers) ---
  /** Fetch a blob by sha and return a `data:` URL for direct `<img>`/embedding
   * (`GET /v1/blobs/{sha}` — auth-gated raw bytes; NOT team-scoped by path). The
   * bytes cross the Rust core as base64 so binary survives the string transport. */
  async getBlobDataUrl(sha: string): Promise<string> {
    const { mime, base64 } = await this.transport.getBytes(`/v1/blobs/${encodeURIComponent(sha)}`);
    return `data:${mime || 'application/octet-stream'};base64,${base64}`;
  }

  /** Fetch a blob's raw `{ mime, base64 }` — used by the artifact viewer, which
   * needs the mime to pick a renderer and the base64 to decode text bodies. */
  getBlobBytes(sha: string): Promise<{ mime: string; base64: string }> {
    return this.transport.getBytes(`/v1/blobs/${encodeURIComponent(sha)}`);
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
   * current version (409 on conflict). `deviceName` (optional) records which
   * machine pushed, for the "last synced from <machine>" status. */
  putVault(ciphertext: string, baseVersion: number, deviceName?: string): Promise<Entity> {
    const body: Record<string, unknown> = { ciphertext, base_version: baseVersion };
    if (deviceName !== undefined && deviceName !== '') body.device_name = deviceName;
    return this.transport.put(this.transport.team('/vault'), body) as Promise<Entity>;
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
    // Channel events key the backfill cursor on `received_ts`, not `seq`.
    return streamSse(this.cfg, this.transport.team(`/channels/${channel}/stream`), {
      cursorField: 'received_ts',
      ...opts,
    });
  }
}
