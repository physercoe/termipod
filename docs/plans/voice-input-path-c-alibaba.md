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

After this plan, voice input has **two distinct entry points** —
each tuned to a different intent. They share one recording stack
and one transcription pipeline; only the post-transcript routing
differs.

### Mode A — Puck long-press (ambient, panel-hidden)

The hands-free path. The user is on some other screen (project
detail, terminal, settings) with the steward overlay puck collapsed.

1. Long-press the floating puck → recording starts. **Panel stays
   closed.** The user's current screen does NOT change.
2. A small floating recording pill anchors next to the puck — red
   pulse, elapsed timer, language chip (`auto / zh / en`). The
   transcript stream does NOT render here (the user isn't looking
   at a text field; partials would just be visual noise).
3. Release → behavior depends on the **"Auto-send puck transcripts"**
   setting (Settings → Voice; default **on**):
   - **Toggle on (default — hands-free):** final transcript is
     auto-sent to the steward directly, bypassing review. A
     transient toast confirms what was sent (e.g. "Sent: 'show me
     the experiment run'"). The panel still stays closed; the user
     keeps watching the screen they were on, knowing the steward
     is now working.
   - **Toggle off (review fallback):** the panel auto-opens with
     the transcript pre-filled in the chat input field. User
     reviews, edits if needed, taps send. This effectively routes
     puck long-press through Mode B's commit handler — same
     review-then-send safety, just initiated from the puck instead
     of the panel mic button.
4. To see the steward's response (auto-send case), the user
   manually taps the puck to open the panel. This decouples "talk
   to the steward" from "watch the steward respond."
5. Drag-out during long-press → cancels with no transcript, no
   send, no panel-open. Critical because the user can't see what
   they said.
6. If transcript is empty / whitespace-only → silently drop
   regardless of the toggle. Don't waste a steward turn (auto-send)
   or pop a useless panel (review fallback) on a misfire.

### Mode B — Panel-open mic button (review-then-send)

The deliberate path. The user has the panel open, is composing a
message, and wants to dictate into the input field.

1. The chat input's send button is replaced by a mic button when
   the field is empty.
2. Long-press → record. Partial transcripts stream into the input
   field as the user speaks (a real-time UX win over batch mode).
3. Release → final transcript replaces partials in the input field.
   User reviews, edits if needed, taps send.
4. Drag-out cancels with no committed text.
5. Honors ADR-023 D4 (review-then-send for deliberate compose
   flow).

### Shared properties

- Recording soft cap **60 s** (50 s haptic hint, 60 s auto-commit).
- Bilingual zh + en + 8 Chinese dialects in one model (Fun-ASR
  auto-detects). Per-utterance language chip overrides the
  Settings default.
- Mic gestures auto-disable when offline (`connectivity_plus`).
- API key in `flutter_secure_storage`; entered via Settings →
  Voice → DashScope API Key.
- **Default region: Beijing** (`dashscope.aliyuncs.com`) — cheapest
  per-second; principal already holds a Beijing key for testing.
- **The hub never sees audio.** Transcripts reach the hub via the
  existing `postAgentInput(kind: 'text')` path, indistinguishable
  from typed input.

### Why two modes, not one

Mode A and Mode B have **different latency budgets and different
risk profiles**, so collapsing them into one would compromise both:

- **Mode A is for short, well-formed commands** the user has already
  composed in their head ("show me the run"). Speed beats safety
  — the user wants action, not a transcript-review modal. The puck
  is the affordance because it's available everywhere.
- **Mode B is for longer or more nuanced messages** where the user
  wants to see what was captured before sending. The panel is the
  affordance because the message belongs in a chat.

The same pipeline serves both; only the commit step differs (toast
+ auto-`postAgentInput` vs `_chatInput.text = transcript`).

### UX layer vs protocol layer (clarification)

"Utterance vs streaming" can mean two different things; this plan
uses both axes but pins them independently:

- **UX layer:** push-to-talk / utterance. The user explicitly starts
  (long-press) and ends (release) each recording. This is not
  always-on dictation with VAD-driven turn-taking. The user owns
  the start/stop boundary, the device just executes.
- **Protocol layer:** WebSocket streaming. PCM16 chunks flow to
  DashScope every ~100 ms while the user is holding; the server
  emits `result-generated` events as it processes each chunk and
  refines its hypothesis.

These axes are **independent**. **Yes, live partial transcripts
work in our utterance UX** — the user does push-to-talk on the
outside while the protocol streams on the inside. That's why
Mode B can show partials accumulating in the input field even
though the user is doing utterance-style press-and-hold (Mode A
intentionally hides those partials because the user isn't watching
a text field).

If we ever wanted always-on dictation (no press-and-hold), we'd
add a third UX layer on top of the same streaming protocol —
toggle button, VAD-driven end-of-utterance detection, etc. Not
in v1.

## Non-goals (locked by Q&A 2026-05-12)

- **Audio logging.** No on-disk audio history. PCM frames flow
  microphone → WebSocket → discarded. The transcript text is the
  only persisted artifact (via the normal `agent_events` flow once
  the user — or auto-send — commits). Principal Q5.
- **Audio telemetry / audit / cost rollup.** No `voice_usage` table,
  no `payload.usage.duration` aggregation, no Settings → Voice →
  "Usage this month" tile. v1 ships with zero observability on the
  voice path beyond standard request-level logging that omits the
  audio payload. Track cost out-of-band in the DashScope console
  if needed. Principal Q5-extended (2026-05-12 followup).
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

The recording stack + ASR pipeline is **shared**. The two modes
differ only in where the transcript lands at the bottom of the
diagram.

```
                 ┌─────────────────────────────────────┐
                 │       Mode A: puck long-press       │
                 │  (panel collapsed, ambient command) │
                 ├─────────────────────────────────────┤
                 │       Mode B: panel mic button      │
                 │  (panel open, compose with review)  │
                 └─────────────────────────────────────┘
                                  │
                                  │ both modes invoke:
                                  ▼
                       RecordingController
                                  │
                                  │ record.startStream(pcm16bits, 16kHz mono)
                                  │
                                  │ Stream<Uint8List> (~100 ms chunks)
                                  ▼
                       AlibabaWebSocketStt
                                  │
                                  │ 1. WS connect → wss://dashscope.aliyuncs.com/api-ws/v1/inference
                                  │    Authorization: Bearer <key>
                                  │ 2. Send run-task JSON
                                  │    { task_id, model: fun-asr-realtime,
                                  │      parameters: { format: pcm, sample_rate: 16000,
                                  │                    language_hints, punctuation_prediction_enabled: true } }
                                  │ 3. Await task-started
                                  │ 4. For each PCM chunk → binary frame
                                  │ 5. Listen for result-generated → emit partials
                                  │ 6. On stop → finish-task → drain → close
                                  ▼
                       Stream<TranscriptUpdate { text, isPartial, isFinal }>
                                  │
                ┌─────────────────┴─────────────────┐
                │                                   │
                ▼                                   ▼
   Mode A (puck commit):                 Mode B (panel commit):
   final text → trim                     each partial → _chatInput.text
   if non-empty:                         final → _chatInput.text (caret end)
     postAgentInput(text)                user reviews → taps send →
     toast "Sent: '<text>'"                postAgentInput(text)
   panel stays closed
```

**The hub is not in the audio path.** Mobile → DashScope direct.
Transcript reaches the hub via the existing `postAgentInput(kind:
'text')` path, indistinguishable from typed input. No new hub
endpoints, no `/v1/voice/*`, no audio bytes ever cross our wire.

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

### W3 — Mic UX for both modes (~200 LOC)

Two integration points share the same `VoiceRecordingSession`
controller; only their commit handlers differ.

**W3a — Mode B: panel-open mic button** (~100 LOC)

- New `lib/widgets/steward_overlay/voice_mic_button.dart`:
  - Replaces send button when `_chatInput` is empty AND
    `settings.voiceInputEnabled == true` AND device is online.
  - Offline state: greyed button + tooltip "Voice input requires
    connection" (`connectivity_plus` listen).
  - `GestureDetector`: `onLongPressStart` → start session;
    `onLongPressEnd` → commit; drag-out → cancel.
  - Partial transcripts stream into `_chatInput.text` as they
    arrive; final replaces partials; caret to end.
  - On error: snackbar; partial text discarded.

**W3b — Mode A: puck long-press** (~100 LOC)

- Extend the existing floating puck widget in
  `lib/widgets/steward_overlay/` to add a long-press recognizer:
  - Existing tap behavior (open panel) is preserved on **short
    tap**. Long-press starts recording — different gesture, no
    collision.
  - `onLongPressStart` → start session; the panel stays closed
    (do NOT call the controller's open-panel API).
  - A new floating `_VoiceRecordingPill` widget anchors next to
    the puck (offset to avoid covering it), showing red pulse +
    elapsed timer + language chip. The pill is NOT a partial-
    transcript surface — the user isn't looking at a text field.
  - `onLongPressEnd` → commit. Read final transcript, `.trim()`.
    - If empty/whitespace: silently drop. No toast, no send, no
      panel-open. (Same regardless of auto-send toggle.)
    - If non-empty AND `settings.puckAutoSend == true`:
      `postAgentInput(kind: 'text', text: <transcript>)` directly
      via `hubProvider`, bypassing the chat input. Emit a
      snackbar/toast: `Sent: "<first 60 chars>…"`.
    - If non-empty AND `settings.puckAutoSend == false`: open the
      overlay panel via the existing controller's open API and
      pre-fill `_chatInput.text` with the transcript (caret to
      end). No auto-send; user reviews and taps send. This is
      Mode B's commit handler, just invoked from a Mode A start.
  - `onLongPressMoveUpdate` with displacement > threshold →
    cancel session, dismiss pill, no commit, no toast.
  - On error mid-session: dismiss pill, snackbar with error,
    no send.
  - Panel state is **not touched** by Mode A. If the panel was
    closed it stays closed; the user opens it manually when
    they want to see the steward's response.

**Shared recording pill widget** (counted in W3b):
- Red pulse animation + elapsed timer (mm:ss).
- Inline language chip cycling `auto / zh / en` on tap (default
  from Settings; per-utterance override only applies to *this*
  recording).
- 50 s elapsed → haptic + amber tint hint; 60 s → auto-commit.
- Dismissible only via commit, cancel, or auto-stop.

**Acceptance:**

- **Mode B:** holding the panel mic + speaking shows partial text in
  the input field within ~600 ms; final commits in input within ~1
  s after release; user can edit + tap send. Drag-out cancels.
- **Mode A:** with the panel collapsed, long-press the puck on any
  screen, speak, release. Within ~1 s a toast appears with the
  sent transcript; the panel does NOT auto-open; the user's
  current screen is unchanged. Manually tapping the puck (short
  tap) opens the panel and the new agent message + steward
  response are visible there.
- Tap the language chip on the pill to toggle `auto → zh → en`
  before/during recording.
- Drag-out during the puck long-press cancels with no send and no
  toast.

### W4 — Settings (~100 LOC)

- New screen `lib/screens/settings/voice_settings_screen.dart`:
  - Toggle: "Voice input" (gates both mic affordances — panel
    button AND puck long-press).
  - Toggle: **"Auto-send puck transcripts"** — default **on**.
    Subtitle: "When off, puck long-press opens the chat for
    review before sending." (Mode B's panel mic button is
    unaffected — it's always review-then-send.)
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
  on the recording pill (both modes), cycles `auto / zh / en`.
  Default = Settings default = `[zh, en]` language hints.
- **Q5 → No audio logging AND no audio telemetry.** Confirmed; PCM
  frames flow microphone → WebSocket → discarded. No `voice_usage`
  table, no `usage.duration` rollup, no Settings → Voice → "Usage"
  tile. Only transcript text persists (via existing `agent_events`).
- **Q6 → No multi-key support.** Confirmed; one key per device.
- **Q7 → Two voice entry points, two commit semantics.** Confirmed:
  - Mode A (puck long-press, panel hidden): **auto-send by
    default**; transient toast confirms; panel does NOT auto-open.
    Settings → Voice → "Auto-send puck transcripts" toggle
    controls this — when off, puck long-press routes through
    Mode B's review handler (panel auto-opens with transcript
    pre-filled).
  - Mode B (panel mic button, panel open): **review-then-send**
    into the chat input field (honors ADR-023 D4). Always.
  - Both modes share recording stack + ASR pipeline; only commit
    handlers differ.
- **Q8 → UX layer ≠ protocol layer.** Confirmed:
  - **UX is utterance** (push-to-talk: user owns start/stop via
    long-press).
  - **Protocol is streaming** (PCM chunks every ~100 ms over
    WebSocket; server emits partials as it processes them).
  - These are independent axes — live partials work in Mode B's
    utterance UX because the underlying protocol is streaming.
    Mode A intentionally suppresses partial rendering even though
    they're available, because the user isn't watching a text
    field while the puck is active.

## Lingering open questions (do not block v1)

- **Reconnect on transient WebSocket drop.** v1 surfaces a
  snackbar on disconnect and discards the partial. A reconnect-
  with-context (preserving the same `task_id`) is potentially
  supported by Fun-ASR's "connection multiplexing" feature but not
  needed for 60 s push-to-talk. Track as v1.x follow-up.
- **Mode A misfire UX.** If the puck long-press auto-sends a
  garbled transcript ("xxxyyyzzz"), the user has no undo. Two
  candidate follow-ups: (a) an undo button in the toast for ~5 s;
  (b) a "voice review threshold" setting where transcripts below
  some confidence drop into Mode B (the panel opens with the text
  in the input). Track for v1.x once tester reports surface real
  misfire rates.

## LOC budget

| Wedge | Content | LOC |
|---|---|---|
| W1  | Recording infrastructure | ~70 |
| W2  | DashScope WebSocket client | ~140 |
| W3a | Mode B — panel mic button | ~100 |
| W3b | Mode A — puck long-press + recording pill | ~100 |
| W4  | Settings screen + provider (incl. auto-send toggle) | ~100 |
| W5  | Tests + docs + memory | ~60 |
| **Total** | | **~570** |

Compare:
- Deferred Path D plan: ~1000 LOC + 85–200 MB APK bloat
- This plan: ~560 LOC + 0 APK bloat (just the `record` +
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

- All wedges shipped (W1 / W2 / W3a / W3b / W4 / W5).
- **Mode A:** with the panel collapsed, long-pressing the puck on
  any screen, speaking, and releasing produces a toast confirming
  what was sent within ~1 s; the panel does NOT auto-open.
- **Mode B:** with the panel open, long-pressing the mic button
  shows partial transcripts in the chat input within 600 ms and a
  final committed transcript within 1 s after release.
- Principal pastes their Beijing-region key in Settings, hits
  "Test recording", and verifies a mixed zh+en utterance round-
  trips correctly.
- Tester walkthrough in `how-to/test-agent-driven-prototype.md`
  covers both modes.
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
