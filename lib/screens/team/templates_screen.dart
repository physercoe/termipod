import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import '../projects/project_create_sheet.dart';
import 'agent_families_screen.dart';
import 'template_icon.dart';

/// Canonical category order across the Library Templates tab and the
/// New-template sheet. Mental flow: who runs it (agents) → what it says
/// (prompts) → how the project unfolds (plans) → which project shape
/// (projects) → guardrails (policies). Server may return categories in
/// any order; this list pins ordering for both the section list and the
/// category chip strip.
const _kTemplateCategoryOrder = <String>[
  'agents',
  'prompts',
  'plans',
  'projects',
  'policies',
];

/// Browser + editor for team templates (agents / prompts / policies)
/// plus the agent-family registry. Hub seeds templates on first init
/// from the embedded FS; the user owns them after that. The mobile
/// editor is intentionally unstructured — raw YAML / markdown / JSON
/// in a mono text field — because the authoritative shape lives in
/// docs/hub-agents.md and we don't want a schema-aware UI fighting
/// upstream changes.
///
/// Two tabs: "Templates" (agent personas, prompts, policies) and
/// "Engines" (the agent-family registry — claude-code/gemini-cli/…).
/// Templates reference engines via backend.kind, so they belong on
/// the same screen. Replaces the old standalone bolt icon entry point.
class TemplatesScreen extends ConsumerStatefulWidget {
  const TemplatesScreen({super.key});

  @override
  ConsumerState<TemplatesScreen> createState() => _TemplatesScreenState();
}

