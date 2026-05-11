import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import 'schedule_create_sheet.dart';
import 'schedule_edit_sheet.dart';

/// Full-screen list of team cron schedules. Each tile toggles enabled,
/// runs-now, edits cron/parameters in place, duplicates, or deletes.
class SchedulesScreen extends ConsumerStatefulWidget {
  /// When non-null, the screen lists only this project's schedules.
  /// Tile-entry call sites should pass the current project; team-wide
  /// entry points pass null.
  final String? projectId;

  const SchedulesScreen({super.key, this.projectId});

  @override
  ConsumerState<SchedulesScreen> createState() => _SchedulesScreenState();
}

class _SchedulesScreenState extends ConsumerState<SchedulesScreen> {
  List<Map<String, dynamic>>? _rows;
  String? _error;
  bool _loading = true;
  // Schedule ids with an in-flight runSchedule call. Gates the Run now
  // button so a double-tap doesn't fire two plans.
  final Set<String> _running = <String>{};

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final pid = widget.projectId;
      final resp = await client.listSchedulesCached(
        projectId: pid != null && pid.isNotEmpty ? pid : null,
      );
      if (!mounted) return;
      setState(() {
        _rows = resp.body;
        _loading = false;
        _error = null;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
  }

  Future<void> _toggle(String id, bool enabled) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      await client.patchSchedule(id, enabled: enabled);
      await _load();
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Toggle failed: $e')),
      );
    }
  }

  Future<void> _confirmDelete(String id, String name) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Delete schedule?'),
        content: Text('"$name" will stop firing. This cannot be undone.'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: DesignColors.error),
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('Delete'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      await client.deleteSchedule(id);
      await _load();
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Delete failed: $e')),
      );
    }
  }

  Future<void> _runNow(String id, String name) async {
    if (_running.contains(id)) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    final messenger = ScaffoldMessenger.of(context);
    setState(() => _running.add(id));
    try {
      final planId = await client.runSchedule(id);
      if (!mounted) return;
      messenger.showSnackBar(SnackBar(
        content: Text(planId.isEmpty
            ? 'Fired $name'
            : 'Fired $name → plan $planId'),
      ));
      await _load();
    } catch (e) {
      if (!mounted) return;
      messenger.showSnackBar(SnackBar(content: Text('Run failed: $e')));
    } finally {
      if (mounted) setState(() => _running.remove(id));
    }
  }

  Future<void> _openCreate({Map<String, dynamic>? initial}) async {
    final created = await showModalBottomSheet<bool>(
      context: context,
      isScrollControlled: true,
      builder: (_) => ScheduleCreateSheet(initial: initial),
    );
    if (created == true) await _load();
  }

  Future<void> _openEdit(Map<String, dynamic> row) async {
    final updated = await showModalBottomSheet<bool>(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.transparent,
      builder: (_) => ScheduleEditSheet(schedule: row),
    );
    if (updated == true) await _load();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Schedules',
          style: GoogleFonts.spaceGrotesk(
              fontSize: 18, fontWeight: FontWeight.w700),
        ),
        actions: [
          PopupMenuButton<String>(
            tooltip: 'More',
            icon: const Icon(Icons.more_vert),
            onSelected: (v) {
              if (v == 'new') _openCreate();
              if (v == 'refresh') _load();
            },
            itemBuilder: (_) => const [
              PopupMenuItem(
                value: 'new',
                child: ListTile(
                  dense: true,
                  contentPadding: EdgeInsets.zero,
                  leading: Icon(Icons.add, size: 20),
                  title: Text('New schedule'),
                ),
              ),
              PopupMenuItem(
                value: 'refresh',
                child: ListTile(
                  dense: true,
                  contentPadding: EdgeInsets.zero,
                  leading: Icon(Icons.refresh, size: 20),
                  title: Text('Refresh'),
                ),
              ),
            ],
          ),
        ],
      ),
      body: _loading && _rows == null
          ? const Center(child: CircularProgressIndicator())
          : _error != null
              ? Center(
                  child: Padding(
                    padding: const EdgeInsets.all(24),
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Text(_error!,
                            textAlign: TextAlign.center,
                            style: GoogleFonts.jetBrainsMono(
                                color: DesignColors.error, fontSize: 12)),
                        const SizedBox(height: 16),
                        FilledButton.tonalIcon(
                          onPressed: () {
                            setState(() {
                              _loading = true;
                              _error = null;
                            });
                            _load();
                          },
                          icon: const Icon(Icons.refresh, size: 16),
                          label: const Text('Retry'),
                        ),
                      ],
                    ),
                  ),
                )
              : RefreshIndicator(
                  onRefresh: _load,
                  child: _buildList(),
                ),
    );
  }

  // Summary header: how many schedules are enabled and when the next
  // firing lands. Lead with status so the screen reads as a monitoring
  // surface first and an authoring surface second.
  String _buildSummary(List<Map<String, dynamic>> rows) {
    if (rows.isEmpty) return '';
    final enabled = rows.where((r) => r['enabled'] == true).length;
    final now = DateTime.now();
    DateTime? nextDt;
    for (final r in rows) {
      if (r['enabled'] != true) continue;
      final iso = (r['next_run_at'] ?? '').toString();
      if (iso.isEmpty) continue;
      final dt = DateTime.tryParse(iso);
      if (dt == null || dt.isBefore(now)) continue;
      if (nextDt == null || dt.isBefore(nextDt)) nextDt = dt;
    }
    final parts = <String>['$enabled of ${rows.length} enabled'];
    if (nextDt != null) {
      parts.add('next fires ${_fmtRel(nextDt.toIso8601String())}');
    }
    return parts.join(' · ');
  }

  Widget _buildList() {
    final rows = _rows ?? const <Map<String, dynamic>>[];
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final summary = _buildSummary(rows);
    return ListView(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 96),
      children: [
        if (summary.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(bottom: 12, top: 4),
            child: Text(
              summary,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 11,
                color: isDark
                    ? DesignColors.textSecondary
                    : DesignColors.textSecondaryLight,
              ),
            ),
          ),
        if (rows.isEmpty)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 48),
            child: Center(
              child: Text(
                'No schedules yet. Use the menu to create one.',
                textAlign: TextAlign.center,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 13,
                  color: isDark
                      ? DesignColors.textMuted
                      : DesignColors.textMutedLight,
                ),
              ),
            ),
          )
        else
          for (final row in rows) ...[
            _ScheduleTile(
              row: row,
              running: _running.contains((row['id'] ?? '').toString()),
              onToggle: (v) => _toggle((row['id'] ?? '').toString(), v),
              onDelete: () => _confirmDelete(
                (row['id'] ?? '').toString(),
                (row['template_id'] ?? row['id'] ?? '').toString(),
              ),
              onRunNow: () => _runNow(
                (row['id'] ?? '').toString(),
                (row['template_id'] ?? row['id'] ?? '').toString(),
              ),
              onEdit: () => _openEdit(row),
              onDuplicate: () => _openCreate(initial: row),
            ),
            const SizedBox(height: 8),
          ],
      ],
    );
  }
}

