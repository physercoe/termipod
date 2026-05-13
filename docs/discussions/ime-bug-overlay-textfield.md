# IME state desync in the steward overlay TextField — debugging journey

> **Type:** discussion
> **Status:** Open (2026-05-13) — workaround shipped (v1.0.561+.562+.563); engine-level root cause unverified, see §6
> **Audience:** contributors · future debuggers
> **Last verified vs code:** v1.0.563-alpha

**TL;DR.** A TextField mounted in `MaterialApp.builder` (outside the
Navigator) has its IME-side state cache desync from Flutter's
`TextEditingController` after user/programmatic edits. Symptoms:
cursor moves ignored by the IME, deleted text restored on next
keystroke, programmatic writes (voice) wiped. The same TextField
config works correctly when mounted inside a Navigator route (Scaffold
body). Root cause sits in the InputMethodManager ↔ Flutter
InputConnection state-sync pipeline; the exact engine code path
remains unverified. The shipped workaround forces InputConnection
re-creation by bouncing focus through a hidden ghost TextField on each
editing-action boundary. This document records the full debugging
journey (8+ wrong theories across v1.0.466–v1.0.560) so a future
engineer can pick up the investigation with full context, avoid
repeating dead ends, and decide whether to upgrade the workaround to a
structural fix.

---

## 1. The bug

Symptoms reported by the tester across v1.0.466 through v1.0.560:

- Type "hello" in the steward-overlay TextField, tap the cursor
  between "h" and "e", type "x" → field shows "hellox" (deleted-and-
  appended) instead of "hxello" (inserted at tap position).
- Backspace some characters, type a new char → deleted text reappears
  and the new char is appended at end.
- Dictate via Mode B voice → voice transcript lands in field → type
  any IME character → voice text vanishes, only the new char remains.
- Multiple IMEs confirmed: WeChat input method, Gboard, system
  defaults. Both CJK and English. Symptoms vary slightly per IME
  (cursor jump always present; delete-restore varies) but root pattern
  is consistent.

Tester's diagnostic clue, the one that broke the investigation open:

> *"if there are two compose boxes — one overlay one normal compose
> under the overlay at the same screen — the overlay is OK (insert at
> any position, delete text) if I first switch (tap) to the normal
> compose box then switch back to the overlay box. Every edit (cursor
> move in one direction) about previously-input text needs tap the
> normal compose box for 'approval' if user wants to relocate cursor
> or change direction to begin another edit in the overlay box."*

That workaround is the smoking gun: the bug is fixed for one editing
operation by switching focus to a different TextField. The fix
mechanism is **forcing InputConnection re-creation**, which resets the
IME's state cache.

## 2. Why the same TextField works in session compose

Side-by-side comparison:

| Aspect | Working `agent_compose.dart` | Broken steward overlay |
|---|---|---|
| Mount point | Inside Scaffold body inside Navigator route | Inside Stack inside `MaterialApp.builder` |
| TextField widget config | Bare — no defensive IME flags, no special decoration | (was) defensive flags layered v1.0.551/552; stripped in v1.0.558 |
| FocusNode | Plain `FocusNode()` | Plain `FocusNode()` |
| Controller | Plain `TextEditingController()` | Plain `TextEditingController()` |
| TextInputType | `multiline` | `multiline` |
| IME experience | All edits work normally | IME cache desync as above |

Identical widget config, identical Flutter, identical Android
InputMethodManager. The only structural difference is the **mount
point**: route-mounted vs. MaterialApp.builder-mounted.

The most likely mechanistic explanation (unverified but consistent
with all observed evidence): route-mounted TextFields get their
`TextInput.attach` re-fired by Navigator/FocusScope lifecycle events
that the overlay never receives. Each fresh `attach` creates a new
`InputConnection` on the Android side, which forces the IME to drop
its cache and re-read state. The overlay's InputConnection persists
indefinitely without a refresh, and the IME's cache accumulates
desync over the lifetime of that connection.

See §6 below for the exact mechanism candidates that would need
verification to confirm this.

## 3. Theories tried and disproven

In chronological order. Each version below is real and shipped to the
tester; each was a fresh hypothesis that turned out wrong. The point
of preserving this list is to save future debuggers from repeating
these dead ends.

### v1.0.466–v1.0.472 — SSE rebuild scope isolation

**Theory.** SSE events tick a Riverpod provider; ancestor `ref.watch`
causes the TextField subtree to rebuild on every event; rebuilds
bounce the IME's predictive cache.

