import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import 'template_icon.dart';

/// Browser + editor for team templates (agents / prompts / policies).
/// Hub seeds these on first init from the embedded FS; the user owns
/// them after that. The mobile editor is intentionally unstructured —
/// raw YAML / markdown / JSON in a mono text field — because the
/// authoritative shape lives in docs/hub-agents.md and we don't want a
/// schema-aware UI fighting upstream changes.
class TemplatesScreen extends ConsumerStatefulWidget {
  const TemplatesScreen({super.key});

  @override
  ConsumerState<TemplatesScreen> createState() => _TemplatesScreenState();
}

class _TemplatesScreenState extends ConsumerState<TemplatesScreen> {
  List<Map<String, dynamic>>? _rows;
  String? _error;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final rows = await client.listTemplates();
      if (!mounted) return;
      setState(() {
        _rows = rows;
        _loading = false;
        _error = null;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
  }

  Future<void> _newTemplate() async {
    final created = await showModalBottomSheet<_NewTemplateRequest>(
      context: context,
      isScrollControlled: true,
      builder: (_) => const _NewTemplateSheet(),
    );
    if (created == null || !mounted) return;
    await Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => TemplateEditorScreen(
        category: created.category,
        name: created.name,
        initialBody: created.body,
        isNew: true,
      ),
    ));
    await _load();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Templates',
          style: GoogleFonts.spaceGrotesk(
              fontSize: 18, fontWeight: FontWeight.w700),
        ),
        actions: [
          IconButton(
            tooltip: 'New template',
            icon: const Icon(Icons.add),
            onPressed: _loading ? null : _newTemplate,
          ),
          IconButton(
            icon: const Icon(Icons.refresh),
            onPressed: _loading ? null : _load,
          ),
        ],
      ),
      body: _body(),
    );
  }

  Widget _body() {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    if (_loading && _rows == null) {
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
                      color: DesignColors.error, fontSize: 12)),
              const SizedBox(height: 16),
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
    final rows = _rows ?? const <Map<String, dynamic>>[];
    if (rows.isEmpty) {
      return Center(
        child: Text('No templates seeded yet.',
            style: GoogleFonts.spaceGrotesk(fontSize: 13, color: muted)),
      );
    }
    final grouped = <String, List<Map<String, dynamic>>>{};
    for (final row in rows) {
      final cat = (row['category'] ?? '').toString();
      grouped.putIfAbsent(cat, () => []).add(row);
    }
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView(
        padding: const EdgeInsets.fromLTRB(0, 8, 0, 24),
        children: [
          for (final entry in grouped.entries) ...[
            Padding(
              padding:
                  const EdgeInsets.fromLTRB(16, 12, 16, 6),
              child: Text(
                entry.key,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 12,
                  fontWeight: FontWeight.w700,
                  color: muted,
                  letterSpacing: 0.6,
                ),
              ),
            ),
            for (final row in entry.value)
              _TemplateTile(row: row, onChanged: _load),
          ],
        ],
      ),
    );
  }
}

class _TemplateTile extends StatelessWidget {
  final Map<String, dynamic> row;
  final VoidCallback onChanged;
  const _TemplateTile({required this.row, required this.onChanged});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final name = (row['name'] ?? '').toString();
    final size = row['size'] is int ? row['size'] as int : 0;
    final cat = (row['category'] ?? '').toString();
    return ListTile(
      leading: templateIconWidget(
        idOrName: name,
        displayName: name,
        size: 24,
      ),
      title: Text(name,
          style: GoogleFonts.jetBrainsMono(fontSize: 13)),
      subtitle: Text(_fmtSize(size),
          style: GoogleFonts.jetBrainsMono(fontSize: 11, color: muted)),
      trailing: const Icon(Icons.chevron_right, size: 20),
      onTap: () async {
        await Navigator.of(context).push(MaterialPageRoute(
          builder: (_) =>
              TemplateEditorScreen(category: cat, name: name),
        ));
        onChanged();
      },
    );
  }

  String _fmtSize(int n) {
    if (n < 1024) return '$n B';
    if (n < 1024 * 1024) return '${(n / 1024).toStringAsFixed(1)} KB';
    return '${(n / 1024 / 1024).toStringAsFixed(1)} MB';
  }
}

