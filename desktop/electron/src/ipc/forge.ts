/// Proxy-aware, byte-capped GET for the Inspect forge sources (round-3 T3 —
/// GitHub / Hugging Face). A forge must honour the app proxy like every other
/// outbound transport (the ADR-055 M4 paydown lesson), so this routes through the
/// same `proxyFetch` as the sync/download transports. The renderer never fetches
/// a forge directly in the shell build — it calls here, where the proxy and the
/// read cap are enforced by the authority.
///
/// GET-only, https-only, and hard-capped: an over-cap body is never downloaded
/// (the "no uncapped reads" anchor applied to the network). A `User-Agent` is
/// injected when the caller omits one (the GitHub API rejects requests without
/// one).
import type { Handler } from './dispatch';
import { isAllowedForgeUrl } from './forgepolicy';
import { proxyFetch } from './net';
import { resolveSystemProxy } from './platform';

const DEFAULT_MAX = 2 * 1024 * 1024; // 2 MB
const HARD_MAX = 16 * 1024 * 1024; // ceiling regardless of the caller's request
const TIMEOUT_MS = 30_000;

// Only the headers the renderer needs — rate-limit signalling + content typing.
// (Never echo Set-Cookie / auth back to the renderer.)
const ECHO_HEADERS = ['content-length', 'content-type', 'x-ratelimit-remaining', 'x-ratelimit-reset', 'link'];

interface ForgeResponse {
  status: number;
  headers: Record<string, string>;
  body: string;
  tooLarge: boolean;
  size: number;
}

export const forgeHandlers: Record<string, Handler> = {
  forge_fetch: async (args): Promise<ForgeResponse> => {
    const url = String(args.url ?? '');
    if (!isAllowedForgeUrl(url, process.env.TERMIPOD_E2E)) throw new Error('forge_fetch: only https URLs are allowed');
    const reqHeaders = args.headers !== null && typeof args.headers === 'object' ? (args.headers as Record<string, string>) : {};
    const maxBytes = Math.min(typeof args.maxBytes === 'number' && args.maxBytes > 0 ? args.maxBytes : DEFAULT_MAX, HARD_MAX);
    const headers: Record<string, string> = { 'User-Agent': 'TermiPod-Inspect', Accept: 'application/vnd.github+json', ...reqHeaders };

    const proxy = await resolveSystemProxy();
    const res = await proxyFetch(url, { method: 'GET', headers, signal: AbortSignal.timeout(TIMEOUT_MS) }, proxy);

    const outHeaders: Record<string, string> = {};
    for (const k of ECHO_HEADERS) {
      const v = res.headers.get(k);
      if (v !== null) outHeaders[k] = v;
    }

    const declared = Number(res.headers.get('content-length') ?? '0');
    if (declared > maxBytes) {
      // Don't pull an over-cap body across the wire.
      try {
        await res.body?.cancel();
      } catch {
        /* already consumed */
      }
      return { status: res.status, headers: outHeaders, body: '', tooLarge: true, size: declared };
    }

    // Cap even when content-length is absent (chunked responses).
    const buf = new Uint8Array(await res.arrayBuffer());
    if (buf.byteLength > maxBytes) return { status: res.status, headers: outHeaders, body: '', tooLarge: true, size: buf.byteLength };
    return { status: res.status, headers: outHeaders, body: new TextDecoder('utf-8', { fatal: false }).decode(buf), tooLarge: false, size: buf.byteLength };
  },
};
