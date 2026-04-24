import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:termipod/l10n/app_localizations.dart';

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
  late final TextEditingController _budgetUsd;
  late final int? _originalBudgetCents;
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
    final origCents = (widget.project['budget_cents'] as num?)?.toInt();
    _originalBudgetCents = origCents;
    _budgetUsd = TextEditingController(
      text: origCents == null ? '' : (origCents / 100).toStringAsFixed(2),
    );
  }

  @override
  void dispose() {
    _name.dispose();
    _goal.dispose();
    _template.dispose();
    _onCreate.dispose();
    _docsRoot.dispose();
    _budgetUsd.dispose();
    super.dispose();
  }

  String? _diffOrNull(String current, String original) {
    final c = current.trim();
    if (c == original.trim()) return null;
    return c;
  }

  /// Parses the USD budget field and returns the cents delta, or null if
  /// unchanged. A blank field means "keep current" — clearing a budget
  /// isn't supported from this sheet because updateProject omits null
  /// values rather than sending SQL NULL.
  int? _budgetCentsChange() {
    final raw = _budgetUsd.text.trim();
    if (raw.isEmpty) return null;
    final dollars = double.tryParse(raw);
    if (dollars == null) return null;
    final cents = (dollars * 100).round();
    if (cents == _originalBudgetCents) return null;
    return cents;
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
    final budgetChange = _budgetCentsChange();

    // Catch parse-errors early so invalid budget input doesn't silently
    // no-op after the user hit Save.
    final budgetRaw = _budgetUsd.text.trim();
    if (budgetRaw.isNotEmpty && double.tryParse(budgetRaw) == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Budget must be a dollar amount')),
      );
      return;
    }

    final anyChange = [
      nameChange,
      goalChange,
      templateChange,
      onCreateChange,
      docsRootChange,
    ].any((v) => v != null) || budgetChange != null;
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
        budgetCents: budgetChange,
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
            Builder(builder: (ctx) {
              final l10n = AppLocalizations.of(ctx)!;
              final isWorkspace =
                  (widget.project['kind'] ?? 'goal').toString() == 'standing';
              return Text(
                isWorkspace ? l10n.workspaceEditTitle : l10n.projectEditTitle,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 18,
                  fontWeight: FontWeight.w700,
                ),
              );
            }),
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
            _field(
              label: 'Budget (USD)',
              controller: _budgetUsd,
              hint: 'e.g. 25.00 — leave blank to keep current',
              mono: true,
              keyboard: const TextInputType.numberWithOptions(decimal: true),
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
    TextInputType? keyboard,
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
            keyboardType: keyboard,
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
