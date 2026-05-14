import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';

/// Embeddable body for the hub's agent-family registry. Lives as a tab
/// inside TemplatesScreen — templates *use* engines via backend.kind, so
/// they belong on the same screen. The previous standalone screen used
/// a bolt icon in the AppBar that read like a snippet preset; merging
/// removes that ambiguity.
///
/// Embedded defaults (claude-code, gemini-cli, codex) are
/// read-only previews; custom families and overrides of embedded ones
/// are editable. Save hits the hub immediately and the next
/// host-runner probe (≈30s) publishes the change to capabilities — no
/// host-runner restart.
class AgentFamiliesTab extends ConsumerStatefulWidget {
  const AgentFamiliesTab({super.key});

  @override
  ConsumerState<AgentFamiliesTab> createState() => AgentFamiliesTabState();
}

class AgentFamiliesTabState extends ConsumerState<AgentFamiliesTab>
    with AutomaticKeepAliveClientMixin {
  List<Map<String, dynamic>>? _rows;
  bool _loading = true;
  String? _error;

  @override
  bool get wantKeepAlive => true;

  @override
  void initState() {
    super.initState();
    load();
  }

  /// Public so the parent TabbedScreen's AppBar refresh action can
  /// trigger a reload from the active tab.
  Future<void> load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final rows =
          (await client.listAgentFamiliesCached()).body.toList();
      rows.sort((a, b) =>
          (a['family'] ?? '').toString().compareTo((b['family'] ?? '').toString()));
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

  /// Public for the parent's tab-aware "+ New" AppBar action.
  Future<void> newFamily() async {
    final name = await showDialog<String>(
      context: context,
      builder: (_) => const _NewFamilyNameDialog(),
    );
    if (name == null || !mounted) return;
    await Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => AgentFamilyEditorScreen(
        family: name,
        isNew: true,
        initialBody: _scaffoldYAML(name),
      ),
    ));
    await load();
  }

  bool get loading => _loading;

  @override
  Widget build(BuildContext context) {
    super.build(context);
    return _body();
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
                  load();
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
        child: Text('No families registered.',
            style: GoogleFonts.spaceGrotesk(fontSize: 13, color: muted)),
      );
    }
    return RefreshIndicator(
      onRefresh: load,
      child: ListView(
        padding: const EdgeInsets.fromLTRB(0, 8, 0, 24),
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
            child: Text(
              'Embedded defaults are read-only. Add a custom family or override an embedded one to change capability probing.',
              style: GoogleFonts.spaceGrotesk(fontSize: 12, color: muted),
            ),
          ),
          for (final row in rows) _FamilyTile(row: row, onChanged: load),
        ],
      ),
    );
  }
}

class _FamilyTile extends StatelessWidget {
  final Map<String, dynamic> row;
  final VoidCallback onChanged;
  const _FamilyTile({required this.row, required this.onChanged});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final family = (row['family'] ?? '').toString();
    final bin = (row['bin'] ?? '').toString();
    final source = (row['source'] ?? 'embedded').toString();
    final supports =
        (row['supports'] as List?)?.map((e) => e.toString()).join(' · ') ?? '';
    return ListTile(
      title: Row(
        children: [
          Flexible(
            child: Text(family,
                style: GoogleFonts.jetBrainsMono(
                    fontSize: 13, fontWeight: FontWeight.w600)),
          ),
          const SizedBox(width: 8),
          _SourceChip(source: source),
        ],
      ),
      subtitle: Text(
        bin.isEmpty ? supports : '$bin   $supports',
        style: GoogleFonts.jetBrainsMono(fontSize: 11, color: muted),
      ),
      trailing: const Icon(Icons.chevron_right, size: 20),
      onTap: () async {
        await Navigator.of(context).push(MaterialPageRoute(
          builder: (_) => AgentFamilyEditorScreen(family: family),
        ));
        onChanged();
      },
    );
  }
}

class _SourceChip extends StatelessWidget {
  final String source;
  const _SourceChip({required this.source});

  @override
  Widget build(BuildContext context) {
    Color bg;
    Color fg;
    switch (source) {
      case 'custom':
        bg = DesignColors.success.withOpacity(0.18);
        fg = DesignColors.success;
        break;
      case 'override':
        bg = DesignColors.warning.withOpacity(0.18);
        fg = DesignColors.warning;
        break;
      default:
        final isDark = Theme.of(context).brightness == Brightness.dark;
        bg = (isDark ? Colors.white : Colors.black).withOpacity(0.07);
        fg = isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    }
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(10),
      ),
      child: Text(
        source,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
          fontWeight: FontWeight.w600,
          color: fg,
          letterSpacing: 0.4,
        ),
      ),
    );
  }
}

