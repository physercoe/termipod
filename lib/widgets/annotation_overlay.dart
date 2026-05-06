import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../providers/hub_provider.dart';
import '../theme/design_colors.dart';

/// ADR-020 W1 — director annotations on a typed-document section.
///
/// Renders the open annotations for one section as a stack of cards, with
/// kind-specific glyphs (comment / redline / suggestion / question), an
/// "Add annotation" affordance, and per-card resolve / reopen toggles.
/// The MVP does not anchor cards to character ranges in the body — the
/// overlay sits *below* the section body, and individual annotations
/// declare their range in their own header line when present.
class AnnotationOverlay extends ConsumerStatefulWidget {
  final String documentId;
  final String sectionSlug;

  /// When non-null, inverts the default to also show resolved annotations
  /// alongside open ones. Useful for the "history" view in section detail.
  final bool showResolved;

  const AnnotationOverlay({
    super.key,
    required this.documentId,
    required this.sectionSlug,
    this.showResolved = false,
  });

  @override
  ConsumerState<AnnotationOverlay> createState() => _AnnotationOverlayState();
}

class _AnnotationOverlayState extends ConsumerState<AnnotationOverlay> {
  bool _loading = true;
  List<Map<String, dynamic>> _items = const [];
  // Non-null when annotations were served from the offline cache.
  DateTime? _staleSince;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void didUpdateWidget(covariant AnnotationOverlay old) {
    super.didUpdateWidget(old);
    if (old.documentId != widget.documentId ||
        old.sectionSlug != widget.sectionSlug ||
        old.showResolved != widget.showResolved) {
      _load();
    }
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      if (mounted) setState(() => _loading = false);
      return;
    }
    setState(() => _loading = true);
    try {
      final out = await client.listAnnotationsCached(
        documentId: widget.documentId,
        section: widget.sectionSlug,
        status: widget.showResolved ? 'all' : 'open',
      );
      if (!mounted) return;
      setState(() {
        _items = out.body;
        _staleSince = out.staleSince;
        _loading = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() => _loading = false);
    }
  }

  Future<void> _addAnnotation() async {
    final result = await showModalBottomSheet<_NewAnnotation>(
      context: context,
      isScrollControlled: true,
      builder: (ctx) => const _AddAnnotationSheet(),
    );
    if (!mounted || result == null) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    final messenger = ScaffoldMessenger.of(context);
    try {
      await client.createAnnotation(
        documentId: widget.documentId,
        sectionSlug: widget.sectionSlug,
        kind: result.kind,
        body: result.body,
      );
      await _load();
    } catch (e) {
      messenger.showSnackBar(SnackBar(content: Text('Failed to add: $e')));
    }
  }

  Future<void> _toggleResolved(Map<String, dynamic> a) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    final messenger = ScaffoldMessenger.of(context);
    final id = (a['id'] ?? '').toString();
    final isOpen = (a['status'] ?? 'open') == 'open';
    try {
      if (isOpen) {
        await client.resolveAnnotation(id);
      } else {
        await client.reopenAnnotation(id);
      }
      await _load();
    } catch (e) {
      messenger.showSnackBar(SnackBar(content: Text('Failed: $e')));
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;

    return Container(
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(12, 10, 4, 6),
            child: Row(
              children: [
                const Icon(Icons.rate_review_outlined,
                    size: 14, color: DesignColors.textMuted),
                const SizedBox(width: 6),
                Text(
                  widget.showResolved ? 'Annotations' : 'Open annotations',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    fontWeight: FontWeight.w700,
                    letterSpacing: 0.3,
                    color: DesignColors.textMuted,
                  ),
                ),
                const Spacer(),
                if (_staleSince != null)
                  Padding(
                    padding: const EdgeInsets.only(right: 8),
                    child: Tooltip(
                      message:
                          'Showing cached annotations · last updated ${_formatHm(_staleSince!)}',
                      child: const Icon(Icons.cloud_off,
                          size: 14, color: DesignColors.warning),
                    ),
                  ),
                TextButton.icon(
                  icon: const Icon(Icons.add, size: 14),
                  label: Text(
                    'Add',
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 11,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  style: TextButton.styleFrom(
                    minimumSize: const Size(0, 28),
                    padding:
                        const EdgeInsets.symmetric(horizontal: 8, vertical: 0),
                    tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                  ),
                  onPressed: _staleSince != null ? null : _addAnnotation,
                ),
              ],
            ),
          ),
          const Divider(height: 1),
          if (_loading)
            const Padding(
              padding: EdgeInsets.all(16),
              child: Center(
                child: SizedBox(
                  width: 18,
                  height: 18,
                  child: CircularProgressIndicator(strokeWidth: 2),
                ),
              ),
            )
          else if (_items.isEmpty)
            Padding(
              padding: const EdgeInsets.all(16),
              child: Center(
                child: Text(
                  widget.showResolved
                      ? 'No annotations yet'
                      : 'No open annotations',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    color: DesignColors.textMuted,
                  ),
                ),
              ),
            )
          else
            for (var i = 0; i < _items.length; i++) ...[
              if (i > 0) const Divider(height: 1, indent: 12, endIndent: 12),
              _AnnotationRow(
                annotation: _items[i],
                onToggleResolved: () => _toggleResolved(_items[i]),
              ),
            ],
        ],
      ),
    );
  }

  static String _formatHm(DateTime t) {
    final l = t.toLocal();
    return '${l.hour.toString().padLeft(2, '0')}:'
        '${l.minute.toString().padLeft(2, '0')}';
  }
}

