import { invoke } from '@tauri-apps/api/core';
import { listen, type UnlistenFn } from '@tauri-apps/api/event';
import { isTauri } from '../platform';
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

  const decoder = new TextDecoder();
  let buf = '';

  function buildUrl(): string {
    const url = new URL(cfg.baseUrl.replace(/\/+$/, '') + path);
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
        if (parsed !== null && typeof parsed === 'object' && 'seq' in parsed) {
          since = String((parsed as Record<string, unknown>).seq);
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
    const id = await invoke<string>('hub_sse_open', { req: { url: buildUrl(), token: cfg.token } });
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
      void listen<{ id: string; bytes: number[] }>('hub-sse', (e) => {
        if (e.payload.id === id && !closed) feed(new Uint8Array(e.payload.bytes));
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
    let backoff = 1000;
    while (!closed) {
      try {
        if (isTauri()) await readViaTauri();
        else await readViaFetch();
        backoff = 1000;
      } catch (err) {
        if (closed) break;
        opts.onError?.(err);
        await new Promise((r) => setTimeout(r, backoff));
        backoff = Math.min(backoff * 2, 15000);
      }
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
