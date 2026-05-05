import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../providers/hub_provider.dart';
import '../theme/design_colors.dart';

/// Overview-side digest of the last few audit events scoped to one
/// project (W2 — IA §6.2). Five rows + a "View all" affordance that the
/// caller wires to the Activity tab in the same project detail screen.
///
/// Renders W1's new lifecycle kinds (`project.phase_advanced`,
/// `project.phase_set`, `project.phase_reverted`) with sensible labels
/// alongside the legacy actor-kind audits (agent.spawn, run.create,
/// document.create, attention.decide, …). Surface is read-only — taps
/// route into the full Activity feed; this card never blocks loading.
class ActivitySnippet extends ConsumerStatefulWidget {
  final String projectId;
  final VoidCallback onViewAll;
  const ActivitySnippet({
    super.key,
    required this.projectId,
    required this.onViewAll,
  });

  @override
  ConsumerState<ActivitySnippet> createState() => _ActivitySnippetState();
}

class _ActivitySnippetState extends ConsumerState<ActivitySnippet> {
  bool _loading = true;
  List<Map<String, dynamic>> _events = const [];

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null || widget.projectId.isEmpty) {
      if (mounted) setState(() => _loading = false);
      return;
    }
    try {
      final cached = await client.listAuditEventsCached(
        projectId: widget.projectId,
        limit: 5,
      );
      if (!mounted) return;
      setState(() {
        _events = cached.body;
        _loading = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;

    return Container(
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(12, 10, 4, 6),
            child: Row(
              children: [
                const Icon(Icons.history,
                    size: 14, color: DesignColors.textMuted),
                const SizedBox(width: 6),
                Text(
                  'Recent activity',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    fontWeight: FontWeight.w700,
                    letterSpacing: 0.3,
                    color: DesignColors.textMuted,
                  ),
                ),
                const Spacer(),
                TextButton(
                  onPressed: widget.onViewAll,
                  style: TextButton.styleFrom(
                    minimumSize: const Size(0, 28),
                    padding:
                        const EdgeInsets.symmetric(horizontal: 8, vertical: 0),
                    tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                  ),
                  child: Text(
                    'View all',
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 11,
                      fontWeight: FontWeight.w600,
                      color: DesignColors.primary,
                    ),
                  ),
                ),
              ],
            ),
          ),
          const Divider(height: 1),
          if (_loading)
            const Padding(
              padding: EdgeInsets.all(16),
              child: Center(
                child: SizedBox(
                  width: 18,
                  height: 18,
                  child: CircularProgressIndicator(strokeWidth: 2),
                ),
              ),
            )
          else if (_events.isEmpty)
            Padding(
              padding: const EdgeInsets.all(16),
              child: Center(
                child: Text(
                  'No activity yet',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    color: DesignColors.textMuted,
                  ),
                ),
              ),
            )
          else
            for (var i = 0; i < _events.length; i++) ...[
              if (i > 0) const Divider(height: 1, indent: 12, endIndent: 12),
              _SnippetRow(evt: _events[i]),
            ],
        ],
      ),
    );
  }
}

class _SnippetRow extends StatelessWidget {
  final Map<String, dynamic> evt;
  const _SnippetRow({required this.evt});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final action = (evt['action'] ?? '').toString();
    final summary = (evt['summary'] ?? '').toString();
    final ts = (evt['ts'] ?? '').toString();
    final actorHandle = (evt['actor_handle'] ?? '').toString();
    final actorKind = (evt['actor_kind'] ?? '').toString();
    final actor = actorHandle.isNotEmpty
        ? '@$actorHandle'
        : (actorKind.isNotEmpty ? actorKind : 'system');
    final icon = activityIconForAction(action);
    final color = activityColorForAction(action);
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.only(top: 1),
            child: Icon(icon, size: 14, color: color),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  summary.isEmpty ? action : summary,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    height: 1.3,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const SizedBox(height: 2),
                Text(
                  '$actor · ${activityActionLabel(action)}',
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 9,
                    color: isDark
                        ? DesignColors.textMuted
                        : DesignColors.textMutedLight,
                  ),
                ),
              ],
            ),
          ),
          const SizedBox(width: 8),
          Text(
            shortRelativeTs(ts),
            style: GoogleFonts.jetBrainsMono(
              fontSize: 9,
              color: isDark
                  ? DesignColors.textMuted
                  : DesignColors.textMutedLight,
            ),
          ),
        ],
      ),
    );
  }
}

