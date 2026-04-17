import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:xterm/core.dart';

import '../ssh/ssh_client.dart';
import 'terminal_backend.dart';

/// RawPtyBackend — direct SSH shell with xterm.dart headless VT state machine.
///
/// Uses [SshClient.startShell()] for an interactive PTY session.
/// The xterm [Terminal] model processes VT sequences and maintains screen state.
/// Screen content is serialized back to ANSI text for [AnsiTextView].
class RawPtyBackend implements TerminalBackend {
  SshClient _sshClient;
  final _contentController = StreamController<void>.broadcast();

  late final Terminal _terminal;
  int _cols;
  int _rows;

  /// Max scrollback lines to keep (user-configurable via settings).
  /// Caps xterm's buffer so `_extractAnsiContent` never emits more
  /// scrollback rows than `AnsiTextView._computeCursorLineIndex` is
  /// willing to index into (it clamps at `settings.scrollbackLines`).
  /// Mismatch there would re-introduce the blank-area-above-cursor
  /// bug we're fixing here.
  final int _scrollbackLines;

  String _cachedContent = '';
  bool _contentDirty = true;
  bool _disposed = false;

  /// Cached `$HOME` fetched once at shell startup. Used by [getCurrentPath]
  /// as the default CWD for file downloads. Null if the probe failed.
  String? _homeDir;

  /// Tmux key name -> VT escape sequence mapping.
  /// Used when callers provide tmux key names (from action bar, nav pad).
  /// Includes both standard names and tmux aliases (e.g., PPage/PageUp)
  /// so action bar buttons work correctly in raw PTY mode.
  static const Map<String, String> _keyToEscape = {
    'Enter': '\r',
    'S-Enter': '\n',
    'Escape': '\x1b',
    'Tab': '\t',
    'BTab': '\x1b[Z',
    'BSpace': '\x7f',
    'Up': '\x1b[A',
    'Down': '\x1b[B',
    'Right': '\x1b[C',
    'Left': '\x1b[D',
    'Home': '\x1b[H',
    'End': '\x1b[F',
    'PageUp': '\x1b[5~',
    'PPage': '\x1b[5~', // tmux alias
    'PageDown': '\x1b[6~',
    'NPage': '\x1b[6~', // tmux alias
    'Insert': '\x1b[2~',
    'Delete': '\x1b[3~',
    'DC': '\x1b[3~', // tmux alias
    'F1': '\x1bOP',
    'F2': '\x1bOQ',
    'F3': '\x1bOR',
    'F4': '\x1bOS',
    'F5': '\x1b[15~',
    'F6': '\x1b[17~',
    'F7': '\x1b[18~',
    'F8': '\x1b[19~',
    'F9': '\x1b[20~',
    'F10': '\x1b[21~',
    'F11': '\x1b[23~',
    'F12': '\x1b[24~',
    'C-a': '\x01',
    'C-b': '\x02',
    'C-c': '\x03',
    'C-d': '\x04',
    'C-e': '\x05',
    'C-f': '\x06',
    'C-g': '\x07',
    'C-h': '\x08',
    'C-i': '\x09',
    'C-j': '\x0a',
    'C-k': '\x0b',
    'C-l': '\x0c',
    'C-m': '\x0d',
    'C-n': '\x0e',
    'C-o': '\x0f',
    'C-p': '\x10',
    'C-q': '\x11',
    'C-r': '\x12',
    'C-s': '\x13',
    'C-t': '\x14',
    'C-u': '\x15',
    'C-v': '\x16',
    'C-w': '\x17',
    'C-x': '\x18',
    'C-y': '\x19',
    'C-z': '\x1a',
    'C-\\': '\x1c',
    'C-]': '\x1d',
    'C-^': '\x1e',
    'C-_': '\x1f',
    'Space': ' ',
  };

  RawPtyBackend({
    required SshClient sshClient,
    int cols = 80,
    int rows = 24,
    int scrollbackLines = 100,
  })  : _sshClient = sshClient,
        _cols = cols,
        _rows = rows,
        _scrollbackLines = scrollbackLines;

