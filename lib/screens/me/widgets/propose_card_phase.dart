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

/// ADR-030 W16 — per-kind propose card for `phase.advance`.
///
/// Body: a from_phase → to_phase transition with the project id and
/// the propose reason. Matches the W15 visual rhythm via the shared
/// [TransitionFrame] / [StalledPill] primitives so a glance at the
/// Me-tab can tell propose-rows apart without reading the header.
///
/// The `from_phase` may be absent on the wire — phase.advance accepts
/// an optimistic-concurrency check OR a forced advance; when absent
/// the [TransitionFrame] renders `→ to_phase` without a from-side.
class ProposeCardPhase extends ConsumerWidget {
  final Map<String, dynamic> attention;
  final String myTier;
  final VoidCallback? onResolved;

  const ProposeCardPhase({
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
    final fromPhase = (changeSpec['from_phase'] ?? '').toString();
    final toPhase = (changeSpec['to_phase'] ?? '?').toString();
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
        TransitionFrame(
          fromLabel: fromPhase,
          toLabel: toPhase,
          family: TransitionChipFamily.phase,
        ),
        if (reason.isNotEmpty) ...[
          const SizedBox(height: 6),
          Text(
            reason,
            style: GoogleFonts.jetBrainsMono(fontSize: 11, color: mutedColor),
          ),
        ],
        if (projectId.isNotEmpty) ...[
          const SizedBox(height: 4),
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
                l10n.viewProject(voc.term(VocabAxis.entityProject).title),
            onViewSource: projectId.isEmpty
                ? null
                : () => _viewProject(context, ref, projectId),
          ),
      ],
    );
  }

  static void _viewProject(BuildContext context, WidgetRef ref, String projectId) {
    final l10n = AppLocalizations.of(context)!;
    final voc = ref.read(vocabularyProvider);
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(l10n.sourceProject(
        voc.term(VocabAxis.entityProject).title, projectId))),
    );
  }
}
