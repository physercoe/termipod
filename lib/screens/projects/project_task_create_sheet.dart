import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../l10n/app_localizations.dart';
import '../../providers/hub_provider.dart';
import '../../providers/vocab_provider.dart';
import '../../services/vocab/vocab_axis.dart';
import '../../theme/task_priority_style.dart';
import '../../theme/tokens.dart';
import '../../theme/design_colors.dart';

/// Bottom sheet for creating a task. Pops `true` on success so the caller
/// reloads the task list.
class ProjectTaskCreateSheet extends ConsumerStatefulWidget {
  final String projectId;
  const ProjectTaskCreateSheet({super.key, required this.projectId});

  @override
  ConsumerState<ProjectTaskCreateSheet> createState() =>
      _ProjectTaskCreateSheetState();
}

class _ProjectTaskCreateSheetState
    extends ConsumerState<ProjectTaskCreateSheet> {
  final _title = TextEditingController();
  final _body = TextEditingController();
  String _status = 'todo';
  TaskPriority _priority = TaskPriority.med;
  bool _busy = false;
  String? _error;

  static const _statuses = ['todo', 'in_progress', 'blocked', 'done'];

  @override
  void dispose() {
    _title.dispose();
    _body.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    if (_title.text.trim().isEmpty) {
      setState(() => _error = AppLocalizations.of(context)!.titleRequired);
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await client.createTask(
        widget.projectId,
        title: _title.text.trim(),
        bodyMd: _body.text.trim().isEmpty ? null : _body.text.trim(),
        status: _status,
        priority: _priority.wire,
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
    final taskTerm = ref.watch(vocabularyProvider).term(VocabAxis.entityTask);
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
              Text(l10n.newTask(taskTerm.lower),
                  style: GoogleFonts.spaceGrotesk(
                      fontSize: 18, fontWeight: FontWeight.w700)),
              const SizedBox(height: 16),
              TextField(
                controller: _title,
                autofocus: true,
                decoration: InputDecoration(
                  labelText: l10n.fieldTitle,
                  border: const OutlineInputBorder(),
                ),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _body,
                maxLines: 4,
                decoration: InputDecoration(
                  labelText: l10n.fieldBodyOptional,
                  border: const OutlineInputBorder(),
                ),
              ),
              const SizedBox(height: 12),
              DropdownButtonFormField<String>(
                value: _status,
                decoration: InputDecoration(
                  labelText: l10n.fieldStatus,
                  border: const OutlineInputBorder(),
                ),
                items: [
                  for (final s in _statuses)
                    DropdownMenuItem(
                        value: s, child: Text(taskStatusLabel(l10n, s))),
                ],
                onChanged: (v) => setState(() => _status = v ?? 'todo'),
              ),
              const SizedBox(height: 12),
              DropdownButtonFormField<TaskPriority>(
                value: _priority,
                decoration: InputDecoration(
                  labelText: l10n.fieldPriority,
                  border: const OutlineInputBorder(),
                ),
                items: [
                  for (final p in TaskPriority.values)
                    DropdownMenuItem(
                      value: p,
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Container(
                            width: 10,
                            height: 10,
                            decoration: BoxDecoration(
                              color: taskPriorityColor(p),
                              shape: BoxShape.circle,
                            ),
                          ),
                          const SizedBox(width: 8),
                          Text(p.localizedLabel(l10n)),
                        ],
                      ),
                    ),
                ],
                onChanged: (v) =>
                    setState(() => _priority = v ?? TaskPriority.med),
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
