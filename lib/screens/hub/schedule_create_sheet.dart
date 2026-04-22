import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';

/// Bottom sheet for creating a schedule (blueprint §6.3). A schedule binds
/// a project to a template with a trigger. Cron schedules require cron_expr;
/// manual and on_create schedules don't. Pops `true` on success.
class ScheduleCreateSheet extends ConsumerStatefulWidget {
  /// Pre-filled values for a duplicate flow. Keys match the hub row
  /// shape returned by `listSchedules`: project_id, template_id,
  /// trigger_kind, cron_expr, parameters (Map or JSON string).
  final Map<String, dynamic>? initial;
  const ScheduleCreateSheet({super.key, this.initial});

  @override
  ConsumerState<ScheduleCreateSheet> createState() =>
      _ScheduleCreateSheetState();
}

class _ScheduleCreateSheetState extends ConsumerState<ScheduleCreateSheet> {
  final _cron = TextEditingController();
  final _template = TextEditingController();
  final _params = TextEditingController();
  String _triggerKind = 'cron';
  String? _projectId;
  List<Map<String, dynamic>> _projects = const [];
  bool _loadingProjects = true;
  bool _busy = false;
  String? _error;

  static const _triggers = ['cron', 'manual', 'on_create'];

  @override
  void initState() {
    super.initState();
    final init = widget.initial;
    if (init != null) {
      _template.text = (init['template_id'] ?? '').toString();
      _cron.text = (init['cron_expr'] ?? '').toString();
      final trig = (init['trigger_kind'] ?? '').toString();
      if (_triggers.contains(trig)) _triggerKind = trig;
      final params = init['parameters'];
      if (params is Map) {
        _params.text = const JsonEncoder.withIndent('  ').convert(params);
      } else if (params is String && params.isNotEmpty) {
        _params.text = params;
      }
    }
    _loadProjects();
  }

  Future<void> _loadProjects() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final rows = await client.listProjects();
      if (!mounted) return;
      final preferred = (widget.initial?['project_id'] ?? '').toString();
      setState(() {
        _projects = rows;
        final ids = rows.map((r) => (r['id'] ?? '').toString()).toList();
        _projectId = preferred.isNotEmpty && ids.contains(preferred)
            ? preferred
            : (rows.isNotEmpty ? ids.first : null);
        _loadingProjects = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loadingProjects = false;
        _error = 'Load projects: $e';
      });
    }
  }

  @override
  void dispose() {
    _cron.dispose();
    _template.dispose();
    _params.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    if (_projectId == null || _projectId!.isEmpty) {
      setState(() => _error = 'Project required');
      return;
    }
    if (_template.text.trim().isEmpty) {
      setState(() => _error = 'Template id required');
      return;
    }
    if (_triggerKind == 'cron' && _cron.text.trim().isEmpty) {
      setState(() => _error = 'Cron expression required for cron trigger');
      return;
    }
    Map<String, dynamic>? parameters;
    final paramsText = _params.text.trim();
    if (paramsText.isNotEmpty) {
      try {
        final decoded = jsonDecode(paramsText);
        if (decoded is! Map) throw const FormatException('not an object');
        parameters = decoded.cast<String, dynamic>();
      } catch (e) {
        setState(() => _error = 'Parameters must be JSON object: $e');
        return;
      }
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await client.createSchedule(
        projectId: _projectId!,
        templateId: _template.text.trim(),
        triggerKind: _triggerKind,
        cronExpr: _triggerKind == 'cron' ? _cron.text.trim() : null,
        parameters: parameters,
      );
      if (mounted) Navigator.of(context).pop(true);
    } catch (e) {
      setState(() {
        _busy = false;
        _error = '$e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final insets = MediaQuery.of(context).viewInsets;
    return Padding(
      padding: EdgeInsets.only(bottom: insets.bottom),
      child: SafeArea(
        top: false,
        child: Padding(
          padding: const EdgeInsets.all(20),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Text(widget.initial == null ? 'New schedule' : 'Duplicate schedule',
                  style: GoogleFonts.spaceGrotesk(
                      fontSize: 18, fontWeight: FontWeight.w700)),
              const SizedBox(height: 16),
              if (_loadingProjects)
                const Padding(
                  padding: EdgeInsets.symmetric(vertical: 8),
                  child: LinearProgressIndicator(),
                )
              else
                DropdownButtonFormField<String>(
                  value: _projectId,
                  decoration: const InputDecoration(
                    labelText: 'Project',
                    border: OutlineInputBorder(),
                  ),
                  items: [
                    for (final p in _projects)
                      DropdownMenuItem(
                        value: (p['id'] ?? '').toString(),
                        child: Text((p['name'] ?? p['id'] ?? '').toString()),
                      ),
                  ],
                  onChanged: (v) => setState(() => _projectId = v),
                ),
              const SizedBox(height: 12),
              TextField(
                controller: _template,
                autofocus: true,
                decoration: const InputDecoration(
                  labelText: 'Template id',
                  hintText: 'agents/steward.v1.yaml',
                  border: OutlineInputBorder(),
                ),
                style: GoogleFonts.jetBrainsMono(fontSize: 13),
              ),
              const SizedBox(height: 12),
              DropdownButtonFormField<String>(
                value: _triggerKind,
                decoration: const InputDecoration(
                  labelText: 'Trigger',
                  border: OutlineInputBorder(),
                ),
                items: [
                  for (final t in _triggers)
                    DropdownMenuItem(value: t, child: Text(t)),
                ],
                onChanged: (v) => setState(() => _triggerKind = v ?? 'cron'),
              ),
              const SizedBox(height: 12),
              if (_triggerKind == 'cron')
                TextField(
                  controller: _cron,
                  style: GoogleFonts.jetBrainsMono(fontSize: 13),
                  decoration: const InputDecoration(
                    labelText: 'Cron expression',
                    hintText: '0 9 * * *',
                    border: OutlineInputBorder(),
                  ),
                ),
              if (_triggerKind == 'cron') const SizedBox(height: 12),
              TextField(
                controller: _params,
                maxLines: 4,
                style: GoogleFonts.jetBrainsMono(fontSize: 12),
                decoration: const InputDecoration(
                  labelText: 'Parameters (JSON, optional)',
                  hintText: '{"key": "value"}',
                  border: OutlineInputBorder(),
                ),
              ),
              if (_error != null) ...[
                const SizedBox(height: 12),
                Text(_error!,
                    style: GoogleFonts.jetBrainsMono(
                        fontSize: 12, color: Colors.red)),
              ],
              const SizedBox(height: 20),
              Row(
                children: [
                  TextButton(
                    onPressed:
                        _busy ? null : () => Navigator.of(context).pop(),
                    child: const Text('Cancel'),
                  ),
                  const Spacer(),
                  FilledButton.icon(
                    onPressed: _busy ? null : _submit,
                    icon: _busy
                        ? const SizedBox(
                            width: 14,
                            height: 14,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : const Icon(Icons.check, size: 16),
                    label: const Text('Create'),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }
}