class _ScheduleTile extends StatelessWidget {
  final Map<String, dynamic> row;
  final bool running;
  final ValueChanged<bool> onToggle;
  final VoidCallback onDelete;
  final VoidCallback onRunNow;
  final VoidCallback onEdit;
  final VoidCallback onDuplicate;
  const _ScheduleTile({
    required this.row,
    required this.running,
    required this.onToggle,
    required this.onDelete,
    required this.onRunNow,
    required this.onEdit,
    required this.onDuplicate,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final template = (row['template_id'] ?? '').toString();
    final trigger = (row['trigger_kind'] ?? 'cron').toString();
    final cron = (row['cron_expr'] ?? '').toString();
    final enabled = row['enabled'] == true;
    final nextRun = (row['next_run_at'] ?? '').toString();
    final lastRun = (row['last_run_at'] ?? '').toString();
    final header = template.isEmpty ? '(unknown template)' : template;
    final detail = trigger == 'cron' && cron.isNotEmpty
        ? '$trigger · $cron'
        : trigger;

    final meta = <String>[];
    if (nextRun.isNotEmpty) meta.add('next ${_fmtRel(nextRun)}');
    if (lastRun.isNotEmpty) meta.add('last ${_fmtRel(lastRun)}');

    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      decoration: BoxDecoration(
        color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(
          color: isDark ? DesignColors.borderDark : DesignColors.borderLight,
        ),
      ),
      child: Row(
        children: [
          Switch(value: enabled, onChanged: onToggle),
          const SizedBox(width: 8),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(header,
                    style: GoogleFonts.spaceGrotesk(
                        fontSize: 14, fontWeight: FontWeight.w600)),
                const SizedBox(height: 2),
                Text(detail,
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 11,
                      color: isDark
                          ? DesignColors.textSecondary
                          : DesignColors.textSecondaryLight,
                    )),
                if (meta.isNotEmpty)
                  Padding(
                    padding: const EdgeInsets.only(top: 2),
                    child: Text(meta.join(' · '),
                        style: GoogleFonts.jetBrainsMono(
                          fontSize: 10,
                          color: isDark
                              ? DesignColors.textMuted
                              : DesignColors.textMutedLight,
                        )),
                  ),
              ],
            ),
          ),
          // Run now works for disabled schedules too — the hub endpoint
          // has no enabled gate and "test a disabled schedule" is a
          // legitimate case.
          IconButton(
            tooltip: 'Run now',
            icon: running
                ? const SizedBox(
                    width: 16,
                    height: 16,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : const Icon(Icons.play_arrow),
            color: DesignColors.success,
            onPressed: running ? null : onRunNow,
          ),
          PopupMenuButton<String>(
            tooltip: 'More',
            icon: const Icon(Icons.more_vert),
            onSelected: (v) {
              if (v == 'edit') onEdit();
              if (v == 'duplicate') onDuplicate();
              if (v == 'delete') onDelete();
            },
            itemBuilder: (_) => const [
              PopupMenuItem(
                value: 'edit',
                child: ListTile(
                  dense: true,
                  contentPadding: EdgeInsets.zero,
                  leading: Icon(Icons.edit_outlined, size: 20),
                  title: Text('Edit'),
                ),
              ),
              PopupMenuItem(
                value: 'duplicate',
                child: ListTile(
                  dense: true,
                  contentPadding: EdgeInsets.zero,
                  leading: Icon(Icons.copy, size: 20),
                  title: Text('Duplicate'),
                ),
              ),
              PopupMenuItem(
                value: 'delete',
                child: ListTile(
                  dense: true,
                  contentPadding: EdgeInsets.zero,
                  leading: Icon(Icons.delete, color: DesignColors.error, size: 20),
                  title: Text('Delete'),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

// Turns an ISO-8601 timestamp into a compact relative string. Falls
// back to the raw value on parse failure so a surprising format still
// shows *something* rather than nothing.
String _fmtRel(String iso) {
  final dt = DateTime.tryParse(iso);
  if (dt == null) return iso;
  final now = DateTime.now();
  final diff = dt.difference(now);
  final abs = diff.abs();
  final future = diff.isNegative == false;
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
