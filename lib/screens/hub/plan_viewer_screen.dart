import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import 'plans_screen.dart';

/// Read-only viewer for a single plan (blueprint §6.2, P2.4). Shows the
/// plan's meta, its spec, and its steps grouped by phase_idx. Editing and
/// step-advance come later — for now this is a review surface so a human
/// director can inspect what an agent or steward emitted.
class PlanViewerScreen extends ConsumerStatefulWidget {
  final String planId;
  final String projectId;
  const PlanViewerScreen({
    super.key,
    required this.planId,
    required this.projectId,
  });

  @override
  ConsumerState<PlanViewerScreen> createState() => _PlanViewerScreenState();
}

class _PlanViewerScreenState extends ConsumerState<PlanViewerScreen> {
  Map<String, dynamic>? _plan;
  List<Map<String, dynamic>> _steps = const [];
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
      // Parallel fetches — the plan meta and its steps are independent.
      final results = await Future.wait([
        client.getPlan(widget.planId),
        client.listPlanSteps(widget.planId),
      ]);
      if (!mounted) return;
      final plan = results[0] as Map<String, dynamic>;
      final steps = (results[1] as List<Map<String, dynamic>>).toList();
      // Steps arrive ordered by phase then step in the API but sort
      // defensively so the viewer stays deterministic.
      steps.sort((a, b) {
        final ap = (a['phase_idx'] as num?)?.toInt() ?? 0;
        final bp = (b['phase_idx'] as num?)?.toInt() ?? 0;
        if (ap != bp) return ap.compareTo(bp);
        final ai = (a['step_idx'] as num?)?.toInt() ?? 0;
        final bi = (b['step_idx'] as num?)?.toInt() ?? 0;
        return ai.compareTo(bi);
      });
      setState(() {
        _plan = plan;
        _steps = steps;
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

  @override
  Widget build(BuildContext context) {
    final status = (_plan?['status'] ?? '').toString();
    return Scaffold(
      appBar: AppBar(
        title: Row(
          children: [
            Text(
              'Plan',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 16,
                fontWeight: FontWeight.w700,
              ),
            ),
            if (status.isNotEmpty) ...[
              const SizedBox(width: 8),
              PlanStatusChip(status: status),
            ],
          ],
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
    final plan = _plan;
    if (plan == null) return const SizedBox.shrink();
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
        children: [
          _header(plan),
          const SizedBox(height: 12),
          _specSection(plan),
          const SizedBox(height: 16),
          _stepsSection(),
        ],
      ),
    );
  }

  Widget _header(Map<String, dynamic> plan) {
    final rows = <_KV>[
      _KV('id', (plan['id'] ?? '').toString()),
      _KV('project', widget.projectId),
      _KV('version', '${plan['version'] ?? 1}'),
      if ((plan['template_id'] ?? '').toString().isNotEmpty)
        _KV('template', (plan['template_id']).toString()),
      _KV('created', (plan['created_at'] ?? '').toString()),
      if ((plan['started_at'] ?? '').toString().isNotEmpty)
        _KV('started', plan['started_at'].toString()),
      if ((plan['completed_at'] ?? '').toString().isNotEmpty)
        _KV('completed', plan['completed_at'].toString()),
    ];
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: rows.map((r) => _kvRow(r.key, r.value)).toList(),
    );
  }

  Widget _specSection(Map<String, dynamic> plan) {
    final spec = plan['spec_json'];
    if (spec == null || spec is Map && spec.isEmpty) {
      return const SizedBox.shrink();
    }
    String pretty;
    try {
      pretty = const JsonEncoder.withIndent('  ').convert(spec);
    } catch (_) {
      pretty = spec.toString();
    }
    return _Section(
      title: 'Spec',
      child: SelectableText(
        pretty,
        style: GoogleFonts.jetBrainsMono(fontSize: 11, height: 1.4),
      ),
    );
  }

  Widget _stepsSection() {
    if (_steps.isEmpty) {
      return _Section(
        title: 'Steps',
        child: Text(
          'No steps yet.',
          style: GoogleFonts.spaceGrotesk(
              fontSize: 12, color: DesignColors.textMuted),
        ),
      );
    }
    // Group by phase_idx so phases can be shown as sub-headers.
    final byPhase = <int, List<Map<String, dynamic>>>{};
    for (final s in _steps) {
      final ph = (s['phase_idx'] as num?)?.toInt() ?? 0;
      byPhase.putIfAbsent(ph, () => []).add(s);
    }
    final phases = byPhase.keys.toList()..sort();
    return _Section(
      title: 'Steps (${_steps.length})',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          for (final ph in phases) ...[
            Padding(
              padding: const EdgeInsets.only(top: 8, bottom: 6),
              child: Text(
                'Phase $ph',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 12,
                  fontWeight: FontWeight.w700,
                  color: DesignColors.textMuted,
                ),
              ),
            ),
            for (final s in byPhase[ph]!) _StepRow(step: s),
          ],
        ],
      ),
    );
  }

  Widget _kvRow(String k, String v) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: RichText(
        text: TextSpan(
          style: GoogleFonts.jetBrainsMono(
            fontSize: 11,
            color: DesignColors.textSecondary,
          ),
          children: [
            TextSpan(
              text: '$k: ',
              style: const TextStyle(color: DesignColors.textMuted),
            ),
            TextSpan(text: v),
          ],
        ),
      ),
    );
  }
}

class _KV {
  final String key;
  final String value;
  const _KV(this.key, this.value);
}

class _Section extends StatelessWidget {
  final String title;
  final Widget child;
  const _Section({required this.title, required this.child});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        border: Border.all(color: DesignColors.borderDark),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(
            title,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 13,
              fontWeight: FontWeight.w700,
            ),
          ),
          const SizedBox(height: 8),
          child,
        ],
      ),
    );
  }
}

class _StepRow extends StatelessWidget {
  final Map<String, dynamic> step;
  const _StepRow({required this.step});

  @override
  Widget build(BuildContext context) {
    final kind = (step['kind'] ?? '').toString();
    final status = (step['status'] ?? '').toString();
    final idx = (step['step_idx'] ?? 0).toString();
    final agentId = (step['agent_id'] ?? '').toString();
    final spec = step['spec_json'];
    String? specPreview;
    if (spec is Map && spec.isNotEmpty) {
      try {
        specPreview = const JsonEncoder.withIndent('  ').convert(spec);
      } catch (_) {
        specPreview = spec.toString();
      }
    }
    return Container(
      margin: const EdgeInsets.only(bottom: 6),
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: DesignColors.surfaceDark.withValues(alpha: 0.4),
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: DesignColors.borderDark),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              PlanStatusChip(status: status),
              const SizedBox(width: 8),
              Text(
                '#$idx',
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  color: DesignColors.textMuted,
                ),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  kind,
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
            ],
          ),
          if (agentId.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(top: 4),
              child: Text(
                'agent: $agentId',
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 10,
                  color: DesignColors.textMuted,
                ),
              ),
            ),
          if (specPreview != null)
            Padding(
              padding: const EdgeInsets.only(top: 6),
              child: SelectableText(
                specPreview,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 10,
                  height: 1.4,
                  color: DesignColors.textSecondary,
                ),
              ),
            ),
        ],
      ),
    );
  }
}