**Fix attempted.** Move messages region into a sibling Consumer; keep
the input subtree off the SSE rebuild path. (`_ChatInputSlot`
pattern.)

**Why it was wrong.** Necessary scaffolding for many other purposes,
but **not sufficient**. The IME bug persisted after this isolation.

### v1.0.480 — CJK IME flag fix

**Theory.** `enableSuggestions: false` breaks CJK IMEs (no candidate
display). Remove the flag.

**Fix attempted.** Removed `enableSuggestions: false`. CJK input
works again. Bug — different bug — actually fixed here. **Not the
overlay IME desync.** Recorded for completeness.

### v1.0.539–v1.0.547 — various dead ends

**Theories tried and rejected.**

- Listener rebuild cascade (`_ctrl.addListener` → setState).
  Rejected: `agent_compose.dart` has identical listener and works
  fine.
- TextField widget identity caching to short-circuit Flutter
  Element.updateChild. Rejected: bug persists with cache.
- Panel-position freeze against per-frame `viewInsets`. Helpful for
  position stability, not the IME bug.

### v1.0.548 — TextField widget instance cache

**Theory.** Each rebuild creates a new TextField widget; Flutter's
diffing might be confusing the State.

**Fix attempted.** Cache the TextField widget instance and reuse it
across rebuilds.

**Why it was wrong.** Defended against a hypothetical rebuild path,
but the bug persisted — proving the bug wasn't about widget identity.
Cache was removed in v1.0.551 to keep the file simpler.

### v1.0.549 — Panel position freeze

**Theory.** Panel's outer `Positioned(top:)` value moves per frame
during Gboard suggestion-strip animations, which moves the
`EditableText` render box, which re-emits
`setEditableSizeAndTransform` to the IME, which makes the IME resync
its predictive cache.

**Fix attempted.** Snap the keyboard inset only on open/close
transitions; ignore per-frame fluctuations.

**Why it was wrong.** Useful for panel-position stability but doesn't
address EditableText's internal MediaQuery subscription, which was
also wrong (see v1.0.555 below).

### v1.0.551 — Disable IME personalized learning

**Theory.** `IME_FLAG_NO_PERSONALIZED_LEARNING` will stop Gboard from
restoring deleted text via its personal cache.

**Fix attempted.** Set `enableIMEPersonalizedLearning: false`.

**Why it was wrong.** Tester clue: "it is not predictive, whatever I
input, the previous old input comes back." Rules out per-field
learning (which IS predictive — prefix-driven).

### v1.0.552 — Stack autocorrect / smart-text disables

**Theory.** `autocorrect: false` + smart-text disables would prevent
the dictionary-driven and smart-replacement paths that might restore
deleted text.

**Fix attempted.** Stacked the four flags (`enableIMEPersonalizedLearning`,
`autocorrect`, `smartDashesType`, `smartQuotesType`) all to disabled.

**Why it was wrong.** Bug persisted. **The working agent_compose has
NONE of these flags.** They were cargo-cult defensive layers added
in response to symptoms, not the cause. Stripped in v1.0.558.

### v1.0.553 — Key the Row children

**Theory.** Row children matched by position; conditional children
(`if (voiceEnabled) ...`) shift the index of the Expanded(TextField),
which causes Flutter to destroy-and-rebuild the EditableText →
reopens IME connection with stale text.

**Fix attempted.** Add `ValueKey` to every Row child.

**Why it was wrong.** Necessary for stability across conditional
voice/attach features asyncing in, but **not sufficient** for the
IME bug.

### v1.0.555 — Mask `MediaQuery.viewInsets` in panel

**Theory.** `EditableText.Scrollable._showCaretOnScreen` subscribes
to `MediaQuery.viewInsets.bottom`; every Gboard animation frame
notifies subscribers; EditableText rebuilds; `_updateRemoteEditingValueIfNeeded`
re-pushes stale `setEditingState`.

**Fix attempted.** Wrap the panel in
`MediaQuery(data: outerMq.copyWith(viewInsets: zero, ...))`.

**Why it was wrong.** A rebuild-storm theory; bug persisted. The
fix shape was right for a different problem (Scaffold uses this exact
pattern), but not for ours.

### v1.0.557 — Non-subscribing MQ read + systemGestureInsets zero

**Theory.** v1.0.555's mask still reads MediaQuery via `MediaQuery.of(context)`,
which subscribes the panel to ancestor MQ ticks. Also `systemGestureInsets`
wasn't in the mask. Read MQ without subscribing, and zero
`systemGestureInsets` too.

