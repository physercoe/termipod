import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';

/// In-place editor for a task's title and body. Status is edited via
/// the chip row on the detail screen, so it's not duplicated here.
///
/// Uses the diff-or-null PATCH pattern: only changed fields are sent.
/// Pops the updated task row on success so the caller can refresh
/// without another round-trip.
class TaskEditSheet extends ConsumerStatefulWidget {
  final String projectId;
  final Map<String, dynamic> task;
  const TaskEditSheet({
    super.key,
    required this.projectId,
    required this.task,
  });

  @override
  ConsumerState<TaskEditSheet> createState() => _TaskEditSheetState();
}

class _TaskEditSheetState extends ConsumerState<TaskEditSheet> {
  late final TextEditingController _title;
  late final TextEditingController _body;
  bool _submitting = false;

  @override
  void initState() {
    super.initState();
    _title = TextEditingController(
        text: (widget.task['title'] ?? '').toString());
    _body = TextEditingController(
        text: (widget.task['body_md'] ?? '').toString());
  }

  @override
  void dispose() {
    _title.dispose();
    _body.dispose();
    super.dispose();
  }

  String? _diffOrNull(String current, String original) {
    if (current == original) return null;
    return current;
  }

  Future<void> _submit() async {
    final taskId = (widget.task['id'] ?? '').toString();
    if (taskId.isEmpty) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;

    final titleChange = _diffOrNull(
        _title.text.trim(), (widget.task['title'] ?? '').toString().trim());
    final bodyChange = _diffOrNull(
        _body.text, (widget.task['body_md'] ?? '').toString());

    if (titleChange == null && bodyChange == null) {
      Navigator.of(context).pop();
      return;
    }
    if (titleChange != null && titleChange.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Title cannot be empty')),
      );
      return;
    }

    setState(() => _submitting = true);
    try {
      final updated = await client.patchTask(
        widget.projectId,
        taskId,
        title: titleChange,
        bodyMd: bodyChange,
      );
      if (!mounted) return;
      Navigator.of(context).pop(updated);
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
              'Edit task',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 18,
                fontWeight: FontWeight.w700,
              ),
            ),
            const SizedBox(height: 16),
            _field(
              label: 'Title',
              controller: _title,
              hint: 'Short task name',
            ),
            _field(
              label: 'Body (markdown)',
              controller: _body,
              hint: 'Details, acceptance criteria, links…',
              maxLines: 12,
              mono: true,
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

  Widget _field({
    required String label,
    required TextEditingController controller,
    String? hint,
    int maxLines = 1,
    bool mono = false,
  }) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 14),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Padding(
            padding: const EdgeInsets.only(bottom: 6),
            child: Text(
              label,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                fontWeight: FontWeight.w700,
                letterSpacing: 0.5,
                color: DesignColors.textMuted,
              ),
            ),
          ),
          TextField(
            controller: controller,
            enabled: !_submitting,
            minLines: 1,
            maxLines: maxLines,
            style: mono
                ? GoogleFonts.jetBrainsMono(fontSize: 13)
                : GoogleFonts.spaceGrotesk(fontSize: 14),
            decoration: InputDecoration(
              border: const OutlineInputBorder(),
              isDense: true,
              hintText: hint,
              hintStyle: GoogleFonts.jetBrainsMono(
                fontSize: 11,
                color: DesignColors.textMuted,
              ),
            ),
          ),
        ],
      ),
    );
  }
}
