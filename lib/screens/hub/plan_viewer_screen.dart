import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import 'plan_step_create_sheet.dart';
import 'plans_screen.dart';

/// Read-write viewer for a single plan (blueprint §6.2, P2.4). Shows the
/// plan's meta, progress across steps, the spec, and the steps grouped by
/// phase_idx. Tapping a step opens a detail sheet that supports status
/// transitions (e.g. mark blocked, mark done) for the cases where a human
/// director needs to override an agent. Plan-level lifecycle moves
/// (ready/running/cancelled/...) live behind a popup menu on the app bar.
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

// Server-side enums (hub/internal/server/handlers_plans.go).
// Step statuses are not validated server-side but the blueprint names
// these; keeping them here as the canonical set shown in the UI.
const _planStatuses = ['draft', 'ready', 'running', 'completed', 'failed', 'cancelled'];
const _stepStatuses = ['pending', 'running', 'completed', 'failed', 'blocked', 'skipped'];

class _PlanViewerScreenState extends ConsumerState<PlanViewerScreen> {
  Map<String, dynamic>? _plan;
  List<Map<String, dynamic>> _steps = const [];
  bool _loading = true;
  bool _busy = false;
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
      final results = await Future.wait([
        client.getPlan(widget.planId),
        client.listPlanSteps(widget.planId),
      ]);
      if (!mounted) return;
      final plan = results[0] as Map<String, dynamic>;
      final steps = (results[1] as List<Map<String, dynamic>>).toList();
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

