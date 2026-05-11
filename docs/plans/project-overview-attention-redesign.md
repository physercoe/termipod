---
name: Project overview attention redesign
description: Tighten the project detail page's attention budget — drop the redundant Discussion AppBar icon (route via existing Discussion tile + per-project editor), collapse outer metadata rows + Archive action into a "More" ExpansionTile, and extend `/v1/insights?scope=team` with `by_project[]` so the Projects-list AppBar can carry a cross-project overview icon.
---

# Project overview attention redesign

> **Type:** plan
> **Status:** Open (2026-05-11)
> **Audience:** principal · contributors · QA
> **Last verified vs code:** v1.0.484

**TL;DR.** The project detail page accumulates surfaces faster than
the user needs them: the Overview tab body has six vertical regions
(attention banner / PortfolioHeader / phase hero / tile strip /
InsightsPanel / metadata + Archive), plus AppBar icons for
Discussion and Template-YAML, plus 5 tab pills, plus chassis
PhaseRibbon. That's twelve interaction zones competing for
above-fold attention. This plan applies the three attention
principles from the prior discussion (Orient → Focus → Explore) to
shrink visible chrome without losing routes, and adds the
team-overview surface the Projects-list page is missing.

Three small wedges. No new MCP tools. No template-schema change.
One hub-side endpoint extension. Risks are explicitly post-MVP.

## Goal

After this plan ships:

- Project detail AppBar has one fewer icon (Discussion); Discussion
  remains reachable via the per-project tile editor (v1.0.484).
- The Overview tab body's "below-the-divider" metadata + Archive
  block defaults to collapsed; the hero+tiles+InsightsPanel chain
  is the visual focus.
- Projects-list AppBar has a cross-project Insights icon routing
  to a `TeamOverviewInsightsScreen` showing one card per project
  (phase, status, progress, attention, open ACs, last activity).

## Non-goals

- **PhaseRibbon changes.** Stays chassis-level above the pill bar;
  past-phase tap → `_openPhaseSummary` is already implemented.
- **PortfolioHeader changes.** Goal de-burial + Show-details
  expander shipped in lifecycle W3; this plan doesn't touch it.
- **InsightsPanel changes.** Already inline above the divider; no
  reframe needed — the panel is already Tier-1 by ADR-022 D3.
- **Risks tile / risks register.** Explicitly post-MVP per
  principal 2026-05-11. The `risks` TileSlug stays in the closed
  enum; nothing surfaces it; backing data doesn't land here.
- **Workspaces in cross-project view.** Standing-kind projects
  have no phase / progress columns set, would render as
  degenerate rows. Filter them out from `by_project[]`; workspaces
  get their own surface post-MVP.

## Wedges

### W1 — Drop the Discussion AppBar icon (project detail)

**Scope.** Remove the `Icons.chat_outlined` IconButton at
`project_detail_screen.dart:190-201`. Discussion remains
reachable via:

- The `TileSlug.discussion` tile (already in the closed enum),
  routing to `ProjectChannelsListScreen`. User adds it to the
  current phase composition via the v1.0.484
  `PhaseTileEditorSheet`.
- Existing deep links to `ProjectChannelsListScreen` (no change).

No template YAML edit — *don't* push Discussion into default
research-phase tiles; the editor is the authoritative
per-project composition mechanism. This wedge trusts the v1.0.484
machinery to do its job.

**Files touched:**

- `lib/screens/projects/project_detail_screen.dart` — remove the
  IconButton block.

**Test plan:**

- AppBar renders without the chat icon.
- Tile-editor sheet still offers Discussion; tapping the resulting
  tile pushes `ProjectChannelsListScreen`.

**LOC estimate:** ~10 mobile.

### W2 — Collapse outer metadata + Archive into "More" ExpansionTile

**Scope.** The `_OverviewView` body at `project_detail_screen.dart:1198-1227`
renders a metadata rows loop (Name / Kind / Status / Goal /
Steward template / On-create template / ID / Docs root / Created)
followed by an Archive button. Wrap both in a single
`ExpansionTile` titled "Details" (or "More"), default collapsed.
Goal stays in PortfolioHeader where it already lives.

The InsightsPanel + Divider chain above stays inline. The
collapse boundary is the existing visual divider.

**Files touched:**

- `lib/screens/projects/project_detail_screen.dart` — wrap rows +
  Archive in `ExpansionTile`.

**Test plan:**

- Overview tab renders with the "Details" expander collapsed by
  default.
- Expanding reveals every metadata row + Archive button intact.
- Archive flow (showDialog → archiveProject) still works.

**LOC estimate:** ~80 mobile.

### W3 — Cross-project Insights AppBar icon + `by_project[]`

**Scope.** Two parts — hub backend + mobile surface.

**Hub:**

- Extend `/v1/insights?team_id=X` (scope=team) and `team_stewards`
  with a new optional `by_project[]` array in the response.
- New row type `insightsProjectAgg` (per open-questions Q2 + Q3):
  ```go
  type insightsProjectAgg struct {
    ProjectID     string  `json:"project_id"`
    Name          string  `json:"name"`
    CurrentPhase  string  `json:"current_phase"`
    Status        string  `json:"status"`
    Progress      float64 `json:"progress"`     // weighted; see Q2
    OpenAttention int     `json:"open_attention"`
    OpenCriteria  int     `json:"open_criteria"` // pending + failed
    TokensIn      int64   `json:"tokens_in"`
    TokensOut     int64   `json:"tokens_out"`
    LastActivity  string  `json:"last_activity"` // ISO ts; max(events.created_at)
  }
  ```
