/// A non-2xx hub response. `status` mirrors HubApiError.status in the Dart client;
/// teamGate scope mismatches surface here as 403.
///
/// The raw response body is NOT put verbatim into the user-facing message: a
/// reverse proxy or the hub's crash page can answer a 500/502 with kilobytes of
/// HTML, and dumping that into a toast/alert is unreadable and can leak internal
/// detail. `summarizeBody` reduces it to one short line (JSON error field →
/// stripped-HTML text → truncated plain text). The untouched body is kept on
/// `.body` for logging/diagnostics that genuinely need it.
export class HubApiError extends Error {
  readonly status: number;
  readonly body: string;
  constructor(status: number, body: string) {
    super(`HubApiError(${status}): ${summarizeBody(body)}`);
    this.name = 'HubApiError';
    this.status = status;
    this.body = body;
  }
}

const MAX_SUMMARY = 240;

/// Collapse an arbitrary response body into a single short human line.
export function summarizeBody(body: string): string {
  const raw = (body ?? '').trim();
  if (raw === '') return '(empty response)';

  // Structured hub errors are JSON — surface the message field, not the braces.
  if (raw.startsWith('{') || raw.startsWith('[')) {
    try {
      const parsed = JSON.parse(raw) as unknown;
      const msg = pickMessage(parsed);
      if (msg !== null) return truncate(msg);
    } catch {
      /* not valid JSON — fall through to text handling */
    }
  }

  // Proxy/crash pages are HTML — strip tags and keep the visible text.
  let text = raw;
  if (/<\/?[a-z][\s\S]*>/i.test(text)) {
    text = text
      .replace(/<(script|style)[\s\S]*?<\/\1>/gi, ' ')
      .replace(/<[^>]+>/g, ' ');
  }
  text = text.replace(/\s+/g, ' ').trim();
  return truncate(text === '' ? raw.replace(/\s+/g, ' ').trim() : text);
}

function pickMessage(parsed: unknown): string | null {
  if (typeof parsed !== 'object' || parsed === null) return null;
  const rec = parsed as Record<string, unknown>;
  for (const key of ['error', 'message', 'detail', 'msg']) {
    const v = rec[key];
    if (typeof v === 'string' && v.trim() !== '') return v.trim();
    if (v !== null && typeof v === 'object') {
      const nested = pickMessage(v);
      if (nested !== null) return nested;
    }
  }
  return null;
}

function truncate(s: string): string {
  return s.length > MAX_SUMMARY ? `${s.slice(0, MAX_SUMMARY)}…` : s;
}
