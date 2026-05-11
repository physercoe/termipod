import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../providers/insights_provider.dart';
import '../../theme/design_colors.dart';
import '../projects/project_detail_screen.dart';

/// Team-level aggregate dashboard surfaced by the AppBar Insights icon
/// on the Projects list. Reads `/v1/insights?team_id=X` (the same
/// endpoint the Projects list watches for per-project rows) and folds
/// `by_project[]` + `by_agent[]` into team-wide rollups.
///
/// Pre–polish wedge this screen was a per-project card list; that data
/// now lives inline on each project row, so this surface pivots to
/// "what's happening across the team that I can't see from one row at
/// a time": phase distribution, activity recency buckets, average
/// progress, top-5 active project leaderboard, top-5 agent
/// leaderboard.
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
        data: (state) {
          final body = state.body;
          if (body == null) return const _EmptyView();
          final projects = _readProjects(body);
          final agents = _readAgents(body);
          if (projects.isEmpty && agents.isEmpty) {
            return const _EmptyView();
          }
          return RefreshIndicator(
            onRefresh: () async {
              ref.invalidate(insightsProvider(scope));
              await ref.read(insightsProvider(scope).future);
            },
            child: ListView(
              padding: const EdgeInsets.all(12),
              children: [
                _SummaryRow(projects: projects),
                const SizedBox(height: 14),
                if (projects.isNotEmpty) ...[
                  _Section(title: 'Phase distribution'),
                  _PhaseDistribution(projects: projects),
                  const SizedBox(height: 14),
                  _Section(title: 'Activity recency'),
                  _ActivityRecency(projects: projects),
                  const SizedBox(height: 14),
                  _Section(title: 'Most recent · top 5'),
                  _MostRecentList(projects: projects),
                  const SizedBox(height: 14),
                ],
                if (agents.isNotEmpty) ...[
                  _Section(title: 'Top agents · by event volume'),
                  _AgentLeaderboard(agents: agents),
                  const SizedBox(height: 14),
                ],
              ],
            ),
          );
        },
      ),
    );
  }
}

class _ProjectAgg {
  final String projectId;
  final String name;
  final String currentPhase;
  final String status;
  final double progress;
  final int openAttention;
  final int openCriteria;
  final String lastActivity;
  const _ProjectAgg({
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

class _AgentAgg {
  final String agentId;
  final String handle;
  final String kind;
  final String engine;
  final String status;
  final int events;
  const _AgentAgg({
    required this.agentId,
    required this.handle,
    required this.kind,
    required this.engine,
    required this.status,
    required this.events,
  });
}

List<_ProjectAgg> _readProjects(Map<String, dynamic> body) {
  final raw = body['by_project'];
  if (raw is! List) return const [];
  final out = <_ProjectAgg>[];
  for (final e in raw) {
    if (e is! Map) continue;
    final m = e.cast<String, dynamic>();
    final id = (m['project_id'] ?? '').toString();
    if (id.isEmpty) continue;
    out.add(_ProjectAgg(
      projectId: id,
      name: (m['name'] ?? '').toString(),
      currentPhase: (m['current_phase'] ?? '').toString(),
      status: (m['status'] ?? '').toString(),
      progress: _asDouble(m['progress']),
      openAttention: _asInt(m['open_attention']),
      openCriteria: _asInt(m['open_criteria']),
      lastActivity: (m['last_activity'] ?? '').toString(),
    ));
  }
  return out;
}

List<_AgentAgg> _readAgents(Map<String, dynamic> body) {
  final raw = body['by_agent'];
  if (raw is! List) return const [];
  final out = <_AgentAgg>[];
  for (final e in raw) {
    if (e is! Map) continue;
    final m = e.cast<String, dynamic>();
    // Tokens-in as a proxy for "event volume" — that's how the hub
    // sorts `by_agent` rows server-side. Falls back to event/tool
    // counts if the field is empty (legacy snapshots).
    final events = _asInt(m['tokens_in']) > 0
        ? _asInt(m['tokens_in'])
        : (_asInt(m['events']) > 0
            ? _asInt(m['events'])
            : _asInt(m['tool_calls']));
    out.add(_AgentAgg(
      agentId: (m['agent_id'] ?? '').toString(),
      handle: (m['handle'] ?? '').toString(),
      kind: (m['kind'] ?? '').toString(),
      engine: (m['engine'] ?? '').toString(),
      status: (m['status'] ?? '').toString(),
      events: events,
    ));
  }
  out.sort((a, b) => b.events.compareTo(a.events));
  return out;
}

class _SummaryRow extends StatelessWidget {
  final List<_ProjectAgg> projects;
  const _SummaryRow({required this.projects});

  @override
  Widget build(BuildContext context) {
    final active = projects.where((p) => p.status != 'archived').length;
    final openAcs =
        projects.fold<int>(0, (acc, p) => acc + p.openCriteria);
    final openAttention =
        projects.fold<int>(0, (acc, p) => acc + p.openAttention);
    final live = projects.where((p) => _isWithin(p.lastActivity, const Duration(hours: 24))).length;
    return Row(
      children: [
        Expanded(
          child: _StatTile(label: 'Active', value: '$active'),
        ),
        const SizedBox(width: 8),
        Expanded(
          child: _StatTile(
            label: 'Open AC',
            value: '$openAcs',
            tone: openAcs > 0 ? DesignColors.warning : null,
          ),
        ),
        const SizedBox(width: 8),
        Expanded(
          child: _StatTile(
            label: 'Open attention',
            value: '$openAttention',
            tone: openAttention > 0 ? DesignColors.warning : null,
          ),
        ),
        const SizedBox(width: 8),
        Expanded(
          child: _StatTile(label: 'Live <24h', value: '$live'),
        ),
      ],
    );
  }
}

class _StatTile extends StatelessWidget {
  final String label;
  final String value;
  final Color? tone;
  const _StatTile({required this.label, required this.value, this.tone});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final accent = tone ?? DesignColors.primary;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 10),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            value,
            style: GoogleFonts.jetBrainsMono(
              fontSize: 18,
              fontWeight: FontWeight.w700,
              color: accent,
            ),
          ),
          const SizedBox(height: 2),
          Text(
            label,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 10,
              color: DesignColors.textMuted,
              letterSpacing: 0.5,
            ),
          ),
        ],
      ),
    );
  }
}

