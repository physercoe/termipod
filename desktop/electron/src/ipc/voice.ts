/// Voice dictation bridge (ADR-055 M2.3) — the Electron port of
/// `src-tauri/src/voice.rs`. Same command names (`voice_open` / `voice_send` /
/// `voice_finish` / `voice_close`) and the same `voice-event` contract, so
/// `src/voice/bridge.ts` drives it unchanged through the bridge.
///
/// DashScope's realtime ASR is a WebSocket authenticated with an
/// `Authorization: bearer <key>` header — which the renderer's own `WebSocket`
/// cannot set (the whole reason this lives in the native layer). The `ws`
/// package can, natively, so the Electron port is simpler than the Rust one: no
/// channel-actor, just the socket held per session.
///
///   frontend --voice_open--> main opens WS (awaits open), sends run-task
///   frontend --voice_send(pcm)--> main forwards a binary audio frame
///   frontend --voice_finish--> main sends finish-task; server flushes final
///   main --emit "voice-event" {id, kind, text}--> frontend
///
/// This is the personal-key path (the director's own DashScope key, from the OS
/// keychain). `ws` is pure JS — no native build, so no ABI-rebuild concern.
import { randomBytes } from 'node:crypto';
import type { WebContents } from 'electron';
import { emit } from '../events';
import type { Handler } from './dispatch';

const ENDPOINT = 'wss://dashscope.aliyuncs.com/api-ws/v1/inference';

// `ws` is bundled (pure JS), but load it lazily so a resolve failure surfaces as
// a rejected `voice_open` rather than a load-time crash — matching the other
// external-module loaders.
type WsModule = typeof import('ws');
let wsModP: Promise<WsModule> | null = null;
function loadWs(): Promise<WsModule> {
  if (wsModP === null) wsModP = import('ws');
  return wsModP;
}

interface Session {
  ws: import('ws').WebSocket;
  sender: WebContents;
  task: string; // the DashScope task id, needed to address finish-task
}

let nextId = 1;
const sessions = new Map<string, Session>();

/// A random 32-hex task id (DashScope requires a unique id per task).
function taskId(): string {
  return randomBytes(16).toString('hex');
}

function runTaskJson(task: string, model: string): string {
  return JSON.stringify({
    header: { action: 'run-task', task_id: task, streaming: 'duplex' },
    payload: {
      task_group: 'audio',
      task: 'asr',
      function: 'recognition',
      model,
      parameters: { format: 'pcm', sample_rate: 16000 },
      input: {},
    },
  });
}

function finishTaskJson(task: string): string {
  return JSON.stringify({
    header: { action: 'finish-task', task_id: task, streaming: 'duplex' },
    payload: { input: {} },
  });
}

/// Parse one server frame into {kind, text}. Returns null for frames that carry
/// no user-visible change (task-started, heartbeats). Mirrors voice.rs.
function parseEvent(text: string): { kind: string; text: string } | null {
  let v: unknown;
  try {
    v = JSON.parse(text);
  } catch {
    return null;
  }
  const root = v as Record<string, unknown>;
  const header = root.header as Record<string, unknown> | undefined;
  const event = header?.event;
  if (typeof event !== 'string') return null;
  if (event === 'result-generated') {
    const payload = root.payload as Record<string, unknown> | undefined;
    const output = payload?.output as Record<string, unknown> | undefined;
    const sentence = output?.sentence as Record<string, unknown> | undefined;
    if (sentence === undefined || sentence === null) return null;
    const t = typeof sentence.text === 'string' ? sentence.text : '';
    const ended = sentence.sentence_end === true;
    return { kind: ended ? 'final' : 'partial', text: t };
  }
  if (event === 'task-finished') return { kind: 'done', text: '' };
  if (event === 'task-failed') {
    const m = typeof header?.error_message === 'string' ? header.error_message : 'task failed';
    return { kind: 'error', text: m };
  }
  return null;
}

/// Close the socket and drop the session. Best-effort and idempotent.
function teardown(id: string): void {
  const s = sessions.get(id);
  if (s === undefined) return;
  sessions.delete(id);
  try {
    s.ws.close();
  } catch {
    /* already closing/closed */
  }
}

export const voiceHandlers: Record<string, Handler> = {
  voice_open: async (args, ctx): Promise<string> => {
    const req = (args.req !== null && typeof args.req === 'object' ? args.req : {}) as {
      api_key?: string;
      model?: string;
    };
    const apiKey = String(req.api_key ?? '');
    const model = String(req.model ?? '');

    const { WebSocket } = await loadWs();
    const ws = new WebSocket(ENDPOINT, { headers: { Authorization: `bearer ${apiKey}` } });
    const id = `v${nextId}`;
    nextId += 1;
    const task = taskId();
    const sender = ctx.sender;

    // Gate the return on the socket opening and run-task being sent — so a later
    // `voice_send` always meets an open socket (voice.rs awaits connect+run-task
    // before returning too). A pre-open error rejects `voice_open`.
    await new Promise<void>((resolve, reject) => {
      const onErr = (err: Error): void => reject(new Error(`connect: ${err.message}`));
      ws.once('error', onErr);
      ws.once('open', () => {
        ws.off('error', onErr);
        resolve();
      });
    });
    ws.send(runTaskJson(task, model));
    sessions.set(id, { ws, sender, task });
    emit(sender, 'voice-event', { id, kind: 'open', text: '' });

    // Attached after run-task: DashScope's first frame (task-started) only
    // arrives on a later I/O tick, so nothing is missed in the gap.
    ws.on('message', (data: import('ws').RawData, isBinary: boolean) => {
      if (isBinary) return;
      const parsed = parseEvent(data.toString());
      if (parsed === null) return;
      emit(sender, 'voice-event', { id, kind: parsed.kind, text: parsed.text });
      if (parsed.kind === 'done' || parsed.kind === 'error') teardown(id);
    });
    ws.on('close', () => teardown(id));
    ws.on('error', () => teardown(id));

    return id;
  },

  voice_send: async (args): Promise<void> => {
    const id = String(args.id ?? '');
    const pcmB64 = String(args.pcmB64 ?? '');
    const s = sessions.get(id);
    if (s === undefined) throw new Error('no such voice session');
    s.ws.send(Buffer.from(pcmB64, 'base64'));
  },

  voice_finish: async (args): Promise<void> => {
    const id = String(args.id ?? '');
    const s = sessions.get(id);
    if (s === undefined) throw new Error('no such voice session');
    s.ws.send(finishTaskJson(s.task));
  },

  // Best-effort teardown; a missing session is not an error (matches voice.rs).
  voice_close: async (args): Promise<void> => {
    teardown(String(args.id ?? ''));
  },
};
