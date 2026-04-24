import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:termipod/l10n/app_localizations.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import 'template_icon.dart';

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
  final String initialKind;
  const ProjectCreateSheet({super.key, this.initialKind = 'goal'});

  @override
  ConsumerState<ProjectCreateSheet> createState() =>
      _ProjectCreateSheetState();
}

class _ProjectCreateSheetState extends ConsumerState<ProjectCreateSheet> {
  final _name = TextEditingController();
  final _goal = TextEditingController();
  final _docsRoot = TextEditingController();
  final _configYaml = TextEditingController();
  late String _kind = widget.initialKind == 'standing' ? 'standing' : 'goal';
  String? _templateId; // FK to a projects row with is_template=1 (blueprint §6.1)
  String? _onCreateTemplateId; // FK to a template project auto-fired on create
  // Populated when the picked template carries a non-empty parameters_json.
  // Tied to _parametersSourceId so clearing the owning template drops them.
  Map<String, dynamic>? _parameters;
  String? _parametersSourceId;
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
        parameters: _parameters,
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

  Future<void> _editCurrentParameters() async {
    final current = _parameters;
    if (current == null) return;
    final edited = await showModalBottomSheet<Map<String, dynamic>?>(
      context: context,
      isScrollControlled: true,
      builder: (_) => _TemplateParameterSheet(
        // Use the current values as both schema (type shape) and defaults.
        schema: current,
        initial: current,
      ),
    );
    if (edited == null) return; // cancelled
    setState(() {
      _parameters = edited;
    });
  }

  Future<void> _pickTemplate({required bool forOnCreate}) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    final picked = await showModalBottomSheet<_TemplatePickResult?>(
      context: context,
      isScrollControlled: true,
      builder: (_) => _TemplatePickerSheet(
        currentValue:
            forOnCreate ? _onCreateTemplateId : _templateId,
      ),
    );
    if (picked == null) return; // dismissed
    setState(() {
      final newId = picked.id.isEmpty ? null : picked.id;
      final previousId = forOnCreate ? _onCreateTemplateId : _templateId;
      if (forOnCreate) {
        _onCreateTemplateId = newId;
      } else {
        _templateId = newId;
      }
      // Parameters are owned by whichever template currently declares them.
      // Clearing that template drops the params; picking a new parametric
      // template replaces them.
      if (newId == null) {
        if (_parametersSourceId != null && _parametersSourceId == previousId) {
          _parameters = null;
          _parametersSourceId = null;
        }
      } else if (picked.parameters != null) {
        _parameters = picked.parameters;
        _parametersSourceId = newId;
      } else if (_parametersSourceId == previousId) {
        _parameters = null;
        _parametersSourceId = null;
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final insets = MediaQuery.of(context).viewInsets;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final l10n = AppLocalizations.of(context)!;
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
              Text(_kind == 'standing' ? l10n.newWorkspace : l10n.newProject,
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
                segments: [
                  ButtonSegment(
                    value: 'goal',
                    label: Text(l10n.kindProject),
                    icon: const Icon(Icons.flag_outlined, size: 16),
                  ),
                  ButtonSegment(
                    value: 'standing',
                    label: Text(l10n.kindWorkspace),
                    icon: const Icon(Icons.all_inclusive, size: 16),
                  ),
                ],
                selected: {_kind},
                onSelectionChanged: (s) =>
                    setState(() => _kind = s.first),
              ),
              const SizedBox(height: 12),
              Text(
                _kind == 'goal'
                    ? l10n.kindProjectHelper
                    : l10n.kindWorkspaceHelper,
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
                    : () => setState(() {
                          if (_parametersSourceId == _templateId) {
                            _parameters = null;
                            _parametersSourceId = null;
                          }
                          _templateId = null;
                        }),
              ),
              const SizedBox(height: 12),
              _TemplateField(
                label: 'On-create template',
                hint: 'Fires once when the project is created',
                value: _onCreateTemplateId,
                onTap: () => _pickTemplate(forOnCreate: true),
                onClear: _onCreateTemplateId == null
                    ? null
                    : () => setState(() {
                          if (_parametersSourceId == _onCreateTemplateId) {
                            _parameters = null;
                            _parametersSourceId = null;
                          }
                          _onCreateTemplateId = null;
                        }),
              ),
              if (_parameters != null) ...[
                const SizedBox(height: 12),
                _ParameterSummary(
                  parameters: _parameters!,
                  onEdit: _editCurrentParameters,
                ),
              ],
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

/// Payload popped by [_TemplatePickerSheet]. `id` empty means "clear"
/// (existing contract). `parameters` is non-null only when the picked
/// template carried a non-empty parameters_json and the user filled it in.
class _TemplatePickResult {
  final String id;
  final Map<String, dynamic>? parameters;
  const _TemplatePickResult(this.id, {this.parameters});
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
                  onPressed: () =>
                      Navigator.of(context).pop(const _TemplatePickResult('')),
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
          onTap: () => _handlePick(t, id),
        );
      },
    );
  }

