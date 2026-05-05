import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:google_fonts/google_fonts.dart';

import '../theme/design_colors.dart';

/// W5a — Basic markdown editor used by the Section Detail screen for
/// manual edits. Textarea + simple toolbar (H1/H2/H3 / list / link /
/// code block) per A4 §5.6 — explicitly NOT a rich editor; the
/// underlying body is plain markdown that flutter_markdown renders on
/// the read path.
///
/// Returns the saved body via Navigator.pop, or `null` on cancel.
class MarkdownSectionEditor extends StatefulWidget {
  final String title;
  final String initialBody;
  final String? guidance;

  const MarkdownSectionEditor({
    super.key,
    required this.title,
    required this.initialBody,
    this.guidance,
  });

  static Future<String?> show(
    BuildContext context, {
    required String title,
    required String initialBody,
    String? guidance,
  }) {
    return Navigator.of(context).push<String>(
      MaterialPageRoute(
        fullscreenDialog: true,
        builder: (_) => MarkdownSectionEditor(
          title: title,
          initialBody: initialBody,
          guidance: guidance,
        ),
      ),
    );
  }

  @override
  State<MarkdownSectionEditor> createState() => _MarkdownSectionEditorState();
}

class _MarkdownSectionEditorState extends State<MarkdownSectionEditor> {
  late final TextEditingController _ctrl;
  bool _dirty = false;

  @override
  void initState() {
    super.initState();
    _ctrl = TextEditingController(text: widget.initialBody);
    _ctrl.addListener(_onChange);
  }

  void _onChange() {
    final dirty = _ctrl.text != widget.initialBody;
    if (dirty != _dirty) setState(() => _dirty = dirty);
  }

  @override
  void dispose() {
    _ctrl.removeListener(_onChange);
    _ctrl.dispose();
    super.dispose();
  }

  void _wrap(String prefix, [String suffix = '']) {
    final sel = _ctrl.selection;
    if (!sel.isValid) {
      // Append.
      _ctrl.text = '${_ctrl.text}$prefix$suffix';
      return;
    }
    final t = _ctrl.text;
    final selected = sel.textInside(t);
    final before = sel.textBefore(t);
    final after = sel.textAfter(t);
    final newText = '$before$prefix$selected$suffix$after';
    final caret = before.length + prefix.length + selected.length;
    _ctrl.value = TextEditingValue(
      text: newText,
      selection: TextSelection.collapsed(offset: caret),
    );
  }

  void _prefixLine(String marker) {
    final sel = _ctrl.selection;
    final t = _ctrl.text;
    final caret = sel.isValid ? sel.start : t.length;
    final before = t.substring(0, caret);
    final lineStart = before.lastIndexOf('\n') + 1;
    final newText =
        '${t.substring(0, lineStart)}$marker${t.substring(lineStart)}';
    _ctrl.value = TextEditingValue(
      text: newText,
      selection: TextSelection.collapsed(offset: caret + marker.length),
    );
  }

  void _saveAndPop() {
    Navigator.of(context).pop(_ctrl.text);
  }

  Future<bool> _onWillPop() async {
    if (!_dirty) return true;
    final discard = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        title: const Text('Discard changes?'),
        content: const Text(
          'You have unsaved edits to this section. Discard them?',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Keep editing'),
          ),
          TextButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Discard'),
          ),
        ],
      ),
    );
    return discard == true;
  }

  @override
  Widget build(BuildContext context) {
    return PopScope(
      canPop: false,
      onPopInvokedWithResult: (didPop, _) async {
        if (didPop) return;
        if (await _onWillPop() && mounted) {
          Navigator.of(context).pop();
        }
      },
      child: Scaffold(
        appBar: AppBar(
          title: Text(
            'Edit · ${widget.title}',
            overflow: TextOverflow.ellipsis,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 15,
              fontWeight: FontWeight.w700,
            ),
          ),
          actions: [
            TextButton(
              onPressed: _dirty ? _saveAndPop : null,
              child: Text(
                'Save',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 13,
                  fontWeight: FontWeight.w700,
                  color: _dirty
                      ? DesignColors.primary
                      : DesignColors.textMuted,
                ),
              ),
            ),
          ],
        ),
        body: Column(
          children: [
            if ((widget.guidance ?? '').isNotEmpty)
              Container(
                width: double.infinity,
                padding: const EdgeInsets.symmetric(
                    horizontal: 16, vertical: 10),
                color: DesignColors.surfaceDark.withValues(alpha: 0.4),
                child: Text(
                  widget.guidance!,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    color: DesignColors.textMuted,
                    fontStyle: FontStyle.italic,
                  ),
                ),
              ),
            _Toolbar(
              onH1: () => _prefixLine('# '),
              onH2: () => _prefixLine('## '),
              onH3: () => _prefixLine('### '),
              onList: () => _prefixLine('- '),
              onLink: () => _wrap('[', '](url)'),
              onCode: () => _wrap('\n```\n', '\n```\n'),
              onClipboardPaste: () async {
                final data = await Clipboard.getData('text/plain');
                final t = data?.text ?? '';
                if (t.isEmpty) return;
                _wrap(t);
              },
            ),
            const Divider(height: 1),
            Expanded(
              child: Padding(
                padding: const EdgeInsets.all(12),
                child: TextField(
                  controller: _ctrl,
                  maxLines: null,
                  expands: true,
                  textAlignVertical: TextAlignVertical.top,
                  keyboardType: TextInputType.multiline,
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 13,
                    height: 1.45,
                  ),
                  decoration: const InputDecoration(
                    border: InputBorder.none,
                    hintText: 'Write the section here…',
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _Toolbar extends StatelessWidget {
  final VoidCallback onH1;
  final VoidCallback onH2;
  final VoidCallback onH3;
  final VoidCallback onList;
  final VoidCallback onLink;
  final VoidCallback onCode;
  final VoidCallback onClipboardPaste;
  const _Toolbar({
    required this.onH1,
    required this.onH2,
    required this.onH3,
    required this.onList,
    required this.onLink,
    required this.onCode,
    required this.onClipboardPaste,
  });

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      height: 40,
      child: SingleChildScrollView(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.symmetric(horizontal: 8),
        child: Row(
          children: [
            _Btn(label: 'H1', onTap: onH1),
            _Btn(label: 'H2', onTap: onH2),
            _Btn(label: 'H3', onTap: onH3),
            const SizedBox(width: 8),
            const VerticalDivider(width: 1),
            const SizedBox(width: 8),
            _Btn(label: '•', onTap: onList, tooltip: 'Bulleted list'),
            _Btn(label: 'link', onTap: onLink, tooltip: 'Insert link'),
            _Btn(label: '```', onTap: onCode, tooltip: 'Code block'),
            _Btn(
              label: 'paste',
              onTap: onClipboardPaste,
              tooltip: 'Paste from clipboard',
            ),
          ],
        ),
      ),
    );
  }
}

class _Btn extends StatelessWidget {
  final String label;
  final VoidCallback onTap;
  final String? tooltip;
  const _Btn({required this.label, required this.onTap, this.tooltip});

  @override
  Widget build(BuildContext context) {
    final btn = TextButton(
      onPressed: onTap,
      style: TextButton.styleFrom(
        minimumSize: const Size(36, 32),
        padding: const EdgeInsets.symmetric(horizontal: 8),
      ),
      child: Text(
        label,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 12,
          fontWeight: FontWeight.w700,
          color: DesignColors.primary,
        ),
      ),
    );
    if (tooltip == null) return btn;
    return Tooltip(message: tooltip!, child: btn);
  }
}
