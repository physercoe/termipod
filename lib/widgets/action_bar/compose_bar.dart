import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:termipod/l10n/app_localizations.dart';

import '../../providers/action_bar_provider.dart';
import '../../providers/compose_draft_provider.dart';
import '../../providers/history_provider.dart';
import '../../providers/settings_provider.dart';
import '../../theme/design_colors.dart';

/// Compose bar: [+] insert menu + text field + [Send] button.
///
/// Primary input mode for the terminal. User composes text, then sends
/// it as a batch via tmux send-keys. Supports multi-line input.
class ComposeBar extends ConsumerStatefulWidget {
  /// Connection this compose bar belongs to — used to persist the compose
  /// draft across navigation (pop back to connections list → reconnect).
  final String connectionId;

  /// Called to send composed text to terminal
  final void Function(String text, {bool withEnter}) onSend;

  /// Called when [+] button is tapped (opens insert menu)
  final VoidCallback? onInsertMenu;

  /// Called to send a special key (used in direct input mode)
  final void Function(String tmuxKey)? onSpecialKeyPressed;

  /// Called to send a literal key (used in direct input mode)
  final void Function(String key)? onKeyPressed;

  /// Placeholder text
  final String hintText;

  final bool hapticFeedback;

  const ComposeBar({
    super.key,
    required this.connectionId,
    required this.onSend,
    this.onInsertMenu,
    this.onSpecialKeyPressed,
    this.onKeyPressed,
    this.hintText = '',
    this.hapticFeedback = true,
  });

  @override
  ConsumerState<ComposeBar> createState() => ComposeBarState();
}

class ComposeBarState extends ConsumerState<ComposeBar> {
  final TextEditingController _controller = TextEditingController();
  final FocusNode _focusNode = FocusNode();
  bool _hasText = false;

  // Direct input mode state (sentinel approach)
  static const String _sentinel = '\u200B';
  final TextEditingController _directController = TextEditingController();
  final FocusNode _directFocusNode = FocusNode();
  bool _isResettingDirect = false;
  bool _isComposing = false;
  String? _lastComposingText;
  DateTime? _lastKeyEventHandledAt;

  @override
  void initState() {
    super.initState();
    // Restore any draft stashed from a previous session on this connection.
    // Read-only seed — the controller becomes the source of truth afterward.
    final draft = ref.read(composeDraftProvider(widget.connectionId));
    if (draft.isNotEmpty) {
      _controller.text = draft;
      _controller.selection =
          TextSelection.collapsed(offset: draft.length);
      _hasText = true;
    }
    _controller.addListener(_onComposeTextChanged);
    _directController.addListener(_onDirectInputChanged);
  }

  void _onComposeTextChanged() {
    final text = _controller.text;
    final hasText = text.isNotEmpty;
    if (hasText != _hasText) {
      setState(() => _hasText = hasText);
    }
    // Mirror every edit into the draft provider so the text survives the
    // terminal screen being popped (e.g. user returns to connections list
    // to reconnect after a dropped session).
    ref.read(composeDraftProvider(widget.connectionId).notifier).set(text);
  }

  @override
  void dispose() {
    _controller.removeListener(_onComposeTextChanged);
    _controller.dispose();
    _focusNode.dispose();
    _directController.removeListener(_onDirectInputChanged);
    _directController.dispose();
    _directFocusNode.dispose();
    super.dispose();
  }

  /// Insert text into compose field at cursor position
  void insertText(String text) {
    final currentText = _controller.text;
    final selection = _controller.selection;
    final start = selection.isValid ? selection.start : currentText.length;
    final end = selection.isValid ? selection.end : currentText.length;

    final newText =
        currentText.substring(0, start) + text + currentText.substring(end);
    _controller.value = TextEditingValue(
      text: newText,
      selection: TextSelection.collapsed(offset: start + text.length),
    );
    _focusNode.requestFocus();
  }

