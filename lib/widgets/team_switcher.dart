import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import 'package:termipod/l10n/app_localizations.dart';

import '../providers/hub_provider.dart';
import '../screens/hub/team_screen.dart';
import '../theme/design_colors.dart';

/// Persistent team switcher pill, shown in the AppBar of every Tier-1 tab
/// per `docs/ia-redesign.md` §11 Wedge 6.
///
/// MVP shape: shows the current team id as a pill; tapping opens the Team
/// Settings surface ([TeamScreen]). When the user belongs to multiple
/// teams in a later iteration, tapping will open a team list sheet and
/// switch the active team.
class TeamSwitcher extends ConsumerWidget {
  const TeamSwitcher({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final hub = ref.watch(hubProvider);
    final teamId = hub.value?.config?.teamId ?? '';
    if (teamId.isEmpty) return const SizedBox.shrink();

    final l10n = AppLocalizations.of(context)!;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final fg = isDark
        ? DesignColors.textSecondary
        : DesignColors.textSecondaryLight;

    return Center(
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 4),
        child: Tooltip(
          message: l10n.teamSwitcherTooltip,
          child: Material(
            color: Colors.transparent,
            child: InkWell(
              borderRadius: BorderRadius.circular(16),
              onTap: () => Navigator.of(context).push(
                MaterialPageRoute(builder: (_) => const TeamScreen()),
              ),
              child: Container(
                padding:
                    const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
                decoration: BoxDecoration(
                  color: bg,
                  borderRadius: BorderRadius.circular(16),
                  border: Border.all(color: border),
                ),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Icon(Icons.groups_2_outlined, size: 14, color: fg),
                    const SizedBox(width: 6),
                    ConstrainedBox(
                      constraints: const BoxConstraints(maxWidth: 96),
                      child: Text(
                        teamId,
                        overflow: TextOverflow.ellipsis,
                        maxLines: 1,
                        style: GoogleFonts.spaceGrotesk(
                          fontSize: 12,
                          fontWeight: FontWeight.w600,
                          color: fg,
                        ),
                      ),
                    ),
                    const SizedBox(width: 2),
                    Icon(Icons.expand_more, size: 14, color: fg),
                  ],
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}
