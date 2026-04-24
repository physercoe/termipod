import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import 'plan_viewer_screen.dart';
import 'plans_screen.dart';
import 'templates_screen.dart';

/// Workflows tab (blueprint §9 P2.5). A workflow = template + its
/// bound schedules + its recent plan runs, aggregated UI-side from the
/// three existing endpoints (`listTemplates`, `listSchedules`,
/// `listPlans`). Lets users see the full surface of a recipe without
/// hopping between three screens, and fire "Run now" per schedule.
class WorkflowsScreen extends ConsumerStatefulWidget {
  const WorkflowsScreen({super.key});

  @override
  ConsumerState<WorkflowsScreen> createState() => _WorkflowsScreenState();
}

class _Workflow {
  final String templateId; // "category/name", e.g. "agents/steward.v1.yaml"
  final String category; // '' if template is missing (orphan schedule)
  final String name;
  final List<Map<String, dynamic>> schedules;
  final List<Map<String, dynamic>> recentPlans;
  _Workflow({
    required this.templateId,
    required this.category,
    required this.name,
    required this.schedules,
    required this.recentPlans,
  });

  bool get hasTemplate => category.isNotEmpty;
}

class _WorkflowsScreenState extends ConsumerState<WorkflowsScreen> {
  List<_Workflow>? _rows;
  bool _loading = true;
  String? _error;
  final Set<String> _firing = <String>{};

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() {
      _loading = _rows == null;
      _error = null;
    });
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() {
        _loading = false;
        _error = 'Hub not configured.';
      });
      return;
    }
    try {
      final results = await Future.wait([
        client.listTemplatesCached(),
        client.listSchedulesCached(),
        client.listPlansCached(),
      ]);
      if (!mounted) return;
      final templates = results[0].body;
      final schedules = results[1].body;
      final plans = results[2].body;
      setState(() {
        _rows = _aggregate(templates, schedules, plans);
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
  }

  // Merge the three collections into per-template _Workflow rows. Templates
  // with no schedules + no runs still appear so users can see the recipe
  // library is stable; orphan schedules (template deleted) get their own
  // synthetic row so they don't silently disappear.
  List<_Workflow> _aggregate(
    List<Map<String, dynamic>> templates,
    List<Map<String, dynamic>> schedules,
    List<Map<String, dynamic>> plans,
  ) {
    final byId = <String, _Workflow>{};
    for (final t in templates) {
      final cat = (t['category'] ?? '').toString();
      final name = (t['name'] ?? '').toString();
      if (cat.isEmpty || name.isEmpty) continue;
      final id = '$cat/$name';
      byId[id] = _Workflow(
        templateId: id,
        category: cat,
        name: name,
        schedules: [],
        recentPlans: [],
      );
    }
    for (final s in schedules) {
      final id = (s['template_id'] ?? '').toString();
      if (id.isEmpty) continue;
      final wf = byId[id] ??
          _Workflow(
            templateId: id,
            category: '',
            name: id,
            schedules: [],
            recentPlans: [],
          );
      wf.schedules.add(s);
      byId[id] = wf;
    }
    // Bucket plans by template, keep newest 3 per template. Plans without a
    // template_id are agent-scheduler or steward-emitted; they live on the
    // Plans screen, not here.
    final plansById = <String, List<Map<String, dynamic>>>{};
    for (final p in plans) {
      final id = (p['template_id'] ?? '').toString();
      if (id.isEmpty) continue;
      plansById.putIfAbsent(id, () => []).add(p);
    }
    for (final entry in plansById.entries) {
      entry.value.sort((a, b) =>
          (b['created_at'] ?? '').toString().compareTo(
                (a['created_at'] ?? '').toString(),
              ));
      final wf = byId[entry.key] ??
          _Workflow(
            templateId: entry.key,
            category: '',
            name: entry.key,
            schedules: [],
            recentPlans: [],
          );
      wf.recentPlans.addAll(entry.value.take(3));
      byId[entry.key] = wf;
    }
    final rows = byId.values.toList();
    // Sort: templates with activity first (schedules or runs), then by id.
    rows.sort((a, b) {
      final aAct = a.schedules.length + a.recentPlans.length;
      final bAct = b.schedules.length + b.recentPlans.length;
      if (aAct != bAct) return bAct.compareTo(aAct);
      return a.templateId.compareTo(b.templateId);
    });
    return rows;
  }

  Future<void> _runSchedule(String scheduleId, String label) async {
    if (_firing.contains(scheduleId)) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    final messenger = ScaffoldMessenger.of(context);
    setState(() => _firing.add(scheduleId));
    try {
      final planId = await client.runSchedule(scheduleId);
      if (!mounted) return;
      messenger.showSnackBar(SnackBar(
        content: Text(planId.isEmpty
            ? 'Fired $label'
            : 'Fired $label → plan $planId'),
      ));
      await _load();
    } catch (e) {
      if (!mounted) return;
      messenger.showSnackBar(SnackBar(content: Text('Run failed: $e')));
    } finally {
      if (mounted) setState(() => _firing.remove(scheduleId));
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Workflows',
          style: GoogleFonts.spaceGrotesk(
              fontSize: 18, fontWeight: FontWeight.w700),
        ),
        actions: [
          IconButton(
            tooltip: 'Refresh',
            icon: const Icon(Icons.refresh),
            onPressed: _loading ? null : _load,
          ),
        ],
      ),
      body: _body(),
    );
  }

  Widget _body() {
    if (_loading && _rows == null) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(_error!,
                  textAlign: TextAlign.center,
                  style: GoogleFonts.jetBrainsMono(
                      fontSize: 12, color: DesignColors.error)),
              const SizedBox(height: 12),
              FilledButton.tonalIcon(
                onPressed: _load,
                icon: const Icon(Icons.refresh, size: 16),
                label: const Text('Retry'),
              ),
            ],
          ),
        ),
      );
    }
    final rows = _rows ?? const <_Workflow>[];
    if (rows.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            'No templates or schedules yet. Add templates under '
            '`team/templates/` and bind them with schedules to see workflows.',
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 13,
              color: DesignColors.textMuted,
            ),
          ),
        ),
      );
    }
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView.builder(
        padding: const EdgeInsets.fromLTRB(12, 8, 12, 24),
        itemCount: rows.length,
        itemBuilder: (_, i) => _WorkflowCard(
          wf: rows[i],
          firing: _firing,
          onRun: _runSchedule,
        ),
      ),
    );
  }
}