  Future<void> _setPlanStatus(String status) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() => _busy = true);
    final messenger = ScaffoldMessenger.of(context);
    try {
      await client.updatePlan(widget.planId, status: status);
      if (!mounted) return;
      messenger.showSnackBar(
          SnackBar(content: Text('Plan → $status')));
      await _load();
    } catch (e) {
      if (!mounted) return;
      messenger.showSnackBar(SnackBar(content: Text('Update failed: $e')));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _setStepStatus(String stepId, String status) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    final messenger = ScaffoldMessenger.of(context);
    try {
      await client.updatePlanStep(
        widget.planId,
        stepId,
        status: status,
      );
      if (!mounted) return;
      messenger.showSnackBar(SnackBar(content: Text('Step → $status')));
      await _load();
    } catch (e) {
      if (!mounted) return;
      messenger.showSnackBar(SnackBar(content: Text('Update failed: $e')));
    }
  }

  Future<void> _openStepCreateSheet() async {
    // Default the new step to phase 0 / step (max+1) within phase 0, or
    // whatever the last phase is if the plan already has steps. Keeps the
    // default sensible while letting the user edit either field.
    int defaultPhase = 0;
    int defaultStep = 0;
    if (_steps.isNotEmpty) {
      defaultPhase = _steps
          .map((s) => (s['phase_idx'] as num?)?.toInt() ?? 0)
          .reduce((a, b) => a > b ? a : b);
      final inPhase = _steps.where(
          (s) => ((s['phase_idx'] as num?)?.toInt() ?? 0) == defaultPhase);
      if (inPhase.isNotEmpty) {
        defaultStep = inPhase
                .map((s) => (s['step_idx'] as num?)?.toInt() ?? 0)
                .reduce((a, b) => a > b ? a : b) +
            1;
      }
    }
    final created = await showModalBottomSheet<Map<String, dynamic>>(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.transparent,
      builder: (_) => PlanStepCreateSheet(
        planId: widget.planId,
        defaultPhaseIdx: defaultPhase,
        defaultStepIdx: defaultStep,
      ),
    );
    if (created == null || !mounted) return;
    await _load();
  }

  void _openStepSheet(Map<String, dynamic> step) {
    showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      useSafeArea: true,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
      builder: (_) => _StepDetailSheet(
        step: step,
        onTransition: (newStatus) {
          Navigator.of(context).pop();
          _setStepStatus((step['id'] ?? '').toString(), newStatus);
        },
      ),
    );
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
          PopupMenuButton<String>(
            tooltip: 'Plan actions',
            enabled: !_busy && _plan != null,
            icon: const Icon(Icons.more_vert),
            onSelected: _setPlanStatus,
            itemBuilder: (_) => [
              for (final s in _planStatuses)
                if (s != status)
                  PopupMenuItem(
                    value: s,
                    child: Row(
                      children: [
                        PlanStatusChip(status: s),
                        const SizedBox(width: 8),
                        Text('Set $s'),
                      ],
                    ),
                  ),
            ],
          ),
        ],
      ),
      body: _body(),
      floatingActionButton: _plan == null
          ? null
          : FloatingActionButton.small(
              heroTag: 'plan-step-fab',
              onPressed: _busy ? null : _openStepCreateSheet,
              tooltip: 'Add step',
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
    final plan = _plan;
    if (plan == null) return const SizedBox.shrink();
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
        children: [
          _progressSection(),
          const SizedBox(height: 12),
          _header(plan),
          const SizedBox(height: 12),
          _specSection(plan),
          const SizedBox(height: 16),
          _stepsSection(),
        ],
      ),
    );
  }

  Widget _progressSection() {
    final total = _steps.length;
    if (total == 0) return const SizedBox.shrink();
    int done = 0, running = 0, failed = 0, blocked = 0;
    for (final s in _steps) {
      final st = (s['status'] ?? '').toString().toLowerCase();
      if (st == 'completed' || st == 'done' || st == 'succeeded') done++;
      if (st == 'running') running++;
      if (st == 'failed' || st == 'error') failed++;
      if (st == 'blocked') blocked++;
    }
    final pct = total == 0 ? 0.0 : done / total;
    return _Section(
      title: 'Progress ($done / $total)',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          ClipRRect(
            borderRadius: BorderRadius.circular(4),
            child: LinearProgressIndicator(
              value: pct,
              minHeight: 6,
              backgroundColor:
                  DesignColors.textMuted.withValues(alpha: 0.2),
              valueColor: const AlwaysStoppedAnimation<Color>(
                  DesignColors.success),
            ),
          ),
          const SizedBox(height: 8),
          Wrap(
            spacing: 12,
            children: [
              _pill('done', done, DesignColors.success),
              if (running > 0)
                _pill('running', running, DesignColors.terminalBlue),
              if (blocked > 0)
                _pill('blocked', blocked, DesignColors.warning),
              if (failed > 0) _pill('failed', failed, DesignColors.error),
            ],
          ),
        ],
      ),
    );
  }

  Widget _pill(String label, int count, Color color) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.12),
        border: Border.all(color: color.withValues(alpha: 0.5)),
        borderRadius: BorderRadius.circular(12),
      ),
      child: Text(
        '$label · $count',
        style: GoogleFonts.jetBrainsMono(
            fontSize: 10, fontWeight: FontWeight.w700, color: color),
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
            for (final s in byPhase[ph]!)
              _StepRow(step: s, onTap: () => _openStepSheet(s)),
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
  final VoidCallback onTap;
  const _StepRow({required this.step, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final kind = (step['kind'] ?? '').toString();
    final status = (step['status'] ?? '').toString();
    final idx = (step['step_idx'] ?? 0).toString();
    final agentId = (step['agent_id'] ?? '').toString();
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(4),
      child: Container(
        margin: const EdgeInsets.only(bottom: 6),
        padding: const EdgeInsets.all(10),
        decoration: BoxDecoration(
          color: DesignColors.surfaceDark.withValues(alpha: 0.4),
          borderRadius: BorderRadius.circular(4),
          border: Border.all(color: DesignColors.borderDark),
        ),
        child: Row(
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
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    kind,
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  if (agentId.isNotEmpty)
                    Text(
                      'agent: $agentId',
                      overflow: TextOverflow.ellipsis,
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 10,
                        color: DesignColors.textMuted,
                      ),
                    ),
                ],
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

class _StepDetailSheet extends StatelessWidget {
  final Map<String, dynamic> step;
  final ValueChanged<String> onTransition;
  const _StepDetailSheet({
    required this.step,
    required this.onTransition,
  });

  @override
  Widget build(BuildContext context) {
    final kind = (step['kind'] ?? '').toString();
    final status = (step['status'] ?? '').toString();
    final phase = (step['phase_idx'] ?? 0).toString();
    final idx = (step['step_idx'] ?? 0).toString();
    final agentId = (step['agent_id'] ?? '').toString();
    final spec = step['spec_json'];
    final inputs = step['input_refs_json'];
    final outputs = step['output_refs_json'];
    return DraggableScrollableSheet(
      expand: false,
      initialChildSize: 0.55,
      minChildSize: 0.3,
      maxChildSize: 0.95,
      builder: (_, controller) => ListView(
        controller: controller,
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
        children: [
          Row(
            children: [
              Expanded(
                child: Row(
                  children: [
                    PlanStatusChip(status: status),
                    const SizedBox(width: 8),
                    Text(
                      'Phase $phase · step $idx',
                      style: GoogleFonts.jetBrainsMono(
                          fontSize: 12,
                          color: DesignColors.textMuted),
                    ),
                  ],
                ),
              ),
              IconButton(
                icon: const Icon(Icons.close),
                onPressed: () => Navigator.of(context).pop(),
              ),
            ],
          ),
          const SizedBox(height: 8),
          Text(
            kind,
            style: GoogleFonts.jetBrainsMono(
                fontSize: 16, fontWeight: FontWeight.w700),
          ),
          if (agentId.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(top: 4),
              child: Text(
                'agent: $agentId',
                style: GoogleFonts.jetBrainsMono(
                    fontSize: 11, color: DesignColors.textMuted),
              ),
            ),
          const SizedBox(height: 16),
          ..._specBlocks(context, spec),
          _kvBlock(context, 'Inputs', inputs),
          _kvBlock(context, 'Outputs', outputs),
          const SizedBox(height: 16),
          Text(
            'Set status',
            style: GoogleFonts.spaceGrotesk(
              fontSize: 12,
              fontWeight: FontWeight.w700,
              color: DesignColors.textMuted,
            ),
          ),
          const SizedBox(height: 8),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              for (final s in _stepStatuses)
                if (s != status.toLowerCase())
                  OutlinedButton(
                    onPressed: () => onTransition(s),
                    child: Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        PlanStatusChip(status: s),
                        const SizedBox(width: 6),
                        Text(s),
                      ],
                    ),
                  ),
            ],
          ),
        ],
      ),
    );
  }

  /// Structured renderer for a step's spec_json: pulls well-known
  /// long-text fields (the actual command/prompt/script the step runs)
  /// into labeled code blocks so a human reviewing a plan can see what
  /// each step actually does without squinting at a JSON tree. The
  /// remaining scalar fields render as a compact kv block; anything
  /// left (nested objects, arrays) falls into the raw JSON tail.
  List<Widget> _specBlocks(BuildContext context, dynamic raw) {
    if (raw == null) return const [];
    if (raw is! Map) return [_kvBlock(context, 'Spec', raw)];
    if (raw.isEmpty) return const [];

    // Known verbose fields, in display order. command/cmd/script tend to
    // be shell-ish (mono), prompt/question/body/description lean prose.
    const textKeys = <String>[
      'command',
      'cmd',
      'script',
      'prompt',
      'question',
      'body',
      'description',
    ];
    final textBlocks = <MapEntry<String, String>>[];
    final scalars = <MapEntry<String, dynamic>>[];
    final other = <String, dynamic>{};

    raw.forEach((k, v) {
      final key = k.toString();
      if (v is String && v.isNotEmpty && textKeys.contains(key)) {
        textBlocks.add(MapEntry(key, v));
      } else if (v is String || v is num || v is bool) {
        scalars.add(MapEntry(key, v));
      } else if (v != null) {
        other[key] = v;
      }
    });

    final out = <Widget>[];
    for (final block in textBlocks) {
      out.add(_codeBlock(context, block.key, block.value));
    }
    if (scalars.isNotEmpty) {
      out.add(_scalarGrid(context, scalars));
    }
    if (other.isNotEmpty) {
      out.add(_kvBlock(context, 'Other', other));
    }
    if (out.isEmpty) {
      // Pure-map spec with no recognised fields (rare): still show it.
      out.add(_kvBlock(context, 'Spec', raw));
    }
    return out;
  }

  Widget _codeBlock(BuildContext context, String title, String content) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            title,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 11,
              fontWeight: FontWeight.w700,
              color: DesignColors.textMuted,
            ),
          ),
          const SizedBox(height: 4),
          Container(
            width: double.infinity,
            padding: const EdgeInsets.all(10),
            decoration: BoxDecoration(
              color: Colors.black.withValues(alpha: 0.35),
              borderRadius: BorderRadius.circular(4),
              border: Border.all(color: DesignColors.borderDark),
            ),
            child: SelectableText(
              content,
              style: GoogleFonts.jetBrainsMono(fontSize: 12, height: 1.45),
            ),
          ),
        ],
      ),
    );
  }

  Widget _scalarGrid(
      BuildContext context, List<MapEntry<String, dynamic>> scalars) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          for (final e in scalars)
            Padding(
              padding: const EdgeInsets.only(bottom: 4),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  SizedBox(
                    width: 100,
                    child: Text(
                      e.key,
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 11,
                        color: DesignColors.textMuted,
                      ),
                    ),
                  ),
                  Expanded(
                    child: SelectableText(
                      e.value.toString(),
                      style:
                          GoogleFonts.jetBrainsMono(fontSize: 11, height: 1.4),
                    ),
                  ),
                ],
              ),
            ),
        ],
      ),
    );
  }

  Widget _kvBlock(BuildContext context, String title, dynamic value) {
    if (value == null) return const SizedBox.shrink();
    if (value is Map && value.isEmpty) return const SizedBox.shrink();
    if (value is List && value.isEmpty) return const SizedBox.shrink();
    String pretty;
    try {
      pretty = const JsonEncoder.withIndent('  ').convert(value);
    } catch (_) {
      pretty = value.toString();
    }
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            title,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 11,
              fontWeight: FontWeight.w700,
              color: DesignColors.textMuted,
            ),
          ),
          const SizedBox(height: 4),
          Container(
            width: double.infinity,
            padding: const EdgeInsets.all(10),
            decoration: BoxDecoration(
              color: DesignColors.surfaceDark.withValues(alpha: 0.4),
              borderRadius: BorderRadius.circular(4),
              border: Border.all(color: DesignColors.borderDark),
            ),
            child: SelectableText(
              pretty,
              style: GoogleFonts.jetBrainsMono(
                  fontSize: 11, height: 1.4),
            ),
          ),
        ],
      ),
    );
  }
}
