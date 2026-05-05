import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import '../../widgets/section_state_pip.dart';
import 'section_detail_screen.dart';

/// W5a — Structured Document Viewer (A4). Body widget rendered inside
/// `DocumentDetailScreen` when the loaded document carries a non-null
/// `schema_id`. Plain markdown documents fall through to the existing
/// markdown rendering instead.
///
/// Parses `content_inline` as the structured `{schema_version, schema_id,
/// sections[]}` JSON shape declared by `project-phase-schema.md` §4.1
/// and renders one row per section with its state pip, title, and body
/// snippet. Tap → section detail.
///
/// Schema-not-found / parse-failure: fall back to a plain-markdown
/// preview of the raw content + a warning banner (A4 §8). Action bar
/// (direct-steward / edit / ratify) is hidden in fallback.
class StructuredDocumentBody extends ConsumerStatefulWidget {
  final Map<String, dynamic> document;
  final VoidCallback? onChanged;
  const StructuredDocumentBody({
    super.key,
    required this.document,
    this.onChanged,
  });

  @override
  ConsumerState<StructuredDocumentBody> createState() =>
      _StructuredDocumentBodyState();
}

class _StructuredDocumentBodyState
    extends ConsumerState<StructuredDocumentBody> {
  late Map<String, dynamic> _doc = Map<String, dynamic>.from(widget.document);

  @override
  void didUpdateWidget(covariant StructuredDocumentBody old) {
    super.didUpdateWidget(old);
    if (old.document != widget.document) {
      _doc = Map<String, dynamic>.from(widget.document);
    }
  }

  Future<void> _refresh() async {
    final client = ref.read(hubProvider.notifier).client;
    final id = (_doc['id'] ?? '').toString();
    if (client == null || id.isEmpty) return;
    try {
      final fresh = await client.getDocument(id);
      if (!mounted) return;
      setState(() => _doc = fresh);
      widget.onChanged?.call();
    } catch (_) {
      // Surface the prior cached body silently — user already sees it.
    }
  }

  StructuredParseResult _parse() {
    final raw = (_doc['content_inline'] ?? '').toString();
    if (raw.isEmpty) {
      return const StructuredParseResult.empty();
    }
    try {
      final m = jsonDecode(raw);
      if (m is! Map) return StructuredParseResult.fallback(raw);
      final sections = (m['sections'] as List?) ?? const [];
      final parsed = <Map<String, dynamic>>[];
      for (final s in sections) {
        if (s is Map) parsed.add(s.cast<String, dynamic>());
      }
      return StructuredParseResult.ok(
        schemaId: (m['schema_id'] ?? '').toString(),
        schemaVersion: (m['schema_version'] is num)
            ? (m['schema_version'] as num).toInt()
            : 1,
        sections: parsed,
      );
    } catch (_) {
      return StructuredParseResult.fallback(raw);
    }
  }

  @override
  Widget build(BuildContext context) {
    final result = _parse();
    if (result.fallback) {
      return _FallbackPlainBody(rawContent: result.rawFallback);
    }
    final sections = result.sections;
    final ratified = sections
        .where((s) => (s['status'] ?? '') == 'ratified')
        .length;
    final total = sections.length;
    return RefreshIndicator(
      onRefresh: _refresh,
      child: ListView(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
        children: [
          _ProgressStrip(ratified: ratified, total: total),
          const SizedBox(height: 12),
          if (sections.isEmpty)
            const _EmptyDocCard()
          else
            for (var i = 0; i < sections.length; i++) ...[
              _SectionRow(
                section: sections[i],
                onTap: () async {
                  final docId = (_doc['id'] ?? '').toString();
                  if (docId.isEmpty) return;
                  await Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => SectionDetailScreen(
                        documentId: docId,
                        documentTitle: (_doc['title'] ?? '').toString(),
                        slug: (sections[i]['slug'] ?? '').toString(),
                        initialSection: sections[i],
                      ),
                    ),
                  );
                  if (mounted) await _refresh();
                },
              ),
              const SizedBox(height: 8),
            ],
        ],
      ),
    );
  }
}

class StructuredParseResult {
  final bool fallback;
  final String rawFallback;
  final String schemaId;
  final int schemaVersion;
  final List<Map<String, dynamic>> sections;

  const StructuredParseResult.empty()
      : fallback = false,
        rawFallback = '',
        schemaId = '',
        schemaVersion = 1,
        sections = const [];

  const StructuredParseResult.ok({
    required this.schemaId,
    required this.schemaVersion,
    required this.sections,
  })  : fallback = false,
        rawFallback = '';

  const StructuredParseResult.fallback(this.rawFallback)
      : fallback = true,
        schemaId = '',
        schemaVersion = 0,
        sections = const [];
}

class _ProgressStrip extends StatelessWidget {
  final int ratified;
  final int total;
  const _ProgressStrip({required this.ratified, required this.total});

