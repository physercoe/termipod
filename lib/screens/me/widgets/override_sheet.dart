import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:termipod/l10n/app_localizations.dart';

import '../../../providers/hub_provider.dart';
import '../../../theme/design_colors.dart';
import '../../../theme/tokens.dart';
import 'propose_card_visuals.dart';

/// ADR-030 W20 — confirmation sheet for principal-override.
///
/// Replaces the inline AlertDialog placeholder shipped with W15 in
/// [StalledProposeActions]. The sheet matches the D-8 design intent
/// from ADR-030: show the principal what they're overriding (kind +
/// change_spec preview), require a reason, then POST decide with
/// `override=true`. The hub's W9 path runs the per-kind Rollback +
/// emits the override audit row + fans back to the original proposer.
///
/// Use:
/// ```dart
/// final reason = await showOverrideSheet(
///   context,
///   attention: row,
/// );
/// if (reason == null) return;
/// // hub.decide(id, 'override', reason: reason, override: true)
/// ```
///
/// The sheet handles the decide call internally — it returns `true`
/// on success, `false` on cancellation. Errors surface via a snack
/// inside the sheet; the caller doesn't need to handle them.
Future<bool> showOverrideSheet(
  BuildContext context, {
  required Map<String, dynamic> attention,
}) async {
  final result = await showModalBottomSheet<bool>(
    context: context,
    isScrollControlled: true,
    backgroundColor: Theme.of(context).scaffoldBackgroundColor,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(12)),
    ),
    builder: (ctx) => Padding(
      // Push the sheet above the keyboard.
      padding: EdgeInsets.only(
        bottom: MediaQuery.of(ctx).viewInsets.bottom,
      ),
      child: _OverrideSheetBody(attention: attention),
    ),
  );
  return result == true;
}

class _OverrideSheetBody extends ConsumerStatefulWidget {
  final Map<String, dynamic> attention;
  const _OverrideSheetBody({required this.attention});

  @override
  ConsumerState<_OverrideSheetBody> createState() => _OverrideSheetBodyState();
}

class _OverrideSheetBodyState extends ConsumerState<_OverrideSheetBody> {
  final TextEditingController _reasonCtrl = TextEditingController();
  bool _submitting = false;
  String? _error;

  @override
  void dispose() {
    _reasonCtrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final isDark = theme.brightness == Brightness.dark;
    final l10n = AppLocalizations.of(context)!;
    final mutedColor =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final changeKind =
        (widget.attention['change_kind'] ?? '').toString();
    final addressee =
        (widget.attention['assigned_tier'] ?? '').toString();
    final changeSpec = decodeJsonObject(widget.attention['change_spec']);
    final targetRef = decodeJsonObject(widget.attention['target_ref']);

    return SafeArea(
      top: false,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 16, 16, 16),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Drag handle.
            Center(
              child: Container(
                width: 40,
                height: 4,
                decoration: BoxDecoration(
                  color: mutedColor.withValues(alpha: 0.4),
                  borderRadius: Radii.xsBorder,
                ),
              ),
            ),
            const SizedBox(height: 16),
            Row(
              children: [
                Icon(Icons.gavel,
                    size: 18,
                    color: isDark
                        ? DesignColors.onWarningContainer
                        : DesignColors.onWarningContainerLight),
                const SizedBox(width: 8),
                Text(
                  'Override decision',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 16,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 8),
            Text(
              'You are about to override the decision originally '
              'addressed to ${addressee.isEmpty ? "the assignee" : "@$addressee"}. '
              'The system will run the per-kind rollback (per ADR-030 W9) '
              'and emit an override audit row. The original assignee will '
              'no longer be able to decide this attention.',
              style: GoogleFonts.jetBrainsMono(
                fontSize: 11,
                color: mutedColor,
              ),
            ),
            const SizedBox(height: 12),
            // Context block — what's being overridden.
            Container(
              padding: const EdgeInsets.all(Spacing.s8),
              decoration: BoxDecoration(
                color: isDark ? DesignColors.canvasDark : DesignColors.canvasLight,
                borderRadius: Radii.smBorder,
              ),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    'change_kind: $changeKind',
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 11,
                      fontWeight: FontWeight.w600,
                      color: isDark ? Colors.white : Colors.black87,
                    ),
                  ),
                  if (changeSpec.isNotEmpty) ...[
                    const SizedBox(height: 4),
                    Text(
                      'change_spec: ${_compactJson(changeSpec)}',
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: FontSizes.label,
                        color: mutedColor,
                      ),
                      maxLines: 3,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ],
                  if (targetRef.isNotEmpty) ...[
                    const SizedBox(height: 2),
                    Text(
                      'target_ref: ${_compactJson(targetRef)}',
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: FontSizes.label,
                        color: mutedColor,
                      ),
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ],
                ],
              ),
            ),
            const SizedBox(height: 14),
            TextField(
              controller: _reasonCtrl,
              autofocus: true,
              maxLines: 3,
              enabled: !_submitting,
              decoration: InputDecoration(
                labelText: l10n.fieldReason,
                hintText:
                    'Why are you overriding the addressee\'s decision?',
                border: OutlineInputBorder(),
                isDense: true,
              ),
              onChanged: (_) {
                if (_error != null) {
                  setState(() => _error = null);
                }
              },
            ),
            if (_error != null) ...[
              const SizedBox(height: 6),
              Text(
                _error!,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: FontSizes.label,
                  color: DesignColors.error,
                ),
              ),
            ],
            const SizedBox(height: 16),
            Row(
              mainAxisAlignment: MainAxisAlignment.end,
              children: [
                TextButton(
                  onPressed:
                      _submitting ? null : () => Navigator.pop(context, false),
                  child: Text(l10n.buttonCancel),
                ),
                const SizedBox(width: 8),
                FilledButton.icon(
                  icon: _submitting
                      ? const SizedBox(
                          width: 14,
                          height: 14,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      : const Icon(Icons.gavel, size: 16),
                  label: Text(l10n.buttonOverride),
                  onPressed: _submitting ? null : _submit,
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  Future<void> _submit() async {
    final reason = _reasonCtrl.text.trim();
    if (reason.isEmpty) {
      setState(() => _error = 'A reason is required to override.');
      return;
    }
    final id = (widget.attention['id'] ?? '').toString();
    if (id.isEmpty) {
      setState(() => _error = 'Attention id missing — cannot submit.');
      return;
    }
    setState(() {
      _submitting = true;
      _error = null;
    });
    try {
      await ref.read(hubProvider.notifier).decide(
            id,
            'override',
            by: '@principal',
            reason: reason,
            override: true,
          );
      if (!mounted) return;
      Navigator.pop(context, true);
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _submitting = false;
        _error = '$e';
      });
    }
  }

  static String _compactJson(Map<String, dynamic> m) {
    // Single-line forensic preview. Not pretty-printed because the
    // sheet has limited vertical space; full payload is on the
    // Details screen.
    final entries = m.entries
        .map((e) => '${e.key}: ${_renderValue(e.value)}')
        .join(', ');
    return '{$entries}';
  }

  static String _renderValue(dynamic v) {
    if (v is String) return '"$v"';
    if (v is Map || v is List) return v.toString();
    return v.toString();
  }
}
