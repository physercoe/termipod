import type { InputAttachments, WireAttachment } from '../hub/client';
import type { SseHandle, SseOptions } from '../hub/sse';
import type { Entity } from '../hub/types.ts';
import type { AgentEventSource } from './agentSource.ts';

/// The desktop-local agent service as an event source (vision-parity L3a).
///
/// The second implementation of `AgentEventSource`, and the one L1 was written
/// for: a session driven by Electron main with **no hub process anywhere**
/// (D-7). The renderer, the folds and the Composer do not learn which producer
/// they are reading — the events are the hub's `agent_events` shape because the
/// service logs them that way.
///
/// **`attention` is absent, not stubbed.** The attention table is a hub concept
/// (a durable cross-agent queue with its own decide route); a local session has
/// none, so the capability is missing and consumers gate on its presence. D-4:
/// degrade to absence, never to a stub that silently no-ops.
///
/// Bridge-free at runtime — the backend is structural and injected — so
/// `node --test` pins this contract without a browser. Same discipline as
/// `agentSource.ts`.

/// The `bridge` surface this module uses, narrowed so a test can supply a fake.
export interface LocalAgentBackend {
  invoke<T = unknown>(cmd: string, args?: Record<string, unknown>): Promise<T>;
  listen<T = unknown>(event: string, cb: (ev: { payload: T }) => void): Promise<() => void>;
}

/// The renderer event main pushes live rows on.
export const LOCAL_AGENT_EVENT = 'localagent-event';

/// One page of a session's log, as `localagent_history` / `localagent_since`
/// return it.
interface LogPage {
  events: Entity[];
  cursor: number;
  resyncRequired: boolean;
}

interface LiveEnvelope {
  session_id: string;
  event: Entity;
}

/// Main only pushes to renderers that asked, and asking is per-window rather
/// than per-session — so the last subscription to close is what unwatches.
/// Without the count, opening two agent views and closing one would silence
/// the other.
function makeWatchRefcount(backend: LocalAgentBackend): { acquire: () => void; release: () => void } {
  let count = 0;
  return {
    acquire: () => {
      count += 1;
      if (count === 1) void backend.invoke('localagent_watch');
    },
    release: () => {
      count = Math.max(0, count - 1);
      if (count === 0) void backend.invoke('localagent_unwatch');
    },
  };
}

function toWire(att: WireAttachment): { mime: string; data: string; filename?: string } {
  // The hub's wire spells it `mime_type`; the local service's input builder
  // takes `mime`. One rename, here, rather than a second attachment vocabulary.
  return { mime: att.mime_type, data: att.data, ...(att.filename !== undefined ? { filename: att.filename } : {}) };
}

export function localAgentSource(backend: LocalAgentBackend): AgentEventSource {
  const watch = makeWatchRefcount(backend);

  return {
    kind: 'local',

    history: async (sessionId, opts) => {
      const page = await backend.invoke<LogPage>('localagent_history', {
        session_id: sessionId,
        ...(opts?.tail !== undefined ? { tail: opts.tail } : {}),
      });
      return page.events;
    },

    subscribe: (sessionId, opts) => {
      let closed = false;
      let unlisten: (() => void) | null = null;

      // Everything that arrives before the gap fetch resolves waits here.
      // Attaching the listener FIRST and buffering is what closes the
      // emit-before-subscribe window: between `history()` returning and this
      // running, the child can emit, and a listener attached after the gap
      // fetch would miss anything produced during it.
      let buffered: Entity[] | null = [];
      // The highest seq already delivered, so the buffer drain cannot re-emit
      // what the gap fetch just sent.
      let delivered = Number(opts.since ?? 0);
      if (!Number.isFinite(delivered)) delivered = 0;

      const deliver = (ev: Entity): void => {
        if (closed) return;
        const seq = typeof ev.seq === 'number' ? ev.seq : undefined;
        if (seq !== undefined) {
          if (seq <= delivered) return;
          delivered = seq;
        }
        opts.onEvent(ev);
      };

      void (async () => {
        try {
          // Synchronous, and deliberately the first statement: the IIFE body
          // runs to its first `await` before subscribe() returns, so the count
          // is already held by the time any close() can release it.
          watch.acquire();
          const stop = await backend.listen<LiveEnvelope>(LOCAL_AGENT_EVENT, ({ payload }) => {
            if (payload.session_id !== sessionId) return;
            if (buffered !== null) buffered.push(payload.event);
            else deliver(payload.event);
          });
          if (closed) {
            stop();
            return;
          }
          unlisten = stop;

          const gap = await backend.invoke<LogPage>('localagent_since', {
            session_id: sessionId,
            cursor: delivered,
          });
          if (closed) return;
          for (const ev of gap.events) deliver(ev);

          const pending = buffered ?? [];
          buffered = null;
          for (const ev of pending) deliver(ev);
        } catch (err) {
          if (!closed) opts.onError?.(err);
        }
      })();

      const handle: SseHandle = {
        close: () => {
          if (closed) return;
          closed = true;
          buffered = null;
          unlisten?.();
          unlisten = null;
          // Unconditional, and safe: `closed` above makes close() idempotent,
          // and acquire() ran synchronously in subscribe(), so there is no
          // path here that never acquired. A guard for one would be a branch
          // nothing can reach.
          watch.release();
        },
      };
      return handle;
    },

    send: async (sessionId, body, att?: InputAttachments) => {
      // audios/videos are dropped rather than forwarded: claude's input does
      // not accept those modalities, and the composer's capability gate
      // already hides the affordance. This is the belt-and-braces drop.
      const images = (att?.images ?? []).map(toWire);
      const pdfs = (att?.pdfs ?? []).map(toWire);
      await backend.invoke('localagent_input', {
        session_id: sessionId,
        kind: 'text',
        payload: {
          body,
          ...(images.length > 0 ? { images } : {}),
          ...(pdfs.length > 0 ? { pdfs } : {}),
        },
      });
    },

    approve: async (sessionId, requestId, decision, optionId) => {
      await backend.invoke('localagent_input', {
        session_id: sessionId,
        kind: 'approval',
        // When the agent offered its own option ids, that id is what it
        // correlates on — the same widening `hubAgentSource` admits.
        payload: { request_id: requestId, decision: optionId ?? decision },
      });
    },

    answer: async (sessionId, requestId, body) => {
      await backend.invoke('localagent_input', {
        session_id: sessionId,
        kind: 'answer',
        payload: { request_id: requestId, body },
      });
    },
  };
}

/// `SseOptions` is re-exported for the tests' benefit — the local source
/// satisfies the same option shape the SSE one does, and pinning that here
/// makes a divergence a compile error rather than a runtime surprise.
export type LocalSubscribeOptions = SseOptions;