/// Editor for one family. Embedded entries open in read-only mode with
/// an "Override" affordance that flips the editor into write mode and
/// scaffolds a starter override body. Custom and override entries open
/// editable; saving PUTs the body and refreshes the list.
class AgentFamilyEditorScreen extends ConsumerStatefulWidget {
  final String family;
  final bool isNew;
  final String? initialBody;
  const AgentFamilyEditorScreen({
    super.key,
    required this.family,
    this.isNew = false,
    this.initialBody,
  });

  @override
  ConsumerState<AgentFamilyEditorScreen> createState() =>
      _AgentFamilyEditorScreenState();
}

class _AgentFamilyEditorScreenState
    extends ConsumerState<AgentFamilyEditorScreen> {
  final _ctrl = TextEditingController();
  bool _loading = true;
  bool _saving = false;
  bool _dirty = false;
  String? _error;
  String _source = '';
  String _savedBody = '';

  @override
  void initState() {
    super.initState();
    _ctrl.addListener(() {
      final dirty = _ctrl.text != _savedBody;
      if (dirty != _dirty) setState(() => _dirty = dirty);
    });
    if (widget.isNew) {
      _source = 'custom';
      _ctrl.text = widget.initialBody ?? '';
      _savedBody = '';
      _dirty = _ctrl.text.isNotEmpty;
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
      final got = await client.getAgentFamily(widget.family);
      final source = (got['source'] ?? 'embedded').toString();
      final body = _yamlFromRecord(got);
      if (!mounted) return;
      setState(() {
        _source = source;
        _ctrl.text = body;
        _savedBody = body;
        _dirty = false;
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

  Future<void> _save() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() {
      _saving = true;
      _error = null;
    });
    try {
      await client.putAgentFamily(widget.family, _ctrl.text);
      if (!mounted) return;
      setState(() {
        _savedBody = _ctrl.text;
        _dirty = false;
        _saving = false;
        // First successful PUT on an embedded family flips it to override.
        if (_source == 'embedded') _source = 'override';
      });
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Saved'), duration: Duration(seconds: 1)),
      );
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _saving = false;
        _error = '$e';
      });
    }
  }

  Future<void> _delete() async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        title: const Text('Delete override?'),
        content: Text(
          _source == 'override'
              ? 'This reverts ${widget.family} to its embedded default.'
              : 'This removes the custom family ${widget.family} from the registry.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Delete'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      await client.deleteAgentFamily(widget.family);
      if (!mounted) return;
      Navigator.of(context).pop();
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = '$e');
    }
  }

  Future<void> _override() async {
    setState(() {
      _ctrl.text = _yamlFromRecord({
        'family': widget.family,
        'bin': _extractField(_ctrl.text, 'bin'),
        'version_flag': _extractField(_ctrl.text, 'version_flag'),
        'supports': _extractList(_ctrl.text, 'supports'),
      });
      _savedBody = '';
      _dirty = true;
      _source = 'override';
    });
  }

  bool get _readOnly => _source == 'embedded';

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Scaffold(
      appBar: AppBar(
        title: Row(
          children: [
            Flexible(
              child: Text(widget.family,
                  overflow: TextOverflow.ellipsis,
                  style: GoogleFonts.jetBrainsMono(
                      fontSize: 14, fontWeight: FontWeight.w600)),
            ),
            const SizedBox(width: 8),
            _SourceChip(source: _source.isEmpty ? 'embedded' : _source),
          ],
        ),
        actions: [
          if (_readOnly)
            TextButton(
              onPressed: _loading ? null : _override,
              child: const Text('Override'),
            ),
          if (!_readOnly && !widget.isNew && _source != 'embedded')
            IconButton(
              tooltip: 'Delete override',
              icon: const Icon(Icons.delete_outline),
              onPressed: _saving ? null : _delete,
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
            onPressed: (_dirty && !_readOnly && !_saving) ? _save : null,
          ),
        ],
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : Column(
              children: [
                if (_error != null)
                  Container(
                    width: double.infinity,
                    padding: const EdgeInsets.all(12),
                    color: DesignColors.error.withOpacity(0.12),
                    child: Text(_error!,
                        style: GoogleFonts.jetBrainsMono(
                            fontSize: 12, color: DesignColors.error)),
                  ),
                if (_readOnly)
                  Container(
                    width: double.infinity,
                    padding: const EdgeInsets.fromLTRB(16, 12, 16, 12),
                    color: (isDark ? Colors.white : Colors.black)
                        .withOpacity(0.04),
                    child: Text(
                      'Embedded default — preview only. Tap Override to edit.',
                      style: GoogleFonts.spaceGrotesk(
                          fontSize: 12, color: muted),
                    ),
                  ),
                Expanded(
                  child: TextField(
                    controller: _ctrl,
                    readOnly: _readOnly,
                    maxLines: null,
                    expands: true,
                    keyboardType: TextInputType.multiline,
                    textAlignVertical: TextAlignVertical.top,
                    style: GoogleFonts.jetBrainsMono(fontSize: 12),
                    inputFormatters: const [],
                    decoration: const InputDecoration(
                      contentPadding: EdgeInsets.all(16),
                      border: InputBorder.none,
                    ),
                  ),
                ),
              ],
            ),
    );
  }
}