class _WorkflowCard extends StatelessWidget {
  final _Workflow wf;
  final Set<String> firing;
  final Future<void> Function(String scheduleId, String label) onRun;
  const _WorkflowCard({
    required this.wf,
    required this.firing,
    required this.onRun,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Container(
      margin: const EdgeInsets.only(bottom: 10),
      padding: const EdgeInsets.fromLTRB(14, 12, 10, 10),
      decoration: BoxDecoration(
        color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(
          color: isDark ? DesignColors.borderDark : DesignColors.borderLight,
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              if (wf.hasTemplate)
                _CategoryChip(category: wf.category)
              else
                _OrphanChip(),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  wf.hasTemplate ? wf.name : wf.templateId,
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 13,
                    fontWeight: FontWeight.w600,
                  ),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              if (wf.hasTemplate)
                IconButton(
                  tooltip: 'View template',
                  icon: const Icon(Icons.description_outlined, size: 18),
                  onPressed: () => Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => TemplateViewerScreen(
                        category: wf.category,
                        name: wf.name,
                      ),
                    ),
                  ),
                ),
            ],
          ),
          const SizedBox(height: 6),
          if (wf.schedules.isEmpty)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 4),
              child: Text(
                'No schedules bound',
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  color: DesignColors.textMuted,
                ),
              ),
            )
          else
            for (final s in wf.schedules)
              _ScheduleLine(
                row: s,
                firing: firing.contains((s['id'] ?? '').toString()),
                onRun: () => onRun(
                  (s['id'] ?? '').toString(),
                  wf.name,
                ),
              ),
          if (wf.recentPlans.isNotEmpty) ...[
            const SizedBox(height: 8),
            Divider(
              height: 1,
              color: isDark
                  ? DesignColors.borderDark
                  : DesignColors.borderLight,
            ),
            const SizedBox(height: 8),
            Text(
              'Recent runs',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                fontWeight: FontWeight.w700,
                color: DesignColors.textMuted,
              ),
            ),
            const SizedBox(height: 4),
            for (final p in wf.recentPlans) _PlanLine(row: p),
          ],
        ],
      ),
    );
  }
}

class _ScheduleLine extends StatelessWidget {
  final Map<String, dynamic> row;
  final bool firing;
  final VoidCallback onRun;
  const _ScheduleLine({
    required this.row,
    required this.firing,
    required this.onRun,
  });

