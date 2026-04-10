import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../models/action_bar_config.dart';
import '../../providers/action_bar_provider.dart';
import '../../providers/settings_provider.dart';
import '../../theme/design_colors.dart';
import 'action_bar_page.dart';

/// Main action bar widget: single button row + [⋮] palette button.
///
/// Sits between terminal display and compose bar. Only the first group
/// of the active profile is shown as a Row; the rest of the profile's
/// keys are reachable via the Key Palette sheet (tap [⋮] or swipe
/// horizontally on the bar). Horizontal swipe cycles to the next/prev
/// profile and auto-opens the palette so the user sees the new keys.
class ActionBar extends ConsumerStatefulWidget {
  /// Send a literal key (text character)
  final void Function(String key) onKeyPressed;

  /// Send a special key (tmux format: Enter, Escape, C-c, etc.)
  final void Function(String tmuxKey) onSpecialKeyPressed;

  /// Action callbacks
  final VoidCallback? onFileTransfer;
  final VoidCallback? onImageTransfer;
  final VoidCallback? onSnippetPicker;
  final VoidCallback? onDirectInputToggle;
  final VoidCallback? onProfileSettings;

  final bool hapticFeedback;

  /// Stable panel identifier so the action bar can show the right
  /// profile for the pane the user is currently looking at (and so
  /// switching profiles only affects that pane). Convention is
  /// `${connectionId}|${paneId}`. When null, the global default is
  /// used — fine for contexts that don't yet know a pane (e.g.
  /// sessions screen preview).
  final String? panelKey;

  const ActionBar({
    super.key,
    required this.onKeyPressed,
    required this.onSpecialKeyPressed,
    this.onFileTransfer,
    this.onImageTransfer,
    this.onSnippetPicker,
    this.onDirectInputToggle,
    this.onProfileSettings,
    this.hapticFeedback = true,
    this.panelKey,
  });

  @override
  ConsumerState<ActionBar> createState() => _ActionBarState();
}

class _ActionBarState extends ConsumerState<ActionBar> {
  void _handleButtonTap(ActionBarButton button) {
    final notifier = ref.read(actionBarProvider.notifier);

    switch (button.type) {
      case ActionBarButtonType.modifier:
        if (button.value == 'ctrl') {
          notifier.toggleCtrl();
        } else if (button.value == 'alt') {
          notifier.toggleAlt();
        }
        break;

      case ActionBarButtonType.action:
        _handleAction(button.value);
        break;

      case ActionBarButtonType.confirm:
        // Tap: send literal + Enter
        widget.onKeyPressed(button.value);
        widget.onSpecialKeyPressed('Enter');
        notifier.resetModifiers();
        break;

      case ActionBarButtonType.literal:
        // Check if modifiers are armed
        final modified = notifier.applyModifiers(button.value);
        if (modified != null) {
          widget.onSpecialKeyPressed(modified);
        } else {
          widget.onKeyPressed(button.value);
        }
        break;

      case ActionBarButtonType.specialKey:
        final modified = notifier.applyModifiers(button.value);
        widget.onSpecialKeyPressed(modified ?? button.value);
        break;

      case ActionBarButtonType.ctrlCombo:
      case ActionBarButtonType.altCombo:
      case ActionBarButtonType.shiftCombo:
        widget.onSpecialKeyPressed(button.value);
        notifier.resetModifiers();
        break;
    }
  }

  void _handleButtonLongPress(ActionBarButton button) {
    switch (button.type) {
      case ActionBarButtonType.confirm:
        // Long-press: send literal only (no Enter)
        widget.onKeyPressed(button.value);
        break;

      case ActionBarButtonType.specialKey:
      case ActionBarButtonType.ctrlCombo:
      case ActionBarButtonType.altCombo:
      case ActionBarButtonType.shiftCombo:
        if (button.longPressValue != null) {
          // Handle multi-key long-press values like "Escape Escape"
          final parts = button.longPressValue!.split(' ');
          for (final part in parts) {
            widget.onSpecialKeyPressed(part);
          }
        }
        break;

      case ActionBarButtonType.modifier:
        // Long-press on modifier is handled by double-tap logic in toggleCtrl/Alt
        break;

      default:
        break;
    }
  }

  void _handleAction(String action) {
    switch (action) {
      case 'file_transfer':
        widget.onFileTransfer?.call();
        break;
      case 'image_transfer':
        widget.onImageTransfer?.call();
        break;
      case 'snippet':
        widget.onSnippetPicker?.call();
        break;
      case 'direct_input':
        widget.onDirectInputToggle?.call();
        break;
    }
  }

  void _openProfileSheet() {
    if (widget.hapticFeedback) {
      HapticFeedback.selectionClick();
    }
    widget.onProfileSettings?.call();
  }

