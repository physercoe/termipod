# Design system enforcement

> **Type:** discussion
> **Status:** Resolved (2026-06-09) — tensions resolved in
> [ADR-047](../decisions/047-design-system-enforcement.md); doc retained
> as the corrected-audit record + rationale
> **Audience:** contributors · reviewers · principal
> **Last verified vs code:** v1.0.810

**TL;DR.** An AI-authored UI audit (issue #71) graded the mobile app 6.0/10
and reported systemic "design-system drift": tokens are defined but
bypassed. Re-verifying every quantitative claim against `lib/` confirms
the *thesis* — there is real drift — but several of the audit's specific
numbers and file citations are wrong, and the deeper problem it didn't
name is that **three sources of truth have diverged**: the named
conventions in [`ui-guidelines.md`](../reference/ui-guidelines.md), the
coded tokens in `lib/theme/`, and the inline values in widgets. This doc
records the corrected findings and frames the enforce-or-not decision so
we don't action a large refactor off inaccurate figures.

---

## 1. The question

The audit's recommendation list (P0–P3) is a multi-week program:
introduce a token file, migrate every font size / padding / radius to a
scale, eliminate every raw `Colors.*`, extract shared chip widgets,
add a real secondary color, standardise elevation. Before committing to
any of that we need to answer: **which of these are real, which are
already-decided-against, and what is the cheapest enforcement that stops
the drift without a big-bang rewrite?**

This is a discussion, not a decision — the load-bearing choices
(tokenize-or-not, lint-or-not) should land in an ADR or a plan once we
pick a direction.

## 2. The real framing — three diverged sources of truth

The audit treats the problem as "design system defined but not
enforced." More precisely, there are **three** places a contributor can
learn "what radius is a card," and they disagree:

1. **Named conventions** — [`ui-guidelines.md`](../reference/ui-guidelines.md)
   (reference-tier). Says cards = radius 8, inputs = 6, pills = 16; body
   text = 13, labels = 12, pills = 10–11; a 4px spacing scale
   (4/8/16/24/32). It also **deliberately rejects spacing tokens**:
   *"Inline numeric values in widgets, not named constants — Dart has no
   `EdgeInsets` token system worth the ceremony."*
2. **Coded tokens** — `lib/theme/design_colors.dart` + `app_theme.dart`.
   The `ThemeData` default card/input radius is **12**, not the 8 the
   guidelines name. `DesignColors.secondary` is amber (`#F59E0B`) — but
   the Material `ColorScheme.secondary` is wired to *primary*
   (`app_theme.dart:44`), so the amber token is defined and unused.
3. **Inline widget values** — the actual `fontSize:`, `EdgeInsets`,
   `BorderRadius.circular(...)` literals scattered across ~70 files,
   which drift off both of the above.

`ui-guidelines.md` is itself **stale** (last verified v1.0.312): its
color table lists error `#CF6679` / success `#4CAF50`, but the code now
ships `#EF4444` / `#22C55E`. So the "named conventions" can't currently
be trusted as the source of truth either. **Reconciling these three is
the actual work** — more than "add tokens."

## 3. The audit, corrected (verified against v1.0.810)

Each claim re-checked by grep / recompute. Citations are `file:line` or
counts reproducible from `lib/`.

| Audit claim | Verdict | What's actually there |
|---|---|---|
| 30+ private chip/pill classes; no `chipTheme` | ✅ **understated** | **45** `_*Chip`/`_*Pill` classes (`_StatusChip`×5, `_Pill`×4, `_KindChip`×4, `_Chip`×4, …); no `ChipThemeData` in `lib/theme/`. The strongest, most actionable finding. |
| `secondary == primary` | ✅ but imprecise | `app_theme.dart:44` wires `ColorScheme.secondary` to primary. The amber `DesignColors.secondary` (`#F59E0B`) **exists** — the fix is *wiring it*, not inventing a color. |
| No design-token / scale file | ✅ | `lib/theme/` = `app_theme`, `design_colors`, `task_priority_style`, `terminal_colors`. No spacing/type/radius scale constants. |
| Overlapping body font sizes | ✅ (the substance) | 10 (236×), 11 (337×), 12 (289×), 13 (182×), 14 (142×) all carry "body" duty. |
| Raw `Colors.*` drift | ✅ roughly | grey 12 files, orange 8, green 9, amber 6 — close. `Colors.red` in **17** (worse than the ~10 claimed). `propose_card_visuals.dart` hand-rolls `amber/grey/green.shade*`. |
| Radius sprawl | ✅ | 6 dominant: 12 (131×), 8 (125×), 10 (73×), 4 (71×), 16 (59×), 6 (54×) + outliers (1, 2.5, 3, 7, 15). |
| Elevation overrides a flat theme | ⚠️ partial | `insight_transcript.dart:1249 elevation: 12` ✓; `boxShadow` in 9 files. Several specific citations (`sessions_rail`, `steward_overlay`, `voice_recording_hud`) no longer match — likely stale (audit dated 2026-06-08, before recent refactors). |

## 4. Where the audit is wrong — don't treat its numbers as a spec

- **"19 font sizes (…36, 40, 48, 64)"** → **16** distinct `fontSize:`
  values; **36/40/48/64 do not appear** as font sizes; `17` exists and
  isn't listed.
- **"`10.5` in `agent_events_sheet.dart`"** → wrong file. It's in
  `run_report_card.dart:423`, `pdf_viewer.dart`, and
  `structured_deliverable_viewer.dart`.
- **"TextTheme is empty"** → **false.** `spaceGroteskTextTheme` defines a
  full weight + letter-spacing scale for all 15 roles; it omits *sizes*
  (the real gap), but it isn't empty.
- **"inline `Color(0xFF…)` ~6 places"** → **understated**: **61**
  occurrences outside `design_colors.dart` (some legitimate — syntax
  highlight / terminal palettes).
- **WCAG: "`#9CA3AF` = 4.6:1, barely AA"** → **wrong.** Recomputed,
  `#9CA3AF` is **7.59:1** on background, **6.46:1** on surface — safely
  AA/AAA. **The audit flagged the safe color and missed the failing
  one:** `textMuted #6B7280` on surface is **3.39:1**, which *fails* AA
  for normal text. That is the genuine accessibility bug.

The audit's own footer notes it was written by an AI from a read-only
pass; independent verification (this doc) is exactly why we don't ship a
refactor off its raw numbers.

## 5. The genuine tensions to settle

1. **Tokenize spacing — or honor the documented anti-token decision?**
   `ui-guidelines.md` deliberately chose inline values over an
   `EdgeInsets` token system. The audit's P0 ("create `design_tokens.dart`
   with a spacing scale") *reverses* that decision. Either is defensible,
   but it's a decision to make consciously, not drift into. The drift
   that's unambiguously bad is **off-scale values** (6, 7, 10, 11, 14)
   regardless of whether the scale is named or inline.
2. **Reconcile the radius default.** The theme default is 12; the
   guidelines name 8. Pick one and make the other follow (update the doc
   *or* the theme), then the "12 vs 8 vs 10" sprawl has a center.
3. **Wire the amber secondary** (or formally accept a monochrome accent).
   A real secondary unlocks a contrast dimension the UI currently can't
   use.
4. **The real WCAG fix:** `textMuted #6B7280` at 3.39:1. This is a
   concrete, isolated bug worth its own issue regardless of the larger
   program.
5. **Chip consolidation is the high-ROI item.** 45 bespoke chip classes
   with hand-coded fg/bg/radius is the single largest source of visual
   inconsistency *and* maintenance cost. A `chipTheme` + 2–3 shared
   widgets (`AppChip`, `AppStatusChip`) is a contained, testable change.

## 6. Options for enforcement

- **A — Status quo + opportunistic cleanup.** Fix outliers as files are
  touched. Zero upfront cost; drift continues; no mechanism.
- **B — Token source-of-truth + lint ratchet.** Add the scales new code
  references, then a custom-lint / CI grep that *fails on new* off-scale
  values and raw `Colors.*` (a ratchet — existing violations grandfathered,
  count only allowed to go down). Stops the bleeding without a big-bang
  migration. Highest leverage; needs the tokenize decision (§5.1) first.
- **C — Big-bang migration (the audit's P0–P3 as written).** Highest risk,
  large diff, much of it churn on a mobile surface we can't analyze
  locally (CI + device-test only). Not recommended as one unit.

## 7. Recommended slice (if we act)

Contained, high-ROI, low-regression — each its own PR, gated for no
behavior change:

1. **`chipTheme` + extract `AppChip`/`AppStatusChip`**, migrate the worst
   offenders first (the ×5/×4 duplicated classes). Biggest consistency win.
2. **Wire the amber secondary** into `ColorScheme.secondary`.
3. **File + fix the real WCAG miss** (`#6B7280` 3.39:1).
4. **Reconcile `ui-guidelines.md`** with the code (colors, radius default,
   the size table) so the named conventions are trustworthy again — cheap,
   unblocks any later ratchet.

Defer the tokenize-everything and eliminate-every-`Colors.*` work to a
lint-enforced ratchet (option B) *after* §5.1 is decided.

## 8. Open questions

> **Resolved** in [ADR-047](../decisions/047-design-system-enforcement.md):
> adopt named tokens (D-1); enforce on-scale discipline via a
> no-new-violations CI ratchet (D-9); single cyan brand accent with amber
> reserved for the warning semantic, secondary wired to a tonal cyan (D-5);
> flat-by-default with a standardized small elevation set (D-8). Retained
> below as the original framing.

- Do we adopt named tokens (reversing the documented stance), or keep
  inline values and only enforce *on-scale* discipline?
- Is a custom-lint ratchet worth the tooling cost on a single-app repo,
  or is review discipline enough?
- Monochrome accent by choice, or activate the amber secondary?
- Flat-by-default: remove the stray elevations, or standardise a small
  elevation set (0 / 4 / 8 / 12)?

## 9. Loose ends

- [`ui-guidelines.md`](../reference/ui-guidelines.md) is stale
  (v1.0.312): color table, radius default, and the secondary wiring all
  diverge from current code. Reconciling it is a prerequisite for trusting
  any "named convention."
- The audit's scope path (`/home/wb/termipod/lib/`) differs from this
  checkout but is cosmetic — the file structure matches.

## Related

- [`reference/ui-guidelines.md`](../reference/ui-guidelines.md) — the
  named design language (needs reconciliation, §9).
- [`reference/coding-conventions.md`](../reference/coding-conventions.md)
  — Flutter/Go style.
- [`spine/information-architecture.md`](../spine/information-architecture.md)
  — navigation IA (axiom-tier; out of scope here).
- Source: issue #71 (the original audit).