class _TemplatesScreenState extends ConsumerState<TemplatesScreen>
    with SingleTickerProviderStateMixin {
  late final TabController _tabs;
  final _familiesKey = GlobalKey<AgentFamiliesTabState>();

  List<Map<String, dynamic>>? _rows;
  String? _error;
  bool _loading = true;

  // Search + collapse state lives on the parent so a single AppBar
  // search field covers both tabs and so collapse persists across
  // tab swaps (going to Engines and back doesn't blow away which
  // Templates categories the user has expanded).
  bool _searching = false;
  String _query = '';
  late final TextEditingController _searchCtl;
  // Categories the user has explicitly collapsed. Default = empty
  // (everything expanded on first open). Persists in-memory for the
  // life of the screen; resetting requires re-opening the Library.
  final Set<String> _collapsed = <String>{};

  @override
  void initState() {
    super.initState();
    _tabs = TabController(length: 2, vsync: this);
    _tabs.addListener(() {
      if (_tabs.indexIsChanging) return;
      // Rebuild AppBar so the action buttons swap to the active tab.
      setState(() {});
    });
    _searchCtl = TextEditingController();
    _load();
  }

  @override
  void dispose() {
    _tabs.dispose();
    _searchCtl.dispose();
    super.dispose();
  }

  void _toggleSearch() {
    setState(() {
      _searching = !_searching;
      if (!_searching) {
        _searchCtl.clear();
        _query = '';
      }
    });
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
    // Project templates live in the projects table (DB row, is_template=1),
    // not on the filesystem like the other categories — route to the
    // project create sheet with isTemplate:true rather than the YAML editor.
    if (created.category == 'projects') {
      await showModalBottomSheet<bool>(
        context: context,
        isScrollControlled: true,
        builder: (_) => const ProjectCreateSheet(isTemplate: true),
      );
      await _load();
      return;
    }
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
    final onTemplates = _tabs.index == 0;
    return Scaffold(
      appBar: AppBar(
        title: _searching
            ? TextField(
                controller: _searchCtl,
                autofocus: true,
                decoration: const InputDecoration(
                  hintText: 'Search templates and engines',
                  border: InputBorder.none,
                ),
                style: GoogleFonts.spaceGrotesk(fontSize: 16),
                onChanged: (v) => setState(() => _query = v),
              )
            : Text(
                'Library',
                style: GoogleFonts.spaceGrotesk(
                    fontSize: 18, fontWeight: FontWeight.w700),
              ),
        actions: [
          IconButton(
            tooltip: _searching ? 'Close search' : 'Search',
            icon: Icon(_searching ? Icons.close : Icons.search),
            onPressed: _toggleSearch,
          ),
          IconButton(
            tooltip: onTemplates ? 'New template' : 'New family',
            icon: const Icon(Icons.add),
            onPressed: onTemplates
                ? (_loading ? null : _newTemplate)
                : () => _familiesKey.currentState?.newFamily(),
          ),
          IconButton(
            icon: const Icon(Icons.refresh),
            onPressed: onTemplates
                ? (_loading ? null : _load)
                : () => _familiesKey.currentState?.load(),
          ),
        ],
        bottom: TabBar(
          controller: _tabs,
          tabs: const [
            Tab(text: 'Templates'),
            Tab(text: 'Engines'),
          ],
        ),
      ),
      body: TabBarView(
        controller: _tabs,
        children: [
          _body(),
          AgentFamiliesTab(key: _familiesKey, query: _query),
        ],
      ),
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
    final allRows = _rows ?? const <Map<String, dynamic>>[];
    if (allRows.isEmpty) {
      return Center(
        child: Text('No templates seeded yet.',
            style: GoogleFonts.spaceGrotesk(fontSize: 13, color: muted)),
      );
    }
    final q = _query.trim().toLowerCase();
    final filtered = q.isEmpty
        ? allRows
        : allRows.where((row) {
            final name = (row['name'] ?? '').toString().toLowerCase();
            final cat = (row['category'] ?? '').toString().toLowerCase();
            return name.contains(q) || cat.contains(q);
          }).toList(growable: false);
    if (filtered.isEmpty) {
      return Center(
        child: Text(
          'No matches for "$_query".',
          style: GoogleFonts.spaceGrotesk(fontSize: 13, color: muted),
        ),
      );
    }
    final grouped = <String, List<Map<String, dynamic>>>{};
    for (final row in filtered) {
      final cat = (row['category'] ?? '').toString();
      grouped.putIfAbsent(cat, () => []).add(row);
    }
    // Sort sections by the canonical order. Unknown categories sink to
    // the bottom in alpha order so a future server-added kind still
    // surfaces without a mobile bump.
    int sectionRank(String cat) {
      final i = _kTemplateCategoryOrder.indexOf(cat);
      return i < 0 ? _kTemplateCategoryOrder.length : i;
    }
    final orderedKeys = grouped.keys.toList()
      ..sort((a, b) {
        final r = sectionRank(a).compareTo(sectionRank(b));
        return r != 0 ? r : a.compareTo(b);
      });
    final searching = q.isNotEmpty;
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView(
        padding: const EdgeInsets.fromLTRB(0, 8, 0, 24),
        children: [
          for (final key in orderedKeys)
            _CategoryGroup(
              category: key,
              rows: grouped[key]!,
              // During an active search, force every matching section
              // open so the user sees the hits without manual taps.
              expanded: searching || !_collapsed.contains(key),
              onToggle: searching
                  ? null
                  : () => setState(() {
                        if (_collapsed.contains(key)) {
                          _collapsed.remove(key);
                        } else {
                          _collapsed.add(key);
                        }
                      }),
              onChanged: _load,
            ),
        ],
      ),
    );
  }
}

/// One collapsible category in the Library Templates tab. Hosts a
/// tappable header row (category name + tile count + expand chevron)
/// and the inline list of `_TemplateTile` entries when expanded. The
/// expand state is driven by the parent — search forces all sections
/// open regardless of the user's prior collapse, which is why this
/// widget is dumb / stateless about expansion.
class _CategoryGroup extends StatelessWidget {
  final String category;
  final List<Map<String, dynamic>> rows;
  final bool expanded;
  final VoidCallback? onToggle;
  final VoidCallback onChanged;

