import 'package:flutter/material.dart';

import '../../l10n/app_localizations.dart';
import '../../theme/design_colors.dart';
import '../../theme/tokens.dart';
import 'digest_issues.dart';

/// The Issues sheet (transcript P5 A2) — the run's **structural** findings:
/// the failures nothing in the run reported. A tool_call whose result never
/// arrived, a result matching no call, a turn left open when the run stopped,
/// an abnormal stop reason, a permission gate nobody answered.
///
/// A modal bottom sheet, not a dock or rail: mobile state surfaces are sheets
/// (parent plan §7.5). Rows seek through the SAME path the Errors stat uses —
/// dismiss, then `onJumpToSeq(coord)` into the shared `TranscriptSeekController`
/// — so there is one seek mechanism, not a second one that can drift.
///
/// Reported errors are NOT merged in here: the hub keeps two taxonomies
/// (`errors` counts what an engine marked failed, `issues` counts what nothing
/// reported) and folding them together in the UI would double-count a failure
/// that shows up in both readings.

/// Opens the sheet. [onJumpToSeq] is invoked after the sheet closes, so the
/// transcript is visible when it moves.
Future<void> showIssuesSheet(
  BuildContext context, {
  required IssueSummary summary,
  void Function(int coord)? onJumpToSeq,
}) {
  return showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    backgroundColor: Colors.transparent,
    builder: (_) => IssuesSheet(summary: summary, onJumpToSeq: onJumpToSeq),
  );
}

@visibleForTesting
class IssuesSheet extends StatelessWidget {
  final IssueSummary summary;
  final void Function(int coord)? onJumpToSeq;

  const IssuesSheet({super.key, required this.summary, this.onJumpToSeq});

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border = isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final muted = isDark ? DesignColors.textMuted : DesignColors.textMutedLight;

