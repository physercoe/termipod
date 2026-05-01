# Doc backfill — load-bearing rationale

> **Type:** plan
> **Status:** In flight (2026-05-01)
> **Audience:** contributors (humans + AI agents)
> **Last verified vs code:** v1.0.350-alpha
> **Resolves:** [discussions/post-rebrand-doc-audit.md](../discussions/post-rebrand-doc-audit.md) Tier 1+2+3

**TL;DR.** Close the 19 load-bearing doc gaps surfaced in the post-rebrand audit. Doc work runs as a coding-equivalent workstream — committed in tiers, not opportunistically. Tier 1 (~6h) before next external review · Tier 2 (~7h) before next contributor onboarding · Tier 3 (~4h) localized expansions.

---

## Why

Doc backfill is structural work. Decisions made and lessons learned in flight aren't real until they're written into a primitive a future reviewer can trust. The audit found 1,343 substantive design exchanges across the past month — most resolved into code, ADRs, or memory, but 19 load-bearing rationale gaps remain. Those gaps are how mid-2025-style "tribal knowledge" rot starts. We close them as deliberate planned work, not "later when there's time."

## Out of scope

- **Pass-3 nice-to-have items** (audit §4, 25 items). Land opportunistically when adjacent code is touched.
- **Doc-spec compliance sweep** (audit §5.3). Separate plan.
- **Tutorials/ population** (audit §5.2). Separate plan.

## Wedges

Each wedge ships as one or more atomic doc commits. Status moves Proposed → In flight → Done with a verified-vs-code version. ADR numbers are dispense-on-creation per doc-spec §6 (no reservation).

### Tier 1 — pre-review (target: 6h)

| ID | Topic | Audit § | Action | Effort |
|---|---|---|---|---|
| T1-A | Fork-detach + rebrand | §3.1 | New ADR (claims #015 — closes the numbering gap) | 1h |
| T1-B | Layered stewards (general + domain) | §3.6 | New ADR + `reference/steward-templates.md` + ADR-004 status block update | 2h |
| T1-C | Lifecycle amendment rationale | §3.13 | New `discussions/lifecycle-amendment-2026-04.md` | 1h |
| T1-D | Snippet / action-bar reference | §3.16 | New `reference/action-bar-system.md` | 1h |
| T1-E | ADR-015 numbering (subsumed by T1-A) | §5.1 | Note in `decisions/README.md` | 5min |
| T1-F | ADR-004 status update (chained to T1-B) | §3.19 | Edit status block | 5min |

### Tier 2 — pre-onboarding (target: 7h)

| ID | Topic | Audit § | Action | Effort |
|---|---|---|---|---|
| T2-A | Personal-tool / hub-mvp positioning | §3.2 | Expand `discussions/positioning.md` | 1h |
| T2-B | Permission model (3 modes + vendor asymmetry) | §3.7 | New `reference/permission-model.md` + sidebar in ADR-011 D6 | 1.5h |
| T2-C | Governance roles axiom | §3.8 | New `spine/governance-roles.md` | 1h |
| T2-D | Attention → notification surface mapping | §3.11 | New `reference/attention-delivery-surfaces.md` | 1h |
| T2-E | Rate-limit / token-bucket model | §3.12 | New `reference/rate-limiting.md` | 45min |
| T2-F | 2026 competitive landscape synthesis | §3.14 | New `reference/competitive-landscape-2026.md` | 1.5h |

### Tier 3 — localized expansions (target: 4h)

| ID | Topic | Audit § | Action | Effort |
|---|---|---|---|---|
| T3-A | Tailnet deployment assumption | §3.3 | New ADR | 45min |
| T3-B | Channels as event log | §3.4 | New ADR | 1h |
| T3-C | Three-layer host-runner rationale | §3.5 | Expand `spine/blueprint.md` §3.4 | 45min |
| T3-D | Manager/IC invariant (folded into T1-B) | §3.9 | Inline in T1-B's layered-stewards ADR + glossary entry | 30min |
| T3-E | Multi-writer fork invariant | §3.10 | Expand ADR-014 | 30min |
| T3-F | Activity feed schema | §3.15 | New `reference/audit-events.md` | 1h |
| T3-G | Persistent steward Me-tab card | §3.17 | Amend `spine/information-architecture.md` §6.1 | 30min |
| T3-H | Schedules instantiate plans rationale | §3.18 | Expand `spine/blueprint.md` forbidden #11 | 30min |

## Verification

- After each tier ships, update audit §2 gap matrix — `gap` cells become `✓` or `~`.
- After Tier 3, audit §6 should have zero outstanding load-bearing items.
- Audit doc itself moves to Resolved when matrix has no `gap` cells.

## Provenance

Tier and effort estimates from [post-rebrand-doc-audit §6](../discussions/post-rebrand-doc-audit.md). Source dossiers under `/tmp/audit/dossiers/` (not checked in, reproducible).
