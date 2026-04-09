import 'dart:async';

/// Abstract interface for terminal backends.
///
/// Implementations handle content retrieval, key input, and resize
/// for a specific terminal mode (tmux polling vs raw PTY stream).
abstract class TerminalBackend {
  /// Whether this backend supports tmux session/window/pane navigation.
  bool get supportsNavigation;

  /// Initialize after SSH is connected.
  Future<void> initialize({required int cols, required int rows});

  /// Current screen content as ANSI-escaped text (what AnsiTextView renders).
  String get currentContent;

  /// Current cursor position (0-based).
  ({int x, int y}) get cursorPosition;

  /// Current terminal dimensions in characters.
  ({int width, int height}) get dimensions;

  /// Send literal text input.
  Future<void> sendText(String text);

  /// Send a special key.
  /// [tmuxKey]: tmux key name (e.g. 'Enter', 'C-c') - used by TmuxBackend.
  /// [escapeSequence]: VT escape bytes (e.g. '\x1b[A') - used by RawPtyBackend.
  Future<void> sendSpecialKey(String tmuxKey, {String? escapeSequence});

  /// Resize the terminal.
  Future<void> resize(int cols, int rows);

  /// Stream that emits when screen content has changed.
  Stream<void> get contentUpdates;

  /// Boost polling/refresh rate (called after key input for responsiveness).
  void boostRefresh();

  /// Whether currently in tmux copy-mode (always false for raw).
  bool get isInCopyMode;

  /// Scrollback line count.
  int get scrollbackSize;

  /// Dispose resources.
  void dispose();
}
