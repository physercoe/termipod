import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../providers/action_bar_provider.dart';
import '../theme/design_colors.dart';

/// Flutter-native QWERTY keyboard for direct input mode.
///
/// Replaces the hidden-TextField + system-IME path in [ComposeBar] when
/// [AppSettings.useCustomKeyboard] is true. Sends raw key events directly
/// through [onKeyPressed] / [onSpecialKeyPressed] callbacks — the same
/// callbacks the action bar and compose bar use — so terminal_screen sees
/// no difference between this, the action bar, and the legacy direct input.
///
/// Height is ~200dp (five rows of ~40dp each), roughly half of what the
/// system keyboard consumes, and terminal keys (Ctrl/Alt/Esc/Tab/arrows)
/// are integrated so users don't need to swap pages for them.
///
/// Ctrl/Alt modifier state is shared with the action bar via
/// [actionBarProvider] — tapping Ctrl here arms the same modifier that the
/// action bar's Ctrl button toggles.
///
/// CJK users who need system IME composition should disable the
/// `useCustomKeyboard` setting; the compose bar then reverts to the legacy
/// hidden-TextField path.
class CustomKeyboard extends ConsumerStatefulWidget {
  final void Function(String char) onKeyPressed;
  final void Function(String tmuxKey) onSpecialKeyPressed;
  final bool haptic;

  const CustomKeyboard({
    super.key,
    required this.onKeyPressed,
    required this.onSpecialKeyPressed,
    this.haptic = true,
  });

  @override
  ConsumerState<CustomKeyboard> createState() => _CustomKeyboardState();
}

class _CustomKeyboardState extends ConsumerState<CustomKeyboard> {
  // Shift / caps state is local to the keyboard — it only affects what
  // character we send for the next letter tap, not any global modifier.
  bool _shiftOn = false;
  bool _shiftLocked = false;

  // Symbols page toggles rows 1-3 between QWERTY and number/symbols.
  // 0 = letters (QWERTY), 1 = symbols page 1, 2 = symbols page 2
  int _symbolsPage = 0;

  // The id of the key currently flashing after a tap. Used by _KeyboardKey
  // to intensify its visual for ~180ms so users get clear tap confirmation.
  String? _flashingKey;
  Timer? _flashTimer;

  // Key-repeat timer for backspace and arrow long-presses.
  Timer? _repeatTimer;

