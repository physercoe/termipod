import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../models/action_bar_config.dart';
import '../../theme/design_colors.dart';

/// A single button in the action bar.
///
/// Supports tap, long-press, and key-repeat (for arrow keys).
/// Modifier buttons show armed/locked state visually.
class ActionBarButtonWidget extends StatefulWidget {
  final ActionBarButton button;
  final bool isModifierArmed;
  final bool isModifierLocked;
  final VoidCallback onTap;
  final VoidCallback? onLongPress;
  final bool hapticFeedback;

  const ActionBarButtonWidget({
    super.key,
    required this.button,
    this.isModifierArmed = false,
    this.isModifierLocked = false,
    required this.onTap,
    this.onLongPress,
    this.hapticFeedback = true,
  });

  @override
  State<ActionBarButtonWidget> createState() => _ActionBarButtonWidgetState();
}

class _ActionBarButtonWidgetState extends State<ActionBarButtonWidget> {
  Timer? _repeatTimer;
  bool _isPressed = false;

  /// Whether this button type supports key repeat on long-press
  bool get _supportsRepeat =>
      widget.button.type == ActionBarButtonType.specialKey &&
      _isArrowKey(widget.button.value);

  bool _isArrowKey(String value) =>
      value == 'Left' ||
      value == 'Right' ||
      value == 'Up' ||
      value == 'Down';

  void _handleTapDown(TapDownDetails _) {
    setState(() => _isPressed = true);
    if (widget.hapticFeedback) {
      HapticFeedback.lightImpact();
    }
  }

  void _handleTapUp(TapUpDetails _) {
    setState(() => _isPressed = false);
    _stopRepeat();
  }

  void _handleTapCancel() {
    setState(() => _isPressed = false);
    _stopRepeat();
  }

  void _handleTap() {
    widget.onTap();
  }

  void _handleLongPress() {
    if (_supportsRepeat) {
      // Start key repeat for arrow keys
      _repeatTimer = Timer.periodic(const Duration(milliseconds: 80), (_) {
        if (widget.hapticFeedback) {
          HapticFeedback.selectionClick();
        }
        widget.onTap();
      });
    } else if (widget.onLongPress != null) {
      if (widget.hapticFeedback) {
        HapticFeedback.mediumImpact();
      }
      widget.onLongPress!();
    }
  }

  void _stopRepeat() {
    _repeatTimer?.cancel();
    _repeatTimer = null;
  }

  @override
  void dispose() {
    _stopRepeat();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final isModifier = widget.button.type == ActionBarButtonType.modifier;
    final isAction = widget.button.type == ActionBarButtonType.action;
    final isConfirm = widget.button.type == ActionBarButtonType.confirm;

    // Determine button appearance
    Color bgColor;
    Color textColor;
    Color borderColor;

    if (isModifier && (widget.isModifierArmed || widget.isModifierLocked)) {
      // Armed/locked modifier: highlighted
      bgColor = DesignColors.primary.withValues(alpha: 0.3);
      textColor = DesignColors.primary;
      borderColor = widget.isModifierLocked
          ? DesignColors.primary
          : DesignColors.primary.withValues(alpha: 0.5);
    } else if (isConfirm) {
      // Confirm buttons: subtle green tint
      bgColor = _isPressed
          ? DesignColors.success.withValues(alpha: 0.3)
          : (isDark
              ? DesignColors.success.withValues(alpha: 0.1)
              : DesignColors.success.withValues(alpha: 0.08));
      textColor = isDark ? DesignColors.success : const Color(0xFF16A34A);
      borderColor = Colors.transparent;
    } else if (isAction) {
      // Action buttons: secondary accent
      bgColor = _isPressed
          ? DesignColors.secondary.withValues(alpha: 0.3)
          : (isDark
              ? DesignColors.keyBackground
              : DesignColors.keyBackgroundLight);
      textColor = DesignColors.secondary;
      borderColor = Colors.transparent;
    } else {
      // Normal key button
      bgColor = _isPressed
          ? (isDark
              ? DesignColors.keyBackgroundHover
              : DesignColors.keyBackgroundHoverLight)
          : (isDark
              ? DesignColors.keyBackground
              : DesignColors.keyBackgroundLight);
      textColor = isDark ? DesignColors.textPrimary : DesignColors.textPrimaryLight;
      borderColor = Colors.transparent;
    }

    final iconData = _resolveIcon(widget.button.iconName);

    return GestureDetector(
      onTapDown: _handleTapDown,
      onTapUp: _handleTapUp,
      onTapCancel: _handleTapCancel,
      onTap: _handleTap,
      onLongPress: _handleLongPress,
      onLongPressEnd: (_) => _stopRepeat(),
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 100),
        height: 32,
        constraints: const BoxConstraints(minWidth: 36),
        padding: const EdgeInsets.symmetric(horizontal: 6),
        decoration: BoxDecoration(
          color: bgColor,
          borderRadius: BorderRadius.circular(6),
          border: borderColor != Colors.transparent
              ? Border.all(color: borderColor, width: 1)
              : null,
        ),
        child: Center(
          child: iconData != null
              ? Icon(iconData, size: 18, color: textColor)
              : Text(
                  widget.button.label,
                  style: TextStyle(
                    fontSize: _fontSize(widget.button.label),
                    fontWeight: FontWeight.w600,
                    color: textColor,
                    letterSpacing: -0.2,
                  ),
                  maxLines: 1,
                  overflow: TextOverflow.clip,
                ),
        ),
      ),
    );
  }

  double _fontSize(String label) {
    if (label.length <= 2) return 12;
    if (label.length <= 4) return 10;
    return 9;
  }

  IconData? _resolveIcon(String? iconName) {
    if (iconName == null) return null;
    switch (iconName) {
      case 'arrow_left':
        return Icons.arrow_left;
      case 'arrow_right':
        return Icons.arrow_right;
      case 'arrow_drop_up':
        return Icons.arrow_drop_up;
      case 'arrow_drop_down':
        return Icons.arrow_drop_down;
      default:
        return null;
    }
  }
}
