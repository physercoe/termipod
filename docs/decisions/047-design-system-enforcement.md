# 047. Design-system enforcement — tokens as the single source of truth

> **Type:** decision
> **Status:** Accepted (2026-06-09) — director-directed resolution of the
> design-drift tensions raised in [the design-system-enforcement
> discussion](../discussions/design-system-enforcement.md) (audit #71)
> **Audience:** contributors · reviewers · principal
> **Last verified vs code:** v1.0.810

**TL;DR.** The app's visual system is *defined but bypassed*, and three
sources of truth (named conventions, coded tokens, inline widget values)
have diverged. We resolve this decisively toward a top-tier product
standard: **named design tokens become the single source of truth,
aligned to Material 3 and WCAG 2.1 AA, enforced by a no-new-violations
ratchet rather than a big-bang rewrite.** This ADR records the ten
decisions that close the open questions.

---

## Context

[Issue #71] audited the Flutter UI and reported systemic design-system
drift. Independent re-verification (the companion discussion doc)
confirmed the thesis while correcting the specifics: **45** bespoke
chip/pill classes with no `chipTheme`; 16 distinct `fontSize:` values
with 10/11/12/13/14 all doing "body" duty; 6 dominant radii (12 is
default but undocumented); raw `Colors.*` in up to 17 files; and a real
WCAG failure the audit missed — `textMuted #6B7280` at **3.39:1** on
surface, below the AA floor.

The deeper problem: `docs/reference/ui-guidelines.md` (the *named*
conventions) is stale and disagrees with `lib/theme/` (the *coded*
tokens), which in turn disagrees with the *inline* values in ~70
widgets. The guidelines also deliberately rejected spacing tokens
("Dart has no `EdgeInsets` token system worth the ceremony") — a
small-app shortcut the evidence has now refuted.

The directive: the UI should be high-standard and professional, matching
the quality bar of top-company products. That bar is unambiguous about
method — every leading design system (Material 3, Apple HIG, Shopify
Polaris, GitHub Primer, Vercel Geist, Linear) is **token-based,
accessibility-floored, and centrally themed**. We adopt that method.

## Decision

**D-1 — Tokens are the single source of truth (reverse the inline-values
stance).** Consistency at scale requires one referent; the inline-values
shortcut measurably failed. Add `lib/theme/tokens.dart` exposing `const`
scales — `Spacing`, `Radii`, `FontSizes`, `IconSizes` — alongside the
existing `DesignColors`. A `const` class is trivial ceremony for a large
payoff; the "no token system worth it" objection is overruled by the
drift it produced.

**D-2 — Spacing: a named 4px grid.** `4 · 8 · 12 · 16 · 24 · 32` (with
`2` reserved for hairline insets only). Off-grid values (6, 7, 10, 11,
14) are eliminated. Asymmetric `fromLTRB` is allowed only when layout
genuinely requires it, and each side must still be a grid value.
(8-point grid, the cross-industry baseline.)

**D-3 — Radius: the Material 3 shape scale.** `4 (xs) · 8 (sm) · 12 (md,
default surfaces) · 16 (lg) · stadium (pills/FAB)`. The coded default is
already 12 (M3 *medium*), so we reconcile the docs to the code, not the
reverse. Migrate 10→12, 14→16; retire 1/2.5/3/7/15/18/20 except where a
shape is semantically required.

**D-4 — Typography: a 6-step semantic scale.** Map to Material 3 roles;
expose named sizes: `title 20/18 · subtitle 16 · body 14 · bodySmall 13
· caption 12 · label 11`. **13 is the floor for primary readable text**
— 8/9/10 are barred for real text (readability + contrast). The 16
ad-hoc sizes collapse to this set; weight/letter-spacing stay on the
existing `TextTheme`.

**D-5 — Color: one brand accent, semantic + neutral tokens, no strays.**
Cyan `#00C0D1` is the *sole* brand accent — restraint is the
professional default (cf. Linear, Vercel, Stripe). Amber `#F59E0B` is
the **warning semantic only**, never a second brand color. Wire
`ColorScheme.secondary` to a tonal cyan variant (`primaryDark
#009AA8`), not a literal copy of `primary`, so the Material slot is
meaningful without a clashing hue. Every raw
`Colors.grey/red/green/orange/amber(.shade*)` and stray inline
`Color(0xFF…)` migrates to a `DesignColors` token;
`propose_card_visuals.dart` (the worst offender) is fixed first.

**D-6 — Accessibility: WCAG 2.1 AA is a hard floor (AAA preferred for
body).** No text-sized color token may fall below **4.5:1** on its
surface. The concrete fix: `textMuted #6B7280` (3.39:1) → **`#868C96`**
(4.85:1 on surface, 5.70:1 on background), still visibly muted below
`textSecondary #9CA3AF`. New tokens are contrast-checked at definition.

**D-7 — Components: one chip, themed centrally.** Define `ChipThemeData`
in the theme and a small shared set (`AppChip`, `AppStatusChip`) with
variants; the 45 bespoke `_*Chip`/`_*Pill` classes collapse into them.
Repeated `Container + BoxDecoration` card surfaces move to the `Card`
theme or a shared surface widget. (Atomic-component principle — every
top-tier system ships exactly one chip.)

**D-8 — Elevation: flat by default, tonal elevation for true overlays.**
Dark UIs use surface-tint, not drop shadows (M3; Linear/GitHub dark).
Remove ad-hoc `boxShadow`; standardize a small set — `0` (default),
`1`, `3`, `8` — for menus, dialogs, and the FAB only.

**D-9 — Enforcement: a no-new-violations ratchet, not a big-bang.** A CI
check counts violations (off-token sizes/paddings/radii, raw `Colors.*`,
new private chip classes, stray `boxShadow`); the count may only
decrease. New code must use tokens (review-enforced); the existing
backlog is grandfathered and burned down opportunistically as files are
touched. This adopts the design system without freezing feature work.

**D-10 — `ui-guidelines.md` documents the tokens.** It is reconciled to
the token values (it is currently stale) and, going forward, *describes*
the tokens in `lib/theme/` — which remain the source of truth — rather
than asserting independent numbers.

## Consequences

- **Easier:** new screens compose from a fixed vocabulary; visual review
  is "does it use tokens?" not "is 11 or 12 right here?"; AA is
  structurally guaranteed; one chip change restyles the whole app.
- **Harder / new cost:** a token-discipline habit; the CI ratchet
  tooling (one script); a migration backlog (tracked as a plan, not this
  ADR). Mobile changes are CI-built + director device-tested (no local
  Flutter), so the rollout is incremental by necessity.
- **Now forbidden:** introducing a new private chip class; a new raw
  `Colors.*` or inline `Color(0xFF…)` where a token exists; a text token
  below 4.5:1; an off-grid spacing or off-scale radius/size in new code.
- **Resolved ambiguities:** the amber-vs-primary "secondary" question
  (amber = warning only; secondary = tonal cyan); the radius 12-vs-8
  split (12, M3 medium); the tokenize-or-not tension (tokenize).

Implementation is tracked in a separate plan —
[`design-system-rollout.md`](../plans/design-system-rollout.md) (WS1
tokens → WS2 accessibility + secondary → WS3 one chip → WS4 ratchet →
WS5 doc reconcile → WS6 burn-down).

## References

- Code: `lib/theme/design_colors.dart`, `lib/theme/app_theme.dart`
  (`ColorScheme.dark/light`, `secondary` at :44/:259),
  `lib/screens/me/widgets/propose_card_visuals.dart`; the 45 `_*Chip`
  classes across `lib/`.
- Discussion: [`design-system-enforcement.md`](../discussions/design-system-enforcement.md)
  (corrected audit + verified figures).
- Reference: [`ui-guidelines.md`](../reference/ui-guidelines.md) (to be
  reconciled, D-10).
- External practice: Material 3 shape & type scales and tonal elevation;
  WCAG 2.1 §1.4.3 (AA contrast 4.5:1); the 8-point grid; atomic
  design-system components.
- Source: issue #71 (the original audit).
