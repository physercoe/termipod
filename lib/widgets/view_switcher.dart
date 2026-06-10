// A small, standalone `View ▾` switcher — a bordered pill that opens a
// PopupMenu of named views. Visually mirrors the switcher baked into
// `session_header.dart` so the run-detail surface (and any future adopter)
// looks the same without depending on the full SessionHeader chrome.
//
// The caller owns the body (typically an IndexedStack keyed on the selected
// index); this widget only drives selection.
import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../l10n/app_localizations.dart';
import '../theme/design_colors.dart';
import '../theme/tokens.dart';

/// One entry in a [ViewSwitcher].
class ViewOption {
  final String label;
  final IconData icon;
  const ViewOption({required this.label, required this.icon});
}

class ViewSwitcher extends StatelessWidget {
  final List<ViewOption> views;
  final int currentView;
  final ValueChanged<int> onSelect;
  const ViewSwitcher({
    super.key,
    required this.views,
    required this.currentView,
    required this.onSelect,
  });

  @override
  Widget build(BuildContext context) {
    if (views.length <= 1) return const SizedBox.shrink();
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final idx = currentView.clamp(0, views.length - 1);
    final cur = views[idx];
    return PopupMenuButton<int>(
      tooltip: AppLocalizations.of(context)!.switchView,
      padding: EdgeInsets.zero,
      onSelected: onSelect,
      itemBuilder: (_) => [
        for (var i = 0; i < views.length; i++)
          PopupMenuItem<int>(
            value: i,
            child: Row(
              children: [
                Icon(views[i].icon,
                    size: 18, color: i == idx ? DesignColors.primary : muted),
                const SizedBox(width: 10),
                Text(
                  views[i].label,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 13,
                    fontWeight: i == idx ? FontWeight.w700 : FontWeight.w500,
                  ),
                ),
              ],
            ),
          ),
      ],
      child: Container(
        margin: const EdgeInsets.only(right: 4),
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: Spacing.s4),
        decoration: BoxDecoration(
          borderRadius: BorderRadius.circular(8),
          border: Border.all(color: border),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(cur.icon, size: 14, color: muted),
            const SizedBox(width: 4),
            Text(
              cur.label,
              style: GoogleFonts.spaceGrotesk(
                  fontSize: 12, fontWeight: FontWeight.w600),
            ),
            Icon(Icons.expand_more, size: 16, color: muted),
          ],
        ),
      ),
    );
  }
}
