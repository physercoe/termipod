import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../l10n/app_localizations.dart';
import '../../providers/hub_provider.dart';
import '../../providers/vocab_provider.dart';
import '../../services/vocab/vocab_axis.dart';
import '../../theme/design_colors.dart';
import '../../theme/tokens.dart';
import '../../widgets/app_chip.dart';

/// Localized label for a document kind wire value
/// (`memo` / `draft` / `report` / `review`). Unknown values fall back to
/// the raw wire string. Shared with the documents list screen.
String documentKindLabel(AppLocalizations l10n, String kind) {
  switch (kind) {
    case 'memo':
      return l10n.docKindMemo;
    case 'draft':
      return l10n.docKindDraft;
    case 'report':
      return l10n.docKindReport;
    case 'review':
      return l10n.docKindReview;
    default:
      return kind;
  }
}

/// Compose a new document (memo, draft, report, or review) from the phone.
/// Hub enforces the kind on the server; we offer the four blueprint §6.7
/// kinds as a chip bar. Content is inline text — artifact-backed docs are
/// produced by agents, not composed here.
class DocumentCreateSheet extends ConsumerStatefulWidget {
  /// Optional pre-filled project scope. When set, the project field is
  /// read-only and the sheet posts under that project. When null, the
  /// user picks a project from the list.
  final String? projectId;
  const DocumentCreateSheet({super.key, this.projectId});

  @override
  ConsumerState<DocumentCreateSheet> createState() =>
      _DocumentCreateSheetState();
}

class _DocumentCreateSheetState extends ConsumerState<DocumentCreateSheet> {
  List<Map<String, dynamic>>? _projects;
  String? _loadError;
  // Set when the hub client is unconfigured. Rendered as a localized
  // message at build time — resolving l10n here would throw, since the
  // client==null branch of _load() runs synchronously during initState.
  bool _hubMissing = false;
  bool _loading = true;

  final _title = TextEditingController();
  final _content = TextEditingController();
  String _kind = 'memo';
  String? _projectId;
  bool _submitting = false;

  static const _kinds = ['memo', 'draft', 'report', 'review'];

  @override
  void initState() {
    super.initState();
    _projectId = widget.projectId;
    if (widget.projectId == null) {
      _load();
    } else {
      _loading = false;
    }
  }

  @override
  void dispose() {
    _title.dispose();
    _content.dispose();
    super.dispose();
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
      if (!mounted) return;
      setState(() {
        _projects = projects;
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
    final title = _title.text.trim();
    final content = _content.text.trim();
    if (projectId == null || projectId.isEmpty || title.isEmpty) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() => _submitting = true);
    try {
      final doc = await client.createDocument(
        projectId: projectId,
        kind: _kind,
        title: title,
        contentInline: content.isEmpty ? '(no content)' : content,
      );
      if (!mounted) return;
      Navigator.of(context).pop(doc);
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
        child: _body(scroll),
      ),
    );
  }

  Widget _body(ScrollController scroll) {
    final l10n = AppLocalizations.of(context)!;
    final voc = ref.watch(vocabularyProvider);
    final documentTerm = voc.term(VocabAxis.entityDocument);
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
    final submittable = (_projectId ?? '').isNotEmpty &&
        _title.text.trim().isNotEmpty &&
        !_submitting;

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
          l10n.newDocument(documentTerm.lower),
          style: GoogleFonts.spaceGrotesk(
            fontSize: 18,
            fontWeight: FontWeight.w700,
          ),
        ),
        const SizedBox(height: 16),
        _label(l10n.fieldKind),
        Wrap(
          spacing: 6,
          runSpacing: 6,
          children: [
            for (final k in _kinds)
              AppChoiceChip(
                label: documentKindLabel(l10n, k),
                selected: _kind == k,
                onTap: () => setState(() => _kind = k),
              ),
          ],
        ),
        const SizedBox(height: 16),
        _label(projectTerm.title),
        if (widget.projectId != null)
          InputDecorator(
            decoration: const InputDecoration(
              border: OutlineInputBorder(),
              isDense: true,
            ),
            child: Text(
              widget.projectId!,
              style: GoogleFonts.jetBrainsMono(fontSize: 13),
            ),
          )
        else
          _ProjectField(
            projects: projects,
            selectedId: _projectId,
            pickHint: l10n.pickProjectHint(projectTerm.lower),
            onChanged: (id) => setState(() => _projectId = id),
          ),
        const SizedBox(height: 16),
        _label(l10n.fieldTitle),
        TextField(
          controller: _title,
          enabled: !_submitting,
          style: GoogleFonts.spaceGrotesk(fontSize: 14),
          decoration: InputDecoration(
            border: const OutlineInputBorder(),
            isDense: true,
            hintText: l10n.docTitleHint,
          ),
          onChanged: (_) => setState(() {}),
        ),
        const SizedBox(height: 16),
        _label(l10n.fieldContentMarkdown),
        TextField(
          controller: _content,
          enabled: !_submitting,
          minLines: 8,
          maxLines: 20,
          style: GoogleFonts.jetBrainsMono(fontSize: 12, height: 1.4),
          decoration: InputDecoration(
            border: const OutlineInputBorder(),
            isDense: true,
            hintText: l10n.docContentHint,
          ),
        ),
        const SizedBox(height: 20),
        FilledButton(
          onPressed: submittable ? _submit : null,
          child: _submitting
              ? const SizedBox(
                  width: 14,
                  height: 14,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : Text(l10n.createDocumentButton(documentTerm.lower)),
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
        final picked = await showModalBottomSheet<String>(
          context: context,
          isScrollControlled: true,
          backgroundColor: Colors.transparent,
          builder: (_) => _ProjectPickerSheet(projects: projects),
        );
        if (picked != null && picked.isNotEmpty) onChanged(picked);
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
            return ListTile(
              title: Text(
                name,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 14,
                  fontWeight: FontWeight.w600,
                ),
              ),
              subtitle: Text(
                id,
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
