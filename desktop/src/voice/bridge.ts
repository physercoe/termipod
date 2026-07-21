import { invoke, listen, type UnlistenFn } from '../bridge';

/// Typed bridge to the Rust voice WebSocket proxy. The webview's own WebSocket
/// can't set the `Authorization` header DashScope's realtime ASR requires, so the
/// connection lives in the Rust core (tokio-tungstenite); this streams PCM up and
/// receives transcript events back over a Tauri event channel. Desktop-only.
export interface VoiceOpenReq {
  api_key: string;
  model: string;
}

export interface VoiceEvent {
  id: string;
  kind: 'open' | 'partial' | 'final' | 'done' | 'error';
  text: string;
}

/** Open a voice ASR session; resolves to a ws id used by the calls below. */
export function voiceOpen(req: VoiceOpenReq): Promise<string> {
  return invoke<string>('voice_open', { req });
}
/** Stream one PCM16/16k frame (base64) to the recogniser. */
export function voiceSend(id: string, pcmB64: string): Promise<void> {
  return invoke('voice_send', { id, pcmB64 });
}
/** Signal end-of-audio; the recogniser flushes a final result then closes. */
export function voiceFinish(id: string): Promise<void> {
  return invoke('voice_finish', { id });
}
/** Tear the session down immediately. */
export function voiceClose(id: string): Promise<void> {
  return invoke('voice_close', { id });
}
/** Subscribe to transcript/lifecycle events for one voice session. */
export function onVoiceEvent(id: string, cb: (e: VoiceEvent) => void): Promise<UnlistenFn> {
  return listen<VoiceEvent>('voice-event', (e) => {
    if (e.payload.id === id) cb(e.payload);
  });
}