- Aggregation:
  - `progress = (phases_done + current_phase_AC_ratio) / phases_total`
    where `phases_done` = count of `phase_history` rows for this
    project with non-null `exited_at`; `current_phase_AC_ratio`
    = met / (pending + met + failed + waived) for the project's
    current_phase row; `phases_total` = count of phases in the
    project's template.
  - `open_criteria` = `count(*) where state IN ('pending', 'failed')`.
  - `last_activity` = `max(agent_events.created_at WHERE project_id = ?)`.
- Filter: `kind != 'standing'` (workspaces out per Q3).
- Sort: `last_activity DESC` server-side; hard cap 100 rows.
- Empty on `agent` / `engine` / `host` / `project` scope.

**Mobile:**

- Add AppBar action on `lib/screens/projects/projects_screen.dart`
  (between `TeamSwitcher` and `Refresh`): `Icons.insights_outlined`
  → routes to new `TeamOverviewInsightsScreen`.
- New screen: list of project cards (one per `by_project[]` row).
  Each card shows: name + phase chip + status pill + progress
  bar + attention badge + open-criteria badge + last-activity
  relative-time. Tap → push project detail.
- Reuses `InsightsScreen(scope: team(teamId))` data fetch — the
  same response carries `by_project[]`, just renders differently.
  Two callers, one data path.

**Files touched:**

- `hub/internal/server/handlers_insights.go` — add `ByProject`
  field + aggregation loop.
- `hub/internal/server/handlers_insights_test.go` — happy +
  workspace-filter + progress-formula + sort tests.
- `lib/screens/projects/projects_screen.dart` — AppBar action.
- `lib/screens/insights/team_overview_insights_screen.dart` —
  new.
- `lib/services/hub/hub_client.dart` — extend the Insights call
  result type to include `byProject`.

**Test plan:**

- `/v1/insights?team_id=X` returns `by_project[]` for goal-kind
  projects; workspaces excluded.
- `progress` matches the weighted formula on a project mid-phase.
- `last_activity` is correctly latest; rows sorted desc.
- Mobile: AppBar icon visible on Projects screen; tap pushes the
  new screen; card renders all fields; tap-card pushes project
  detail.

**LOC estimate:** ~150 mobile + ~250 hub + ~120 hub tests.

## Total budget

- ~240 mobile + ~370 hub LOC. ~1 working day (mobile-only W1 + W2
  in an hour each; W3 splits across a hub afternoon + a mobile
  afternoon).
- No new APK dep. No migration.

## Dependencies on other plans

- Companion to [`artifact-type-registry.md`](artifact-type-registry.md)
  and [`agent-artifact-rendering-tier-1.md`](agent-artifact-rendering-tier-1.md)
  — those lock the artifact axis; this plan locks the layout axis.
- ADR-022 (Insights surface scope-parameterization) — this plan
  extends D3 with a new dimension on team scope; not a re-decision.

## Rollout

1. W1 + W2 land together as a single mobile-only commit. Visible
   chrome cleanup; no behavioural regression.
2. W3 hub change lands next (additive; safe for older mobile
   builds that don't know about `by_project[]`).
3. W3 mobile change lands last; AppBar icon + new screen.
4. Tag alpha once W3 mobile is in.

## Open questions

Resolved 2026-05-11 against the prior discussion. Recommended
answers locked here; principal sign-off via review is still
welcome but does not block W1/W2 implementation.

### Resolved

**Q1 — Where does Discussion go when the AppBar icon drops?**
**Resolved (a):** route via the existing `TileSlug.discussion`
tile + per-project editor. Don't push Discussion into default
template phase compositions — the editor is the authoritative
per-project mechanism and v1.0.484 already shipped it. Users
who relied on the AppBar icon get one extra tap (open editor →
add Discussion to current phase) in exchange for AppBar real
estate consistent with the rest of the IA.

**Q2 — Progress formula.**
**Resolved (c):** `progress = (phases_done + current_phase_AC_ratio) / phases_total`.

- `phases_done`: count of `phase_history` rows with non-null
  `exited_at` for this project.
- `current_phase_AC_ratio`: `met / (pending + met + failed + waived)`
  for ACs scoped to the project's current_phase. Zero if no ACs.
- `phases_total`: count of phases declared in the project's
  template (read from the cached template YAML).

This formula avoids the rubber-banding of pure AC-ratio at phase
advance and the granularity loss of pure phases-done. Matches
the smooth-monotonic expectation users carry over from Linear /
Asana progress bars.

**Q3 — Workspaces in `by_project[]`.**
**Resolved (a):** filter out `kind = 'standing'`. Workspaces have
no phase / progress columns set; including them with null fields
forces every consumer to handle two row shapes. Workspaces
overview is a separate post-MVP plan.

### Resolved at implementation (recorded so they don't drift)

- **What stays inline vs collapses in W2.** Outer metadata rows
  + Archive collapse. Goal stays in PortfolioHeader (already
  shipped). InsightsPanel stays inline above the divider.
- **`open_criteria` definition.** Counts `state IN ('pending', 'failed')`.
- **Sort order of project cards.** `last_activity DESC`. Pin /
  star is a follow-up.
- **Pagination.** Server-side hard cap 100; no pagination
  controls in W3 mobile. Revisit if a team's project count
  approaches the cap.

## Status

Open (2026-05-11). Drafted alongside the artifact-type-registry
plan (commit 4759289) after principal review of the v1.0.484
lifecycle arc identified the project-detail attention budget as
the next bottleneck. No commits yet against this plan; W1/W2 land
together in the first commit after sign-off, W3 in the second.
