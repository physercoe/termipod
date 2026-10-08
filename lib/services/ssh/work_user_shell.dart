import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:dartssh2/dartssh2.dart';

import 'command_executor.dart';

/// A private PTY authenticates su once; a non-interactive shell then serves
/// framed commands as the target user. It never falls back to the login user.
class WorkUserShell {
  WorkUserShell(this._transport)
    : _nonce = List.generate(
        16,
        (_) => Random.secure().nextInt(256).toRadixString(16).padLeft(2, '0'),
      ).join();

  final SSHClient _transport;
  final String _nonce;
  SSHSession? _session;
  final _subscriptions = <StreamSubscription<String>>[];
  Completer<void>? _ready;
  Completer<CommandResult>? _pending;
  Completer<void>? _lock;
  String _buffer = '';
  String? _password;
  String? _username;
  bool _passwordSent = false;
  bool _closed = false;
  int _sequence = 0;

  String get _readyPrefix => '\x01MP_${_nonce}_READY:';
  String get _startMarker => '\x01MP_${_nonce}_${_sequence}_START\x01';
  String get _endPrefix => '\x01MP_${_nonce}_${_sequence}_END:';

  Future<void> start({
    required String username,
    required String password,
    Duration timeout = const Duration(seconds: 15),
  }) async {
    if (_closed || _ready != null) {
      throw WorkUserAuthenticationError(
        'Working-user channel cannot be restarted',
      );
    }
    if (!RegExp(r'^[a-zA-Z_][a-zA-Z0-9_.-]*\$?$').hasMatch(username)) {
      throw WorkUserAuthenticationError('Invalid working username');
    }
    if (password.isEmpty || RegExp(r'[\x00-\x1f\x7f]').hasMatch(password)) {
      throw WorkUserAuthenticationError(
        'A working-user password without control characters is required',
      );
    }
    _username = username;
    _password = password;
    final ready = Completer<void>();
    ready.future.ignore();
    _ready = ready;
    final worker = r'while IFS= read -r line; do eval "$line"; done';
    final bootstrap =
        "stty raw -echo || exit; "
        "printf '\\001MP_${_nonce}_READY:%s\\001' \"\$(id -un)\"; "
        'exec /bin/sh -c ${_quote(worker)}';
    // Disable echo before su can ask for a password. Force only the prompt's
    // locale; su - supplies the working user's login environment afterwards.
    final command =
        'stty -echo || exit; LC_ALL=C su - ${_quote(username)} '
        '-c ${_quote(bootstrap)}';
    try {
      final opening = _transport.execute(command, pty: const SSHPtyConfig());
      unawaited(
        opening.then((session) {
          if (_closed) {
            session.close();
            return;
          }
          _session = session;
          for (final stream in [session.stdout, session.stderr]) {
            _subscriptions.add(
              stream
                  .cast<List<int>>()
                  .transform(const Utf8Decoder(allowMalformed: true))
                  .listen(
                    _onData,
                    onDone: () => _fail('Working-user command channel closed'),
                    onError: (Object _) =>
                        _fail('Working-user command channel failed'),
                  ),
            );
          }
        }, onError: (Object _) => _fail('Could not open working-user channel')),
      );
      await ready.future.timeout(timeout);
    } catch (_) {
      await dispose();
      throw WorkUserAuthenticationError(
        'Could not switch to $username. Check the su password and host policy.',
      );
    } finally {
      _password = null;
    }
  }

