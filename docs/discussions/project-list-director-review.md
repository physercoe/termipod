# Project list page: director-mental-model review

> **Type:** discussion
> **Status:** Open — deferred to post-MVP (2026-05-05)
> **Audience:** contributors
> **Last verified vs code:** v1.0.351

**TL;DR.** The Projects tab is the director's portfolio surface — the
place where they triage what to attend to. Today it answers "what
projects exist?" but answers "which need me, which are stalled, what
are they for?" only by drill-down. The director's mental model
(grounded in Drucker, Grove, Cagan, and validated against
Linear/Asana/Notion/GitHub/Height conventions) asks ~8 questions in a
specific priority order; the current row format collapses several
dimensions into one slot and silently drops recency when attention
exists. This doc captures the gap analysis and a proposed two-layer
reframe (portfolio header + denser two-line row), but **the list page
is not on the MVP path** — execution focus is the Project Detail
surface (see `project-detail-lifecycle-architecture.md`). Listed here
so the analysis isn't lost when we come back to it.

---

## 1. Why this discussion exists

The Projects tab grew from the original Hub list. It's functional but
hasn't been audited against the director persona since the IA reorg
shipped (v1.0.175–v1.0.182). Two specific concerns prompted this:

1. The single trailing slot per row holds *either* the attention badge
   *or* the creation timestamp — never both — silently hiding recency
   when attention exists.
2. There's no portfolio-level summary at the top of the tab, so a
   director with >10 projects has no glanceable triage signal.

The page is not urgent for MVP (most users have <5 projects), but the
mental-model framing is reusable for the Project Detail review.

---

## 2. Director's mental model — frameworks

Four converging traditions agree on what a manager scans for and in
what order. Naming them so the analysis is grounded, not taste-driven.

| Tradition | Contribution to the mental model |
|---|---|
| **Drucker (MBO) / Doerr (OKR)** | Objectives first. A project list without visible *purpose* is a task list, not a portfolio. |
| **Grove (High Output Mgmt) / Goldratt (TOC)** | Manage by exception. The blocked/red items demand 80% of attention; healthy work needs near-zero pixels. |
| **Cagan (Inspired) / Horowitz (Good PM)** | Outcomes over output. Surprise minimization — early-warning beats lagging totals. |
| **Kahneman (System 1/2) + Tufte** | Glanceable visual encoding (color, position, size) is parsed in <500ms; text needs 2–5s. Don't make the eye read what a dot can say. |

Cross-validated by Linear, Jira, Asana, Notion, GitHub Projects,
Height — every mature PM tool, having converged independently,
exposes: *status pill (color-coded) · owner avatar · last-activity
timestamp · counts (open/blocked) · filter+sort bar · group/segment
toggle.* That convergence is signal, not coincidence.

---

## 3. Director's question priority

Validated against PM dashboard literature (Few, Tufte, IBM Carbon,
Material 3 dashboards):

1. **Is anything on fire?** (red status, missed deadline, escalated attention)
2. **Where am I needed?** (attention items mine, reviews mine)
3. **What's moving / what's stalled?** (last-activity recency)
4. **What's each project trying to achieve?** (goal/objective text)
5. **Who's running it?** (owner / steward / agent count)
6. **What did we ship?** (outcomes, artifacts) — only when drilling in
7. **Search/filter by my own slicing** (mine, by status, by kind)
8. **Create new** — last, because it's frequent but not first-glance

Termipod's twist: the director directs **agents**, not people. So
"owner" = steward; "what's moving" = *agent activity heartbeat*; "needs
me" = `attention_items` (already first-class). This *strengthens* the
mental model — surprise minimization matters more when an LLM is
acting on your behalf overnight.

---

## 4. Audit of the current Projects tab

What's there:

- Two-section split (Projects / Workspaces) by `kind` — strong, matches
  "bounded outcome vs ongoing container" mental model.
- Attention badge per row, with child rollup — strong.
- Sub-project indent + rail — strong.
- Create FAB — present, well-placed.
- Pull-to-refresh — present.

Gaps in director-priority order:

