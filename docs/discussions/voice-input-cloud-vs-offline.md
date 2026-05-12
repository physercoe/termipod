# Voice input — cloud STT vs offline model vs system IME

> **Type:** discussion
> **Status:** Open (Drafted 2026-05-11) — re-opens the design space
> that [`plans/voice-input-overlay-v1.md`](../plans/voice-input-overlay-v1.md)
> picked. No implementation commitment; this captures the
> comparative analysis so the next prioritisation has it ready.
> **Audience:** contributors · principal
> **Last verified vs code:** v1.0.507

**TL;DR.** Voice input on the steward overlay has three paths past
"just use the keyboard's mic button" (which
[ADR-023 D3](../decisions/023-agent-driven-mobile-ui.md) made the
default, zero-cost answer). The shipping path the *deferred* plan
picked is **offline SenseVoice-Small via sherpa_onnx** (~1000 LOC,
~85–200 MB APK cost). The principal's question is: **would a
commercial cloud API be much easier?** Answer: **yes, much easier —
roughly 1/4 the LOC, zero APK bloat, zero model loader, no APK split,
no native library bring-up.** But the **truly easiest non-zero path**
is the system-native `speech_to_text` Flutter package (system OS STT,
not a third-party API) at ~150 LOC + zero ongoing cost. The right
sequencing if voice gets prioritised again is **system-native first,
mobile-direct cloud as a power-user option, offline only if a real
privacy / offline requirement emerges**.

**The hub stays out of the audio path entirely.** For cloud STT, the
mobile calls the vendor's API directly and only the resulting
transcript (text) reaches the hub — identical to typed input. The
hub never touches audio bytes. This corrects an earlier framing that
proposed hub-proxied audio; that variant added a hop, doubled
bandwidth, and bought nothing the simpler shape doesn't already give.

The existing
[`plans/voice-input-overlay-v1.md`](../plans/voice-input-overlay-v1.md)
deferred SenseVoice plan should be either rewritten around system-
native + cloud, or kept frozen and superseded by a new plan if voice
re-enters scope.

---

## 1. Frame

The principal asked: offline voice models are post-MVP, but if we use
*online commercial APIs* (Whisper, Deepgram, Google Cloud STT, etc.),
would it be much easier to implement?

Yes — and reading further, the answer changes shape:

1. **There is a path simpler than commercial APIs**: system-native STT
   exposed by iOS / Android via a thin Flutter plugin. No API keys, no
   network handling on our side, no per-minute cost.
2. **There is a path simpler still**: the existing
   [ADR-023 D3](../decisions/023-agent-driven-mobile-ui.md) "voice via
   system IME" stance — zero code on our side; the user taps the
   keyboard's mic button. This is what's "shipping" today, by
   omission.
3. **The deferred plan picked offline** (SenseVoice via sherpa_onnx)
   based on privacy + cost + bilingual quality + offline resilience.
   None of those reasons are wrong; they are *premium* properties
   that the demo arc doesn't yet require.

So the real comparison is among four paths, not two.

---

## 2. Existing state to honour

Before deciding anything new, recall what's already on the record:

- **ADR-023 D3** locks "voice via system IME" as the canonical
  text-entry path. Any custom voice surface must compose with this,
  not replace it.
- **`plans/voice-input-overlay-v1.md`** is *Deferred*, gated on the
  steward lifecycle walkthrough being done-criteria green. The plan
  picked SenseVoice-Small + sherpa_onnx + APK split + lite-flavor
  download manager. It is the most complete write-up in this space.
- **No mobile code today touches the microphone.** No
  `permission_handler` mic flow, no `record` / `speech_to_text`
  package, no microphone permission in `AndroidManifest.xml` /
  `Info.plist`. `just_audio` + `video_player` are for *playback only*
  (audio/video artifact viewers, v1.0.497).
- **[`discussions/agent-driven-mobile-ui.md`](agent-driven-mobile-ui.md)
  §4.2** argues affirmatively for deferring to system IME — "the
  user's insight here saves a wedge of work." That doc and the
  deferred plan disagree on framing; this discussion is the place to
  reconcile.