**Fix attempted.** Switch to
`context.getElementForInheritedWidgetOfExactType<MediaQuery>()` and
add `systemGestureInsets: EdgeInsets.zero`.

**Why it was wrong.** Refinement of the rebuild-storm theory, also
wrong. The bug isn't about rebuilds.

### v1.0.558 — Strip the defensive IME flags

**Theory.** Maybe the four defensive flags are themselves causing the
bug by pushing Android into a non-standard EditorInfo mode.

**Fix attempted.** Removed all four defensive flags. TextField now
matches `agent_compose.dart` exactly on IME config.

**Why it was wrong.** Bug pre-dated the flags, so they couldn't be
the cause. Matching config didn't fix the bug. Useful housekeeping
nonetheless — those flags were cargo-cult.

### v1.0.559 — Scaffold wrap for IME plumbing

**Theory.** Scaffold provides ScaffoldMessenger,
ScrollNotificationObserver, DefaultSelectionStyle, etc. that the
overlay's bare `Material(transparency)` was missing.

**Fix attempted.** Replace `Material(type: transparency)` with
`Scaffold(backgroundColor: transparent, resizeToAvoidBottomInset: false)`.

**Why it was wrong.** Scaffold provides plumbing but doesn't trigger
InputConnection refresh. Bug persisted.

### v1.0.560 — Explicit FocusScope wrap (Flutter issue #28986)

