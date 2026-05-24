import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../../theme/design_colors.dart';
import 'propose_addressee.dart';
import 'propose_card_actions.dart';
import 'propose_card_visuals.dart';

/// ADR-030 W15 — per-kind propose card for `deliverable.set_state`.
///
/// Two visual variants:
/// - **primary** (viewer IS addressee): Approve / Reject buttons, no
///   top pill. Uses [PrimaryProposeActions] for the action row.
/// - **stalled** (viewer is NOT addressee but escalation surfaced it
///   to them): top pill "⏱ Stuck — addressed to @<addressee>", buttons
///   flip to [StalledProposeActions] (Override / View deliverable).
///
/// Body block is identical in both variants:
/// `<from_state> → <to_state>` transition with the deliverable id +
/// the propose reason. Visual primitives (chips, frame, stalled pill)
/// come from [propose_card_visuals.dart] so the four propose cards
/// (W15-W18) share rendering invariants.
class ProposeCardDeliverable extends ConsumerWidget {
  final Map<String, dynamic> attention;
  final String myTier;
  final VoidCallback? onResolved;

  const ProposeCardDeliverable({
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
    final fromState = (changeSpec['from_state'] ?? '?').toString();
    final toState = (changeSpec['to_state'] ?? '?').toString();
    final deliverableId = (targetRef['deliverable_id'] ?? '').toString();
    final reason = (attention['summary'] ?? '').toString();
    final addressee = (attention['assigned_tier'] ?? '').toString();
    final id = (attention['id'] ?? '').toString();

    final mutedColor =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (stalled && !isAddressee) StalledPill(addressee: addressee),
        if (stalled && !isAddressee) const SizedBox(height: 6),
        TransitionFrame(
          fromLabel: fromState,
          toLabel: toState,
          family: TransitionChipFamily.state,
        ),
        if (reason.isNotEmpty) ...[
          const SizedBox(height: 6),
          Text(
            reason,
            style: GoogleFonts.jetBrainsMono(fontSize: 11, color: mutedColor),
          ),
        ],
        if (deliverableId.isNotEmpty) ...[
          const SizedBox(height: 4),
          Text(
            'deliverable: $deliverableId',
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
            viewSourceLabel: 'View deliverable',
            onViewSource: deliverableId.isEmpty
                ? null
                : () => _viewDeliverable(context, deliverableId),
          ),
      ],
    );
  }

  static void _viewDeliverable(BuildContext context, String deliverableId) {
    // Phase 3 wire — the existing deliverable viewer takes a project
    // context, which we don't have on a bare attention row. Until the
    // W19.6-mobile digest card lands with the cross-screen nav helper,
    // surface the id in a snack so the principal can copy/paste it.
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text('Source deliverable: $deliverableId')),
    );
  }
}
