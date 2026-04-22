import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';

/// Read-only browser for team templates (agents / prompts / policies).
/// Hub seeds these on first init from the embedded FS and then the user
/// edits them on the server — the mobile app only surfaces the current
/// state so stewards and directors can see what's in flight without
/// SSHing into the host.
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
    // Group by category; hub sorts them already so preserving order works.
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
            for (final row in entry.value) _TemplateTile(row: row),
          ],
        ],
      ),
    );
  }
}

class _TemplateTile extends StatelessWidget {
  final Map<String, dynamic> row;
  const _TemplateTile({required this.row});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final name = (row['name'] ?? '').toString();
    final size = row['size'] is int ? row['size'] as int : 0;
    final cat = (row['category'] ?? '').toString();
    return ListTile(
      title: Text(name,
          style: GoogleFonts.jetBrainsMono(fontSize: 13)),
      subtitle: Text('${_fmtSize(size)}',
          style: GoogleFonts.jetBrainsMono(fontSize: 11, color: muted)),
      trailing: const Icon(Icons.chevron_right, size: 20),
      onTap: () => Navigator.of(context).push(MaterialPageRoute(
        builder: (_) => TemplateViewerScreen(category: cat, name: name),
      )),
    );
  }

  String _fmtSize(int n) {
    if (n < 1024) return '$n B';
    if (n < 1024 * 1024) return '${(n / 1024).toStringAsFixed(1)} KB';
    return '${(n / 1024 / 1024).toStringAsFixed(1)} MB';
  }
}

/// Read-only body viewer. Template bodies are YAML/markdown/JSON — the
/// endpoint doesn't parse, so we render as selectable mono text. Good
/// enough for a first pass; markdown rendering can come later when
/// someone actually needs it.
class TemplateViewerScreen extends ConsumerStatefulWidget {
  final String category;
  final String name;
  const TemplateViewerScreen({
    super.key,
    required this.category,
    required this.name,
  });

  @override
  ConsumerState<TemplateViewerScreen> createState() =>
      _TemplateViewerScreenState();
}

class _TemplateViewerScreenState extends ConsumerState<TemplateViewerScreen> {
  bool _loading = true;
  String? _error;
  String _body = '';

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final body = await client.getTemplate(widget.category, widget.name);
      if (!mounted) return;
      setState(() {
        _body = body;
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
    return Scaffold(
      appBar: AppBar(
        title: Text(
          widget.name,
          style: GoogleFonts.spaceGrotesk(
              fontSize: 16, fontWeight: FontWeight.w700),
        ),
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : _error != null
              ? Padding(
                  padding: const EdgeInsets.all(24),
                  child: Text(_error!,
                      style: GoogleFonts.jetBrainsMono(
                          color: DesignColors.error, fontSize: 12)),
                )
              : SingleChildScrollView(
                  padding: const EdgeInsets.all(16),
                  child: SelectableText(
                    _body,
                    style: GoogleFonts.jetBrainsMono(
                        fontSize: 12, height: 1.4),
                  ),
                ),
    );
  }
}
