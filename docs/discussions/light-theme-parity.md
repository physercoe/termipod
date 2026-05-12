# Light-theme parity gaps (post-MVP)

> **Type:** discussion
> **Status:** Open — Deferred to post-MVP (2026-05-12)
> **Audience:** contributors · principal
> **Last verified vs code:** v1.0.516

**TL;DR.** Termipod was designed dark-first. The light theme exists
as a Material `ThemeData.light` shim, but several surfaces have
hard-coded dark-palette colours (overlay backgrounds, viewer
chrome, decorative scrim layers) that don't repaint when the user
switches to light mode. The list below is the catalogue of known
mismatches as of v1.0.516; closing the gaps is a focused
post-MVP wedge, not a per-release polish item.

## Known gaps as of v1.0.516

1. **Canvas-app viewer overlay (v1.0.516 fix).**
   `_ArtifactCanvasViewerState` uses `DesignColors.canvasDark` as
   the opaque overlay that covers the WebView's cold-init window.
   A light-theme user still sees a brief dark flash on first open
   instead of white. Less jarring than the original white flash
   (which contrasted hard with the dark theme), but technically
   still mismatched. Theme-correct fix: read
   `Theme.of(context).colorScheme.surface` instead of pinning the
   dark palette literal.
   - File: `lib/widgets/artifact_viewers/canvas_viewer.dart`
2. **Most artifact viewers' background colours** (PDF, image,
   audio, video, code bundle) similarly use dark-palette literals
   from `lib/theme/design_colors.dart`. Quick audit needed once
   light-theme work starts; the literal references show up via
   `grep -n "DesignColors\." lib/widgets/artifact_viewers/`.
3. **Steward overlay puck + panel.** The persistent floating
   overlay (ADR-023) was painted exclusively against the dark
   palette. In light theme the puck retains its dark tint and the
   chat panel's surface contrast is off.
4. **Code highlights in transcripts.** `flutter_highlight` is
   imported with a single theme constant; we don't switch
   highlight themes against the active brightness. Code blocks
   in light mode look like they belong in dark mode.
5. **Diagnostic strips & error widgets.** v1.0.514+'s PDF
   diagnostic strip uses `DesignColors.surfaceDark` and
   `DesignColors.borderDark` literals; same story for several
   "Cannot render" placeholders across viewers.

## Why this is deferred

- **Demo arc is dark-themed.** The reference screenshots, tester
  walkthroughs (`how-to/test-agent-driven-prototype.md`), and the
  principal's primary device all run dark mode. Closing the
  light-theme gap doesn't move the MVP target.
- **It's a single coherent wedge, not a polish loop.** The fix is
  one pass through `lib/theme/design_colors.dart` to introduce
  semantic getters (`surface`, `divider`, `canvasNeutral`) that
  resolve against `Theme.of(context).brightness`, plus a
  search-and-replace of literal references. Doing it piecemeal
  per release leads to inconsistent contrast as half-converted
  widgets ship before the audit is complete.
- **Risk of regressing dark.** A poorly-staged conversion can
  introduce subtle dark-theme contrast bugs (the path we already
  trust). Better to scope it as one wedge with a screenshot
  walkthrough across surfaces.

## When to revisit

Reopen this discussion when one of these triggers fires:

- A tester reports they prefer light mode and want it usable
  end-to-end (currently no such report).
- We add OLED-aware power-save flows that benefit from a
  reliable light-mode appearance.
- The dark/light system-setting toggle (Settings → Appearance)
  surfaces a complaint in tester reports.

## Estimated effort when prioritised

- ~1–2 wedges (~200–400 LOC plus screenshot audit).
- W1: introduce `BuildContext`-aware getters in `design_colors.
  dart`, deprecate the `*Dark` / `*Light` literal pairs.
- W2: convert all `lib/widgets/artifact_viewers/` + steward
  overlay surfaces to the new getters, capture before/after
  screenshots, add a golden-test matrix for the worst-offending
  surfaces.

## References

- [`reference/ui-guidelines.md`](../reference/ui-guidelines.md)
  — the design-system source of truth (which should grow a
  "theme-correct colour usage" section as part of W1).
- [`decisions/023-agent-driven-mobile-ui.md`](../decisions/023-agent-driven-mobile-ui.md)
  — overlay design; will need theme-aware audit when this
  reopens.
- `lib/theme/design_colors.dart` — the source of the
  hard-coded literals to replace.
