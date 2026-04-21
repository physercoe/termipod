import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import 'schedule_create_sheet.dart';

/// Full-screen list of team cron schedules. Edits aren't supported server-side
/// yet, so users toggle enabled, delete, or create a replacement.
class SchedulesScreen extends ConsumerStatefulWidget {
  const SchedulesScreen({super.key});

  @override
  ConsumerState<SchedulesScreen> createState() => _SchedulesScreenState();
}

class _SchedulesScreenState extends ConsumerState<SchedulesScreen> {
  List<Map<String, dynamic>>? _rows;
  String? _error;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final rows = await client.listSchedules();
      if (!mounted) return;
      setState(() {
        _rows = rows;
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

  Future<void> _openCreate() async {
    final created = await showModalBottomSheet<bool>(
      context: context,
      isScrollControlled: true,
      builder: (_) => const ScheduleCreateSheet(),
    );
    if (created == true) await _load();
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
      ),
      body: _loading && _rows == null
          ? const Center(child: CircularProgressIndicator())
          : _error != null
              ? Center(
                  child: Padding(
                    padding: const EdgeInsets.all(24),
                    child: Text(_error!,
                        style: GoogleFonts.jetBrainsMono(
                            color: DesignColors.error, fontSize: 12)),
                  ),
                )
              : RefreshIndicator(
                  onRefresh: _load,
                  child: _buildList(),
                ),
      floatingActionButton: FloatingActionButton.extended(
        heroTag: 'schedules-fab',
        onPressed: _openCreate,
        icon: const Icon(Icons.add),
        label: const Text('New'),
      ),
    );
  }

  Widget _buildList() {
    final rows = _rows ?? const <Map<String, dynamic>>[];
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return ListView(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 96),
      children: [
        if (rows.isEmpty)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 48),
            child: Center(
              child: Text(
                'No schedules yet. Tap + to create one.',
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
              onToggle: (v) => _toggle((row['id'] ?? '').toString(), v),
              onDelete: () => _confirmDelete(
                (row['id'] ?? '').toString(),
                (row['name'] ?? '').toString(),
              ),
            ),
            const SizedBox(height: 8),
          ],
        Padding(
          padding: const EdgeInsets.only(top: 16),
          child: Text(
            'Edits require delete + recreate.',
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 12,
              color: isDark
                  ? DesignColors.textMuted
                  : DesignColors.textMutedLight,
            ),
          ),
        ),
      ],
    );
  }
}

class _ScheduleTile extends StatelessWidget {
  final Map<String, dynamic> row;
  final ValueChanged<bool> onToggle;
  final VoidCallback onDelete;
  const _ScheduleTile({
    required this.row,
    required this.onToggle,
    required this.onDelete,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final name = (row['name'] ?? '').toString();
    final cron = (row['cron_expr'] ?? '').toString();
    final enabled = row['enabled'] == true;
    final nextRun = (row['next_run_at'] ?? '').toString();
    final lastRun = (row['last_run_at'] ?? '').toString();

    final meta = <String>[];
    if (nextRun.isNotEmpty) meta.add('next $nextRun');
    if (lastRun.isNotEmpty) meta.add('last $lastRun');

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
                Text(name.isEmpty ? '(unnamed)' : name,
                    style: GoogleFonts.spaceGrotesk(
                        fontSize: 14, fontWeight: FontWeight.w600)),
                const SizedBox(height: 2),
                Text(cron,
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
          IconButton(
            icon: const Icon(Icons.delete),
            color: DesignColors.error,
            onPressed: onDelete,
          ),
        ],
      ),
    );
  }
}