  // Focus node for hardware keyboard capture. Claims focus on mount so USB
  // and Bluetooth keyboards flow through _handleHardwareKey.
  final FocusNode _focusNode = FocusNode(debugLabel: 'CustomKeyboard');

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) _focusNode.requestFocus();
    });
  }

  @override
  void dispose() {
    _flashTimer?.cancel();
    _repeatTimer?.cancel();
    _focusNode.dispose();
    super.dispose();
  }

  // ---------------------------------------------------------------------------
  // Key press flow
  // ---------------------------------------------------------------------------

  void _onLetterKey(String char) {
    final display = (_shiftOn || _shiftLocked) ? char.toUpperCase() : char;
    final state = ref.read(actionBarProvider);

    if (state.ctrlArmed || state.altArmed) {
      // applyModifiers consumes armed (non-locked) modifiers internally.
      final combined = ref
          .read(actionBarProvider.notifier)
          .applyModifiers(display.toLowerCase());
      if (combined != null) {
        widget.onSpecialKeyPressed(combined);
      }
    } else {
      widget.onKeyPressed(display);
    }

    // Shift un-sticks after one key unless locked.
    if (_shiftOn && !_shiftLocked) {
      setState(() => _shiftOn = false);
    }

    _triggerFlash(char);
  }

  void _onSymbolKey(String char) {
    // Symbols/digits bypass the shift state — they send literally.
    final state = ref.read(actionBarProvider);
    if (state.ctrlArmed || state.altArmed) {
      final combined =
          ref.read(actionBarProvider.notifier).applyModifiers(char);
      if (combined != null) {
        widget.onSpecialKeyPressed(combined);
      }
    } else {
      widget.onKeyPressed(char);
    }
    _triggerFlash(char);
  }

  void _onSpecialKey(String tmuxKey) {
    widget.onSpecialKeyPressed(tmuxKey);
    _triggerFlash(tmuxKey);
  }

  void _onShiftTap() {
    setState(() {
      if (_shiftLocked) {
        // Locked → off
        _shiftLocked = false;
        _shiftOn = false;
      } else if (_shiftOn) {
        // Single-shot armed → lock (double-tap)
        _shiftLocked = true;
      } else {
        // Off → armed for one key
        _shiftOn = true;
      }
    });
    _triggerFlash('shift');
  }

  void _onSymbolsToggle() {
    setState(() => _symbolsPage = _symbolsPage == 0 ? 1 : 0);
    _triggerFlash('symbols');
  }

  void _onSymbolsPageSwitch() {
    setState(() => _symbolsPage = _symbolsPage == 1 ? 2 : 1);
    _triggerFlash('symbols2');
  }

  void _onCtrlTap() {
    ref.read(actionBarProvider.notifier).toggleCtrl();
    _triggerFlash('ctrl');
  }

  void _onAltTap() {
    ref.read(actionBarProvider.notifier).toggleAlt();
    _triggerFlash('alt');
  }

  void _triggerFlash(String keyId) {
    if (widget.haptic) HapticFeedback.mediumImpact();
    _flashTimer?.cancel();
    setState(() => _flashingKey = keyId);
    _flashTimer = Timer(const Duration(milliseconds: 180), () {
      if (!mounted) return;
      setState(() => _flashingKey = null);
    });
  }

  // ---------------------------------------------------------------------------
  // Key repeat (backspace + arrows)
  // ---------------------------------------------------------------------------

  void _startRepeat(String tmuxKey) {
    _repeatTimer?.cancel();
    _repeatTimer = Timer.periodic(const Duration(milliseconds: 80), (_) {
      widget.onSpecialKeyPressed(tmuxKey);
      if (widget.haptic) HapticFeedback.selectionClick();
    });
  }

  void _stopRepeat() {
    _repeatTimer?.cancel();
    _repeatTimer = null;
  }

  // ---------------------------------------------------------------------------
  // Hardware keyboard capture
  // ---------------------------------------------------------------------------

  static final _hwSpecialKeyMap = <LogicalKeyboardKey, String>{
    LogicalKeyboardKey.escape: 'Escape',
    LogicalKeyboardKey.tab: 'Tab',
    LogicalKeyboardKey.arrowUp: 'Up',
    LogicalKeyboardKey.arrowDown: 'Down',
    LogicalKeyboardKey.arrowLeft: 'Left',
    LogicalKeyboardKey.arrowRight: 'Right',
    LogicalKeyboardKey.home: 'Home',
    LogicalKeyboardKey.end: 'End',
    LogicalKeyboardKey.pageUp: 'PPage',
    LogicalKeyboardKey.pageDown: 'NPage',
    LogicalKeyboardKey.delete: 'DC',
    LogicalKeyboardKey.backspace: 'BSpace',
    LogicalKeyboardKey.f1: 'F1',
    LogicalKeyboardKey.f2: 'F2',
    LogicalKeyboardKey.f3: 'F3',
    LogicalKeyboardKey.f4: 'F4',
    LogicalKeyboardKey.f5: 'F5',
    LogicalKeyboardKey.f6: 'F6',
    LogicalKeyboardKey.f7: 'F7',
    LogicalKeyboardKey.f8: 'F8',
    LogicalKeyboardKey.f9: 'F9',
    LogicalKeyboardKey.f10: 'F10',
    LogicalKeyboardKey.f11: 'F11',
    LogicalKeyboardKey.f12: 'F12',
  };

  KeyEventResult _handleHardwareKey(FocusNode node, KeyEvent event) {
    if (event is! KeyDownEvent && event is! KeyRepeatEvent) {
      return KeyEventResult.ignored;
    }

    final key = event.logicalKey;

    // Ctrl/Meta + letter
    final isCtrl = HardwareKeyboard.instance.isControlPressed ||
        HardwareKeyboard.instance.isMetaPressed;
    if (isCtrl) {
      final label = key.keyLabel;
      if (label.length == 1 && RegExp(r'^[A-Za-z]$').hasMatch(label)) {
        widget.onSpecialKeyPressed('C-${label.toLowerCase()}');
        return KeyEventResult.handled;
      }
    }

    // Enter
    if (key == LogicalKeyboardKey.enter ||
        key == LogicalKeyboardKey.numpadEnter) {
      widget.onSpecialKeyPressed('Enter');
      return KeyEventResult.handled;
    }

    // Mapped special keys
    final tmuxKey = _hwSpecialKeyMap[key];
    if (tmuxKey != null) {
      widget.onSpecialKeyPressed(tmuxKey);
      return KeyEventResult.handled;
    }

    // Regular character input from the hardware keyboard
    final char = event.character;
    if (char != null && char.isNotEmpty) {
      widget.onKeyPressed(char);
      return KeyEventResult.handled;
    }

    return KeyEventResult.ignored;
  }

  // ---------------------------------------------------------------------------
  // Build
  // ---------------------------------------------------------------------------

  // Letter rows (lowercase — shift casing happens at send time)
  static const _row1 = ['q', 'w', 'e', 'r', 't', 'y', 'u', 'i', 'o', 'p'];
  static const _row2 = ['a', 's', 'd', 'f', 'g', 'h', 'j', 'k', 'l'];
  static const _row3 = ['z', 'x', 'c', 'v', 'b', 'n', 'm'];

  // Symbols page 1
  static const _sym1 = ['1', '2', '3', '4', '5', '6', '7', '8', '9', '0'];
  static const _sym2 = ['!', '@', '#', r'$', '%', '^', '&', '*', '(', ')'];
  static const _sym3 = ['-', '=', '[', ']', '\\', ';', "'", '/'];

  // Symbols page 2 (shifted / extra punctuation)
  static const _sym2b = ['`', '~', '<', '>', '{', '}', ':', '"', '|', '+'];
  static const _sym3b = ['?', '_', '.', ','];

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final state = ref.watch(actionBarProvider);
    final ctrlActive = state.ctrlArmed || state.ctrlLocked;
    final altActive = state.altArmed || state.altLocked;

    return Focus(
      focusNode: _focusNode,
      onKeyEvent: _handleHardwareKey,
      child: Container(
        decoration: BoxDecoration(
          color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
          border: Border(
            top: BorderSide(
              color:
                  isDark ? DesignColors.borderDark : DesignColors.borderLight,
              width: 0.5,
            ),
          ),
        ),
        padding: const EdgeInsets.fromLTRB(4, 6, 4, 8),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            _buildRow1(isDark),
            const SizedBox(height: 4),
            _buildRow2(isDark),
            const SizedBox(height: 4),
            _buildRow3(isDark),
            const SizedBox(height: 4),
            _buildRow4(isDark, ctrlActive, altActive, state.ctrlLocked,
                state.altLocked),
            const SizedBox(height: 4),
            _buildRow5(isDark),
          ],
        ),
      ),
    );
  }

  Widget _buildRow1(bool isDark) {
    final isSymbols = _symbolsPage > 0;
    final letters = isSymbols ? _sym1 : _row1;
    return Row(
      children: [
        for (final ch in letters)
          Expanded(
            flex: 2,
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 2),
              child: _KeyboardKey(
                label: _displayLetter(ch),
                isFlashing: _flashingKey == ch,
                onTap: () => isSymbols ? _onSymbolKey(ch) : _onLetterKey(ch),
                isDark: isDark,
              ),
            ),
          ),
        Expanded(
          flex: 3,
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 2),
            child: _KeyboardKey(
              icon: Icons.backspace_outlined,
              isFlashing: _flashingKey == 'BSpace',
              onTap: () => _onSpecialKey('BSpace'),
              onLongPressStart: () => _startRepeat('BSpace'),
              onLongPressEnd: _stopRepeat,
              isDark: isDark,
            ),
          ),
        ),
      ],
    );
  }

  Widget _buildRow2(bool isDark) {
    final isSymbols = _symbolsPage > 0;
    final letters = isSymbols
        ? (_symbolsPage == 2 ? _sym2b : _sym2)
        : _row2;
    return Row(
      children: [
        // Small left margin to offset the staggered QWERTY layout
        if (!isSymbols) const SizedBox(width: 8),
        for (final ch in letters)
          Expanded(
            flex: 2,
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 2),
              child: _KeyboardKey(
                label: _displayLetter(ch),
                isFlashing: _flashingKey == ch,
                onTap: () => isSymbols ? _onSymbolKey(ch) : _onLetterKey(ch),
                isDark: isDark,
              ),
            ),
          ),
        if (!isSymbols) const SizedBox(width: 8),
        Expanded(
          flex: 3,
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 2),
            child: _KeyboardKey(
              label: '↵',
              isFlashing: _flashingKey == 'Enter',
              onTap: () => _onSpecialKey('Enter'),
              accent: DesignColors.primary,
              isDark: isDark,
            ),
          ),
        ),
      ],
    );
  }

  Widget _buildRow3(bool isDark) {
    final isSymbols = _symbolsPage > 0;
    final letters = isSymbols
        ? (_symbolsPage == 2 ? _sym3b : _sym3)
        : _row3;
    return Row(
      children: [
        // Shift key (letters) or page switch (#+=) for symbols
        Expanded(
          flex: 3,
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 2),
            child: isSymbols
                ? _KeyboardKey(
                    label: _symbolsPage == 2 ? '!@#' : '#+=',
                    isFlashing: _flashingKey == 'symbols2',
                    isToggled: _symbolsPage == 2,
                    onTap: _onSymbolsPageSwitch,
                    isDark: isDark,
                  )
                : _KeyboardKey(
                    icon: _shiftLocked
                        ? Icons.keyboard_capslock
                        : Icons.arrow_upward_rounded,
                    isFlashing: _flashingKey == 'shift',
                    isToggled: _shiftOn || _shiftLocked,
                    onTap: _onShiftTap,
                    isDark: isDark,
                  ),
          ),
        ),
        for (final ch in letters)
          Expanded(
            flex: 2,
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 2),
              child: _KeyboardKey(
                label: _displayLetter(ch),
                isFlashing: _flashingKey == ch,
                onTap: () => isSymbols ? _onSymbolKey(ch) : _onLetterKey(ch),
                isDark: isDark,
              ),
            ),
          ),
        if (!isSymbols) ...[
          Expanded(
            flex: 2,
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 2),
              child: _KeyboardKey(
                label: ',',
                isFlashing: _flashingKey == ',',
                onTap: () => _onSymbolKey(','),
                isDark: isDark,
              ),
            ),
          ),
          Expanded(
            flex: 2,
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 2),
              child: _KeyboardKey(
                label: '.',
                isFlashing: _flashingKey == '.',
                onTap: () => _onSymbolKey('.'),
                isDark: isDark,
              ),
            ),
          ),
        ],
      ],
    );
  }

  Widget _buildRow4(bool isDark, bool ctrlActive, bool altActive,
      bool ctrlLocked, bool altLocked) {
    return Row(
      children: [
        Expanded(
          flex: 3,
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 2),
            child: _KeyboardKey(
              label: _symbolsPage > 0 ? 'ABC' : '?123',
              isFlashing: _flashingKey == 'symbols',
              isToggled: _symbolsPage > 0,
              onTap: _onSymbolsToggle,
              isDark: isDark,
            ),
          ),
        ),
        Expanded(
          flex: 3,
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 2),
            child: _KeyboardKey(
              label: 'Ctrl',
              isFlashing: _flashingKey == 'ctrl',
              isToggled: ctrlActive,
              isLocked: ctrlLocked,
              onTap: _onCtrlTap,
              isDark: isDark,
            ),
          ),
        ),
        Expanded(
          flex: 3,
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 2),
            child: _KeyboardKey(
              label: 'Alt',
              isFlashing: _flashingKey == 'alt',
              isToggled: altActive,
              isLocked: altLocked,
              onTap: _onAltTap,
              isDark: isDark,
            ),
          ),
        ),
        Expanded(
          flex: 3,
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 2),
            child: _KeyboardKey(
              label: 'Esc',
              isFlashing: _flashingKey == 'Escape',
              onTap: () => _onSpecialKey('Escape'),
              isDark: isDark,
            ),
          ),
        ),
        Expanded(
          flex: 4,
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 2),
            child: _KeyboardKey(
              label: 'Tab',
              isFlashing: _flashingKey == 'Tab',
              onTap: () => _onSpecialKey('Tab'),
              isDark: isDark,
            ),
          ),
        ),
      ],
    );
  }

  Widget _buildRow5(bool isDark) {
    return Row(
      children: [
        Expanded(
          flex: 2,
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 2),
            child: _KeyboardKey(
              icon: Icons.chevron_left,
              isFlashing: _flashingKey == 'Left',
              onTap: () => _onSpecialKey('Left'),
              onLongPressStart: () => _startRepeat('Left'),
              onLongPressEnd: _stopRepeat,
              isDark: isDark,
            ),
          ),
        ),
        Expanded(
          flex: 2,
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 2),
            child: _KeyboardKey(
              icon: Icons.keyboard_arrow_down,
              isFlashing: _flashingKey == 'Down',
              onTap: () => _onSpecialKey('Down'),
              onLongPressStart: () => _startRepeat('Down'),
              onLongPressEnd: _stopRepeat,
              isDark: isDark,
            ),
          ),
        ),
        Expanded(
          flex: 2,
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 2),
            child: _KeyboardKey(
              icon: Icons.keyboard_arrow_up,
              isFlashing: _flashingKey == 'Up',
              onTap: () => _onSpecialKey('Up'),
              onLongPressStart: () => _startRepeat('Up'),
              onLongPressEnd: _stopRepeat,
              isDark: isDark,
            ),
          ),
        ),
        Expanded(
          flex: 2,
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 2),
            child: _KeyboardKey(
              icon: Icons.chevron_right,
              isFlashing: _flashingKey == 'Right',
              onTap: () => _onSpecialKey('Right'),
              onLongPressStart: () => _startRepeat('Right'),
              onLongPressEnd: _stopRepeat,
              isDark: isDark,
            ),
          ),
        ),
        Expanded(
          flex: 8,
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 2),
            child: _KeyboardKey(
              label: '␣',
              isFlashing: _flashingKey == ' ',
              onTap: () => _onLetterKey(' '),
              isDark: isDark,
            ),
          ),
        ),
      ],
    );
  }

  String _displayLetter(String ch) {
    if (_symbolsPage > 0) return ch;
    return (_shiftOn || _shiftLocked) ? ch.toUpperCase() : ch;
  }
}

