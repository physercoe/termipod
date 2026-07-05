import type { HubConfig } from './config';

export interface SseHandle {
  close(): void;
}

export interface SseOptions {
  onEvent: (data: unknown) => void;
  onError?: (err: unknown) => void;
  /** Backfill cursor: agent streams use integer `seq`, channels `received_ts`. */
  since?: string;
}

/// Reads a hub SSE stream (`…/agents/{id}/stream`, `…/channels/{ch}/stream`)
/// with a bearer header via `fetch` — NOT `EventSource`, which cannot set
/// Authorization. Parses `data:` frames, ignores `: ping` keepalives, advances
/// the `since` cursor off each event's `seq`, and reconnects with backoff.
///
/// Works identically in the browser build and the Tauri webview; the Tauri Rust
/// core can later intercept this if token-in-JS must be avoided.
export function streamSse(cfg: HubConfig, path: string, opts: SseOptions): SseHandle {
  const controller = new AbortController();
  let closed = false;
  let since = opts.since;

  async function run(): Promise<void> {
    let backoff = 1000;
    while (!closed) {
      try {
        const url = new URL(cfg.baseUrl.replace(/\/+$/, '') + path);
        if (since !== undefined) url.searchParams.set('since', since);
        const res = await fetch(url.toString(), {
          headers: { authorization: `Bearer ${cfg.token}`, accept: 'text/event-stream' },
          signal: controller.signal,
        });
        if (!res.ok || res.body === null) throw new Error(`sse status ${res.status}`);
        backoff = 1000;
        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        let buf = '';
        while (!closed) {
          const { done, value } = await reader.read();
          if (done) break;
          buf += decoder.decode(value, { stream: true });
          let sep = buf.indexOf('\n\n');
          while (sep !== -1) {
            const frame = buf.slice(0, sep);
            buf = buf.slice(sep + 2);
            emitFrame(frame);
            sep = buf.indexOf('\n\n');
          }
        }
      } catch (err) {
        if (closed) break;
        opts.onError?.(err);
        await new Promise((r) => setTimeout(r, backoff));
        backoff = Math.min(backoff * 2, 15000);
      }
    }
  }

  function emitFrame(frame: string): void {
    for (const line of frame.split('\n')) {
      if (!line.startsWith('data:')) continue;
      const payload = line.slice(5).trim();
      if (payload === '') continue;
      try {
        const parsed: unknown = JSON.parse(payload);
        if (parsed !== null && typeof parsed === 'object' && 'seq' in parsed) {
          since = String((parsed as Record<string, unknown>).seq);
        }
        opts.onEvent(parsed);
      } catch {
        // Non-JSON data line — ignore.
      }
    }
  }

  void run();
  return {
    close(): void {
      closed = true;
      controller.abort();
    },
  };
}