  @override
  Widget build(BuildContext context) {
    final trigger = (row['trigger_kind'] ?? 'cron').toString();
    final cron = (row['cron_expr'] ?? '').toString();
    final enabled = row['enabled'] == true;
    final nextRun = (row['next_run_at'] ?? '').toString();
    final lastRun = (row['last_run_at'] ?? '').toString();
    final label = trigger == 'cron' && cron.isNotEmpty
        ? '$trigger · $cron'
        : trigger;
    final meta = <String>[];
    if (nextRun.isNotEmpty) meta.add('next ${_fmtRel(nextRun)}');
    if (lastRun.isNotEmpty) meta.add('last ${_fmtRel(lastRun)}');
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Row(
        children: [
          Icon(
            enabled ? Icons.radio_button_checked : Icons.radio_button_off,
            size: 14,
            color: enabled ? DesignColors.success : DesignColors.textMuted,
          ),
          const SizedBox(width: 6),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  label,
                  style: GoogleFonts.jetBrainsMono(fontSize: 11),
                ),
                if (meta.isNotEmpty)
                  Text(
                    meta.join(' · '),
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 10,
                      color: DesignColors.textMuted,
                    ),
                  ),
              ],
            ),
          ),
          IconButton(
            tooltip: 'Run now',
            iconSize: 18,
            color: DesignColors.success,
            icon: firing
                ? const SizedBox(
                    width: 14,
                    height: 14,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : const Icon(Icons.play_arrow),
            onPressed: firing ? null : onRun,
          ),
        ],
      ),
    );
  }
}

class _PlanLine extends StatelessWidget {
  final Map<String, dynamic> row;
  const _PlanLine({required this.row});

  @override
  Widget build(BuildContext context) {
    final id = (row['id'] ?? '').toString();
    final status = (row['status'] ?? '').toString();
    final created = (row['created_at'] ?? '').toString();
    final projectId = (row['project_id'] ?? '').toString();
    return InkWell(
      onTap: id.isEmpty
          ? null
          : () => Navigator.of(context).push(MaterialPageRoute(
                builder: (_) => PlanViewerScreen(
                  planId: id,
                  projectId: projectId,
                ),
              )),
      borderRadius: BorderRadius.circular(6),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 4, horizontal: 2),
        child: Row(
          children: [
            PlanStatusChip(status: status),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                created.isEmpty ? id : _fmtRel(created),
                style: GoogleFonts.jetBrainsMono(fontSize: 11),
                overflow: TextOverflow.ellipsis,
              ),
            ),
            const Icon(Icons.chevron_right,
                size: 16, color: DesignColors.textMuted),
          ],
        ),
      ),
    );
  }
}

class _CategoryChip extends StatelessWidget {
  final String category;
  const _CategoryChip({required this.category});

  @override
  Widget build(BuildContext context) {
    final color = switch (category) {
      'agents' => DesignColors.terminalCyan,
      'prompts' => DesignColors.terminalBlue,
      'policies' => DesignColors.warning,
      _ => DesignColors.textMuted,
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: color.withValues(alpha: 0.5)),
      ),
      child: Text(
        category,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
          fontWeight: FontWeight.w700,
          color: color,
        ),
      ),
    );
  }
}

class _OrphanChip extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: DesignColors.error.withValues(alpha: 0.1),
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: DesignColors.error.withValues(alpha: 0.4)),
      ),
      child: Text(
        'orphan',
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
          fontWeight: FontWeight.w700,
          color: DesignColors.error,
        ),
      ),
    );
  }
}

// Compact relative-time formatter. Same behaviour as the one in
// schedules_screen.dart; duplicated rather than exported because both
// files keep their helpers file-local.
String _fmtRel(String iso) {
  final dt = DateTime.tryParse(iso);
  if (dt == null) return iso;
  final now = DateTime.now();
  final diff = dt.difference(now);
  final abs = diff.abs();
  final future = !diff.isNegative;
  String mag;
  if (abs.inSeconds < 60) {
    mag = '<1m';
  } else if (abs.inMinutes < 60) {
    mag = '${abs.inMinutes}m';
  } else if (abs.inHours < 24) {
    mag = '${abs.inHours}h';
  } else if (abs.inDays < 30) {
    mag = '${abs.inDays}d';
  } else {
    return iso.split('T').first;
  }
  return future ? 'in $mag' : '$mag ago';
}