  void _onData(String data) {
    if (_closed) return;
    _buffer += data;
    final ready = _ready;
    if (ready != null && !ready.isCompleted) {
      final start = _buffer.indexOf(_readyPrefix);
      if (start >= 0) {
        final end = _buffer.indexOf('\x01', start + _readyPrefix.length);
        if (end < 0) return;
        final identity = _buffer.substring(start + _readyPrefix.length, end);
        if (identity != _username) {
          _fail('Working-user identity verification failed');
          return;
        }
        _buffer = '';
        _password = null;
        ready.complete();
        return;
      }
      final prompt = RegExp(r'password:\s*$', caseSensitive: false);
      if (prompt.hasMatch(_buffer)) {
        if (_passwordSent) {
          _fail('Working-user authentication rejected');
          return;
        }
        _passwordSent = true;
        _buffer = '';
        _session!.write(utf8.encode('$_password\n'));
      } else if (_buffer.length > 16384) {
        _fail('Unexpected working-user authentication response');
      }
      return;
    }
    final pending = _pending;
    if (pending == null || pending.isCompleted) {
      _buffer = '';
      return;
    }
    final start = _buffer.indexOf(_startMarker);
    final end = _buffer.indexOf(_endPrefix);
    if (start < 0 || end < start) return;
    final statusEnd = _buffer.indexOf('\x01', end + _endPrefix.length);
    if (statusEnd < 0) return;
    final status = int.tryParse(
      _buffer.substring(end + _endPrefix.length, statusEnd),
    );
    if (status == null) {
      _fail('Invalid working-user command response');
      return;
    }
    // A PTY merges stderr into stdout, as an ordinary terminal does.
    final output = _buffer.substring(start + _startMarker.length, end);
    _buffer = '';
    _pending = null;
    pending.complete((stdout: output, stderr: '', exitCode: status));
  }

  Future<CommandResult> run(
    String command, {
    Duration timeout = const Duration(seconds: 15),
  }) async {
    final clock = Stopwatch()..start();
    Duration remaining() {
      final left = timeout - clock.elapsed;
      if (left <= Duration.zero) {
        throw TimeoutException('Working-user command timed out');
      }
      return left;
    }

    while (_lock != null) {
      await _lock!.future.timeout(remaining());
    }
    remaining();
    if (_closed || _session == null || _ready?.isCompleted != true) {
      throw StateError('Working-user command channel is unavailable');
    }
    final lock = Completer<void>();
    _lock = lock;
    try {
      _sequence++;
      _buffer = '';
      final pending = Completer<CommandResult>();
      // Channel writes can fail synchronously before the reply is awaited.
      pending.future.ignore();
      _pending = pending;
      // Encode embedded newlines and shell metacharacters in one wire line.
      // Raw mode avoids the PTY's canonical input length limit for large pastes.
      final encoded = utf8
          .encode(command)
          .map((b) => '\\0${b.toRadixString(8).padLeft(3, '0')}')
          .join();
      _session!.write(
        utf8.encode(
          "printf '\\001MP_${_nonce}_${_sequence}_START\\001'; "
          "(eval \"\$(printf '%b' '$encoded')\"); "
          "printf '\\001MP_${_nonce}_${_sequence}_END:%s\\001' \"\$?\"\n",
        ),
      );
      return await pending.future.timeout(remaining());
    } on TimeoutException {
      // A timed-out mutation may have executed. Close rather than replay it.
      await dispose();
      rethrow;
    } catch (_) {
      await dispose();
      rethrow;
    } finally {
      _pending = null;
      _lock = null;
      lock.complete();
    }
  }

  void _fail(String message) {
    if (_closed) return;
    _closed = true;
    _password = null;
    if (_ready?.isCompleted == false) {
      _ready!.completeError(WorkUserAuthenticationError(message));
    }
    if (_pending?.isCompleted == false) {
      _pending!.completeError(StateError(message));
    }
    _session?.close();
  }

  Future<void> dispose() async {
    _fail('Working-user command channel closed');
    for (final subscription in _subscriptions) {
      unawaited(subscription.cancel());
    }
    _subscriptions.clear();
    _buffer = '';
  }

  static String _quote(String value) => "'${value.replaceAll("'", "'\\''")}'";
}

class WorkUserAuthenticationError implements Exception {
  WorkUserAuthenticationError(this.message);
  final String message;

  @override
  String toString() => message;
}
