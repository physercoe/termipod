# Design-system rollout — implementation plan

> **Type:** plan
> **Status:** Proposed (2026-06-09) — not started; execution begins in a
> dedicated focus session
> **Audience:** contributors
> **Last verified vs code:** v1.0.810

**TL;DR.** Implementation tracker for
[ADR-047](../decisions/047-design-system-enforcement.md) (design-system
enforcement). Named tokens become the single source of truth, aligned to
Material 3 and floored at WCAG 2.1 AA, rolled out as small gated PRs
behind a **no-new-violations ratchet** rather than a big-bang rewrite.
Six workstreams; WS1–WS4 are bounded and shippable, WS5 is ongoing
burn-down. No local Flutter SDK — every mobile WS is CI-built +
director device-tested; the ratchet (WS4) is pure bash, locally testable.

---

## 1. Goal

Make the documented visual conventions structurally true: new code can
only compose from a fixed token vocabulary, accessibility is guaranteed,
and one central theme drives chips/surfaces/elevation. End state — a
contributor (or steward) writing a new screen reaches for
`Spacing.s16` / `Radii.md` / `FontSizes.body` / `AppChip`, never an
ad-hoc literal, and CI blocks regressions.

## 2. Non-goals

- **Not a visual redesign.** The brand (cyan accent, dark-first), the IA,
  and the navigation are unchanged. This is consistency + accessibility,
  not a new look.
- **Not a wholesale M3-component migration.** We adopt the M3 *scales*
  (shape, type, tonal elevation); we don't rewrite every widget to M3
  components.
- **Not a freeze.** Feature work continues; the ratchet (WS4) only blocks
  *new* violations, the backlog burns down opportunistically.
- **No typed-token DSL / theming framework.** Plain `const` classes — the
  minimum ceremony ADR-047 D-1 calls for.

## 3. Surfaces affected

- `lib/theme/` — new `tokens.dart`; edits to `design_colors.dart`
  (`textMuted`) and `app_theme.dart` (`ColorScheme.secondary`, `chipTheme`).
- `lib/widgets/` — new `app_chip.dart` (`AppChip` / `AppStatusChip`); the
  45 private `_*Chip` / `_*Pill` classes migrate into it over time.
- `docs/reference/ui-guidelines.md` — reconciled to describe the tokens
  (D-10).
- `scripts/` + `.github/workflows/ci.yml` — the ratchet (WS4).

## 4. Workstreams

Each WS is its own branch + PR, gated for no behavior change unless noted,
CI-green before merge.

### WS1 — Foundation tokens (`lib/theme/tokens.dart`)

Pure additions — nothing consumes them yet, so zero visual risk.

- `Spacing` — `s2, s4, s8, s12, s16, s24, s32` (4px grid; `s2` hairline
  only). ADR D-2.
- `Radii` — `xs = 4, sm = 8, md = 12, lg = 16, stadium`
  (`BorderRadius`/`Radius` getters). ADR D-3.
- `FontSizes` — `label = 11, caption = 12, bodySmall = 13, body = 14,
  subtitle = 16, title = 18, titleLarge = 20`. ADR D-4.
- `IconSizes` — `sm = 14, md = 18, lg = 22`. ADR D-4/§6.
- **Test:** `test/theme/tokens_test.dart` — pins the scale values so a
  later edit is deliberate. (Pure Dart, CI-runnable.)

### WS2 — Accessibility + secondary wiring (ship-now, tiny)

The only WS with an intentional visual delta; small and isolated.

- `design_colors.dart` — `textMuted #6B7280 → #868C96` (4.85:1 on
  surface, 5.70:1 on background; still visibly muted under
  `textSecondary #9CA3AF`). Audit the **light** muted token
  (`textMutedLight`) the same way. ADR D-6.
- `app_theme.dart:44` / `:259` — `ColorScheme.secondary:
  DesignColors.primary` → a tonal cyan (`primaryDark #009AA8`). Amber
  stays `DesignColors.warning`. ADR D-5.
- **Check before merge:** grep `colorScheme.secondary` /
  `.secondary` / `onSecondary` usages — confirm the cyan→darker-cyan
  shift is acceptable everywhere it lands (today nothing derives amber
  from it, since secondary == primary).
- **Test:** a pure-Dart WCAG-ratio helper + assertions that every
  text-tier token clears 4.5:1 on its surface (guards D-6 forever).
- Device-test: muted text still legible; no element turns amber
  unexpectedly.

### WS3 — One chip (`chipTheme` + `AppChip` / `AppStatusChip`)