  /// Cycle this panel's active profile by [delta] (+1 = next, -1 = prev),
  /// then open the Key Palette sheet so the user immediately sees the
  /// new profile's keys. Wraps around both ends. Only the *current
  /// panel's* profile changes — other panes retain theirs.
  void _cycleProfileAndOpenPalette(int delta) {
    final notifier = ref.read(actionBarProvider.notifier);
    final state = ref.read(actionBarProvider);
    if (state.profiles.length < 2) {
      // Single profile: swiping still opens the palette so there's
      // always visible feedback for the gesture.
      _openProfileSheet();
      return;
    }
    final currentId = state.profileIdForPanel(widget.panelKey);
    final currentIndex =
        state.profiles.indexWhere((p) => p.id == currentId);
    final base = currentIndex < 0 ? 0 : currentIndex;
    final nextIndex = (base + delta) % state.profiles.length;
    final wrapped =
        nextIndex < 0 ? nextIndex + state.profiles.length : nextIndex;
    final nextProfileId = state.profiles[wrapped].id;
    final panelKey = widget.panelKey;
    if (panelKey != null) {
      notifier.setActiveProfileForPanel(panelKey, nextProfileId);
    } else {
      notifier.setActiveProfile(nextProfileId);
    }
    if (widget.hapticFeedback) {
      HapticFeedback.selectionClick();
    }
    widget.onProfileSettings?.call();
  }

  @override
  Widget build(BuildContext context) {
    final state = ref.watch(actionBarProvider);
    final settings = ref.watch(settingsProvider);
    // Pane-scoped profile: each tmux pane remembers its own active
    // profile, so switching tabs restores the previous profile for
    // that pane. Falls back to the global default when panelKey is
    // null or the pane has no override yet.
    final panelGroups = state.groupsForPanel(widget.panelKey);
    // When nav pad is active, filter out the "Navigate" group (arrows/tab/enter/esc
    // are handled by the nav pad instead). Only the FIRST remaining
    // group is shown in the bar — the rest live in the Key Palette.
    final groups = settings.navPadMode != 'off'
        ? panelGroups.where((g) => g.name != 'Navigate').toList()
        : panelGroups;
    final primaryGroup = groups.isEmpty ? null : groups.first;
    final isDark = Theme.of(context).brightness == Brightness.dark;

    return Container(
      decoration: BoxDecoration(
        color: isDark ? DesignColors.footerBackground : DesignColors.footerBackgroundLight,
        border: Border(
          top: BorderSide(
            color: isDark ? DesignColors.borderDark : DesignColors.borderLight,
            width: 0.5,
          ),
        ),
      ),
      child: SizedBox(
        height: 40,
        child: Row(
          children: [
            // Primary button group. Horizontal swipe cycles the active
            // profile (not the group) and opens the palette for the
            // new profile — old behavior of swiping between groups was
            // removed because most profiles are now curated to a
            // single row and multi-page PageView was rarely used.
            Expanded(
              child: primaryGroup == null
                  ? const SizedBox.shrink()
                  : GestureDetector(
                      behavior: HitTestBehavior.translucent,
                      onHorizontalDragEnd: (details) {
                        final v = details.primaryVelocity ?? 0;
                        if (v.abs() < 200) return;
                        // Swipe left (negative velocity) = next profile.
                        _cycleProfileAndOpenPalette(v < 0 ? 1 : -1);
                      },
                      child: Center(
                        child: ActionBarPage(
                          group: primaryGroup,
                          ctrlArmed: state.ctrlArmed,
                          ctrlLocked: state.ctrlLocked,
                          altArmed: state.altArmed,
                          altLocked: state.altLocked,
                          onButtonTap: _handleButtonTap,
                          onButtonLongPress: _handleButtonLongPress,
                          hapticFeedback: widget.hapticFeedback,
                        ),
                      ),
                    ),
            ),
            // [grid] Palette button — taps open the full key palette.
            // Enlarged from 32dp to 44dp and given a grid icon so the
            // chip-grid palette behind it is visually discoverable
            // (earlier ⋮ icon was hard to associate with "open palette").
            GestureDetector(
              onTap: _openProfileSheet,
              behavior: HitTestBehavior.opaque,
              child: Container(
                width: 44,
                height: 40,
                alignment: Alignment.center,
                decoration: BoxDecoration(
                  border: Border(
                    left: BorderSide(
                      color: isDark
                          ? DesignColors.borderDark
                          : DesignColors.borderLight,
                      width: 0.5,
                    ),
                  ),
                ),
                child: Icon(
                  Icons.grid_view_rounded,
                  size: 20,
                  color: DesignColors.primary.withValues(alpha: 0.85),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}
