import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:termipod/l10n/app_localizations.dart';

import '../../../providers/hub_provider.dart';
import '../../../theme/design_colors.dart';
import '../plan_viewer_screen.dart';
import '../plans_screen.dart';
import '../reviews_screen.dart';
import 'registry.dart';

/// Workspace Overview chassis for kind=standing projects (W6, IA §6.2).
///
/// Goal projects get the W4 A+B portfolio chassis. Workspaces are
/// "CI-like" — they never close, have no progress %, and their state
/// reads as cadence: when does it fire next, when did it last fire and
/// what happened, and what's running now. Structure:
///
///   1. WorkspaceHeader — goal, steward, budget, attention, cadence
///      summary (derived from enabled schedules), last-firing chip.
///   2. Hero — [RecentFiringsList], a rolling list of the most recent
///      plan rows for this project (schedules materialize as plans).
///
/// This widget is not registered under [buildOverviewWidget] because the
/// standing branch takes a different chassis entirely; callers should
/// switch on kind and call [buildWorkspaceOverview]. The raw hero list
/// is however exposed as `recent_firings_list` in the registry so a
/// template may still declare it for a goal project if needed.
Widget buildWorkspaceOverview(OverviewContext ctx) {
  return _WorkspaceOverview(ctx: ctx);
}

class _WorkspaceOverview extends ConsumerWidget {
  final OverviewContext ctx;
  const _WorkspaceOverview({required this.ctx});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        WorkspaceHeader(ctx: ctx),
        const SizedBox(height: 12),
        RecentFiringsList(ctx: ctx),
      ],
    );
  }
}

/// Header card for a workspace. Sibling of [PortfolioHeader] — sharing
/// styling but not layout, since workspaces surface cadence / last-run
/// where goals surface task progress.
class WorkspaceHeader extends ConsumerStatefulWidget {
  final OverviewContext ctx;
  const WorkspaceHeader({super.key, required this.ctx});

  @override
  ConsumerState<WorkspaceHeader> createState() => _WorkspaceHeaderState();
}

class _WorkspaceHeaderState extends ConsumerState<WorkspaceHeader> {
  bool _goalExpanded = false;
  bool _loaded = false;
  List<Map<String, dynamic>> _schedules = const [];
  Map<String, dynamic>? _lastPlan;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    final projectId = widget.ctx.projectId;
    if (client == null || projectId.isEmpty) {
      if (mounted) setState(() => _loaded = true);
      return;
    }
    try {
      final results = await Future.wait([
        client.listSchedulesCached(projectId: projectId),
        client.listPlansCached(projectId: projectId),
      ]);
      final schedules = results[0].body;
      final plans = results[1].body;
      if (!mounted) return;
      setState(() {
        _schedules = schedules;
        _lastPlan = plans.isEmpty ? null : plans.first;
        _loaded = true;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() => _loaded = true);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg =
        isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final project = widget.ctx.project;
    final goal = (project['goal'] ?? '').toString();
    final status = (project['status'] ?? '').toString();
    final budgetCents = project['budget_cents'];
    final stewardAgentId = (project['steward_agent_id'] ?? '').toString();

    final attention = ref.watch(hubProvider).value?.attention ?? const [];
    final openAttention = attention
        .where((a) =>
            (a['project_id'] ?? '').toString() == widget.ctx.projectId)
        .length;

    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (goal.isNotEmpty) ...[
            InkWell(
              onTap: () => setState(() => _goalExpanded = !_goalExpanded),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const Icon(Icons.flag_outlined,
                      size: 14, color: DesignColors.primary),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(
                      goal,
                      maxLines: _goalExpanded ? null : 1,
                      overflow:
                          _goalExpanded ? null : TextOverflow.ellipsis,
                      style: GoogleFonts.spaceGrotesk(
                        fontSize: 13,
                        height: 1.35,
                      ),
                    ),
                  ),
                  const SizedBox(width: 4),
                  Icon(
                    _goalExpanded
                        ? Icons.expand_less
                        : Icons.expand_more,
                    size: 16,
                    color: DesignColors.textMuted,
                  ),
                ],
              ),
            ),
            const SizedBox(height: 8),
          ],
          Wrap(
            spacing: 6,
            runSpacing: 6,
            children: [
              _Chip(
                label: status.isEmpty ? 'active' : status,
                color: status == 'archived'
                    ? DesignColors.textMuted
                    : DesignColors.terminalGreen,
              ),
              _Chip(
                label: stewardAgentId.isEmpty
                    ? 'steward: not-configured'
                    : 'steward: configured',
                color: stewardAgentId.isEmpty
                    ? DesignColors.textMuted
                    : DesignColors.terminalCyan,
                icon: Icons.smart_toy_outlined,
              ),
              if (budgetCents is int)
                _Chip(
                  label:
                      'budget: \$0 / \$${(budgetCents / 100).toStringAsFixed(0)}',
                  color: DesignColors.warning,
                  icon: Icons.attach_money,
                ),
              if (openAttention > 0)
                InkWell(
                  borderRadius: BorderRadius.circular(4),
                  onTap: () => Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) =>
                          ReviewsScreen(projectId: widget.ctx.projectId),
                    ),
                  ),
                  child: _Chip(
                    label: openAttention == 1
                        ? '1 review'
                        : '$openAttention reviews',
                    color: DesignColors.warning,
                    icon: Icons.flag_outlined,
                  ),
                ),
            ],
          ),
          const SizedBox(height: 10),
          _CadenceRow(
            loaded: _loaded,
            schedules: _schedules,
            isDark: isDark,
            l10n: l10n,
          ),
          if (_loaded && _lastPlan != null) ...[
            const SizedBox(height: 6),
            _LastFiringRow(plan: _lastPlan!, l10n: l10n, isDark: isDark),
          ],
        ],
      ),
    );
  }
}

