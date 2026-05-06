import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../providers/hub_provider.dart';
import '../screens/deliverables/structured_deliverable_viewer.dart';
import '../screens/projects/artifacts_screen.dart';
import '../screens/projects/blobs_section.dart';
import '../screens/projects/documents_screen.dart';
import '../screens/projects/plans_screen.dart';
import '../screens/projects/project_channels_list_screen.dart';
import '../screens/projects/runs_screen.dart';
import '../screens/projects/schedules_screen.dart';
import '../theme/design_colors.dart';

/// Closed registry of shortcut-tile slugs (template-yaml-schema §11).
/// Templates pick a per-phase subset; the chassis enforces the slug
/// vocabulary so a typo in YAML can't smuggle in a new tile.
enum TileSlug {
  outputs,
  documents,
  schedules,
  plans,
  assets,
  experiments,
  references,
  risks,
  discussion,
}

TileSlug? _slugFromString(String raw) {
  switch (raw.trim().toLowerCase()) {
    case 'outputs':
      return TileSlug.outputs;
    case 'documents':
      return TileSlug.documents;
    case 'schedules':
      return TileSlug.schedules;
    case 'plans':
      return TileSlug.plans;
    case 'assets':
      return TileSlug.assets;
    case 'experiments':
      return TileSlug.experiments;
    case 'references':
      return TileSlug.references;
    case 'risks':
      return TileSlug.risks;
    case 'discussion':
      return TileSlug.discussion;
  }
  return null;
}

/// Hardcoded research-template phase→tiles map. Mirrors
/// `docs/reference/research-template-spec.md` §3 exactly. W7 will pull
/// these from the project YAML and supersede this constant; W4 ships
/// the mobile rendering ahead of W7 so the demo path validates the
/// chassis cut without waiting on the template-loader work.
const Map<String, List<TileSlug>> _researchPhaseTiles = {
  'idea': [],
  'lit-review': [TileSlug.references, TileSlug.documents],
  'method': [
    TileSlug.references,
    TileSlug.documents,
    TileSlug.plans,
  ],
  'experiment': [
    TileSlug.outputs,
    TileSlug.documents,
    TileSlug.experiments,
  ],
  'paper': [TileSlug.outputs, TileSlug.documents],
};

/// Chassis default when no template/phase tile mapping applies. Aligned
/// with template-yaml-schema §11 ("`Outputs`, `Documents` if `tiles:`
/// is absent on a phase").
const List<TileSlug> _chassisDefault = [TileSlug.outputs, TileSlug.documents];

/// Resolves the tile set for a (template, phase) pair.
///
/// Resolution order:
///   1. Template YAML (W7) — not yet wired; placeholder for the future
///      `phaseTiles` argument.
///   2. Hardcoded research-template map for the well-known phase ids.
///   3. Chassis default `[Outputs, Documents]`.
List<TileSlug> resolveTilesForPhase({
  required String templateId,
  required String phase,
  Map<String, List<String>>? phaseTilesYaml,
}) {
  if (phaseTilesYaml != null && phaseTilesYaml.containsKey(phase)) {
    final raw = phaseTilesYaml[phase] ?? const <String>[];
    return raw.map(_slugFromString).whereType<TileSlug>().toList();
  }
  if (_researchPhaseTiles.containsKey(phase)) {
    return _researchPhaseTiles[phase]!;
  }
  return _chassisDefault;
}

/// Renders the template-declared shortcut tiles for the project's
/// current phase (W4 — IA §6.2 / template-yaml-schema §11). Replaces
/// the prior 7-hard-coded-tile strip on Overview with a phase-filtered
/// set; Reviews is intentionally absent (the orange attention banner
/// already serves it, plan §6.4 acceptance gap #4).
class ShortcutTileStrip extends ConsumerWidget {
  final String projectId;
  final String projectName;
  final String templateId;
  final String phase;
  final Map<String, List<String>>? phaseTilesYaml;

