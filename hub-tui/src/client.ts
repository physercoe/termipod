import type { HubConfig } from './config.js';

/**
 * Error thrown for non-2xx HTTP responses. Matches the mobile client's
 * HubApiError so both codebases can speak the same language about hub
 * failures.
 */
export class HubApiError extends Error {
  constructor(public status: number, message: string) {
    super(`HubApiError(${status}): ${message}`);
    this.name = 'HubApiError';
  }
}

/**
 * Thin REST wrapper around the Termipod Hub HTTP API.
 *
 * Kept intentionally dumb: returns the decoded JSON as `unknown`. Views
 * narrow types themselves — no shared schema package yet since the wire
 * shape is still changing slice-by-slice.
 */
export class HubClient {
  constructor(private readonly cfg: HubConfig) {}

  private url(path: string, query?: Record<string, string>): string {
    const base = this.cfg.baseUrl.endsWith('/')
      ? this.cfg.baseUrl.slice(0, -1)
      : this.cfg.baseUrl;
    const qs = query
      ? '?' +
        Object.entries(query)
          .filter(([, v]) => v !== undefined && v !== '')
          .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(v)}`)
          .join('&')
      : '';
    return `${base}${path}${qs}`;
  }

  private authHeaders(extra?: HeadersInit): HeadersInit {
    return {
      Accept: 'application/json',
      Authorization: `Bearer ${this.cfg.token}`,
      ...extra,
    };
  }

  private async request<T = unknown>(
    method: string,
    path: string,
    opts: { query?: Record<string, string>; body?: unknown; auth?: boolean } = {},
  ): Promise<T> {
    const headers: Record<string, string> = { Accept: 'application/json' };
    if (opts.auth !== false) {
      headers.Authorization = `Bearer ${this.cfg.token}`;
    }
    if (opts.body !== undefined) {
      headers['Content-Type'] = 'application/json';
    }
    const res = await fetch(this.url(path, opts.query), {
      method,
      headers,
      body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
    });
    const text = await res.text();
    if (!res.ok) throw new HubApiError(res.status, text);
    if (!text) return null as T;
    return JSON.parse(text) as T;
  }

  // ---- probe ----

  getInfo(): Promise<Record<string, unknown>> {
    return this.request('GET', '/v1/_info', { auth: false });
  }

  verifyAuth(): Promise<unknown> {
    return this.request('GET', `/v1/teams/${this.cfg.teamId}/hosts`);
  }

  // ---- attention ----

  listAttention(status = 'open'): Promise<Array<Record<string, unknown>>> {
    return this.request('GET', `/v1/teams/${this.cfg.teamId}/attention`, {
      query: { status },
    }).then((x) => (x as any) ?? []);
  }

  decideAttention(
    id: string,
    decision: 'approve' | 'reject',
    opts: { by?: string; reason?: string } = {},
  ): Promise<Record<string, unknown>> {
    const body: Record<string, string> = { decision };
    if (opts.by) body.by = opts.by;
    if (opts.reason) body.reason = opts.reason;
    return this.request(
      'POST',
      `/v1/teams/${this.cfg.teamId}/attention/${id}/decide`,
      { body },
    );
  }

  resolveAttention(
    id: string,
    opts: { by?: string; reason?: string } = {},
  ): Promise<Record<string, unknown>> {
    const body: Record<string, string> = {};
    if (opts.by) body.by = opts.by;
    if (opts.reason) body.reason = opts.reason;
    return this.request(
      'POST',
      `/v1/teams/${this.cfg.teamId}/attention/${id}/resolve`,
      { body },
    );
  }

  // ---- inventory ----

  listHosts(): Promise<Array<Record<string, unknown>>> {
    return this.request('GET', `/v1/teams/${this.cfg.teamId}/hosts`).then(
      (x) => (x as any) ?? [],
    );
  }

  listAgents(): Promise<Array<Record<string, unknown>>> {
    return this.request('GET', `/v1/teams/${this.cfg.teamId}/agents`).then(
      (x) => (x as any) ?? [],
    );
  }

  listProjects(): Promise<Array<Record<string, unknown>>> {
    return this.request('GET', `/v1/teams/${this.cfg.teamId}/projects`).then(
      (x) => (x as any) ?? [],
    );
  }

  listChannels(projectId: string): Promise<Array<Record<string, unknown>>> {
    return this.request(
      'GET',
      `/v1/teams/${this.cfg.teamId}/projects/${projectId}/channels`,
    ).then((x) => (x as any) ?? []);
  }

  // ---- tasks ----

  listTasks(
    projectId: string,
    opts: { status?: string } = {},
  ): Promise<Array<Record<string, unknown>>> {
    return this.request(
      'GET',
      `/v1/teams/${this.cfg.teamId}/projects/${projectId}/tasks`,
      { query: opts.status ? { status: opts.status } : undefined },
    ).then((x) => (x as any) ?? []);
  }

  // Exposed for the SSE reader, which builds its own fetch() call.
  streamUrl(projectId: string, channelId: string, since?: string): string {
    return this.url(
      `/v1/teams/${this.cfg.teamId}/projects/${projectId}/channels/${channelId}/stream`,
      since ? { since } : undefined,
    );
  }

  streamHeaders(): HeadersInit {
    return this.authHeaders({
      Accept: 'text/event-stream',
      'Cache-Control': 'no-cache',
    });
  }
}
