import { startMic, type MicHandle } from './mic';
import { onVoiceEvent, voiceClose, voiceFinish, voiceOpen, voiceSend } from './bridge';
import { getVoiceApiKey, getVoiceModel } from './settings';
import type { UnlistenFn } from '@tauri-apps/api/event';

/// Orchestrates a dictation session: mic → PCM frames → Rust WS bridge →
/// DashScope, accumulating finals + the current partial into a running
/// transcript (parity — mobile voice_recording_session.dart). `onTranscript`
/// fires with the best-so-far text; `onDone` fires with the final text.
export interface VoiceCallbacks {
  onTranscript: (text: string) => void;
  onDone: (finalText: string) => void;
  onError: (message: string) => void;
}

/// Localized copy for the two device-side error paths (no key / mic denied).
/// Required (#320): the session layer has no t(), so the messages must come
/// from the i18n map at the construction site — never a hardcoded fallback.
export interface VoiceStrings {
  noApiKey: string;
  micDenied: string;
}

export class VoiceSession {
  private mic: MicHandle | null = null;
  private wsId: string | null = null;
  private unlisten: UnlistenFn | null = null;
  private finals = '';
  private partial = '';
  private stopped = false;

  constructor(
    private readonly cb: VoiceCallbacks,
    private readonly strings: VoiceStrings,
  ) {}

  private best(): string {
    return `${this.finals}${this.partial}`.trim();
  }

  async start(): Promise<void> {
    const apiKey = await getVoiceApiKey();
    if (apiKey === null || apiKey === '') {
      this.cb.onError(this.strings.noApiKey);
      return;
    }
    try {
      this.wsId = await voiceOpen({ api_key: apiKey, model: getVoiceModel() });
    } catch (e) {
      this.cb.onError(e instanceof Error ? e.message : String(e));
      return;
    }
    this.unlisten = await onVoiceEvent(this.wsId, (ev) => {
      switch (ev.kind) {
        case 'partial':
          this.partial = ev.text;
          this.cb.onTranscript(this.best());
          break;
        case 'final':
          this.finals = `${this.finals}${ev.text} `;
          this.partial = '';
          this.cb.onTranscript(this.best());
          break;
        case 'done':
          this.cb.onDone(this.best());
          void this.dispose();
          break;
        case 'error':
          this.cb.onError(ev.text);
          void this.dispose();
          break;
        default:
          break;
      }
    });
    try {
      this.mic = await startMic((pcm) => {
        if (this.wsId !== null && !this.stopped) {
          let binary = '';
          for (let i = 0; i < pcm.length; i += 1) binary += String.fromCharCode(pcm[i]);
          void voiceSend(this.wsId, btoa(binary));
        }
      });
    } catch {
      this.cb.onError(this.strings.micDenied);
      void this.dispose();
    }
  }

  /** Stop the mic and ask the recogniser to flush a final result. */
  async finish(): Promise<void> {
    this.stopped = true;
    this.mic?.stop();
    this.mic = null;
    if (this.wsId !== null) await voiceFinish(this.wsId);
  }

  /** Discard the recording: tear everything down WITHOUT flushing a final, so
   *  onDone never fires and the draft keeps whatever it had before (#323). */
  async cancel(): Promise<void> {
    await this.dispose();
  }

  async dispose(): Promise<void> {
    this.stopped = true;
    this.mic?.stop();
    this.mic = null;
    if (this.unlisten !== null) {
      this.unlisten();
      this.unlisten = null;
    }
    if (this.wsId !== null) {
      await voiceClose(this.wsId);
      this.wsId = null;
    }
  }
}