---

## 3. The four paths compared

### Path A — System IME only (status quo per ADR-023 D3)

The user taps the keyboard's mic button (Gboard, iOS dictation,
Samsung Keyboard). Transcript lands in our text field via the IME's
`commitText` callback. **Zero code on our side.** No permissions, no
plugins, no manifest entries. This is what termipod does today.

| Axis | Score |
|---|---|
| LOC | 0 |
| APK cost | 0 |
| Privacy | Mediated by Apple / Google / Samsung — the user already trusts them |
| Cost | $0 |
| Latency | 100–300 ms (system STT) |
| Quality | Depends on user's keyboard; Gboard + iOS dictation are excellent |
| Offline | No (system STT usually cloud-backed for long form) |
| Discoverability | Low — users may not know the mic is there |

**The killer downside:** discoverability. A user new to the overlay
puck doesn't see a mic affordance *inside termipod*. They have to
notice the keyboard's tiny mic glyph. The principal's framing —
"voice input" — implies a feature the user can find in the app, not
one they have to know to invoke from their keyboard.

### Path B — System-native STT (Flutter plugin)

`speech_to_text` (or similar) Flutter package wraps iOS
`SFSpeechRecognizer` + Android `SpeechRecognizer`. Mic button in the
overlay chat input; tap to record, get partial transcripts streaming,
release to commit. No third-party API, no API key. Audio handled by
the OS; transcript text comes back.

| Axis | Score |
|---|---|
| LOC | ~150 (mic button + recognizer wrapper + permission handling) |
| APK cost | <1 MB (plugin shim) |
| Privacy | OS routes audio to its STT — Apple's is largely on-device for short utterances; Google's is cloud-backed by default |
| Cost | $0 |
| Latency | 100–300 ms |
| Quality | iOS: excellent. Android: highly variable by OEM, generally good. |
| Offline | iOS short utterances: yes; long-form & Android: no |
| Discoverability | High — explicit mic button next to send |

**Implementation shape:**
- Add `speech_to_text: ^7.x` to pubspec
- Add `RECORD_AUDIO` to AndroidManifest + `NSMicrophoneUsageDescription` + `NSSpeechRecognitionUsageDescription` to Info.plist
- New widget `lib/widgets/steward_overlay/steward_overlay_mic_button.dart` replacing the send button when the field is empty (mirroring the deferred plan's UX)
- Reuse the `permission_handler` package (already vendored)
- Settings → Experimental → Voice Input toggle (mirroring the deferred plan)

**Why this beats commercial APIs for a v1:** no API key management,
no network cost, no hub plumbing, no per-utterance billing, fully
self-contained.

### Path C — Commercial cloud STT (Whisper / Deepgram / Google / Azure)

Record audio locally, POST to a chosen vendor's REST endpoint, get a
transcript back. **The hub stays out of the audio path entirely** —
only the resulting transcript text reaches the hub, identical to
typed input. The mobile calls the vendor directly with a
user-supplied API key, stored in `flutter_secure_storage` the same
way SSH keys and engine credentials already are.

| Axis | Score |
|---|---|
| LOC | ~250 (mic button + gesture + vendor REST client + settings entry) |
| APK cost | <1 MB |
| Privacy | Audio leaves the device for the user's chosen vendor; the hub never sees audio bytes |
| Cost | Whisper $0.006/min; Deepgram $0.0043/min real-time. At 10 utterances × 10 s/day × 30 days ≈ 50 min/month ≈ $0.30/user/month |
| Latency | 300–600 ms (mobile → vendor → mobile, no hub hop) |
| Quality | Whisper / Deepgram nova-2: state-of-the-art, multilingual, robust to accents |
| Offline | No |
| Discoverability | High |

#### Why the hub stays out

For mobile-originated audio, the natural pattern is:

```
mic → record audio locally → POST audio to vendor STT → transcript
    → drop transcript into chat field → user reviews + taps send
    → hub receives chat message exactly as if typed
```

The hub never touches audio. The hub's "credential mediation"
pattern (e.g. engines authed via host-runner, not via mobile) applies
to *host-side* third-party access — agents running on the host-
runner shouldn't have to ship secrets to mobile. STT is a
*mobile-side* third-party call, and the secret already has to be on
the device that's making the call. Sending audio through the hub
just to put the key on the hub instead of the mobile adds a hop,
doubles bandwidth on already-slow connections, and buys nothing —
the vendor still sees the audio either way.

#### Key management

The vendor API key lives in mobile's `flutter_secure_storage`. Two
ways to populate it:

- **C-direct** — User pastes their personal vendor API key in
  Settings → Voice. Mobile uses it for STT calls. This is the
  default and simplest path. The trust model matches "user gives a
  third-party app an API key" — no different from how Whisper /
  Deepgram first-party clients work.
- **C-distributed** (optional enhancement) — Hub stores the team's
  vendor key in a team-level settings row; mobile fetches it once at
  startup via an authed `GET /v1/teams/{team}/voice/credentials`
  endpoint and caches it in secure storage. Useful if a team wants
  one shared key across multiple devices. Audio still never goes
  through the hub. ~50 extra LOC if added later.

#### Why this is much easier than the deferred offline plan

- No native library packaging (sherpa-onnx prebuilt + arm64/x86_64 ABI matrix)
- No ~85 MB model file in the APK / no lite-flavor download manager
- No model loader state machine (`notLoaded → downloading → loading → ready → failed`)
- No background isolate for inference
- No APK split build + CI matrix

The deferred plan's W1 (native libs + model loader), W4 (split APK),
and W5 (download manager) — ~500 LOC of the 1000 LOC budget —
**simply don't exist** in the cloud variant. W2 (audio capture +
gesture) and W3 (transcribe call) also shrink because the REST call
is a single `package:http` POST, not a `package:sherpa_onnx`
initialised inference session.

