import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../providers/hub_provider.dart';
import '../theme/design_colors.dart';

/// Modal sheet that surfaces the project's underlying template YAML
/// (W3 — resolves the C1 §11.1 prep gap). The chassis-vs-template seam
/// is the demo's recurring beat: the sheet opens at three checkpoints
/// (idea, method, experiment) so the audience sees that everything
/// they just watched the steward do is declared by data, not code.
///
/// Loads `GET /v1/teams/{team}/templates/projects/{name}.yaml` on
/// open. Empty / missing template renders an explanatory placeholder.
class TemplateYamlSheet extends ConsumerStatefulWidget {
  final String templateId;

  const TemplateYamlSheet({super.key, required this.templateId});

  static Future<void> show(BuildContext context, String templateId) {
    return showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.transparent,
      builder: (_) => TemplateYamlSheet(templateId: templateId),
    );
  }

  @override
  ConsumerState<TemplateYamlSheet> createState() =>
      _TemplateYamlSheetState();
}

class _TemplateYamlSheetState extends ConsumerState<TemplateYamlSheet> {
  String? _yaml;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    final tpl = widget.templateId.trim();
    if (client == null || tpl.isEmpty) {
      if (mounted) setState(() => _error = 'No template id on this project.');
      return;
    }
    try {
      final body = await client.getProjectTemplateYaml(tpl);
      if (!mounted) return;
      setState(() => _yaml = body);
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = e.toString());
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return DraggableScrollableSheet(
      initialChildSize: 0.7,
      minChildSize: 0.3,
      maxChildSize: 0.95,
      builder: (context, scroll) => Container(
        decoration: BoxDecoration(
          color: isDark
              ? DesignColors.surfaceDark
              : DesignColors.surfaceLight,
          borderRadius: const BorderRadius.vertical(top: Radius.circular(16)),
        ),
        child: Column(
          children: [
            Container(
              width: 36,
              height: 4,
              margin: const EdgeInsets.symmetric(vertical: 8),
              decoration: BoxDecoration(
                color: DesignColors.textMuted.withValues(alpha: 0.4),
                borderRadius: BorderRadius.circular(2),
              ),
            ),
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
              child: Row(
                children: [
                  const Icon(Icons.description_outlined,
                      size: 16, color: DesignColors.primary),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(
                      'Template · ${widget.templateId}',
                      style: GoogleFonts.spaceGrotesk(
                        fontSize: 14,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                  ),
                  if (_yaml != null)
                    IconButton(
                      icon: const Icon(Icons.copy, size: 18),
                      tooltip: 'Copy YAML',
                      onPressed: () async {
                        await Clipboard.setData(
                            ClipboardData(text: _yaml!));
                        if (mounted) {
                          ScaffoldMessenger.of(context).showSnackBar(
                            const SnackBar(content: Text('YAML copied')),
                          );
                        }
                      },
                    ),
                ],
              ),
            ),
            const Divider(height: 1),
            Expanded(
              child: _body(scroll, isDark),
            ),
          ],
        ),
      ),
    );
  }

  Widget _body(ScrollController scroll, bool isDark) {
    if (_error != null) {
      return Padding(
        padding: const EdgeInsets.all(24),
        child: Center(
          child: Text(
            _error!,
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(color: DesignColors.terminalRed),
          ),
        ),
      );
    }
    if (_yaml == null) {
      return const Center(child: CircularProgressIndicator());
    }
    return SingleChildScrollView(
      controller: scroll,
      padding: const EdgeInsets.all(16),
      child: SelectableText(
        _yaml!,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 12,
          height: 1.4,
          color: isDark
              ? DesignColors.textPrimary
              : DesignColors.textPrimaryLight,
        ),
      ),
    );
  }
}