class _Section extends StatelessWidget {
  final String title;
  const _Section({required this.title});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(4, 0, 4, 6),
      child: Text(
        title.toUpperCase(),
        style: GoogleFonts.spaceGrotesk(
          fontSize: 11,
          fontWeight: FontWeight.w700,
          color: DesignColors.textMuted,
          letterSpacing: 0.8,
        ),
      ),
    );
  }
}

class _PhaseDistribution extends StatelessWidget {
  final List<_ProjectAgg> projects;
  const _PhaseDistribution({required this.projects});

  @override
  Widget build(BuildContext context) {
    final counts = <String, int>{};
    for (final p in projects) {
      final phase = p.currentPhase.isEmpty ? '(no phase)' : p.currentPhase;
      counts[phase] = (counts[phase] ?? 0) + 1;
    }
    final entries = counts.entries.toList()
      ..sort((a, b) => b.value.compareTo(a.value));
    final max = entries.fold<int>(0, (m, e) => e.value > m ? e.value : m);
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: border),
      ),
      child: Column(
        children: [
          for (final e in entries) ...[
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 4),
              child: Row(
                children: [
                  SizedBox(
                    width: 90,
                    child: Text(
                      e.key,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: GoogleFonts.spaceGrotesk(
                        fontSize: 12,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ),
                  Expanded(
                    child: ClipRRect(
                      borderRadius: BorderRadius.circular(3),
                      child: LinearProgressIndicator(
                        value: max == 0 ? 0 : e.value / max,
                        minHeight: 6,
                        backgroundColor: border,
                        valueColor: const AlwaysStoppedAnimation(
                            DesignColors.primary),
                      ),
                    ),
                  ),
                  const SizedBox(width: 8),
                  SizedBox(
                    width: 24,
                    child: Text(
                      '${e.value}',
                      textAlign: TextAlign.right,
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 11,
                        fontWeight: FontWeight.w700,
                        color: DesignColors.textMuted,
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ],
        ],
      ),
    );
  }
}

class _ActivityRecency extends StatelessWidget {
  final List<_ProjectAgg> projects;
  const _ActivityRecency({required this.projects});

  @override
  Widget build(BuildContext context) {
    final under24h = <_ProjectAgg>[];
    final under7d = <_ProjectAgg>[];
    final stale = <_ProjectAgg>[];
    final never = <_ProjectAgg>[];
    for (final p in projects) {
      if (p.lastActivity.isEmpty) {
        never.add(p);
        continue;
      }
      final dt = DateTime.tryParse(p.lastActivity);
      if (dt == null) {
        never.add(p);
        continue;
      }
      final age = DateTime.now().toUtc().difference(dt.toUtc());
      if (age < const Duration(hours: 24)) {
        under24h.add(p);
      } else if (age < const Duration(days: 7)) {
        under7d.add(p);
      } else {
        stale.add(p);
      }
    }
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: border),
      ),
      child: Row(
        children: [
          Expanded(
            child: _Bucket(
              label: '<24h',
              count: under24h.length,
              tone: DesignColors.terminalGreen,
            ),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: _Bucket(
              label: '<7d',
              count: under7d.length,
              tone: DesignColors.primary,
            ),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: _Bucket(
              label: '>7d',
              count: stale.length,
              tone: DesignColors.warning,
            ),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: _Bucket(
              label: 'idle',
              count: never.length,
              tone: DesignColors.textMuted,
            ),
          ),
        ],
      ),
    );
  }
}

