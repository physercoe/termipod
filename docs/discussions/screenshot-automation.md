# Screenshot automation — auto-generated Flutter screenshots in CI

> **Type:** discussion
> **Status:** Open (post-demo; revisit when README screenshots need refresh after Candidate-A hardware demo lands)
> **Audience:** contributors
> **Last verified vs code:** v1.0.319

**TL;DR.** README screenshots drift the moment the IA changes. We
just hit this — current screens are tmux-era, pre-IA-redesign. CI
can generate them automatically using `integration_test` +
`binding.takeScreenshot()` against a `seed-demo` hub. ~1–2 days to
build the harness, ~10 min per CI run. Defer until post-demo
because the surface is still in flight; capturing against a moving
target wastes the work. Captured here as the design, ready to
execute when the trigger fires.

---

## 1. The drift problem this solves

README screenshots are a maintenance liability:
- Last refresh predates IA redesign (v1.0.175–v1.0.182) and steward
  chat work (v1.0.281+).
- Manual refresh = boot the app, navigate to each screen, screenshot,
  crop, light/dark variants — easily a half-day of work each time.
- Without automation, the README banner drifts toward "outdated"
  every IA polish wedge.

A CI-generated approach makes refresh a build artifact, not a
manual chore.

---

## 2. The two flavors of Flutter screenshots

| Flavor | Tool | Use case | CI cost |
|---|---|---|---|
| **Golden tests** | `flutter test --update-goldens` | Pixel-by-pixel widget regression testing ("AgentFeed renders identically across PRs") | Free — runs headless, no emulator |
| **Integration screenshots** | `integration_test` + `binding.takeScreenshot()` | Full-screen captures of a real driven app — README quality, marketing-quality | Needs Android emulator (Linux runner, free) or iOS simulator (macOS runner, paid minutes) |

For README/marketing screenshots: flavor 2.
For regression testing: flavor 1 — out of scope here, separate concern.

---

## 3. Proposed harness shape

A `make screenshots` target that:

### 3a. Test setup

```dart
// integration_test/screenshot_test.dart
import 'package:integration_test/integration_test.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  binding.framePolicy = LiveTestWidgetsFlutterBindingFramePolicy.fullyLive;

  testWidgets('projects tab — dark', (tester) async {
    await _bootSeededApp(tester, themeMode: ThemeMode.dark);
    await tester.tap(find.byKey(const Key('nav.projects')));
    await tester.pumpAndSettle();
    await binding.takeScreenshot('projects_dark');
  });

  testWidgets('me tab — dark', ...);
  testWidgets('activity tab — dark', ...);
  // ...one per (tab × theme)
}
```

### 3b. App fixture

The screenshot test boots the real app, but with two overrides:
- `HubClient` points at a `seed-demo`-loaded hub (started by the
  Make target before the test).
- Auth token comes from a fixture file (gitignored).

This way the screenshots show real screens with rich data — no
empty-state placeholders, no skeleton loaders.

### 3c. Make target

```makefile
.PHONY: screenshots
screenshots:
	# 1. Start ephemeral hub with seed data
	cd hub && go run ./cmd/hub-server init --data .screenshots-data
	cd hub && go run ./cmd/hub-server seed-demo --data .screenshots-data
	cd hub && go run ./cmd/hub-server serve --data .screenshots-data --listen 127.0.0.1:5555 &
	# 2. Run the integration test (boots Android emulator if needed)
	flutter test integration_test/screenshot_test.dart \
	  --dart-define=HUB_URL=http://10.0.2.2:5555 \
	  --dart-define=HUB_TOKEN=$(SCREENSHOT_TOKEN)
	# 3. Move captured PNGs into docs/screens/
	mv build/integration_test/screenshots/*.png docs/screens/
	# 4. Tear down hub
	rm -rf hub/.screenshots-data
```

### 3d. CI workflow

```yaml
# .github/workflows/screenshots.yml
name: Refresh screenshots

on:
  workflow_dispatch:        # manual trigger from GitHub UI
  push:
    tags: ['v*']            # auto-refresh on each release tag

jobs:
  android:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: subosito/flutter-action@v2
      - uses: actions/setup-go@v5
      - uses: reactivecircus/android-emulator-runner@v2
        with:
          api-level: 34
          script: make screenshots
      - name: Commit refreshed screenshots
        run: |
          git config user.email "actions@github.com"
          git config user.name "screenshot-bot"
          git add docs/screens/
          git diff --cached --quiet || git commit -m "docs: refresh screenshots from CI"
          git push
```

iOS variant runs on `macos-latest` with `xcrun simctl` driving the
simulator — same shape, paid minutes.

---

## 4. Tradeoffs

### Pros
- **Screenshots stop drifting.** Each release tag refreshes them
  automatically; readers see the current app.
- **Integration smoke test for free.** The harness IS an
  integration test — every refresh proves "the app boots, connects
  to a seeded hub, navigates to every tab without crashing."
- **Reproducible locally.** Anyone with Flutter + the Android
  emulator can `make screenshots` and verify.
- **Dark + light variants for free.** Theme toggle mid-test.
- **Multi-device for free.** Run the same test on different emulator
  configurations (phone, tablet, foldable) — useful for the
  adaptive-layout claim in README.

### Cons
- **1–2 days to build** the harness, fixture, Make target, CI workflow,
  commit-back logic.
- **Maintenance.** When widget structure changes, test selectors
  (`find.byKey`) break. Tests need updating alongside UI changes.
- **Setup brittleness.** Emulator boot is flaky on shared CI; font
  loading + locale + status bar size all matter for consistent
  output.
- **Marketing trade-off.** Auto-generated screenshots may look
  generic — human curation picks the *right* state to show. The
  fix: seed-demo carries a curated state (it already does for the
  demo), and the test drives to the most-impressive views.
- **iOS minutes cost** if we want both platforms. Probably skip iOS
  initially — README only needs one set.

---

## 5. Recommendation

**Defer until post-demo, then ship as a 1-day wedge.**

Triggers:
- Post-Candidate-A hardware demo lands → screenshots need refresh
  anyway → at that point, automating saves work going forward.
- IA stabilizes (no major polish wedges in flight) → the test
  selectors won't churn.

When the trigger fires, the work breaks into:
1. Add `integration_test/screenshot_test.dart` — drive the app
   through every README-listed surface (~½ day).
2. Add `Makefile` `screenshots` target + GitHub Actions workflow
   (~¼ day).
3. Run it once, commit the new screenshots, update README captions
   (~¼ day).

Future refreshes are then `gh workflow run screenshots` or
automatic on tag push.

---

## 6. Decision criteria — when to revisit

- **README banner gets re-updated** ("outdated" pointer breaks down
  because someone wants to share the README) → forcing function.
- **Hardware demo lands** → natural refresh point; shipping
  automation now saves the next refresh.
- **External users land** → screenshots become a credibility signal,
  worth investing in.

Until one of those fires, the markdown banner ("Outdated, refresh
post-demo") is sufficient signal to readers and the work stays
deferred.

---

## 7. Related

- `../decisions/001-locked-candidate-a.md` — demo target gates the
  refresh trigger
- `../plans/research-demo-gaps.md` — `seed-demo` + `mock-trainer`
  are the same fixtures the screenshot harness would use
- `simple-vs-advanced-mode.md` — IA stability question; if we
  pivot to a 3-tab layout post-demo, screenshots refresh anyway
- README banner: `../../README.md` Screenshots section
