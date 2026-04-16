import 'dart:async';

import '../ssh/ssh_client.dart';
import '../tmux/tmux_commands.dart';
import 'terminal_backend.dart';

/// Callback signature for when cursor position / pane size is updated from polling.
typedef CursorUpdateCallback = void Function(
  int cursorX,
  int cursorY,
  int? paneWidth,
  int? paneHeight,
  int? historySize,
);

/// Callback signature for when tmux copy-mode state changes.
typedef CopyModeCallback = void Function(bool isInCopyMode);

/// Callback signature for adaptive polling interval recommendation.
typedef PollingIntervalCallback = int Function();

/// TmuxBackend — polls capture-pane, sends keys via tmux send-keys.
///
/// Extracted from terminal_screen.dart to implement [TerminalBackend].
class TmuxBackend implements TerminalBackend {
  SshClient _sshClient;
  final String? Function() _getCurrentTarget;
  final CursorUpdateCallback? onCursorUpdate;
  final CopyModeCallback? onCopyModeChange;
  final PollingIntervalCallback? getRecommendedInterval;

  /// Baseline scrollback window size set at construction — the value the
  /// user configured in settings. [resetScrollback] snaps back to this.
  final int _defaultScrollbackLines;

  /// Current scrollback window size, possibly extended at runtime via
  /// [extendScrollback] when the user scrolls past the top of the buffer.
  int _scrollbackLines;

  /// Hard ceiling on [_scrollbackLines] to prevent runaway memory/SSH
  /// transfer cost from repeated top-of-buffer extensions.
  static const int _maxScrollbackLines = 10000;

  /// Current (possibly extended) scrollback window size in lines.
  int get scrollbackLines => _scrollbackLines;

  // Polling state
  Timer? _pollTimer;
  bool _isPolling = false;
  bool _disposed = false;

  // Adaptive polling
  int _currentPollingInterval = 100;
  static const int _minPollingInterval = 50;
  static const int _maxPollingInterval = 2000;

  // Content state
  String _currentContent = '';
  int _cursorX = 0;
  int _cursorY = 0;
  int _paneWidth = 80;
  int _paneHeight = 24;
  int _scrollbackSize = 0;
  bool _isAlternateScreen = false;
  bool _isInCopyMode = false;
  int _latency = 0;

  final _contentController = StreamController<void>.broadcast();

  TmuxBackend({
    required SshClient sshClient,
    required String? Function() getCurrentTarget,
    this.onCursorUpdate,
    this.onCopyModeChange,
    this.getRecommendedInterval,
    int scrollbackLines = 100,
  })  : _sshClient = sshClient,
        _getCurrentTarget = getCurrentTarget,
        _defaultScrollbackLines = scrollbackLines,
        _scrollbackLines = scrollbackLines;

  @override
  bool get supportsNavigation => true;

  @override
  bool get isInCopyMode => _isInCopyMode;

  @override
  int get scrollbackSize => _isAlternateScreen ? 0 : _scrollbackSize;

  @override
  String get currentContent => _currentContent;

  @override
  ({int x, int y}) get cursorPosition => (x: _cursorX, y: _cursorY);

  @override
  ({int width, int height}) get dimensions => (width: _paneWidth, height: _paneHeight);

  /// Latency of last poll in milliseconds.
  int get latency => _latency;

  /// The max polling interval cap (exposed for copy-mode override).
  int get maxPollingInterval => _maxPollingInterval;

  /// Set polling interval cap externally (e.g. for copy-mode detection).
  void setPollingIntervalCap(int maxMs) {
    _currentPollingInterval = _currentPollingInterval.clamp(
      _minPollingInterval,
      maxMs,
    );
  }

  @override
  Future<void> initialize({required int cols, required int rows}) async {
    _paneWidth = cols;
    _paneHeight = rows;
    _startPolling();
  }

  @override
  Future<void> rebindSshClient(SshClient newClient) async {
    // Stop polling against the dead client, swap in the new one, and
    // reset polling state so the next tick hits the fresh socket.
    _pollTimer?.cancel();
    _isPolling = false;
    _sshClient = newClient;
    _currentPollingInterval = _minPollingInterval;
    if (!_disposed) {
      _scheduleNextPoll();
    }
  }

