import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../../providers/hub_provider.dart';
import '../../../theme/design_colors.dart';
import 'propose_addressee.dart';
import 'propose_card_actions.dart';

/// ADR-030 W15 — per-kind propose card for `deliverable.set_state`.
///
/// Two visual variants:
/// - **primary** (viewer IS addressee): Approve / Reject buttons, no
///   top pill. Uses [PrimaryProposeActions] for the action row.
/// - **stalled** (viewer is NOT addressee but escalation surfaced it
///   to them): top pill "⏱ Stuck — addressed to @<addressee>", buttons
///   flip to [StalledProposeActions] (Override / View source).
///
/// Body block is identical in both variants:
/// `<from_state> → <to_state>` transition with the deliverable id +
/// the propose reason. Once a richer deliverable-title lookup lands,
/// this swaps in `summary` for the title — for now the wire summary
/// is already "Propose deliverable.set_state — <reason>" so the card
/// composes nicely without an extra fetch.
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

    final changeSpec = _decodeJsonObject(attention['change_spec']);
    final targetRef = _decodeJsonObject(attention['target_ref']);
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
        if (stalled && !isAddressee)
          _StalledPill(addressee: addressee, theme: theme),
        if (stalled && !isAddressee) const SizedBox(height: 6),
        // State-transition arrow — the single most useful piece of
        // context for the addressee at-a-glance. Monospace so digits
        // (e.g. v1.0.687) and underscores in state names render cleanly.
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
          decoration: BoxDecoration(
            border: Border.all(
              color: mutedColor.withValues(alpha: 0.35),
            ),
            borderRadius: BorderRadius.circular(6),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              _StateChip(label: fromState, theme: theme, role: _StateRole.from),
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 6),
                child: Icon(Icons.arrow_forward, size: 12, color: mutedColor),
              ),
              _StateChip(label: toState, theme: theme, role: _StateRole.to),
            ],
          ),
        ),
        if (reason.isNotEmpty) ...[
          const SizedBox(height: 6),
          Text(
            reason,
            style: GoogleFonts.jetBrainsMono(
              fontSize: 11,
              color: mutedColor,
            ),
          ),
        ],
        if (deliverableId.isNotEmpty) ...[
          const SizedBox(height: 4),
          Text(
            'deliverable: $deliverableId',
            style: GoogleFonts.jetBrainsMono(
              fontSize: 10,
              color: mutedColor,
            ),
          ),
        ],
        const SizedBox(height: 10),
        if (isAddressee)
          PrimaryProposeActions(
            id: id,
            onResolved: onResolved,
          )
        else
          StalledProposeActions(
            id: id,
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
    // surface the id in a snack so the principal can copy/paste it
    // into the existing project navigator. Replaced by a real push
    // route in the W19.6-mobile follow-up.
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text('Source deliverable: $deliverableId'),
        action: SnackBarAction(
          label: 'Copy',
          onPressed: () {},
        ),
      ),
    );
  }
}

Map<String, dynamic> _decodeJsonObject(dynamic raw) {
  if (raw == null) return const {};
  if (raw is Map<String, dynamic>) return raw;
  if (raw is String) {
    try {
      final decoded = jsonDecode(raw);
      if (decoded is Map<String, dynamic>) return decoded;
    } catch (_) {
      return const {};
    }
  }
  return const {};
}

enum _StateRole { from, to }

class _StateChip extends StatelessWidget {
  final String label;
  final ThemeData theme;
  final _StateRole role;
  const _StateChip({required this.label, required this.theme, required this.role});

  @override
  Widget build(BuildContext context) {
    final isDark = theme.brightness == Brightness.dark;
    final emphasis = role == _StateRole.to;
    final bg = emphasis
        ? (isDark ? Colors.green.shade900 : Colors.green.shade100)
        : (isDark ? Colors.grey.shade800 : Colors.grey.shade200);
    final fg = emphasis
        ? (isDark ? Colors.green.shade200 : Colors.green.shade900)
        : (isDark ? Colors.grey.shade200 : Colors.grey.shade800);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        label,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 11,
          fontWeight: emphasis ? FontWeight.w600 : FontWeight.w400,
          color: fg,
        ),
      ),
    );
  }
}

class _StalledPill extends StatelessWidget {
  final String addressee;
  final ThemeData theme;
  const _StalledPill({required this.addressee, required this.theme});

  @override
  Widget build(BuildContext context) {
    final isDark = theme.brightness == Brightness.dark;
    final bg = isDark ? Colors.amber.shade900 : Colors.amber.shade100;
    final fg = isDark ? Colors.amber.shade100 : Colors.amber.shade900;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(4),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(Icons.schedule, size: 12, color: fg),
          const SizedBox(width: 4),
          Text(
            addressee.isEmpty
                ? 'Stuck — awaiting decision'
                : 'Stuck — addressed to $addressee',
            style: GoogleFonts.jetBrainsMono(
              fontSize: 10,
              fontWeight: FontWeight.w600,
              color: fg,
            ),
          ),
        ],
      ),
    );
  }
}
