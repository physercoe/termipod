# Voice input — Path C, Alibaba DashScope (`fun-asr-realtime`)

> **Type:** plan
> **Status:** Proposed (2026-05-12; principal Q&A locked) — supersedes
> [`voice-input-overlay-v1.md`](voice-input-overlay-v1.md) (deferred
> SenseVoice / Path D). Path B (system-native STT) explicitly skipped
> on quality-variance grounds — Android OEM speech recognizers are too
> inconsistent for the principal's zh+en code-switching utterances.
> **Audience:** contributors · principal
> **Last verified vs code:** v1.0.514

**TL;DR.** Add push-to-talk voice input to the steward overlay chat
using Alibaba's `fun-asr-realtime` over **WebSocket streaming**
(`wss://dashscope.aliyuncs.com/api-ws/v1/inference`). Mobile streams
PCM16 16 kHz mono chunks live; DashScope streams partial transcripts
back; final text drops into the chat input for review-then-send. The
hub never touches audio. ~500 LOC across 5 wedges, no APK bloat, no
native library bring-up. **Strategic value:** Fun-ASR / Paraformer
weights are openly published (Alibaba's FunASR project), so this is a
clean Path C → Path D migration when offline becomes load-bearing —
same model architecture, swap the WebSocket adapter for a local
inference adapter behind the same `CloudStt` interface.

## Goal

After this plan:

1. The steward overlay chat input has a microphone button (replaces
   the send button when the input field is empty, mirroring the
   deferred plan's UX).
2. Tap-and-hold to record; release to commit. Drag-out cancels.
3. **Partial transcripts stream into the input field as the user
   speaks** — a real-time UX win over batch mode. The final result
   replaces the partials on release.
4. Recording soft cap **60 s** (DashScope WebSocket has no hard cap;
   the UX cap matches push-to-talk's reasonable ceiling).
5. Bilingual zh + en + 8 Chinese dialects handled in one model
   (Fun-ASR auto-detects). Per-utterance language chip (`auto / zh /
   en`) on the recording pill lets the user override when auto-
   detect mis-flags.
6. Mic button auto-disables when offline (`connectivity_plus`),
   greyed with a tooltip.
7. API key lives in `flutter_secure_storage`; user pastes it in
   Settings → Voice → DashScope API Key.
8. **Default region: Beijing** (`dashscope.aliyuncs.com`) — principal
   tested with a Beijing-region key and Beijing's per-second fee is
   cheaper than Singapore / US.
9. Transcripts land in the text field for **review-then-send** — not
   auto-sent (honors ADR-023 D4).

## Non-goals (locked by Q&A 2026-05-12)

- **Audio logging.** No on-disk audio history. PCM frames flow
  microphone → WebSocket → discarded. The transcript text is the
  only persisted artifact (via the normal `agent_events` flow once
  the user hits send). Principal Q5.
- **Hub-distributed API key.** One key per device, stored locally.
  No `GET /v1/teams/{team}/voice/credentials` endpoint. Principal
  Q6.
- **Multiple keys per user.** Single key per device. Adding a work-
  vs-personal picker is a deferred follow-up. Principal Q6.
- **iOS in v1.** Plan covers Android first; iOS follow-up needs
  `NSMicrophoneUsageDescription` + Pod-side audio session config —
  ~30 LOC.
- **Vendor swap UX.** v1 ships only the Alibaba adapter. The
  `CloudStt` interface is in place so a Soniox / Whisper adapter
  can be added later, but the picker UI ships hidden in v1.
- **Path D migration.** Self-hosting Fun-ASR open weights is the
  Path D follow-on. The strategic point of choosing Alibaba **now**
  is that Path D will be a backend swap behind the same adapter —
  no UX or settings re-shuffle.

## Why this vendor, not the alternatives

Cross-vendor research lives in
[`discussions/voice-input-cloud-vs-offline.md` §5.4](../discussions/voice-input-cloud-vs-offline.md).
This plan picks **Alibaba `fun-asr-realtime`** specifically because:

- **Open weights for Path D.** Fun-ASR / Paraformer / SenseVoice are
  all published by Alibaba's FunASR project on GitHub. Soniox and
  ElevenLabs are closed; Whisper open but weaker on zh+en code-
  switching; iFlytek closed and PRC-jurisdiction-only.
- **Code-switching quality.** Fun-ASR supports Mandarin + 8 Chinese
  dialects (Cantonese, Wu, Min Nan, Hakka, Gan, Xiang, Jin) + English
  + Japanese in one model with semantic punctuation + VAD-based
  sentence segmentation. Better fit than Paraformer-realtime-v2 for a
  user mixing Mandarin + English mid-utterance.
- **Real-time WebSocket = partial transcripts.** Sub-second perceived
  latency. The user sees their words appear as they speak, not after
  release. This eliminates the "did the mic catch my last word?"
  uncertainty that drives users back to typing.
- **Beijing region cheapest.** Principal already has a Beijing-region
  key; per-second cost lower than Singapore / US.

## Architecture

```
overlay chat input
       │
       │ [hold mic]
       ▼
RecordingController
       │
       │ record.startStream(pcm16bits, 16kHz mono)
       │
       │ Stream<Uint8List> (each chunk ~100 ms)
       ▼
AlibabaWebSocketStt
       │
       │ 1. WebSocket connect → wss://dashscope.aliyuncs.com/api-ws/v1/inference
       │    Authorization: Bearer <key>
       │ 2. Send run-task JSON { task_id, model: "fun-asr-realtime", parameters: { format: pcm, sample_rate: 16000, language_hints, punctuation_prediction_enabled: true } }
       │ 3. Await task-started event
       │ 4. For each PCM chunk → send as binary WebSocket frame
       │ 5. Listen for result-generated events → emit partial transcript stream
       │ 6. On stop → send finish-task JSON → drain final result-generated → close
       ▼
Stream<TranscriptUpdate { text, isPartial, isFinal }>
       │
       ▼
_chatInput field (partials replace prior; final text is the committed value)
       │
       │ [user reviews, edits if needed, taps send]
       ▼
postAgentInput(kind: 'text')   ← existing hub path; unchanged
```

**The hub is not in the audio path.** Mobile → DashScope direct. The
transcript reaches the hub via the existing `postAgentInput(kind:
'text')` path, indistinguishable from typed input. No new hub
endpoints, no `/v1/voice/*`, no audio bytes ever cross our wire.

### Why `record.startStream(pcm16bits)`

- `record: ^5.x` exposes `startStream(RecordConfig(encoder:
  AudioEncoder.pcm16bits))` which emits a `Stream<Uint8List>` of raw
  PCM directly — no file roundtripping, no transcoding step.
- DashScope wants 16 kHz mono PCM in binary WebSocket frames at
  ~100 ms cadence; the `record` stream already chunks at that
  cadence on Android.
- Cancellation is clean: `record.stop()` closes the stream; we don't
  need to delete a temp file.

### WebSocket client shape

Drops into `lib/services/voice/cloud_stt.dart`:

```dart
abstract class CloudStt {
  /// Streams transcript updates while [audioChunks] is open.
  /// Closes when [audioChunks] closes AND the server has sent its
  /// final result-generated event after our finish-task.
  Stream<TranscriptUpdate> transcribeStream(
    Stream<Uint8List> audioChunks, {
    required List<String> languageHints, // e.g. ['zh', 'en']
  });
}

class AlibabaWebSocketStt implements CloudStt {
  AlibabaWebSocketStt({
    required this.apiKey,
    required this.region,        // beijing | singapore | us
    required this.model,         // fun-asr-realtime | paraformer-realtime-v2
  });
  // wss://dashscope[-intl|-us].aliyuncs.com/api-ws/v1/inference
  // ... task_id UUID, run-task / task-started / binary chunks /
  // result-generated / finish-task / task-finished orchestration
}

class TranscriptUpdate {
  final String text;
  final bool isPartial; // true while server is still refining
  final bool isFinal;   // true on the last frame of a sentence
}
```

### Why review-then-send

ADR-023 D4 locks the principle: "the user always reviews what
reaches the steward." Voice's per-utterance error rate is non-zero
even at SOTA — auto-send risks the steward acting on a misheard
verb ("show" → "remove"). The mic→partial→edit→send loop pays back
the extra tap with safety.

## Wedges

Each wedge is independently shippable.

### W1 — Audio recording infrastructure (~70 LOC)

- Add `record: ^5.x` to `pubspec.yaml`.
- `AndroidManifest.xml` — add `<uses-permission android:name="android.permission.RECORD_AUDIO"/>`.
- `Info.plist` — add `NSMicrophoneUsageDescription` (deferred to iOS
  follow-up but cheap to add now).
- New `lib/services/voice/recording_controller.dart`:
  - `Future<Stream<Uint8List>> start()` — request permission,
    configure `pcm16bits` + 16 kHz mono, return the recorder's PCM
    chunk stream.
  - `Future<void> stop()` — closes the underlying recorder; downstream
    consumer (`AlibabaWebSocketStt`) sees the stream close and sends
    `finish-task`.
  - `void cancel()` — closes without committing.
- Unit tests: permission flow + mock `AudioRecorder`.

**Acceptance:** can open a 5 s PCM16 stream from the controller in a
test harness; permission denial surfaces a snackbar.

### W2 — DashScope WebSocket client (~140 LOC)

- Add `web_socket_channel: ^2.x` to `pubspec.yaml`.
- New `lib/services/voice/cloud_stt.dart`:
  - `CloudStt` abstract class (see Architecture).
  - `AlibabaWebSocketStt` concrete impl with state machine:
    - `connecting` → `running` → `finishing` → `closed`
    - On connect: send `run-task` JSON, wait for `task-started`
    - On each PCM chunk: send as binary frame
    - On each `result-generated`: parse and emit
      `TranscriptUpdate(text, isPartial, isFinal)`
    - On audio stream close: send `finish-task`, drain
      `result-generated` until `task-finished`, then close socket
    - On `task-failed`: emit error, close
  - Region enum: `beijing | singapore | us` → endpoint URL.
  - Model enum: `funAsrRealtime | paraformerRealtimeV2`.
  - Per-second cost: TODO — verify in DashScope console once a key
    is loaded (no public docs found; track usage from `payload.usage.
    duration` echoes).
- Unit tests with a `FakeWebSocketChannel`:
  - happy path: connect → task-started → 3 PCM chunks → 2 partials
    + 1 final → finish-task → task-finished → close
  - 401 (bad key) at handshake
  - task-failed mid-stream (server-side error code)
  - client disconnect mid-stream (audio stream cancelled before
    finish-task)

**Acceptance:** with a valid key + a recorded 5 s PCM16 stream from
W1, returns 1+ partial `TranscriptUpdate` and exactly one final
update with the full sentence. Tests cover the four state-machine
exit paths.

### W3 — Mic FAB + push-to-talk UX (~140 LOC)

- New `lib/widgets/steward_overlay/voice_mic_button.dart`:
  - Replaces send button when `_chatInput` is empty AND
    `settings.voiceInputEnabled == true` AND device is online.
  - Offline state: greyed button + tooltip "Voice input requires
    connection" (`connectivity_plus` listen).
  - `GestureDetector`: `onLongPressStart` → start; `onLongPressEnd`
    → commit; drag-out → cancel.
  - Recording pill: red pulse + elapsed timer (mm:ss) + a small
    inline language chip (`auto / zh / en`) per-utterance override.
  - 50 s soft haptic hint; 60 s auto-stop → calls commit.
  - Partial transcripts stream into `_chatInput.text` as they
    arrive; final replaces partials; caret to end.
  - On error: snackbar with the error message; partial text
    discarded.

**Acceptance:** in the overlay, holding the mic + speaking shows
partial text in the input field within ~600 ms, the final commit
within ~1 s after release. Tap the language chip to toggle `auto →
zh → en` before recording. Drag-out cancels with no transcript.

### W4 — Settings (~90 LOC)

- New screen `lib/screens/settings/voice_settings_screen.dart`:
  - Toggle: "Voice input" (gates the mic button).
  - API key field (password input, paste-friendly, secure storage).
  - Region picker (Beijing default / Singapore / US).
  - Model picker (Fun-ASR realtime default / Paraformer realtime
    v2). Help text explaining the zh+en+dialect coverage tradeoff.
  - Default language hints (multi-select: zh, en, ja, yue, ko, de,
    fr, ru — Paraformer-only; Fun-ASR is fixed-set).
  - "Test recording" button — opens a sheet that records 5 s with
    the current config and shows the streamed transcript inline,
    so the user can verify their key works without rolling the
    dice on a real steward turn.
- New provider `voiceSettingsProvider` (Notifier; `await _ready`
  pattern per `feedback_prefs_load_race.md`).

**Acceptance:** user pastes a Beijing-region key, hits "Test
recording", says "Hello world 你好", sees partials stream then the
final mixed-language transcript within ~1 s.

### W5 — Tests + docs + memory (~60 LOC)

- Integration test: end-to-end with `AlibabaWebSocketStt`
  injected via a fake `WebSocketChannel`.
- Update `docs/changelog.md`.
- Update
  [`how-to/test-agent-driven-prototype.md`](../how-to/test-agent-driven-prototype.md)
  with "Voice input (Path C)" section: how to enter a key (Beijing
  region), how to test, how to revoke.
- Update
  [`discussions/voice-input-cloud-vs-offline.md`](../discussions/voice-input-cloud-vs-offline.md)
  to mark Path C as "shipped v1.0.5xx" and the sequencing decision
  as resolved.
- Update memory: re-point
  `project_voice_input_discussion.md` at the new plan + lessons
  worth saving (e.g. "DashScope WebSocket = binary PCM frames, not
  base64 JSON" if it ends up being a foot-gun).

**Acceptance:** changelog entry, how-to walkthrough, and discussion
status block all reflect the shipped state.

## Locked decisions (principal Q&A, 2026-05-12)

- **Q1 → Beijing default.** Cheaper per-second; principal already
  has a Beijing-region key for testing.
- **Q2 → Offline auto-disable.** Confirmed; mic greyed when
  `connectivity_plus` reports no connection.
- **Q3 → 60 s soft cap.** Confirmed; 50 s haptic hint, 60 s auto-
  stop.
- **Q4 → Per-utterance language chip.** Ship in v1, not v2. Lives
  on the recording pill, cycles `auto / zh / en`. Default = Settings
  default = `[zh, en]` language hints.
- **Q5 → No audio logging.** Confirmed; PCM frames flow microphone
  → WebSocket → discarded. Only transcript text persists (via
  existing `agent_events`).
- **Q6 → No multi-key support.** Confirmed; one key per device.

## Lingering open questions (do not block v1)

- **Cost telemetry.** Per-second pricing isn't in the public docs;
  `payload.usage.duration` echoes the billed seconds per result.
  We could roll those up into a local "voice input usage this
  month" tile in Settings → Voice. Track as a v1.x follow-up.
- **Reconnect on transient WebSocket drop.** v1 surfaces a
  snackbar on disconnect and discards the partial. A reconnect-
  with-context (preserving the same `task_id`) is potentially
  supported by Fun-ASR's "connection multiplexing" feature but not
  needed for 60 s push-to-talk. Track as v1.x follow-up.

## LOC budget

| Wedge | Content | LOC |
|---|---|---|
| W1 | Recording infrastructure | ~70 |
| W2 | DashScope WebSocket client | ~140 |
| W3 | Mic FAB + push-to-talk UX | ~140 |
| W4 | Settings screen + provider | ~90 |
| W5 | Tests + docs + memory | ~60 |
| **Total** | | **~500** |

Compare:
- Deferred Path D plan: ~1000 LOC + 85–200 MB APK bloat
- This plan: ~500 LOC + 0 APK bloat (just the `record` +
  `web_socket_channel` plugin shims)

## Migration path to Path D (offline)

Once Path C ships and tester feedback is collected:

1. Add a `LocalFunAsrStt implements CloudStt` adapter that wraps a
   self-hosted Fun-ASR or SenseVoice inference (`sherpa_onnx` or
   `onnxruntime`).
2. Adapter signature is unchanged: `Stream<TranscriptUpdate>
   transcribeStream(audioChunks, languageHints)`. The UX surface,
   settings layout, mic gesture, language chip, and review-then-
   send loop **do not move**.
3. Settings adds a fourth region: "Local (offline)" → routes to
   the local adapter.
4. The Path D plan's APK-split + model-download manager work
   (~500 LOC) is what makes Path D expensive; the *integration*
   into the overlay was already done by Path C.

This is the strategic dividend of picking Alibaba: a one-time
~500 LOC investment now buys both the cloud surface and the
offline migration runway.

## Risks

- **R1: DashScope WebSocket outages / regional flakiness.**
  Mitigate with reconnect-on-handshake-failure (2 attempts, 250 ms
  / 500 ms backoff) and a clear snackbar on persistent failure.
  Cap loss: the user types instead. Voice is additive, not load-
  bearing.
- **R2: Audio recording permission denied.** Permission flow
  surfaces a snackbar + "Enable in Settings" deep link. Mic button
  stays visible but throws a tooltip on tap until granted.
- **R3: Bilingual model misclassifies mid-utterance switch.** Less
  likely with Fun-ASR than with Whisper/Deepgram by published
  benchmarks. Per-utterance language chip (Q4) is the escape
  hatch.
- **R4: PCM frames at 16 kHz × 16-bit × mono = 32 KB/sec.** Over 5
  minutes that's 9.6 MB; well within typical mobile bandwidth. At
  100 ms chunks that's 3.2 KB per WebSocket frame — small enough
  that bandwidth and latency stay flat across both Wi-Fi and
  cellular.
- **R5: API key in transit.** TLS only via `dashscope[-intl|-us].
  aliyuncs.com`. Key never logged, never sent to hub, never
  persisted outside `flutter_secure_storage`. PCM frames never
  hit disk.
- **R6: WebSocket vs Android background.** If the user
  backgrounds the app mid-recording, Android may suspend the
  socket. v1 cancels the recording on app pause; partial text is
  discarded. (Foreground-service variant is a v1.x follow-up if
  testers complain.)

## Done criteria

- All five wedges shipped.
- Holding the mic in the overlay for 5 s and speaking produces
  partial transcripts within 600 ms and a final committed
  transcript within 1 s after release.
- Principal pastes their Beijing-region key in Settings, hits
  "Test recording", and verifies a mixed zh+en utterance round-
  trips correctly.
- Tester walkthrough in `how-to/test-agent-driven-prototype.md`
  covers the voice path.
- Memory file `project_voice_input_discussion.md` updated to
  point at the shipped state.

## References

- [`discussions/voice-input-cloud-vs-offline.md`](../discussions/voice-input-cloud-vs-offline.md)
  — the design space; this plan implements Path C.
- [`decisions/023-agent-driven-mobile-ui.md`](../decisions/023-agent-driven-mobile-ui.md)
  — D3 (voice via IME is the canonical text-entry path; this plan
  *augments* not *replaces* it) and D4 (review-then-send).
- [`plans/voice-input-overlay-v1.md`](voice-input-overlay-v1.md) —
  the deferred Path D plan this supersedes. Reactivate when offline
  becomes load-bearing.
- DashScope WebSocket — Fun-ASR realtime: <https://help.aliyun.com/zh/model-studio/fun-asr-realtime-websocket-api>
- DashScope WebSocket — Paraformer realtime v2: <https://help.aliyun.com/zh/model-studio/websocket-for-paraformer-real-time-service>
- Fun-ASR open weights: <https://github.com/modelscope/FunASR>
