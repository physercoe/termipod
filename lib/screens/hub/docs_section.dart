import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import 'doc_viewer_screen.dart';

/// Read-only tree-style docs browser for a project. The hub returns a flat
/// list of entries under the project's docs_root; we indent by path depth
/// to give a tree feel without building an actual tree structure.
class DocsSection extends ConsumerStatefulWidget {
  final String projectId;
  const DocsSection({super.key, required this.projectId});

  @override
  ConsumerState<DocsSection> createState() => _DocsSectionState();
}

class _DocsSectionState extends ConsumerState<DocsSection> {
  bool _loading = true;
  String? _error;
  List<Map<String, dynamic>> _entries = const [];

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final rows = await client.listProjectDocs(widget.projectId);
      rows.sort((a, b) =>
          (a['path'] ?? '').toString().compareTo((b['path'] ?? '').toString()));
      if (!mounted) return;
      setState(() {
        _entries = rows;
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
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(_error!,
              style: GoogleFonts.jetBrainsMono(
                  fontSize: 12, color: DesignColors.error)),
        ),
      );
    }
    if (_entries.isEmpty) {
      return _EmptyState(onRefresh: _load);
    }
    return Column(
      children: [
        const _FilesGuidance(),
        Expanded(
          child: RefreshIndicator(
            onRefresh: _load,
            child: ListView.builder(
              padding: const EdgeInsets.fromLTRB(8, 0, 8, 24),
              itemCount: _entries.length,
              itemBuilder: (_, i) => _DocRow(
                entry: _entries[i],
                projectId: widget.projectId,
              ),
            ),
          ),
        ),
      ],
    );
  }
}

/// Role banner for the Files tab. Files are the project's path-keyed
/// input surface — things agents read by name via MCP `get_project_doc`.
/// The copy answers the user's decision question ("Files or Assets?")
/// in two lines: who reads it, and where to put ambient content instead.
class _FilesGuidance extends StatelessWidget {
  const _FilesGuidance();

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Container(
      margin: const EdgeInsets.fromLTRB(12, 8, 12, 8),
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: border),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Icon(Icons.folder_outlined,
              size: 18, color: DesignColors.primary),
          const SizedBox(width: 8),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'Files agents read by path',
                  style: GoogleFonts.spaceGrotesk(
                      fontSize: 12, fontWeight: FontWeight.w700),
                ),
                const SizedBox(height: 2),
                Text(
                  'Trainer code, datasets, configs, prompts. '
                  'For ambient references (screenshots, audio), drop them in a channel.',
                  style: GoogleFonts.spaceGrotesk(fontSize: 11, color: muted),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _DocRow extends StatelessWidget {
  final Map<String, dynamic> entry;
  final String projectId;
  const _DocRow({required this.entry, required this.projectId});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final path = (entry['path'] ?? '').toString();
    final isDir = entry['is_dir'] == true;
    final segments = path.split('/');
    final depth = segments.length - 1;
    final name = segments.isEmpty ? path : segments.last;
    final mutedColor =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;

    return SizedBox(
      child: Padding(
        padding: EdgeInsets.only(left: depth * 16.0),
        child: ListTile(
          dense: true,
          leading: Icon(_iconFor(path, isDir),
              size: 20,
              color: isDir ? DesignColors.primary : mutedColor),
          title: Text(
            name,
            style: GoogleFonts.spaceGrotesk(
                fontSize: 13, fontWeight: FontWeight.w600),
          ),
          subtitle: Text(
            _subtitle(entry, isDir),
            style: GoogleFonts.jetBrainsMono(
              fontSize: 10,
              color: mutedColor,
            ),
          ),
          onTap: isDir
              ? null
              : () {
                  Navigator.of(context).push(MaterialPageRoute(
                    builder: (_) => DocViewerScreen(
                      projectId: projectId,
                      path: path,
                    ),
                  ));
                },
        ),
      ),
    );
  }

  IconData _iconFor(String path, bool isDir) {
    if (isDir) return Icons.folder;
    final lower = path.toLowerCase();
    if (lower.endsWith('.md') || lower.endsWith('.markdown')) {
      return Icons.description;
    }
    if (lower.endsWith('.txt')) return Icons.description;
    if (lower.endsWith('.yaml') ||
        lower.endsWith('.yml') ||
        lower.endsWith('.json')) {
      return Icons.code;
    }
    return Icons.insert_drive_file;
  }

  String _subtitle(Map<String, dynamic> e, bool isDir) {
    if (isDir) {
      final mt = (e['mod_time'] ?? '').toString();
      return mt.length >= 10 ? mt.substring(0, 10) : mt;
    }
    final size = (e['size'] is int) ? e['size'] as int : 0;
    return _formatSize(size);
  }

  String _formatSize(int bytes) {
    if (bytes < 1024) return '$bytes B';
    final kb = bytes / 1024;
    if (kb < 1024) return '${kb.toStringAsFixed(1)} KB';
    final mb = kb / 1024;
    if (mb < 1024) return '${mb.toStringAsFixed(1)} MB';
    final gb = mb / 1024;
    return '${gb.toStringAsFixed(1)} GB';
  }
}

class _EmptyState extends StatelessWidget {
  final Future<void> Function() onRefresh;
  const _EmptyState({required this.onRefresh});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return RefreshIndicator(
      onRefresh: onRefresh,
      child: ListView(
        children: [
          const _FilesGuidance(),
          const SizedBox(height: 48),
          Icon(Icons.folder_open, size: 48, color: muted),
          const SizedBox(height: 12),
          Center(
            child: Text(
              'No files yet.',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 14,
                fontWeight: FontWeight.w600,
                color: muted,
              ),
            ),
          ),
          const SizedBox(height: 6),
          Center(
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 32),
              child: Text(
                'Populate docs_root on the hub host (writes from mobile not yet supported).',
                textAlign: TextAlign.center,
                style: GoogleFonts.jetBrainsMono(fontSize: 11, color: muted),
              ),
            ),
          ),
        ],
      ),
    );
  }
}