/// Editor for an existing or freshly-named template. Loads body via
/// getTemplate, edits in a mono text field, persists via putTemplate.
/// Rename and delete live in the overflow menu — same authority as PUT
/// (any token with /templates write access on this team).
class TemplateEditorScreen extends ConsumerStatefulWidget {
  final String category;
  final String name;
  final String? initialBody;
  final bool isNew;
  const TemplateEditorScreen({
    super.key,
    required this.category,
    required this.name,
    this.initialBody,
    this.isNew = false,
  });

  @override
  ConsumerState<TemplateEditorScreen> createState() =>
      _TemplateEditorScreenState();
}

class _TemplateEditorScreenState extends ConsumerState<TemplateEditorScreen> {
  late final TextEditingController _ctrl;
  bool _loading;
  bool _saving = false;
  bool _dirty = false;
  bool _previewMd = false;
  String? _error;
  String _name;
  String _savedBody = '';

  _TemplateEditorScreenState()
      : _loading = true,
        _name = '';

  @override
  void initState() {
    super.initState();
    _name = widget.name;
    _ctrl = TextEditingController(text: widget.initialBody ?? '');
    _ctrl.addListener(() {
      final dirty = _ctrl.text != _savedBody;
      if (dirty != _dirty) setState(() => _dirty = dirty);
    });
    if (widget.isNew) {
      // _savedBody is the on-disk content; for a brand-new template the
      // file doesn't exist yet, so any starter body is dirty and the
      // Save button is enabled the moment the editor opens.
      _savedBody = '';
      _dirty = (widget.initialBody ?? '').isNotEmpty;
      _loading = false;
    } else {
      _load();
    }
  }

