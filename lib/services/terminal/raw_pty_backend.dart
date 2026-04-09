import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:xterm/xterm.dart' as xterm;

import '../ssh/ssh_client.dart';
import 'terminal_backend.dart';

/// RawPtyBackend — direct SSH shell with xterm.dart headless VT state machine.
///
/// Uses [SshClient.startShell()] for an interactive PTY session.
/// The xterm [Terminal] model processes VT sequences and maintains screen state.
/// Screen content is serialized back to ANSI text for [AnsiTextView].
class RawPtyBackend implements TerminalBackend {
  final SshClient _sshClient;
  final _contentController = StreamController<void>.broadcast();

  late final xterm.Terminal _terminal;
  int _cols;
  int _rows;

  String _cachedContent = '';
  bool _contentDirty = true;
  bool _disposed = false;

  /// Tmux key name → VT escape sequence mapping.
  /// Used when callers provide tmux key names (from action bar, nav pad).
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
    'PageDown': '\x1b[6~',
    'Insert': '\x1b[2~',
    'Delete': '\x1b[3~',
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
  })  : _sshClient = sshClient,
        _cols = cols,
        _rows = rows;

  @override
  bool get supportsNavigation => false;

  @override
  bool get isInCopyMode => false;

  @override
  int get scrollbackSize => _terminal.buffer.lines.length - _rows;

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
    _terminal = xterm.Terminal(maxLines: 1000);
    _terminal.resize(cols, rows);

    // Start interactive PTY shell
    _sshClient.updateEventHandlers(onData: _onShellData);
    await _sshClient.startShell(ShellOptions(
      term: 'xterm-256color',
      cols: cols,
      rows: rows,
    ));
  }

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
    final height = _rows;

    for (int y = 0; y < height; y++) {
      final lineIndex = buffer.absoluteY - height + 1 + y;
      if (lineIndex < 0 || lineIndex >= buffer.lines.length) {
        buf.writeln();
        continue;
      }
      final line = buffer.lines[lineIndex];
      buf.writeln(_lineToAnsi(line));
    }

    return buf.toString();
  }

  /// Convert a single buffer line to an ANSI-escaped string.
  String _lineToAnsi(xterm.BufferLine line) {
    final buf = StringBuffer();
    int prevFg = -1;
    int prevBg = -1;
    int prevFlags = 0;
    bool hasStyle = false;

    final length = line.length;
    // Find last non-space character to trim trailing spaces
    int lastNonSpace = -1;
    for (int x = length - 1; x >= 0; x--) {
      final cp = line.cellGetContent(x);
      if (cp != 0 && cp != 32) {
        lastNonSpace = x;
        break;
      }
    }

    for (int x = 0; x <= lastNonSpace; x++) {
      final cp = line.cellGetContent(x);
      final width = line.cellGetWidth(x);

      // Skip trailing cells of wide characters
      if (width == 0) continue;

      final fg = line.cellGetFg(x);
      final bg = line.cellGetBg(x);
      final flags = line.cellGetFlags(x);

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

    // Flags (bold, italic, underline, etc.)
    if (flags & xterm.CellFlags.bold != 0) params.add(1);
    if (flags & xterm.CellFlags.dim != 0) params.add(2);
    if (flags & xterm.CellFlags.italic != 0) params.add(3);
    if (flags & xterm.CellFlags.underline != 0) params.add(4);
    if (flags & xterm.CellFlags.blink != 0) params.add(5);
    if (flags & xterm.CellFlags.inverse != 0) params.add(7);
    if (flags & xterm.CellFlags.invisible != 0) params.add(8);
    if (flags & xterm.CellFlags.strikethrough != 0) params.add(9);

    // Foreground color
    _emitColor(params, fg, true);

    // Background color
    _emitColor(params, bg, false);

    if (params.isNotEmpty) {
      buf.write('\x1b[${params.join(";")}m');
    }
  }

  /// Emit color parameters for SGR sequence.
  void _emitColor(List<int> params, int color, bool isForeground) {
    if (color == 0) return; // Default color, no param needed

    final base = isForeground ? 30 : 40;

    // xterm.dart encodes colors as packed int:
    // - Bits 0-23: RGB value
    // - Bits 24-25: color type (0=default, 1=named, 2=palette, 3=rgb)
    final type = (color >> 24) & 0x03;

    switch (type) {
      case 1: // Named color (0-7 standard, 8-15 bright)
        final index = color & 0xFF;
        if (index < 8) {
          params.add(base + index);
        } else {
          params.add(base + 60 + (index - 8));
        }
      case 2: // 256-color palette
        final index = color & 0xFF;
        params.addAll([isForeground ? 38 : 48, 5, index]);
      case 3: // 24-bit RGB
        final r = (color >> 16) & 0xFF;
        final g = (color >> 8) & 0xFF;
        final b = color & 0xFF;
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

    // Fall back to tmux key name → escape sequence mapping
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

    // Last resort: send as literal text
    _sshClient.write(tmuxKey);
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
  Stream<void> get contentUpdates => _contentController.stream;

  @override
  void dispose() {
    _disposed = true;
    _contentController.close();
  }
}
