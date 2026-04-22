import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';

/// Declare a new experiment run (blueprint §6.5). Runs are the unit of
/// work for training, evaluation, and notebooks; the hub stores metadata
/// and status, the actual bytes stay on-host. This sheet is the "I'm
/// about to kick off $thing" entry point for humans working without an
/// agent in the loop.
class RunCreateSheet extends ConsumerStatefulWidget {
  final String? projectId;
  const RunCreateSheet({super.key, this.projectId});

  @override
  ConsumerState<RunCreateSheet> createState() => _RunCreateSheetState();
}

class _RunCreateSheetState extends ConsumerState<RunCreateSheet> {
  List<Map<String, dynamic>>? _projects;
  String? _loadError;
  bool _loading = true;

  final _name = TextEditingController();
  final _agent = TextEditingController();
  final _parent = TextEditingController();
  final _metadata = TextEditingController();
  String _kind = 'train';
  String? _projectId;
  bool _submitting = false;
  String? _metaError;

  static const _kinds = ['train', 'eval', 'notebook', 'bench', 'other'];

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
    _name.dispose();
    _agent.dispose();
    _parent.dispose();
    _metadata.dispose();
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
    if (projectId == null || projectId.isEmpty) return;
    Map<String, dynamic>? metadata;
    final metaText = _metadata.text.trim();
    if (metaText.isNotEmpty) {
      try {
        final decoded = jsonDecode(metaText);
        if (decoded is! Map) {
          setState(() => _metaError = 'Metadata must be a JSON object.');
          return;
        }
        metadata = decoded.cast<String, dynamic>();
      } catch (e) {
        setState(() => _metaError = 'Invalid JSON: $e');
        return;
      }
    }
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() {
      _submitting = true;
      _metaError = null;
    });
    try {
      final name = _name.text.trim();
      final agent = _agent.text.trim();
      final parent = _parent.text.trim();
      final created = await client.createRun(
        projectId: projectId,
        kind: _kind,
        name: name.isEmpty ? null : name,
        agentId: agent.isEmpty ? null : agent,
        parentRunId: parent.isEmpty ? null : parent,
        metadata: metadata,
      );
      if (!mounted) return;
      Navigator.of(context).pop(created);
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
    final submittable = (_projectId ?? '').isNotEmpty && !_submitting;

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
          'New run',
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
        _label('Name (optional)'),
        TextField(
          controller: _name,
          enabled: !_submitting,
          style: GoogleFonts.spaceGrotesk(fontSize: 14),
          decoration: const InputDecoration(
            border: OutlineInputBorder(),
            isDense: true,
            hintText: 'e.g. sft-4b-lr3e-5',
          ),
        ),
        const SizedBox(height: 16),
        _label('Agent id (optional)'),
        TextField(
          controller: _agent,
          enabled: !_submitting,
          style: GoogleFonts.jetBrainsMono(fontSize: 13),
          decoration: const InputDecoration(
            border: OutlineInputBorder(),
            isDense: true,
            hintText: 'If driven by an agent',
          ),
        ),
        const SizedBox(height: 16),
        _label('Parent run id (optional)'),
        TextField(
          controller: _parent,
          enabled: !_submitting,
          style: GoogleFonts.jetBrainsMono(fontSize: 13),
          decoration: const InputDecoration(
            border: OutlineInputBorder(),
            isDense: true,
            hintText: 'For forks / child sweeps',
          ),
        ),
        const SizedBox(height: 16),
        _label('Metadata (JSON, optional)'),
        TextField(
          controller: _metadata,
          enabled: !_submitting,
          minLines: 6,
          maxLines: 16,
          style: GoogleFonts.jetBrainsMono(fontSize: 12, height: 1.4),
          decoration: InputDecoration(
            border: const OutlineInputBorder(),
            isDense: true,
            hintText: '{\n  "dataset": "v3",\n  "lr": 3e-5\n}',
            hintStyle: GoogleFonts.jetBrainsMono(
              fontSize: 11,
              color: DesignColors.textMuted,
            ),
            errorText: _metaError,
          ),
          onChanged: (_) {
            if (_metaError != null) setState(() => _metaError = null);
          },
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
              : const Text('Create run'),
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