  @override
  void dispose() {
    _ctrl.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final body = await client.getTemplate(widget.category, _name);
      if (!mounted) return;
      setState(() {
        _ctrl.text = body;
        _savedBody = body;
        _loading = false;
        _dirty = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
  }

  Future<void> _save() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() => _saving = true);
    try {
      await client.putTemplate(widget.category, _name, _ctrl.text);
      if (!mounted) return;
      setState(() {
        _saving = false;
        _savedBody = _ctrl.text;
        _dirty = false;
      });
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Saved'), duration: Duration(seconds: 1)),
      );
    } catch (e) {
      if (!mounted) return;
      setState(() => _saving = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Save failed: $e')),
      );
    }
  }

  Future<void> _rename() async {
    final newName = await showDialog<String>(
      context: context,
      builder: (ctx) => _RenameDialog(currentName: _name),
    );
    if (newName == null || newName == _name) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      await client.renameTemplate(widget.category, _name, newName);
      if (!mounted) return;
      setState(() => _name = newName);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Renamed to $newName'),
            duration: const Duration(seconds: 1)),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Rename failed: $e')),
      );
    }
  }

  Future<void> _delete() async {
    final confirm = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Delete template?'),
        content: Text(
          'Removes ${widget.category}/$_name from disk. '
          'If the template ships built-in, the embedded copy will surface '
          'on next read.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('Cancel'),
          ),
          FilledButton.tonal(
            style: FilledButton.styleFrom(
                backgroundColor: DesignColors.error.withValues(alpha: 0.15),
                foregroundColor: DesignColors.error),
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('Delete'),
          ),
        ],
      ),
    );
    if (confirm != true) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      await client.deleteTemplate(widget.category, _name);
      if (!mounted) return;
      Navigator.of(context).pop();
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Delete failed: $e')),
      );
    }
  }

  bool get _isMarkdown {
    final lower = _name.toLowerCase();
    return lower.endsWith('.md') || lower.endsWith('.markdown');
  }

  @override
  Widget build(BuildContext context) {
    return PopScope(
      canPop: !_dirty,
      onPopInvokedWithResult: (didPop, _) async {
        if (didPop || !_dirty) return;
        final discard = await showDialog<bool>(
          context: context,
          builder: (ctx) => AlertDialog(
            title: const Text('Discard changes?'),
            content: const Text('Unsaved edits will be lost.'),
            actions: [
              TextButton(
                onPressed: () => Navigator.of(ctx).pop(false),
                child: const Text('Keep editing'),
              ),
              FilledButton.tonal(
                onPressed: () => Navigator.of(ctx).pop(true),
                child: const Text('Discard'),
              ),
            ],
          ),
        );
        if (discard == true && mounted) Navigator.of(context).pop();
      },
      child: Scaffold(
        appBar: AppBar(
          title: Text(
            _name,
            style: GoogleFonts.spaceGrotesk(
                fontSize: 16, fontWeight: FontWeight.w700),
          ),
          actions: [
            if (_isMarkdown)
              IconButton(
                tooltip: _previewMd ? 'Edit' : 'Preview',
                icon: Icon(_previewMd ? Icons.edit : Icons.visibility),
                onPressed: () => setState(() => _previewMd = !_previewMd),
              ),
            IconButton(
              tooltip: 'Save',
              icon: _saving
                  ? const SizedBox(
                      width: 18,
                      height: 18,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : const Icon(Icons.save_outlined),
              onPressed: (_dirty && !_saving) ? _save : null,
            ),
            PopupMenuButton<String>(
              onSelected: (v) {
                switch (v) {
                  case 'rename':
                    _rename();
                  case 'delete':
                    _delete();
                }
              },
              itemBuilder: (_) => const [
                PopupMenuItem(value: 'rename', child: Text('Rename')),
                PopupMenuItem(value: 'delete', child: Text('Delete')),
              ],
            ),
          ],
        ),
        body: _body(),
      ),
    );
  }

  Widget _body() {
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_error != null) {
      return Padding(
        padding: const EdgeInsets.all(24),
        child: Text(_error!,
            style: GoogleFonts.jetBrainsMono(
                color: DesignColors.error, fontSize: 12)),
      );
    }
    if (_previewMd && _isMarkdown) {
      return Markdown(
        data: _ctrl.text,
        selectable: true,
        padding: const EdgeInsets.all(16),
      );
    }
    return Padding(
      padding: const EdgeInsets.all(12),
      child: TextField(
        controller: _ctrl,
        enabled: !_saving,
        maxLines: null,
        expands: true,
        textAlignVertical: TextAlignVertical.top,
        style: GoogleFonts.jetBrainsMono(fontSize: 12, height: 1.45),
        decoration: const InputDecoration(
          border: OutlineInputBorder(),
          isDense: true,
          contentPadding: EdgeInsets.all(10),
        ),
        keyboardType: TextInputType.multiline,
        textCapitalization: TextCapitalization.none,
        autocorrect: false,
        enableSuggestions: false,
      ),
    );
  }
}

class _NewTemplateRequest {
  final String category;
  final String name;
  final String body;
  _NewTemplateRequest(this.category, this.name, this.body);
}

class _NewTemplateSheet extends StatefulWidget {
  const _NewTemplateSheet();

  @override
  State<_NewTemplateSheet> createState() => _NewTemplateSheetState();
}

class _NewTemplateSheetState extends State<_NewTemplateSheet> {
  static const _categories = ['agents', 'prompts', 'policies'];
  String _category = 'agents';
  final _name = TextEditingController();
  String? _err;

  @override
  void dispose() {
    _name.dispose();
    super.dispose();
  }

  String _suggestedExt() => switch (_category) {
        'prompts' => '.md',
        _ => '.yaml',
      };

