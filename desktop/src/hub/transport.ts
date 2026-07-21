import { invoke } from '../bridge';
import { shellKind } from '../platform';
import { proxyForConnection } from '../state/proxy';
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
/// hub rejects (no CORS) and also exposes the token to JS. The browser and
/// Electron builds `fetch` the hub directly (under Electron the bearer is
/// injected by the main process via `session.webRequest`; ADR-055 plan §7),
/// so only the Tauri shell takes the proxy path.
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
    if (shellKind() === 'tauri') {
      return await invoke<RawResponse>('hub_request', {
        req: { method, url, headers, body: bodyText ?? null, proxy: proxyForConnection('hub') ?? null },
      });
    }
    // Time out a hung request instead of pending forever (the Tauri path is
    // bounded in the Rust core; this covers the plain-browser build).
    const res = await fetch(url, { method, headers, body: bodyText, signal: AbortSignal.timeout(30000) });
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

  /** GET raw binary bytes (e.g. `/v1/blobs/{sha}` image/pdf blobs) as base64 plus
   * the response content-type. The JSON/text transports would corrupt non-UTF-8
   * bytes, so under Tauri this routes through the Rust core's `hub_request_bytes`
   * (reqwest → base64); the browser and Electron builds read the ArrayBuffer
   * directly. */
  async getBytes(path: string): Promise<{ mime: string; base64: string }> {
    const url = this.buildUrl(path);
    const headers = this.headers(true);
    if (shellKind() === 'tauri') {
      const res = await invoke<{ status: number; mime: string; base64: string }>('hub_request_bytes', {
        req: { method: 'GET', url, headers, body: null, proxy: proxyForConnection('hub') ?? null },
      });
      if (res.status < 200 || res.status >= 300) throw new HubApiError(res.status, res.base64);
      return { mime: res.mime, base64: res.base64 };
    }
    const res = await fetch(url, { method: 'GET', headers, signal: AbortSignal.timeout(120000) });
    if (res.status < 200 || res.status >= 300) throw new HubApiError(res.status, await res.text());
    const mime = res.headers.get('content-type') ?? '';
    const buf = new Uint8Array(await res.arrayBuffer());
    let binary = '';
    for (let i = 0; i < buf.length; i += 1) binary += String.fromCharCode(buf[i]);
    return { mime, base64: btoa(binary) };
  }

  /** `/v1/_info` is allowed unauthenticated — probe a candidate URL/token. */
  probe(): Promise<Json> {
    return this.get('/v1/_info', undefined, false);
  }
}