  const _CategoryGroup({
    required this.category,
    required this.rows,
    required this.expanded,
    required this.onChanged,
    this.onToggle,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        InkWell(
          onTap: onToggle,
          child: Padding(
            padding: const EdgeInsets.fromLTRB(16, 12, 16, 6),
            child: Row(
              children: [
                Icon(
                  expanded ? Icons.expand_more : Icons.chevron_right,
                  size: 18,
                  color: muted,
                ),
                const SizedBox(width: 4),
                Text(
                  category,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    fontWeight: FontWeight.w700,
                    color: muted,
                    letterSpacing: 0.6,
                  ),
                ),
                const SizedBox(width: 8),
                Text(
                  '${rows.length}',
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 11,
                    color: muted,
                  ),
                ),
              ],
            ),
          ),
        ),
        if (expanded)
          for (final row in rows)
            _TemplateTile(row: row, onChanged: onChanged),
      ],
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

  Future<void> _resetToDefault() async {
    final confirm = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Reset to default?'),
        content: Text(
          'Replaces ${widget.category}/$_name on disk with the built-in '
          'embedded copy. Use this if your template is missing fields '
          'because it was seeded by an older hub version.\n\n'
          'Fails if no built-in version exists for this template.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('Cancel'),
          ),
          FilledButton.tonal(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('Reset'),
          ),
        ],
      ),
    );
    if (confirm != true) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() => _saving = true);
    try {
      await client.deleteTemplate(widget.category, _name);
      final embedded = await client.getTemplate(widget.category, _name);
      await client.putTemplate(widget.category, _name, embedded);
      if (!mounted) return;
      setState(() {
        _ctrl.text = embedded;
        _savedBody = embedded;
        _dirty = false;
        _saving = false;
      });
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
            content: Text('Reset to built-in default'),
            duration: Duration(seconds: 2)),
      );
    } catch (e) {
      if (!mounted) return;
      setState(() => _saving = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Reset failed: $e')),
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
                  case 'reset':
                    _resetToDefault();
                  case 'delete':
                    _delete();
                }
              },
              itemBuilder: (_) => const [
                PopupMenuItem(value: 'rename', child: Text('Rename')),
                PopupMenuItem(
                    value: 'reset', child: Text('Reset to default')),
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

class _NewTemplateSheet extends ConsumerStatefulWidget {
  const _NewTemplateSheet();

  @override
  ConsumerState<_NewTemplateSheet> createState() => _NewTemplateSheetState();
}

class _NewTemplateSheetState extends ConsumerState<_NewTemplateSheet> {
  // Filesystem categories — order matches the canonical Library order so
  // the chip strip and the section list visually agree. 'projects' is
  // appended last because it's a special-cased route (DB row, not a
  // filesystem write).
  static const _fsCategories = ['agents', 'prompts', 'plans', 'policies'];
  static const _categories = [..._fsCategories, 'projects'];

  String _category = 'agents';
  final _name = TextEditingController();
  String? _err;

  // Clone source: when non-null, the new template's body is seeded from
  // an existing template rather than the built-in starter. Cleared on
  // category change so a "clone agent" pick doesn't leak into a "blank
  // policy" intent.
  String? _cloneSourceName;
  String? _cloneSourceBody;

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
        // Mirrors hub/templates/agents/coder.v1.yaml — the canonical
        // worker template — so a user-authored agent has the same
        // required fields a steward expects (driving_mode resolves the
        // launcher's mode plumbing; without it the steward reports
        // "missing driving_mode" at spawn time). default_workdir is
        // intentionally absent so the launcher auto-derives
        // ~/hub-work/<pid8>/<handle> per project (v1.0.595).
        return '''# Custom agent template. Edit freely — user files always win
# over the embedded built-ins.
template: agents.$id
version: 1
extends: null

driving_mode: M2
fallback_modes: [M4]

backend:
  kind: claude-code
  model: claude-sonnet-4-6
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
      case 'plans':
        return '''# Custom plan template. See docs/hub-plans.md for shape.
template: plans.$id
version: 1
phases:
  - id: p1
    name: "Phase 1"
    steps: []
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

  /// Open a picker showing existing templates of the current category;
  /// on pick, fetch the body via getTemplate and stash it as the clone
  /// source. The submit path uses `_cloneSourceBody` over `_starterBody()`
  /// when set so users can fork an existing template instead of editing
  /// the built-in starter scaffold.
  Future<void> _pickCloneSource() async {
    if (_category == 'projects') return; // routed elsewhere
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    final rows = await client.listTemplates();
    final inCat = rows
        .where((r) => (r['category'] ?? '').toString() == _category)
        .toList();
    if (!mounted) return;
    if (inCat.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text('No existing $_category templates to clone from.'),
      ));
      return;
    }
    final picked = await showModalBottomSheet<String>(
      context: context,
      isScrollControlled: true,
      builder: (_) => _ClonePickerSheet(
        category: _category,
        rows: inCat,
      ),
    );
    if (picked == null || !mounted) return;
    try {
      final body = await client.getTemplate(_category, picked);
      if (!mounted) return;
      setState(() {
        _cloneSourceName = picked;
        _cloneSourceBody = body;
        // Suggest a sensible default name = "<source>-copy" so the user
        // isn't tempted to overwrite the bundled template (PUT to the
        // same name would shadow the embedded one).
        if (_name.text.trim().isEmpty) {
          final stem = picked.replaceAll(RegExp(r'\.[^.]+$'), '');
          _name.text = '$stem-copy';
        }
      });
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text('Clone failed: $e'),
      ));
    }
  }

  void _clearClone() {
    setState(() {
      _cloneSourceName = null;
      _cloneSourceBody = null;
    });
  }

  void _submit() {
    // Project templates skip name/body collection here — the project
    // create sheet owns those fields. Return a sentinel so the parent
    // can dispatch the routing.
    if (_category == 'projects') {
      Navigator.of(context).pop(_NewTemplateRequest('projects', '', ''));
      return;
    }
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
    final body = _cloneSourceBody ?? _starterBody();
    Navigator.of(context).pop(_NewTemplateRequest(_category, name, body));
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
                  onSelected: (_) => setState(() {
                    _category = c;
                    _clearClone();
                  }),
                ),
            ],
          ),
          const SizedBox(height: 12),
          if (_category == 'projects') ...[
            // Project templates are DB rows, not files — the parent
            // routes us to the project create sheet so name/goal/etc are
            // collected there. Help text explains why this category
            // skips the inline name field.
            Container(
              padding: const EdgeInsets.all(10),
              decoration: BoxDecoration(
                color: DesignColors.surfaceDark,
                borderRadius: BorderRadius.circular(8),
                border: Border.all(color: DesignColors.borderDark),
              ),
              child: Text(
                'Project templates live in the projects table. Continue '
                'to the project create sheet to fill in name, goal, and '
                'phase shape — it will be saved with is_template=1.',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 12,
                  color: DesignColors.textMuted,
                ),
              ),
            ),
          ] else ...[
            // Clone affordance — surfaces above the name field so users
            // see "fork an existing one" before they start typing a name
            // and committing to the default scaffold.
            if (_cloneSourceName == null)
              OutlinedButton.icon(
                onPressed: _pickCloneSource,
                icon: const Icon(Icons.copy_outlined, size: 16),
                label: Text('Clone from existing $_category template'),
              )
            else
              Row(
                children: [
                  const Icon(Icons.content_copy, size: 14,
                      color: DesignColors.textMuted),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      'Cloning from $_cloneSourceName',
                      style: GoogleFonts.jetBrainsMono(
                          fontSize: 12, color: DesignColors.textMuted),
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                  TextButton.icon(
                    onPressed: _clearClone,
                    icon: const Icon(Icons.close, size: 14),
                    label: const Text('Clear'),
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
          ],
          const SizedBox(height: 16),
          FilledButton(
            onPressed: _submit,
            child: Text(_category == 'projects'
                ? 'Continue'
                : 'Create + open editor'),
          ),
        ],
      ),
    );
  }
}