  // ---------------------------------------------------------------------------
  // Polling
  // ---------------------------------------------------------------------------

  void _startPolling() {
    _pollTimer?.cancel();
    _scheduleNextPoll();
  }

  void _scheduleNextPoll() {
    if (_disposed) return;
    _pollTimer?.cancel();
    _pollTimer = Timer(
      Duration(milliseconds: _currentPollingInterval),
      () async {
        await _pollPaneContent();
        _scheduleNextPoll();
      },
    );
  }

  @override
  void boostRefresh() {
    _currentPollingInterval = _minPollingInterval;
    _pollTimer?.cancel();
    _scheduleNextPoll();
  }

  @override
  Future<int> extendScrollback(int extraLines) async {
    if (extraLines <= 0 || _disposed) return 0;
    if (_scrollbackLines >= _maxScrollbackLines) return 0;
    final newTotal = (_scrollbackLines + extraLines).clamp(
      _defaultScrollbackLines,
      _maxScrollbackLines,
    );
    final added = newTotal - _scrollbackLines;
    if (added <= 0) return 0;
    _scrollbackLines = newTotal;
    boostRefresh();
    return added;
  }

  @override
  Future<void> resetScrollback() async {
    if (_scrollbackLines == _defaultScrollbackLines) return;
    _scrollbackLines = _defaultScrollbackLines;
    boostRefresh();
  }

  void _updatePollingInterval() {
    if (getRecommendedInterval != null) {
      final recommended = getRecommendedInterval!();
      _currentPollingInterval = recommended.clamp(
        _minPollingInterval,
        _maxPollingInterval,
      );
    }
  }

  Future<void> _pollPaneContent() async {
    if (_isPolling || _disposed) return;
    _isPolling = true;

    try {
      if (!_sshClient.isConnected) {
        _isPolling = false;
        return;
      }

      final target = _getCurrentTarget();
      if (target == null) {
        _isPolling = false;
        return;
      }

      final startTime = DateTime.now();

      // --- Phase 1: query pane metadata (cursor, size, alternate_on,
      // pane_mode) so we know the screen state BEFORE deciding how much
      // scrollback to capture.  This costs one lightweight display-message
      // round-trip but prevents the one-poll lag that caused old shell
      // content to bleed into fullscreen apps like vi.
      final metaCommand =
          '${TmuxCommands.getCursorPosition(target)}; '
          '${TmuxCommands.getPaneMode(target)}';

      final metaOutput = await _sshClient.execPersistent(
        metaCommand,
        timeout: const Duration(seconds: 2),
      );

      if (_disposed) return;

      // Parse metadata — cursor info is first line, pane mode is second.
      final metaLines = metaOutput.split('\n');
      final cursorOutput = metaLines.isNotEmpty ? metaLines[0] : '';
      final paneModeOutput = metaLines.length >= 2 ? metaLines[1] : '';

      // Parse cursor position, pane size, and alternate_on flag BEFORE
      // building the capture command so effectiveScrollback is accurate.
      int? historySize;
      if (cursorOutput.isNotEmpty) {
        final parts = cursorOutput.trim().split(',');
        if (parts.length >= 4) {
          final x = int.tryParse(parts[0]);
          final y = int.tryParse(parts[1]);
          final w = int.tryParse(parts[2]);
          final h = int.tryParse(parts[3]);
          historySize = parts.length >= 5 ? int.tryParse(parts[4]) : null;
          _isAlternateScreen =
              parts.length >= 6 && parts[5].trim() == '1';

          if (x != null) _cursorX = x;
          if (y != null) _cursorY = y;
          if (w != null) _paneWidth = w;
          if (h != null) _paneHeight = h;

          onCursorUpdate?.call(
            _cursorX, _cursorY, w, h, historySize,
          );
        }
      }

      _scrollbackSize = historySize ?? _scrollbackSize;

      // Copy-mode detection
      final paneMode = paneModeOutput.trim();
      final wasCopyMode = _isInCopyMode;
      _isInCopyMode = paneMode.isNotEmpty;
      if (_isInCopyMode != wasCopyMode) {
        onCopyModeChange?.call(_isInCopyMode);
      }

      // --- Phase 2: capture pane content using the now-accurate flags.
      // When on the alternate screen (vi, less, man, etc.) or history_size
      // is 0, don't request scrollback — only capture the visible pane.
      // This prevents stale content from appearing above fullscreen apps.
      final effectiveScrollback =
          (_isAlternateScreen || _scrollbackSize == 0) ? 0 : _scrollbackLines;
      final captureCommand = TmuxCommands.capturePane(
        target,
        escapeSequences: true,
        startLine: effectiveScrollback > 0 ? -effectiveScrollback : null,
      );

      final output = await _sshClient.execPersistent(
        captureCommand,
        timeout: const Duration(seconds: 2),
      );

      if (_disposed) return;

      final processedOutput = output.endsWith('\n')
          ? output.substring(0, output.length - 1)
          : output;

      final endTime = DateTime.now();
      _latency = endTime.difference(startTime).inMilliseconds;

      // Update content only when it actually changed.
      if (processedOutput != _currentContent) {
        _currentContent = processedOutput;
        _contentController.add(null);
      }

      _updatePollingInterval();
    } catch (_) {
      // Poll errors are silently ignored (retried on next poll)
    } finally {
      _isPolling = false;
    }
  }

