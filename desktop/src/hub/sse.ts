import { invoke, listen, type UnlistenFn } from '../bridge';
import { isShell } from '../platform';
import { proxyForConnection } from '../state/proxy';
import type { HubConfig } from './config';

export interface SseHandle {
  close(): void;
}

/// Decode a base64 chunk (from the Rust `hub-sse` event) to bytes. The core
/// base64-encodes each chunk so it doesn't cross IPC as a multi-KB JSON number
/// array (see `SseChunk.b64` in lib.rs).
function b64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i += 1) out[i] = bin.charCodeAt(i);
  return out;
}

export interface SseOptions {
  onEvent: (data: unknown) => void;
  onError?: (err: unknown) => void;
  /** Backfill cursor: agent streams use integer `seq`, channels `received_ts`. */
  since?: string;
  /** The event field the cursor advances off. MUST match what `since` keys on:
   *  'seq' for agent streams (default), 'received_ts' for channels. Mismatching
   *  it means the cursor never advances, so every reconnect replays from the
   *  initial `since` — a flood of duplicate messages. */
  cursorField?: string;
  /** Extra static query params on the stream URL (re-sent on every reconnect),
   *  e.g. `{session}` to scope an agent stream to a whole session. */
  query?: Record<string, string>;
}

/// Reads a hub SSE stream (`…/agents/{id}/stream`, `…/channels/{ch}/stream`)
/// with a bearer header. NOT `EventSource` (which cannot set Authorization).
/// Parses `data:` frames, ignores `: ping` keepalives, advances the `since`
/// cursor off each event's `seq`, and reconnects with backoff.
///
/// Browser build: `fetch` streaming. Tauri build: the Rust core streams the
/// bytes (`hub_sse_open` → `hub-sse` events) because the webview `fetch` is a
/// cross-origin/no-CORS call the hub rejects — the frontend still owns frame
/// parsing, the cursor, and reconnect.
export function streamSse(cfg: HubConfig, path: string, opts: SseOptions): SseHandle {
  const controller = new AbortController();
  let closed = false;
  let since = opts.since;
  let tauriCleanup: (() => void) | null = null;

  const cursorField = opts.cursorField ?? 'seq';
  // Recreated per connection (see readVia*): a stream cut mid-codepoint would
  // otherwise leave partial state that mangles the first character of the next.
  let decoder = new TextDecoder();
  let buf = '';

  function buildUrl(): string {
    const url = new URL(cfg.baseUrl.replace(/\/+$/, '') + path);
    if (opts.query !== undefined) {
      for (const [k, v] of Object.entries(opts.query)) url.searchParams.set(k, v);
    }
    if (since !== undefined) url.searchParams.set('since', since);
    return url.toString();
  }

  function emitFrame(frame: string): void {
    for (const line of frame.split('\n')) {
      if (!line.startsWith('data:')) continue;
      const payload = line.slice(5).trim();
      if (payload === '') continue;
      try {
        const parsed: unknown = JSON.parse(payload);
        if (parsed !== null && typeof parsed === 'object' && cursorField in parsed) {
          const v = (parsed as Record<string, unknown>)[cursorField];
          if (v !== undefined && v !== null) since = String(v);
        }
        opts.onEvent(parsed);
      } catch {
        // Non-JSON data line — ignore.
      }
    }
  }

  function feed(chunk: Uint8Array): void {
    buf += decoder.decode(chunk, { stream: true });
    let sep = buf.indexOf('\n\n');
    while (sep !== -1) {
      const frame = buf.slice(0, sep);
      buf = buf.slice(sep + 2);
      emitFrame(frame);
      sep = buf.indexOf('\n\n');
    }
  }

  async function readViaFetch(): Promise<void> {
    buf = '';
    decoder = new TextDecoder();
    const res = await fetch(buildUrl(), {
      headers: { authorization: `Bearer ${cfg.token}`, accept: 'text/event-stream' },
      signal: controller.signal,
    });
    if (!res.ok || res.body === null) throw new Error(`sse status ${res.status}`);
    const reader = res.body.getReader();
    while (!closed) {
      const { done, value } = await reader.read();
      if (done) break;
      if (value) feed(value);
    }
  }

  async function readViaTauri(): Promise<void> {
    buf = '';
    decoder = new TextDecoder();
    const id = await invoke<string>('hub_sse_open', {
      req: { url: buildUrl(), token: cfg.token, proxy: proxyForConnection('hub') ?? null },
    });
    if (closed) {
      void invoke('hub_sse_close', { id });
      return;
    }
    await new Promise<void>((resolve, reject) => {
      let unData: UnlistenFn | null = null;
      let unEnd: UnlistenFn | null = null;
      const cleanup = (): void => {
        unData?.();
        unEnd?.();
        tauriCleanup = null;
      };
      tauriCleanup = () => {
        cleanup();
        void invoke('hub_sse_close', { id });
        resolve();
      };
      void listen<{ id: string; b64: string }>('hub-sse', (e) => {
        if (e.payload.id === id && !closed) feed(b64ToBytes(e.payload.b64));
      }).then((u) => {
        unData = u;
        if (closed) u();
      });
      void listen<{ id: string; error?: string | null }>('hub-sse-end', (e) => {
        if (e.payload.id !== id) return;
        cleanup();
        if (e.payload.error) reject(new Error(e.payload.error));
        else resolve();
      }).then((u) => {
        unEnd = u;
      });
    });
  }

  async function run(): Promise<void> {
    const BASE = 1000;
    let backoff = BASE;
    while (!closed) {
      let errored = false;
      try {
        if (isShell()) await readViaTauri();
        else await readViaFetch();
      } catch (err) {
        if (closed) break;
        errored = true;
        opts.onError?.(err);
      }
      if (closed) break;
      // Pause before EVERY reconnect. A stream that ends cleanly (proxy
      // idle-timeout, hub deploy) previously fell straight back into the loop
      // with no delay → a hot reconnect loop against an accept-then-close
      // endpoint. A clean end reconnects at the base delay; only errors escalate
      // the backoff. Jitter avoids a thundering herd after a hub restart.
      const wait = errored ? backoff : BASE;
      await new Promise((r) => setTimeout(r, wait + Math.random() * 0.3 * wait));
      backoff = errored ? Math.min(backoff * 2, 15000) : BASE;
    }
  }

  void run();
  return {
    close(): void {
      closed = true;
      controller.abort();
      tauriCleanup?.();
    },
  };
}