### Path D — Offline model (the deferred SenseVoice plan)

The status-quo proposal in
[`plans/voice-input-overlay-v1.md`](../plans/voice-input-overlay-v1.md):
SenseVoice-Small via sherpa_onnx. Best privacy + offline + bilingual
story; highest implementation cost.

| Axis | Score |
|---|---|
| LOC | ~1000 across 6 wedges |
| APK cost | Full flavor: ~200 MB; lite flavor: ~40 MB install + 85 MB download |
| Privacy | Audio never leaves the device |
| Cost | $0 |
| Latency | ~70 ms inference for 10 s audio (per published benchmarks) — fastest of the four |
| Quality | Excellent for bilingual zh+en utterances; weaker than Whisper for long-form / accents |
| Offline | Yes |
| Discoverability | High |

The plan's strengths are real (privacy, latency, cost-at-scale,
offline). The plan's cost is *also* real: a 200 MB APK is a
non-trivial install-time hit, and the model loader state machine adds
boot-time complexity that affects *every* user even when they never
use voice.

---

## 4. Comparing them honestly

### 4.1 Difficulty ranking

```
easiest                                       hardest
  │                                               │
  ▼                                               ▼
  A          B                  C             D
  (IME)     (system-native)    (cloud,        (offline)
                                mobile→vendor)
   0 LOC    ~150 LOC            ~250 LOC      ~1000 LOC
```

The principal's intuition that "online would be much easier than
offline" is correct — but the gap between *cloud API* and *system-
native* is also large, and in the easier direction. Cloud APIs are
*much easier* than offline. System-native is *somewhat easier still*
than cloud, and free.

### 4.2 Quality ranking (subjective, for short utterances)

```
worst                                                     best
  │                                                          │
  ▼                                                          ▼
  Android   iOS         Android      OpenAI Whisper-1,    SenseVoice
  default   SF Speech   Gboard       Deepgram nova-2     (zh+en)
                                     Google Cloud STT
```