/// One-line cadence summary. Handles 0 / 1 / N schedules, manual-only,
/// and the "enabled but next_run_at pending" edge.
class _CadenceRow extends StatelessWidget {
  final bool loaded;
  final List<Map<String, dynamic>> schedules;
  final bool isDark;
  final AppLocalizations l10n;
  const _CadenceRow({
    required this.loaded,
    required this.schedules,
    required this.isDark,
    required this.l10n,
  });

  @override
  Widget build(BuildContext context) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const Icon(Icons.schedule_outlined,
            size: 14, color: DesignColors.primary),
        const SizedBox(width: 6),
        Text('${l10n.workspaceCadence}: ',
            style: GoogleFonts.spaceGrotesk(
              fontSize: 11,
              fontWeight: FontWeight.w600,
            )),
        Expanded(
          child: Text(
            !loaded ? '…' : _sentence(),
            style: GoogleFonts.spaceGrotesk(
              fontSize: 11,
              color: isDark
                  ? DesignColors.textSecondary
                  : DesignColors.textSecondaryLight,
            ),
          ),
        ),
      ],
    );
  }

  String _sentence() {
    final enabled =
        schedules.where((s) => s['enabled'] == true).toList();
    if (schedules.isEmpty) return l10n.workspaceNoSchedules;
    if (enabled.isEmpty) {
      final manualOnly = schedules.every((s) =>
          (s['trigger_kind'] ?? '').toString() != 'cron');
      if (manualOnly) return l10n.workspaceManualOnly;
      return l10n.workspaceNoSchedules;
    }
    if (enabled.length > 1) {
      // Collapse N-schedule case to a count with the soonest next run.
      final soonest = _soonestNext(enabled);
      final count = l10n.workspaceMultipleSchedules(enabled.length);
      if (soonest == null) return count;
      return '$count · ${l10n.workspaceNextIn(formatRelative(soonest))}';
    }
    final s = enabled.first;
    final trigger = (s['trigger_kind'] ?? '').toString();
    final cron = (s['cron_expr'] ?? '').toString();
    final nextAt = _parseTs(s['next_run_at']);
    final cadenceText = trigger == 'cron' && cron.isNotEmpty
        ? _humanizeCron(cron, l10n)
        : l10n.workspaceManualOnly;
    if (trigger != 'cron') return cadenceText;
    if (nextAt == null) {
      return '$cadenceText · ${l10n.workspaceNextRunPending}';
    }
    return '$cadenceText · ${l10n.workspaceNextIn(formatRelative(nextAt))}';
  }

  DateTime? _soonestNext(List<Map<String, dynamic>> rows) {
    DateTime? best;
    for (final r in rows) {
      final t = _parseTs(r['next_run_at']);
      if (t == null) continue;
      if (best == null || t.isBefore(best)) best = t;
    }
    return best;
  }
}

DateTime? _parseTs(Object? raw) {
  if (raw == null) return null;
  final s = raw.toString();
  if (s.isEmpty) return null;
  return DateTime.tryParse(s);
}

