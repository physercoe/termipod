/// Proxy-aware fetch for the outbound sync/download transports (ADR-055 M4 — the
/// paydown of the webdav/s3/zotero/drawio "proxy accepted but not applied" gap).
///
/// Node's global `fetch` has no per-request proxy, and its internal undici does
/// not expose `ProxyAgent`. So when a proxy is configured we route through the
/// `undici` PACKAGE — using ITS `fetch` with a `ProxyAgent` dispatcher, both from
/// the same install, so the dispatcher is compatible (mixing the built-in fetch
/// with a package agent is the classic dual-undici footgun). With NO proxy we
/// call the platform global `fetch` unchanged — the direct path keeps its exact
/// prior behaviour, so this is purely additive.
///
/// The proxy string is the `scheme://host:port` shape `system_proxy` /
/// `proxyForConnection` produce. Only http/https proxies are honoured (undici's
/// ProxyAgent tunnels via CONNECT); a socks/other scheme falls through to a
/// direct connection rather than throwing — degraded, not broken.

// Lazily loaded so the direct path never pulls undici in, and a missing module
// degrades to direct rather than crashing the handler.
let undiciP: Promise<typeof import('undici')> | null = null;
function undiciMod(): Promise<typeof import('undici')> {
  if (undiciP === null) undiciP = import('undici');
  return undiciP;
}

// One ProxyAgent per distinct proxy URL, reused across requests (connection
// pooling). Keyed by the proxy string.
const agents = new Map<string, import('undici').ProxyAgent>();
async function agentFor(proxy: string): Promise<import('undici').ProxyAgent> {
  let a = agents.get(proxy);
  if (a === undefined) {
    const { ProxyAgent } = await undiciMod();
    a = new ProxyAgent(proxy);
    agents.set(proxy, a);
  }
  return a;
}

function proxyable(proxy: string | null | undefined): proxy is string {
  return typeof proxy === 'string' && /^https?:\/\//i.test(proxy);
}

/// fetch that routes through `proxy` when one is set (and is http/https),
/// otherwise the direct global fetch. Same call shape as `fetch`, returns the
/// same `Response`.
export async function proxyFetch(
  url: string,
  init: RequestInit,
  proxy?: string | null,
): Promise<Response> {
  if (!proxyable(proxy)) return fetch(url, init);
  let undiciFetch: typeof import('undici').fetch;
  let dispatcher: import('undici').ProxyAgent;
  try {
    ({ fetch: undiciFetch } = await undiciMod());
    dispatcher = await agentFor(proxy);
  } catch {
    // undici unavailable (module load failed) or the proxy string is unusable
    // (ProxyAgent rejected it) — fall back to a direct fetch so sync still works
    // off-proxy rather than hard-failing. The request itself runs OUTSIDE this
    // try: a live network/proxy error must propagate to the caller, not silently
    // retry direct — that would leak deliberately-proxied traffic and mask a
    // down proxy as a working one.
    return fetch(url, init);
  }
  // undici's fetch/Response are structurally the WHATWG shapes the callers use
  // (.status/.ok/.text/.arrayBuffer/.headers); the cast bridges the nominal
  // type gap between undici's RequestInit/Response and the global lib types.
  return (await undiciFetch(url, { ...init, dispatcher } as never)) as unknown as Response;
}