  // ---------------------------------------------------------------------------
  // Key sending
  // ---------------------------------------------------------------------------

  @override
  Future<void> sendText(String text) async {
    if (!_sshClient.isConnected) return;
    final target = _getCurrentTarget();
    if (target == null) return;

    try {
      await _sshClient.exec(TmuxCommands.sendKeys(target, text, literal: true));
      boostRefresh();
    } catch (_) {}
  }

  @override
  Future<void> sendSpecialKey(String tmuxKey, {String? escapeSequence}) async {
    if (!_sshClient.isConnected) return;
    final target = _getCurrentTarget();
    if (target == null) return;

    try {
      // Whitespace-separated chords like "C-b c" (new window) must be
      // sent as separate positional args to `tmux send-keys` so tmux
      // reads them as a sequence, not as one literal key name. The
      // default sendKeys path would escape the whole string into a
      // single quoted arg, which tmux rejects as an unknown key.
      final cmd = tmuxKey.contains(RegExp(r'\s'))
          ? TmuxCommands.sendKeySequence(target, tmuxKey)
          : TmuxCommands.sendKeys(target, tmuxKey, literal: false);
      await _sshClient.exec(cmd);
      boostRefresh();
    } catch (_) {}
  }

  @override
  Future<String?> getCurrentPath() async {
    if (!_sshClient.isConnected) return null;
    final target = _getCurrentTarget();
    if (target == null) return null;
    try {
      final out = await _sshClient.exec(TmuxCommands.paneCurrentPath(target));
      final trimmed = out.trim();
      return trimmed.isEmpty ? null : trimmed;
    } catch (_) {
      return null;
    }
  }

  /// Enter tmux copy-mode.
  Future<void> enterCopyMode() async {
    if (!_sshClient.isConnected) return;
    final target = _getCurrentTarget();
    if (target == null) return;
    try {
      await _sshClient.exec(TmuxCommands.enterCopyMode(target));
      boostRefresh();
    } catch (_) {}
  }

  /// Cancel tmux copy-mode.
  Future<void> cancelCopyMode() async {
    if (!_sshClient.isConnected) return;
    final target = _getCurrentTarget();
    if (target == null) return;
    try {
      await _sshClient.exec(TmuxCommands.cancelCopyMode(target));
      boostRefresh();
    } catch (_) {}
  }

  // ---------------------------------------------------------------------------
  // Resize
  // ---------------------------------------------------------------------------

  @override
  Future<void> resize(int cols, int rows) async {
    if (!_sshClient.isConnected) return;
    final target = _getCurrentTarget();
    if (target == null) return;

    try {
      await _sshClient.exec(
        TmuxCommands.resizePaneToSize(target, cols: cols, rows: rows),
      );
      _paneWidth = cols;
      _paneHeight = rows;
      boostRefresh();
    } catch (_) {}
  }

  // ---------------------------------------------------------------------------
  // Stream / lifecycle
  // ---------------------------------------------------------------------------

  @override
  Stream<void> get contentUpdates => _contentController.stream;

  @override
  void dispose() {
    _disposed = true;
    _pollTimer?.cancel();
    _contentController.close();
  }
}