  @override
  bool get supportsNavigation => false;

  @override
  bool get isInCopyMode => false;

  @override
  bool get isFullscreen => _terminal.isUsingAltBuffer;

  @override
  int get scrollbackSize {
    final sb = _terminal.buffer.scrollBack;
    return sb > 0 ? sb : 0;
  }

  @override
  ({int x, int y}) get cursorPosition =>
      (x: _terminal.buffer.cursorX, y: _terminal.buffer.cursorY);

  @override
  ({int width, int height}) get dimensions => (width: _cols, height: _rows);

  @override
  String get currentContent {
    if (_contentDirty) {
      _cachedContent = _extractAnsiContent();
      _contentDirty = false;
    }
    return _cachedContent;
  }

  @override
  Future<void> initialize({required int cols, required int rows}) async {
    _cols = cols;
    _rows = rows;
    // maxLines = visible rows + allowed scrollback. Using a fixed 1000
    // here previously meant xterm kept up to 976 scrollback rows, but
    // `_extractAnsiContent` only emitted the visible 24 — AnsiTextView
    // then tried to index the cursor at row `scrollbackSize + cursorY`
    // into a content buffer that ended at row 24, causing a growing
    // blank area above the cursor as history accumulated.
    _terminal = Terminal(maxLines: rows + _scrollbackLines);
    _terminal.resize(cols, rows);

    // Probe $HOME on a throwaway exec channel before starting the shell.
    // dartssh2 serializes channels, so we must run this BEFORE startShell()
    // rather than concurrently. Cost: one extra round-trip (~50-100ms) that
    // happens inside the connection spinner and is invisible to users.
    try {
      final out = await _sshClient.exec('printf %s "\$HOME"');
      final trimmed = out.trim();
      if (trimmed.isNotEmpty) _homeDir = trimmed;
    } catch (_) {
      // Ignore — getCurrentPath() will return null and callers will
      // fall back to settings.fileRemotePath.
    }

    // Start interactive PTY shell
    _sshClient.updateEventHandlers(onData: _onShellData);
    await _sshClient.startShell(ShellOptions(
      term: 'xterm-256color',
      cols: cols,
      rows: rows,
    ));
  }

  @override
  Future<void> rebindSshClient(SshClient newClient) async {
    if (_disposed) return;

    // Swap to the fresh client and re-register the shell data handler.
    // The xterm.dart Terminal buffer is kept intact so users don't lose
    // on-screen content or scrollback across the reconnect.
    _sshClient = newClient;
    _sshClient.updateEventHandlers(onData: _onShellData);

    // Re-probe $HOME on the new client. Failure is non-fatal — the
    // previous value (if any) is kept as a best-effort fallback.
    try {
      final out = await _sshClient.exec('printf %s "\$HOME"');
      final trimmed = out.trim();
      if (trimmed.isNotEmpty) _homeDir = trimmed;
    } catch (_) {}

    // Start a fresh interactive shell on the new client. Without this
    // the new connection has no PTY and _sshClient.write() would throw
    // "shell not started".
    try {
      await _sshClient.startShell(ShellOptions(
        term: 'xterm-256color',
        cols: _cols,
        rows: _rows,
      ));
    } catch (_) {
      // If the shell fails to start, leave the backend idle; the next
      // reconnect attempt will try again.
      return;
    }

    // Nudge the view so it repaints at least once after the rebind —
    // the new shell prompt typically arrives on its own, but this
    // keeps the latency/dimensions fresh even if it doesn't.
    _contentDirty = true;
    _contentController.add(null);
  }

  @override
  Future<String?> getCurrentPath() async => _homeDir;

  void _onShellData(Uint8List data) {
    if (_disposed) return;
    _terminal.write(utf8.decode(data, allowMalformed: true));
    _contentDirty = true;
    _contentController.add(null);
  }

  // ---------------------------------------------------------------------------
  // ANSI content extraction from xterm Terminal buffer
  // ---------------------------------------------------------------------------

