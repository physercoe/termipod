import 'package:flutter/material.dart';

import '../../models/action_bar_config.dart';
import 'action_bar_button.dart';

/// A single page (group) of buttons in the action bar.
class ActionBarPage extends StatelessWidget {
  final ActionBarGroup group;
  final bool ctrlArmed;
  final bool ctrlLocked;
  final bool altArmed;
  final bool altLocked;
  final void Function(ActionBarButton button) onButtonTap;
  final void Function(ActionBarButton button)? onButtonLongPress;
  final bool hapticFeedback;

  const ActionBarPage({
    super.key,
    required this.group,
    this.ctrlArmed = false,
    this.ctrlLocked = false,
    this.altArmed = false,
    this.altLocked = false,
    required this.onButtonTap,
    this.onButtonLongPress,
    this.hapticFeedback = true,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 4),
      child: Row(
        children: group.buttons.map((button) {
          final isCtrlButton =
              button.type == ActionBarButtonType.modifier &&
              button.value == 'ctrl';
          final isAltButton =
              button.type == ActionBarButtonType.modifier &&
              button.value == 'alt';

          return Expanded(
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 2),
              child: ActionBarButtonWidget(
                button: button,
                isModifierArmed: isCtrlButton
                    ? ctrlArmed
                    : isAltButton
                        ? altArmed
                        : false,
                isModifierLocked: isCtrlButton
                    ? ctrlLocked
                    : isAltButton
                        ? altLocked
                        : false,
                onTap: () => onButtonTap(button),
                onLongPress: onButtonLongPress != null
                    ? () => onButtonLongPress!(button)
                    : null,
                hapticFeedback: hapticFeedback,
              ),
            ),
          );
        }).toList(),
      ),
    );
  }
}
