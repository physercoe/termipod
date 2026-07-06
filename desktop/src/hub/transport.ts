import { invoke } from '@tauri-apps/api/core';
import { isTauri } from '../platform';
import type { HubConfig } from './config';
import { HubApiError } from './errors';

export type Json = unknown;
type Query = Record<string, string | undefined>;

interface RawResponse {
  status: number;
  body: string;
}

/// Shared HTTP transport for the hub SDK — the web analogue of Dart's
/// HubTransport. Injects the bearer token, builds team-scoped paths, and maps
/// non-2xx responses to HubApiError (incl. teamGate 403 on scope≠path).
///
/// Under Tauri the request is routed through the Rust core's `hub_request`
/// command (reqwest) — the webview's `fetch` would be a cross-origin call the
/// hub rejects (no CORS) and also exposes the token to JS. The plain-browser
/// build uses `fetch` directly.
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

  /** The one place the two transports diverge: Rust core vs webview fetch. */
  private async raw(
    method: string,
    url: string,
    headers: Record<string, string>,
    bodyText?: string,
  ): Promise<RawResponse> {
    if (isTauri()) {
      return await invoke<RawResponse>('hub_request', {
        req: { method, url, headers, body: bodyText ?? null },
      });
    }
    const res = await fetch(url, { method, headers, body: bodyText });
    return { status: res.status, body: await res.text() };
  }

  private async request(
    method: string,
    path: string,
    opts: { body?: Json; query?: Query; auth?: boolean } = {},
  ): Promise<Json> {
    const bodyText = opts.body !== undefined ? JSON.stringify(opts.body) : undefined;
    const { status, body } = await this.raw(
      method,
      this.buildUrl(path, opts.query),
      this.headers(opts.auth ?? true),
      bodyText,
    );
    if (status < 200 || status >= 300) throw new HubApiError(status, body);
    return body ? (JSON.parse(body) as Json) : null;
  }

  /** Send a raw (non-JSON) body — e.g. the policy document is YAML text the
   * hub reads verbatim (`handlePutPolicy` yaml.Unmarshals the raw bytes). */
  private async requestRaw(method: string, path: string, bodyText: string): Promise<Json> {
    const h = this.headers(true);
    h['content-type'] = 'application/yaml';
    const { status, body } = await this.raw(method, this.buildUrl(path), h, bodyText);
    if (status < 200 || status >= 300) throw new HubApiError(status, body);
    return body ? (JSON.parse(body) as Json) : null;
  }

  get(path: string, query?: Query, auth = true): Promise<Json> {
    return this.request('GET', path, { query, auth });
  }
  /** GET a raw text body (e.g. the policy document is YAML, not JSON — the hub
   * serves it with `Content-Type: application/yaml` and JSON.parse would throw). */
  async getText(path: string): Promise<string> {
    const { status, body } = await this.raw('GET', this.buildUrl(path), this.headers(true));
    if (status < 200 || status >= 300) throw new HubApiError(status, body);
    return body;
  }
  post(path: string, body: Json, query?: Query): Promise<Json> {
    return this.request('POST', path, { body, query });
  }
  put(path: string, body: Json): Promise<Json> {
    return this.request('PUT', path, { body });
  }
  putText(path: string, bodyText: string): Promise<Json> {
    return this.requestRaw('PUT', path, bodyText);
  }
  patch(path: string, body: Json): Promise<Json> {
    return this.request('PATCH', path, { body });
  }
  delete(path: string): Promise<Json> {
    return this.request('DELETE', path);
  }

  /** `/v1/_info` is allowed unauthenticated — probe a candidate URL/token. */
  probe(): Promise<Json> {
    return this.get('/v1/_info', undefined, false);
  }
}