| # | Gap | Director question affected |
|---|---|---|
| 1 | No color-coded status pill — status is text in subtitle (e.g. "active", "blocked") | Q1 — needs <500ms parsing |
| 2 | No last-activity timestamp — a 3-week-stale project looks identical to a churning one | Q3 |
| 3 | No goal/objective excerpt — only project name | Q4 |
| 4 | No owner/steward indicator — no avatar, no agent count | Q5 |
| 5 | No filter/sort affordance — order is creation-order; no "Mine", "Active", "Needs me", "Recent" | Q7 |
| 6 | No portfolio rollup at top — "3 need you, 2 stalled, 1 over budget" header would be a 1-line dashboard for free | Q1+Q2 combined |
| 7 | No empty-state coaching for new directors — `projectsEmpty` is a hint string but no visual onboarding | cold-start UX |
| 8 | Trailing slot conflates two things — attention badge OR creation timestamp, never both. Recency is silently dropped when attention exists. | Q1 + Q3 collision |

Crucially, the row's **trailing slot is the highest-value pixel**
(rightmost, last-scanned, sticky to the thumb's reach) and it
currently flips between two unrelated signals.

---

## 5. Proposed reframe (deferred — not for MVP)

Two-layer architecture mapping to System 1 / System 2 cognition:

### Layer A — top-of-tab portfolio strip (System 1, 1-line)

```
▾ All projects · 12 total · 4 need you · 2 stalled
[All] [Mine] [Needs me] [Stalled] [Recent]
```

Optional filter chips below. Sticky on scroll for triage tools (Few,
*Information Dashboard Design*).

### Layer B — denser two-line row

```
[●green]  Project Name                         [3⚠]
          Goal text excerpt · 2h ago
          @steward · 4 agents
```

Slot-by-slot:

- **Leading:** colored status dot. Kind chip can move into the title
  row or merge with the dot (kind+status are orthogonal — encode kind
  as dot shape or keep chip).
- **Title line:** name (unchanged) · trailing attention badge (always,
  when >0).
- **Subtitle line 1:** goal excerpt (1 line, ellipsis) · `·` ·
  last-activity ago.
- **Subtitle line 2:** owner/steward + agent count (only when >0).
- **No timestamp/attention conflict** — they're on different lines.

### Sectioning candidates (one of)

- Keep current Projects/Workspaces split (kind-based) — most stable.
- Add a "Needs you" pinned section above both — attention-first per
  IA-A1.
- Group by status (Active / Stalled / Done) — Kanban mental model;
  potentially noisy.

---

## 6. Tradeoffs

1. **Density vs scan-speed.** Two-line rows hurt list-density but each
   line answers a different mental-model question. Linear/Notion
   default to two-line; one-line is dashboard, two-line is workspace.
2. **Color discipline.** Status colors need a single source of truth;
   currently `status` is freeform text. Either constrain hub-side to a
   small enum or derive a "health" field from status + budget +
   activity.
3. **Last-activity source.** `audit_events` already exists. Cheap to
   compute `MAX(at) GROUP BY project_id` server-side; or piggyback on
   existing fetches.
4. **Filter persistence.** Should "Mine" / "Stalled" survive across
   app launches (sticky filter)? Linear yes; Asana no. Sticky reduces
   re-tap cost but risks "where did my projects go?"
5. **Portfolio strip = fixed or scrollaway?** Sticky costs 32–48px
   always. Scrollaway means it's invisible after the first scroll.
   Sticky preferred for triage tools.
6. **Roles still apply.** Per IA-A5, the same hierarchy must hold for
   Reviewer/Observer/Member — most filter chips degrade gracefully
   ("Mine" still means "assigned to me").

---

## 7. Recommendation

Defer this work to post-MVP. The list page is functionally adequate
for <10 projects (the demo target) and the highest-leverage UX work is
on Project Detail (lifecycle-aware overview, structured proposal
viewer, steward liveness). When we do come back:

1. **Lock the question-priority frame** in §3 (or amend it).
2. **Pick 2–3 highest-leverage gaps** — likely status color, last-activity,
   goal excerpt — covers Q1/Q3/Q4, the three you currently can't answer
   at a glance.
3. **Defer portfolio strip + filters** to a follow-up wedge — they only
   pay off above ~10 projects, and the demo target is well below that.

---

## 8. References

- `spine/information-architecture.md` §6.2 — Projects tab IA
- `spine/blueprint.md` §6.8 — attention rollup
- `screens/` — current screen designs
- `discussions/project-detail-lifecycle-architecture.md` — sister
  discussion (the urgent one)