  const ShortcutTileStrip({
    super.key,
    required this.projectId,
    required this.projectName,
    required this.templateId,
    required this.phase,
    this.phaseTilesYaml,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final tiles = resolveTilesForPhase(
      templateId: templateId,
      phase: phase,
      phaseTilesYaml: phaseTilesYaml,
    );
    if (tiles.isEmpty) {
      return _NoTilesPlaceholder(phase: phase);
    }
    return Column(
      children: [
        for (final t in tiles) ...[
          _TileRow(
            slug: t,
            projectId: projectId,
            projectName: projectName,
          ),
          const SizedBox(height: 8),
        ],
      ],
    );
  }
}

class _NoTilesPlaceholder extends StatelessWidget {
  final String phase;
  const _NoTilesPlaceholder({required this.phase});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        color: isDark
            ? DesignColors.surfaceDark.withValues(alpha: 0.5)
            : DesignColors.surfaceLight.withValues(alpha: 0.5),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(
          color: isDark
              ? DesignColors.borderDark
              : DesignColors.borderLight,
        ),
      ),
      child: Row(
        children: [
          const Icon(Icons.chat_outlined,
              size: 14, color: DesignColors.textMuted),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              phase.isEmpty
                  ? 'No shortcuts for this project — keep the conversation going.'
                  : 'Conversation-first phase — no shortcut tiles for "$phase".',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                color: DesignColors.textMuted,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _TileRow extends ConsumerWidget {
  final TileSlug slug;
  final String projectId;
  final String projectName;
  const _TileRow({
    required this.slug,
    required this.projectId,
    required this.projectName,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final spec = tileSpecFor(slug);
    return InkWell(
      borderRadius: BorderRadius.circular(8),
      onTap: () => _open(context, ref),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        decoration: BoxDecoration(
          color:
              isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
          borderRadius: BorderRadius.circular(8),
          border: Border.all(
            color: isDark
                ? DesignColors.borderDark
                : DesignColors.borderLight,
          ),
        ),
        child: Row(
          children: [
            Icon(spec.icon, size: 18, color: DesignColors.primary),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    spec.label,
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 14,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  const SizedBox(height: 2),
                  Text(
                    spec.subtitle,
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 11,
                      color: DesignColors.textMuted,
                    ),
                  ),
                ],
              ),
            ),
            const Icon(Icons.chevron_right, size: 18),
          ],
        ),
      ),
    );
  }

  Future<void> _open(BuildContext context, WidgetRef ref) async {
    // References needs an async resolve: route to the project's
    // lit-review deliverable when one exists (its body carries
    // citations + prior-work), else fall back to the Documents screen.
    if (slug == TileSlug.references) {
      await _openReferences(context, ref);
      return;
    }
    Widget page;
    switch (slug) {
      case TileSlug.outputs:
        page = ArtifactsScreen(projectId: projectId);
      case TileSlug.documents:
        page = DocumentsScreen(projectId: projectId);
      case TileSlug.schedules:
        page = const SchedulesScreen();
      case TileSlug.plans:
        page = const PlansScreen();
      case TileSlug.assets:
        page = const _AssetsHostScreen();
      case TileSlug.experiments:
        page = RunsScreen(projectId: projectId);
      case TileSlug.references:
        // Handled by the early return above.
        return;
      case TileSlug.risks:
        page = _StubScreen(
          title: 'Risks',
          message: 'Risk register lands with W5b/W7.',
        );
      case TileSlug.discussion:
        page = ProjectChannelsListScreen(
          projectId: projectId,
          projectName: projectName,
        );
    }
    if (!context.mounted) return;
    Navigator.of(context).push(MaterialPageRoute(builder: (_) => page));
  }

  Future<void> _openReferences(BuildContext context, WidgetRef ref) async {
    final client = ref.read(hubProvider.notifier).client;
    Map<String, dynamic>? litReviewDeliverable;
    if (client != null && projectId.isNotEmpty) {
      try {
        final dls = await client.listDeliverables(
          projectId: projectId,
          includeComponents: true,
        );
        for (final d in dls) {
          if ((d['kind'] ?? '').toString() == 'lit-review') {
            litReviewDeliverable = d;
            break;
          }
        }
      } catch (_) {
        // Swallow — fall through to the Documents screen below.
      }
    }
    if (!context.mounted) return;
    if (litReviewDeliverable != null) {
      Navigator.of(context).push(MaterialPageRoute(
        builder: (_) => StructuredDeliverableViewer(
          projectId: projectId,
          deliverableId: (litReviewDeliverable!['id'] ?? '').toString(),
          initialDeliverable: litReviewDeliverable,
        ),
      ));
      return;
    }
    // No lit-review deliverable yet — open the Documents screen so the
    // director can see whatever the steward has in flight, with a
    // breadcrumb that explains References will fill in once the
    // lit-review phase ratifies a doc.
    Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => DocumentsScreen(projectId: projectId),
    ));
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(
        content: Text(
          'No lit-review document yet — showing all project documents. '
          'References will land here once the lit-review deliverable ratifies.',
        ),
      ),
    );
  }
}

