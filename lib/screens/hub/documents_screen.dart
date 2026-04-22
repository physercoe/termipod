import 'package:flutter/material.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';

/// Team-wide documents browser (blueprint §6.7).
///
/// Documents are versioned writeups — memos, drafts, reports, reviews.
/// They're the output of agents (briefings, steward digests) and the
/// input to human reviews. This screen lists them, filters by kind,
/// and opens a per-doc viewer that pairs with the review queue.
class DocumentsScreen extends ConsumerStatefulWidget {
  /// Optional project scope. When null, shows all team documents.
  final String? projectId;
  const DocumentsScreen({super.key, this.projectId});

  @override
  ConsumerState<DocumentsScreen> createState() => _DocumentsScreenState();
}

class _DocumentsScreenState extends ConsumerState<DocumentsScreen> {
  // null = all kinds.
  String? _kind;
  List<Map<String, dynamic>>? _rows;
  bool _loading = true;
  String? _error;

  static const _kinds = <String?>[null, 'memo', 'draft', 'report', 'review'];

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() {
        _loading = false;
        _error = 'Hub not configured.';
      });
      return;
    }
    try {
      final rows = await client.listDocuments(projectId: widget.projectId);
      rows.sort((a, b) => (b['created_at'] ?? '')
          .toString()
          .compareTo((a['created_at'] ?? '').toString()));
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

  List<Map<String, dynamic>> get _filtered {
    final rows = _rows ?? const [];
    if (_kind == null) return rows;
    return rows
        .where((r) => (r['kind'] ?? '').toString() == _kind)
        .toList(growable: false);
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          widget.projectId == null
              ? 'Documents'
              : 'Documents · ${widget.projectId}',
          style: GoogleFonts.spaceGrotesk(
            fontSize: 16,
            fontWeight: FontWeight.w700,
          ),
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh),
            tooltip: 'Refresh',
            onPressed: _loading ? null : _load,
          ),
        ],
      ),
      body: Column(
        children: [
          _KindBar(
            kinds: _kinds,
            selected: _kind,
            onChanged: (v) => setState(() => _kind = v),
          ),
          Expanded(child: _body()),
        ],
      ),
    );
  }

  Widget _body() {
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_error != null) {
      return Padding(
        padding: const EdgeInsets.all(24),
        child: Text(
          _error!,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 12,
            color: DesignColors.error,
          ),
        ),
      );
    }
    final rows = _filtered;
    if (rows.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            _rows == null || _rows!.isEmpty
                ? 'No documents yet.'
                : 'No ${_kind ?? "documents"} match.',
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 13,
              color: DesignColors.textMuted,
            ),
          ),
        ),
      );
    }
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView.separated(
        padding: const EdgeInsets.symmetric(vertical: 4),
        itemCount: rows.length,
        separatorBuilder: (_, __) => const Divider(height: 1),
        itemBuilder: (_, i) => _DocumentRow(
          row: rows[i],
          onTap: () async {
            await Navigator.of(context).push(MaterialPageRoute(
              builder: (_) => DocumentDetailScreen(
                documentId: (rows[i]['id'] ?? '').toString(),
                summary: rows[i],
              ),
            ));
            if (mounted) _load();
          },
        ),
      ),
    );
  }
}

class _KindBar extends StatelessWidget {
  final List<String?> kinds;
  final String? selected;
  final ValueChanged<String?> onChanged;
  const _KindBar({
    required this.kinds,
    required this.selected,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      child: Row(
        children: [
          for (final k in kinds) ...[
            _Pill(
              label: k ?? 'all',
              selected: k == selected,
              onTap: () => onChanged(k),
            ),
            const SizedBox(width: 6),
          ],
        ],
      ),
    );
  }
}

