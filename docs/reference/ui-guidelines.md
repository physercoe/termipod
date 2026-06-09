# UI guidelines

> **Type:** reference
> **Status:** Current (2026-06-09) — reconciled to the named design tokens per [ADR-047](../decisions/047-design-system-enforcement.md)
> **Audience:** contributors
> **Last verified vs code:** v1.0.810

**TL;DR.** Design language for the Flutter app. Colors, spacing,
radius, and typography are **named tokens** in `lib/theme/` — the
single source of truth ([ADR-047](../decisions/047-design-system-enforcement.md)
D-1). This doc *describes* those tokens and points to the source; it
never asserts independent numbers. New code composes from the tokens,
and a CI ratchet (`scripts/lint-design-tokens.sh`) blocks new
off-token drift. Navigation IA is spine material — see
`../spine/information-architecture.md`.

---

## 1. Source of truth

The Flutter code is authoritative, not this doc:

- Colors: `lib/theme/design_colors.dart` (`DesignColors`)
- Spacing / radius / type / icon scales: `lib/theme/tokens.dart`
  (`Spacing`, `Radii`, `FontSizes`, `IconSizes`)
- Theme + light/dark + `chipTheme`: `lib/theme/app_theme.dart`
- Chips: `lib/widgets/app_chip.dart` (`AppStatusChip`, `AppChoiceChip`)
- Terminal palette: `lib/theme/terminal_colors.dart`
- Task priority badges: `lib/theme/task_priority_style.dart`

When this doc and the code disagree, the code wins. Update the doc
in the same commit that changes a token. A CI ratchet
(`scripts/lint-design-tokens.sh`, ADR-047 D-9) fails the build when a
new off-token value appears, so the backlog only shrinks.

---

## 2. Color tokens (current)

Pulled from `DesignColors` (v1.0.810):

| Token | Dark | Light |
|---|---|---|
| Primary (brand accent) | `#00C0D1` | (same) |
| Primary dark | `#009AA8` | (same) |
| Background | `#0E0E11` | `#F9FAFB` |
| Surface | `#1E1F27` | `#FFFFFF` |
| Canvas | `#101116` | `#F3F4F6` |
| Input | `#0B0F13` | `#F9FAFB` |
| Border | `#2A2B36` | `#E5E7EB` |
| Text — primary | `#FFFFFF` | `#111827` |
| Text — secondary | `#9CA3AF` | `#4B5563` |
| Text — muted | `#868C96` | `#646B73` |
| Status — success | `#22C55E` | (same) |
| Status — warning (amber) | `#F59E0B` | (same) |
| Status — error | `#EF4444` | (same) |

**One brand accent** (ADR-047 D-5). Cyan `#00C0D1` is the sole brand
accent. The Material `ColorScheme.secondary` slot is wired to the
tonal cyan `#009AA8` (not a clashing hue). Amber `#F59E0B` is the
**warning semantic only** — `DesignColors.warning`, never a second
brand color. (`DesignColors.secondary` still holds the amber value for
back-compat but is not used as an accent.)

**Accessibility** (ADR-047 D-6). Every text token clears WCAG 2.1 AA
(≥4.5:1) on each surface it renders on; `test/theme/contrast_test.dart`
guards this. The muted tokens were darkened/lightened to that floor
(the old `#6B7280` / `#9CA3AF` failed it).

---

## 3. Spacing

A named 4px grid — `Spacing` in `lib/theme/tokens.dart` (ADR-047 D-2).
Reach for the token, not a bare number; off-grid values (6, 7, 10, 11,
14) are barred in new code and counted by the ratchet.

| Token | px | Use |
|---|---|---|
| `Spacing.s2` | 2 | hairline insets only (borders/dividers) |
| `Spacing.s4` | 4 | tight gaps |
| `Spacing.s8` | 8 | default gap |
| `Spacing.s12` | 12 | card padding (horizontal) |
| `Spacing.s16` | 16 | section padding |
| `Spacing.s24` | 24 | block separation |
| `Spacing.s32` | 32 | page-level spacing |

`EdgeInsets.symmetric(horizontal: Spacing.s12, vertical: Spacing.s8)`
is the typical card padding.

---

## 4. Radius

The Material 3 shape scale — `Radii` in `lib/theme/tokens.dart`
(ADR-047 D-3). `Radii.md` (12) is the default for cards and surfaces
(it matches the coded `ThemeData` default).

| Token | px | Use |
|---|---|---|
| `Radii.xs` | 4 | code-block / terminal-style surfaces |
| `Radii.sm` | 8 | inputs, small chips |
| `Radii.md` | 12 | cards, surfaces (default) |
| `Radii.lg` | 16 | large containers, sheets |
| `Radii.stadium` | — | pills, FAB (`stadiumBorder`) |

---

## 5. Typography

| Use | Font |
|---|---|
| UI text | Space Grotesk (via `google_fonts`) |
| Mono / code / terminal | JetBrains Mono |
| Terminal CJK fallback | HackGen / PlemolJP |

A 6-step size scale — `FontSizes` in `lib/theme/tokens.dart` (ADR-047
D-4). **13 (`bodySmall`) is the floor for primary readable text**;
8/9/10 are barred for real text (label micro-text uses `label`).
Weight and letter-spacing live on the `TextTheme`.

| Token | px | Use |
|---|---|---|
| `FontSizes.label` | 11 | chip/badge labels, metadata |
| `FontSizes.caption` | 12 | captions, dense labels |
| `FontSizes.bodySmall` | 13 | body / card content (floor) |
| `FontSizes.body` | 14 | tappable controls, primary body |
| `FontSizes.subtitle` | 16 | subtitles |
| `FontSizes.title` | 18 | card / section headers |
| `FontSizes.titleLarge` | 20 | screen titles |

---

## 6. Iconography

- Material Icons (built into Flutter)
- Sizes: `IconSizes` in `lib/theme/tokens.dart` — `sm` 14, `md` 18,
  `lg` 22 (ADR-047 D-4).
- Tool-call cards use a per-tool glyph map — see `_toolIconFor()` in
  `lib/widgets/transcript/tool_renderers.dart` for the canonical
  mapping (Bash → terminal, Edit → pencil, Read → description, etc.)

---

## 7. Information architecture

Bottom-nav structure and screen layouts live in
`../spine/information-architecture.md`. Don't duplicate here — IA is
axiom-tier and changes affect every screen.

---

## 8. Folding-device support

Side-by-side layouts kick in via `MediaQuery` width breakpoints. The
session detail screen is the canonical example — left pane shows the
session list, right pane the active transcript when the screen is
wider than ~720dp.

---

## 9. Logo

`../logo/logo.svg` is the source. Assets are pre-rendered into
`assets/icon/` for app launcher icons.
