# Voice input for the steward overlay — v1 (push-to-talk, Android)

> **Type:** plan
> **Status:** Open
> **Audience:** contributors
> **Last verified vs code:** v1.0.472

**TL;DR.** Add push-to-talk voice input to the steward overlay
chat: long-press the mic button to record, release to transcribe,
drag-out to cancel. Android-only for v1; iOS deferred. Uses
SenseVoice-Small via `sherpa_onnx` Flutter package. Model loads
eagerly on app boot (parallel to hub bootstrap) so the first mic
tap feels instant. Ship in two APK flavors: `full` bundles the
model (~200 MB), `lite` downloads it on first use (~40 MB install
+ on-demand pull). Aligns with the agent-driven-mobile-ui framing
where the user *directs* the steward via short utterances ("show
me the insights view") rather than typing them.

## Goal

After this wedge:

1. The steward overlay chat input has a microphone button. Tap-and-
   hold to record, release to transcribe. The transcript appears
   in the text field (not auto-sent) so the user can review +
   correct before tapping send.
2. Drag finger off the mic button while still holding → recording
   discarded, no transcript produced.
3. Recording cap matches what SenseVoice + push-to-talk can handle
   without VAD: 30 s soft limit per recording. UI shows an elapsed
   timer; at 25 s a haptic + colour shift hints "wrap up." At 30 s
   the recording auto-stops and transcribes whatever was captured.
4. Bilingual zh + en in one model (no language toggle needed —
   SenseVoice auto-detects).
5. Two distributable APK flavors: `full` (model bundled) and
   `lite` (model downloaded on first voice activation).

## Non-goals

- **iOS.** Deferred to v2. The native lib + model strategy is
  identical, but iOS needs CoreML conversion + xcframework
  packaging + App Store size tier work; that's its own wedge.
- **Streaming / live partial transcripts.** v1 is utterance-mode
  only: record → release → result. Streaming dictation
  (`streaming-zipformer-bilingual-zh-en`) lives in a v2 wedge if
  push-to-talk's UX limits become the bottleneck.
- **Voice Activity Detection (VAD).** Not needed for push-to-talk
  — the user controls start/stop manually. VAD enters the picture
  only if we add long-form voice messages or true streaming.
- **Auto-send after transcribe.** v1 always lands the transcript
  in the input field for review. Auto-send is one toggle in
  Settings → Experimental but ships **off** by default.
- **Other languages.** SenseVoice-Small supports zh / en / ja /
  ko / yue out of the box; we ship as bilingual zh-en for v1
  framing, but the model handles ja/ko/yue too if a user happens
  to speak them.
- **Voice output (TTS).** Steward stays text-only for v1. ADR-023
  Q-future tracks whether to add TTS for accessibility / hands-free
  modes.
- **Hub change.** All work is mobile-side. Transcripts feed into
  the existing `postAgentInput(kind: 'text')` path unchanged.

## Why now

- Voice is the natural input modality for the agent-driven UX
  framing: "tell the steward what you want to see" is faster
  spoken than typed, especially in zh.
- The `_ChatInput` IME-stability work already shipped (v1.0.471–
  472) means the input field is now stable enough to be a
  *transcript display surface* — predictive text won't fight the
  programmatically-set value.
- The `sherpa_onnx` Flutter package is genuinely production-ready
  (see [`docs/discussions/voice-input-research.md`](../discussions/voice-input-research.md)
  if drafted; otherwise the principal's research conversation).
  No infrastructure work is required on our side.
- Andriod-first scoping matches the user base in the demo arc.
  iOS slips one wedge with no protocol cost.

## Architecture

### Stack

| Layer | Choice | Why |
|---|---|---|
| Runtime | `sherpa_onnx` Flutter package (pub.dev) + `sherpa_onnx_android` | Production-grade, ~51× faster than whisper.cpp, has Flutter bindings, used in shipped apps |
| Model | SenseVoice-Small int8 (~85 MB) | Best accuracy/speed tradeoff for utterance-mode bilingual zh-en; 70 ms inference for 10 s audio per published benchmarks |
| Audio capture | `record` Flutter package | Mature, supports raw PCM 16 kHz mono (sherpa-onnx's expected input); permission handling included |
| Permissions | `permission_handler` | App already vendors it; reuse |
| Native ABIs | arm64-v8a + x86_64 | arm64 for real devices, x86_64 for the emulator. armeabi-v7a omitted — sherpa-onnx perf is poor on 32-bit |

### Component layout

```
lib/services/voice/
├── voice_input_service.dart       # Lifecycle: load → record → transcribe
├── voice_model_loader.dart        # Eager model load on app boot; download for lite
└── audio_capture.dart             # `record` wrapper with PCM-16k normalization

lib/widgets/steward_overlay/
├── steward_overlay_mic_button.dart  # New: mic FAB with long-press recognizer
└── steward_overlay_chat.dart        # Modified: replace single send button with mic + send

lib/providers/
└── voice_input_provider.dart      # Riverpod surface; consumed by chat input
```

### Eager model load on app boot

Added to `main.dart` post-hub-bootstrap:

```dart
// After settings load, before runApp completes — kick off model load
// in a background isolate so the first mic tap doesn't pay the
// ~500ms-1s init cost. Settings → Experimental → Voice Input toggle
// gates this; if voice is off, we never spawn the loader.
if (settings.voiceInputEnabled) {
  unawaited(VoiceModelLoader.instance.startLoading());
}
```

`VoiceModelLoader` exposes a `loadingState` ValueNotifier:

- `notLoaded` (initial; voice off OR not started)
- `downloading(progress: 0.0-1.0)` (lite flavor first run only)
- `loading` (model file present, sherpa-onnx initializing)
- `ready` (model alive in an isolate, ready to transcribe)
- `failed(reason)` (download/init failed; surface via Settings)

The mic button reads this state to render:
- `notLoaded`/`loading` → greyed mic + tooltip "Voice loading…"
- `downloading` → progress ring around mic + "Downloading 12.4 MB / 85 MB"
- `ready` → live mic, long-press enabled
- `failed` → red mic + tap shows error sheet with retry

### Push-to-talk UX

The mic button replaces the send IconButton in `_ChatInput` when
the field is empty. When the field has text, the send button
returns. (Keeps the affordance count low; users dictating won't
manually type.)

```dart
class _ChatInput extends StatefulWidget {
  // ... existing onSend ...
  final Future<void> Function(String text) onTranscribed; // text into field
}
```

Gesture handling on the mic FAB:

- `GestureDetector(onLongPressStart: _startRecording, onLongPressEnd: _stopRecording, onLongPressMoveUpdate: _maybeCancel)`
- `_startRecording`: haptic medium impact, start recorder, expand
  button to a "recording pill" with timer + cancel hint ("← drag to cancel")
- `_maybeCancel`: if pointer moves > 80 px from start position
  (typical thumb travel), set `_cancelArmed = true`, swap pill to
  red "release to cancel"
- `_stopRecording`:
  - if `_cancelArmed`: discard buffer, light haptic
  - else: feed buffer through `VoiceInputService.transcribe()`,
    populate text field, focus the input so the user can edit /
    send

```
Idle:                [ Type… ] [🎤]
Press-hold:          [ Recording 0:03 ◀ slide to cancel  ] (red bar)
Drag-cancel armed:   [ Release to cancel                 ] (deep red)
Release (commit):    [ "show me the insights view" |    ] [✈️]
Release (cancel):    [ Type… ] [🎤]   (back to idle)
```

### Recording timer + 30 s cap

The 30 s cap is the SenseVoice single-segment limit. UI:
- 0–25 s: timer text, calm colour
- 25–30 s: timer pulses amber, haptic at :25
- 30 s: auto-release as if user lifted, transcribe what we have

Beyond 30 s would need VAD-segmented chunking (v2). For v1 we
hard-cut and accept the small accuracy hit on the very edge.

### Threading

Inference runs in a Dart isolate. The main isolate:
- captures audio (16 kHz PCM, ~32 KB/s)
- ships the buffer to the inference isolate via SendPort
- awaits the transcript reply
- updates the controller value

Keeps the UI thread free during the ~150–300 ms ASR call.

### Data flow

```
[mic tap]
    ↓
[record package → 16kHz mono PCM bytes]
    ↓ (release)
[isolate: sherpa_onnx.transcribe(pcm) → "show me the insights view"]
    ↓
[ChatInput._ctrl.text = transcript]
    ↓
[user reviews + taps send  →  postAgentInput(kind:'text')]
```

## APK split — `full` vs `lite`

### Build flavors

`android/app/build.gradle.kts` gains:

```kotlin
flavorDimensions += "model"
productFlavors {
    create("lite") {
        dimension = "model"
        applicationIdSuffix = ".lite"
        manifestPlaceholders["modelBundled"] = "false"
    }
    create("full") {
        dimension = "model"
        manifestPlaceholders["modelBundled"] = "true"
    }
}
```

Flutter side: a build-time const indicates which flavor:

```dart
const bool kModelBundled = bool.fromEnvironment('MODEL_BUNDLED');
```

Set via `--dart-define=MODEL_BUNDLED=true` for full, `false`
for lite. CI release matrix produces both APKs.

### `full` flavor

- `assets/voice_models/sense-voice-small-int8/` ships in APK
- ~85 MB model file → APK ~200 MB total (incl. native libs ~25 MB)
- `VoiceModelLoader` reads from `rootBundle` and writes to app
  docs dir on first run (sherpa-onnx needs a file path, not bytes)
- First-run extraction takes ~3–5 s; subsequent runs ~50 ms init

### `lite` flavor

- No model assets in APK
- APK ~40 MB total (just native libs + Flutter runtime)
- On first long-press of mic (or earlier if user pre-enables in
  Settings → Voice → "Download model now"), `VoiceModelLoader`:
  1. Confirms wifi-or-cellular policy (default: wifi-only, with
     a toggle for "Use cellular" and a cost confirmation dialog)
  2. Downloads from a CDN-hosted SHA256-verified tarball (~85 MB)
  3. Extracts to app docs dir
  4. Loads into sherpa-onnx
- Total cold-start time on lite first-run: ~30–60 s on wifi
- Settings → Voice → "Delete downloaded model" frees the space

### Distribution

Both APKs go to GitHub Releases. README + voice settings sheet
explain the tradeoff. Default direct-link points users to `lite`;
`full` is the option for users who want offline-from-install.

## Workband layout

### W1 — Native libs + model loader scaffolding (~250 LOC)

- Add `sherpa_onnx`, `sherpa_onnx_android`, `record`,
  `permission_handler` to pubspec
- Wire arm64 + x86_64 ABI splits in build.gradle
- Implement `VoiceModelLoader` with state machine: notLoaded /
  downloading / loading / ready / failed
- Eager-load hook in `main.dart` (gated on settings flag)
- Settings → Experimental → "Voice input" toggle (default off);
  sub-pane has "Download model" button (lite only) + "Delete
  model"

### W2 — Audio capture + push-to-talk gesture (~200 LOC)

- Implement `AudioCapture`: 16 kHz mono PCM, ring buffer up to
  30 s, exposes `Stream<int>` for level (drives waveform/timer
  UI)
- Implement `_StewardOverlayMicButton` with long-press +
  drag-cancel state machine
- Replace `_ChatInput`'s send IconButton with a slot that swaps
  between mic (empty field) and send (non-empty field)
- Recording pill UI: timer, level meter, cancel hint
- Permission request flow on first long-press

### W3 — SenseVoice integration (~200 LOC)

- Inference isolate: pin sherpa-onnx instance, accept PCM via
  SendPort, return transcript
- Wire `VoiceInputService.transcribe(pcm)` → isolate call →
  transcript
- Hook into mic-button release path: success → write to
  `_ctrl.text`, focus field; failure → snackbar + restore mic
  state
- Auto-send toggle (Settings → Experimental → Voice → "Send
  immediately after transcribe", default off)

### W4 — Split APK build (~100 LOC + CI matrix)

- Add `lite` / `full` flavors to build.gradle
- `MODEL_BUNDLED` Dart-define
- Asset manifest setup for `full`
- CI release.yml matrix: 2 flavors × 2 ABIs = 4 APKs (or
  app-bundle if Play distribution becomes a path)
- Release notes template: clarifies "full" vs "lite" choice

### W5 — Lite-flavor download manager (~150 LOC)

- CDN URL config (likely the GitHub Releases asset for the
  model tarball, hosted alongside binaries)
- SHA256 verification post-download
- Resume-on-fail (HTTP Range)
- Cellular cost confirmation dialog
- Progress UI in Settings → Voice
- "Delete model" action

### W6 — Polish (~100 LOC, optional)

- Waveform visualisation in recording pill
- Haptic vocabulary: start record, drag-cancel armed, 25 s warn,
  release commit, release cancel
- Empty-state copy update on overlay chat: "or hold the mic to
  speak"
- Settings → Voice → "Test microphone" button: records 3 s,
  transcribes, shows result so users can verify accuracy without
  fighting the steward UX

## Sequencing

W1 first (gates everything). W2 + W3 in series (W3 needs W2's
PCM stream). W4 in parallel with W2/W3 — independent file. W5
after W4. W6 after W5.

Time estimate: ~5–8 days end-to-end for one engineer; W4 + W5
parallelisable cuts to ~5 days.

## Risks

- **Microphone permission UX.** Users who deny on first prompt
  hit a dead button. Need a clear re-prompt path via Settings →
  Voice + a system-settings deeplink on second denial.
- **Model load time on cold start (full flavor).** First-run
  extraction from rootBundle to docs dir is ~3–5 s; if the user
  long-presses mic before that completes, the button must visibly
  indicate "loading" (W1 covers this).
- **APK size politics for `full` flavor.** ~200 MB is a lot for
  a sideloaded APK. We mitigate via app-bundle splits + the
  `lite` flavor as the default direct-link.
- **Network policy edge cases on `lite`.** First long-press on
  data-only no-wifi: do we block? Show cost confirmation? Default
  is "wifi-only with explicit opt-in for cellular." Settle in W5.
- **Background recording / lock-screen.** v1 records only while
  the user holds the button — no background recording, no
  lock-screen capture. Eliminates a category of permission /
  notification work that's not yet earned.
- **Locale auto-detection.** SenseVoice's auto-detect is good
  but not perfect. If a user mixes zh + en in one utterance
  ("show me the insights view 给项目 X"), the model handles it,
  but accuracy may dip. Acceptable for v1; surface via the
  "Test microphone" button so users can sanity-check.
- **Hot-reload churn during dev.** sherpa-onnx isolate state
  doesn't survive Flutter hot-reload cleanly. Document the
  full-restart requirement in `docs/how-to/voice-input-dev.md`.

## Testing strategy

- **Unit-friendly slices:** `AudioCapture` PCM normalisation, the
  drag-cancel state machine, `VoiceModelLoader` state transitions,
  the `MODEL_BUNDLED` build-time switch. All testable without
  mic / model.
- **Manual rig:** "Test microphone" button (W6) is a built-in
  rig for the contributor + QA.
- **CI:** native lib presence in built APK + flavor flag
  correctness verifiable in `release.yml` post-build step.
- **No automated ASR accuracy gate.** WER measurement is its own
  infrastructure; v1 ships with manual QA on the demo phrases
  ("show me X", "open Y", "what's blocked", common zh
  equivalents).

## Out of scope (explicitly)

- iOS, streaming, VAD, voice messages, voice output (TTS),
  multi-speaker recognition, language packs beyond zh+en+ja+ko+yue
  (SenseVoice's native set), wake-word ("Hey steward"), background
  recording, lock-screen integration, accessibility features
  beyond what the OS provides for free, on-device personalisation.

Each is its own future wedge once v1's UX is validated.

## Sizing

| Workband | LOC (mobile) | Native / build | Risk |
|---|---|---|---|
| W1 model loader scaffold | ~250 | pubspec + Android NDK | low |
| W2 capture + gesture | ~200 | none | low |
| W3 SenseVoice integration | ~200 | isolate plumbing | medium |
| W4 split APK flavors | ~100 | build.gradle + CI matrix | low |
| W5 download manager | ~150 | CDN/Releases asset | low |
| W6 polish | ~100 | none | low |
| **Total v1** | **~1000** | matrix ×4 APKs | medium |

APK delta:
- `lite`: +40 MB (native libs + Flutter)
- `full`: +200 MB (above + SenseVoice-Small int8 + Silero VAD seed
  for v2 readiness)

RAM peak during transcribe: ~250–400 MB (sherpa-onnx model + audio
buffers + isolate overhead). Comfortable on any 2020+ Android phone.

Battery: brief inference burst per utterance (~150–300 ms at full
core). Negligible vs the always-on SSE stream the overlay already
runs.

## Sequencing within ADR-023

Voice input is consistent with — but doesn't depend on — ADR-023.
The IAA framework already presumes voice as the natural intent
channel; this wedge realises it on-device. If ADR-023 ratifies
the agent-driven UX as MVP-critical, voice input shifts from
"experimental" to "MVP feature" and the Settings toggle moves out
of Experimental.

## Open questions for v1 review

1. **`full` flavor distribution.** GitHub Releases caps single
   asset at 2 GB — comfortably under our 200 MB. But the typical
   sideload UX expects <100 MB. Do we ship `full` only on
   request, or as a normal release artefact?
2. **Auto-send default.** v1 lands transcript in the field for
   review. Once accuracy is validated, do we flip auto-send to
   default on (faster) or default off (safer)?
3. **30 s soft cap UX.** Is the haptic + colour shift at :25
   sufficient warning, or do we need an audible chime?
4. **Voice toggle visibility.** Currently planned under Settings →
   Experimental. Once stable, does it move to a dedicated "Voice
   & accessibility" section?

These are settle-during-spike, not blockers.
