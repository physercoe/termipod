import 'dart:async';
import 'dart:convert';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/action_bar_config.dart';
import '../providers/settings_provider.dart';
import '../theme/design_colors.dart';

/// Default action buttons when no custom buttons are configured.
const _defaultActionButtons = [
  (label: 'ESC', tmuxKey: 'Escape'),
  (label: 'TAB', tmuxKey: 'Tab'),
  (label: 'C-C', tmuxKey: 'C-c'),
  (label: 'ENT', tmuxKey: 'Enter'),
];

/// Parse custom action buttons from JSON settings, falling back to defaults.
List<({String label, String tmuxKey})> _parseActionButtons(String? json) {
  if (json == null) return _defaultActionButtons;
  try {
    final list = jsonDecode(json) as List;
    if (list.length != 4) return _defaultActionButtons;
    return list.map((e) {
      final btn = ActionBarButton.fromJson(e as Map<String, dynamic>);
      return (label: btn.label, tmuxKey: btn.value);
    }).toList();
  } catch (_) {
    return _defaultActionButtons;
  }
}

/// Game-style D-pad + action buttons for thumb-optimized terminal navigation.
///
/// Three modes: full (D-pad/joystick + 2x2 grid), compact (single row), off (hidden).
/// Auto-hides when software keyboard is open.
class NavigationPad extends ConsumerWidget {
  final void Function(String tmuxKey) onSpecialKeyPressed;
  final void Function(String key) onKeyPressed;
  /// Called when user double-taps the D-pad center to toggle gesture mode.
  final VoidCallback? onGestureToggle;