class TileSpec {
  final String label;
  final String subtitle;
  final IconData icon;
  const TileSpec({
    required this.label,
    required this.subtitle,
    required this.icon,
  });
}

/// Public for tests / future template-yaml lookup. The chassis owns the
/// label + icon mapping; templates only pick which slugs to surface.
TileSpec tileSpecFor(TileSlug slug) {
  switch (slug) {
    case TileSlug.outputs:
      return const TileSpec(
        label: 'Outputs',
        subtitle: 'Outputs runs produce · checkpoints, curves, reports',
        icon: Icons.output_outlined,
      );
    case TileSlug.documents:
      return const TileSpec(
        label: 'Documents',
        subtitle: 'Authored writeups · memos, drafts, reports',
        icon: Icons.article_outlined,
      );
    case TileSlug.schedules:
      return const TileSpec(
        label: 'Schedules',
        subtitle: 'Recurring firings across the team',
        icon: Icons.schedule_outlined,
      );
    case TileSlug.plans:
      return const TileSpec(
        label: 'Plans',
        subtitle: 'Plan templates the steward executes',
        icon: Icons.playlist_play_outlined,
      );
    case TileSlug.assets:
      return const TileSpec(
        label: 'Assets',
        subtitle: 'Browse media from channels · standalone uploads',
        icon: Icons.perm_media_outlined,
      );
    case TileSlug.experiments:
      return const TileSpec(
        label: 'Experiments',
        subtitle: 'ML training/eval runs (blueprint §6.5)',
        icon: Icons.science_outlined,
      );
    case TileSlug.references:
      return const TileSpec(
        label: 'References',
        subtitle: 'Citations · SOTA library · prior art',
        icon: Icons.menu_book_outlined,
      );
    case TileSlug.risks:
      return const TileSpec(
        label: 'Risks',
        subtitle: 'Open risks · mitigations · status',
        icon: Icons.warning_amber_outlined,
      );
    case TileSlug.discussion:
      return const TileSpec(
        label: 'Discussion',
        subtitle: 'Channels · steward thread',
        icon: Icons.chat_outlined,
      );
  }
}

/// Lightweight host so the Assets tile can push a Scaffold-titled page
/// without tying every caller to the private `_BlobsScreen` inside the
/// project detail file.
class _AssetsHostScreen extends StatelessWidget {
  const _AssetsHostScreen();

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Assets',
          style: GoogleFonts.spaceGrotesk(
            fontSize: 18,
            fontWeight: FontWeight.w700,
          ),
        ),
      ),
      body: const BlobsSection(),
    );
  }
}

class _StubScreen extends StatelessWidget {
  final String title;
  final String message;
  const _StubScreen({required this.title, required this.message});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          title,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 18,
            fontWeight: FontWeight.w700,
          ),
        ),
      ),
      body: Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            message,
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 13,
              color: DesignColors.textMuted,
            ),
          ),
        ),
      ),
    );
  }
}
