# 048. Themed vocabulary overlay — role terms swap by preset × language

> **Type:** decision
> **Status:** Accepted (2026-06-10) — director-directed; promotes the
> vocabulary-theme wedge deferred in [the post-MVP domain-packs
> discussion](../discussions/post-mvp-domain-packs.md) and the
> [vocabulary reference](../reference/vocabulary.md) to **MVP**, triggered
> by a tester for whom "steward" did not fit their domain (issue #138).
> **Audience:** contributors · reviewers · principal
> **Last verified vs code:** v1.0.815

**TL;DR.** The app's role-bound nouns (steward, agent, principal, run,
deliverable, …) are hardcoded to one wording. A tester found that wording
("steward" / 管家) wrong for their domain. We add a **vocabulary preset**
dimension — a small term table resolved by `(preset, language)` that sits
*orthogonal* to gen-l10n: l10n keeps owning language (en/zh), the preset
owns the role wording. Four presets ship (**tech** default · **business**
= company · **political** = policy · **research** = academy), each in en
and zh. This is MVP, not the post-MVP marketplace; it is just the
"vocabulary" row of a future content pack, shipped early because a real
user is blocked.

> **Term note.** "Vocabulary preset" is deliberately *not* called a
> "theme" in load-bearing prose — **theme** already denotes the visual
> system (dark/light, design tokens, ADR-047). The
> [vocabulary reference](../reference/vocabulary.md) historically said
> "theme preset"; this ADR renames the dimension to **vocabulary preset**
> to remove the collision. A glossary entry lands with the implementation
> PR.

---

## Context

`docs/reference/vocabulary.md` already audited this: 21 role-bound **axes**
(`role.steward`, `role.agent`, `role.principal`, `entity.run`,
`entity.output`, `surface.attention`, …) that must co-vary when wording
changes, plus ~80% of strings that are *always-neutral* (buttons, errors,
SSH/tmux, settings) and never swap. It defined four presets and an
implementation sketch — but marked the swap a **future post-MVP wedge**
and **deferred all ZH equivalents**. No runtime exists; the ARB hardcodes
the `tech` wording.

Two things forced the question now:

1. **A tester is blocked.** "Steward" / 管家 reads wrong for their use; the
   role concept does not map to their mental model. This is a
   parity/short-board bug (roadmap "superset, not replacement"), not
   polish — the user cannot rename the role to fit their domain.
2. **Issue #138** (the i18n sweep — ~90 files / ~600 hardcoded English
   strings unlocalized) has to touch every role-bound string anyway. Doing
   the neutral i18n pass *and* the preset wedge in one program means each
   role-bound string is migrated once, into the right slot, rather than
   twice.

The vocabulary audit offered two shapes: **Shape A** (call-site resolver +
a small per-preset term map; l10n stays language-only and preset-neutral)
and **Shape B** (per-preset `.arb` packs, multiplying the ARB count by
presets × languages). The audit preferred Shape A.

## Decision

**Adopt Shape A, extended across language, as MVP.**

1. **Two orthogonal dimensions.** gen-l10n owns **language** (en/zh).
   A new **`VocabPack`** owns **preset wording**, resolved by
   `(preset, language)` → `axis → term forms`. Four presets × two
   languages × ~21 axes is a small table, *not* a duplicated ARB.

2. **Neutral strings stay in plain ARB** (en/zh), untouched by preset —
   ~80% of keys.

3. **Role-bound strings are templates with a `{role}` placeholder**,
   filled at render: `l10n.noAgentsYet(vocab.term(Axis.agent))`. The ARB
   sentence is language-only; the role noun comes from the active pack.
   Each axis stores the grammatical forms English needs (title/lower ×
   singular/plural); zh stores one. Where English grammar cannot be
   composed cleanly, that string falls back to a per-preset full key.

4. **Preset is a client setting now** — an enum
   `tech | business | political | research` beside the existing locale
   override in `settings_provider`, exposed by a `vocabProvider` and a
   settings picker. It graduates to a **hub-served / per-team** pack later
   (the post-MVP per-team-overlay story) without a rewrite. The **hub
   stays neutral**: `agents.kind = "steward."` and friends are data/ids;
   presets are display-only, client-side.

5. **Corrected preset wording.** The director set the role terms; they
   supersede the audit's draft English presets (which were wrong — 经理 is
   *Manager*, not the audit's "Chief of Staff"). The headline axes:

   | axis | tech (default) | business / 公司 | political / 政策 | research / 学术 |
   |---|---|---|---|---|
   | `role.steward` | Steward 管家 | Manager 经理 | Secretary 秘书 | Supervisor 主管 |
   | `role.principal` | Owner 负责人 | Boss 老板 | Leader 领导 | PI 课题组负责人 |
   | `role.agent` | Agent 智能体 | Specialist 专员 | Operative 干事 | Researcher 研究员 |

   The full 21-axis × 4-preset × {en, zh} matrix is authored in
   [the program plan](../plans/themed-vocabulary-and-i18n-sweep.md) and
   becomes the canonical table in `vocabulary.md` §2 when WS-A lands.

6. **A `lint-vocab.sh` gate** (bash + python3, mirroring
   `lint-design-tokens.sh` / `lint-arb.sh`) enforces that every axis is
   present in all eight packs with consistent forms. With no Flutter SDK
   on the build host, this and `lint-arb.sh` are the only *local* gates;
   CI runs the Flutter analyze/test/build.

## Consequences

- **The tester unblocks on the first slice.** WS-A delivers the picker
  plus `role.steward / role.principal / role.agent` wired into the
  steward-heavy surfaces (Sessions category headers, steward badge/config,
  the Me page). Switching the preset to company/policy/research replaces
  "steward / 管家" everywhere those axes render.
- **i18n and re-wording are one migration.** Every role-bound string moves
  to a `{role}` template once; neutral strings move to plain ARB once.
- **A new always-on invariant.** New user-facing strings must be triaged:
  neutral → ARB; role-bound → `{role}` template + axis tag. `lint-vocab` +
  the standing rule in `vocabulary.md` §6 hold the line.
- **No hub change, no ARB explosion.** Rejecting Shape B keeps the ARB at
  two language files; preset wording lives in one compact table.
- **Grammar debt is bounded but real.** English plural/case forms per axis
  add bookkeeping; the per-preset full-key fallback absorbs the cases
  composition cannot.
- **A term was renamed.** "Vocabulary preset" replaces "theme preset" in
  prose to avoid colliding with the visual theme; `vocabulary.md` and the
  glossary follow.
- **Forward path preserved.** Client-local presets graduate to hub-served
  per-team packs (post-MVP domain packs) by changing the pack *source*,
  not the call sites.

## References

- Reference: [`reference/vocabulary.md`](../reference/vocabulary.md)
  (axes, presets, standing rule)
- Plan: [`plans/themed-vocabulary-and-i18n-sweep.md`](../plans/themed-vocabulary-and-i18n-sweep.md)
  (workstreams, full matrix, sequencing)
- Discussion: [`discussions/post-mvp-domain-packs.md`](../discussions/post-mvp-domain-packs.md)
  (the content-pack frame this wedge is the first slice of)
- Related: [ADR-047](047-design-system-enforcement.md) (visual theme /
  design tokens — the *other* "theme"), [ADR-005](005-owner-authority-model.md)
  (principal / steward roles)
- Issue: #138 (i18n sweep + the tester's role-wording blocker)
