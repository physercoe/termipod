import 'package:flutter/material.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
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
  bool _preview = false;

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
            _bodyField(),
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

  Widget _bodyField() {
    // Edit ↔ preview toggle keeps the body editor honest — authors can
    // check that their headings, lists, and code fences render before
    // saving without leaving the sheet.
    return Padding(
      padding: const EdgeInsets.only(bottom: 14),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Padding(
            padding: const EdgeInsets.only(bottom: 6),
            child: Row(
              children: [
                Text(
                  'Body (markdown)',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 11,
                    fontWeight: FontWeight.w700,
                    letterSpacing: 0.5,
                    color: DesignColors.textMuted,
                  ),
                ),
                const Spacer(),
                _ModeSegment(
                  preview: _preview,
                  onChanged: (v) => setState(() => _preview = v),
                ),
              ],
            ),
          ),
          if (_preview)
            Container(
              constraints: const BoxConstraints(minHeight: 120),
              padding: const EdgeInsets.all(10),
              decoration: BoxDecoration(
                color: Colors.black.withValues(alpha: 0.25),
                borderRadius: BorderRadius.circular(6),
                border: Border.all(color: DesignColors.borderDark),
              ),
              child: _body.text.trim().isEmpty
                  ? Text(
                      '(empty)',
                      style: GoogleFonts.spaceGrotesk(
                        fontSize: 13,
                        color: DesignColors.textMuted,
                      ),
                    )
                  : MarkdownBody(
                      data: _body.text,
                      selectable: true,
                      styleSheet: MarkdownStyleSheet(
                        p: GoogleFonts.spaceGrotesk(
                            fontSize: 13, height: 1.4),
                        code: GoogleFonts.jetBrainsMono(fontSize: 12),
                        h1: GoogleFonts.spaceGrotesk(
                            fontSize: 18, fontWeight: FontWeight.w700),
                        h2: GoogleFonts.spaceGrotesk(
                            fontSize: 16, fontWeight: FontWeight.w700),
                        h3: GoogleFonts.spaceGrotesk(
                            fontSize: 14, fontWeight: FontWeight.w700),
                      ),
                    ),
            )
          else
            TextField(
              controller: _body,
              enabled: !_submitting,
              minLines: 6,
              maxLines: 14,
              style: GoogleFonts.jetBrainsMono(fontSize: 13),
              decoration: InputDecoration(
                border: const OutlineInputBorder(),
                isDense: true,
                hintText: 'Details, acceptance criteria, links…',
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

class _ModeSegment extends StatelessWidget {
  final bool preview;
  final ValueChanged<bool> onChanged;
  const _ModeSegment({required this.preview, required this.onChanged});

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        border: Border.all(color: DesignColors.borderDark),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          _seg('Edit', !preview, () => onChanged(false)),
          Container(width: 1, height: 22, color: DesignColors.borderDark),
          _seg('Preview', preview, () => onChanged(true)),
        ],
      ),
    );
  }

  Widget _seg(String label, bool selected, VoidCallback onTap) {
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
        child: Text(
          label,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 10,
            fontWeight: selected ? FontWeight.w700 : FontWeight.w500,
            color:
                selected ? DesignColors.primary : DesignColors.textMuted,
          ),
        ),
      ),
    );
  }
}
