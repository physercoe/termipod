import 'dart:async';

import '../ssh/ssh_client.dart';

/// Abstract interface for terminal backends.
///
/// Implementations handle content retrieval, key input, and resize
/// for a specific terminal mode (tmux polling vs raw PTY stream).
abstract class TerminalBackend {
  /// Whether this backend supports tmux session/window/pane navigation.
  bool get supportsNavigation;

  /// Initialize after SSH is connected.
  Future<void> initialize({required int cols, required int rows});

  /// Rebind to a new SSH client after a reconnect.
  ///
  /// Must be called when the underlying [SshClient] has been replaced
  /// (e.g. after [SshNotifier.reconnectNow]). Backends capture the
  /// client reference at construction and would otherwise keep talking
  /// to the dead socket. Implementations should:
  /// - Cancel any in-flight work tied to the old client
  /// - Swap the internal reference to [newClient]
  /// - Re-establish streams / polling against the new client
  /// - Preserve display state (scrollback, terminal buffer) where possible
  Future<void> rebindSshClient(SshClient newClient);

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

  /// Temporarily extend the scrollback window by [extraLines] additional
  /// history lines so the next capture/poll returns more context.
  ///
  /// Called when the user scrolls to the top of the visible buffer and
  /// wants to look further back. Backends should clamp the total at a
  /// safe cap (~10000 lines) to prevent runaway growth. After extension,
  /// call [boostRefresh] so the user sees the new content promptly.
  ///
  /// Returns the number of lines actually added (may be less than
  /// requested if the cap was hit; 0 if already at cap or unsupported).
  Future<int> extendScrollback(int extraLines);

  /// Drop any runtime scrollback extensions and return to the default
  /// window size. Called when the user taps jump-to-bottom, since
  /// extension is intended for one-off lookups, not a persistent state.
  ///
  /// No-op for backends that do not support runtime extension.
  Future<void> resetScrollback();

  /// Whether currently in tmux copy-mode (always false for raw).
  bool get isInCopyMode;

  /// Whether the active pane is currently showing a fullscreen TUI
  /// (vi, less, htop, …) or tmux's alternate screen buffer.
  ///
  /// When true, the captured content IS the visible pane — there is no
  /// scrollback above it and cursor coordinates describe a position
  /// *inside the editor screen*, not inside an unbounded history.
  /// Callers use this to:
  /// - Render `[row|col]` indicators instead of line-of-lines counters
  /// - Jump to raw content bottom instead of cursor+margin (which can
  ///   sit near the top of the editor and scroll the wrong direction)
  /// - Detect the transition back to a shell prompt, to re-anchor the
  ///   viewport at the bottom when scrollback reinflates.
  ///
  /// - TmuxBackend: `#{alternate_on}` OR `pane_current_command` lookup.
  /// - RawPtyBackend: `Terminal.isUsingAltBuffer`.
  bool get isFullscreen;

  /// Scrollback line count.
  int get scrollbackSize;

  /// Returns the current working directory of the active shell/pane, or
  /// null if the backend cannot determine it.
  ///
  /// - TmuxBackend: reads `#{pane_current_path}` of the active pane.
  /// - RawPtyBackend: returns the cached `$HOME` captured at shell startup
  ///   (the initial CWD for an interactive SSH login). Real `$PWD` tracking
  ///   is not implemented in this release.
  Future<String?> getCurrentPath();

  /// Dispose resources.
  void dispose();
}