class _Bucket extends StatelessWidget {
  final String label;
  final int count;
  final Color tone;
  const _Bucket({
    required this.label,
    required this.count,
    required this.tone,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        Text(
          '$count',
          style: GoogleFonts.jetBrainsMono(
            fontSize: 18,
            fontWeight: FontWeight.w700,
            color: tone,
          ),
        ),
        const SizedBox(height: 2),
        Text(
          label,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 10,
            color: DesignColors.textMuted,
            letterSpacing: 0.5,
          ),
        ),
      ],
    );
  }
}

class _MostRecentList extends ConsumerWidget {
  final List<_ProjectAgg> projects;
  const _MostRecentList({required this.projects});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final ranked = [...projects]
      ..sort((a, b) => b.lastActivity.compareTo(a.lastActivity));
    final top = ranked.take(5).toList();
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    return Container(
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: border),
      ),
      child: Column(
        children: [
          for (var i = 0; i < top.length; i++) ...[
            InkWell(
              onTap: () {
                final projectsList =
                    ref.read(hubProvider).value?.projects ?? const [];
                final match = projectsList.firstWhere(
                  (p) => (p['id'] ?? '').toString() == top[i].projectId,
                  orElse: () => const <String, dynamic>{},
                );
                if (match.isEmpty) return;
                Navigator.of(context).push(
                  MaterialPageRoute(
                    builder: (_) => ProjectDetailScreen(project: match),
                  ),
                );
              },
              borderRadius: BorderRadius.circular(8),
              child: Padding(
                padding: const EdgeInsets.symmetric(
                    horizontal: 12, vertical: 10),
                child: Row(
                  children: [
                    Expanded(
                      child: Text(
                        top[i].name.isEmpty ? '—' : top[i].name,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: GoogleFonts.spaceGrotesk(
                          fontSize: 13,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ),
                    const SizedBox(width: 8),
                    Text(
                      _relativeTime(top[i].lastActivity),
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 10,
                        color: DesignColors.textMuted,
                      ),
                    ),
                  ],
                ),
              ),
            ),
            if (i < top.length - 1)
              Divider(height: 1, color: border),
          ],
        ],
      ),
    );
  }
}

class _AgentLeaderboard extends StatelessWidget {
  final List<_AgentAgg> agents;
  const _AgentLeaderboard({required this.agents});

  @override
  Widget build(BuildContext context) {
    final top = agents.take(5).toList();
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    return Container(
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: border),
      ),
      child: Column(
        children: [
          for (var i = 0; i < top.length; i++) ...[
            Padding(
              padding: const EdgeInsets.symmetric(
                  horizontal: 12, vertical: 10),
              child: Row(
                children: [
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          top[i].handle.isEmpty ? '—' : top[i].handle,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: GoogleFonts.spaceGrotesk(
                            fontSize: 13,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                        const SizedBox(height: 2),
                        Text(
                          _agentSub(top[i]),
                          style: GoogleFonts.jetBrainsMono(
                            fontSize: 10,
                            color: DesignColors.textMuted,
                          ),
                        ),
                      ],
                    ),
                  ),
                  const SizedBox(width: 8),
                  Text(
                    _compactCount(top[i].events),
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 11,
                      fontWeight: FontWeight.w700,
                      color: DesignColors.textMuted,
                    ),
                  ),
                ],
              ),
            ),
            if (i < top.length - 1)
              Divider(height: 1, color: border),
          ],
        ],
      ),
    );
  }

  static String _agentSub(_AgentAgg a) {
    final parts = <String>[];
    if (a.kind.isNotEmpty) parts.add(a.kind);
    if (a.engine.isNotEmpty) parts.add(a.engine);
    if (a.status.isNotEmpty) parts.add(a.status);
    return parts.isEmpty ? '—' : parts.join(' · ');
  }

  static String _compactCount(int n) {
    if (n >= 1000) return '${(n / 1000).toStringAsFixed(1)}k';
    return '$n';
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
          'No team activity yet.\nCreate a project or run an agent to populate this view.',
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

int _asInt(dynamic v) {
  if (v is int) return v;
  if (v is num) return v.toInt();
  if (v is String) return int.tryParse(v) ?? 0;
  return 0;
}

double _asDouble(dynamic v) {
  if (v is double) return v;
  if (v is num) return v.toDouble();
  if (v is String) return double.tryParse(v) ?? 0;
  return 0;
}

bool _isWithin(String iso, Duration window) {
  if (iso.isEmpty) return false;
  final dt = DateTime.tryParse(iso);
  if (dt == null) return false;
  return DateTime.now().toUtc().difference(dt.toUtc()) < window;
}

/// Format an ISO-8601 ts as a coarse relative time.
String _relativeTime(String iso) {
  if (iso.isEmpty) return '—';
  final dt = DateTime.tryParse(iso);
  if (dt == null) return iso;
  final diff = DateTime.now().toUtc().difference(dt.toUtc());
  if (diff.inMinutes < 1) return 'just now';
  if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
  if (diff.inHours < 24) return '${diff.inHours}h ago';
  if (diff.inDays < 30) return '${diff.inDays}d ago';
  return iso;
}
