# Simple vs advanced mode for the mobile UI

> **Type:** discussion
> **Status:** Open (post-demo; revisit after Candidate-A hardware run lands)
> **Audience:** principal, contributors
> **Last verified vs code:** v1.0.319

**TL;DR.** The 5-tab IA (Projects · Activity · Me · Hosts · Settings)
serves a *technical* principal well but exposes operator-grade
surfaces (Activity = audit log, Hosts = machine inventory) to
non-technical users. This memo frames the design space — five paths
ordered by effort — and recommends path 5 (fold Activity/Hosts
highlights into Me) as the cleanest long-term shape, with path 1
(Settings toggle) as the cheap interim. Decision deferred until
post-MVP demo.

---

## 1. The observation

The current bottom-nav matches `docs/spine/information-architecture.md`
§6.1: Projects · Activity · Me · Hosts · Settings. Each tab serves a
purpose; the question is **whose purpose**.

| Tab | User-facing? | Why |
|---|---|---|
| **Projects** | ✅ | "What am I working on" |
| **Me** | ✅ | "What needs my attention" — pending approvals, urgent tasks, digest |
| **Settings** | ✅ | App config |
| **Activity** | ⚠️ | `audit_events` timeline. Granular: actor IDs, technical event names, run state transitions, channel post events. Reads like a log file. |
| **Hosts** | ⚠️ | Physical machine inventory: `host_id`, `runner_commit`, `last_seen_at`, capabilities probe. Non-tech users don't think in terms of machines — they think in terms of "where the AI runs," and they want it to land sensibly without having to pick. |

ADR-005 establishes the user as principal/director, not operator. By
that lens, Activity and Hosts are operator-shaped surfaces leaking
into the principal's nav. They're *correct* for the technical
principal we built for first; they're *misplaced* for a less-technical
principal we may serve later.

---

## 2. Design space — five paths

Ordered by engineering effort, lowest first.

### Path 1 — Settings toggle: "Show developer tabs"

A single boolean in Settings:

```
☐ Show developer tabs (Activity, Hosts)
```

