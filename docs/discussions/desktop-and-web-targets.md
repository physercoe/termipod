# Desktop and web targets — gap analysis

> **Type:** discussion
> **Status:** Open — deferred, no work planned
> **Audience:** contributors evaluating cross-platform scope
> **Last verified vs code:** v1.0.721

**TL;DR.** Flutter supports Linux/macOS/Windows/web as first-class
targets. TermiPod has all six platform shells scaffolded but the
runtime code, dependencies, and CI are mobile-only. This doc
inventories what would change. **No work is planned today;** this
file exists so the next person who asks doesn't redo the survey.

---

## Premise

Flutter is a multi-platform UI toolkit. One Dart codebase compiles to:

| Target | Renderer | Maturity |
|---|---|---|
| Android, iOS | Impeller / Skia | Stable, primary |
| Linux, macOS, Windows | GTK / Cocoa / Win32 + Skia | Stable since 3.x |
| Web | CanvasKit (WASM) or HTML | Stable, with caveats |

The widget tree and Dart code are identical across targets. The fork
happens at **plugins** (which expose native APIs) and at the
**`dart:io` / `dart:html` boundary**.

## State of the repo

Scaffolded but inactive:

- All six platform shells exist: `android/`, `ios/`, `linux/`,
  `macos/`, `web/`, `windows/`.
- `pubspec.yaml` does not platform-restrict the package.
- CI builds **APK** (`.github/workflows/release.yml`) and **iOS IPA**
  (`.github/workflows/release-ios.yml`). No desktop or web build job.

Mobile-only assumptions in code:

- `lib/main.dart:2` — `import 'dart:io' show Platform;` and
  `Platform.isAndroid` branches at boot.
- `lib/services/public_file_store.dart` — `Platform.isAndroid` /
  `Platform.isIOS` branches for MediaStore vs `UIFileSharingEnabled`.
- `lib/services/update_service.dart:3` — `dart:io` `HttpClient`.

`dart:io` is unavailable on web; any of these paths reached at runtime
on web would crash at load.

## The four kinds of work

### 1. Plugins that need replacement, a guard, or a swap

Each line below is a `pubspec.yaml` dependency and its
desktop/web posture. The ones marked **mobile-only** would block a
build on the missing target unless guarded by `kIsWeb` /
`Platform.isLinux` etc., or behind a conditional-import shim.

| Plugin | Notes |
|---|---|
| `dartssh2` | Pure Dart but uses `dart:io` `Socket`. Works on desktop. **Web: no raw TCP** — would need a WebSocket-to-SSH proxy on the hub (or accept that SSH/terminal screens are absent on web, which is fine given those are the breakglass layer per `spine/blueprint.md`). |
| `flutter_secure_storage` | All platforms; web uses IndexedDB. **Web storage is not actually secure** — re-evaluate key handling before shipping web. |
| `local_auth` | Mobile + macOS only. Hide biometric prompts elsewhere. |
| `flutter_foreground_task` | Android/iOS only. Replace with a Timer on desktop/web; foreground promotion isn't needed there. |
| `flutter_local_notifications` | All platforms; web limited to Push API. Functional but cosmetically different. |
| `media_store_plus` | **Android-only by design.** Desktop/web use save dialogs via `file_picker`. |
| `flutter_image_compress` | Mobile only. Replace with `package:image` on desktop/web. |
| `webview_flutter` | Mobile + macOS. **No Linux/Windows; web is iframe-only.** Disable canvas-app artifact viewing on the missing targets. |
| `pdfrx` | Native + web (WASM PDFium). OK on desktop. |
| `record` | Cross-platform; web uses MediaRecorder. Check codec parity. |
| `just_audio`, `video_player` | Cross-platform. OK. |
| `share_plus` | Cross-platform; web uses Web Share API where available. OK. |
| `image_picker`, `file_picker`, `wakelock_plus`, `connectivity_plus`, `url_launcher`, `web_socket_channel` | Cross-platform. OK. |

Rough scope: **~4 plugins** need replacement or a build-time
exclusion, **~6 more** need feature-gates so the UI hides the
affordance on targets that can't do it.

### 2. `dart:io` and `Platform.*` branches

Hot spots already identified:

- `lib/main.dart:2,32` — Android-specific boot path.
- `lib/services/public_file_store.dart` — Android/iOS branches.
- `lib/services/update_service.dart:3` — `HttpClient` (not on web).

Refactor pattern:

```dart
// runtime_io.dart  (uses dart:io)
// runtime_web.dart (stub)
import 'runtime_io.dart' if (dart.library.html) 'runtime_web.dart';
```

Or guard with `if (kIsWeb) … else if (Platform.isLinux) …` at call
sites where the branch is small.

### 3. CI plumbing

Add jobs to `.github/workflows/`:

```yaml
flutter build linux       # ubuntu-latest
flutter build macos       # macos-latest
flutter build windows     # windows-latest
flutter build web         # any runner; produces build/web/
```

Web also needs a host (GitHub Pages / object store / served by the
hub at e.g. `/app`).

### 4. UI density

Mobile IA (`spine/information-architecture.md`) is five bottom tabs
on a phone canvas. On a 1440px desktop screen that's a strip of tabs
on an otherwise-empty canvas.

Two options:

- **Adaptive layout** — `LayoutBuilder` + master-detail split for
  wide canvases. Riverpod state is already centralised, so the
  providers don't change — only the widget tree.
- **Thin web wrapper** — render the mobile width centred in a ~400px
  column. Quick; doesn't feel native on a laptop.

## Suggested order if this work ever happens

1. **Pick one target.** Web is highest-value-per-effort for a
   director's cockpit (no app store, instant URL). Desktop is easier
   than web (more plugins work out-of-the-box) but ships as a binary
   again. Director-on-laptop suggests **web first**.
2. **Add a CI build job** for the chosen target. Watch what breaks.
3. **Plugin-by-plugin fix the build** — gate, replace, or stub.
4. **Refactor `dart:io` call sites** behind the conditional-import
   shim.
5. **Adaptive layout pass** once basics work.

Rough sizing:

- **Web** — ~1–2 weeks to a usable build, mostly plugin swaps and
  the SSH-over-WebSocket question for `dartssh2`.
- **Linux / macOS / Windows desktop** — ~3–5 days each once one of
  them is wired up, mostly biometric/foreground gating + CI.

## Why deferred

The mobile-first cockpit is still the load-bearing surface. Hub
features, agent governance, and engine parity have priority. This
doc is a placeholder to prevent re-surveying the gap from scratch
the next time the question comes up.

## Related

- [`desktop-research-surface.md`](desktop-research-surface.md) — the
  *product/role* companion to this mechanical gap inventory: what work
  the desktop app serves (reading, authoring, debugging, graph-thinking,
  run-comparison) and how the delivery model follows from it. It
  challenges this doc's "one adaptive Flutter tree" lean.
- `spine/blueprint.md` — establishes mobile-first as the principal
  surface, with hub-TUI as an alternate cockpit.
- `spine/information-architecture.md` — the IA that would need an
  adaptive layout pass.
- The hub already exposes hub-TUI for the desktop case
  (`hub-tui/`); not Flutter, but covers the desktop-from-terminal
  niche.