  @override
  Widget build(BuildContext context) {
    final ratio = total == 0 ? 0.0 : ratified / total;
    return Row(
      children: [
        Expanded(
          child: ClipRRect(
            borderRadius: BorderRadius.circular(3),
            child: LinearProgressIndicator(
              value: ratio,
              minHeight: 4,
              backgroundColor: DesignColors.borderDark,
              valueColor:
                  const AlwaysStoppedAnimation(DesignColors.terminalGreen),
            ),
          ),
        ),
        const SizedBox(width: 10),
        Text(
          '$ratified / $total ratified',
          style: GoogleFonts.jetBrainsMono(
            fontSize: 10,
            fontWeight: FontWeight.w600,
            color: DesignColors.textMuted,
          ),
        ),
      ],
    );
  }
}

class _SectionRow extends StatelessWidget {
  final Map<String, dynamic> section;
  final VoidCallback onTap;
  const _SectionRow({required this.section, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final title = (section['title'] ?? section['slug'] ?? '').toString();
    final state = parseSectionState((section['status'] ?? '').toString());
    final body = (section['body'] ?? '').toString();
    final lastAuthored = (section['last_authored_at'] ?? '').toString();
    final snippet = body.isEmpty
        ? 'No content yet — direct the steward or write manually.'
        : _firstLine(body);
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(8),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        decoration: BoxDecoration(
          color:
              isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
          borderRadius: BorderRadius.circular(8),
          border: Border.all(
            color: isDark
                ? DesignColors.borderDark
                : DesignColors.borderLight,
          ),
        ),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Padding(
              padding: const EdgeInsets.only(top: 3),
              child: SectionStatePip(state: state, showLabel: false),
            ),
            const SizedBox(width: 10),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    title,
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 14,
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    snippet,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 12,
                      height: 1.35,
                      color: body.isEmpty
                          ? DesignColors.textMuted
                          : (isDark
                              ? DesignColors.textSecondary
                              : DesignColors.textSecondaryLight),
                      fontStyle:
                          body.isEmpty ? FontStyle.italic : FontStyle.normal,
                    ),
                  ),
                  const SizedBox(height: 4),
                  Row(
                    children: [
                      SectionStatePip(state: state, size: 8),
                      if (lastAuthored.isNotEmpty) ...[
                        const SizedBox(width: 8),
                        Text(
                          '· ${_relativeTs(lastAuthored)}',
                          style: GoogleFonts.jetBrainsMono(
                            fontSize: 9,
                            color: DesignColors.textMuted,
                          ),
                        ),
                      ],
                    ],
                  ),
                ],
              ),
            ),
            const Icon(Icons.chevron_right,
                size: 18, color: DesignColors.textMuted),
          ],
        ),
      ),
    );
  }

  static String _firstLine(String body) {
    final stripped = body
        .split('\n')
        .map((l) => l.trim())
        .firstWhere((l) => l.isNotEmpty, orElse: () => body.trim());
    // Strip leading markdown markers (#, -, *) for the snippet; the
    // index doesn't render markdown.
    return stripped.replaceAll(RegExp(r'^[#\-*]+\s*'), '');
  }

  static String _relativeTs(String raw) {
    final t = DateTime.tryParse(raw);
    if (t == null) return raw;
    final diff = DateTime.now().toUtc().difference(t.toUtc());
    if (diff.inMinutes < 1) return 'now';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m';
    if (diff.inHours < 24) return '${diff.inHours}h';
    if (diff.inDays < 7) return '${diff.inDays}d';
    return '${(diff.inDays / 7).floor()}w';
  }
}

class _FallbackPlainBody extends StatelessWidget {
  final String rawContent;
  const _FallbackPlainBody({required this.rawContent});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
            decoration: BoxDecoration(
              color: DesignColors.warning.withValues(alpha: 0.12),
              borderRadius: BorderRadius.circular(8),
              border: Border.all(
                color: DesignColors.warning.withValues(alpha: 0.5),
              ),
            ),
            child: Row(
              children: [
                const Icon(Icons.warning_amber_outlined,
                    size: 16, color: DesignColors.warning),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    'This document is typed but its body could not be parsed as structured JSON. Showing raw content.',
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 11,
                      color: DesignColors.warning,
                    ),
                  ),
                ),
              ],
            ),
          ),
          const SizedBox(height: 12),
          Expanded(
            child: SingleChildScrollView(
              child: SelectableText(
                rawContent,
                style: GoogleFonts.jetBrainsMono(fontSize: 12, height: 1.4),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _EmptyDocCard extends StatelessWidget {
  const _EmptyDocCard();

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: DesignColors.surfaceDark.withValues(alpha: 0.5),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: DesignColors.borderDark),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'This document has no sections yet.',
            style: GoogleFonts.spaceGrotesk(
              fontSize: 13,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 8),
          Text(
            'Direct the steward or open a section detail to begin authoring.',
            style: GoogleFonts.spaceGrotesk(
              fontSize: 12,
              color: DesignColors.textMuted,
            ),
          ),
        ],
      ),
    );
  }
}