Off by default for new installs; on by default for existing users
(don't surprise current behavior).

**Pros:** trivial implementation. ~20 LoC. Reversible per-user.

**Cons:** discoverability — power users have to know the toggle
exists in Settings to find Activity/Hosts when they need them.
Mitigated by adding a one-time hint in Me: "Looking for Activity?
Enable in Settings → Display."

### Path 2 — Role-based visibility

The IA already names roles (director / steward / reviewer / member /
observer / council). Tabs filter per role: a `director` sees the
full 5; a `member` sees Projects + Me + Settings only.

**Pros:** correct long-term answer. Aligns with ADR-005's
director-vs-operator distinction. Reuses an axis the IA already has.

**Cons:** ADR-004 defers per-member roles to post-MVP (single-user
team today, no role assignment). Implementing this now means adding
a per-user setting that *acts like* a role until real per-member
roles ship — same as path 1 mechanically. So path 2 is path 1 with a
roadmap onramp; not different effort today.

### Path 3 — Progressive disclosure within tabs

Keep all 5 tabs but make Activity and Hosts non-technical by default:

- **Activity** defaults to a "noteworthy events" filter (decisions,
  reviews, agent failures, schedule fires) — hides routine events
  (channel posts, individual tool calls). A "Show all events" toggle
  reveals the raw audit feed.
- **Hosts** collapses to a status pill ("✅ 2 hosts online · 1 away")
  when the user has ≤3 hosts. Tap to drill into the per-host detail
  for management.

**Pros:** no nav change; users keep all entry points. Solves the
"reads like a log" problem at the content level.

**Cons:** more complex to implement (need event categorization, host
collapse logic). Doesn't reduce the 5-tab cognitive load.

### Path 4 — Move Activity / Hosts to Settings drawers

Bottom-nav drops to 3 tabs (Projects · Me · Settings). Activity and
Hosts become Settings sub-pages: `Settings → Activity log`,
`Settings → Hosts`.

**Pros:** clean for non-technical users; bottom-nav is exclusively
principal-shaped. Aligns with how OS settings demote infrequently-used
panels.

**Cons:** loses one-tap access for power users. Settings is
muscle-memory-deep; "go check Activity" becomes "open Settings → tap
Activity log → wait for it to load." For a developer-leaning user
that's friction.

### Path 5 — Fold highlights into Me; demote Activity/Hosts to drilldowns

Me already aggregates principal-level info (attention, reviews, my
work, digest). Add two cards:

- **Recent activity digest** — top 3 noteworthy events from the last
  24h, with "View all activity" tap-target opening the full Activity
  screen as a routed (not bottom-nav) view.
- **Hosts status** — "✅ 2 hosts online" pill with "Manage hosts"
  tap-target opening the Hosts screen as a routed view.

Bottom-nav reduces to 3 tabs (Projects · Me · Settings). Activity
and Hosts remain *screens* — addressable, deep-linkable, fully
functional — but no longer claim a slot in the principal's nav.

**Pros:** best UX for the common user. Power users still get one-tap
access via Me's drilldown cards. Me becomes the *one* tab that
matters when you open the app to "see where things stand."

**Cons:** Me starts to feel crowded — needs care with card
prioritization so the digest doesn't drown the attention queue.
Largest implementation: card components on Me, route changes for
Activity/Hosts, possibly settings to hide the cards.

---

## 3. Recommendation

**Long-term:** Path 5 — Me as the cockpit, Activity/Hosts as
drilldowns. This matches the "director, not operator" principle
most cleanly and keeps the principal's nav free of operator-shaped
surfaces.

**Cheap interim if path 5 is too far:** Path 1 — a Settings toggle
that hides Activity/Hosts. Defaults off for new users, on for
existing. Ships in a wedge or two. Buys learning ("do users actually
miss Activity when it's hidden?") before committing to a structural
IA change.

**Don't do path 4** in isolation — pushing Activity/Hosts to Settings
without surfacing their highlights elsewhere makes the principal
unaware that their AI agents are even running. Path 5 fixes this by
keeping the highlights on Me; path 4 doesn't.

**Don't do path 2 yet** — it's path 1 plus a per-member roles
infrastructure that's gated on ADR-004's deferral. The mechanical
effort today is the same as path 1; the conceptual integrity comes
later when roles actually exist.

**Path 3 is a content polish** that's worth doing regardless — even
if Activity stays as a tab, "noteworthy events" filtering is a
better default than the raw feed. Could land independently as a
small wedge.

---

## 4. Decision criteria — when to revisit

This is post-MVP. Revisit when one of these triggers fires:

- **Hardware Candidate-A demo lands and is recorded** (the MVP
  milestone). At that point we have a real demo to show non-technical
  reviewers, and their feedback on the IA is signal we should act on.
- **Second user onboards** (per ADR-004's per-member-stewards trigger).
  At that point role-based visibility (path 2) becomes load-bearing
  and we're forced to address the structure.
- **Mobile usage analytics show Activity/Hosts unused** (per the
  deferred analytics in `../plans/research-demo-gaps.md` — when local
  stats land, low engagement on Activity/Hosts becomes a quantitative
  signal).

Until one of those fires, the cost of structural change isn't
justified by the friction.

---

## 5. Related

- ADR-005 (`../decisions/005-owner-authority-model.md`) —
  director-vs-operator distinction
- ADR-004 (`../decisions/004-single-steward-mvp.md`) — per-member
  roles deferred (rules out path 2 today)
- IA spec (`../spine/information-architecture.md`) §6.1 — the
  current 5-tab layout; §11 — the wedge plan that shipped
- `../discussions/post-mvp-domain-packs.md` — overlapping
  consideration of how non-technical users encounter the system
