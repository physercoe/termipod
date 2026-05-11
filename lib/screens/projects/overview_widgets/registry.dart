import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../../theme/design_colors.dart';
import 'children_status.dart';
import 'recent_artifacts.dart';
import 'research_phase_heroes.dart';
import 'sweep_compare.dart';
import 'task_milestone_list.dart';
import 'workspace_overview.dart';

/// Context bag passed to every hero widget. The Overview chassis resolves
/// the shared project shape once and hands it to whichever hero the
/// template selected, so individual widgets stay dumb and swap cleanly.
///
/// Fields are intentionally minimal right now: widgets pull the rest
/// (tasks, artifacts, runs, children) off Riverpod via projectId. Keep
/// the surface small so adding a new hero in a future wedge only has to
/// lean on what's here plus `ref`.
class OverviewContext {
  /// Raw project row as returned by `/v1/teams/{team}/projects/{id}`.
  /// Hero widgets may read additional fields (goal, kind, template_id)
  /// directly off this map; no Project model class exists yet.
  final Map<String, dynamic> project;

  const OverviewContext({required this.project});

  String get projectId => (project['id'] ?? '').toString();
}

/// Canonical list of hero widget kinds. Must stay in sync with the hub
/// enum in `hub/internal/server/init.go` (`validOverviewWidgets`). An
/// unknown string coming off the wire degrades to a muted placeholder,
/// not a null render, so a stray enum value stays obvious in-app.
const Set<String> kKnownOverviewWidgets = {
  'task_milestone_list',
  'sweep_compare',
  'recent_artifacts',
  'children_status',
  'recent_firings_list',
  // W7 — research-template heroes (A6 §2 + §3-§7).
  'portfolio_header',
  'idea_conversation',
  'deliverable_focus',
  'experiment_dash',
  'paper_acceptance',
};

/// Default hero when the project has no template or the template did not
/// declare an overview_widget. Mirrors the hub's overviewWidgetDefault.
const String kDefaultOverviewWidget = 'task_milestone_list';

/// Human-readable label + short description for each hero slug, used by
/// the hero picker on `PhaseTileEditorSheet` (ADR-024 D10). Unknown
/// slugs fall through to a generic "Custom widget" label so the picker
/// stays useful across version drift.
class OverviewWidgetSpec {
  final String label;
  final String subtitle;
  const OverviewWidgetSpec({required this.label, required this.subtitle});
}

const Map<String, OverviewWidgetSpec> kOverviewWidgetSpecs = {
  'task_milestone_list': OverviewWidgetSpec(
    label: 'Tasks + milestones',
    subtitle: 'Default goal-project hero · task list with progress',
  ),
  'sweep_compare': OverviewWidgetSpec(
    label: 'Sweep compare',
    subtitle: 'Cross-run scatter for ablation-style sweeps',
  ),
  'recent_artifacts': OverviewWidgetSpec(
    label: 'Recent artifacts',
    subtitle: 'Latest outputs across the project · checkpoints + reports',
  ),
  'children_status': OverviewWidgetSpec(
    label: 'Children status',
    subtitle: 'Tree of sub-projects · phase + status per child',
  ),
  'recent_firings_list': OverviewWidgetSpec(
    label: 'Recent firings',
    subtitle: 'Standing-project default · last schedule firings',
  ),
  'portfolio_header': OverviewWidgetSpec(
    label: 'Portfolio header',
    subtitle: 'Minimal pointer hero · phase ribbon does the work',
  ),
  'idea_conversation': OverviewWidgetSpec(
    label: 'Idea conversation',
    subtitle: 'Conversation-first; nudge to ratify scope criterion',
  ),
  'deliverable_focus': OverviewWidgetSpec(
    label: 'Deliverable focus',
    subtitle: 'Single active deliverable · tap to ratify / send back',
  ),
  'experiment_dash': OverviewWidgetSpec(
    label: 'Experiment dashboard',
    subtitle: 'Mixed deliverable: report + artifacts + runs',
  ),
  'paper_acceptance': OverviewWidgetSpec(
    label: 'Paper draft',
    subtitle: 'Paper synthesis · ratify draft to close the project',
  ),
};

OverviewWidgetSpec overviewWidgetSpecFor(String slug) {
  return kOverviewWidgetSpecs[slug] ??
      OverviewWidgetSpec(
        label: slug.isEmpty ? 'Default widget' : slug,
        subtitle: 'Custom widget',
      );
}

/// Resolve a wire value to the widget kind to actually render. Empty /
/// unknown → the default. Caller is free to pass the raw wire string.
String normalizeOverviewWidget(String? raw) {
  final v = (raw ?? '').trim();
  if (v.isEmpty) return kDefaultOverviewWidget;
  if (kKnownOverviewWidgets.contains(v)) return v;
  return kDefaultOverviewWidget;
}

/// Dispatch on the declared hero kind. Called once from the Overview
/// body, below the portfolio header (A+B chassis, IA §6.2).
Widget buildOverviewWidget(String? kind, OverviewContext ctx) {
  final resolved = normalizeOverviewWidget(kind);
  switch (resolved) {
    case 'task_milestone_list':
      return TaskMilestoneListHero(ctx: ctx);
    case 'sweep_compare':
      return SweepCompareHero(ctx: ctx);
    case 'recent_artifacts':
      return RecentArtifactsHero(ctx: ctx);
    case 'children_status':
      return ChildrenStatusHero(ctx: ctx);
    case 'recent_firings_list':
      // W6 Workspace default hero. Usable as a template-declared hero
      // for goal projects too, though Workspace Overview is its primary
      // surface.
      return RecentFiringsList(ctx: ctx);
    case 'portfolio_header':
      return PortfolioHeaderHero(ctx: ctx);
    case 'idea_conversation':
      return IdeaConversationHero(ctx: ctx);
    case 'deliverable_focus':
      return DeliverableFocusHero(ctx: ctx);
    case 'experiment_dash':
      return ExperimentDashHero(ctx: ctx);
    case 'paper_acceptance':
      return PaperAcceptanceHero(ctx: ctx);
    default:
      return _UnknownOverviewHero(kind: kind ?? '');
  }
}

/// Rendered only if the hub hands us a widget kind newer than this build
/// knows about. We prefer a visible placeholder over silently degrading
/// to the default — the user needs a pointer to update the app.
class _UnknownOverviewHero extends StatelessWidget {
  final String kind;
  const _UnknownOverviewHero({required this.kind});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 14),
      decoration: BoxDecoration(
        color: DesignColors.surfaceDark.withValues(alpha: 0.3),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: DesignColors.borderDark),
      ),
      child: Text(
        'Unknown overview widget: $kind',
        style: GoogleFonts.jetBrainsMono(
          fontSize: 11,
          color: DesignColors.textMuted,
        ),
      ),
    );
  }
}