  Future<void> _handlePick(Map<String, dynamic> row, String id) async {
    final schema = _extractParameterSchema(row['parameters_json']);
    if (schema == null || schema.isEmpty) {
      Navigator.of(context).pop(_TemplatePickResult(id));
      return;
    }
    // Push the parameter form as a nested sheet. Cancel drops back to the
    // picker so the user can choose a different template.
    final filled = await showModalBottomSheet<Map<String, dynamic>?>(
      context: context,
      isScrollControlled: true,
      builder: (_) => _TemplateParameterSheet(
        schema: schema,
        initial: schema,
        templateName: (row['name'] ?? '').toString(),
      ),
    );
    if (filled == null) return; // cancelled — keep the picker open
    if (!mounted) return;
    Navigator.of(context).pop(_TemplatePickResult(id, parameters: filled));
  }

  /// The API returns `parameters_json` as either a decoded Map (normal
  /// case) or a JSON-encoded string (defensive). Anything else is treated
  /// as "no schema".
  Map<String, dynamic>? _extractParameterSchema(dynamic raw) {
    if (raw == null) return null;
    if (raw is Map) return raw.cast<String, dynamic>();
    if (raw is String) {
      if (raw.isEmpty) return null;
      try {
        final decoded = jsonDecode(raw);
        if (decoded is Map) return decoded.cast<String, dynamic>();
      } catch (_) {
        return null;
      }
    }
    return null;
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
    // Match curated icons by `name` - that is what the init seed
    // populates for built-in project templates (`ablation-sweep`,
    // `reproduce-paper`, `write-memo`, `benchmark-comparison`). The
    // `id` column is a UUID and wouldn't match anything curated.
    // When selected we stick with the check mark so the selection
    // state stays unambiguous.
    final leading = selected
        ? const Icon(
            Icons.check_circle,
            color: DesignColors.success,
            size: 24,
          )
        : templateIconWidget(
            idOrName: name,
            displayName: name,
            size: 24,
          );
    return ListTile(
      dense: true,
      leading: leading,
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

/// Compact read-only view of the parameters the user entered for the
/// picked template. Tapping "Edit" re-opens the param form against the
/// current values (keeping current value shapes as the inferred schema).
class _ParameterSummary extends StatelessWidget {
  final Map<String, dynamic> parameters;
  final VoidCallback onEdit;
  const _ParameterSummary({
    required this.parameters,
    required this.onEdit,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final entries = parameters.entries.toList();
    return Container(
      padding: const EdgeInsets.fromLTRB(12, 10, 8, 10),
      decoration: BoxDecoration(
        border: Border.all(
          color: isDark ? DesignColors.borderDark : DesignColors.borderLight,
        ),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  'Template parameters',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              TextButton.icon(
                style: TextButton.styleFrom(
                  visualDensity: VisualDensity.compact,
                  padding: const EdgeInsets.symmetric(horizontal: 8),
                ),
                onPressed: onEdit,
                icon: const Icon(Icons.edit, size: 14),
                label: const Text('Edit'),
              ),
            ],
          ),
          const SizedBox(height: 4),
          for (final e in entries)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 2),
              child: Text(
                '${e.key}: ${jsonEncode(e.value)}',
                style: GoogleFonts.jetBrainsMono(fontSize: 11),
              ),
            ),
        ],
      ),
    );
  }
}

