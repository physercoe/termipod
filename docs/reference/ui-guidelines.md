# UI guidelines

> **Type:** reference
> **Status:** Current (2026-04-28) — rewrites the prior MuxPod-era version (pre-rebrand)
> **Audience:** contributors
> **Last verified vs code:** v1.0.312

**TL;DR.** Lightweight design language for the Flutter app. Colors,
spacing, and typography are codified in `lib/theme/`; this doc names
the conventions and points to the source. Navigation IA is spine
material — see `../spine/information-architecture.md`.

---

## 1. Source of truth

The Flutter code is authoritative, not this doc:

- Colors: `lib/theme/design_colors.dart` (`DesignColors`)
- Theme + light/dark: `lib/theme/app_theme.dart`
- Terminal palette: `lib/theme/terminal_colors.dart`
- Task priority badges: `lib/theme/task_priority_style.dart`

When this doc and the code disagree, the code wins. Update the doc
in the same commit that changes a token.

---

## 2. Color tokens (current)

Pulled from `DesignColors` (v1.0.312):

| Token | Dark | Light |
|---|---|---|
| Primary | `#00C0D1` | (same) |
| Primary dark | `#009AA8` | (same) |
| Secondary | `#F59E0B` (amber) | (same) |
| Background | `#0E0E11` | `#F9FAFB` |
| Surface | `#1E1F27` | `#FFFFFF` |
| Canvas | `#101116` | `#F3F4F6` |
| Input | `#0B0F13` | `#F9FAFB` |
| Border | `#2A2B36` | `#E5E7EB` |
| Text — primary | `#FFFFFF` | `#111827` |
| Text — secondary | `#9CA3AF` | `#4B5563` |
| Text — muted | `#6B7280` | `#9CA3AF` |
| Status — success | `#4CAF50` | (same) |
| Status — warning | `#F59E0B` | (same) |
| Status — error | `#CF6679` | (same) |

---

## 3. Spacing

Tailwind-style 4px scale. Inline numeric values in widgets, not named
constants — Dart has no `EdgeInsets` token system worth the ceremony.

| Step | px |
|---|---|
| xs | 4 |
| sm | 8 |
| md | 16 |
| lg | 24 |
| xl | 32 |

`EdgeInsets.symmetric(horizontal: 12, vertical: 8)` is the typical
card padding.

---

## 4. Radius

| Surface | Radius |
|---|---|
| Cards | 8 |
| Inputs | 6 |
| Buttons | 8 |
| Pills / chips | 16 |

Code-block surfaces (the syntax-highlight wrapper, diff view) use 4
to read more like terminal output.

---

## 5. Typography

| Use | Font |
|---|---|
| UI text | Space Grotesk (via `google_fonts`) |
| Mono / code / terminal | JetBrains Mono |
| Terminal CJK fallback | HackGen / PlemolJP |

Sizes:
- Body / card content: 13
- Card headers / labels: 12 (700-weight)
- Pills / metadata: 10–11
- Tappable controls: 14 (matches Material default for finger targets)

---

## 6. Iconography

- Material Icons (built into Flutter)
- Tool-call cards use a per-tool glyph map — see
  `lib/widgets/agent_feed.dart` `toolIconFor()` for the canonical
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
