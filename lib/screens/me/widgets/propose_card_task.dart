import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:termipod/l10n/app_localizations.dart';

import '../../../providers/vocab_provider.dart';
import '../../../services/vocab/vocab_axis.dart';
import '../../../theme/design_colors.dart';
import '../../../theme/tokens.dart';
import 'propose_addressee.dart';
import 'propose_card_actions.dart';
import 'propose_card_visuals.dart';

/// ADR-030 W17 — per-kind propose card for `task.set_status`.
///
/// Body: status transition (current → proposed), result_summary if
/// present, and the project + task ids. Per W7 the propose-permitted
/// status set is narrowed to `{done, cancelled}` — in_progress / blocked
/// / todo are auto-derived and not reachable through propose; the card
/// nonetheless renders whatever `to_status` the row carries so a
/// hypothetical mis-shaped row still surfaces meaningfully.
///
/// Note: the wire's change_spec uses `status` (not `to_status`) for the
/// target status. There's no `from_status` — task.set_status compares
/// the target row's current status at Apply time. The card shows
/// `→ status` (no from-side chip) so the visual is honest about what
/// the wire knows.
class ProposeCardTask extends ConsumerWidget {
  final Map<String, dynamic> attention;
  final String myTier;
  final VoidCallback? onResolved;

  const ProposeCardTask({
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
    final targetRef = decodeJsonObject(attention['target_ref']);
    final toStatus = (changeSpec['status'] ?? '?').toString();
    final resultSummary = (changeSpec['result_summary'] ?? '').toString();
    final taskId = (targetRef['task_id'] ?? '').toString();
    final projectId = (targetRef['project_id'] ?? '').toString();
    final reason = (attention['summary'] ?? '').toString();
    final addressee = (attention['assigned_tier'] ?? '').toString();
    final id = (attention['id'] ?? '').toString();

    final mutedColor =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final l10n = AppLocalizations.of(context)!;
    final voc = ref.read(vocabularyProvider);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (stalled && !isAddressee) StalledPill(addressee: addressee),
        if (stalled && !isAddressee) const SizedBox(height: 6),
        // task.set_status has no from-status on the wire — render
        // `→ to_status` only. TransitionFrame handles this by accepting
        // an empty fromLabel.
        TransitionFrame(
          fromLabel: '',
          toLabel: toStatus,
          family: TransitionChipFamily.status,
        ),
        if (reason.isNotEmpty) ...[
          const SizedBox(height: 6),
          Text(
            reason,
            style: GoogleFonts.jetBrainsMono(fontSize: 11, color: mutedColor),
          ),
        ],
        if (resultSummary.isNotEmpty) ...[
          const SizedBox(height: 6),
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: Spacing.s8),
            decoration: BoxDecoration(
              color: (isDark ? DesignColors.surfaceDark : DesignColors.canvasLight),
              borderRadius: BorderRadius.circular(4),
            ),
            child: Text(
              resultSummary,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 11,
                color: isDark ? DesignColors.textPrimary : DesignColors.textPrimaryLight,
              ),
            ),
          ),
        ],
        if (taskId.isNotEmpty) ...[
          const SizedBox(height: 4),
          Text(
            'task: $taskId',
            style: GoogleFonts.jetBrainsMono(fontSize: FontSizes.label, color: mutedColor),
          ),
        ],
        if (projectId.isNotEmpty) ...[
          const SizedBox(height: 2),
          Text(
            'project: $projectId',
            style: GoogleFonts.jetBrainsMono(fontSize: FontSizes.label, color: mutedColor),
          ),
        ],
        const SizedBox(height: 10),
        if (isAddressee)
          PrimaryProposeActions(id: id, onResolved: onResolved)
        else
          StalledProposeActions(
            attention: attention,
            onResolved: onResolved,
            viewSourceLabel:
                l10n.viewTask(voc.term(VocabAxis.entityTask).lower),
            onViewSource: taskId.isEmpty
                ? null
                : () => _viewTask(context, ref, taskId),
          ),
      ],
    );
  }

  static void _viewTask(BuildContext context, WidgetRef ref, String taskId) {
    final l10n = AppLocalizations.of(context)!;
    final voc = ref.read(vocabularyProvider);
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(l10n.sourceTask(
        voc.term(VocabAxis.entityTask).lower, taskId))),
    );
  }
}
