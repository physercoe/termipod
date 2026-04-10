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
  final int scrollbackLines;

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
  bool _isInCopyMode = false;
  int _latency = 0;

  final _contentController = StreamController<void>.broadcast();

  TmuxBackend({
    required SshClient sshClient,
    required String? Function() getCurrentTarget,
    this.onCursorUpdate,
    this.onCopyModeChange,
    this.getRecommendedInterval,
    this.scrollbackLines = 100,
  })  : _sshClient = sshClient,
        _getCurrentTarget = getCurrentTarget;

  @override
  bool get supportsNavigation => true;

  @override
  bool get isInCopyMode => _isInCopyMode;

  @override
  int get scrollbackSize => _scrollbackSize;

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

      final combinedCommand =
          '${TmuxCommands.capturePane(target, escapeSequences: true, startLine: -scrollbackLines)}; '
          '${TmuxCommands.getCursorPosition(target)}; '
          '${TmuxCommands.getPaneMode(target)}';

      final combinedOutput = await _sshClient.execPersistent(
        combinedCommand,
        timeout: const Duration(seconds: 2),
      );

      if (_disposed) return;

      // Split output: last line = pane_mode, second-to-last = cursor info, rest = content
      final lines = combinedOutput.split('\n');
      final paneModeOutput = lines.isNotEmpty ? lines.removeLast() : '';
      final cursorOutput = lines.isNotEmpty ? lines.removeLast() : '';
      final output = lines.join('\n');

      final processedOutput = output.endsWith('\n')
          ? output.substring(0, output.length - 1)
          : output;

      final endTime = DateTime.now();
      _latency = endTime.difference(startTime).inMilliseconds;

      // Parse cursor position and pane size
      int? historySize;
      if (cursorOutput.isNotEmpty) {
        final parts = cursorOutput.trim().split(',');
        if (parts.length >= 4) {
          final x = int.tryParse(parts[0]);
          final y = int.tryParse(parts[1]);
          final w = int.tryParse(parts[2]);
          final h = int.tryParse(parts[3]);
          historySize = parts.length >= 5 ? int.tryParse(parts[4]) : null;

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

      // Update content
      if (processedOutput != _currentContent || _latency != latency) {
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
