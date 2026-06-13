import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../../l10n/app_localizations.dart';
import '../../../providers/hub_provider.dart';
import '../../../theme/design_colors.dart';
import '../../../theme/tokens.dart';
import '../../me/widgets/propose_card_router.dart';

/// ADR-030 W19 — steward-side propose inbox.
///
/// AppBar pill (badge over an inbox icon) that surfaces when the
/// session's agent is a project-steward AND there's at least one
/// `propose` attention row addressed to the project-steward tier
/// scoped to this agent's project.
///
/// Tap opens [StewardProposeInboxScreen] — a list view of the matching
/// rows, each rendered via [ProposeCardRouter] with `myTier:
/// 'project-steward'` so the per-kind cards show their primary
/// variant (Approve/Reject) for the steward.
///
/// Visibility gating:
///   - agentKind must start with `steward.` (drops the pill on
///     worker / non-steward sessions; project-steward in particular
///     per ADR-030's tier semantics)
///   - projectId must be non-empty (steward must be project-bound;
///     team-scoped general steward doesn't get this surface — they
///     see propose rows on the principal's Me-page)
///   - count of matching rows must be > 0 (no clutter on empty)
class StewardProposeInboxPill extends ConsumerWidget {
  final String agentKind;
  final String projectId;
  const StewardProposeInboxPill({
    super.key,
    required this.agentKind,
    required this.projectId,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (!agentKind.startsWith('steward.') || projectId.isEmpty) {
      return const SizedBox.shrink();
    }
    final l10n = AppLocalizations.of(context)!;
    final hubState = ref.watch(hubProvider).value;
    if (hubState == null) return const SizedBox.shrink();
    final rows = _matchingRows(hubState.attention, projectId);
    if (rows.isEmpty) return const SizedBox.shrink();

    return IconButton(
      tooltip: l10n.stewardProposeInboxTooltip(rows.length),
      onPressed: () => Navigator.of(context).push(MaterialPageRoute(
        builder: (_) => StewardProposeInboxScreen(
          projectId: projectId,
          agentKind: agentKind,
        ),
      )),
      icon: Stack(
        clipBehavior: Clip.none,
        children: [
          const Icon(Icons.inbox_outlined, size: 22),
          Positioned(
            right: -6,
            top: -4,
            child: _BadgeCount(count: rows.length),
          ),
        ],
      ),
    );
  }
}

class _BadgeCount extends StatelessWidget {
  final int count;
  const _BadgeCount({required this.count});
  @override
  Widget build(BuildContext context) {
    const bg = DesignColors.warning;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 4, vertical: Spacing.s2),
      constraints: const BoxConstraints(minWidth: 16),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(8),
      ),
      child: Text(
        '$count',
        textAlign: TextAlign.center,
        style: GoogleFonts.jetBrainsMono(
          fontSize: FontSizes.label,
          fontWeight: FontWeight.w700,
          color: Colors.white,
        ),
      ),
    );
  }
}

/// Filter the global attention list to rows that THIS steward should
/// act on. The exact match set:
///   - kind == 'propose'
///   - assigned_tier == 'project-steward'
///   - status == 'open' (Me-page already fetches with status='open',
///     but defensive — a refresh races with a decide and could
///     surface a resolved row mid-flight)
///   - project_id matches the steward's project
///
/// Sibling-shared with the W19 test file's expectations. Exposed at
/// the top level (not a class static) so unit tests can verify the
/// predicate without instantiating widgets.
List<Map<String, dynamic>> _matchingRows(
  List<Map<String, dynamic>> attention,
  String projectId,
) {
  final out = <Map<String, dynamic>>[];
  for (final a in attention) {
    if ((a['kind'] ?? '').toString() != 'propose') continue;
    if ((a['assigned_tier'] ?? '').toString() != 'project-steward') continue;
    if ((a['status'] ?? '').toString() != 'open') continue;
    if ((a['project_id'] ?? '').toString() != projectId) continue;
    out.add(a);
  }
  return out;
}

/// Public-test alias for the row-matching predicate. The widget's
/// own internal calls use `_matchingRows`; tests import this one.
List<Map<String, dynamic>> stewardProposeInboxRows(
  List<Map<String, dynamic>> attention,
  String projectId,
) =>
    _matchingRows(attention, projectId);

/// Screen pushed when the inbox pill is tapped. Renders the matching
/// rows via [ProposeCardRouter] with `myTier: 'project-steward'` so
/// the per-kind cards show their primary variant (Approve/Reject) for
/// the addressee.
class StewardProposeInboxScreen extends ConsumerWidget {
  final String projectId;
  final String agentKind;
  const StewardProposeInboxScreen({
    super.key,
    required this.projectId,
    required this.agentKind,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context)!;
    final hubState = ref.watch(hubProvider).value;
    final rows = hubState == null
        ? <Map<String, dynamic>>[]
        : _matchingRows(hubState.attention, projectId);
    final theme = Theme.of(context);
    final isDark = theme.brightness == Brightness.dark;
    final mutedColor =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Scaffold(
      appBar: AppBar(
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              l10n.stewardProposeInboxTitle,
              style: GoogleFonts.spaceGrotesk(
                  fontSize: 16, fontWeight: FontWeight.w700),
            ),
            Text(
              l10n.stewardProposeInboxSubtitle(projectId),
              style: GoogleFonts.jetBrainsMono(
                  fontSize: FontSizes.label, color: mutedColor),
            ),
          ],
        ),
      ),
      body: rows.isEmpty
          ? Center(
              child: Padding(
                padding: const EdgeInsets.all(24),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Icon(Icons.inbox, size: 32, color: mutedColor),
                    const SizedBox(height: 8),
                    Text(
                      l10n.stewardProposeInboxEmptyTitle,
                      style: GoogleFonts.spaceGrotesk(
                          fontSize: 14, fontWeight: FontWeight.w600),
                    ),
                    const SizedBox(height: 4),
                    Text(
                      l10n.stewardProposeInboxEmptyBody,
                      textAlign: TextAlign.center,
                      style: GoogleFonts.jetBrainsMono(
                          fontSize: 11, color: mutedColor),
                    ),
                  ],
                ),
              ),
            )
          : ListView.separated(
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
              itemCount: rows.length,
              separatorBuilder: (_, __) => const SizedBox(height: 12),
              itemBuilder: (context, i) {
                final row = rows[i];
                final summary = (row['summary'] ?? '').toString();
                return Container(
                  padding: const EdgeInsets.all(12),
                  decoration: BoxDecoration(
                    color: isDark
                        ? DesignColors.surfaceDark
                        : DesignColors.surfaceLight,
                    borderRadius: BorderRadius.circular(8),
                    border: Border.all(
                      color: mutedColor.withValues(alpha: 0.2),
                    ),
                  ),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      if (summary.isNotEmpty) ...[
                        Text(
                          summary,
                          style: GoogleFonts.spaceGrotesk(
                            fontSize: 13,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                        const SizedBox(height: 8),
                      ],
                      // Per-kind body + actions. myTier='project-steward'
                      // makes the addressee predicate true → primary
                      // variant (Approve/Reject).
                      ProposeCardRouter(
                        attention: row,
                        myTier: 'project-steward',
                      ),
                    ],
                  ),
                );
              },
            ),
    );
  }
}