  /// Get the compose text field focus node
  FocusNode get focusNode => _focusNode;

  /// Current contents of the compose text field. Used by the insert menu's
  /// "save to snippet" action to capture whatever the user has typed so far
  /// without forcing them to re-enter it.
  String get currentText => _controller.text;

  /// Clear the compose field contents (used after stashing the draft into
  /// snippets so the bar is ready for the next input).
  void clearText() {
    _controller.clear();
    ref.read(composeDraftProvider(widget.connectionId).notifier).clear();
  }

  void _handleSend() {
    if (widget.hapticFeedback) {
      HapticFeedback.lightImpact();
    }

    final text = _controller.text;
    if (text.isEmpty) {
      // Empty compose: send Enter
      widget.onSend('', withEnter: true);
    } else {
      widget.onSend(text, withEnter: true);
      // Add to history
      ref.read(historyProvider.notifier).add(text);
      _controller.clear();
      // _onComposeTextChanged will propagate the empty string, but clear
      // explicitly so there's no race with listener ordering.
      ref.read(composeDraftProvider(widget.connectionId).notifier).clear();
    }
  }

  void _handleLongPressSend() {
    if (widget.hapticFeedback) {
      HapticFeedback.mediumImpact();
    }

    final text = _controller.text;
    if (text.isNotEmpty) {
      widget.onSend(text, withEnter: false);
      ref.read(historyProvider.notifier).add(text);
      _controller.clear();
      ref.read(composeDraftProvider(widget.connectionId).notifier).clear();
    }
  }

  // ---------------------------------------------------------------------------
  // Direct input mode
  // ---------------------------------------------------------------------------

  void _onDirectInputChanged() {
    if (_isResettingDirect) return;

    final text = _directController.text;
    final value = _directController.value;

    _isComposing = value.composing.isValid && !value.composing.isCollapsed;

    if (_isComposing) {
      _lastComposingText = text.replaceAll(_sentinel, '');

      // Samsung IME workaround: intercept modifier+letter during composing
      final state = ref.read(actionBarProvider);
      if ((state.ctrlArmed || state.altArmed) &&
          _lastComposingText!.length == 1) {
        final char = _lastComposingText!;
        if (RegExp(r'^[A-Za-z]$').hasMatch(char)) {
          final notifier = ref.read(actionBarProvider.notifier);
          final modified = notifier.applyModifiers(char.toLowerCase());
          if (modified != null) {
            widget.onSpecialKeyPressed?.call(modified);
          }
          _lastComposingText = null;
          _resetDirectToSentinel();
          return;
        }
      }
      return;
    }

    // Sentinel deleted = Backspace
    if (text.isEmpty) {
      _lastComposingText = null;
      widget.onSpecialKeyPressed?.call('BSpace');
      _resetDirectToSentinel();
      return;
    }

    final actualText = text.replaceAll(_sentinel, '');
    if (actualText.isNotEmpty) {
      if (_isRecentKeyEventHandled()) {
        _lastComposingText = null;
        _resetDirectToSentinel();
        return;
      }

      // iOS duplicate detection
      String textToSend = actualText;
      if (_lastComposingText != null &&
          actualText.length > _lastComposingText!.length &&
          actualText.startsWith(_lastComposingText!)) {
        textToSend = _lastComposingText!;
      }
      _lastComposingText = null;

      // Modifier + key
      final state = ref.read(actionBarProvider);
      if ((state.ctrlArmed || state.altArmed) &&
          textToSend.length == 1 &&
          RegExp(r'^[A-Za-z]$').hasMatch(textToSend)) {
        final notifier = ref.read(actionBarProvider.notifier);
        final modified = notifier.applyModifiers(textToSend.toLowerCase());
        if (modified != null) {
          widget.onSpecialKeyPressed?.call(modified);
        }
      } else {
        widget.onKeyPressed?.call(textToSend);
      }

      _resetDirectToSentinel();
    }
  }