  String _extractAnsiContent() {
    final buf = StringBuffer();
    final buffer = _terminal.buffer;
    final sb = buffer.scrollBack;

    // Emit scrollback lines first (0..sb-1), then visible rows
    // (sb..sb+rows-1). AnsiTextView expects parsedLines to contain
    // scrollbackSize history rows followed by the visible pane so that
    // `cursorLineIndex = scrollbackSize + cursorY` points at the right
    // row. Without scrollback, history was hidden AND the cursor index
    // pointed into empty trailing rows above the real content.
    final totalRows = sb + _rows;
    for (int i = 0; i < totalRows; i++) {
      if (i < 0 || i >= buffer.lines.length) {
        buf.writeln();
        continue;
      }
      buf.writeln(_lineToAnsi(buffer.lines[i]));
    }

    return buf.toString();
  }

  /// Convert a single buffer line to an ANSI-escaped string.
  String _lineToAnsi(BufferLine line) {
    final buf = StringBuffer();
    int prevFg = -1;
    int prevBg = -1;
    int prevFlags = 0;
    bool hasStyle = false;

    final length = line.length;
    // Find last non-space character to trim trailing spaces
    int lastNonSpace = -1;
    for (int x = length - 1; x >= 0; x--) {
      final cp = line.getCodePoint(x);
      if (cp != 0 && cp != 32) {
        lastNonSpace = x;
        break;
      }
    }

    for (int x = 0; x <= lastNonSpace; x++) {
      final cp = line.getCodePoint(x);
      final width = line.getWidth(x);

      // Skip trailing cells of wide characters
      if (width == 0) continue;

      final fg = line.getForeground(x);
      final bg = line.getBackground(x);
      final flags = line.getAttributes(x);

      // Emit SGR if attributes changed
      if (fg != prevFg || bg != prevBg || flags != prevFlags) {
        _emitSgr(buf, fg, bg, flags, hasStyle);
        prevFg = fg;
        prevBg = bg;
        prevFlags = flags;
        hasStyle = true;
      }

      if (cp == 0) {
        buf.write(' ');
      } else {
        buf.writeCharCode(cp);
      }
    }

    // Reset at end of line
    if (hasStyle) {
      buf.write('\x1b[0m');
    }

    return buf.toString();
  }

  /// Emit SGR escape sequence for attribute changes.
  void _emitSgr(StringBuffer buf, int fg, int bg, int flags, bool hadStyle) {
    final params = <int>[];

    // Reset first if we had previous style
    if (hadStyle) {
      params.add(0);
    }

    // Flags — CellAttr bit positions from xterm.dart cell.dart
    if (flags & CellAttr.bold != 0) params.add(1);
    if (flags & CellAttr.faint != 0) params.add(2);
    if (flags & CellAttr.italic != 0) params.add(3);
    if (flags & CellAttr.underline != 0) params.add(4);
    if (flags & CellAttr.blink != 0) params.add(5);
    if (flags & CellAttr.inverse != 0) params.add(7);
    if (flags & CellAttr.invisible != 0) params.add(8);
    if (flags & CellAttr.strikethrough != 0) params.add(9);

    // Foreground color
    _emitColor(params, fg, true);

    // Background color
    _emitColor(params, bg, false);

    if (params.isNotEmpty) {
      buf.write('\x1b[${params.join(";")}m');
    }
  }

  /// Emit color parameters for SGR sequence.
  ///
  /// xterm.dart CellColor encoding:
  /// - Bits 0-24: value (RGB or index)
  /// - Bits 25-26: type (normal=0, named=1, palette=2, rgb=3)
  void _emitColor(List<int> params, int color, bool isForeground) {
    if (color == 0) return; // Default color, no param needed

    final type = (color >> CellColor.typeShift) & 0x03;
    final base = isForeground ? 30 : 40;

    switch (type) {
      case 1: // Named color (0-7 standard, 8-15 bright)
        final index = color & CellColor.valueMask;
        if (index < 8) {
          params.add(base + index);
        } else if (index < 16) {
          params.add(base + 60 + (index - 8));
        }
      case 2: // 256-color palette
        final index = color & CellColor.valueMask;
        params.addAll([isForeground ? 38 : 48, 5, index]);
      case 3: // 24-bit RGB
        final value = color & CellColor.valueMask;
        final r = (value >> 16) & 0xFF;
        final g = (value >> 8) & 0xFF;
        final b = value & 0xFF;
        params.addAll([isForeground ? 38 : 48, 2, r, g, b]);
    }
  }

