import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../models/action_bar_config.dart';
import '../../providers/action_bar_provider.dart';
import '../../theme/design_colors.dart';
import 'action_bar_page.dart';

/// Main action bar widget: swipeable button groups + [⋮] settings button.
///
/// Sits between terminal display and compose bar.
class ActionBar extends ConsumerStatefulWidget {
  /// Send a literal key (text character)
  final void Function(String key) onKeyPressed;

  /// Send a special key (tmux format: Enter, Escape, C-c, etc.)
  final void Function(String tmuxKey) onSpecialKeyPressed;

  /// Action callbacks
  final VoidCallback? onFileTransfer;
  final VoidCallback? onImageTransfer;
  final VoidCallback? onSnippetPicker;
  final VoidCallback? onCommandMenu;
  final VoidCallback? onDirectInputToggle;
  final VoidCallback? onProfileSettings;

  final bool hapticFeedback;

  const ActionBar({
    super.key,
    required this.onKeyPressed,
    required this.onSpecialKeyPressed,
    this.onFileTransfer,
    this.onImageTransfer,
    this.onSnippetPicker,
    this.onCommandMenu,
    this.onDirectInputToggle,
    this.onProfileSettings,
    this.hapticFeedback = true,
  });

  @override
  ConsumerState<ActionBar> createState() => _ActionBarState();
}

class _ActionBarState extends ConsumerState<ActionBar> {
  late PageController _pageController;

  @override
  void initState() {
    super.initState();
    final initialPage = ref.read(actionBarProvider).currentPage;
    _pageController = PageController(initialPage: initialPage);
  }

  @override
  void dispose() {
    _pageController.dispose();
    super.dispose();
  }

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
      case 'command_menu':
        widget.onCommandMenu?.call();
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

  @override
  Widget build(BuildContext context) {
    final state = ref.watch(actionBarProvider);
    final groups = state.activeGroups;
    final isDark = Theme.of(context).brightness == Brightness.dark;

    // Sync page controller when profile changes
    if (_pageController.hasClients &&
        state.currentPage < groups.length &&
        _pageController.page?.round() != state.currentPage) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (_pageController.hasClients && state.currentPage < groups.length) {
          _pageController.jumpToPage(state.currentPage);
        }
      });
    }

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
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          // Button groups + settings button
          SizedBox(
            height: 40,
            child: Row(
              children: [
                // Swipeable group pages
                Expanded(
                  child: groups.isEmpty
                      ? const SizedBox.shrink()
                      : PageView.builder(
                          controller: _pageController,
                          itemCount: groups.length,
                          onPageChanged: (page) {
                            ref
                                .read(actionBarProvider.notifier)
                                .setCurrentPage(page);
                          },
                          itemBuilder: (context, index) {
                            return Center(
                              child: ActionBarPage(
                                group: groups[index],
                                ctrlArmed: state.ctrlArmed,
                                ctrlLocked: state.ctrlLocked,
                                altArmed: state.altArmed,
                                altLocked: state.altLocked,
                                onButtonTap: _handleButtonTap,
                                onButtonLongPress: _handleButtonLongPress,
                                hapticFeedback: widget.hapticFeedback,
                              ),
                            );
                          },
                        ),
                ),
                // [⋮] Settings button
                GestureDetector(
                  onTap: _openProfileSheet,
                  child: Container(
                    width: 32,
                    height: 40,
                    alignment: Alignment.center,
                    child: Icon(
                      Icons.more_vert,
                      size: 18,
                      color: isDark
                          ? DesignColors.textSecondary
                          : DesignColors.textSecondaryLight,
                    ),
                  ),
                ),
              ],
            ),
          ),
          // Page dots
          if (groups.length > 1)
            Padding(
              padding: const EdgeInsets.only(bottom: 4),
              child: Row(
                mainAxisAlignment: MainAxisAlignment.center,
                children: List.generate(groups.length, (index) {
                  final isActive = index == state.currentPage;
                  return Container(
                    width: isActive ? 8 : 5,
                    height: isActive ? 5 : 5,
                    margin: const EdgeInsets.symmetric(horizontal: 2),
                    decoration: BoxDecoration(
                      color: isActive
                          ? DesignColors.primary
                          : (isDark
                              ? DesignColors.textMuted.withValues(alpha: 0.4)
                              : DesignColors.textMutedLight.withValues(alpha: 0.4)),
                      borderRadius: BorderRadius.circular(3),
                    ),
                  );
                }),
              ),
            ),
        ],
      ),
    );
  }
}
