import 'package:flutter/material.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../services/hub/hub_client.dart';
import '../../theme/design_colors.dart';
import '../../widgets/markdown_section_editor.dart';
import '../../widgets/section_state_pip.dart';

/// W5a — Section detail screen (A4 §5). Per-section markdown body +
/// sticky action bar (Edit / Ratify / Unratify).
///
/// State transitions handled via the hub's section endpoints
/// (`PATCH /sections/{slug}` and `POST /sections/{slug}/status`); the
/// screen reloads the document after each mutation so the pip and
/// snippet stay in sync.
class SectionDetailScreen extends ConsumerStatefulWidget {
  final String documentId;
  final String documentTitle;
  final String slug;
  final Map<String, dynamic> initialSection;

  const SectionDetailScreen({
    super.key,
    required this.documentId,
    required this.documentTitle,
    required this.slug,
    required this.initialSection,
  });

  @override
  ConsumerState<SectionDetailScreen> createState() =>
      _SectionDetailScreenState();
}

class _SectionDetailScreenState extends ConsumerState<SectionDetailScreen> {
  late Map<String, dynamic> _section =
      Map<String, dynamic>.from(widget.initialSection);
  bool _busy = false;

  String get _body => (_section['body'] ?? '').toString();
  SectionState get _state =>
      parseSectionState((_section['status'] ?? '').toString());
  String? get _lastAuthored {
    final t = (_section['last_authored_at'] ?? '').toString();
    return t.isEmpty ? null : t;
  }

  Future<void> _edit() async {
    final messenger = ScaffoldMessenger.of(context);
    final next = await MarkdownSectionEditor.show(
      context,
      title: (_section['title'] ?? widget.slug).toString(),
      initialBody: _body,
    );
    if (!mounted || next == null || next == _body) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() => _busy = true);
    try {
      final updated = await client.patchDocumentSection(
        documentId: widget.documentId,
        slug: widget.slug,
        body: next,
        expectedLastAuthoredAt: _lastAuthored,
      );
      if (!mounted) return;
      setState(() {
        _section = updated;
        _busy = false;
      });
    } on HubApiError catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      if (e.status == 412) {
        messenger.showSnackBar(
          const SnackBar(
            content: Text(
              'This section was edited elsewhere. Reload to see the latest.',
            ),
          ),
        );
        return;
      }
      messenger.showSnackBar(SnackBar(content: Text('Save failed: $e')));
    } catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      messenger.showSnackBar(SnackBar(content: Text('Save failed: $e')));
    }
  }

  Future<void> _setStatus(String status) async {
    final messenger = ScaffoldMessenger.of(context);
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() => _busy = true);
    try {
      final updated = await client.setDocumentSectionStatus(
        documentId: widget.documentId,
        slug: widget.slug,
        status: status,
      );
      if (!mounted) return;
      setState(() {
        _section = updated;
        _busy = false;
      });
    } on HubApiError catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      messenger.showSnackBar(
        SnackBar(content: Text('Status change failed: ${e.message}')),
      );
    } catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      messenger.showSnackBar(
        SnackBar(content: Text('Status change failed: $e')),
      );
    }
  }

  Future<void> _confirmUnratify() async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        title: const Text('Unratify section?'),
        content: const Text(
          'This moves the section back to draft. Director-only gesture.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Unratify'),
          ),
        ],
      ),
    );
    if (ok == true) await _setStatus('draft');
  }

  @override
  Widget build(BuildContext context) {
    final title = (_section['title'] ?? widget.slug).toString();
    return Scaffold(
      appBar: AppBar(
        title: Row(
          children: [
            Flexible(
              child: Text(
                title,
                overflow: TextOverflow.ellipsis,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 15,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ),
            const SizedBox(width: 8),
            SectionStatePip(state: _state),
          ],
        ),
      ),
      body: Stack(
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 12, 16, 80),
            child: _bodyView(),
          ),
          Positioned(
            left: 0,
            right: 0,
            bottom: 0,
            child: _ActionBar(
              state: _state,
              busy: _busy,
              onEdit: _busy ? null : _edit,
              onRatify: _busy ||
                      _state == SectionState.empty ||
                      _state == SectionState.ratified
                  ? null
                  : () => _setStatus('ratified'),
              onUnratify: _busy || _state != SectionState.ratified
                  ? null
                  : _confirmUnratify,
            ),
          ),
        ],
      ),
    );
  }

  Widget _bodyView() {
    if (_state == SectionState.empty) {
      return Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            width: double.infinity,
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
                  "This section hasn't been authored yet.",
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 13,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const SizedBox(height: 8),
                Text(
                  'Write content manually with the editor below, or direct the project steward from the strip on the project Overview.',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    color: DesignColors.textMuted,
                  ),
                ),
              ],
            ),
          ),
          const SizedBox(height: 16),
        ],
      );
    }
    return Markdown(
      data: _body,
      selectable: true,
      shrinkWrap: false,
      padding: EdgeInsets.zero,
      styleSheet: MarkdownStyleSheet.fromTheme(Theme.of(context)).copyWith(
        p: GoogleFonts.spaceGrotesk(fontSize: 14, height: 1.5),
        code: GoogleFonts.jetBrainsMono(fontSize: 12),
      ),
    );
  }
}

class _ActionBar extends StatelessWidget {
  final SectionState state;
  final bool busy;
  final VoidCallback? onEdit;
  final VoidCallback? onRatify;
  final VoidCallback? onUnratify;
  const _ActionBar({
    required this.state,
    required this.busy,
    required this.onEdit,
    required this.onRatify,
    required this.onUnratify,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Container(
      padding: EdgeInsets.fromLTRB(
        12,
        10,
        12,
        10 + MediaQuery.of(context).padding.bottom,
      ),
      decoration: BoxDecoration(
        color:
            isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        border: Border(
          top: BorderSide(
            color: isDark
                ? DesignColors.borderDark
                : DesignColors.borderLight,
          ),
        ),
      ),
      child: Row(
        children: [
          Expanded(
            child: OutlinedButton.icon(
              icon: const Icon(Icons.edit_outlined, size: 16),
              label: Text(state == SectionState.empty ? 'Write' : 'Edit'),
              onPressed: onEdit,
            ),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: state == SectionState.ratified
                ? OutlinedButton.icon(
                    icon: const Icon(Icons.undo, size: 16),
                    label: const Text('Unratify'),
                    style: OutlinedButton.styleFrom(
                      foregroundColor: DesignColors.warning,
                    ),
                    onPressed: onUnratify,
                  )
                : ElevatedButton.icon(
                    icon: const Icon(Icons.check_circle_outline, size: 16),
                    label: const Text('Ratify'),
                    style: ElevatedButton.styleFrom(
                      backgroundColor: DesignColors.terminalGreen,
                      foregroundColor: Colors.white,
                    ),
                    onPressed: onRatify,
                  ),
          ),
          if (busy) ...[
            const SizedBox(width: 12),
            const SizedBox(
              width: 18,
              height: 18,
              child: CircularProgressIndicator(strokeWidth: 2),
            ),
          ],
        ],
      ),
    );
  }
}
