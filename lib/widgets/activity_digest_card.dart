import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import 'package:termipod/l10n/app_localizations.dart';

import '../theme/design_colors.dart';

/// "Last 24 hours" digest summarising the audit feed.
///
/// Rendered at the top of the Activity tab (firehose context) and at the
/// bottom of the Me tab under "Since you were last here" (summary context)
/// per `docs/ia-redesign.md` §6.3.
class ActivityDigestCard extends StatelessWidget {
  final List<Map<String, dynamic>> events;
  const ActivityDigestCard({super.key, required this.events});

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;

    final cutoff = DateTime.now().toUtc().subtract(const Duration(hours: 24));
    final recent = events.where((e) {
      final ts = DateTime.tryParse((e['ts'] ?? '').toString());
      return ts != null && ts.isAfter(cutoff);
    }).toList();

    final actors = <String>{};
    final actions = <String, int>{};
    for (final e in recent) {
      final handle = (e['actor_handle'] ?? '').toString();
      if (handle.isNotEmpty) actors.add(handle);
      final a = (e['action'] ?? '').toString();
      if (a.isNotEmpty) actions[a] = (actions[a] ?? 0) + 1;
    }
    final topActions = actions.entries.toList()
      ..sort((a, b) => b.value.compareTo(a.value));

    return Container(
      margin: const EdgeInsets.fromLTRB(16, 12, 16, 8),
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            l10n.activityDigest24h,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 11,
              fontWeight: FontWeight.w700,
              color: muted,
              letterSpacing: 0.8,
            ),
          ),
          const SizedBox(height: 10),
          if (recent.isEmpty)
            Text(
              l10n.activityDigestEmpty,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 13,
                color: muted,
              ),
            )
          else ...[
            Row(
              children: [
                _Stat(
                  value: '${recent.length}',
                  label: l10n.activityDigestEvents,
                ),
                const SizedBox(width: 20),
                _Stat(
                  value: '${actors.length}',
                  label: l10n.activityDigestActors,
                ),
                const SizedBox(width: 20),
                _Stat(
                  value: '${actions.length}',
                  label: l10n.activityDigestKinds,
                ),
              ],
            ),
            if (topActions.isNotEmpty) ...[
              const SizedBox(height: 10),
              Wrap(
                spacing: 6,
                runSpacing: 6,
                children: [
                  for (final e in topActions.take(4))
                    Container(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 8, vertical: 3),
                      decoration: BoxDecoration(
                        color: DesignColors.primary.withValues(alpha: 0.12),
                        borderRadius: BorderRadius.circular(6),
                        border: Border.all(
                          color: DesignColors.primary.withValues(alpha: 0.35),
                        ),
                      ),
                      child: Text(
                        '${e.key} · ${e.value}',
                        style: GoogleFonts.jetBrainsMono(
                          fontSize: 10,
                          fontWeight: FontWeight.w600,
                          color: DesignColors.primary,
                        ),
                      ),
                    ),
                ],
              ),
            ],
          ],
        ],
      ),
    );
  }
}

class _Stat extends StatelessWidget {
  final String value;
  final String label;
  const _Stat({required this.value, required this.label});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          value,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 22,
            fontWeight: FontWeight.w700,
          ),
        ),
        Text(
          label,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 11,
            color: muted,
          ),
        ),
      ],
    );
  }
}