Highest ROI — collapses the 45-class duplication. ADR D-7.

- Define `ChipThemeData` in `app_theme.dart` (both schemes).
- New `lib/widgets/app_chip.dart`: `AppChip` (label + optional
  leading/​color variant) and `AppStatusChip` (status → token color),
  using `Radii.stadium`, `Spacing`, `FontSizes.label`.
- Migrate the most-duplicated first: `_StatusChip` (×5), `_Pill` (×4),
  `_KindChip` (×4), `_Chip` (×4). Each migration is byte-identical
  visually where possible; gated.
- **Test:** widget test for `AppChip`/`AppStatusChip` variants +
  golden-free assertions (color/shape from theme).

### WS4 — The ratchet (`scripts/lint-design-tokens.sh`)

Pure bash — locally testable, the enforcement keystone. ADR D-9.

- Counts violations across `lib/`: off-grid `EdgeInsets` numbers,
  off-scale `fontSize:`, off-scale `BorderRadius.circular(...)`, raw
  `Colors.(grey|red|green|orange|amber)`, stray `Color(0xFF…)` outside
  `design_colors.dart`/palette files, `boxShadow`, and `class
  _.*(Chip|Pill)`.
- A committed baseline (`scripts/design-token-baseline.txt`) holds the
  current counts; CI **fails if any count exceeds baseline** (monotone
  non-increasing). A `--update` flag rewrites the baseline *downward*
  after a burn-down PR.
- Wire into `ci.yml` "Analyze & Test" job (a new "Lint design tokens"
  step, mirroring "Lint docs").
- **Verify:** run locally; introduce a deliberate violation → fails;
  remove → passes.

### WS5 — Reconcile `ui-guidelines.md` (doc-only)

ADR D-10. Update the color table (it's stale: lists error `#CF6679` /
success `#4CAF50` vs code `#EF4444` / `#22C55E`), the radius table (→ M3
scale, 12 default), and the type table to *describe the tokens*; add a
line stating `lib/theme/tokens.dart` is the source of truth. Re-stamp
`Last verified vs code`. No version bump (doc-only).

### WS6 — Burn-down (ongoing, ratchet-guarded)

Not a single PR. As files are touched (or in dedicated sweeps): migrate
off-scale sizes/paddings/radii to tokens, raw `Colors.*` to
`DesignColors`, remaining `_*Chip` classes to `AppChip`, stray
`boxShadow` to the standardized elevation set (`0/1/3/8`, ADR D-8). Each
sweep lowers the WS4 baseline. `propose_card_visuals.dart` is the first
target (the worst color offender).

## 5. Sequencing & verification

- **Order:** WS1 → WS2 → WS3 → WS4 → WS5 → WS6 (ongoing). WS1 before all
  (everything references the tokens); WS4 before heavy WS6 (so burn-down
  can ratchet). WS2 can ship in parallel with WS1 if desired (different
  files).
- **Per PR:** branch off `main`; `flutter analyze` + `flutter test` green
  in CI (no local Flutter); gated for no behavior change except WS2
  (muted text) and WS5 (doc); director device-test for any visual WS.
- **Definition of done for the program:** the ratchet baseline reaches
  zero for new-code-reachable categories, or stabilizes at an explicitly
  accepted residual (e.g. legitimate palette files).

## 6. Risks

- **Mobile is CI-only.** Every visual WS leans on the director's
  device-test; keep diffs small and gated so a regression is easy to
  localize. (Established working model.)
- **Chip migration scope creep.** 45 classes is large; migrate in
  batches by frequency, not all at once — WS3 ships the shared widgets +
  the top offenders, the rest is WS6.
- **Secondary shift (WS2).** Low risk but real — verify no widget relied
  on `secondary == primary` for an effect.
- **Ratchet false positives.** Palette/syntax-highlight files use raw
  colors legitimately; the WS4 script must allowlist them
  (`design_colors.dart`, `terminal_colors.dart`, syntax themes).

## 7. References

- Decision: [ADR-047](../decisions/047-design-system-enforcement.md).
- Discussion (corrected audit + verified figures):
  [`design-system-enforcement.md`](../discussions/design-system-enforcement.md).
- Reference to reconcile: [`ui-guidelines.md`](../reference/ui-guidelines.md).
- Code anchors: `lib/theme/design_colors.dart`,
  `lib/theme/app_theme.dart` (`:44`/`:259` secondary),
  `lib/screens/me/widgets/propose_card_visuals.dart`.
- Source: issue #71 (the original audit).
