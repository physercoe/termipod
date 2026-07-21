/// Hub CORS bridging (ADR-055 M1.2, plan §3 / §7 rows 1–2).
///
/// The renderer is served from `app://termipod`, so its direct `fetch` to the
/// (user-configured, arbitrary-host) hub is cross-origin — and the hub sends no
/// CORS headers, so the browser would block the response and preflight any
/// JSON/authorized request. Rather than re-introduce the Rust `hub_request*`
/// proxy the migration deletes, we fix CORS in-process here: reflect the app
/// origin onto responses to renderer-initiated requests, and satisfy the
/// preflight. The renderer's own transport still sets the bearer (moving the
/// token out of renderer JS via `onBeforeSendHeaders` is a later optimization,
/// plan §7 row 2 — not a correctness blocker, since the header is sent today).
///
/// `onHeadersReceived` doesn't carry request headers, so we can't read the
/// Origin there. We instead mark the request id in `onBeforeSendHeaders` (which
/// does) when its Origin is ours, and act on that id when the response returns —
/// leaving external `open_browser_window` traffic (a different origin, sharing
/// the session) untouched.
import type { Session, OnBeforeSendHeadersListenerDetails, OnHeadersReceivedListenerDetails } from 'electron';
import { APP_ORIGIN } from './appscheme';

const HTTP_FILTER = { urls: ['http://*/*', 'https://*/*'] };

/** First value of a request header, case-insensitively. */
function requestHeader(details: OnBeforeSendHeadersListenerDetails, name: string): string | undefined {
  const wanted = name.toLowerCase();
  for (const [k, v] of Object.entries(details.requestHeaders)) {
    if (k.toLowerCase() === wanted) return Array.isArray(v) ? v[0] : v;
  }
  return undefined;
}

/** Set a response header, replacing any existing case-insensitive variant. */
function setResponseHeader(headers: Record<string, string[]>, name: string, value: string): void {
  const wanted = name.toLowerCase();
  for (const k of Object.keys(headers)) {
    if (k.toLowerCase() === wanted) delete headers[k];
  }
  headers[name] = [value];
}

export function installHubCors(sess: Session): void {
  // Request ids whose Origin is our renderer — correlates the two callbacks.
  const fromAppOrigin = new Set<number>();

  sess.webRequest.onBeforeSendHeaders(HTTP_FILTER, (details, cb) => {
    if (requestHeader(details, 'Origin') === APP_ORIGIN) fromAppOrigin.add(details.id);
    cb({ requestHeaders: details.requestHeaders });
  });

  sess.webRequest.onHeadersReceived(HTTP_FILTER, (details: OnHeadersReceivedListenerDetails, cb) => {
    if (!fromAppOrigin.has(details.id)) {
      cb({});
      return;
    }
    fromAppOrigin.delete(details.id);

    const headers: Record<string, string[]> = { ...(details.responseHeaders ?? {}) };
    // Credentialed reflection (specific origin, not `*`) so cookies/auth are
    // allowed if ever used; harmless otherwise.
    setResponseHeader(headers, 'Access-Control-Allow-Origin', APP_ORIGIN);
    setResponseHeader(headers, 'Access-Control-Allow-Credentials', 'true');
    setResponseHeader(headers, 'Access-Control-Allow-Methods', 'GET, POST, PUT, PATCH, DELETE, OPTIONS');
    setResponseHeader(headers, 'Access-Control-Allow-Headers', 'Authorization, Content-Type');
    setResponseHeader(headers, 'Access-Control-Expose-Headers', 'Content-Type');

    // A preflight (OPTIONS) must be a 2xx to pass; the hub has no OPTIONS route
    // and returns 404/405, so promote it. Non-preflight statuses pass through.
    let statusLine = details.statusLine;
    if (details.method === 'OPTIONS' && (details.statusCode < 200 || details.statusCode >= 300)) {
      statusLine = 'HTTP/1.1 200 OK';
    }
    cb({ responseHeaders: headers, statusLine });
  });

  // Don't leak ids for requests that error before a response.
  const forget = (details: { id: number }): void => {
    fromAppOrigin.delete(details.id);
  };
  sess.webRequest.onErrorOccurred(HTTP_FILTER, forget);
}