/// Renders one input per key in the template's `parameters_json`.
///
/// Type inference comes from the shape of each default value:
///   - List<int>/List<num>  -> comma-separated numeric text field
///   - List<String>         -> comma-separated text field
///   - int/num              -> numeric text field
///   - String               -> text field
///   - anything else        -> raw JSON text area, validated on submit
class _TemplateParameterSheet extends StatefulWidget {
  final Map<String, dynamic> schema;
  final Map<String, dynamic> initial;
  final String? templateName;
  const _TemplateParameterSheet({
    required this.schema,
    required this.initial,
    this.templateName,
  });

  @override
  State<_TemplateParameterSheet> createState() =>
      _TemplateParameterSheetState();
}

class _TemplateParameterSheetState extends State<_TemplateParameterSheet> {
  late final List<_ParamFieldSpec> _specs;
  final Map<String, TextEditingController> _controllers = {};
  final Map<String, String?> _errors = {};

  @override
  void initState() {
    super.initState();
    _specs = widget.schema.entries
        .map((e) => _ParamFieldSpec.infer(e.key, e.value))
        .toList();
    for (final spec in _specs) {
      final initial = widget.initial[spec.key] ?? spec.defaultValue;
      _controllers[spec.key] = TextEditingController(
        text: spec.format(initial),
      );
    }
  }

  @override
  void dispose() {
    for (final c in _controllers.values) {
      c.dispose();
    }
    super.dispose();
  }

  void _submit() {
    final result = <String, dynamic>{};
    final errors = <String, String?>{};
    var ok = true;
    for (final spec in _specs) {
      final text = _controllers[spec.key]!.text;
      try {
        result[spec.key] = spec.parse(text);
        errors[spec.key] = null;
      } catch (e) {
        errors[spec.key] = '$e';
        ok = false;
      }
    }
    setState(() {
      _errors
        ..clear()
        ..addAll(errors);
    });
    if (!ok) return;
    Navigator.of(context).pop(result);
  }

