import type { HubConfig } from './config';
import { HubApiError } from './errors';

export type Json = unknown;
type Query = Record<string, string | undefined>;

/// Shared HTTP transport for the hub SDK — the web analogue of Dart's
/// HubTransport. Injects the bearer token, builds team-scoped paths, and maps
/// non-2xx responses to HubApiError (incl. teamGate 403 on scope≠path).
///
/// In the browser build this uses `fetch` directly. Under Tauri the same calls
/// can be routed through the Rust core (which holds the token); the frontend
/// interface is unchanged.
export class HubTransport {
  constructor(private readonly cfg: HubConfig) {}

  get teamId(): string {
    return this.cfg.teamId;
  }

  get baseUrl(): string {
    return this.cfg.baseUrl.replace(/\/+$/, '');
  }

  /** Team-scoped path prefix: `/v1/teams/{team}{path}`. */
  team(path: string): string {
    return `/v1/teams/${this.cfg.teamId}${path}`;
  }

  headers(auth = true): Record<string, string> {
    const h: Record<string, string> = { 'content-type': 'application/json' };
    if (auth && this.cfg.token) h.authorization = `Bearer ${this.cfg.token}`;
    return h;
  }

  private buildUrl(path: string, query?: Query): string {
    const u = new URL(this.baseUrl + path);
    if (query) {
      for (const [k, v] of Object.entries(query)) {
        if (v !== undefined) u.searchParams.set(k, v);
      }
    }
    return u.toString();
  }

  private async request(
    method: string,
    path: string,
    opts: { body?: Json; query?: Query; auth?: boolean } = {},
  ): Promise<Json> {
    const res = await fetch(this.buildUrl(path, opts.query), {
      method,
      headers: this.headers(opts.auth ?? true),
      body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
    });
    const text = await res.text();
    if (!res.ok) throw new HubApiError(res.status, text);
    return text ? (JSON.parse(text) as Json) : null;
  }

  get(path: string, query?: Query, auth = true): Promise<Json> {
    return this.request('GET', path, { query, auth });
  }
  post(path: string, body: Json, query?: Query): Promise<Json> {
    return this.request('POST', path, { body, query });
  }
  put(path: string, body: Json): Promise<Json> {
    return this.request('PUT', path, { body });
  }
  delete(path: string): Promise<Json> {
    return this.request('DELETE', path);
  }

  /** `/v1/_info` is allowed unauthenticated — probe a candidate URL/token. */
  probe(): Promise<Json> {
    return this.get('/v1/_info', undefined, false);
  }
}
