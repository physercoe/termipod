import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../providers/insights_provider.dart';
import '../../theme/design_colors.dart';
import '../../widgets/insights_breakdown_section.dart';
import '../../widgets/insights_by_agent_section.dart';
import '../../widgets/insights_host_distribution.dart';
import '../../widgets/insights_lifecycle_section.dart';
import '../../widgets/insights_panel.dart';
import '../../widgets/insights_tools_section.dart';

/// Fullscreen Insights view per ADR-022 D7 — "six entry points, one
/// fullscreen view, NO sixth bottom-nav tab." Activity AppBar opens it
/// (Phase 2 W2); Me Stats card (W3), Hosts Detail (W4), Agent Detail
/// (W4) push to this same screen with their own scope. Sessions
/// AppBar (steward wedge) opens it with `teamStewards` scope.
///
/// The body delegates to [InsightsPanel] so the metric tile rendering
/// stays in one place — the fullscreen view's job is to set the scope
/// label, run the refresh action, hold the time-range chip row, and
/// hold space for future drilldown sheets.
class InsightsScreen extends ConsumerStatefulWidget {
  final InsightsScope scope;

  const InsightsScreen({super.key, required this.scope});

  @override
  ConsumerState<InsightsScreen> createState() => _InsightsScreenState();
}

/// Time-range chips for the InsightsScreen. Each maps to a (since,
/// until) pair where `until = now` and `since = now - duration`.
/// 24h is the same default the hub aggregator uses when no since is
/// passed; we send it explicitly so the cache key carries the window.
enum _Range { day, week, month }

extension on _Range {
  String get label => switch (this) {
        _Range.day => '24h',
        _Range.week => '7d',
        _Range.month => '30d',
      };

  Duration get duration => switch (this) {
        _Range.day => const Duration(days: 1),
        _Range.week => const Duration(days: 7),
        _Range.month => const Duration(days: 30),
      };
}

class _InsightsScreenState extends ConsumerState<InsightsScreen> {
  _Range _range = _Range.day;

  /// Build the windowed scope for the currently-selected chip. The
  /// `until` is fixed to "now" rather than rebuilt on every frame so
  /// the family-provider cache key stays stable inside one chip view.
  /// (The screen-level `_now` resets on chip change in `_setRange`.)
  late DateTime _now = DateTime.now().toUtc();

  void _setRange(_Range r) {
    setState(() {
      _range = r;
      _now = DateTime.now().toUtc();
    });
  }

  InsightsScope get _windowedScope => widget.scope.withWindow(
        since: _now.subtract(_range.duration),
        until: _now,
      );

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final scope = _windowedScope;

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
            const SizedBox(height: 8),
            _RangePicker(
              selected: _range,
              onChanged: _setRange,
            ),
            const SizedBox(height: 16),
            InsightsPanel(scope: scope),
            const SizedBox(height: 24),
            // Phase 2 W5a — engine + model breakdown.
            InsightsBreakdownSection(scope: scope),
            // Steward wedge — per-agent breakdown. Each row taps into
            // an agent-scoped InsightsScreen drill-in.
            InsightsByAgentSection(scope: scope),
            // Phase 2 W5c — tool-call efficiency.
            InsightsToolsSection(scope: scope),
            // Phase 2 W5d — lifecycle (project scope only).
            InsightsLifecycleSection(scope: scope),
            // Phase 2 W5b — multi-host distribution.
            InsightsHostDistribution(scope: scope),
          ],
        ),
      ),
    );
  }
}

/// Time-range chip row. Three options for MVP — 24h / 7d / 30d.
/// Selecting a chip replaces the parent's scope and re-keys the
/// family provider so the panel + breakdowns refetch from the hub
/// (or render their cached snapshot for that window first).
class _RangePicker extends StatelessWidget {
  final _Range selected;
  final ValueChanged<_Range> onChanged;
  const _RangePicker({required this.selected, required this.onChanged});

  @override
  Widget build(BuildContext context) {
    return Wrap(
      spacing: 8,
      children: [
        for (final r in _Range.values)
          ChoiceChip(
            label: Text(r.label),
            selected: r == selected,
            onSelected: (sel) {
              if (sel) onChanged(r);
            },
            labelStyle: GoogleFonts.jetBrainsMono(
              fontSize: 12,
              fontWeight: FontWeight.w600,
            ),
          ),
      ],
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
      case InsightsScopeKind.teamStewards:
        return Icons.support_agent_outlined;
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
      case InsightsScopeKind.teamStewards:
        return 'Stewards · ${scope.id}';
      case InsightsScopeKind.agent:
        return 'Agent · ${scope.id}';
      case InsightsScopeKind.engine:
        return 'Engine · ${scope.id}';
      case InsightsScopeKind.host:
        return 'Host · ${scope.id}';
    }
  }
}
