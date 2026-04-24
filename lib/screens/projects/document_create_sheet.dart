import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';

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
        _loadError = 'Hub not configured.';
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
        SnackBar(content: Text('Create failed: $e')),
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
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_loadError != null) {
      return Center(
        child: Text(
          _loadError!,
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
              borderRadius: BorderRadius.circular(2),
            ),
          ),
        ),
        Text(
          'New document',
          style: GoogleFonts.spaceGrotesk(
            fontSize: 18,
            fontWeight: FontWeight.w700,
          ),
        ),
        const SizedBox(height: 16),
        _label('Kind'),
        Wrap(
          spacing: 6,
          runSpacing: 6,
          children: [
            for (final k in _kinds)
              _KindChip(
                label: k,
                selected: _kind == k,
                onTap: () => setState(() => _kind = k),
              ),
          ],
        ),
        const SizedBox(height: 16),
        _label('Project'),
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
            onChanged: (id) => setState(() => _projectId = id),
          ),
        const SizedBox(height: 16),
        _label('Title'),
        TextField(
          controller: _title,
          enabled: !_submitting,
          style: GoogleFonts.spaceGrotesk(fontSize: 14),
          decoration: const InputDecoration(
            border: OutlineInputBorder(),
            isDense: true,
            hintText: 'e.g. Week 14 progress',
          ),
          onChanged: (_) => setState(() {}),
        ),
        const SizedBox(height: 16),
        _label('Content (markdown)'),
        TextField(
          controller: _content,
          enabled: !_submitting,
          minLines: 8,
          maxLines: 20,
          style: GoogleFonts.jetBrainsMono(fontSize: 12, height: 1.4),
          decoration: const InputDecoration(
            border: OutlineInputBorder(),
            isDense: true,
            hintText: '## Summary\n...',
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
              : const Text('Create document'),
        ),
      ],
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

class _KindChip extends StatelessWidget {
  final String label;
  final bool selected;
  final VoidCallback onTap;
  const _KindChip({
    required this.label,
    required this.selected,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(14),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
        decoration: BoxDecoration(
          color: selected
              ? DesignColors.primary.withValues(alpha: 0.2)
              : Colors.transparent,
          borderRadius: BorderRadius.circular(14),
          border: Border.all(
            color: selected ? DesignColors.primary : DesignColors.borderDark,
          ),
        ),
        child: Text(
          label,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 11,
            fontWeight: selected ? FontWeight.w700 : FontWeight.w500,
            color: selected ? DesignColors.primary : DesignColors.textMuted,
          ),
        ),
      ),
    );
  }
}

class _ProjectField extends StatelessWidget {
  final List<Map<String, dynamic>> projects;
  final String? selectedId;
  final ValueChanged<String?> onChanged;
  const _ProjectField({
    required this.projects,
    required this.selectedId,
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
    if (selectedId == null) return 'Pick a project';
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
                  fontSize: 10,
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
