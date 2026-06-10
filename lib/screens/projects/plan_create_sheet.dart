import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../l10n/app_localizations.dart';
import '../../providers/hub_provider.dart';
import '../../providers/vocab_provider.dart';
import '../../services/template_filter.dart';
import '../../services/vocab/vocab_axis.dart';
import '../../theme/design_colors.dart';
import '../../theme/tokens.dart';
import '../team/template_icon.dart';

/// Draft-plan launcher. Picks a project and (optional) template and POSTs
/// `/v1/teams/{team}/plans`. Returns the new plan id on success so the
/// caller can jump straight into the viewer.
///
/// The sheet deliberately doesn't let the user hand-author phase/step
/// spec_json — on a phone that'd be painful, and the blueprint's
/// steward-expansion flow is the intended path: pick a template, the
/// steward fills in phases and steps.
class PlanCreateSheet extends ConsumerStatefulWidget {
  const PlanCreateSheet({super.key});

  @override
  ConsumerState<PlanCreateSheet> createState() => _PlanCreateSheetState();
}

class _PlanCreateSheetState extends ConsumerState<PlanCreateSheet> {
  List<Map<String, dynamic>>? _projects;
  List<Map<String, dynamic>>? _templates;
  String? _loadError;
  // client==null branch of _load() runs synchronously during initState;
  // render hubNotConfigured at build, not here (would throw).
  bool _hubMissing = false;
  bool _loading = true;

  String? _projectId;
  String? _templateId;
  bool _submitting = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() {
        _hubMissing = true;
        _loading = false;
      });
      return;
    }
    try {
      final projects = await client.listProjects();
      final templates = await client.listTemplates();
      if (!mounted) return;
      setState(() {
        _projects = projects;
        _templates = templates;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loadError = '$e';
        _loading = false;
      });
    }
  }

  Future<void> _submit() async {
    final projectId = _projectId;
    if (projectId == null || projectId.isEmpty) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() => _submitting = true);
    try {
      final plan = await client.createPlan(
        projectId: projectId,
        templateId: (_templateId == null || _templateId!.isEmpty)
            ? null
            : _templateId,
      );
      if (!mounted) return;
      Navigator.of(context).pop(plan);
    } catch (e) {
      if (!mounted) return;
      setState(() => _submitting = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
            content: Text(AppLocalizations.of(context)!.createFailedError('$e'))),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return DraggableScrollableSheet(
      initialChildSize: 0.6,
      minChildSize: 0.4,
      maxChildSize: 0.9,
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
        child: _body(scroll),
      ),
    );
  }

  Widget _body(ScrollController scroll) {
    final l10n = AppLocalizations.of(context)!;
    final voc = ref.watch(vocabularyProvider);
    final planTerm = voc.term(VocabAxis.entityPlan);
    final projectTerm = voc.term(VocabAxis.entityProject);
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_hubMissing || _loadError != null) {
      return Center(
        child: Text(
          _hubMissing ? l10n.hubNotConfigured : _loadError!,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 12,
            color: DesignColors.error,
          ),
        ),
      );
    }
    final projects = _projects ?? const [];
    final templates = _templates ?? const [];
    // Agent-kind templates are the ones that expand into phased plans.
    // Prompts and policies aren't plan scaffolds, so filter them out.
    // Then narrow by the selected project's template_id so the picker
    // only shows agent templates relevant to that project's domain.
    String? projectTemplateId;
    if (_projectId != null && _projectId!.isNotEmpty) {
      for (final p in projects) {
        if ((p['id'] ?? '').toString() == _projectId) {
          projectTemplateId = (p['template_id'] ?? '').toString();
          break;
        }
      }
    }
    final agentTemplates = filterTemplatesForProject(
      templates
          .where((t) => (t['category'] ?? '').toString() == 'agents')
          .toList(),
      projectTemplateId,
    );

    return ListView(
      controller: scroll,
      children: [
        Center(
          child: Container(
            width: 36,
            height: 4,
            margin: const EdgeInsets.only(bottom: 12),
            decoration: BoxDecoration(
              color: DesignColors.borderDark,
              borderRadius: Radii.xsBorder,
            ),
          ),
        ),
        Text(
          l10n.startPlan(planTerm.lower),
          style: GoogleFonts.spaceGrotesk(
            fontSize: 18,
            fontWeight: FontWeight.w700,
          ),
        ),
        const SizedBox(height: 4),
        Text(
          l10n.planCreateNote(
            planTerm.lower,
            projectTerm.lower,
            voc.term(VocabAxis.roleSteward).lower,
          ),
          style: GoogleFonts.spaceGrotesk(
            fontSize: 12,
            color: DesignColors.textMuted,
          ),
        ),
        const SizedBox(height: 16),
        _label(projectTerm.title),
        _ProjectField(
          projects: projects,
          selectedId: _projectId,
          pickHint: l10n.pickProjectHint(projectTerm.lower),
          onChanged: (id) => setState(() => _projectId = id),
        ),
        const SizedBox(height: 16),
        _label(l10n.fieldTemplateOptional),
        _TemplateField(
          templates: agentTemplates,
          selectedPath: _templateId,
          blankHint: l10n.noTemplateBlankDraft,
          blankItemLabel: l10n.noTemplateBlankDraftItem,
          onChanged: (id) => setState(() => _templateId = id),
        ),
        const SizedBox(height: 24),
        FilledButton(
          onPressed:
              _submitting || _projectId == null || _projectId!.isEmpty
                  ? null
                  : _submit,
          child: _submitting
              ? const SizedBox(
                  width: 14,
                  height: 14,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : Text(l10n.createDraftPlan(planTerm.lower)),
        ),
      ],
    );
  }

  Widget _label(String s) => Padding(
        padding: const EdgeInsets.only(bottom: Spacing.s8),
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

class _ProjectField extends StatelessWidget {
  final List<Map<String, dynamic>> projects;
  final String? selectedId;
  final String pickHint;
  final ValueChanged<String?> onChanged;
  const _ProjectField({
    required this.projects,
    required this.selectedId,
    required this.pickHint,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: () async {
        final picked = await showModalBottomSheet<String?>(
          context: context,
          isScrollControlled: true,
          backgroundColor: Colors.transparent,
          builder: (_) => _ProjectPickerSheet(projects: projects),
        );
        if (picked != null) onChanged(picked);
      },
      child: InputDecorator(
        decoration: const InputDecoration(
          border: OutlineInputBorder(),
          isDense: true,
          suffixIcon: Icon(Icons.arrow_drop_down),
        ),
        child: Text(
          _label(projects),
          style: GoogleFonts.jetBrainsMono(
            fontSize: 13,
            color: selectedId == null ? DesignColors.textMuted : null,
          ),
        ),
      ),
    );
  }

  String _label(List<Map<String, dynamic>> projects) {
    if (selectedId == null) return pickHint;
    for (final p in projects) {
      if ((p['id'] ?? '').toString() == selectedId) {
        return (p['name'] ?? selectedId).toString();
      }
    }
    return selectedId!;
  }
}

class _ProjectPickerSheet extends StatelessWidget {
  final List<Map<String, dynamic>> projects;
  const _ProjectPickerSheet({required this.projects});

  @override
  Widget build(BuildContext context) {
    return DraggableScrollableSheet(
      initialChildSize: 0.6,
      minChildSize: 0.3,
      maxChildSize: 0.9,
      expand: false,
      builder: (_, scroll) => Container(
        decoration: const BoxDecoration(
          color: DesignColors.surfaceDark,
          borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
        ),
        padding: const EdgeInsets.fromLTRB(8, 12, 8, 16),
        child: ListView.separated(
          controller: scroll,
          itemCount: projects.length,
          separatorBuilder: (_, __) => const Divider(height: 1),
          itemBuilder: (_, i) {
            final p = projects[i];
            final id = (p['id'] ?? '').toString();
            final name = (p['name'] ?? id).toString();
            final kind = (p['kind'] ?? '').toString();
            return ListTile(
              title: Text(
                name,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 14,
                  fontWeight: FontWeight.w600,
                ),
              ),
              subtitle: Text(
                [if (kind.isNotEmpty) kind, id].join(' · '),
                style: GoogleFonts.jetBrainsMono(
                  fontSize: FontSizes.label,
                  color: DesignColors.textMuted,
                ),
              ),
              onTap: () => Navigator.of(context).pop(id),
            );
          },
        ),
      ),
    );
  }
}

