import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../providers/settings_provider.dart';
import '../theme/design_colors.dart';

/// Game-style D-pad + action buttons for thumb-optimized terminal navigation.
///
/// Three modes: full (D-pad + 2x2 grid), compact (single row), off (hidden).
/// Auto-hides when software keyboard is open.
class NavigationPad extends ConsumerWidget {
  final void Function(String tmuxKey) onSpecialKeyPressed;
  final void Function(String key) onKeyPressed;

  const NavigationPad({
    super.key,
    required this.onSpecialKeyPressed,
    required this.onKeyPressed,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final settings = ref.watch(settingsProvider);
    final mode = settings.navPadMode;

    if (mode == 'off') return const SizedBox.shrink();

    // Auto-hide when software keyboard is open
    final keyboardHeight = MediaQuery.of(context).viewInsets.bottom;
    if (keyboardHeight > 50) return const SizedBox.shrink();

    final isDark = Theme.of(context).brightness == Brightness.dark;

    return AnimatedSize(
      duration: const Duration(milliseconds: 150),
      child: Container(
        height: mode == 'full' ? 60 : 40,
        decoration: BoxDecoration(
          color: isDark
              ? DesignColors.footerBackground
              : DesignColors.footerBackgroundLight,
          border: Border(
            top: BorderSide(
              color: isDark ? DesignColors.borderDark : DesignColors.borderLight,
              width: 0.5,
            ),
          ),
        ),
        child: Row(
          children: [
            const SizedBox(width: 8),
            if (mode == 'full') ...[
              _DpadFull(
                onSpecialKeyPressed: onSpecialKeyPressed,
                repeatRate: settings.navPadRepeatRate,
                haptic: settings.navPadHaptic,
              ),
              const SizedBox(width: 12),
              _ActionGrid(
                onSpecialKeyPressed: onSpecialKeyPressed,
                repeatRate: settings.navPadRepeatRate,
                haptic: settings.navPadHaptic,
              ),
            ] else ...[
              _CompactRow(
                onSpecialKeyPressed: onSpecialKeyPressed,
                repeatRate: settings.navPadRepeatRate,
                haptic: settings.navPadHaptic,
              ),
            ],
            const Spacer(),
            _ChevronToggle(ref: ref, mode: mode),
            const SizedBox(width: 4),
          ],
        ),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// D-pad (full mode) — classic cross layout
// ---------------------------------------------------------------------------

class _DpadFull extends StatelessWidget {
  final void Function(String tmuxKey) onSpecialKeyPressed;
  final int repeatRate;
  final bool haptic;

  const _DpadFull({
    required this.onSpecialKeyPressed,
    required this.repeatRate,
    required this.haptic,
  });

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: 100,
      height: 56,
      child: Stack(
        children: [
          // Up
          Positioned(
            top: 0,
            left: 34,
            child: _NavButton(
              icon: Icons.keyboard_arrow_up,
              tmuxKey: 'Up',
              width: 32,
              height: 20,
              onSpecialKeyPressed: onSpecialKeyPressed,
              repeatRate: repeatRate,
              haptic: haptic,
            ),
          ),
          // Left
          Positioned(
            top: 18,
            left: 0,
            child: _NavButton(
              icon: Icons.keyboard_arrow_left,
              tmuxKey: 'Left',
              width: 32,
              height: 20,
              onSpecialKeyPressed: onSpecialKeyPressed,
              repeatRate: repeatRate,
              haptic: haptic,
            ),
          ),
          // Right
          Positioned(
            top: 18,
            right: 0,
            child: _NavButton(
              icon: Icons.keyboard_arrow_right,
              tmuxKey: 'Right',
              width: 32,
              height: 20,
              onSpecialKeyPressed: onSpecialKeyPressed,
              repeatRate: repeatRate,
              haptic: haptic,
            ),
          ),
          // Down
          Positioned(
            bottom: 0,
            left: 34,
            child: _NavButton(
              icon: Icons.keyboard_arrow_down,
              tmuxKey: 'Down',
              width: 32,
              height: 20,
              onSpecialKeyPressed: onSpecialKeyPressed,
              repeatRate: repeatRate,
              haptic: haptic,
            ),
          ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Action buttons (full mode) — 2x2 grid: ESC, Tab, Ctrl+C, Enter
// ---------------------------------------------------------------------------

class _ActionGrid extends StatelessWidget {
  final void Function(String tmuxKey) onSpecialKeyPressed;
  final int repeatRate;
  final bool haptic;

  const _ActionGrid({
    required this.onSpecialKeyPressed,
    required this.repeatRate,
    required this.haptic,
  });

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: 100,
      height: 56,
      child: Column(
        children: [
          Row(
            children: [
              _NavButton(
                label: 'ESC',
                tmuxKey: 'Escape',
                width: 48,
                height: 26,
                onSpecialKeyPressed: onSpecialKeyPressed,
                repeatRate: repeatRate,
                haptic: haptic,
              ),
              const SizedBox(width: 4),
              _NavButton(
                label: 'TAB',
                tmuxKey: 'Tab',
                width: 48,
                height: 26,
                onSpecialKeyPressed: onSpecialKeyPressed,
                repeatRate: repeatRate,
                haptic: haptic,
              ),
            ],
          ),
          const SizedBox(height: 4),
          Row(
            children: [
              _NavButton(
                label: 'C-C',
                tmuxKey: 'C-c',
                width: 48,
                height: 26,
                onSpecialKeyPressed: onSpecialKeyPressed,
                repeatRate: repeatRate,
                haptic: haptic,
              ),
              const SizedBox(width: 4),
              _NavButton(
                label: 'ENT',
                tmuxKey: 'Enter',
                width: 48,
                height: 26,
                onSpecialKeyPressed: onSpecialKeyPressed,
                repeatRate: repeatRate,
                haptic: haptic,
              ),
            ],
          ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Compact row — single row of 8 buttons
// ---------------------------------------------------------------------------

class _CompactRow extends StatelessWidget {
  final void Function(String tmuxKey) onSpecialKeyPressed;
  final int repeatRate;
  final bool haptic;

  const _CompactRow({
    required this.onSpecialKeyPressed,
    required this.repeatRate,
    required this.haptic,
  });

  @override
  Widget build(BuildContext context) {
    const buttons = [
      ('Left', Icons.keyboard_arrow_left, true),
      ('Up', Icons.keyboard_arrow_up, true),
      ('Down', Icons.keyboard_arrow_down, true),
      ('Right', Icons.keyboard_arrow_right, true),
    ];
    const actionButtons = [
      ('Escape', 'ESC'),
      ('Tab', 'TAB'),
      ('C-c', 'C-C'),
      ('Enter', 'ENT'),
    ];

    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        // Arrow buttons
        for (final (key, icon, _) in buttons) ...[
          _NavButton(
            icon: icon,
            tmuxKey: key,
            width: 34,
            height: 32,
            onSpecialKeyPressed: onSpecialKeyPressed,
            repeatRate: repeatRate,
            haptic: haptic,
            supportsRepeat: true,
          ),
          const SizedBox(width: 2),
        ],
        const SizedBox(width: 4),
        // Action buttons
        for (final (key, label) in actionButtons) ...[
          _NavButton(
            label: label,
            tmuxKey: key,
            width: 36,
            height: 32,
            onSpecialKeyPressed: onSpecialKeyPressed,
            repeatRate: repeatRate,
            haptic: haptic,
          ),
          const SizedBox(width: 2),
        ],
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Chevron toggle — cycles full → compact → off
// ---------------------------------------------------------------------------

class _ChevronToggle extends StatelessWidget {
  final WidgetRef ref;
  final String mode;

  const _ChevronToggle({required this.ref, required this.mode});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return GestureDetector(
      onTap: () => ref.read(settingsProvider.notifier).cycleNavPadMode(),
      child: Container(
        width: 28,
        height: double.infinity,
        alignment: Alignment.center,
        child: Icon(
          mode == 'full' ? Icons.expand_less : Icons.expand_more,
          size: 20,
          color: isDark
              ? DesignColors.textPrimary.withValues(alpha: 0.5)
              : DesignColors.textPrimaryLight.withValues(alpha: 0.5),
        ),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Single nav button — reusable for D-pad arrows and action buttons
// ---------------------------------------------------------------------------

class _NavButton extends StatefulWidget {
  final IconData? icon;
  final String? label;
  final String tmuxKey;
  final double width;
  final double height;
  final void Function(String tmuxKey) onSpecialKeyPressed;
  final int repeatRate;
  final bool haptic;
  final bool supportsRepeat;

  const _NavButton({
    this.icon,
    this.label,
    required this.tmuxKey,
    required this.width,
    required this.height,
    required this.onSpecialKeyPressed,
    required this.repeatRate,
    required this.haptic,
    bool? supportsRepeat,
  }) : supportsRepeat = supportsRepeat ??
            (tmuxKey == 'Up' ||
                tmuxKey == 'Down' ||
                tmuxKey == 'Left' ||
                tmuxKey == 'Right');

  @override
  State<_NavButton> createState() => _NavButtonState();
}

class _NavButtonState extends State<_NavButton> {
  Timer? _repeatTimer;
  bool _isPressed = false;

  void _handleTapDown(TapDownDetails _) {
    setState(() => _isPressed = true);
    if (widget.haptic) {
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
    widget.onSpecialKeyPressed(widget.tmuxKey);
  }

  void _handleLongPress() {
    if (widget.supportsRepeat) {
      _repeatTimer = Timer.periodic(
        Duration(milliseconds: widget.repeatRate),
        (_) {
          if (widget.haptic) {
            HapticFeedback.selectionClick();
          }
          widget.onSpecialKeyPressed(widget.tmuxKey);
        },
      );
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

    final bgColor = _isPressed
        ? (isDark
            ? DesignColors.keyBackgroundHover
            : DesignColors.keyBackgroundHoverLight)
        : (isDark
            ? DesignColors.keyBackground
            : DesignColors.keyBackgroundLight);
    final textColor =
        isDark ? DesignColors.textPrimary : DesignColors.textPrimaryLight;

    return GestureDetector(
      onTapDown: _handleTapDown,
      onTapUp: _handleTapUp,
      onTapCancel: _handleTapCancel,
      onTap: _handleTap,
      onLongPress: _handleLongPress,
      onLongPressEnd: (_) => _stopRepeat(),
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 80),
        width: widget.width,
        height: widget.height,
        decoration: BoxDecoration(
          color: bgColor,
          borderRadius: BorderRadius.circular(6),
        ),
        child: Center(
          child: widget.icon != null
              ? Icon(widget.icon, size: 16, color: textColor)
              : Text(
                  widget.label!,
                  style: TextStyle(
                    fontSize: widget.label!.length <= 3 ? 10 : 9,
                    fontWeight: FontWeight.w600,
                    color: textColor,
                    letterSpacing: -0.2,
                  ),
                ),
        ),
      ),
    );
  }
}