  // ---------------------------------------------------------------------------
  // Key sending
  // ---------------------------------------------------------------------------

  @override
  Future<void> sendText(String text) async {
    if (!_sshClient.isConnected) return;
    _sshClient.write(text);
  }

  @override
  Future<void> sendSpecialKey(String tmuxKey, {String? escapeSequence}) async {
    if (!_sshClient.isConnected) return;

    // Prefer escape sequence if provided (from KeyInputEvent.data)
    if (escapeSequence != null && escapeSequence.isNotEmpty) {
      _sshClient.write(escapeSequence);
      return;
    }

    // Direct lookup in the key map
    final seq = _keyToEscape[tmuxKey];
    if (seq != null) {
      _sshClient.write(seq);
      return;
    }

    // Handle dynamic C-<letter> patterns not in the static map
    if (tmuxKey.startsWith('C-') && tmuxKey.length == 3) {
      final char = tmuxKey[2].toLowerCase();
      final code = char.codeUnitAt(0) - 'a'.codeUnitAt(0) + 1;
      if (code >= 1 && code <= 26) {
        _sshClient.write(String.fromCharCode(code));
        return;
      }
    }

    // Handle Alt/Meta combos: M-<char> → ESC + char
    if (tmuxKey.startsWith('M-') && tmuxKey.length == 3) {
      _sshClient.write('\x1b${tmuxKey[2]}');
      return;
    }

    // Handle space-separated key sequences (e.g., "Escape Escape", "C-b c").
    // Each token is resolved independently through this same method.
    if (tmuxKey.contains(' ')) {
      for (final part in tmuxKey.split(' ')) {
        await sendSpecialKey(part.trim());
      }
      return;
    }

    // Single printable character — send as literal (safe for 1-char strings
    // like "y", "n", ":" from action bar confirm/literal buttons).
    if (tmuxKey.length == 1) {
      _sshClient.write(tmuxKey);
      return;
    }

    // Unknown multi-character key name — drop silently rather than
    // injecting garbage into the terminal (e.g., vi interprets each
    // letter of "PPage" as a separate command).
  }

  // ---------------------------------------------------------------------------
  // Resize
  // ---------------------------------------------------------------------------

  @override
  Future<void> resize(int cols, int rows) async {
    _cols = cols;
    _rows = rows;
    _terminal.resize(cols, rows);
    _sshClient.resize(cols, rows);
    _contentDirty = true;
    _contentController.add(null);
  }

  // ---------------------------------------------------------------------------
  // Stream / lifecycle
  // ---------------------------------------------------------------------------

  @override
  void boostRefresh() {
    // No-op for raw PTY (stream-driven, not polled)
  }

  @override
  void pausePolling() {
    // No-op for raw PTY — there is no poll timer to pause. Destructive
    // operations on tmux don't apply here; raw PTY's "operations" all
    // go through the stream-driven SSH shell directly.
  }

  @override
  void resumePolling() {
    // No-op (see pausePolling).
  }

  @override
  bool get isPolling => false;

  @override
  Stream<void> get pollHeartbeat => _contentController.stream;

  @override
  Future<int> extendScrollback(int extraLines) async {
    // No-op: xterm.dart Terminal uses a fixed maxLines buffer that can't
    // be resized at runtime without reconstructing the whole terminal.
    // Raw PTY already ships with a larger default scrollback than tmux.
    return 0;
  }

  @override
  Future<void> resetScrollback() async {
    // No-op — see extendScrollback.
  }

  @override
  Stream<void> get contentUpdates => _contentController.stream;

  @override
  void dispose() {
    _disposed = true;
    _contentController.close();
  }
}