/// Small picker bottom-sheet listing existing templates of a single
/// category. Pops the chosen template's name (e.g. `coder.v1.yaml`) so
/// the caller can fetch and clone the body.
class _ClonePickerSheet extends StatelessWidget {
  final String category;
  final List<Map<String, dynamic>> rows;
  const _ClonePickerSheet({required this.category, required this.rows});

  @override
  Widget build(BuildContext context) {
    return DraggableScrollableSheet(
      expand: false,
      initialChildSize: 0.55,
      minChildSize: 0.3,
      maxChildSize: 0.9,
      builder: (_, controller) => Column(
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 12, 8, 0),
            child: Row(
              children: [
                Expanded(
                  child: Text(
                    'Clone $category template',
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
          const Divider(height: 1),
          Expanded(
            child: ListView.separated(
              controller: controller,
              itemCount: rows.length,
              separatorBuilder: (_, __) => const Divider(height: 1),
              itemBuilder: (_, i) {
                final r = rows[i];
                final name = (r['name'] ?? '').toString();
                return ListTile(
                  leading: templateIconWidget(
                    idOrName: name,
                    displayName: name,
                    size: 24,
                  ),
                  title: Text(
                    name,
                    style: GoogleFonts.jetBrainsMono(fontSize: 13),
                  ),
                  onTap: () => Navigator.of(context).pop(name),
                );
              },
            ),
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