  void _onDirectInputSubmitted(String value) {
    if (_isRecentKeyEventHandled()) return;
    widget.onSpecialKeyPressed?.call('Enter');
    _resetDirectToSentinel();
  }

  void _resetDirectToSentinel() {
    _isResettingDirect = true;
    _directController.value = TextEditingValue(
      text: _sentinel,
      selection: TextSelection.collapsed(offset: _sentinel.length),
    );
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      final currentValue = _directController.value;
      final hasActiveComposing =
          currentValue.composing.isValid && !currentValue.composing.isCollapsed;
      if (!hasActiveComposing && _directController.text != _sentinel) {
        _directController.value = TextEditingValue(
          text: _sentinel,
          selection: TextSelection.collapsed(offset: _sentinel.length),
        );
      }
      _isResettingDirect = false;
    });
  }

  void _markKeyEventHandled() {
    _lastKeyEventHandledAt = DateTime.now();
  }

  bool _isRecentKeyEventHandled() {
    if (_lastKeyEventHandledAt == null) return false;
    return DateTime.now().difference(_lastKeyEventHandledAt!) <
        const Duration(milliseconds: 100);
  }

  /// External keyboard key handler for direct input mode
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

  KeyEventResult _handleDirectKeyEvent(FocusNode node, KeyEvent event) {
    if (event is! KeyDownEvent && event is! KeyRepeatEvent) {
      return KeyEventResult.ignored;
    }
    if (_isComposing) return KeyEventResult.ignored;

    final key = event.logicalKey;

    // Ctrl/Meta + letter
    final isCtrl = HardwareKeyboard.instance.isControlPressed ||
        HardwareKeyboard.instance.isMetaPressed;
    if (isCtrl) {
      final keyLabel = key.keyLabel;
      if (keyLabel.length == 1 && RegExp(r'^[A-Za-z]$').hasMatch(keyLabel)) {
        _markKeyEventHandled();
        widget.onSpecialKeyPressed?.call('C-${keyLabel.toLowerCase()}');
        return KeyEventResult.handled;
      }
    }

    // Enter
    if (key == LogicalKeyboardKey.enter ||
        key == LogicalKeyboardKey.numpadEnter) {
      _markKeyEventHandled();
      widget.onSpecialKeyPressed?.call('Enter');
      _resetDirectToSentinel();
      return KeyEventResult.handled;
    }

    // Backspace: handled by sentinel
    if (key == LogicalKeyboardKey.backspace) {
      return KeyEventResult.ignored;
    }

    // Special keys
    final tmuxKey = _hwSpecialKeyMap[key];
    if (tmuxKey != null) {
      _markKeyEventHandled();
      widget.onSpecialKeyPressed?.call(tmuxKey);
      return KeyEventResult.handled;
    }

    return KeyEventResult.ignored;
  }

  // ---------------------------------------------------------------------------
  // Build
  // ---------------------------------------------------------------------------

  @override
  Widget build(BuildContext context) {
    final state = ref.watch(actionBarProvider);
    final useCustomKeyboard =
        ref.watch(settingsProvider.select((s) => s.useCustomKeyboard));
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final colorScheme = Theme.of(context).colorScheme;

    return Container(
      decoration: BoxDecoration(
        color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        border: Border(
          top: BorderSide(
            color: isDark ? DesignColors.borderDark : DesignColors.borderLight,
            width: 0.5,
          ),
        ),
      ),
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 6),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          // [+] Insert menu button
          GestureDetector(
            onTap: () {
              if (widget.hapticFeedback) HapticFeedback.selectionClick();
              widget.onInsertMenu?.call();
            },
            child: Container(
              width: 36,
              height: 36,
              alignment: Alignment.center,
              decoration: BoxDecoration(
                color: isDark
                    ? DesignColors.keyBackground
                    : DesignColors.keyBackgroundLight,
                borderRadius: BorderRadius.circular(18),
              ),
              child: Icon(
                Icons.add,
                size: 20,
                color: isDark
                    ? DesignColors.textSecondary
                    : DesignColors.textSecondaryLight,
              ),
            ),
          ),
          const SizedBox(width: 6),

          // Text field (compose or direct input)
          Expanded(
            child: state.composeMode
                ? _buildComposeField(isDark, colorScheme)
                : (useCustomKeyboard
                    ? _buildLiveStatusChip(isDark)
                    : _buildDirectInputField(isDark, colorScheme)),
          ),
          const SizedBox(width: 6),

          // Send button (compose mode) or mode switch (direct mode)
          if (state.composeMode)
            _buildSendButton(isDark)
          else
            _buildComposeSwitchButton(isDark),
        ],
      ),
    );
  }

  void _handleClear() {
    if (widget.hapticFeedback) HapticFeedback.selectionClick();
    _controller.clear();
    _focusNode.requestFocus();
  }

  void _handleLongPressClear() {
    if (widget.hapticFeedback) HapticFeedback.mediumImpact();
    _controller.clear();
    // Also clear the terminal input line via C-u
    widget.onSpecialKeyPressed?.call('C-u');
    _focusNode.requestFocus();
  }

  Widget _buildComposeField(bool isDark, ColorScheme colorScheme) {
    return ConstrainedBox(
      constraints: const BoxConstraints(maxHeight: 120),
      child: TextField(
        controller: _controller,
        focusNode: _focusNode,
        maxLines: null,
        minLines: 1,
        keyboardType: TextInputType.multiline,
        textInputAction: TextInputAction.newline,
        style: TextStyle(
          fontSize: 14,
          color: colorScheme.onSurface,
        ),
        decoration: InputDecoration(
          hintText: widget.hintText.isEmpty ? AppLocalizations.of(context)!.typeCommandHint : widget.hintText,
          hintStyle: TextStyle(
            fontSize: 14,
            color: colorScheme.onSurface.withValues(alpha: 0.4),
          ),
          filled: true,
          fillColor: isDark ? DesignColors.inputDark : DesignColors.inputLight,
          border: OutlineInputBorder(
            borderRadius: BorderRadius.circular(18),
            borderSide: BorderSide.none,
          ),
          contentPadding: const EdgeInsets.symmetric(
            horizontal: 14,
            vertical: 8,
          ),
          isDense: true,
          suffixIcon: _hasText
              ? GestureDetector(
                  onTap: _handleClear,
                  onLongPress: _handleLongPressClear,
                  child: Padding(
                    padding: const EdgeInsets.only(right: 4),
                    child: Icon(
                      Icons.close_rounded,
                      size: 18,
                      color: isDark
                          ? DesignColors.textMuted
                          : DesignColors.textMutedLight,
                    ),
                  ),
                )
              : null,
          suffixIconConstraints:
              const BoxConstraints(minWidth: 28, minHeight: 28),
        ),
      ),
    );
  }

  Widget _buildDirectInputField(bool isDark, ColorScheme colorScheme) {
    // Initialize sentinel if entering direct mode
    if (_directController.text.isEmpty || _directController.text == '') {
      _isResettingDirect = true;
      _directController.value = TextEditingValue(
        text: _sentinel,
        selection: TextSelection.collapsed(offset: _sentinel.length),
      );
      _isResettingDirect = false;
    }

    return Focus(
      onKeyEvent: _handleDirectKeyEvent,
      child: TextField(
        controller: _directController,
        focusNode: _directFocusNode,
        autocorrect: false,
        enableSuggestions: false,
        onSubmitted: _onDirectInputSubmitted,
        style: TextStyle(
          fontSize: 14,
          color: Colors.transparent, // Hide the sentinel
        ),
        decoration: InputDecoration(
          hintText: AppLocalizations.of(context)!.directInputHint,
          hintStyle: TextStyle(
            fontSize: 14,
            color: DesignColors.secondary.withValues(alpha: 0.6),
          ),
          filled: true,
          fillColor: isDark ? DesignColors.inputDark : DesignColors.inputLight,
          border: OutlineInputBorder(
            borderRadius: BorderRadius.circular(18),
            borderSide: BorderSide(
              color: DesignColors.secondary.withValues(alpha: 0.4),
            ),
          ),
          enabledBorder: OutlineInputBorder(
            borderRadius: BorderRadius.circular(18),
            borderSide: BorderSide(
              color: DesignColors.secondary.withValues(alpha: 0.3),
            ),
          ),
          focusedBorder: OutlineInputBorder(
            borderRadius: BorderRadius.circular(18),
            borderSide: BorderSide(
              color: DesignColors.secondary.withValues(alpha: 0.6),
            ),
          ),
          contentPadding: const EdgeInsets.symmetric(
            horizontal: 14,
            vertical: 8,
          ),
          isDense: true,
          // LIVE indicator
          suffixIcon: Container(
            margin: const EdgeInsets.only(right: 8),
            padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
            decoration: BoxDecoration(
              color: DesignColors.success.withValues(alpha: 0.2),
              borderRadius: BorderRadius.circular(4),
            ),
            child: const Text(
              'LIVE',
              style: TextStyle(
                fontSize: 9,
                fontWeight: FontWeight.w700,
                color: DesignColors.success,
              ),
            ),
          ),
          suffixIconConstraints:
              const BoxConstraints(minWidth: 0, minHeight: 0),
        ),
      ),
    );
  }

  /// Status chip displayed in direct input mode when the Flutter-native
  /// custom keyboard is active. The text field is not mounted because the
  /// custom keyboard captures input directly — this chip simply signals
  /// that direct mode is live so the compose bar still feels "active".
  Widget _buildLiveStatusChip(bool isDark) {
    return Container(
      height: 36,
      alignment: Alignment.center,
      decoration: BoxDecoration(
        color: DesignColors.success.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(18),
        border: Border.all(
          color: DesignColors.success.withValues(alpha: 0.3),
          width: 1,
        ),
      ),
      child: Row(
        mainAxisAlignment: MainAxisAlignment.center,
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(
            Icons.keyboard_rounded,
            size: 14,
            color: DesignColors.success,
          ),
          const SizedBox(width: 6),
          Text(
            AppLocalizations.of(context)!.customKeyboardLive,
            style: const TextStyle(
              fontSize: 11,
              fontWeight: FontWeight.w700,
              color: DesignColors.success,
              letterSpacing: 0.5,
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildSendButton(bool isDark) {
    return GestureDetector(
      onTap: _handleSend,
      onLongPress: _handleLongPressSend,
      child: Container(
        width: 36,
        height: 36,
        decoration: BoxDecoration(
          color: DesignColors.primary,
          borderRadius: BorderRadius.circular(18),
        ),
        child: const Icon(
          Icons.arrow_upward_rounded,
          color: Colors.white,
          size: 20,
        ),
      ),
    );
  }

  Widget _buildComposeSwitchButton(bool isDark) {
    return GestureDetector(
      onTap: () {
        if (widget.hapticFeedback) HapticFeedback.selectionClick();
        ref.read(actionBarProvider.notifier).setComposeMode(true);
      },
      child: Container(
        width: 36,
        height: 36,
        decoration: BoxDecoration(
          color: isDark
              ? DesignColors.keyBackground
              : DesignColors.keyBackgroundLight,
          borderRadius: BorderRadius.circular(18),
        ),
        child: Icon(
          Icons.edit_note_rounded,
          color: isDark
              ? DesignColors.textSecondary
              : DesignColors.textSecondaryLight,
          size: 20,
        ),
      ),
    );
  }
}