For English-only steward dictation, all four paths are good enough.
For bilingual zh+en (the demo's principal language pair), commercial
APIs and SenseVoice both excel; system-native is variable.

### 4.3 What the deferred plan got right

- **Bilingual support.** SenseVoice's zh+en in one model is
  qualitatively better than what Gboard or iOS dictation give you
  when the user code-switches mid-utterance.
- **Latency.** 70 ms beats every cloud API by a factor of 5–10.
- **Cost at scale.** $0/min if voice usage grows.
- **Privacy.** Audio stays on-device.
- **Offline resilience.** Critical for the "mobile dev tools" framing
  where the user is on commute / bad Wi-Fi.

### 4.4 What the deferred plan over-priced

- **First-time install pain.** A 200 MB APK is a tester-friction
  problem when nobody has used voice yet to validate it's worth that
  size.
- **Boot-time complexity.** The model loader state machine touches
  app startup. Every cold launch pays for it.
- **Maintenance.** Native library bring-up (sherpa_onnx_android, ABI
  matrix, version bumps) is a recurring engineering tax.
- **CI matrix.** Split APK build doubles the release pipeline.

---

## 5. Recommendation if voice gets prioritised again

### 5.1 Sequence (independent, each shippable on its own)

1. **Path A — already shipping.** Update docs (
   [`how-to/test-agent-driven-prototype.md`](../how-to/test-agent-driven-prototype.md)
   already says "Voice via system IME only" — keep it; this is the
   default). No new work; baseline.
2. **Path B — system-native STT.** ~150 LOC, ~1 wedge. Adds a mic
   button to the overlay chat input for discoverability. Free, fast,
   no API keys.
3. **Path C — mobile-direct cloud STT** (post-Path B). ~250 LOC.
   Mobile calls Whisper / Deepgram / Google / Azure directly with a
   user-supplied API key in `flutter_secure_storage`. The hub never
   touches audio — only the resulting transcript reaches it, exactly
   as if typed. Optionally add hub-distributed credentials later
   (~50 extra LOC) if a team wants one shared key across devices.
4. **Path D — offline.** The deferred plan's actual content. Defer
   *further* until either testers explicitly ask for offline /
   privacy / bilingual-mid-utterance, or zh+en code-switch dictation
   becomes load-bearing.

This is the inverse of the deferred plan's choice — but the deferred
plan was written when the steward lifecycle walkthrough wasn't yet
green and the team was pre-committing on a privacy/quality stance
that hadn't been pressure-tested. Now the lifecycle walkthrough is
done (v1.0.484+), so voice can be revisited from a position of
"prove the simplest version works first."

### 5.2 What "ship Path B" actually looks like

A single wedge, mirroring the deferred plan's UX but with system-
native plumbing:

| Wedge | Content | LOC |
|---|---|---|
| Mobile: mic button + permission flow | `speech_to_text` integration; mic FAB replaces send when field is empty; tap-to-record / drag-to-cancel | ~120 |
| Settings | "Voice input" toggle under Experimental | ~20 |
| Tests | mock recognizer; gesture coverage | ~30 |

Total: ~170 LOC. No hub changes. No APK bloat. No model loader. No
permissions ceremony beyond what `permission_handler` already
handles.

This is the **honest "much easier" answer to the principal's
question**: cloud APIs are easier than offline, but system-native is
*easier still*. The right v1 is Path B; cloud (Path C) is the
post-MVP power-user upgrade.

### 5.3 When cloud (Path C) actually pays off

- The user wants better-than-system quality on Android (where OEMs
  ship inconsistent STT) → Path C with Whisper
- The user is doing long-form dictation (memo bodies, briefings) →
  Path C with Whisper or Deepgram (system STT degrades past ~60 s)
- Bilingual zh+en code-switching is load-bearing → Path C with
  Whisper (or Path D with SenseVoice)
- Compliance requires a specific provider → Path C with that vendor

None of these are MVP demo blockers. All are legitimate post-MVP
upgrades. None require the hub to be in the audio path.

---

## 6. Reconciling with ADR-023 D3 + the deferred plan

This discussion's recommendation conflicts with the deferred plan
but **does not violate ADR-023 D3**:

- ADR-023 D3 says "voice via system IME" — Path A. The discussion
  recommends *adding* Path B as a discoverable affordance, with the
  system IME's mic button still available as the underlying
  mechanism. Path B *complements* D3, not replaces.
- The deferred plan picked Path D. If voice re-enters scope, the
  deferred plan should be **rewritten** (or marked Superseded) in
  favour of a new plan implementing Path B, with Path D as
  the explicit post-Path-B option for the "offline / bilingual /
  scale-to-many-users" wave.

Either:

- **Update** `plans/voice-input-overlay-v1.md` to add a "2026-05-11
  re-eval" block pointing at this discussion, and reframe its body
  as a Path D plan whose prerequisite is "Path B has shipped and
  testers want more."
- **Supersede** it with a new
  `plans/voice-input-overlay-v2-system-native.md` per Path B; mark
  the v1 plan Archived; keep its body for reference.

This is a doc-spec question, not a code question. The discussion
defers that choice to whoever actually picks voice up next.

---

## 7. Open questions

- **OQ-1.** Is bilingual zh+en code-switching load-bearing for the
  research demo today? The deferred plan implies yes (it's why
  SenseVoice was chosen). If the demo runs in English-only, Path B's
  iOS dictation is fine; if zh+en is core, Whisper (Path C) becomes
  the right v1, not Path B. **Lean:** ask the principal —
  English-only is much cheaper.
- **OQ-2.** Should the mic button live on the overlay puck chat input
  only, or also on the full-session chat input (`session/details`)?
  Both are text-field surfaces. Adding to both = same widget reused.
- **OQ-3.** Push-to-talk vs tap-to-toggle? The deferred plan picked
  push-to-talk (long-press, drag-to-cancel). This is the right UX
  pattern; preserve regardless of path. iOS dictation has a built-in
  affordance for "tap to start, tap again to stop" — match the
  platform on iOS, push-to-talk on Android for VAD-free simplicity.
- **OQ-4.** TTS read-back for steward replies? Out of scope for this
  discussion. Probably defer further — the user is reading on the
  screen.
- **OQ-5.** For Path C, where does the vendor API key live?
  **Default:** user pastes it in mobile Settings → Voice; mobile
  stores it in `flutter_secure_storage` and calls the vendor
  directly. No hub involvement.
  **Optional later:** hub-distributed credentials via authed
  `GET /v1/teams/{team}/voice/credentials` so multi-device teams
  share one key. Audio still goes mobile → vendor; the hub only
  hands out the secret, never proxies the audio.
- **OQ-6.** Local cache of recent transcripts for "replay last
  utterance" — useful or feature creep? **Lean:** feature creep until
  testers ask.

---

## 8. Verdict

To the principal's direct question: **yes, online commercial APIs
are much easier to implement than the offline plan — roughly 1/4 the
LOC, zero APK bloat, no model loader, no native libraries, no APK
split.** The cost is per-minute billing and audio leaving the device
for the user's chosen vendor.

But the **truly easiest non-zero path** is system-native STT (iOS /
Android system APIs via `speech_to_text` Flutter plugin) at ~150
LOC, $0 ongoing cost, zero API keys.

The recommended sequencing if voice is prioritised again is:

```
Path A (status quo) → Path B (system-native, ~150 LOC, MVP-adjacent)
                    → Path C (cloud, mobile-direct, ~250 LOC, post-MVP)
                    → Path D (offline, ~1000 LOC + 85-200 MB, only if
                              a real privacy/offline/bilingual
                              requirement emerges)
```

**For every cloud path, the hub stays out of the audio path** —
mobile calls the vendor directly, only the resulting transcript text
reaches the hub. The "hub-mediated audio" variant earlier in this
discussion's draft was wrong: it doubled bandwidth, added a hop, and
bought nothing the simpler shape doesn't already give.

The deferred SenseVoice plan should be either revised to live at the
Path D rung of this ladder, or marked Superseded by a future Path B
plan. Either way: no code commitment from this discussion. The doc
captures the comparison so the next time voice comes up, the
analysis isn't rebuilt from scratch.