class _TemplateField extends StatelessWidget {
  final List<Map<String, dynamic>> templates;
  final String? selectedPath;
  final String blankHint;
  final String blankItemLabel;
  final ValueChanged<String?> onChanged;
  const _TemplateField({
    required this.templates,
    required this.selectedPath,
    required this.blankHint,
    required this.blankItemLabel,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: () async {
        final picked = await showModalBottomSheet<String?>(
          context: context,
          isScrollControlled: true,
          backgroundColor: Colors.transparent,
          builder: (_) =>
              _TemplatePickerSheet(templates: templates, blankLabel: blankItemLabel),
        );
        if (picked != null) onChanged(picked.isEmpty ? null : picked);
      },
      child: InputDecorator(
        decoration: InputDecoration(
          border: const OutlineInputBorder(),
          isDense: true,
          suffixIcon: selectedPath != null
              ? IconButton(
                  icon: const Icon(Icons.clear),
                  onPressed: () => onChanged(null),
                )
              : const Icon(Icons.arrow_drop_down),
        ),
        child: Text(
          selectedPath ?? blankHint,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 13,
            color: selectedPath == null ? DesignColors.textMuted : null,
          ),
        ),
      ),
    );
  }
}

class _TemplatePickerSheet extends StatelessWidget {
  final List<Map<String, dynamic>> templates;
  final String blankLabel;
  const _TemplatePickerSheet({
    required this.templates,
    required this.blankLabel,
  });

  @override
  Widget build(BuildContext context) {
    return DraggableScrollableSheet(
      initialChildSize: 0.5,
      minChildSize: 0.3,
      maxChildSize: 0.9,
      expand: false,
      builder: (_, scroll) => Container(
        decoration: const BoxDecoration(
          color: DesignColors.surfaceDark,
          borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
        ),
        padding: const EdgeInsets.fromLTRB(8, 12, 8, 16),
        child: ListView.separated(
          controller: scroll,
          itemCount: templates.length + 1,
          separatorBuilder: (_, __) => const Divider(height: 1),
          itemBuilder: (_, i) {
            if (i == 0) {
              return ListTile(
                leading: const Icon(Icons.clear, size: 18),
                title: Text(
                  blankLabel,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 13,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                onTap: () => Navigator.of(context).pop(''),
              );
            }
            final t = templates[i - 1];
            final cat = (t['category'] ?? '').toString();
            final name = (t['name'] ?? '').toString();
            final path = cat.isEmpty ? name : '$cat/$name';
            return ListTile(
              leading: templateIconWidget(
                idOrName: name,
                displayName: name,
                size: 24,
              ),
              title: Text(
                name,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 13,
                  fontWeight: FontWeight.w600,
                ),
              ),
              subtitle: Text(
                path,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: FontSizes.label,
                  color: DesignColors.textMuted,
                ),
              ),
              onTap: () => Navigator.of(context).pop(path),
            );
          },
        ),
      ),
    );
  }
}