/// _NewFamilyNameDialog enforces the same regex the hub gates on
/// (lowercase, dash-friendly, ≤32 chars). Reject early so a typo
/// doesn't surface as a 400 from the editor screen.
class _NewFamilyNameDialog extends StatefulWidget {
  const _NewFamilyNameDialog();

  @override
  State<_NewFamilyNameDialog> createState() => _NewFamilyNameDialogState();
}

class _NewFamilyNameDialogState extends State<_NewFamilyNameDialog> {
  final _ctrl = TextEditingController();
  static final _nameRe = RegExp(r'^[a-z0-9][a-z0-9-]{0,31}$');
  String? _error;

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('New agent family'),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          TextField(
            controller: _ctrl,
            autofocus: true,
            inputFormatters: [
              FilteringTextInputFormatter.allow(RegExp(r'[a-z0-9-]')),
              LengthLimitingTextInputFormatter(32),
            ],
            decoration: InputDecoration(
              hintText: 'kimi-code',
              labelText: 'Family name',
              errorText: _error,
            ),
          ),
          const SizedBox(height: 8),
          Text(
            'Lowercase letters, digits, and dashes. Must match a CLI binary on PATH.',
            style: GoogleFonts.spaceGrotesk(fontSize: 11, color: Colors.grey),
          ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: () {
            final v = _ctrl.text.trim();
            if (!_nameRe.hasMatch(v)) {
              setState(() => _error = 'Invalid name');
              return;
            }
            Navigator.pop(context, v);
          },
          child: const Text('Create'),
        ),
      ],
    );
  }
}

String _scaffoldYAML(String family) => '''
family: $family
bin: $family
version_flag: --version
supports: [M2, M4]
# Optional: list mode/billing combos this CLI cannot serve, e.g.
# incompatibilities:
#   - mode: M1
#     billing: subscription
#     reason: "vendor SDK requires api_key"
''';

/// Minimal YAML serializer for a single family record. We don't need
/// full YAML emit — the schema is fixed and the editor lets the user
/// hand-edit anyway. This just gives a sane initial body when loading
/// an existing family for edit/preview.
String _yamlFromRecord(Map<String, dynamic> rec) {
  final buf = StringBuffer();
  buf.writeln('family: ${rec['family'] ?? ''}');
  if ((rec['bin'] ?? '').toString().isNotEmpty) {
    buf.writeln('bin: ${rec['bin']}');
  }
  if ((rec['version_flag'] ?? '').toString().isNotEmpty) {
    buf.writeln('version_flag: ${rec['version_flag']}');
  }
  final supports = (rec['supports'] as List?)?.cast<dynamic>() ?? const [];
  if (supports.isNotEmpty) {
    buf.writeln('supports: [${supports.join(', ')}]');
  }
  final incompat = (rec['incompatibilities'] as List?) ?? const [];
  if (incompat.isNotEmpty) {
    buf.writeln('incompatibilities:');
    for (final ic in incompat) {
      final m = (ic as Map).cast<String, dynamic>();
      buf.writeln('  - mode: ${m['mode'] ?? ''}');
      buf.writeln('    billing: ${m['billing'] ?? ''}');
      final reason = (m['reason'] ?? '').toString();
      if (reason.isNotEmpty) {
        buf.writeln('    reason: ${_yamlQuote(reason)}');
      }
    }
  }
  return buf.toString();
}

String _yamlQuote(String s) {
  if (s.contains('"')) {
    return "'${s.replaceAll("'", "''")}'";
  }
  return '"$s"';
}

String _extractField(String yaml, String key) {
  for (final line in yaml.split('\n')) {
    final t = line.trim();
    if (t.startsWith('$key:')) {
      return t.substring(key.length + 1).trim();
    }
  }
  return '';
}

List<String> _extractList(String yaml, String key) {
  final raw = _extractField(yaml, key);
  if (raw.isEmpty) return const [];
  final stripped = raw.replaceAll(RegExp(r'[\[\]]'), '');
  return stripped.split(',').map((e) => e.trim()).where((e) => e.isNotEmpty).toList();
}
