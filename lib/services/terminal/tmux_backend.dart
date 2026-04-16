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
  /// True when [pane_current_command] looks like a fullscreen TUI
  /// (vi/vim/less/htop/…). Used as a fallback when `#{alternate_on}`
  /// stays 0 — some vim configs and older tmux versions don't set
  /// the alt-screen flag, but the running command is still a reliable
  /// hint that scrollback should be suppressed.
  bool _isFullscreenCommand = false;
  bool _isInCopyMode = false;
  int _latency = 0;

  /// Exact command names treated as fullscreen TUIs. Matched
  /// case-insensitively against `#{pane_current_command}`. The
  /// list is intentionally explicit (no fuzzy substring matching)
  /// because false positives — suppressing scrollback for a regular
  /// shell command — also hurt UX. False negatives we *do* want to
  /// catch via the variant prefix rules below.
  static const Set<String> _fullscreenCommandsExact = {
    // vim family (incl. read-only and restricted variants)
    'vi', 'vim', 'view', 'ex', 'rvim', 'rview',
    'vimdiff', 'vimtutor', 'gvim',
    // neovim family
    'nvim', 'neovim',
    // other editors
    'nano', 'pico', 'ee', 'joe', 'mg', 'micro',
    'emacs', 'emacsclient',
    'helix', 'hx',
    'kakoune', 'kak',
    // pagers
    'less', 'more', 'most', 'lv', 'w3m',
    // manual / info viewers
    'man', 'info', 'mandoc',
    // system monitors
    'htop', 'top', 'btop', 'btm', 'glances', 'atop',
    'gtop', 'vtop', 'bashtop', 'bpytop', 'nmon',
    // file managers
    'ncdu', 'nnn', 'ranger', 'mc', 'lf', 'fzf',
    'broot', 'xplr', 'vifm',
    // git TUIs
    'tig', 'lazygit', 'gitui',
    // mail clients
    'mutt', 'neomutt', 'alpine', 'pine',
    // IRC / chat
    'irssi', 'weechat', 'finch', 'gomuks',
    // misc
    'tmux',  // nested tmux session also runs fullscreen
    'screen',
  };

  /// Command-name *prefixes* with required separator. Catches
  /// Debian/Ubuntu vim alternatives (`vim.basic`, `vim.tiny`,
  /// `vim.gtk3`, `vim.nox`, `vim.athena`, `vim-tiny`, …) and
  /// neovim packaging variants (`nvim-qt`, `nvim.appimage`).
  ///
  /// Each entry must match as `<prefix><sep><suffix>` where `<sep>`
  /// is `.` or `-`. Plain `vis`/`vipw` won't match, but `vim.basic`
  /// will. Exact matches are handled by the set above, not here.
  static const List<String> _fullscreenCommandPrefixes = [
    'vim',
    'nvim',
  ];

  /// True when `name` looks like a fullscreen TUI process. Public so
  /// the legacy poll loop in `terminal_screen.dart` can use the same
  /// detection — keeping the two paths in lock-step is the only way
  /// to guarantee scrollback gets suppressed under either loop.
  static bool isFullscreenCommandName(String? name) {
    if (name == null) return false;
    final n = name.trim().toLowerCase();
    if (n.isEmpty) return false;
    if (_fullscreenCommandsExact.contains(n)) return true;
    for (final p in _fullscreenCommandPrefixes) {
      if (n.startsWith('$p.') || n.startsWith('$p-')) return true;
    }
    return false;
  }

  /// Delimiter the poll command embeds between capture-pane content and
  /// the trailing metadata fields (cursor info + pane mode). Uses SOH
  /// (0x01) bytes to make the marker robust against literal text in
  /// pane content — `tmux capture-pane -e` only emits ANSI escapes
  /// (0x1b), never SOH, so collisions are vanishingly rare.
  static const String pollMetaDelimiter = '\x01META\x01';

  /// Pattern of a single line emitted by [TmuxCommands.getCursorPosition]:
  /// `cursor_x,cursor_y,pane_width,pane_height,history_size,alternate_on,pane_current_command`.
  /// Used as a defense-in-depth check by [parsePollOutput] — if this
  /// pattern ever appears as the trailing line of pane content, we
  /// know the META split misfired and would otherwise leak text like
  /// "33,0,56,44,0,0,bash" onto the user's terminal screen for one
  /// frame before the next poll cleans it up.
  static final RegExp _cursorMetaLinePattern =
      RegExp(r'^\d+,\d+,\d+,\d+,\d+,[01],\S*$');

  /// Parses the combined output of one poll iteration into pane
  /// content + the two metadata lines. Returns null when the parse is
  /// not safe to use — callers MUST skip their content update in that
  /// case, leaving the previous frame on screen and letting the next
  /// poll recover. Dumping the raw blob as content (the old fallback)
  /// briefly leaks cursor-metadata text onto the terminal.
  ///
  /// Two failure modes are detected:
  ///
  ///  1. The [pollMetaDelimiter] is missing entirely. Most often a
  ///     transient PTY buffering / shell-restart edge case, but we
  ///     can't tell content from metadata without it.
  ///
  ///  2. The delimiter is present but `contentRaw`'s trailing line
  ///     matches [_cursorMetaLinePattern]. This shouldn't happen with
  ///     correct parsing — but if a future change ever splits at the
  ///     wrong position, this guard catches the leak before it ships.
  static TmuxPollParseResult? parsePollOutput(String combinedOutput) {
    final delimIndex = combinedOutput.lastIndexOf(pollMetaDelimiter);
    if (delimIndex == -1) return null;

    var content = combinedOutput.substring(0, delimIndex);
    if (content.endsWith('\n')) {
      content = content.substring(0, content.length - 1);
    }

    if (content.isNotEmpty) {
      final lastNewline = content.lastIndexOf('\n');
      final lastLine = lastNewline == -1
          ? content
          : content.substring(lastNewline + 1);
      if (_cursorMetaLinePattern.hasMatch(lastLine.trim())) {
        return null;
      }
    }

    final metaPart =
        combinedOutput.substring(delimIndex + pollMetaDelimiter.length);
    final metaLines =
        metaPart.split('\n').where((l) => l.isNotEmpty).toList();
    return TmuxPollParseResult(
      content: content,
      cursorLine: metaLines.isNotEmpty ? metaLines[0] : '',
      paneModeLine: metaLines.length >= 2 ? metaLines[1] : '',
    );
  }

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
  bool get isFullscreen => _isAlternateScreen || _isFullscreenCommand;

  @override
  int get scrollbackSize =>
      (_isAlternateScreen || _isFullscreenCommand) ? 0 : _scrollbackSize;

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

      // When on the alternate screen (vi, less, man, etc.) or running a
      // fullscreen TUI command, or when history_size is 0, don't request
      // scrollback — only capture the visible pane.  This prevents stale
      // content from appearing above fullscreen apps.
      //
      // Both signals are checked because some configs (vim with
      // `set t_ti= t_te=`, old tmux versions) leave `alternate_on` at 0
      // even inside vim; the command name is a reliable fallback.
      final suppressScrollback =
          _isAlternateScreen || _isFullscreenCommand;
      final effectiveScrollback =
          (suppressScrollback || _scrollbackSize == 0) ? 0 : _scrollbackLines;

      // Single combined command: capture-pane, then a unique delimiter,
      // then metadata (cursor info + pane mode).  Using \x01 delimiter
      // makes parsing robust regardless of whether the persistent shell
      // or the exec() fallback is used — we search for the delimiter
      // instead of counting lines from the end.
      final combinedCommand =
          '${TmuxCommands.capturePane(target, escapeSequences: true, startLine: effectiveScrollback > 0 ? -effectiveScrollback : null)}; '
          "printf '\\x01META\\x01\\n'; "
          '${TmuxCommands.getCursorPosition(target)}; '
          '${TmuxCommands.getPaneMode(target)}';

      final combinedOutput = await _sshClient.execPersistent(
        combinedCommand,
        timeout: const Duration(seconds: 2),
      );

      if (_disposed) return;

      // Split on the META delimiter. Returning null here is a deliberate
      // *skip* signal: the previous frame stays on screen and the next
      // poll recovers. The earlier "dump combinedOutput as content"
      // fallback briefly leaked the trailing cursor-metadata line onto
      // the terminal screen (e.g. "33,0,56,44,0,0,bash") whenever the
      // delimiter went missing for a single iteration. Latency / polling
      // cadence are still updated below so the watchdog doesn't react.
      final parsed = parsePollOutput(combinedOutput);
      if (parsed == null) {
        _latency =
            DateTime.now().difference(startTime).inMilliseconds;
        _updatePollingInterval();
        return;
      }
      final contentRaw = parsed.content;
      final cursorOutput = parsed.cursorLine;
      final paneModeOutput = parsed.paneModeLine;

      // Parse cursor position, pane size, alternate_on flag, and the
      // current command name (used as fullscreen-app fallback).
      int? historySize;
      if (cursorOutput.isNotEmpty) {
        // Only split the *first* line — pane_current_command is the
        // last field and is well-formed (single token, no commas), but
        // be defensive in case tmux ever emits a trailing newline.
        final firstLine = cursorOutput.split('\n').first;
        final parts = firstLine.trim().split(',');
        if (parts.length >= 4) {
          final x = int.tryParse(parts[0]);
          final y = int.tryParse(parts[1]);
          final w = int.tryParse(parts[2]);
          final h = int.tryParse(parts[3]);
          historySize = parts.length >= 5 ? int.tryParse(parts[4]) : null;
          _isAlternateScreen =
              parts.length >= 6 && parts[5].trim() == '1';
          final currentCommand = parts.length >= 7 ? parts[6].trim() : null;
          _isFullscreenCommand = isFullscreenCommandName(currentCommand);

          if (x != null) _cursorX = x;
          if (y != null) _cursorY = y;
          if (w != null) _paneWidth = w;
          if (h != null) _paneHeight = h;

          onCursorUpdate?.call(
            _cursorX, _cursorY, w, h, historySize,
          );
        }
      }

      // Gate scrollback reporting on what we actually captured this
      // poll. If `effectiveScrollback` was 0 (fullscreen / alternate
      // screen), the content above is just `_paneHeight` rows — it
      // does NOT contain `historySize` lines of scrollback. Reporting
      // the raw `historySize` here would lie to AnsiTextView, which
      // uses `scrollbackSize` to place the cursor: it would think
      // there are 1988 scrollback rows above the captured 30-row
      // pane, push `cursorLineIndex` off the rendered list, and make
      // scroll-to-bottom jump into a sea of empty trailing lines.
      // Matches the duplicate poll path in terminal_screen.dart.
      _scrollbackSize =
          effectiveScrollback == 0 ? 0 : (historySize ?? _scrollbackSize);

      // Copy-mode detection
      final paneMode = paneModeOutput.trim();
      final wasCopyMode = _isInCopyMode;
      _isInCopyMode = paneMode.isNotEmpty;
      if (_isInCopyMode != wasCopyMode) {
        onCopyModeChange?.call(_isInCopyMode);
      }

      // If we just detected a fullscreen app (alternate screen flag OR
      // a known TUI command) but captured with scrollback this poll
      // (one-poll lag), strip the scrollback portion so only the visible
      // pane content remains.  The NEXT poll will use effectiveScrollback=0.
      //
      // Without this fallback path, users whose vim runs without
      // alternate-screen toggling would see the previous shell output
      // above the editor and have to scroll down to find the cursor.
      var processedOutput = contentRaw;
      final detectedFullscreen = _isAlternateScreen || _isFullscreenCommand;
      if (detectedFullscreen && effectiveScrollback > 0 && _paneHeight > 0) {
        final lines = processedOutput.split('\n');
        if (lines.length > _paneHeight) {
          processedOutput = lines.sublist(lines.length - _paneHeight).join('\n');
        }
      }

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

/// Parsed components of a single poll iteration's combined output.
/// See [TmuxBackend.parsePollOutput] — a null return there means the
/// poller should skip its content update for this frame.
class TmuxPollParseResult {
  /// The capture-pane portion, with its trailing newline stripped.
  final String content;

  /// The cursor / dimensions / fullscreen-hint line emitted by
  /// [TmuxCommands.getCursorPosition]. Empty if missing.
  final String cursorLine;

  /// The pane-mode line emitted by [TmuxCommands.getPaneMode].
  /// Empty when not in tmux copy-mode (which is the common case).
  final String paneModeLine;

  const TmuxPollParseResult({
    required this.content,
    required this.cursorLine,
    required this.paneModeLine,
  });
}
