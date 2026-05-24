import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../../theme/design_colors.dart';
import 'propose_addressee.dart';
import 'propose_card_actions.dart';
import 'propose_card_visuals.dart';

/// ADR-030 W18 — per-kind propose card for `template.install`.
///
/// template.install's change_spec carries:
/// - category (prompts / agents / plans / families)
/// - name (filename relative to category)
/// - blob_sha256 (the proposed body blob — fetched separately)
/// - rationale (free text)
/// - proposed_by (handle)
///
/// The card surfaces `<category>/<name>` as the at-a-glance signal,
/// rationale below, and a sha256 short prefix as forensic context.
/// The full YAML body lives behind the legacy template-proposal
/// preview block (v1.0.602) accessible via Details — the card doesn't
/// re-render it here since the body is a blob fetch and the propose
/// card flow is intentionally lightweight.
class ProposeCardTemplateInstall extends ConsumerWidget {
  final Map<String, dynamic> attention;
  final String myTier;
  final VoidCallback? onResolved;

  const ProposeCardTemplateInstall({
    super.key,
    required this.attention,
    this.myTier = 'principal',
    this.onResolved,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final isDark = theme.brightness == Brightness.dark;
    final isAddressee = isAddresseeOfPropose(attention, myTier);
    final stalled = isStalledPropose(attention);

    final changeSpec = decodeJsonObject(attention['change_spec']);
    final category = (changeSpec['category'] ?? '').toString();
    final name = (changeSpec['name'] ?? '').toString();
    final blobSha = (changeSpec['blob_sha256'] ?? '').toString();
    final rationale = (changeSpec['rationale'] ?? '').toString();
    final proposedBy = (changeSpec['proposed_by'] ?? '').toString();
    final addressee = (attention['assigned_tier'] ?? '').toString();
    final id = (attention['id'] ?? '').toString();

    final mutedColor =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final pathLabel = category.isEmpty || name.isEmpty
        ? '(unknown)'
        : '$category/$name';

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (stalled && !isAddressee) StalledPill(addressee: addressee),
        if (stalled && !isAddressee) const SizedBox(height: 6),
        // Header: path as the primary signal.
        Row(
          children: [
            Icon(Icons.description, size: 14, color: mutedColor),
            const SizedBox(width: 4),
            Expanded(
              child: Text(
                pathLabel,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                  color: isDark ? Colors.white : Colors.black87,
                ),
                overflow: TextOverflow.ellipsis,
              ),
            ),
          ],
        ),
        if (rationale.isNotEmpty) ...[
          const SizedBox(height: 6),
          Text(
            rationale,
            style: GoogleFonts.jetBrainsMono(fontSize: 11, color: mutedColor),
          ),
        ],
        if (proposedBy.isNotEmpty) ...[
          const SizedBox(height: 4),
          Text(
            'proposed by: $proposedBy',
            style: GoogleFonts.jetBrainsMono(fontSize: 10, color: mutedColor),
          ),
        ],
        if (blobSha.isNotEmpty) ...[
          const SizedBox(height: 2),
          Text(
            'sha256: ${_shortSha(blobSha)}',
            style: GoogleFonts.jetBrainsMono(fontSize: 10, color: mutedColor),
          ),
        ],
        const SizedBox(height: 10),
        if (isAddressee)
          PrimaryProposeActions(id: id, onResolved: onResolved)
        else
          StalledProposeActions(
            attention: attention,
            onResolved: onResolved,
            viewSourceLabel: 'View template body',
            onViewSource: () => _viewBody(context, pathLabel),
          ),
      ],
    );
  }

  static String _shortSha(String sha) {
    if (sha.length <= 12) return sha;
    return sha.substring(0, 12);
  }

  static void _viewBody(BuildContext context, String pathLabel) {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(
          'Open Details for the full body of $pathLabel',
        ),
      ),
    );
  }
}
