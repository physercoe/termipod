import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';

/// In-place editor for an existing schedule. patchSchedule accepts
/// `cron_expr`, `parameters_json`, and `enabled`; template_id, project_id,
/// and trigger_kind are frozen at create time — the server rejects
/// attempts to flip them, so we don't expose them here.
///
/// Returns `true` if a write succeeded so the caller can refetch.
class ScheduleEditSheet extends ConsumerStatefulWidget {
  final Map<String, dynamic> schedule;
  const ScheduleEditSheet({super.key, required this.schedule});

  @override
  ConsumerState<ScheduleEditSheet> createState() => _ScheduleEditSheetState();
}

class _ScheduleEditSheetState extends ConsumerState<ScheduleEditSheet> {
  late final TextEditingController _cron;
  late final TextEditingController _params;
  bool _submitting = false;
  String? _paramsError;

  String get _triggerKind =>
      (widget.schedule['trigger_kind'] ?? 'cron').toString();

  @override
  void initState() {
    super.initState();
    _cron = TextEditingController(
        text: (widget.schedule['cron_expr'] ?? '').toString());
    _params = TextEditingController(text: _formatParams());
  }

  @override
  void dispose() {
    _cron.dispose();
    _params.dispose();
    super.dispose();
  }

  String _formatParams() {
    final raw = widget.schedule['parameters_json'];
    if (raw is Map) {
      if (raw.isEmpty) return '';
      try {
        return const JsonEncoder.withIndent('  ').convert(raw);
      } catch (_) {}
    }
    if (raw is String && raw.trim().isNotEmpty) {
      try {
        final decoded = jsonDecode(raw);
        return const JsonEncoder.withIndent('  ').convert(decoded);
      } catch (_) {
        return raw;
      }
    }
    return '';
  }

  String? _diffString(String current, String original) {
    final c = current.trim();
    if (c == original.trim()) return null;
    return c;
  }

  Future<void> _submit() async {
    final scheduleId = (widget.schedule['id'] ?? '').toString();
    if (scheduleId.isEmpty) return;

    final cronOriginal = (widget.schedule['cron_expr'] ?? '').toString();
    final cronChange =
        _triggerKind == 'cron' ? _diffString(_cron.text, cronOriginal) : null;

    Map<String, dynamic>? paramsChange;
    final paramsText = _params.text.trim();
    final paramsOriginal = _formatParams().trim();
    if (paramsText != paramsOriginal) {
      if (paramsText.isEmpty) {
        paramsChange = const {};
      } else {
        try {
          final decoded = jsonDecode(paramsText);
          if (decoded is! Map) {
            setState(() => _paramsError = 'Parameters must be a JSON object.');
            return;
          }
          paramsChange = decoded.cast<String, dynamic>();
        } catch (e) {
          setState(() => _paramsError = 'Invalid JSON: $e');
          return;
        }
      }
    }

    if (cronChange == null && paramsChange == null) {
      Navigator.of(context).pop(false);
      return;
    }

    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() {
      _submitting = true;
      _paramsError = null;
    });
    try {
      await client.patchSchedule(
        scheduleId,
        cronExpr: cronChange,
        parameters: paramsChange,
      );
      if (!mounted) return;
      Navigator.of(context).pop(true);
    } catch (e) {
      if (!mounted) return;
      setState(() => _submitting = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Update failed: $e')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final template = (widget.schedule['template_id'] ?? '').toString();
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
              'Edit schedule',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 18,
                fontWeight: FontWeight.w700,
              ),
            ),
            const SizedBox(height: 4),
            Text(
              'Template, project, and trigger kind are frozen after '
              'create. Duplicate the schedule if you need to change those.',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                color: DesignColors.textMuted,
              ),
            ),
            const SizedBox(height: 16),
            _label('Template'),
            InputDecorator(
              decoration: const InputDecoration(
                border: OutlineInputBorder(),
                isDense: true,
              ),
              child: Text(
                template.isEmpty ? '(unknown)' : template,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 13,
                  color: DesignColors.textMuted,
                ),
              ),
            ),
            const SizedBox(height: 16),
            _label('Trigger'),
            InputDecorator(
              decoration: const InputDecoration(
                border: OutlineInputBorder(),
                isDense: true,
              ),
              child: Text(
                _triggerKind,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 13,
                  color: DesignColors.textMuted,
                ),
              ),
            ),
            if (_triggerKind == 'cron') ...[
              const SizedBox(height: 16),
              _label('Cron expression'),
              TextField(
                controller: _cron,
                enabled: !_submitting,
                style: GoogleFonts.jetBrainsMono(fontSize: 13),
                decoration: const InputDecoration(
                  border: OutlineInputBorder(),
                  isDense: true,
                  hintText: '0 */6 * * *',
                ),
              ),
            ],
            const SizedBox(height: 16),
            _label('Parameters (JSON)'),
            TextField(
              controller: _params,
              enabled: !_submitting,
              minLines: 6,
              maxLines: 16,
              style: GoogleFonts.jetBrainsMono(fontSize: 12, height: 1.4),
              decoration: InputDecoration(
                border: const OutlineInputBorder(),
                isDense: true,
                hintText: '{\n  "target": "main"\n}',
                hintStyle: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  color: DesignColors.textMuted,
                ),
                errorText: _paramsError,
              ),
              onChanged: (_) {
                if (_paramsError != null) {
                  setState(() => _paramsError = null);
                }
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
                  : const Text('Save changes'),
            ),
          ],
        ),
      ),
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
}
