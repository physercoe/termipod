import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';

/// Project creation sheet (blueprint §9 P2.7).
///
/// Lets the user pick the project `kind` (goal vs. standing), attach an
/// optional goal statement, and bind agent templates to drive the project
/// (a steward recipe via `template_id` and an on-create recipe via
/// `on_create_template_id`). The extra fields are optional — a one-liner
/// "just a project" create still works by filling only the name.
///
/// Pops `true` on success so the caller can refresh the project list.
class ProjectCreateSheet extends ConsumerStatefulWidget {
  const ProjectCreateSheet({super.key});

  @override
  ConsumerState<ProjectCreateSheet> createState() =>
      _ProjectCreateSheetState();
}

class _ProjectCreateSheetState extends ConsumerState<ProjectCreateSheet> {
  final _name = TextEditingController();
  final _goal = TextEditingController();
  final _docsRoot = TextEditingController();
  final _configYaml = TextEditingController();
  String _kind = 'goal';
  String? _templateId; // FK to a projects row with is_template=1 (blueprint §6.1)
  String? _onCreateTemplateId; // FK to a template project auto-fired on create
  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _name.dispose();
    _goal.dispose();
    _docsRoot.dispose();
    _configYaml.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    if (_name.text.trim().isEmpty) {
      setState(() => _error = 'Name required');
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await client.createProject(
        name: _name.text.trim(),
        kind: _kind,
        goal: _goal.text.trim().isEmpty ? null : _goal.text.trim(),
        templateId: _templateId,
        onCreateTemplateId: _onCreateTemplateId,
        docsRoot: _docsRoot.text.trim().isEmpty ? null : _docsRoot.text.trim(),
        configYaml:
            _configYaml.text.trim().isEmpty ? null : _configYaml.text.trim(),
      );
      if (mounted) Navigator.of(context).pop(true);
    } catch (e) {
      setState(() {
        _busy = false;
        _error = '$e';
      });
    }
  }

  Future<void> _pickTemplate({required bool forOnCreate}) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    final picked = await showModalBottomSheet<String?>(
      context: context,
      isScrollControlled: true,
      builder: (_) => _TemplatePickerSheet(
        currentValue:
            forOnCreate ? _onCreateTemplateId : _templateId,
      ),
    );
    if (picked == null) return; // dismissed
    setState(() {
      if (forOnCreate) {
        _onCreateTemplateId = picked.isEmpty ? null : picked;
      } else {
        _templateId = picked.isEmpty ? null : picked;
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final insets = MediaQuery.of(context).viewInsets;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Padding(
      padding: EdgeInsets.only(bottom: insets.bottom),
      child: SafeArea(
        top: false,
        child: SingleChildScrollView(
          padding: const EdgeInsets.all(20),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Text('New project',
                  style: GoogleFonts.spaceGrotesk(
                      fontSize: 18, fontWeight: FontWeight.w700)),
              const SizedBox(height: 16),
              TextField(
                controller: _name,
                autofocus: true,
                decoration: const InputDecoration(
                  labelText: 'Name',
                  border: OutlineInputBorder(),
                ),
                textInputAction: TextInputAction.next,
              ),
              const SizedBox(height: 16),
              Text(
                'Kind',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                  color: isDark
                      ? DesignColors.textMuted
                      : DesignColors.textMutedLight,
                ),
              ),
              const SizedBox(height: 6),
              SegmentedButton<String>(
                segments: const [
                  ButtonSegment(
                    value: 'goal',
                    label: Text('Goal'),
                    icon: Icon(Icons.flag_outlined, size: 16),
                  ),
                  ButtonSegment(
                    value: 'standing',
                    label: Text('Standing'),
                    icon: Icon(Icons.all_inclusive, size: 16),
                  ),
                ],
                selected: {_kind},
                onSelectionChanged: (s) =>
                    setState(() => _kind = s.first),
              ),
              const SizedBox(height: 12),
              Text(
                _kind == 'goal'
                    ? 'Ships when the goal is met, then archives.'
                    : 'Runs continuously; no completion state.',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 11,
                  color: isDark
                      ? DesignColors.textMuted
                      : DesignColors.textMutedLight,
                ),
              ),
              if (_kind == 'goal') ...[
                const SizedBox(height: 12),
                TextField(
                  controller: _goal,
                  maxLines: 3,
                  decoration: const InputDecoration(
                    labelText: 'Goal (what success looks like)',
                    hintText:
                        'e.g. "Reproduce the attention-is-all-you-need '
                        'ablation and write a memo."',
                    border: OutlineInputBorder(),
                  ),
                ),
              ],
              const SizedBox(height: 20),
              _TemplateField(
                label: 'Steward template',
                hint: 'Recipe that drives this project',
                value: _templateId,
                onTap: () => _pickTemplate(forOnCreate: false),
                onClear: _templateId == null
                    ? null
                    : () => setState(() => _templateId = null),
              ),
              const SizedBox(height: 12),
              _TemplateField(
                label: 'On-create template',
                hint: 'Fires once when the project is created',
                value: _onCreateTemplateId,
                onTap: () => _pickTemplate(forOnCreate: true),
                onClear: _onCreateTemplateId == null
                    ? null
                    : () => setState(() => _onCreateTemplateId = null),
              ),
              const SizedBox(height: 20),
              ExpansionTile(
                tilePadding: EdgeInsets.zero,
                childrenPadding: EdgeInsets.zero,
                title: Text(
                  'Advanced',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                children: [
                  const SizedBox(height: 4),
                  TextField(
                    controller: _docsRoot,
                    decoration: const InputDecoration(
                      labelText: 'Docs root (optional, e.g. docs/)',
                      border: OutlineInputBorder(),
                    ),
                    textInputAction: TextInputAction.next,
                  ),
                  const SizedBox(height: 12),
                  TextField(
                    controller: _configYaml,
                    maxLines: 4,
                    style: GoogleFonts.jetBrainsMono(fontSize: 12),
                    decoration: const InputDecoration(
                      labelText: 'Config YAML (optional)',
                      border: OutlineInputBorder(),
                    ),
                  ),
                ],
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
                    onPressed: _busy ? null : () => Navigator.of(context).pop(),
                    child: const Text('Cancel'),
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
                    label: const Text('Create'),
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

/// Tappable faux-field that surfaces the picked template id (or a hint
/// when none). Keeps the create sheet tidy — the actual picker lives in
/// `_TemplatePickerSheet` so the sheet isn't cluttered by a raw list.
class _TemplateField extends StatelessWidget {
  final String label;
  final String hint;
  final String? value;
  final VoidCallback onTap;
  final VoidCallback? onClear;
  const _TemplateField({
    required this.label,
    required this.hint,
    required this.value,
    required this.onTap,
    required this.onClear,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final hasValue = value != null && value!.isNotEmpty;
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(4),
      child: InputDecorator(
        decoration: InputDecoration(
          labelText: label,
          border: const OutlineInputBorder(),
          suffixIcon: hasValue
              ? IconButton(
                  tooltip: 'Clear',
                  icon: const Icon(Icons.close, size: 18),
                  onPressed: onClear,
                )
              : const Icon(Icons.chevron_right, size: 20),
        ),
        child: Text(
          hasValue ? value! : hint,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 13,
            color: hasValue
                ? null
                : (isDark
                    ? DesignColors.textMuted
                    : DesignColors.textMutedLight),
          ),
        ),
      ),
    );
  }
}

class _TemplatePickerSheet extends ConsumerStatefulWidget {
  final String? currentValue;
  const _TemplatePickerSheet({this.currentValue});

  @override
  ConsumerState<_TemplatePickerSheet> createState() =>
      _TemplatePickerSheetState();
}

class _TemplatePickerSheetState extends ConsumerState<_TemplatePickerSheet> {
  List<Map<String, dynamic>>? _rows;
  bool _loading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() {
        _loading = false;
        _error = 'Hub not configured.';
      });
      return;
    }
    try {
      // Per blueprint §6.1, `template_id` on a project row FKs another
      // project row with `is_template=1`. The picker reads project rows,
      // not YAML files under team/templates/{...}/ (those are agent /
      // prompt / policy building-blocks, not project skeletons).
      final rows = await client.listProjects(isTemplate: true);
      if (!mounted) return;
      setState(() {
        _rows = rows;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return DraggableScrollableSheet(
      expand: false,
      initialChildSize: 0.6,
      minChildSize: 0.4,
      maxChildSize: 0.9,
      builder: (_, controller) {
        return Column(
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 12, 8, 0),
              child: Row(
                children: [
                  Expanded(
                    child: Text(
                      'Pick a template',
                      style: GoogleFonts.spaceGrotesk(
                        fontSize: 16,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                  ),
                  IconButton(
                    icon: const Icon(Icons.close),
                    onPressed: () => Navigator.of(context).pop(),
                  ),
                ],
              ),
            ),
            if (widget.currentValue != null && widget.currentValue!.isNotEmpty)
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
                child: OutlinedButton.icon(
                  icon: const Icon(Icons.link_off, size: 16),
                  label: const Text('Clear selection'),
                  onPressed: () => Navigator.of(context).pop(''),
                ),
              ),
            const Divider(height: 1),
            Expanded(child: _list(controller, isDark)),
          ],
        );
      },
    );
  }

  Widget _list(ScrollController controller, bool isDark) {
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(_error!,
                  textAlign: TextAlign.center,
                  style: GoogleFonts.jetBrainsMono(
                      fontSize: 12, color: DesignColors.error)),
              const SizedBox(height: 12),
              FilledButton.tonalIcon(
                onPressed: () {
                  setState(() {
                    _loading = true;
                    _error = null;
                  });
                  _load();
                },
                icon: const Icon(Icons.refresh, size: 16),
                label: const Text('Retry'),
              ),
            ],
          ),
        ),
      );
    }
    final rows = _rows ?? const [];
    if (rows.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            'No project templates yet. Mark an existing project as a '
            'template (is_template=true) or seed built-in ones from the hub.',
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 12,
              color: DesignColors.textMuted,
            ),
          ),
        ),
      );
    }
    return ListView.builder(
      controller: controller,
      itemCount: rows.length,
      itemBuilder: (_, i) {
        final t = rows[i];
        final id = (t['id'] ?? '').toString();
        return _TemplatePickRow(
          row: t,
          selected: widget.currentValue == id,
          onTap: () => Navigator.of(context).pop(id),
        );
      },
    );
  }
}

class _TemplatePickRow extends StatelessWidget {
  final Map<String, dynamic> row;
  final bool selected;
  final VoidCallback onTap;
  const _TemplatePickRow({
    required this.row,
    required this.selected,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final name = (row['name'] ?? '').toString();
    final goal = (row['goal'] ?? '').toString();
    final kind = (row['kind'] ?? '').toString();
    return ListTile(
      dense: true,
      leading: Icon(
        selected ? Icons.check_circle : Icons.folder_special_outlined,
        color: selected ? DesignColors.success : null,
        size: 18,
      ),
      title: Text(
        name,
        style: GoogleFonts.jetBrainsMono(fontSize: 13),
      ),
      subtitle: Text(
        goal.isNotEmpty ? goal : kind,
        maxLines: 2,
        overflow: TextOverflow.ellipsis,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
          color: DesignColors.textMuted,
        ),
      ),
      onTap: onTap,
    );
  }
}