    return DraggableScrollableSheet(
      initialChildSize: 0.55,
      minChildSize: 0.3,
      maxChildSize: 0.95,
      expand: false,
      builder: (context, controller) => Container(
        decoration: BoxDecoration(
          color: bg,
          borderRadius: const BorderRadius.vertical(top: Radius.circular(Radii.lg)),
          border: Border.all(color: border),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            _grabHandle(muted),
            Padding(
              padding: const EdgeInsets.fromLTRB(
                  Spacing.s16, Spacing.s4, Spacing.s16, Spacing.s8),
              child: Row(
                children: [
                  Expanded(
                    child: Text(
                      l10n.issuesSheetTitle(summary.total),
                      style: TextStyle(
                        fontSize: FontSizes.subtitle,
                        fontWeight: FontWeight.w700,
                        color: isDark
                            ? DesignColors.textPrimary
                            : DesignColors.textPrimaryLight,
                      ),
                    ),
                  ),
                ],
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(Spacing.s16, 0, Spacing.s16, Spacing.s8),
              child: Text(l10n.issuesSheetExplainer,
                  style: TextStyle(fontSize: FontSizes.label, color: muted)),
            ),
            Divider(height: 1, color: border),
            Expanded(
              child: summary.isEmpty
                  ? _emptyState(l10n, muted)
                  : ListView.builder(
                      controller: controller,
                      padding: const EdgeInsets.symmetric(vertical: Spacing.s8),
                      itemCount: summary.classes.length,
                      itemBuilder: (context, i) => _IssueClassTile(
                        group: summary.classes[i],
                        onJumpToSeq: onJumpToSeq,
                      ),
                    ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _grabHandle(Color muted) => Center(
        child: Container(
          width: 36,
          height: 4,
          margin: const EdgeInsets.only(top: Spacing.s8, bottom: Spacing.s8),
          decoration: BoxDecoration(
            color: muted,
            borderRadius: BorderRadius.circular(Spacing.s2),
          ),
        ),
      );

  // The affirmative-clean signal is the point of the surface, so the empty
  // state says the checks RAN — not merely that nothing is listed.
  Widget _emptyState(AppLocalizations l10n, Color muted) => Center(
        child: Padding(
          padding: const EdgeInsets.all(Spacing.s24),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(Icons.verified_outlined, size: IconSizes.lg, color: muted),
              const SizedBox(height: Spacing.s8),
              Text(l10n.issuesSheetNone,
                  textAlign: TextAlign.center,
                  style: TextStyle(fontSize: FontSizes.bodySmall, color: muted)),
            ],
          ),
        ),
      );
}

/// Colour for a severity. Kept next to the sheet so both the tile and the
/// stat chip tint from one table.
Color issueSeverityColor(String severity) {
  switch (severity) {
    case 'error':
      return DesignColors.error;
    case 'info':
      return DesignColors.textMuted;
    default:
      return DesignColors.warning;
  }
}

String issueClassLabel(AppLocalizations l10n, String cls) {
  switch (cls) {
    case 'missing_tool_result':
      return l10n.issueMissingToolResult;
    case 'orphan_tool_result':
      return l10n.issueOrphanToolResult;
    case 'unanswered_permission':
      return l10n.issueUnansweredPermission;
    case 'incomplete_turn':
      return l10n.issueIncompleteTurn;
    case 'abnormal_stop':
      return l10n.issueAbnormalStop;
    case 'mixed_id_shape':
      return l10n.issueMixedIdShape;
  }
  // A class added hub-side stays readable on an older app rather than blank.
  return cls;
}

String issueSeverityLabel(AppLocalizations l10n, String severity) {
  switch (severity) {
    case 'error':
      return l10n.issueSeverityError;
    case 'info':
      return l10n.issueSeverityInfo;
    default:
      return l10n.issueSeverityWarning;
  }
}

/// One class row: severity bar + label + count, expanding in place to its
/// capped sample list.
class _IssueClassTile extends StatefulWidget {
  final IssueClassGroup group;
  final void Function(int coord)? onJumpToSeq;

  const _IssueClassTile({required this.group, this.onJumpToSeq});

  @override
  State<_IssueClassTile> createState() => _IssueClassTileState();
}

class _IssueClassTileState extends State<_IssueClassTile> {
  bool _open = false;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted = isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final primary =
        isDark ? DesignColors.textPrimary : DesignColors.textPrimaryLight;
    final g = widget.group;
    final tint = issueSeverityColor(g.severity);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        InkWell(
          onTap: () => setState(() => _open = !_open),
          child: Padding(
            padding: const EdgeInsets.symmetric(
                horizontal: Spacing.s16, vertical: Spacing.s8),
            child: Row(
              children: [
                Container(width: 3, height: 28, color: tint),
                const SizedBox(width: Spacing.s8),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(issueSeverityLabel(l10n, g.severity).toUpperCase(),
                          style: TextStyle(
                              fontSize: FontSizes.label,
                              letterSpacing: 0.4,
                              fontWeight: FontWeight.w700,
                              color: tint)),
                      const SizedBox(height: 2),
                      Text(issueClassLabel(l10n, g.cls),
                          maxLines: 2,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(fontSize: FontSizes.bodySmall, color: primary)),
                    ],
                  ),
                ),
                Text('×${g.count}',
                    style: TextStyle(fontSize: FontSizes.caption, color: muted)),
                Icon(_open ? Icons.expand_less : Icons.expand_more,
                    size: IconSizes.md, color: muted),
              ],
            ),
          ),
        ),
        if (_open) ...[
          ...g.samples.map((s) => _sampleRow(context, s, muted, primary)),
          // No silent caps: a partial list says so.
          if (g.capped)
            Padding(
              padding: const EdgeInsets.fromLTRB(
                  Spacing.s32, 0, Spacing.s16, Spacing.s8),
              child: Text(
                  l10n.issuesSheetCapped(g.samples.length, g.count),
                  style: TextStyle(fontSize: FontSizes.label, color: muted)),
            ),
        ],
      ],
    );
  }

  Widget _sampleRow(
      BuildContext context, IssueSample s, Color muted, Color primary) {
    final seekable = s.coord > 0 && widget.onJumpToSeq != null;
    final headline = s.label.isNotEmpty ? s.label : '#${s.coord}';
    return InkWell(
      onTap: seekable
          ? () {
              // Dismiss first so the transcript is on screen when it moves.
              Navigator.of(context).pop();
              widget.onJumpToSeq!(s.coord);
            }
          : null,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(
            Spacing.s32, Spacing.s4, Spacing.s16, Spacing.s4),
        child: Row(
          children: [
            Expanded(
              child: Text(headline,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(fontSize: FontSizes.caption, color: primary)),
            ),
            if (s.ts.isNotEmpty)
              Text(_clock(s.ts),
                  style: TextStyle(fontSize: FontSizes.label, color: muted)),
            if (seekable) ...[
              const SizedBox(width: Spacing.s4),
              Icon(Icons.my_location, size: IconSizes.sm, color: muted),
            ],
          ],
        ),
      ),
    );
  }
}

/// HH:MM from an ISO timestamp; empty if unparseable.
String _clock(String iso) {
  final t = DateTime.tryParse(iso);
  if (t == null) return '';
  final l = t.toLocal();
  return '${l.hour.toString().padLeft(2, '0')}:'
      '${l.minute.toString().padLeft(2, '0')}';
}