  const NavigationPad({
    super.key,
    required this.onSpecialKeyPressed,
    required this.onKeyPressed,
    this.onGestureToggle,
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
    final actionButtons = _parseActionButtons(settings.navPadButtons);

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
        child: LayoutBuilder(
          builder: (context, constraints) {
            final screenWidth = constraints.maxWidth;
            // Wide layout threshold: foldable unfolded or tablet (>600dp)
            final isWide = screenWidth > 600;

            if (mode == 'full') {
              return _FullModeLayout(
                onSpecialKeyPressed: onSpecialKeyPressed,
                onGestureToggle: onGestureToggle,
                settings: settings,
                actionButtons: actionButtons,
                isWide: isWide,
                screenWidth: screenWidth,
                ref: ref,
                mode: mode,
              );
            } else {
              return _CompactModeLayout(
                onSpecialKeyPressed: onSpecialKeyPressed,
                settings: settings,
                actionButtons: actionButtons,
                isWide: isWide,
                ref: ref,
                mode: mode,
              );
            }
          },
        ),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Full mode layout — D-pad/joystick + action grid, evenly distributed
// ---------------------------------------------------------------------------

class _FullModeLayout extends StatelessWidget {
  final void Function(String tmuxKey) onSpecialKeyPressed;
  final VoidCallback? onGestureToggle;
  final AppSettings settings;
  final List<({String label, String tmuxKey})> actionButtons;
  final bool isWide;
  final double screenWidth;
  final WidgetRef ref;
  final String mode;

  const _FullModeLayout({
    required this.onSpecialKeyPressed,
    required this.onGestureToggle,
    required this.settings,
    required this.actionButtons,
    required this.isWide,
    required this.screenWidth,
    required this.ref,
    required this.mode,
  });

  @override
  Widget build(BuildContext context) {
    final repeatRate = settings.navPadRepeatRate;
    final haptic = settings.navPadHaptic;
    final inputStyle = settings.navPadDpadStyle;

    final directional = inputStyle == 'joystick'
        ? _JoystickFull(
            onSpecialKeyPressed: onSpecialKeyPressed,
            repeatRate: repeatRate,
            haptic: haptic,
            onDoubleTapCenter: onGestureToggle,
          ) as Widget
        : _DpadFull(
            onSpecialKeyPressed: onSpecialKeyPressed,
            repeatRate: repeatRate,
            haptic: haptic,
            onDoubleTapCenter: onGestureToggle,
          );

    final actionGrid = _ActionGrid(
      onSpecialKeyPressed: onSpecialKeyPressed,
      repeatRate: repeatRate,
      haptic: haptic,
      buttons: actionButtons,
    );

    return Row(
      children: [
        // Left side — directional control, centered in its half
        Expanded(
          child: Center(child: directional),
        ),
        // Right side — action grid, centered in its half
        Expanded(
          child: Center(child: actionGrid),
        ),
        // Chevron toggle
        _ChevronToggle(ref: ref, mode: mode),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Compact mode layout — single row, evenly distributed
// ---------------------------------------------------------------------------

class _CompactModeLayout extends StatelessWidget {
  final void Function(String tmuxKey) onSpecialKeyPressed;
  final AppSettings settings;
  final List<({String label, String tmuxKey})> actionButtons;
  final bool isWide;
  final WidgetRef ref;
  final String mode;

  const _CompactModeLayout({
    required this.onSpecialKeyPressed,
    required this.settings,
    required this.actionButtons,
    required this.isWide,
    required this.ref,
    required this.mode,
  });

  @override
  Widget build(BuildContext context) {
    final repeatRate = settings.navPadRepeatRate;
    final haptic = settings.navPadHaptic;

    return Row(
      children: [
        Expanded(
          child: _CompactRow(
            onSpecialKeyPressed: onSpecialKeyPressed,
            repeatRate: repeatRate,
            haptic: haptic,
            buttons: actionButtons,
          ),
        ),
        _ChevronToggle(ref: ref, mode: mode),
      ],
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
  final VoidCallback? onDoubleTapCenter;

  const _DpadFull({
    required this.onSpecialKeyPressed,
    required this.repeatRate,
    required this.haptic,
    this.onDoubleTapCenter,
  });

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: 100,
      height: 56,
      child: Stack(
        children: [
          // Center double-tap zone for gesture mode toggle
          if (onDoubleTapCenter != null)
            Positioned(
              top: 16,
              left: 34,
              child: GestureDetector(
                onDoubleTap: onDoubleTapCenter,
                child: const SizedBox(width: 32, height: 24),
              ),
            ),
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
// Joystick (full mode) — circular drag zone
// ---------------------------------------------------------------------------

class _JoystickFull extends StatefulWidget {
  final void Function(String tmuxKey) onSpecialKeyPressed;
  final int repeatRate;
  final bool haptic;
  final VoidCallback? onDoubleTapCenter;

  const _JoystickFull({
    required this.onSpecialKeyPressed,
    required this.repeatRate,
    required this.haptic,
    this.onDoubleTapCenter,
  });

  @override
  State<_JoystickFull> createState() => _JoystickFullState();
}

class _JoystickFullState extends State<_JoystickFull> {
  static const _size = 56.0;
  static const _deadZone = 8.0;

  Offset _thumbOffset = Offset.zero;
  String? _activeDirection;
  Timer? _repeatTimer;

  void _onPanUpdate(DragUpdateDetails details) {
    final center = const Offset(_size / 2, _size / 2);
    final local = details.localPosition - center;
    final distance = local.distance;
    final maxRadius = _size / 2 - 4;

    // Clamp thumb to circle
    final clamped = distance > maxRadius ? local / distance * maxRadius : local;
    setState(() => _thumbOffset = clamped);

    if (distance < _deadZone) {
      _stopRepeat();
      _activeDirection = null;
      return;
    }

    // Determine direction from angle
    final angle = math.atan2(local.dy, local.dx);
    String direction;
    if (angle > -math.pi / 4 && angle <= math.pi / 4) {
      direction = 'Right';
    } else if (angle > math.pi / 4 && angle <= 3 * math.pi / 4) {
      direction = 'Down';
    } else if (angle > -3 * math.pi / 4 && angle <= -math.pi / 4) {
      direction = 'Up';
    } else {
      direction = 'Left';
    }

    if (direction != _activeDirection) {
      _activeDirection = direction;
      _stopRepeat();
      // Send immediately + start repeat
      widget.onSpecialKeyPressed(direction);
      if (widget.haptic) HapticFeedback.lightImpact();
      _repeatTimer = Timer.periodic(
        Duration(milliseconds: widget.repeatRate),
        (_) {
          if (widget.haptic) HapticFeedback.selectionClick();
          widget.onSpecialKeyPressed(direction);
        },
      );
    }
  }

  void _onPanEnd(DragEndDetails _) {
    _stopRepeat();
    _activeDirection = null;
    setState(() => _thumbOffset = Offset.zero);
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
    final bgColor = isDark ? DesignColors.keyBackground : DesignColors.keyBackgroundLight;
    final thumbColor = isDark
        ? DesignColors.textPrimary.withValues(alpha: 0.6)
        : DesignColors.textPrimaryLight.withValues(alpha: 0.6);
    final activeThumbColor = DesignColors.primary;

    return GestureDetector(
      onDoubleTap: widget.onDoubleTapCenter,
      onPanUpdate: _onPanUpdate,
      onPanEnd: _onPanEnd,
      onPanCancel: () {
        _stopRepeat();
        _activeDirection = null;
        setState(() => _thumbOffset = Offset.zero);
      },
      child: SizedBox(
        width: _size,
        height: _size,
        child: CustomPaint(
          painter: _JoystickPainter(
            bgColor: bgColor,
            thumbColor: _activeDirection != null ? activeThumbColor : thumbColor,
            thumbOffset: _thumbOffset,
          ),
        ),
      ),
    );
  }
}

class _JoystickPainter extends CustomPainter {
  final Color bgColor;
  final Color thumbColor;
  final Offset thumbOffset;

  _JoystickPainter({
    required this.bgColor,
    required this.thumbColor,
    required this.thumbOffset,
  });

  @override
  void paint(Canvas canvas, Size size) {
    final center = Offset(size.width / 2, size.height / 2);
    final radius = size.width / 2;

    // Background circle
    canvas.drawCircle(
      center,
      radius,
      Paint()..color = bgColor,
    );

    // Outer ring
    canvas.drawCircle(
      center,
      radius - 1,
      Paint()
        ..color = thumbColor.withValues(alpha: 0.3)
        ..style = PaintingStyle.stroke
        ..strokeWidth = 1,
    );

    // Thumb dot
    canvas.drawCircle(
      center + thumbOffset,
      8,
      Paint()..color = thumbColor,
    );
  }

  @override
  bool shouldRepaint(_JoystickPainter old) =>
      thumbOffset != old.thumbOffset ||
      thumbColor != old.thumbColor;
}

// ---------------------------------------------------------------------------
// Action buttons (full mode) — 2x2 grid, customizable
// ---------------------------------------------------------------------------

class _ActionGrid extends StatelessWidget {
  final void Function(String tmuxKey) onSpecialKeyPressed;
  final int repeatRate;
  final bool haptic;
  final List<({String label, String tmuxKey})> buttons;

  const _ActionGrid({
    required this.onSpecialKeyPressed,
    required this.repeatRate,
    required this.haptic,
    required this.buttons,
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
                label: buttons[0].label,
                tmuxKey: buttons[0].tmuxKey,
                width: 48,
                height: 26,
                onSpecialKeyPressed: onSpecialKeyPressed,
                repeatRate: repeatRate,
                haptic: haptic,
              ),
              const SizedBox(width: 4),
              _NavButton(
                label: buttons[1].label,
                tmuxKey: buttons[1].tmuxKey,
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
                label: buttons[2].label,
                tmuxKey: buttons[2].tmuxKey,
                width: 48,
                height: 26,
                onSpecialKeyPressed: onSpecialKeyPressed,
                repeatRate: repeatRate,
                haptic: haptic,
              ),
              const SizedBox(width: 4),
              _NavButton(
                label: buttons[3].label,
                tmuxKey: buttons[3].tmuxKey,
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
  final List<({String label, String tmuxKey})> buttons;

  const _CompactRow({
    required this.onSpecialKeyPressed,
    required this.repeatRate,
    required this.haptic,
    required this.buttons,
  });

  @override
  Widget build(BuildContext context) {
    const arrowButtons = [
      ('Left', Icons.keyboard_arrow_left),
      ('Up', Icons.keyboard_arrow_up),
      ('Down', Icons.keyboard_arrow_down),
      ('Right', Icons.keyboard_arrow_right),
    ];

    // Each child uses Expanded so the 8 buttons stretch to fill the available
    // width (matches ActionBar stretch behavior on wide/foldable screens).
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 4),
      child: Row(
        children: [
          // Arrow buttons
          for (final (key, icon) in arrowButtons)
            Expanded(
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 2),
                child: _NavButton(
                  icon: icon,
                  tmuxKey: key,
                  width: double.infinity,
                  height: 32,
                  onSpecialKeyPressed: onSpecialKeyPressed,
                  repeatRate: repeatRate,
                  haptic: haptic,
                  supportsRepeat: true,
                ),
              ),
            ),
          // Small divider between arrow group and action group
          const SizedBox(width: 4),
          // Action buttons (customizable)
          for (final btn in buttons)
            Expanded(
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 2),
                child: _NavButton(
                  label: btn.label,
                  tmuxKey: btn.tmuxKey,
                  width: double.infinity,
                  height: 32,
                  onSpecialKeyPressed: onSpecialKeyPressed,
                  repeatRate: repeatRate,
                  haptic: haptic,
                ),
              ),
            ),
        ],
      ),
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