/// Humanize a handful of common cron shapes for the cadence summary. We
/// deliberately do not pull in a full cron parser — unknown shapes fall
/// back to the raw expression, which still reads correctly.
String _humanizeCron(String expr, AppLocalizations l10n) {
  final parts = expr.trim().split(RegExp(r'\s+'));
  if (parts.length < 5) return l10n.workspaceCadenceEvery(expr);
  final min = parts[0];
  final hour = parts[1];
  final dom = parts[2];
  final mon = parts[3];
  final dow = parts[4];
  final time = _formatHHmm(hour, min);
  // "every day at HH:MM"
  if (mon == '*' && dom == '*' && dow == '*') {
    if (time != null) {
      return l10n.workspaceCadenceEveryAt('day', time);
    }
  }
  // "every <weekday> at HH:MM"
  if (mon == '*' && dom == '*' && dow != '*' && time != null) {
    final day = _weekdayName(dow);
    if (day != null) {
      return l10n.workspaceCadenceEveryAt(day, time);
    }
  }
  return l10n.workspaceCadenceEvery(expr);
}

String? _formatHHmm(String hour, String min) {
  final h = int.tryParse(hour);
  final m = int.tryParse(min);
  if (h == null || m == null) return null;
  if (h < 0 || h > 23 || m < 0 || m > 59) return null;
  final hh = h.toString().padLeft(2, '0');
  final mm = m.toString().padLeft(2, '0');
  return '$hh:$mm';
}

String? _weekdayName(String dow) {
  const names = {
    '0': 'Sun',
    '1': 'Mon',
    '2': 'Tue',
    '3': 'Wed',
    '4': 'Thu',
    '5': 'Fri',
    '6': 'Sat',
    '7': 'Sun',
  };
  return names[dow.trim()];
}

/// Render "last fired 2h ago · completed". Uses plan.created_at when
/// started_at is null (drafts), and maps status to a label/color.
class _LastFiringRow extends StatelessWidget {
  final Map<String, dynamic> plan;
  final AppLocalizations l10n;
  final bool isDark;
  const _LastFiringRow({
    required this.plan,
    required this.l10n,
    required this.isDark,
  });

  @override
  Widget build(BuildContext context) {
    final status = (plan['status'] ?? '').toString();
    final started = _parseTs(plan['started_at']) ??
        _parseTs(plan['created_at']);
    if (started == null) return const SizedBox.shrink();
    final color = _statusColor(status);
    return Row(
      children: [
        Icon(Icons.history, size: 14, color: color),
        const SizedBox(width: 6),
        Expanded(
          child: Text(
            l10n.workspaceLastFired(formatRelative(started), status),
            style: GoogleFonts.spaceGrotesk(
              fontSize: 11,
              color: isDark
                  ? DesignColors.textSecondary
                  : DesignColors.textSecondaryLight,
            ),
          ),
        ),
      ],
    );
  }
}

Color _statusColor(String status) {
  switch (status) {
    case 'completed':
      return DesignColors.terminalGreen;
    case 'failed':
      return DesignColors.error;
    case 'running':
      return DesignColors.primary;
    case 'cancelled':
      return DesignColors.textMuted;
    default:
      return DesignColors.textMuted;
  }
}

/// Rolling list of recent plans for a project. Capped at [limit] rows to
/// keep the Overview scannable; a "View all firings" footer routes to
/// the full PlansScreen, where the filters live.
class RecentFiringsList extends ConsumerStatefulWidget {
  final OverviewContext ctx;
  final int limit;
  const RecentFiringsList({
    super.key,
    required this.ctx,
    this.limit = 15,
  });

  @override
  ConsumerState<RecentFiringsList> createState() =>
      _RecentFiringsListState();
}

