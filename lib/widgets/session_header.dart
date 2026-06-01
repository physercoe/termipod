// Shared header for the agent/session transcript surfaces (P2 —
// docs/plans/agent-transcript-debug-and-header-parity.md).
//
// One widget that both SessionChatScreen (full-screen Scaffold) and the
// project-agent sheet render, so their headers can't drift the way the
// two hand-rolled ones did (one an AppBar, the other a custom Row).
// Owns: title (+ optional subtitle), an optional session chip, a
// dedicated `View ▾` switcher, caller-supplied trailing actions + a ⋮
// menu, and an optional × close.
//
// The body — an IndexedStack over [views] — is rendered by the PARENT
// keyed on [currentView]; this header only drives selection. That keeps
// each surface owning its own view widgets and per-view state while
// sharing the chrome.
import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../theme/design_colors.dart';

/// One entry in the header's `View ▾` switcher.
class SessionView {
  final String label;
  final IconData icon;
  const SessionView({required this.label, required this.icon});
}

class SessionHeader extends StatelessWidget {
  final String title;
  // Second line under the title — engine · host, etc. Null hides it.
  final String? subtitle;
  // The session chip (a dense SessionInitChip). Placed inline when the
  // row has room, else dropped to its own slim row 2 (hybrid). Null
  // hides it.
  final Widget? chip;
  // The views reachable via `View ▾`. One view (or none) hides the
  // switcher — a feed-only surface shows no chrome it can't use.
  final List<SessionView> views;
  final int currentView;
  final ValueChanged<int> onSelectView;
  // Optional widget at the very start of the row — a back button on the
  // full-screen surface. The project sheet leaves it null and uses the
  // [onClose] × on the right instead.
  final Widget? leading;
  // Trailing status/scope pills shown before the menu. Caller-owned
  // because the action sets genuinely differ between surfaces.
  final List<Widget> leadingActions;
  // The ⋮ overflow menu (a caller-built PopupMenuButton); null hides it.
  final Widget? menu;
  // When set, renders a × that calls this (sheet surfaces). Null on
  // AppBar-backed surfaces that already carry a back affordance.
  final VoidCallback? onClose;
  const SessionHeader({
    super.key,
    required this.title,
    this.subtitle,
    this.chip,
    this.views = const [],
    this.currentView = 0,
    required this.onSelectView,
    this.leading,
    this.leadingActions = const [],
    this.menu,
    this.onClose,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;

    // Fixed controls (never shrink): leading actions, View ▾, ⋮, ×.
    final controls = Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        ...leadingActions,
        _viewSwitcher(context, muted),
        if (menu != null) menu!,
        if (onClose != null)
          IconButton(
            icon: const Icon(Icons.close),
            visualDensity: VisualDensity.compact,
            onPressed: onClose,
          ),
      ],
    );

    final hasSubtitle = subtitle != null && subtitle!.isNotEmpty;
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 10, 8, 6),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          // Row 1: title (ellipsizes) + the fixed controls. The chip no
          // longer shares this row — it moved to row 2 beside the identity
          // subtitle, which removes the old chip↔status-pill collision and
          // the divider that tried to patch it.
          Row(
            children: [
              if (leading != null) leading!,
              Expanded(
                child: Text(
                  title,
                  style: GoogleFonts.spaceGrotesk(
                      fontSize: 16, fontWeight: FontWeight.w700),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              controls,
            ],
          ),
          // Row 2: identity (e.g. "steward.v1 @host") + the session chip.
          // Subtitle ellipsizes first; the slim chip keeps its size.
          if (hasSubtitle || chip != null)
            Padding(
              padding: const EdgeInsets.only(top: 2),
              child: Row(
                children: [
                  if (hasSubtitle)
                    Flexible(
                      child: Text(
                        subtitle!,
                        style: GoogleFonts.jetBrainsMono(
                            fontSize: 10, color: muted),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                  if (hasSubtitle && chip != null) const SizedBox(width: 8),
                  if (chip != null) chip!,
                ],
              ),
            ),
        ],
      ),
    );
  }

  Widget _viewSwitcher(BuildContext context, Color muted) {
    if (views.length <= 1) return const SizedBox.shrink();
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final idx = currentView.clamp(0, views.length - 1);
    final cur = views[idx];
    return PopupMenuButton<int>(
      tooltip: 'Switch view',
      padding: EdgeInsets.zero,
      onSelected: onSelectView,
      itemBuilder: (_) => [
        for (var i = 0; i < views.length; i++)
          PopupMenuItem<int>(
            value: i,
            child: Row(
              children: [
                Icon(views[i].icon,
                    size: 18,
                    color: i == idx ? DesignColors.primary : muted),
                const SizedBox(width: 10),
                Text(
                  views[i].label,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 13,
                    fontWeight:
                        i == idx ? FontWeight.w700 : FontWeight.w500,
                  ),
                ),
              ],
            ),
          ),
      ],
      child: Container(
        margin: const EdgeInsets.only(right: 4),
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 5),
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
