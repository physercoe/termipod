import 'package:flutter/material.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';

/// Full-screen read-only viewer for a single project doc. Markdown files
/// render via flutter_markdown; anything else falls back to monospace
/// selectable text so code/config files stay copyable.
class DocViewerScreen extends ConsumerStatefulWidget {
  final String projectId;
  final String path;
  const DocViewerScreen({
    super.key,
    required this.projectId,
    required this.path,
  });

  @override
  ConsumerState<DocViewerScreen> createState() => _DocViewerScreenState();
}

class _DocViewerScreenState extends ConsumerState<DocViewerScreen> {
  bool _loading = true;
  String? _error;
  String _content = '';

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final body = await client.getProjectDoc(widget.projectId, widget.path);
      if (!mounted) return;
      setState(() {
        _content = body;
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
    final segments = widget.path.split('/');
    final title = segments.isEmpty ? widget.path : segments.last;
    return Scaffold(
      appBar: AppBar(
        title: Text(
          title,
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
              : _body(),
    );
  }

  Widget _body() {
    final lower = widget.path.toLowerCase();
    final isMarkdown = lower.endsWith('.md') || lower.endsWith('.markdown');
    if (isMarkdown) {
      return SingleChildScrollView(
        padding: const EdgeInsets.all(16),
        child: MarkdownBody(data: _content, selectable: true),
      );
    }
    return SingleChildScrollView(
      padding: const EdgeInsets.all(16),
      child: SelectableText(
        _content,
        style: GoogleFonts.jetBrainsMono(fontSize: 12, height: 1.4),
      ),
    );
  }
}