  String _starterBody() {
    final base = _name.text.trim().replaceAll(RegExp(r'\.[^.]+$'), '');
    final id = base.isEmpty ? 'untitled' : base;
    switch (_category) {
      case 'agents':
        return '''# Custom agent template. Edit freely — user files always win
# over the embedded built-ins.
template: agents.$id
version: 1
extends: null

backend:
  kind: claude-code
  model: claude-sonnet-4-6
  default_workdir: ~/hub-work
  permission_modes:
    skip: "--dangerously-skip-permissions"
    prompt: "--permission-prompt-tool mcp__termipod__permission_prompt"
  cmd: "claude --model {{model}} --print --output-format stream-json --input-format stream-json --verbose {{permission_flag}}"

default_role: worker.generic
display_label: "$id"

default_capabilities:
  - blob.read
  - blob.write

prompt: $id.v1.md
''';
      case 'prompts':
        return '''# $id

You are an agent for {{principal.handle}}'s team. Describe your role,
constraints, and the journal contract here.
''';
      case 'policies':
        return '''# Custom policy. See docs/hub-policies.md for shape.
version: 1
allow:
  - kind: "*"
deny: []
''';
    }
    return '';
  }

  void _submit() {
    var name = _name.text.trim();
    if (name.isEmpty) {
      setState(() => _err = 'Name required.');
      return;
    }
    if (name.contains('/') ||
        name.contains(r'\') ||
        name.startsWith('.')) {
      setState(() => _err = 'Name cannot contain /, \\, or start with a dot.');
      return;
    }
    if (!name.contains('.')) name = '$name${_suggestedExt()}';
    Navigator.of(context).pop(
      _NewTemplateRequest(_category, name, _starterBody()),
    );
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: EdgeInsets.fromLTRB(
        16, 12, 16, 16 + MediaQuery.of(context).viewInsets.bottom,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Center(
            child: Container(
              width: 36, height: 4,
              margin: const EdgeInsets.only(bottom: 12),
              decoration: BoxDecoration(
                color: DesignColors.borderDark,
                borderRadius: BorderRadius.circular(2),
              ),
            ),
          ),
          Text('New template',
              style: GoogleFonts.spaceGrotesk(
                  fontSize: 18, fontWeight: FontWeight.w700)),
          const SizedBox(height: 16),
          Wrap(
            spacing: 8,
            children: [
              for (final c in _categories)
                ChoiceChip(
                  label: Text(c),
                  selected: _category == c,
                  onSelected: (_) => setState(() => _category = c),
                ),
            ],
          ),
          const SizedBox(height: 12),
          TextField(
            controller: _name,
            inputFormatters: [
              FilteringTextInputFormatter.deny(RegExp(r'[/\\]')),
            ],
            autofocus: true,
            style: GoogleFonts.jetBrainsMono(fontSize: 13),
            decoration: InputDecoration(
              labelText: 'Name',
              border: const OutlineInputBorder(),
              isDense: true,
              hintText: 'my-agent.v1${_suggestedExt()}',
              errorText: _err,
            ),
            onSubmitted: (_) => _submit(),
          ),
          const SizedBox(height: 16),
          FilledButton(
            onPressed: _submit,
            child: const Text('Create + open editor'),
          ),
        ],
      ),
    );
  }
}

class _RenameDialog extends StatefulWidget {
  final String currentName;
  const _RenameDialog({required this.currentName});

  @override
  State<_RenameDialog> createState() => _RenameDialogState();
}

class _RenameDialogState extends State<_RenameDialog> {
  late final TextEditingController _ctrl;
  String? _err;

  @override
  void initState() {
    super.initState();
    _ctrl = TextEditingController(text: widget.currentName);
  }

  @override
  void dispose() {
    _ctrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Rename template'),
      content: TextField(
        controller: _ctrl,
        autofocus: true,
        inputFormatters: [
          FilteringTextInputFormatter.deny(RegExp(r'[/\\]')),
        ],
        style: GoogleFonts.jetBrainsMono(fontSize: 13),
        decoration: InputDecoration(
          border: const OutlineInputBorder(),
          isDense: true,
          errorText: _err,
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: () {
            final v = _ctrl.text.trim();
            if (v.isEmpty || v.startsWith('.')) {
              setState(() => _err = 'Invalid name');
              return;
            }
            Navigator.of(context).pop(v);
          },
          child: const Text('Rename'),
        ),
      ],
    );
  }
}
