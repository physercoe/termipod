import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../l10n/app_localizations.dart';
import '../../providers/hub_provider.dart';
import '../../providers/vocab_provider.dart';
import '../../services/vocab/vocab_axis.dart';
import '../../theme/tokens.dart';
import '../../theme/design_colors.dart';

/// Localized label for a schedule trigger wire value
/// (`cron` / `manual` / `on_create`). Unknown values fall back to the raw
/// wire string. Shared with the edit sheet and the schedules list.
String scheduleTriggerLabel(AppLocalizations l10n, String kind) {
  switch (kind) {
    case 'cron':
      return l10n.triggerCron;
    case 'manual':
      return l10n.triggerManual;
    case 'on_create':
      return l10n.triggerOnCreate;
    default:
      return kind;
  }
}

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
      final l10n = AppLocalizations.of(context)!;
      final projects =
          ref.read(vocabularyProvider).term(VocabAxis.entityProject).pluralLower;
      setState(() {
        _loadingProjects = false;
        _error = l10n.loadProjectsError(projects, '$e');
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
    final l10n = AppLocalizations.of(context)!;
    if (_projectId == null || _projectId!.isEmpty) {
      final project =
          ref.read(vocabularyProvider).term(VocabAxis.entityProject).title;
      setState(() => _error = l10n.projectRequired(project));
      return;
    }
    if (_template.text.trim().isEmpty) {
      setState(() => _error = l10n.templateIdRequired);
      return;
    }
    if (_triggerKind == 'cron' && _cron.text.trim().isEmpty) {
      setState(() => _error = l10n.cronExprRequired);
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
        setState(() => _error = l10n.paramsMustBeJsonObjectError('$e'));
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
    final l10n = AppLocalizations.of(context)!;
    final voc = ref.watch(vocabularyProvider);
    final scheduleTerm = voc.term(VocabAxis.entitySchedule);
    final projectTerm = voc.term(VocabAxis.entityProject);
    final insets = MediaQuery.of(context).viewInsets;
    return Padding(
      padding: EdgeInsets.only(bottom: insets.bottom),
      child: SafeArea(
        top: false,
        child: Padding(
          padding: const EdgeInsets.all(Spacing.s16),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Text(
                  widget.initial == null
                      ? l10n.newSchedule(scheduleTerm.lower)
                      : l10n.duplicateSchedule(scheduleTerm.lower),
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
                  decoration: InputDecoration(
                    labelText: projectTerm.title,
                    border: const OutlineInputBorder(),
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
                decoration: InputDecoration(
                  labelText: l10n.fieldTemplateId,
                  hintText: 'agents/steward.v1.yaml',
                  border: const OutlineInputBorder(),
                ),
                style: GoogleFonts.jetBrainsMono(fontSize: 13),
              ),
              const SizedBox(height: 12),
              DropdownButtonFormField<String>(
                value: _triggerKind,
                decoration: InputDecoration(
                  labelText: l10n.fieldTrigger,
                  border: const OutlineInputBorder(),
                ),
                items: [
                  for (final t in _triggers)
                    DropdownMenuItem(
                        value: t, child: Text(scheduleTriggerLabel(l10n, t))),
                ],
                onChanged: (v) => setState(() => _triggerKind = v ?? 'cron'),
              ),
              const SizedBox(height: 12),
              if (_triggerKind == 'cron')
                TextField(
                  controller: _cron,
                  style: GoogleFonts.jetBrainsMono(fontSize: 13),
                  decoration: InputDecoration(
                    labelText: l10n.fieldCronExpr,
                    hintText: '0 9 * * *',
                    border: const OutlineInputBorder(),
                  ),
                ),
              if (_triggerKind == 'cron') const SizedBox(height: 12),
              TextField(
                controller: _params,
                maxLines: 4,
                style: GoogleFonts.jetBrainsMono(fontSize: 12),
                decoration: InputDecoration(
                  labelText: l10n.fieldParamsJsonOptional,
                  hintText: '{"key": "value"}',
                  border: const OutlineInputBorder(),
                ),
              ),
              if (_error != null) ...[
                const SizedBox(height: 12),
                Text(_error!,
                    style: GoogleFonts.jetBrainsMono(
                        fontSize: 12, color: DesignColors.error)),
              ],
              const SizedBox(height: 20),
              Row(
                children: [
                  TextButton(
                    onPressed:
                        _busy ? null : () => Navigator.of(context).pop(),
                    child: Text(l10n.buttonCancel),
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
                    label: Text(l10n.buttonCreate),
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
