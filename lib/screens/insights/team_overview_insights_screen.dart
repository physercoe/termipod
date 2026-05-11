import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../providers/insights_provider.dart';
import '../../theme/design_colors.dart';
import '../projects/project_detail_screen.dart';

/// Cross-project rollup surfaced by the AppBar Insights icon on the
/// Projects list. Reads `by_project[]` from `/v1/insights?team_id=X`
/// (hub-side extension W3 of plan
/// `project-overview-attention-redesign.md`). One card per goal-kind,
/// non-archived project, sorted by last-activity desc. Workspaces are
/// filtered out server-side.
///
/// Tap a card to open the corresponding project detail.
class TeamOverviewInsightsScreen extends ConsumerWidget {
  final String teamId;
  const TeamOverviewInsightsScreen({super.key, required this.teamId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final scope = InsightsScope.team(teamId);
    final async = ref.watch(insightsProvider(scope));
    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Team overview',
          style: GoogleFonts.spaceGrotesk(
            fontSize: 18,
            fontWeight: FontWeight.w700,
          ),
        ),
        actions: [
          IconButton(
            tooltip: 'Refresh',
            icon: const Icon(Icons.refresh),
            onPressed: () => ref.invalidate(insightsProvider(scope)),
          ),
        ],
      ),
      body: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => _ErrorView(message: '$e'),
        data: (cached) {
          final body = cached.body;
          final raw = body['by_project'];
          if (raw is! List || raw.isEmpty) {
            return const _EmptyView();
          }
          final rows = <_ProjectRow>[];
          for (final entry in raw) {
            if (entry is! Map) continue;
            final m = entry.cast<String, dynamic>();
            final id = (m['project_id'] ?? '').toString();
            if (id.isEmpty) continue;
            rows.add(_ProjectRow(
              projectId: id,
              name: (m['name'] ?? '').toString(),
              currentPhase: (m['current_phase'] ?? '').toString(),
              status: (m['status'] ?? '').toString(),
              progress: _double(m['progress']),
              openAttention: _int(m['open_attention']),
              openCriteria: _int(m['open_criteria']),
              lastActivity: (m['last_activity'] ?? '').toString(),
            ));
          }
          if (rows.isEmpty) return const _EmptyView();
          return RefreshIndicator(
            onRefresh: () async {
              ref.invalidate(insightsProvider(scope));
              await ref.read(insightsProvider(scope).future);
            },
            child: ListView.separated(
              padding: const EdgeInsets.all(12),
              itemCount: rows.length,
              separatorBuilder: (_, __) => const SizedBox(height: 8),
              itemBuilder: (_, i) => _ProjectCard(row: rows[i]),
            ),
          );
        },
      ),
    );
  }
}

class _ProjectRow {
  final String projectId;
  final String name;
  final String currentPhase;
  final String status;
  final double progress;
  final int openAttention;
  final int openCriteria;
  final String lastActivity;

  const _ProjectRow({
    required this.projectId,
    required this.name,
    required this.currentPhase,
    required this.status,
    required this.progress,
    required this.openAttention,
    required this.openCriteria,
    required this.lastActivity,
  });
}

