import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';

/// Append a new step to an existing plan (blueprint §6.2). The server
/// accepts phase_idx / step_idx / kind as required fields; spec_json is
/// optional and interpreted per kind. Human directors use this when they
/// need to inject an ad-hoc shell command or a new human_decision hop
/// into a running plan.
class PlanStepCreateSheet extends ConsumerStatefulWidget {
  final String planId;
  final int defaultPhaseIdx;
  final int defaultStepIdx;
  const PlanStepCreateSheet({
    super.key,
    required this.planId,
    required this.defaultPhaseIdx,
    required this.defaultStepIdx,
  });

  @override
  ConsumerState<PlanStepCreateSheet> createState() =>
      _PlanStepCreateSheetState();
}

class _PlanStepCreateSheetState extends ConsumerState<PlanStepCreateSheet> {
  late final TextEditingController _phase;
  late final TextEditingController _step;
  final _spec = TextEditingController();
  String _kind = 'shell';
  bool _submitting = false;
  String? _specError;

  static const _kinds = [
    'agent_spawn',
    'llm_call',
    'shell',
    'mcp_call',
    'human_decision',
  ];

  @override
  void initState() {
    super.initState();
    _phase = TextEditingController(text: widget.defaultPhaseIdx.toString());
    _step = TextEditingController(text: widget.defaultStepIdx.toString());
  }

  @override
  void dispose() {
    _phase.dispose();
    _step.dispose();
    _spec.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    final phase = int.tryParse(_phase.text.trim());
    final step = int.tryParse(_step.text.trim());
    if (phase == null || step == null) return;
    Map<String, dynamic>? spec;
    final specText = _spec.text.trim();
    if (specText.isNotEmpty) {
      try {
        final decoded = jsonDecode(specText);
        if (decoded is! Map) {
          setState(() => _specError = 'Spec must be a JSON object.');
          return;
        }
        spec = decoded.cast<String, dynamic>();
      } catch (e) {
        setState(() => _specError = 'Invalid JSON: $e');
        return;
      }
    }
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() {
      _submitting = true;
      _specError = null;
    });
    try {
      final created = await client.createPlanStep(
        widget.planId,
        phaseIdx: phase,
        stepIdx: step,
        kind: _kind,
        spec: spec,
      );
      if (!mounted) return;
      Navigator.of(context).pop(created);
    } catch (e) {
      if (!mounted) return;
      setState(() => _submitting = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Create failed: $e')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return DraggableScrollableSheet(
      initialChildSize: 0.85,
      minChildSize: 0.5,
      maxChildSize: 0.95,
      expand: false,
      builder: (_, scroll) => Container(
        decoration: const BoxDecoration(
          color: DesignColors.surfaceDark,
          borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
        ),
        padding: EdgeInsets.fromLTRB(
          16,
          12,
          16,
          16 + MediaQuery.of(context).viewInsets.bottom,
        ),
        child: ListView(
          controller: scroll,
          children: [
            Center(
              child: Container(
                width: 36,
                height: 4,
                margin: const EdgeInsets.only(bottom: 12),
                decoration: BoxDecoration(
                  color: DesignColors.borderDark,
                  borderRadius: BorderRadius.circular(2),
                ),
              ),
            ),
            Text(
              'Add plan step',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 18,
                fontWeight: FontWeight.w700,
              ),
            ),
            const SizedBox(height: 16),
            _label('Kind'),
            Wrap(
              spacing: 6,
              runSpacing: 6,
              children: [
                for (final k in _kinds)
                  _KindChip(
                    label: k,
                    selected: _kind == k,
                    onTap: () => setState(() => _kind = k),
                  ),
              ],
            ),
            const SizedBox(height: 16),
            Row(
              children: [
                Expanded(
                  child: _numField(
                    label: 'Phase index',
                    controller: _phase,
                  ),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: _numField(
                    label: 'Step index',
                    controller: _step,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 16),
            _label('Spec (JSON, optional)'),
            TextField(
              controller: _spec,
              enabled: !_submitting,
              minLines: 6,
              maxLines: 18,
              style: GoogleFonts.jetBrainsMono(fontSize: 12, height: 1.4),
              decoration: InputDecoration(
                border: const OutlineInputBorder(),
                isDense: true,
                hintText: _hintForKind(_kind),
                hintStyle: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  color: DesignColors.textMuted,
                ),
                errorText: _specError,
              ),
              onChanged: (_) {
                if (_specError != null) setState(() => _specError = null);
              },
            ),
            const SizedBox(height: 20),
            FilledButton(
              onPressed: _submitting ? null : _submit,
              child: _submitting
                  ? const SizedBox(
                      width: 14,
                      height: 14,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : const Text('Add step'),
            ),
          ],
        ),
      ),
    );
  }

  Widget _numField({
    required String label,
    required TextEditingController controller,
  }) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _label(label),
        TextField(
          controller: controller,
          enabled: !_submitting,
          keyboardType: TextInputType.number,
          inputFormatters: [FilteringTextInputFormatter.digitsOnly],
          style: GoogleFonts.jetBrainsMono(fontSize: 13),
          decoration: const InputDecoration(
            border: OutlineInputBorder(),
            isDense: true,
          ),
        ),
      ],
    );
  }

  Widget _label(String s) => Padding(
        padding: const EdgeInsets.only(bottom: 6),
        child: Text(
          s,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 11,
            fontWeight: FontWeight.w700,
            letterSpacing: 0.5,
            color: DesignColors.textMuted,
          ),
        ),
      );

  String _hintForKind(String kind) => switch (kind) {
        'shell' => '{\n  "cmd": "bash -lc \'echo hi\'"\n}',
        'llm_call' =>
          '{\n  "model": "claude-opus-4-7",\n  "prompt": "..."\n}',
        'agent_spawn' =>
          '{\n  "template": "agents/worker.v1.yaml"\n}',
        'mcp_call' => '{\n  "tool": "search",\n  "args": {}\n}',
        'human_decision' =>
          '{\n  "question": "Ship?",\n  "options": ["yes", "no"]\n}',
        _ => '{}',
      };
}

class _KindChip extends StatelessWidget {
  final String label;
  final bool selected;
  final VoidCallback onTap;
  const _KindChip({
    required this.label,
    required this.selected,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(14),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
        decoration: BoxDecoration(
          color: selected
              ? DesignColors.primary.withValues(alpha: 0.2)
              : Colors.transparent,
          borderRadius: BorderRadius.circular(14),
          border: Border.all(
            color: selected ? DesignColors.primary : DesignColors.borderDark,
          ),
        ),
        child: Text(
          label,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 11,
            fontWeight: selected ? FontWeight.w700 : FontWeight.w500,
            color: selected ? DesignColors.primary : DesignColors.textMuted,
          ),
        ),
      ),
    );
  }
}