class _RecentFiringsListState extends ConsumerState<RecentFiringsList> {
  bool _loading = true;
  List<Map<String, dynamic>> _plans = const [];

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    final projectId = widget.ctx.projectId;
    if (client == null || projectId.isEmpty) {
      if (mounted) setState(() => _loading = false);
      return;
    }
    try {
      final resp = await client.listPlansCached(projectId: projectId);
      if (!mounted) return;
      setState(() {
        _plans = resp.body.take(widget.limit).toList();
        _loading = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg =
        isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;

    return Container(
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(12, 10, 12, 8),
            child: Row(
              children: [
                const Icon(Icons.playlist_play_outlined,
                    size: 16, color: DesignColors.primary),
                const SizedBox(width: 6),
                Text(
                  l10n.workspaceRecentFirings,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 13,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ],
            ),
          ),
          if (_loading)
            Padding(
              padding: const EdgeInsets.fromLTRB(12, 0, 12, 12),
              child: Text(
                l10n.workspaceFiringLoading,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 10,
                  color: DesignColors.textMuted,
                ),
              ),
            )
          else if (_plans.isEmpty)
            Padding(
              padding: const EdgeInsets.fromLTRB(12, 0, 12, 12),
              child: Text(
                l10n.workspaceNoFirings,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 12,
                  color: DesignColors.textMuted,
                ),
              ),
            )
          else ...[
            for (var i = 0; i < _plans.length; i++) ...[
              if (i > 0)
                Divider(height: 1, color: border),
              _FiringRow(
                plan: _plans[i],
                projectId: widget.ctx.projectId,
              ),
            ],
            Divider(height: 1, color: border),
            InkWell(
              onTap: () => Navigator.of(context).push(MaterialPageRoute(
                builder: (_) => const PlansScreen(),
              )),
              child: Padding(
                padding:
                    const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
                child: Row(
                  children: [
                    Text(
                      l10n.workspaceViewAllFirings,
                      style: GoogleFonts.spaceGrotesk(
                        fontSize: 12,
                        fontWeight: FontWeight.w600,
                        color: DesignColors.primary,
                      ),
                    ),
                    const Spacer(),
                    const Icon(Icons.chevron_right,
                        size: 18, color: DesignColors.primary),
                  ],
                ),
              ),
            ),
          ],
        ],
      ),
    );
  }
}

class _FiringRow extends StatelessWidget {
  final Map<String, dynamic> plan;
  final String projectId;
  const _FiringRow({required this.plan, required this.projectId});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final id = (plan['id'] ?? '').toString();
    final name = _deriveName(plan);
    final status = (plan['status'] ?? '').toString();
    final started = _parseTs(plan['started_at']) ??
        _parseTs(plan['created_at']);
    final finished = _parseTs(plan['completed_at']);
    final statusColor = _statusColor(status);

    return InkWell(
      onTap: id.isEmpty
          ? null
          : () => Navigator.of(context).push(MaterialPageRoute(
                builder: (_) => PlanViewerScreen(
                  planId: id,
                  projectId: projectId,
                ),
              )),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Container(
              width: 8,
              height: 8,
              margin: const EdgeInsets.only(top: 5, right: 10),
              decoration: BoxDecoration(
                color: statusColor,
                shape: BoxShape.circle,
              ),
            ),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    name,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 13,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  const SizedBox(height: 2),
                  Text(
                    _subtitle(started, finished, status),
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 10,
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
              status,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 10,
                fontWeight: FontWeight.w700,
                color: statusColor,
              ),
            ),
          ],
        ),
      ),
    );
  }

  /// Derive a readable label. Plan rows don't carry a human name, so we
  /// fall back to template id + short plan id suffix.
  String _deriveName(Map<String, dynamic> plan) {
    final template = (plan['template_id'] ?? '').toString();
    final id = (plan['id'] ?? '').toString();
    final short = id.length > 8 ? id.substring(id.length - 8) : id;
    if (template.isNotEmpty) return '$template · $short';
    if (id.isNotEmpty) return 'plan $short';
    return 'plan';
  }

  String _subtitle(DateTime? started, DateTime? finished, String status) {
    if (started == null) return status;
    final startRel = formatRelative(started);
    if (finished == null) return 'started $startRel';
    return 'started $startRel · ${formatRelative(finished)}';
  }
}

/// Small chip duplicating the styling in PortfolioHeader. Kept local so
/// the two chassis can diverge without a shared base.
class _Chip extends StatelessWidget {
  final String label;
  final Color color;
  final IconData? icon;
  const _Chip({required this.label, required this.color, this.icon});

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
          if (icon != null) ...[
            Icon(icon, size: 11, color: color),
            const SizedBox(width: 3),
          ],
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

/// Compact "2h ago" / "in 18h" relative formatter. Returns an unsigned
/// magnitude; callers supply the "last fired / next in" framing.
String formatRelative(DateTime t) {
  final now = DateTime.now();
  final diff = t.isAfter(now) ? t.difference(now) : now.difference(t);
  if (diff.inSeconds < 60) return '${diff.inSeconds}s';
  if (diff.inMinutes < 60) return '${diff.inMinutes}m';
  if (diff.inHours < 24) return '${diff.inHours}h';
  if (diff.inDays < 7) return '${diff.inDays}d';
  return '${(diff.inDays / 7).floor()}w';
}
