/// Microphone capture → PCM16 mono 16 kHz frames (parity — mobile
/// recording_controller.dart, which gets PCM16/16k for free from the `record`
/// plugin). The browser/webview delivers Float32 at the device sample rate
/// (usually 48 kHz), so we downsample + quantise here. A ScriptProcessorNode is
/// used deliberately: it needs no separate AudioWorklet module file (awkward to
/// bundle under Vite/Tauri) and its latency is irrelevant for streaming ASR.
export interface MicHandle {
  stop: () => void;
}

const TARGET_RATE = 16000;

/// Start capture; `onFrame` receives little-endian PCM16 chunks. Rejects if mic
/// permission is denied. Call `stop()` to release the mic and audio graph.
export async function startMic(onFrame: (pcm: Uint8Array) => void): Promise<MicHandle> {
  const stream = await navigator.mediaDevices.getUserMedia({ audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true } });
  const AudioCtor = window.AudioContext ?? (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext;
  const ctx = new AudioCtor();
  const source = ctx.createMediaStreamSource(stream);
  const processor = ctx.createScriptProcessor(4096, 1, 1);
  const inRate = ctx.sampleRate;

  processor.onaudioprocess = (e: AudioProcessingEvent): void => {
    const input = e.inputBuffer.getChannelData(0);
    const ratio = inRate / TARGET_RATE;
    const outLen = Math.floor(input.length / ratio);
    const out = new DataView(new ArrayBuffer(outLen * 2));
    for (let i = 0; i < outLen; i += 1) {
      // Nearest-sample decimation; sufficient for 16 kHz speech ASR.
      const sample = input[Math.floor(i * ratio)] ?? 0;
      const clamped = Math.max(-1, Math.min(1, sample));
      out.setInt16(i * 2, clamped < 0 ? clamped * 0x8000 : clamped * 0x7fff, true);
    }
    onFrame(new Uint8Array(out.buffer));
  };

  source.connect(processor);
  processor.connect(ctx.destination);

  return {
    stop: () => {
      processor.disconnect();
      source.disconnect();
      for (const track of stream.getTracks()) track.stop();
      void ctx.close();
    },
  };
}