/// A single key cap. Renders with flash/toggle/locked states via the
/// simple `isFlashing`, `isToggled`, `isLocked` flags.
class _KeyboardKey extends StatelessWidget {
  final String? label;
  final IconData? icon;
  final bool isFlashing;
  final bool isToggled;
  final bool isLocked;
  final VoidCallback onTap;
  final VoidCallback? onLongPressStart;
  final VoidCallback? onLongPressEnd;
  final Color? accent;
  final bool isDark;

  const _KeyboardKey({
    this.label,
    this.icon,
    required this.isFlashing,
    this.isToggled = false,
    this.isLocked = false,
    required this.onTap,
    this.onLongPressStart,
    this.onLongPressEnd,
    this.accent,
    required this.isDark,
  });

  @override
  Widget build(BuildContext context) {
    Color bgColor;
    Color textColor;
    Color? borderColor;

    if (isFlashing) {
      bgColor = DesignColors.primary.withValues(alpha: 0.55);
      textColor = DesignColors.primary;
      borderColor = DesignColors.primary.withValues(alpha: 0.9);
    } else if (isLocked) {
      bgColor = DesignColors.primary.withValues(alpha: 0.35);
      textColor = DesignColors.primary;
      borderColor = DesignColors.primary;
    } else if (isToggled) {
      bgColor = DesignColors.primary.withValues(alpha: 0.22);
      textColor = DesignColors.primary;
      borderColor = DesignColors.primary.withValues(alpha: 0.5);
    } else if (accent != null) {
      bgColor = accent!.withValues(alpha: 0.18);
      textColor = accent!;
      borderColor = null;
    } else {
      bgColor = isDark
          ? DesignColors.keyBackground
          : DesignColors.keyBackgroundLight;
      textColor =
          isDark ? DesignColors.textPrimary : DesignColors.textPrimaryLight;
      borderColor = null;
    }

    return GestureDetector(
      onTap: onTap,
      onLongPressStart:
          onLongPressStart == null ? null : (_) => onLongPressStart!(),
      onLongPressEnd: onLongPressEnd == null ? null : (_) => onLongPressEnd!(),
      behavior: HitTestBehavior.opaque,
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 80),
        height: 40,
        decoration: BoxDecoration(
          color: bgColor,
          borderRadius: BorderRadius.circular(6),
          border: borderColor != null
              ? Border.all(color: borderColor, width: isLocked ? 1.5 : 1)
              : null,
        ),
        child: Center(
          child: icon != null
              ? Icon(icon, size: 20, color: textColor)
              : Text(
                  label!,
                  style: TextStyle(
                    fontSize: _labelFontSize(label!),
                    fontWeight: FontWeight.w600,
                    color: textColor,
                    letterSpacing: -0.2,
                  ),
                ),
        ),
      ),
    );
  }

  double _labelFontSize(String label) {
    if (label.length <= 1) return 18;
    if (label.length <= 3) return 14;
    return 12;
  }
}
