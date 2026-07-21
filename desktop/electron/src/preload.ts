/// Electron preload — the renderer-facing half of the shell bridge (ADR-055 M1).
///
/// Runs in a sandboxed, context-isolated world and exposes exactly the two
/// primitives `src/bridge/index.ts` expects on `window.__ELECTRON_BRIDGE__`:
/// `invoke(cmd, args)` and `listen(event, cb)`. Injecting this object is what
/// flips the bridge from its dormant `browser` degrade path onto the `electron`
/// path — no frontend call site changes (plan §3).
///
/// The command allowlist is enforced in the MAIN process (the authority); this
/// side is a thin, untrusted forwarder. Events are pushed from main on one
/// channel and fanned out here to per-event subscribers, delivered in Tauri's
/// `{ event, id, payload }` envelope so every existing `listen` call site
/// (which reads `e.payload`) is byte-identical across the shell swap.
import { contextBridge, ipcRenderer, type IpcRendererEvent } from 'electron';

/** Matches Tauri's `Event<T>` so call sites keep reading `e.payload`. */
interface EventEnvelope<T> {
  event: string;
  id: number;
  payload: T;
}
type Callback<T> = (e: EventEnvelope<T>) => void;

// event name → set of live renderer callbacks.
const listeners = new Map<string, Set<Callback<unknown>>>();
let nextId = 1;

// Single main→renderer push channel; fan out to the event's subscribers.
ipcRenderer.on('bridge:event', (_e: IpcRendererEvent, msg: { event: string; payload: unknown }) => {
  const set = listeners.get(msg.event);
  if (set === undefined) return;
  const env: EventEnvelope<unknown> = { event: msg.event, id: nextId++, payload: msg.payload };
  for (const cb of set) {
    try {
      cb(env);
    } catch {
      /* one bad subscriber must not break the fan-out */
    }
  }
});

const bridge = {
  invoke<T = unknown>(cmd: string, args?: unknown): Promise<T> {
    return ipcRenderer.invoke('bridge:invoke', cmd, args ?? null) as Promise<T>;
  },

  listen<T = unknown>(event: string, cb: Callback<T>): Promise<() => void> {
    let set = listeners.get(event);
    if (set === undefined) {
      set = new Set();
      listeners.set(event, set);
    }
    const wrapped = cb as Callback<unknown>;
    set.add(wrapped);
    // Some producers gate emission on a subscriber existing (the pty_start
    // subscribe-gate that prevents losing the first prompt); tell main.
    ipcRenderer.send('bridge:subscribe', event);
    const unlisten = (): void => {
      const s = listeners.get(event);
      if (s === undefined) return;
      s.delete(wrapped);
      if (s.size === 0) {
        listeners.delete(event);
        ipcRenderer.send('bridge:unsubscribe', event);
      }
    };
    return Promise.resolve(unlisten);
  },
};

contextBridge.exposeInMainWorld('__ELECTRON_BRIDGE__', bridge);