/// Maps an audit `action` to a human label. Lifecycle kinds added in W1
/// (and the W5b/W6 kinds the plan locks in) are spelled out so the feed
/// reads like a human narrative — "Phase advanced" rather than
/// "project.phase_advanced". Unknown actions fall through to the raw key.
String activityActionLabel(String action) {
  switch (action) {
    case 'project.phase_advanced':
      return 'Phase advanced';
    case 'project.phase_reverted':
      return 'Phase reverted';
    case 'project.phase_set':
      return 'Phase set';
    case 'project.create':
      return 'Project created';
    case 'project.update':
      return 'Project updated';
    case 'project.archive':
      return 'Project archived';
    case 'agent.spawn':
      return 'Agent spawned';
    case 'agent.terminate':
      return 'Agent terminated';
    case 'agent.archive':
      return 'Agent archived';
    case 'run.create':
      return 'Run created';
    case 'run.complete':
      return 'Run completed';
    case 'document.create':
      return 'Document created';
    case 'review.request':
      return 'Review requested';
    case 'review.decide':
      return 'Review decided';
    case 'attention.decide':
      return 'Attention resolved';
    case 'artifact.create':
      return 'Artifact created';
    case 'session.open':
      return 'Session opened';
    case 'session.archive':
      return 'Session archived';
    case 'plan.create':
      return 'Plan created';
    case 'plan.update':
      return 'Plan updated';
    case 'deliverable.ratify':
      return 'Deliverable ratified';
    case 'criterion.met':
      return 'Criterion met';
    default:
      return action;
  }
}

IconData activityIconForAction(String action) {
  if (action.startsWith('project.phase_')) return Icons.flag_outlined;
  if (action.startsWith('agent.spawn')) return Icons.rocket_launch_outlined;
  if (action.startsWith('agent.terminate')) return Icons.power_settings_new;
  if (action.startsWith('agent.')) return Icons.smart_toy_outlined;
  if (action.startsWith('run.')) return Icons.science_outlined;
  if (action.startsWith('document.')) return Icons.article_outlined;
  if (action.startsWith('review.')) return Icons.rate_review_outlined;
  if (action.startsWith('attention.')) return Icons.flag_outlined;
  if (action.startsWith('artifact.')) return Icons.output_outlined;
  if (action.startsWith('session.')) return Icons.terminal;
  if (action.startsWith('plan.')) return Icons.playlist_play_outlined;
  if (action.startsWith('deliverable.')) return Icons.task_alt_outlined;
  if (action.startsWith('criterion.')) return Icons.check_circle_outline;
  if (action.startsWith('project.')) return Icons.folder_outlined;
  return Icons.history;
}

Color activityColorForAction(String action) {
  if (action == 'project.phase_advanced' ||
      action == 'project.create' ||
      action == 'agent.spawn' ||
      action == 'run.create' ||
      action == 'session.open' ||
      action == 'criterion.met' ||
      action == 'deliverable.ratify') {
    return DesignColors.primary;
  }
  if (action == 'agent.terminate' ||
      action == 'project.archive' ||
      action == 'agent.archive' ||
      action == 'session.archive') {
    return DesignColors.error;
  }
  if (action == 'attention.decide' || action == 'review.decide') {
    return DesignColors.warning;
  }
  return DesignColors.textMuted;
}

/// Compact "5m / 2h / 3d / 1w" formatter matching the audit screen's
/// shortTime feel but relative-to-now (the snippet doesn't have room for
/// absolute timestamps).
String shortRelativeTs(String raw) {
  if (raw.isEmpty) return '';
  final t = DateTime.tryParse(raw);
  if (t == null) return raw;
  final diff = DateTime.now().toUtc().difference(t.toUtc());
  if (diff.inSeconds < 30) return 'now';
  if (diff.inMinutes < 1) return '${diff.inSeconds}s';
  if (diff.inMinutes < 60) return '${diff.inMinutes}m';
  if (diff.inHours < 24) return '${diff.inHours}h';
  if (diff.inDays < 7) return '${diff.inDays}d';
  return '${(diff.inDays / 7).floor()}w';
}