class _ProjectCard extends ConsumerWidget {
  final _ProjectRow row;
  const _ProjectCard({required this.row});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final progressClamped = row.progress.clamp(0.0, 1.0).toDouble();
    final pct = (progressClamped * 100).round();
    return InkWell(
      borderRadius: BorderRadius.circular(10),
      onTap: () {
        // Look up the full project map off hubProvider; ProjectDetailScreen
        // takes a Map, not just an id. If the hub state isn't loaded yet
        // (unlikely — we got here via the same hubProvider) the tap is a
        // no-op rather than a crash.
        final projects =
            ref.read(hubProvider).value?.projects ?? const [];
        final match = projects.firstWhere(
          (p) => (p['id'] ?? '').toString() == row.projectId,
          orElse: () => const <String, dynamic>{},
        );
        if (match.isEmpty) return;
        Navigator.of(context).push(
          MaterialPageRoute(
            builder: (_) => ProjectDetailScreen(project: match),
          ),
        );
      },
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        decoration: BoxDecoration(
          color: bg,
          borderRadius: BorderRadius.circular(10),
          border: Border.all(color: border),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(
                    row.name.isEmpty ? '—' : row.name,
                    overflow: TextOverflow.ellipsis,
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 14,
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                ),
                if (row.openAttention > 0) ...[
                  const SizedBox(width: 6),
                  _Badge(
                    label: '${row.openAttention}',
                    icon: Icons.flag_outlined,
                    color: DesignColors.warning,
                  ),
                ],
              ],
            ),
            const SizedBox(height: 6),
            Wrap(
              spacing: 6,
              runSpacing: 4,
              crossAxisAlignment: WrapCrossAlignment.center,
              children: [
                if (row.currentPhase.isNotEmpty)
                  _Pill(label: row.currentPhase),
                _StatusPill(status: row.status),
                if (row.openCriteria > 0)
                  _Badge(
                    label: '${row.openCriteria} open AC',
                    icon: Icons.check_circle_outline,
                    color: DesignColors.textMuted,
                  ),
              ],
            ),
            const SizedBox(height: 8),
            Row(
              children: [
                Expanded(
                  child: ClipRRect(
                    borderRadius: BorderRadius.circular(3),
                    child: LinearProgressIndicator(
                      value: progressClamped,
                      minHeight: 4,
                      backgroundColor: isDark
                          ? DesignColors.borderDark
                          : DesignColors.borderLight,
                      valueColor: const AlwaysStoppedAnimation(
                          DesignColors.primary),
                    ),
                  ),
                ),
                const SizedBox(width: 8),
                Text(
                  '$pct%',
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 10,
                    fontWeight: FontWeight.w700,
                    color: DesignColors.textMuted,
                  ),
                ),
              ],
            ),
            if (row.lastActivity.isNotEmpty) ...[
              const SizedBox(height: 6),
              Text(
                _relativeTime(row.lastActivity),
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 10,
                  color: DesignColors.textMuted,
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

class _Pill extends StatelessWidget {
  final String label;
  const _Pill({required this.label});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: DesignColors.primary.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
        border:
            Border.all(color: DesignColors.primary.withValues(alpha: 0.4)),
      ),
      child: Text(
        label,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
          fontWeight: FontWeight.w700,
          color: DesignColors.primary,
        ),
      ),
    );
  }
}

class _StatusPill extends StatelessWidget {
  final String status;
  const _StatusPill({required this.status});

  @override
  Widget build(BuildContext context) {
    final label = status.isEmpty ? 'active' : status;
    final color = switch (label) {
      'active' => DesignColors.terminalGreen,
      'archived' => DesignColors.textMuted,
      _ => DesignColors.primary,
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: color.withValues(alpha: 0.5)),
      ),
      child: Text(
        label,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
          fontWeight: FontWeight.w700,
          color: color,
        ),
      ),
    );
  }
}

class _Badge extends StatelessWidget {
  final String label;
  final IconData icon;
  final Color color;
  const _Badge({
    required this.label,
    required this.icon,
    required this.color,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: color.withValues(alpha: 0.5)),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 11, color: color),
          const SizedBox(width: 3),
          Text(
            label,
            style: GoogleFonts.jetBrainsMono(
              fontSize: 10,
              fontWeight: FontWeight.w700,
              color: color,
            ),
          ),
        ],
      ),
    );
  }
}

class _EmptyView extends StatelessWidget {
  const _EmptyView();

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Text(
          'No projects in this team yet.\nCreate one from the Projects list.',
          textAlign: TextAlign.center,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 13,
            color: DesignColors.textMuted,
          ),
        ),
      ),
    );
  }
}

class _ErrorView extends StatelessWidget {
  final String message;
  const _ErrorView({required this.message});

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Text(
          'Could not load team overview.\n$message',
          textAlign: TextAlign.center,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 13,
            color: DesignColors.error,
          ),
        ),
      ),
    );
  }
}

int _int(dynamic v) {
  if (v is int) return v;
  if (v is num) return v.toInt();
  if (v is String) return int.tryParse(v) ?? 0;
  return 0;
}

double _double(dynamic v) {
  if (v is double) return v;
  if (v is num) return v.toDouble();
  if (v is String) return double.tryParse(v) ?? 0;
  return 0;
}

/// Format an ISO-8601 ts as a coarse relative time. Mirrors the project
/// list's compact "5m ago / 2h ago / 3d ago" formatting; long-form
/// timestamps are not useful at glance density.
String _relativeTime(String iso) {
  final dt = DateTime.tryParse(iso);
  if (dt == null) return iso;
  final diff = DateTime.now().toUtc().difference(dt.toUtc());
  if (diff.inMinutes < 1) return 'just now';
  if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
  if (diff.inHours < 24) return '${diff.inHours}h ago';
  if (diff.inDays < 30) return '${diff.inDays}d ago';
  return iso;
}