  @override
  Widget build(BuildContext context) {
    final insets = MediaQuery.of(context).viewInsets;
    final title = widget.templateName == null || widget.templateName!.isEmpty
        ? 'Template parameters'
        : 'Parameters for ${widget.templateName}';
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
              Text(
                title,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 18,
                  fontWeight: FontWeight.w700,
                ),
              ),
              const SizedBox(height: 4),
              Text(
                'Defaults come from the template. Adjust what you need.',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 11,
                  color: DesignColors.textMuted,
                ),
              ),
              const SizedBox(height: 16),
              for (final spec in _specs) ...[
                _ParamField(
                  spec: spec,
                  controller: _controllers[spec.key]!,
                  errorText: _errors[spec.key],
                ),
                const SizedBox(height: 12),
              ],
              const SizedBox(height: 4),
              Row(
                children: [
                  TextButton(
                    onPressed: () => Navigator.of(context).pop(),
                    child: const Text('Cancel'),
                  ),
                  const Spacer(),
                  FilledButton.icon(
                    onPressed: _submit,
                    icon: const Icon(Icons.check, size: 16),
                    label: const Text('Use these values'),
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

enum _ParamKind { intList, numList, stringList, intScalar, numScalar, stringScalar, rawJson }

class _ParamFieldSpec {
  final String key;
  final _ParamKind kind;
  final dynamic defaultValue;
  const _ParamFieldSpec(this.key, this.kind, this.defaultValue);

  factory _ParamFieldSpec.infer(String key, dynamic value) {
    if (value is List) {
      if (value.every((e) => e is int)) {
        return _ParamFieldSpec(key, _ParamKind.intList, value);
      }
      if (value.every((e) => e is num)) {
        return _ParamFieldSpec(key, _ParamKind.numList, value);
      }
      if (value.every((e) => e is String)) {
        return _ParamFieldSpec(key, _ParamKind.stringList, value);
      }
      return _ParamFieldSpec(key, _ParamKind.rawJson, value);
    }
    if (value is int) return _ParamFieldSpec(key, _ParamKind.intScalar, value);
    if (value is num) return _ParamFieldSpec(key, _ParamKind.numScalar, value);
    if (value is String) {
      return _ParamFieldSpec(key, _ParamKind.stringScalar, value);
    }
    return _ParamFieldSpec(key, _ParamKind.rawJson, value);
  }

  String get hint {
    switch (kind) {
      case _ParamKind.intList:
        return 'Comma-separated integers (e.g. 128, 256, 384)';
      case _ParamKind.numList:
        return 'Comma-separated numbers';
      case _ParamKind.stringList:
        return 'Comma-separated values';
      case _ParamKind.intScalar:
        return 'Integer';
      case _ParamKind.numScalar:
        return 'Number';
      case _ParamKind.stringScalar:
        return 'Text';
      case _ParamKind.rawJson:
        return 'Raw JSON';
    }
  }

  bool get multiline => kind == _ParamKind.rawJson;

  TextInputType get keyboardType {
    switch (kind) {
      case _ParamKind.intList:
      case _ParamKind.numList:
        return const TextInputType.numberWithOptions(
            signed: true, decimal: true);
      case _ParamKind.intScalar:
        return const TextInputType.numberWithOptions(signed: true);
      case _ParamKind.numScalar:
        return const TextInputType.numberWithOptions(
            signed: true, decimal: true);
      case _ParamKind.stringList:
      case _ParamKind.stringScalar:
        return TextInputType.text;
      case _ParamKind.rawJson:
        return TextInputType.multiline;
    }
  }

  String format(dynamic value) {
    if (value == null) return '';
    switch (kind) {
      case _ParamKind.intList:
      case _ParamKind.numList:
      case _ParamKind.stringList:
        if (value is List) return value.map((e) => '$e').join(', ');
        return '';
      case _ParamKind.intScalar:
      case _ParamKind.numScalar:
      case _ParamKind.stringScalar:
        return '$value';
      case _ParamKind.rawJson:
        try {
          return const JsonEncoder.withIndent('  ').convert(value);
        } catch (_) {
          return '$value';
        }
    }
  }

  /// Parses user input back to a JSON-serialisable value. Throws
  /// [FormatException] with a user-facing message on failure; the sheet
  /// surfaces the message as the field's error text.
  dynamic parse(String raw) {
    final text = raw.trim();
    switch (kind) {
      case _ParamKind.intList:
        if (text.isEmpty) return <int>[];
        return _splitCsv(text).map((s) {
          final n = int.tryParse(s);
          if (n == null) {
            throw FormatException('"$s" is not an integer');
          }
          return n;
        }).toList();
      case _ParamKind.numList:
        if (text.isEmpty) return <num>[];
        return _splitCsv(text).map((s) {
          final n = num.tryParse(s);
          if (n == null) {
            throw FormatException('"$s" is not a number');
          }
          return n;
        }).toList();
      case _ParamKind.stringList:
        if (text.isEmpty) return <String>[];
        return _splitCsv(text).toList();
      case _ParamKind.intScalar:
        final n = int.tryParse(text);
        if (n == null) throw const FormatException('Enter an integer');
        return n;
      case _ParamKind.numScalar:
        final n = num.tryParse(text);
        if (n == null) throw const FormatException('Enter a number');
        return n;
      case _ParamKind.stringScalar:
        return text;
      case _ParamKind.rawJson:
        if (text.isEmpty) return null;
        try {
          return jsonDecode(text);
        } catch (e) {
          throw FormatException('Invalid JSON: $e');
        }
    }
  }

  static Iterable<String> _splitCsv(String text) sync* {
    for (final chunk in text.split(',')) {
      final t = chunk.trim();
      if (t.isEmpty) continue;
      yield t;
    }
  }
}

class _ParamField extends StatelessWidget {
  final _ParamFieldSpec spec;
  final TextEditingController controller;
  final String? errorText;
  const _ParamField({
    required this.spec,
    required this.controller,
    required this.errorText,
  });

  @override
  Widget build(BuildContext context) {
    final mono = spec.kind == _ParamKind.rawJson ||
        spec.kind == _ParamKind.intList ||
        spec.kind == _ParamKind.numList ||
        spec.kind == _ParamKind.intScalar ||
        spec.kind == _ParamKind.numScalar;
    return TextField(
      controller: controller,
      keyboardType: spec.keyboardType,
      maxLines: spec.multiline ? 6 : 1,
      style: mono ? GoogleFonts.jetBrainsMono(fontSize: 12) : null,
      decoration: InputDecoration(
        labelText: spec.key,
        hintText: spec.hint,
        border: const OutlineInputBorder(),
        errorText: errorText,
      ),
    );
  }
}