class _Pill extends StatelessWidget {
  final String label;
  final bool selected;
  final VoidCallback onTap;
  const _Pill({
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

class _DocumentRow extends StatelessWidget {
  final Map<String, dynamic> row;
  final VoidCallback onTap;
  const _DocumentRow({required this.row, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final kind = (row['kind'] ?? '').toString();
    final title = (row['title'] ?? '(untitled)').toString();
    final version = (row['version'] ?? 1).toString();
    final project = (row['project_id'] ?? '').toString();
    final author = (row['author_agent_id'] ?? '').toString();
    final created = (row['created_at'] ?? '').toString();
    return ListTile(
      onTap: onTap,
      title: Row(
        children: [
          DocKindChip(kind: kind),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              title,
              overflow: TextOverflow.ellipsis,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 13,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
          Text(
            'v$version',
            style: GoogleFonts.jetBrainsMono(
              fontSize: 10,
              color: DesignColors.textMuted,
            ),
          ),
        ],
      ),
      subtitle: Padding(
        padding: const EdgeInsets.only(top: 2),
        child: Text(
          [
            if (project.isNotEmpty) project,
            if (author.isNotEmpty) author,
            if (created.isNotEmpty) created,
          ].join(' · '),
          style: GoogleFonts.jetBrainsMono(
            fontSize: 10,
            color: DesignColors.textMuted,
          ),
        ),
      ),
      trailing: const Icon(Icons.chevron_right, size: 20),
    );
  }
}

class DocKindChip extends StatelessWidget {
  final String kind;
  const DocKindChip({super.key, required this.kind});

  @override
  Widget build(BuildContext context) {
    final k = kind.toLowerCase();
    final color = switch (k) {
      'memo' => DesignColors.terminalCyan,
      'draft' => DesignColors.textMuted,
      'report' => DesignColors.terminalBlue,
      'review' => DesignColors.warning,
      _ => DesignColors.textMuted,
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: color.withValues(alpha: 0.5)),
      ),
      child: Text(
        k.isEmpty ? '?' : k,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
          fontWeight: FontWeight.w700,
          color: color,
        ),
      ),
    );
  }
}

/// Read-only viewer for a document with version history and "Request
/// review" action. Inline markdown renders through flutter_markdown;
/// artifact-backed docs show a placeholder pointer since the bytes live
/// on the host (blueprint §6.7: metadata in hub, bytes stay put).
class DocumentDetailScreen extends ConsumerStatefulWidget {
  final String documentId;
  final Map<String, dynamic>? summary;
  const DocumentDetailScreen({
    super.key,
    required this.documentId,
    this.summary,
  });

  @override
  ConsumerState<DocumentDetailScreen> createState() =>
      _DocumentDetailScreenState();
}

class _DocumentDetailScreenState extends ConsumerState<DocumentDetailScreen> {
  Map<String, dynamic>? _doc;
  List<Map<String, dynamic>>? _versions;
  bool _loading = true;
  String? _error;
  bool _requestingReview = false;

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
      final doc = await client.getDocument(widget.documentId);
      List<Map<String, dynamic>>? vs;
      try {
        vs = await client.listDocumentVersions(widget.documentId);
      } catch (_) {
        // Version endpoint optional — some doc ids may be the only version.
      }
      if (!mounted) return;
      setState(() {
        _doc = doc;
        _versions = vs;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = '$e';
        _loading = false;
      });
    }
  }

  Future<void> _requestReview() async {
    final result = await showDialog<_ReviewRequest>(
      context: context,
      builder: (_) => const _RequestReviewDialog(),
    );
    if (result == null) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() => _requestingReview = true);
    try {
      await client.createReview(
        documentId: widget.documentId,
        reviewerHandle:
            result.reviewer.isEmpty ? null : result.reviewer,
        note: result.note.isEmpty ? null : result.note,
      );
      if (!mounted) return;
      setState(() => _requestingReview = false);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Review requested')),
      );
    } catch (e) {
      if (!mounted) return;
      setState(() => _requestingReview = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Request failed: $e')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final d = _doc ?? widget.summary ?? const <String, dynamic>{};
    final title = (d['title'] ?? '(document)').toString();
    final kind = (d['kind'] ?? '').toString();
    return Scaffold(
      appBar: AppBar(
        title: Row(
          children: [
            DocKindChip(kind: kind),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                title,
                overflow: TextOverflow.ellipsis,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 15,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ),
          ],
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.rate_review_outlined),
            tooltip: 'Request review',
            onPressed:
                _loading || _requestingReview ? null : _requestReview,
          ),
        ],
      ),
      body: _body(d),
    );
  }

  Widget _body(Map<String, dynamic> d) {
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
      return Padding(
        padding: const EdgeInsets.all(16),
        child: Text(
          _error!,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 12,
            color: DesignColors.error,
          ),
        ),
      );
    }
    final content = (d['content_inline'] ?? '').toString();
    final artifactId = (d['artifact_id'] ?? '').toString();
    final version = (d['version'] ?? 1).toString();
    final project = (d['project_id'] ?? '').toString();
    final author = (d['author_agent_id'] ?? '').toString();
    final created = (d['created_at'] ?? '').toString();

    return RefreshIndicator(
      onRefresh: _load,
      child: ListView(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
        children: [
          _metaRow('version', 'v$version'),
          if (project.isNotEmpty) _metaRow('project', project),
          if (author.isNotEmpty) _metaRow('author', author),
          if (created.isNotEmpty) _metaRow('created', created),
          const SizedBox(height: 14),
          _sectionLabel('Content'),
          if (content.isNotEmpty)
            _ContentBody(body: content)
          else if (artifactId.isNotEmpty)
            _ArtifactPointer(artifactId: artifactId)
          else
            Text(
              '(no content)',
              style: GoogleFonts.jetBrainsMono(
                fontSize: 11,
                color: DesignColors.textMuted,
              ),
            ),
          if ((_versions ?? const []).length > 1) ...[
            const SizedBox(height: 20),
            _sectionLabel('Version history'),
            ...(_versions!..sort((a, b) =>
                    ((b['version'] ?? 0) as num)
                        .compareTo((a['version'] ?? 0) as num)))
                .map(_versionRow),
          ],
        ],
      ),
    );
  }

  Widget _versionRow(Map<String, dynamic> v) {
    final version = (v['version'] ?? 1).toString();
    final created = (v['created_at'] ?? '').toString();
    final author = (v['author_agent_id'] ?? '').toString();
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Text(
        'v$version · ${[
          if (author.isNotEmpty) author,
          if (created.isNotEmpty) created,
        ].join(' · ')}',
        style: GoogleFonts.jetBrainsMono(
          fontSize: 11,
          color: DesignColors.textMuted,
        ),
      ),
    );
  }

  Widget _metaRow(String k, String v) => Padding(
        padding: const EdgeInsets.only(bottom: 4),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            SizedBox(
              width: 80,
              child: Text(
                k,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  color: DesignColors.textMuted,
                ),
              ),
            ),
            Expanded(
              child: SelectableText(
                v,
                style: GoogleFonts.jetBrainsMono(fontSize: 11),
              ),
            ),
          ],
        ),
      );

  Widget _sectionLabel(String label) => Padding(
        padding: const EdgeInsets.only(bottom: 6),
        child: Text(
          label,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 11,
            fontWeight: FontWeight.w700,
            color: DesignColors.textMuted,
            letterSpacing: 0.5,
          ),
        ),
      );
}