class _AnnotationRow extends StatelessWidget {
  final Map<String, dynamic> annotation;
  final VoidCallback onToggleResolved;
  const _AnnotationRow({
    required this.annotation,
    required this.onToggleResolved,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final kind = (annotation['kind'] ?? 'comment').toString();
    final body = (annotation['body'] ?? '').toString();
    final status = (annotation['status'] ?? 'open').toString();
    final author = (annotation['author_handle'] ?? '').toString();
    final authorKind = (annotation['author_kind'] ?? '').toString();
    final actorLabel = author.isNotEmpty
        ? '@$author'
        : (authorKind.isNotEmpty ? authorKind : 'system');
    final isResolved = status == 'resolved';
    final glyph = _annotationIcon(kind);
    final color = _annotationColor(kind);
    return InkWell(
      onTap: onToggleResolved,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Padding(
              padding: const EdgeInsets.only(top: 1),
              child: Icon(glyph, size: 16, color: color),
            ),
            const SizedBox(width: 10),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    body,
                    maxLines: 4,
                    overflow: TextOverflow.ellipsis,
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 13,
                      height: 1.35,
                      fontWeight: FontWeight.w500,
                      decoration: kind == 'redline'
                          ? TextDecoration.lineThrough
                          : null,
                      color: isResolved
                          ? DesignColors.textMuted
                          : (isDark ? null : Colors.black87),
                    ),
                  ),
                  const SizedBox(height: 3),
                  Row(
                    children: [
                      Text(
                        '$actorLabel · ${_kindLabel(kind)}',
                        style: GoogleFonts.jetBrainsMono(
                          fontSize: 10,
                          color: isDark
                              ? DesignColors.textMuted
                              : DesignColors.textMutedLight,
                        ),
                      ),
                      const SizedBox(width: 8),
                      if (isResolved)
                        _StatusChip(label: 'resolved', color: color),
                    ],
                  ),
                ],
              ),
            ),
            const SizedBox(width: 6),
            Icon(
              isResolved ? Icons.refresh : Icons.check_circle_outline,
              size: 16,
              color: DesignColors.textMuted,
            ),
          ],
        ),
      ),
    );
  }
}

class _StatusChip extends StatelessWidget {
  final String label;
  final Color color;
  const _StatusChip({required this.label, required this.color});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        label,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 9,
          fontWeight: FontWeight.w600,
          color: color,
        ),
      ),
    );
  }
}

class _NewAnnotation {
  final String kind;
  final String body;
  const _NewAnnotation({required this.kind, required this.body});
}

class _AddAnnotationSheet extends StatefulWidget {
  const _AddAnnotationSheet();
  @override
  State<_AddAnnotationSheet> createState() => _AddAnnotationSheetState();
}

class _AddAnnotationSheetState extends State<_AddAnnotationSheet> {
  String _kind = 'comment';
  final _ctl = TextEditingController();

  @override
  void dispose() {
    _ctl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final media = MediaQuery.of(context);
    return Padding(
      padding: EdgeInsets.only(bottom: media.viewInsets.bottom),
      child: SafeArea(
        top: false,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(16, 16, 16, 12),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                'Add annotation',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 16,
                  fontWeight: FontWeight.w700,
                ),
              ),
              const SizedBox(height: 12),
              Wrap(
                spacing: 8,
                children: [
                  for (final k in const [
                    'comment',
                    'redline',
                    'suggestion',
                    'question',
                  ])
                    ChoiceChip(
                      label: Text(_kindLabel(k)),
                      selected: _kind == k,
                      onSelected: (_) => setState(() => _kind = k),
                    ),
                ],
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _ctl,
                autofocus: true,
                minLines: 3,
                maxLines: 6,
                decoration: const InputDecoration(
                  hintText: 'What needs attention here?',
                  border: OutlineInputBorder(),
                ),
              ),
              const SizedBox(height: 12),
              Row(
                mainAxisAlignment: MainAxisAlignment.end,
                children: [
                  TextButton(
                    onPressed: () => Navigator.of(context).pop(),
                    child: const Text('Cancel'),
                  ),
                  const SizedBox(width: 8),
                  FilledButton.icon(
                    icon: const Icon(Icons.send, size: 16),
                    label: const Text('Post'),
                    onPressed: () {
                      final body = _ctl.text.trim();
                      if (body.isEmpty) return;
                      Navigator.of(context)
                          .pop(_NewAnnotation(kind: _kind, body: body));
                    },
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

IconData _annotationIcon(String kind) {
  switch (kind) {
    case 'redline':
      return Icons.format_strikethrough;
    case 'suggestion':
      return Icons.swap_horiz;
    case 'question':
      return Icons.help_outline;
    default:
      return Icons.chat_bubble_outline;
  }
}

Color _annotationColor(String kind) {
  switch (kind) {
    case 'redline':
      return DesignColors.error;
    case 'suggestion':
      return DesignColors.primary;
    case 'question':
      return DesignColors.warning;
    default:
      return DesignColors.textMuted;
  }
}

String _kindLabel(String kind) {
  switch (kind) {
    case 'redline':
      return 'Redline';
    case 'suggestion':
      return 'Suggestion';
    case 'question':
      return 'Question';
    default:
      return 'Comment';
  }
}
