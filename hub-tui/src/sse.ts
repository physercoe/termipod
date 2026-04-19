import type { HubClient } from './client.js';

export interface SseEvent {
  [key: string]: unknown;
}

/**
 * Streams decoded JSON events for one (project, channel). The hub sends
 *   data: {...}\n\n
 * frames separated by blank lines, with `: ping` comments every 15s that
 * we drop.
 *
 * Usage:
 *   const ctrl = new AbortController();
 *   for await (const evt of streamEvents(client, pid, cid, { signal: ctrl.signal })) { ... }
 *   // ctrl.abort() tears the connection down cleanly.
 */
export async function* streamEvents(
  client: HubClient,
  projectId: string,
  channelId: string,
  opts: { since?: string; signal?: AbortSignal } = {},
): AsyncGenerator<SseEvent> {
  const res = await fetch(client.streamUrl(projectId, channelId, opts.since), {
    headers: client.streamHeaders(),
    signal: opts.signal,
  });
  if (!res.ok) {
    const body = await res.text();
    throw new Error(`stream failed ${res.status}: ${body}`);
  }
  const body = res.body;
  if (!body) return;

  const decoder = new TextDecoder('utf-8');
  let buffer = '';
  const reader = body.getReader();

  while (true) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });

    // Each SSE frame ends at a blank line. Pull complete frames out and
    // leave the remainder in `buffer` for the next chunk.
    let idx: number;
    while ((idx = buffer.indexOf('\n\n')) !== -1) {
      const frame = buffer.slice(0, idx);
      buffer = buffer.slice(idx + 2);
      const payload = extractData(frame);
      if (payload == null) continue;
      try {
        const decoded = JSON.parse(payload) as SseEvent;
        if (decoded && typeof decoded === 'object') yield decoded;
      } catch {
        // A malformed frame shouldn't kill the stream.
      }
    }
  }
}

/**
 * Pull `data:` payloads out of a multi-line SSE frame. Comments (`:` prefix)
 * and unknown fields are ignored per the WHATWG spec.
 */
function extractData(frame: string): string | null {
  const lines = frame.split('\n');
  const out: string[] = [];
  for (const raw of lines) {
    const line = raw.endsWith('\r') ? raw.slice(0, -1) : raw;
    if (line.length === 0 || line.startsWith(':')) continue;
    if (line.startsWith('data:')) {
      const v = line.slice(5);
      out.push(v.startsWith(' ') ? v.slice(1) : v);
    }
  }
  if (out.length === 0) return null;
  return out.join('\n');
}