**Theory.** Flutter contributor HansMuller (closed issue
[#28986](https://github.com/flutter/flutter/issues/28986)): *"To
create a TextField in an Overlay you need to provide an enclosing
FocusScope."*

**Fix attempted.** Long-lived `FocusScopeNode` on `_StewardOverlayState`,
wrapped the panel in `FocusScope(node: node, child: Scaffold(...))`.

**Why it was wrong.** #28986 addressed a *different* manifestation
(TextField can't gain focus or open keyboard at all). Our TextField
*can* focus and *can* open keyboard; the issue is post-focus state
desync. Explicit FocusScope was structurally correct but not
sufficient.

## 4. The fix that worked — focus-bounce via ghost FocusNode

### v1.0.561 — Ghost-focus bounce automation

**Theory.** Confirmed via tester workaround: forcing
InputConnection re-creation by switching focus to a *different*
EditableText fixes the IME cache for one editing operation.
Automate that switch.

**Fix.** A hidden 1×1 ghost TextField parked offscreen at
`(-1000, -1000)` inside the input's Stack, wrapped in IgnorePointer.
Has its own `_ghostController` + `_ghostFocus`. A `_ctrl` listener
detects bug-trigger conditions and runs:

```dart
void _bounceFocusForImeResync() {
  _isResyncing = true;
  _ghostFocus.requestFocus();
  WidgetsBinding.instance.addPostFrameCallback((_) {
    if (!mounted) { _isResyncing = false; return; }
    _focus.requestFocus();
    _isResyncing = false;
  });
}
```

Triggers (`_onCtrlChanged` listener):

1. **Cursor-only change** — text unchanged, selection changed (user
   tapped to move cursor). Detected by
   `last.text == cur.text && last.selection != cur.selection`.
2. **Programmatic mutation** — voice / pick-text-file / send-error
   restore wrote to `_ctrl.value`. Detected by a flag set
   immediately before the write.
3. **Delete or replace** (added v1.0.562) — `textChanged && !isPureAppend && !involvesComposing`.
   Catches backspace and selection-replace. The `involvesComposing`
   guard skips CJK candidate selection (composing "ni" → committed
   "你"), where `composing.isValid` transitions from valid → empty.

**Timing.** `addPostFrameCallback` (not `Future.microtask`). The
post-frame gap is required: Android needs to actually register the
ghost EditText as the "served view" between the two focus changes.
Microtask would coalesce both changes within one render frame and
defeat the InputConnection refresh.

### v1.0.562 — Extend to delete/replace, exclude composing

Tester reported v1.0.561 fixed cursor moves + voice writes but
backspace was still broken. Same root cause: IME doesn't update its
cache on backspace either. Extended the listener to catch text
shrinks; gated by `!involvesComposing` so CJK commits still flow
through normally.

### v1.0.563 — Pin input border color across focus states

The focus-bounce produces a brief frame where the real TextField is
unfocused (ghost holds focus). Material's `InputDecorator` picks
different colors for `enabledBorder` vs `focusedBorder`, visible as a
border-color flash on every bounce. Fix: pin all three border slots
(`border`, `enabledBorder`, `focusedBorder`) to a single
`_kStableInputBorder`.

## 5. What remains imperfect

- **IME-state flicker** persists — the suggestion bar / candidate
  popup briefly refreshes on each focus switch. Intrinsic to the
  focus-bounce mechanism; can't be eliminated without changing the
  timing (which defeats the fix) or changing the mount point (see
  §7).
- **Mechanistic certainty.** §6 below lists the candidate
  explanations for why this happens in the first place; none are
  verified against Flutter engine source or instrumented runtime
  observations.

## 6. Where verification work would continue

If a future debugger wants to confirm the exact code path, the
diagnostic steps are:

1. **Read Flutter engine source.** Specifically
   `engine/shell/platform/android/io/flutter/plugin/editing/TextInputPlugin.java`
   and `flutter/src/services/text_input.dart`. Trace the
   `setEditingState` call from Dart → platform channel → Android
   plugin → `InputMethodManager.updateSelection()` and verify under
   what conditions the IMM call is skipped or routed to a stale
   target.
2. **Instrument adb logcat.** Run a debug build with
   `adb logcat -s InputMethodManager InputConnectionWrapper` and
   compare the overlay TextField against agent_compose. The expected
   signal is whether `updateSelection` is being called by the
   plugin, whether IMM accepts it, and whether the IME's
   `onUpdateSelection` callback fires.
3. **Compare InputConnection client IDs.** Each `TextInput.attach`
   produces a new client_id; check whether the overlay's client_id
   gets stuck at a value the IMM no longer recognizes vs.
   agent_compose's which gets refreshed.

The three candidate root causes, in order of likelihood:

- **Lifecycle-driven InputConnection refresh.** Route-mounted
  TextFields receive `attach` re-fires from Navigator/FocusScope
  lifecycle events; overlay TextFields don't. The desync exists in
  both cases but is invisible in route-mounted because the
  connection is refreshed naturally.
- **IMM "currently served view" mismatch.** Android's
  `InputMethodManager.updateSelection()` is conditional on the View
  matching its tracked "served view." Some property of the overlay
  TextField's mount might cause Flutter to call updateSelection on
  a View that IMM doesn't currently consider served.
- **Flutter plugin `InputTarget` tracking drift.** Flutter's Android
  `TextInputPlugin` keeps an `InputTarget` per client_id. In
  non-route subtrees, this mapping might drift such that
  `setEditingState` updates the Dart-side cache without propagating
  the IMM notification.

## 7. Structural escape valve

If the focus-bounce workaround proves insufficient (e.g., the
remaining IME-state flicker becomes a UX blocker, or new edge cases
emerge), the next-tier fix is the **OverlayEntry refactor**: move the
panel from `MaterialApp.builder` mount to an `OverlayEntry` inserted
into Navigator's overlay system via `Overlay.of(navigatorContext).insert(...)`.
This puts the TextField inside the Navigator's overlay hierarchy,
giving it the same lifecycle that route-mounted TextFields get.

Estimated cost: 100–150 LOC across `lib/widgets/steward_overlay/steward_overlay.dart`
and `lib/main.dart`. Risk: route/lifecycle/focus management
regressions in adjacent panel UX (drag, resize, drag-handle gestures,
puck collapse interactions).

Not shipped because the focus-bounce workaround is empirically
sufficient (per tester verification at v1.0.563). Revisit if the
residual flicker becomes load-bearing or if a new manifestation
emerges that focus-bounce can't reach.

## 8. References

- [Flutter issue #28986 — TextField in Overlay needs FocusScope](https://github.com/flutter/flutter/issues/28986)
- [HansMuller's FocusScope gist](https://gist.github.com/HansMuller/55f4e530a8e96777ad53af8bb20fb83b)
- [Flutter issue #119849 — TextField outside MaterialApp.router](https://github.com/flutter/flutter/issues/119849)
- [Flutter docs — TextInputClient.currentTextEditingValue migration](https://docs.flutter.dev/release/breaking-changes/text-input-client-current-value)
- [`lib/widgets/steward_overlay/steward_overlay_chat.dart`](../../lib/widgets/steward_overlay/steward_overlay_chat.dart) — the
  shipped fix, in `_ChatInputState._onCtrlChanged` /
  `_bounceFocusForImeResync`.
- [`lib/widgets/steward_overlay/steward_overlay.dart`](../../lib/widgets/steward_overlay/steward_overlay.dart) — the
  `_panelFocusScope` field (kept from v1.0.560; not load-bearing for
  the bug, but established structural correctness).
