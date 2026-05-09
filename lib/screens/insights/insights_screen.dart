import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../providers/insights_provider.dart';
import '../../theme/design_colors.dart';
import '../../widgets/insights_breakdown_section.dart';
import '../../widgets/insights_panel.dart';

/// Fullscreen Insights view per ADR-022 D7 — "six entry points, one
/// fullscreen view, NO sixth bottom-nav tab." Activity AppBar opens it
/// (Phase 2 W2); Me Stats card (W3), Hosts Detail (W4), Agent Detail
/// (W4) will all push to this same screen with their own scope.
///
/// The body delegates to [InsightsPanel] so the metric tile rendering
/// stays in one place — the fullscreen view's job is to set the scope
/// label, run the refresh action, and hold space for future drilldown
/// sheets (Phase 2 W5 Tier-2 dimensions).
class InsightsScreen extends ConsumerWidget {
  final InsightsScope scope;

  const InsightsScreen({super.key, required this.scope});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;

    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Insights',
          style: GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700),
        ),
        actions: [
          IconButton(
            tooltip: 'Refresh',
            icon: const Icon(Icons.refresh),
            onPressed: () => ref.invalidate(insightsProvider(scope)),
          ),
        ],
      ),
      body: RefreshIndicator(
        onRefresh: () async {
          ref.invalidate(insightsProvider(scope));
          // Wait for the rebuild to land before the indicator dismisses
          // so the user sees the fresh tiles, not a flash of the old
          // ones.
          await ref.read(insightsProvider(scope).future);
        },
        child: ListView(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 32),
          children: [
            _ScopeBanner(scope: scope, muted: muted),
            const SizedBox(height: 16),
            InsightsPanel(scope: scope),
            const SizedBox(height: 24),
            // Phase 2 W5a — engine + model breakdown. Reads `by_engine`
            // and `by_model` from the same /v1/insights response the
            // panel already fetched, so this is pure-mobile cost.
            // Lifecycle flow / tool-call efficiency / unit economics /
            // snippet usage / multi-host distribution still pending.
            InsightsBreakdownSection(scope: scope),
          ],
        ),
      ),
    );
  }
}

/// Scope banner — labels the scope kind + id so the user knows what
/// they're looking at when they jump in from a non-project entry point.
/// Project scope tries to resolve the project's display name from
/// hubProvider; other scopes show the raw id (handle for engine, ULID
/// for the rest) since there's no name lookup at MVP.
class _ScopeBanner extends ConsumerWidget {
  final InsightsScope scope;
  final Color muted;
  const _ScopeBanner({required this.scope, required this.muted});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final hubState = ref.watch(hubProvider).value;
    final label = _labelForScope(scope, hubState);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        color: Theme.of(context).brightness == Brightness.dark
            ? DesignColors.surfaceDark
            : DesignColors.surfaceLight,
        borderRadius: BorderRadius.circular(10),
      ),
      child: Row(
        children: [
          Icon(_iconForScope(scope.kind), size: 16, color: muted),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              label,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 12,
                fontWeight: FontWeight.w600,
              ),
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
            ),
          ),
        ],
      ),
    );
  }

  static IconData _iconForScope(InsightsScopeKind kind) {
    switch (kind) {
      case InsightsScopeKind.project:
        return Icons.folder_outlined;
      case InsightsScopeKind.team:
        return Icons.groups_outlined;
      case InsightsScopeKind.agent:
        return Icons.smart_toy_outlined;
      case InsightsScopeKind.engine:
        return Icons.memory_outlined;
      case InsightsScopeKind.host:
        return Icons.dns_outlined;
    }
  }

  static String _labelForScope(InsightsScope scope, HubState? hub) {
    switch (scope.kind) {
      case InsightsScopeKind.project:
        final p = hub?.projects.firstWhere(
          (p) => (p['id']?.toString() ?? '') == scope.id,
          orElse: () => const <String, dynamic>{},
        );
        final name = (p?['name']?.toString() ?? '').trim();
        return name.isEmpty
            ? 'Project · ${scope.id}'
            : 'Project · $name';
      case InsightsScopeKind.team:
        return 'Team · ${scope.id}';
      case InsightsScopeKind.agent:
        return 'Agent · ${scope.id}';
      case InsightsScopeKind.engine:
        return 'Engine · ${scope.id}';
      case InsightsScopeKind.host:
        return 'Host · ${scope.id}';
    }
  }
}
