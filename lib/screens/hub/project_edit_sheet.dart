import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';

/// In-place editor for a project's mutable fields. The blueprint's
/// `kind` is deliberately excluded — flipping a standing project to a
/// goal project (or vice versa) changes the lifecycle semantics of the
/// whole thing, so we don't expose that here.
///
/// On success the sheet pops the updated project row so the caller can
/// refresh its display without another round-trip.
class ProjectEditSheet extends ConsumerStatefulWidget {
  final Map<String, dynamic> project;
  const ProjectEditSheet({super.key, required this.project});

  @override
  ConsumerState<ProjectEditSheet> createState() => _ProjectEditSheetState();
}

class _ProjectEditSheetState extends ConsumerState<ProjectEditSheet> {
  late final TextEditingController _name;
  late final TextEditingController _goal;
  late final TextEditingController _template;
  late final TextEditingController _onCreate;
  late final TextEditingController _docsRoot;
  bool _submitting = false;

  @override
  void initState() {
    super.initState();
    _name = TextEditingController(
        text: (widget.project['name'] ?? '').toString());
    _goal = TextEditingController(
        text: (widget.project['goal'] ?? '').toString());
    _template = TextEditingController(
        text: (widget.project['template_id'] ?? '').toString());
    _onCreate = TextEditingController(
        text: (widget.project['on_create_template_id'] ?? '').toString());
    _docsRoot = TextEditingController(
        text: (widget.project['docs_root'] ?? '').toString());
  }

  @override
  void dispose() {
    _name.dispose();
    _goal.dispose();
    _template.dispose();
    _onCreate.dispose();
    _docsRoot.dispose();
    super.dispose();
  }

  String? _diffOrNull(String current, String original) {
    final c = current.trim();
    if (c == original.trim()) return null;
    return c;
  }

  Future<void> _submit() async {
    final projectId = (widget.project['id'] ?? '').toString();
    if (projectId.isEmpty) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;

    final nameChange =
        _diffOrNull(_name.text, (widget.project['name'] ?? '').toString());
    final goalChange =
        _diffOrNull(_goal.text, (widget.project['goal'] ?? '').toString());
    final templateChange = _diffOrNull(
        _template.text, (widget.project['template_id'] ?? '').toString());
    final onCreateChange = _diffOrNull(_onCreate.text,
        (widget.project['on_create_template_id'] ?? '').toString());
    final docsRootChange = _diffOrNull(
        _docsRoot.text, (widget.project['docs_root'] ?? '').toString());

    final anyChange = [
      nameChange,
      goalChange,
      templateChange,
      onCreateChange,
      docsRootChange,
    ].any((v) => v != null);
    if (!anyChange) {
      Navigator.of(context).pop();
      return;
    }

    setState(() => _submitting = true);
    try {
      final updated = await client.updateProject(
        projectId,
        name: nameChange,
        goal: goalChange,
        templateId: templateChange,
        onCreateTemplateId: onCreateChange,
        docsRoot: docsRootChange,
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
              'Edit project',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 18,
                fontWeight: FontWeight.w700,
              ),
            ),
            const SizedBox(height: 16),
            _field(
              label: 'Name',
              controller: _name,
              hint: 'Project name',
            ),
            _field(
              label: 'Goal',
              controller: _goal,
              hint: 'What this project aims to achieve',
              maxLines: 4,
            ),
            _field(
              label: 'Steward template',
              controller: _template,
              hint: 'e.g. agents/steward.v1.yaml',
              mono: true,
            ),
            _field(
              label: 'On-create template',
              controller: _onCreate,
              hint: 'e.g. prompts/onboarding.md',
              mono: true,
            ),
            _field(
              label: 'Docs root',
              controller: _docsRoot,
              hint: 'relative path under the hub dataRoot',
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
