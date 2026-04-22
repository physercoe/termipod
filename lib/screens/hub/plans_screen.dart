import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import 'plan_create_sheet.dart';
import 'plan_viewer_screen.dart';

/// Read-only list of team plans (blueprint §6.2, P2.4). Plans are the
/// shallow phase/step scaffolds that agents or schedulers drive — the
/// viewer screen handles the per-plan detail; this screen is just the
/// index. Rows come from `GET /v1/teams/{team}/plans`.
class PlansScreen extends ConsumerStatefulWidget {
  const PlansScreen({super.key});

  @override
  ConsumerState<PlansScreen> createState() => _PlansScreenState();
}

class _PlansScreenState extends ConsumerState<PlansScreen> {
  List<Map<String, dynamic>>? _rows;
  bool _loading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
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
      final rows = await client.listPlans();
      // Newest first — the API orders by created_at but defensively sort
      // here too so future API tweaks don't flip the list.
      rows.sort((a, b) => (b['created_at'] ?? '')
          .toString()
          .compareTo((a['created_at'] ?? '').toString()));
      if (!mounted) return;
      setState(() {
        _rows = rows;
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

  Future<void> _createPlan() async {
    final plan = await showModalBottomSheet<Map<String, dynamic>>(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.transparent,
      builder: (_) => const PlanCreateSheet(),
    );
    if (!mounted || plan == null) return;
    final planId = (plan['id'] ?? '').toString();
    final projectId = (plan['project_id'] ?? '').toString();
    await _load();
    if (!mounted || planId.isEmpty) return;
    await Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => PlanViewerScreen(
        planId: planId,
        projectId: projectId,
      ),
    ));
    if (mounted) _load();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Plans',
          style: GoogleFonts.spaceGrotesk(
            fontSize: 16,
            fontWeight: FontWeight.w700,
          ),
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh),
            tooltip: 'Refresh',
            onPressed: _loading ? null : _load,
          ),
        ],
      ),
      body: _body(),
      floatingActionButton: FloatingActionButton.small(
        heroTag: 'plans-fab',
        onPressed: _createPlan,
        tooltip: 'Start a plan',
        child: const Icon(Icons.add),
      ),
    );
  }

  Widget _body() {
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_error != null) {
      return Padding(
        padding: const EdgeInsets.all(24),
        child: Text(_error!,
            style: GoogleFonts.jetBrainsMono(
                fontSize: 12, color: DesignColors.error)),
      );
    }
    final rows = _rows ?? const [];
    if (rows.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            'No plans yet — plans appear here once a steward or template '
            'emits a phased scaffold.',
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(
                fontSize: 13, color: DesignColors.textMuted),
          ),
        ),
      );
    }
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView.separated(
        padding: const EdgeInsets.symmetric(vertical: 8),
        itemCount: rows.length,
        separatorBuilder: (_, __) => const Divider(height: 1),
        itemBuilder: (ctx, i) {
          final p = rows[i];
          return _PlanRow(plan: p);
        },
      ),
    );
  }
}

class _PlanRow extends StatelessWidget {
  final Map<String, dynamic> plan;
  const _PlanRow({required this.plan});

  @override
  Widget build(BuildContext context) {
    final id = (plan['id'] ?? '').toString();
    final projectId = (plan['project_id'] ?? '').toString();
    final version = (plan['version'] ?? 1).toString();
    final status = (plan['status'] ?? '').toString();
    final created = (plan['created_at'] ?? '').toString();
    return ListTile(
      title: Row(
        children: [
          PlanStatusChip(status: status),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              projectId.isEmpty ? '(no project)' : projectId,
              overflow: TextOverflow.ellipsis,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 13,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
          Text(
            'v$version',
            style: GoogleFonts.jetBrainsMono(
              fontSize: 11,
              color: DesignColors.textMuted,
            ),
          ),
        ],
      ),
      subtitle: Padding(
        padding: const EdgeInsets.only(top: 2),
        child: Text(
          '$id · $created',
          style: GoogleFonts.jetBrainsMono(
            fontSize: 10,
            color: DesignColors.textMuted,
          ),
        ),
      ),
      trailing: const Icon(Icons.chevron_right, size: 20),
      onTap: () => Navigator.of(context).push(MaterialPageRoute(
        builder: (_) => PlanViewerScreen(
          planId: id,
          projectId: projectId,
        ),
      )),
    );
  }
}

/// Colored status pill shared between the list and viewer screens.
/// Status values come from blueprint §6.2 plan lifecycle.
class PlanStatusChip extends StatelessWidget {
  final String status;
  const PlanStatusChip({super.key, required this.status});

  @override
  Widget build(BuildContext context) {
    final s = status.toLowerCase();
    final color = switch (s) {
      'running' => DesignColors.terminalBlue,
      'done' || 'completed' || 'succeeded' => DesignColors.success,
      'failed' || 'error' => DesignColors.error,
      'paused' || 'blocked' => DesignColors.warning,
      'cancelled' || 'skipped' => DesignColors.textMuted,
      'ready' => DesignColors.terminalCyan,
      'draft' || 'proposed' || 'pending' => DesignColors.textMuted,
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
        s.isEmpty ? '?' : s,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
          fontWeight: FontWeight.w700,
          color: color,
        ),
      ),
    );
  }
}