class _ContentBody extends StatelessWidget {
  final String body;
  const _ContentBody({required this.body});

  @override
  Widget build(BuildContext context) {
    // Heuristic: anything with markdown heading/list/code markers renders as
    // markdown. Plain text falls back to mono so whitespace is preserved.
    final looksMd = RegExp(r'(^|\n)(#|- |\* |\d+\. |```)').hasMatch(body);
    if (looksMd) {
      return MarkdownBody(
        data: body,
        selectable: true,
        styleSheet: MarkdownStyleSheet(
          p: GoogleFonts.spaceGrotesk(fontSize: 13, height: 1.4),
          code: GoogleFonts.jetBrainsMono(fontSize: 12),
        ),
      );
    }
    return Container(
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: Colors.black.withValues(alpha: 0.25),
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: DesignColors.borderDark),
      ),
      child: SelectableText(
        body,
        style: GoogleFonts.jetBrainsMono(fontSize: 12, height: 1.4),
      ),
    );
  }
}

class _ArtifactPointer extends StatelessWidget {
  final String artifactId;
  const _ArtifactPointer({required this.artifactId});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: DesignColors.warning.withValues(alpha: 0.1),
        borderRadius: BorderRadius.circular(6),
        border: Border.all(
          color: DesignColors.warning.withValues(alpha: 0.5),
        ),
      ),
      child: Row(
        children: [
          const Icon(Icons.link, size: 16, color: DesignColors.warning),
          const SizedBox(width: 8),
          Expanded(
            child: SelectableText(
              'Stored as artifact $artifactId — bytes live on host, not in hub.',
              style: GoogleFonts.jetBrainsMono(fontSize: 11),
            ),
          ),
        ],
      ),
    );
  }
}

class _ReviewRequest {
  final String reviewer;
  final String note;
  const _ReviewRequest(this.reviewer, this.note);
}

class _RequestReviewDialog extends StatefulWidget {
  const _RequestReviewDialog();

  @override
  State<_RequestReviewDialog> createState() => _RequestReviewDialogState();
}

class _RequestReviewDialogState extends State<_RequestReviewDialog> {
  final _reviewer = TextEditingController();
  final _note = TextEditingController();

  @override
  void dispose() {
    _reviewer.dispose();
    _note.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Request review'),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          TextField(
            controller: _reviewer,
            decoration: const InputDecoration(
              labelText: 'Reviewer handle (optional)',
              hintText: 'e.g. @director',
            ),
          ),
          const SizedBox(height: 8),
          TextField(
            controller: _note,
            minLines: 2,
            maxLines: 4,
            decoration: const InputDecoration(
              labelText: 'Note (optional)',
              border: OutlineInputBorder(),
            ),
          ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: () => Navigator.pop(
            context,
            _ReviewRequest(
              _reviewer.text.trim(),
              _note.text.trim(),
            ),
          ),
          child: const Text('Request'),
        ),
      ],
    );
  }
}
