# Doc freshness maintenance — tiers and anchors

> **Type:** discussion
> **Status:** Open (2026-05-24)
> **Audience:** contributors
> **Last verified vs code:** v1.0.673
> **Freshness:** rolling

**TL;DR.** Maintaining ~200 docs by re-reading each one whenever code
moves is intractable. This doc proposes a two-mechanism fix that
scales: (1) **stratify** docs into three freshness contracts
(`contract` / `rolling` / `snapshot`) so the audit scope collapses to
the ~30 docs that actually need to track code; (2) embed **anchor
markers** in the load-bearing claims so a lint script greps the
cited symbols and fails the build when they no longer exist. Tier 3
— AI-native per-tag re-verification via the
[probing harness](agent-driven-system-probing.md) — is the natural
follow-on but is not required for the bulk of the win. This is a
concrete proposal for the open questions raised in
[ai-native-codebase-legibility.md §8](ai-native-codebase-legibility.md#8-open-questions);
it operationalises that doc's freshness-SLA thesis.

---

## 1. The problem framed correctly

At ~200 docs and a fast-moving codebase, "re-verify every doc on
every release" is intractable as a process: even a 5-minute audit
per doc is a day of work per release. The instinct to "make CI block
on stale docs" runs immediately into 179 outstanding stale-WARNs and
would freeze all contribution until they're addressed.

But the framing itself is wrong. The right question is not
*"how do I keep 200 docs fresh?"* It is *"which doc-code couplings
matter, and how do I make them automatically detectable?"*

Two observations make this tractable:

1. **The coupling graph is sparse, not dense.** A bugfix in
   `agent_feed.dart` has no relationship to ADR-014 or `glossary.md`.
   A change to `hub/internal/server/policy.go` may affect three docs.
   A renumbering of migrations may affect five. The vast majority of
   `(doc, commit)` pairs are unrelated and need no audit.
2. **Most docs are not contracts with code.** Discussion docs
   capture debate at a moment in time — they're a *snapshot*, not a
   live mirror. Shipped plans are historical. Archived ADRs are
   frozen. Of the ~200 docs, perhaps 30 — the ones a contributor or
   agent *will read to act* — are actually load-bearing for
   correctness.

Both observations point at the same fix: **scope the freshness
contract, and make the load-bearing parts self-verifying.**

---

## 2. The existing mechanism — `lint-docs.sh` stale warning

`lint-docs.sh` already does the right *shape* of work:

- Every doc declares `Last verified vs code: vX.Y.Z` in its status
  block (per doc-spec §3).
- The linter compares each declared version to the current
  `pubspec.yaml` version. If the gap exceeds `STALE_THRESHOLD = 5`
  minor versions, a `WARN [stale-doc]` line prints.
- The check is non-failing — drift accumulates without blocking
  contribution.

As of v1.0.673, this produces 179 stale-WARNs. The signal is
correct (these docs *are* stale by the time threshold), but it's
**uniform** — it tells you a discussion from 2026-01 is "300
versions stale" with the same urgency as a how-to that drives
release validation. That uniformity is the bug.

---

## 3. Tier 1 — freshness contracts (three tiers)

Add a fourth status-block field, `Freshness:`. Three legal values:

| Value | Semantics | Lint behaviour |
|---|---|---|
| `contract` | Doc is a live mirror of code. Drift IS wrongness. | **Fails CI** when gap > threshold. |
| `rolling` | Doc is the current statement, but drift is acceptable. | Warns at threshold; current default behaviour. |
| `snapshot` | Doc captures a moment in time; later drift doesn't make it wrong. | **No drift check.** |

**Sensible defaults by primitive** (used when `Freshness:` is
absent, for backward compatibility):

| Primitive | Default |
|---|---|
| AXIOM (spine) | `rolling` |
| VISION (roadmap) | `rolling` |
| PLAN (Proposed / In flight) | `contract` |
| PLAN (Done / Deferred / Cancelled) | `snapshot` |
| DECISION (Proposed) | `contract` |
| DECISION (Accepted) | `rolling` |
| DECISION (Superseded / Deprecated) | `snapshot` |
| REFERENCE | `contract` |
| HOW-TO | `rolling` |
| DISCUSSION | `snapshot` |
| TUTORIAL | `contract` |
| ARCHIVE | `snapshot` |

The author can override the default by writing the field
explicitly. The defaults are not enforced — they're the value the
linter substitutes when the field is missing, biased toward
backward compatibility (most defaults are `rolling`, matching
existing behaviour). Authors elect to escalate a doc to `contract`
when they want CI to defend it.

**Why these defaults.** Proposed ADRs need to track the code they
describe; accepted ADRs are append-only and the prose doesn't go
stale (only the references at the bottom drift, which Tier 2
addresses). Reference docs (glossary, coding-conventions) are
`contract` because their whole purpose is to be a single source of
truth. Discussions are `snapshot` because they document debate;
re-reading them post-facto with new context is fine. Tutorials are
`contract` because broken tutorials destroy contributor trust on
first contact.

**Rollout.** Phase 1 ships the field + lint gating, all defaults
preserve current behaviour (mostly `rolling`, which warns
identically to today). No CI breakage. Phase 2 is incremental: as
contributors touch a doc, they pick the right tier and re-verify;
over time the `contract` tier grows and the `snapshot` tier
absorbs the 100+ discussion / archive docs that should never have
been WARN-tagged in the first place.

---

## 4. Tier 2 — anchor markers (self-verifying claims)

The expensive thing about re-verifying a doc isn't *time*; it's
re-greping every file:line and function name the doc cites to
confirm they still exist. The
[ADR-030 audit](../decisions/030-governed-actions-and-propose-verb.md) earlier
this session checked 11 such claims by hand. Each was a 5-second
grep. The aggregate was 10 minutes — tractable for one doc,
intractable across the contract tier.

**Mechanism.** Embed inline HTML comments that name a verifiable
fact:

```markdown
The reload loop in `policy.go:65` <!-- verify symbol hub/internal/server/policy.go reload -->
reads the file on every mtime change.

Migration 0044 (handle normalization) <!-- verify file hub/migrations/0044_strip_handle_at_prefix.up.sql -->
is occupied; migration 0099 is unused <!-- verify no-file hub/migrations/0099_*.up.sql -->.

There are 10 bundled steward templates <!-- verify glob hub/templates/prompts/steward.*.md 10 -->.
```

The markers are HTML comments — invisible in rendered Markdown,
visible in source, no schema migration of any prior doc required.

**Marker kinds** (MVP):

| Kind | Args | Check |
|---|---|---|
| `file` | `<path>` | File exists |
| `no-file` | `<glob>` | No file matches |
| `symbol` | `<file> <name>` | `\b<name>\b` appears in `<file>` |
| `glob` | `<pattern> <expected_count>` | Exactly N files match |

The four kinds cover the audit work I did on ADR-030 1:1. Line
numbers are deliberately not a primary marker — they're
inherently brittle (any insertion above shifts them). Authors are
encouraged to anchor to **symbols**, not lines; the line number in
the prose stays as a navigation hint, but the authoritative check
is the symbol presence.

**New script:** `scripts/lint-doc-anchors.sh`. Walks every
contract / rolling doc, extracts markers, runs each check.
`FAIL [broken-anchor]` exits 1; clean exits 0.

**What this catches:**

- Rename a function → every doc citing it fails until updated.
- Delete a migration → every doc referencing it fails.
- Add a new bundled template without updating glob counts in
  W12-style prose → fails.

**What this doesn't catch:**

- Semantic drift: the function still exists but its behaviour
  changed in a way the doc doesn't reflect. This is an
  irreducible AI / human review responsibility (and the
  motivation for Tier 3).

---

## 5. Tier 3 — AI-native re-verification (deferred to L1b harness)

Already on the roadmap as the L1b wedge in
[`agent-driven-system-probing.md`](agent-driven-system-probing.md).
The shape:

1. At every release tag, dispatch one probe agent per `freshness:
   contract` doc.
2. Each probe re-reads the doc, greps every anchor + extracts every
   uncited factual claim it can identify (function names, schema
   columns, file paths in code spans, version cites).
3. Probe emits a `{clean, drift, broken}` report using the
   probing-harness report contract.
4. A reviewer agent reads the reports; on `clean`, auto-bumps the
   `Last verified vs code:` stamp via a PR.

This catches the semantic drift Tier 2 can't catch, and removes
the residual human burden of bumping stamps on docs whose anchors
still all verify. It builds on the probing-harness infrastructure
that's separately tracked, so it doesn't block Tier 1 + Tier 2.

---

## 6. What this avoids (rejected alternatives)

- **"AI rewrites all docs every release."** Bad — hallucinates,
  normalises prose, loses authorial voice, and doesn't scale to the
  reviewer's attention budget.
- **"Block every PR until all docs verify."** Bad — discourages
  drive-by fixes, makes the codebase hostile to new contributors,
  forces every author to become a doc auditor.
- **"Build a versioned docs site (Docusaurus / Antora)."** Bad —
  solves a different problem (consumer-facing version selection);
  doesn't reduce author burden and adds a build pipeline.
- **"Couple every doc to the release process."** Already what
  `lint-docs.sh` half-does; the failure mode is uniform staleness
  with no signal about which gaps matter.

---

## 7. Implementation order

Tier 1 → Tier 2 → Tier 3, in cost order and dependency order:

- **Tier 1 alone** drops the maintenance scope from 200 → ~30
  contract docs without breaking any current CI. Free; one doc-spec
  update + one lint-docs.sh extension.
- **Tier 2** makes those ~30 self-verifying. One new shell script
  (~150 LOC); incremental adoption — anchor markers added per doc
  as authors touch each one.
- **Tier 3** automates re-verification at tag, so even the 30
  don't need human attention until lint actually fires. Bigger
  lift, but builds on the probing-harness work already on the
  roadmap.

Tier 1 + Tier 2 ship in the same commit that introduces this doc.
Tier 3 is left as a follow-up.

---

## 8. Open questions

These remain for future contributors:

- **Should `contract` docs require anchor markers?** A `contract`
  doc with no anchor markers can still drift in ways the version-
  stamp gate alone won't catch. Should `lint-doc-anchors.sh` warn
  when a `contract` doc has zero markers? (Proposed: yes, with a
  one-cycle grace period to let the backlog of contract docs add
  markers without breaking CI on day 1.)
- **What counts as the "current version" for staleness math?**
  Today it's `pubspec.yaml`. If we ever decouple hub releases from
  the mobile app version, the staleness math needs a second axis
  per doc — or just use git tag count.
- **Tutorials in particular are tricky.** A tutorial that runs
  end-to-end against the current binary is the strongest possible
  contract, but they're hard to auto-verify without actually
  running them. Tier 3 (probe agent + sandbox host-runner) is the
  obvious resolution but it's a real wedge.
- **Anchor marker syntax sprawl.** The MVP has 4 kinds. As authors
  use it, they'll want more (regex match? JSON-path into a config
  file? line-range bounds?). Each new kind expands the marker
  registry; we should resist sprawl and prefer composition (a
  `verify file` plus a `verify symbol` already cover most of what a
  regex would).

---

## 9. Related

- [`ai-native-codebase-legibility.md`](ai-native-codebase-legibility.md)
  — the foundational thesis (freshness SLA, drift between map and
  territory). This doc operationalises §5 + §7 + §8 of that one.
- [`agent-driven-system-probing.md`](agent-driven-system-probing.md)
  — the AI-native harness Tier 3 builds on.
- [`../doc-spec.md`](../doc-spec.md) §3 — the status-block contract
  this proposal extends with the new `Freshness:` field.
- [`../../scripts/lint-docs.sh`](../../scripts/lint-docs.sh) — the
  existing stale-WARN linter being upgraded to read the new field.
- [`../../scripts/lint-glossary.sh`](../../scripts/lint-glossary.sh)
  — the prior-art template for `lint-doc-anchors.sh`.
- [`../../CLAUDE.md`](../../CLAUDE.md) — the convention
  *Verify, don't guess* the anchor markers make mechanical.
