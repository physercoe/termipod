import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:dartssh2/dartssh2.dart';

/// SOCKS5 proxy error
class Socks5Exception implements Exception {
  final String message;
  Socks5Exception(this.message);

  @override
  String toString() => 'Socks5Exception: $message';
}

/// SSHSocket implementation that tunnels through a SOCKS5 proxy.
///
/// Performs RFC 1928 handshake over a single socket subscription, preserving
/// any post-handshake bytes (such as a coalesced SSH server greeting) before
/// handing the stream to dartssh2.
class Socks5Socket implements SSHSocket {
  final Socket _socket;
  final StreamController<Uint8List> _streamController =
      StreamController<Uint8List>();
  StreamSubscription<Uint8List>? _subscription;

  Socks5Socket._(this._socket, _Socks5HandshakeStream handshakeStream) {
    _subscription = handshakeStream.transferTo(_streamController);
    _streamController.onPause = () => _subscription?.pause();
    _streamController.onResume = () => _subscription?.resume();
    _streamController.onCancel = () => _subscription?.cancel();
  }

  /// Connect to [targetHost]:[targetPort] through SOCKS5 proxy at
  /// [proxyHost]:[proxyPort].
  ///
  /// Optional [username]/[password] for SOCKS5 authentication (RFC 1929).
  static Future<Socks5Socket> connect({
    required String proxyHost,
    required int proxyPort,
    required String targetHost,
    required int targetPort,
    String? username,
    String? password,
    Duration timeout = const Duration(seconds: 30),
  }) async {
    final watch = Stopwatch()..start();
    Duration remaining() {
      final left = timeout - watch.elapsed;
      if (left <= Duration.zero) {
        throw TimeoutException('SOCKS5 connection timed out');
      }
      return left;
    }

    final socket = await Socket.connect(
      proxyHost,
      proxyPort,
      timeout: remaining(),
    );
    final handshakeStream = _Socks5HandshakeStream(socket);
    try {
      await _handshake(
        socket,
        handshakeStream,
        targetHost: targetHost,
        targetPort: targetPort,
        username: username,
        password: password,
        remaining: remaining,
      );
      return Socks5Socket._(socket, handshakeStream);
    } catch (e) {
      unawaited(handshakeStream.cancel());
      socket.destroy();
      rethrow;
    }
  }

  static Future<void> _handshake(
    Socket socket,
    _Socks5HandshakeStream reader, {
    required String targetHost,
    required int targetPort,
    required Duration Function() remaining,
    String? username,
    String? password,
  }) async {
    final hasAuth = username != null && password != null;

    // Client greeting: VER=0x05, NMETHODS, METHODS
    // 0x00 = no auth, 0x02 = username/password
    if (hasAuth) {
      socket.add([0x05, 0x02, 0x00, 0x02]);
    } else {
      socket.add([0x05, 0x01, 0x00]);
    }
    await socket.flush().timeout(remaining());

    // Server auth method selection
    final authReply = await reader.readExact(2, remaining());
    if (authReply[0] != 0x05) {
      throw Socks5Exception('Invalid SOCKS version: ${authReply[0]}');
    }

    final selectedMethod = authReply[1];
    if (selectedMethod == 0xFF) {
      throw Socks5Exception('No acceptable auth methods');
    }

    // Username/password sub-negotiation (RFC 1929)
    if (selectedMethod == 0x02) {
      if (!hasAuth) {
        throw Socks5Exception('Proxy requires authentication');
      }
      final userBytes = username.codeUnits;
      final passBytes = password.codeUnits;
      socket.add([
        0x01, // sub-negotiation version
        userBytes.length, ...userBytes,
        passBytes.length, ...passBytes,
      ]);
      await socket.flush().timeout(remaining());

      final authResult = await reader.readExact(2, remaining());
      if (authResult[1] != 0x00) {
        throw Socks5Exception('Proxy authentication failed');
      }
    } else if (selectedMethod != 0x00) {
      throw Socks5Exception('Unsupported auth method: $selectedMethod');
    }

    // CONNECT request
    // VER=0x05, CMD=0x01 (CONNECT), RSV=0x00, ATYP, DST.ADDR, DST.PORT
    final hostBytes = targetHost.codeUnits;
    final portHigh = (targetPort >> 8) & 0xFF;
    final portLow = targetPort & 0xFF;
    socket.add([
      0x05, 0x01, 0x00,
      0x03, // ATYP = domain name
      hostBytes.length, ...hostBytes,
      portHigh, portLow,
    ]);
    await socket.flush().timeout(remaining());

    // Server reply: VER, REP, RSV, ATYP, BND.ADDR, BND.PORT
    final reply = await reader.readExact(4, remaining());
    if (reply[0] != 0x05) {
      throw Socks5Exception('Invalid SOCKS version in reply: ${reply[0]}');
    }
    if (reply[1] != 0x00) {
      throw Socks5Exception('SOCKS connect failed: ${_replyMessage(reply[1])}');
    }

    // Skip bound address based on ATYP
    final atyp = reply[3];
    switch (atyp) {
      case 0x01: // IPv4
        await reader.readExact(4 + 2, remaining());
      case 0x03: // Domain
        final lenBuf = await reader.readExact(1, remaining());
        await reader.readExact(lenBuf[0] + 2, remaining());
      case 0x04: // IPv6
        await reader.readExact(16 + 2, remaining());
      default:
        throw Socks5Exception('Unknown address type: $atyp');
    }
    // Handshake complete — socket now tunnels to target
  }

  static String _replyMessage(int code) {
    switch (code) {
      case 0x01: return 'General failure';
      case 0x02: return 'Connection not allowed by ruleset';
      case 0x03: return 'Network unreachable';
      case 0x04: return 'Host unreachable';
      case 0x05: return 'Connection refused';
      case 0x06: return 'TTL expired';
      case 0x07: return 'Command not supported';
      case 0x08: return 'Address type not supported';
      default:   return 'Unknown error ($code)';
    }
  }

  // --- SSHSocket interface ---

  @override
  Stream<Uint8List> get stream => _streamController.stream;

  @override
  StreamSink<List<int>> get sink => _socket;

  @override
  Future<void> get done => _socket.done;

  @override
  Future<void> close() async {
    await _subscription?.cancel();
    await _socket.close();
  }

  @override
  void destroy() {
    unawaited(_subscription?.cancel());
    _socket.destroy();
  }
}

/// Buffers a single-subscription [Socket] during the SOCKS5 handshake, then
/// transfers any remaining coalesced bytes and future events to the tunnel.
class _Socks5HandshakeStream {
  _Socks5HandshakeStream(Socket socket) {
    _subscription = socket.listen(
      _onData,
      onError: _onError,
      onDone: _onDone,
    );
  }

  late final StreamSubscription<Uint8List> _subscription;
  final List<int> _buffer = [];
  StreamController<Uint8List>? _tunnel;
  Completer<Uint8List>? _pending;
  int _needed = 0;
  Object? _error;
  StackTrace? _stackTrace;
  bool _done = false;

  Future<Uint8List> readExact(int count, Duration timeout) {
    if (_buffer.length >= count) {
      final result = Uint8List.fromList(_buffer.sublist(0, count));
      _buffer.removeRange(0, count);
      return Future.value(result);
    }
    if (_error != null) {
      return Future.error(_error!, _stackTrace);
    }
    if (_done) {
      return Future.error(
        Socks5Exception('Connection closed during SOCKS5 handshake'),
      );
    }
    final completer = Completer<Uint8List>();
    _pending = completer;
    _needed = count;
    return completer.future.timeout(
      timeout,
      onTimeout: () {
        if (identical(_pending, completer)) {
          _pending = null;
          _needed = 0;
        }
        throw TimeoutException('SOCKS5 handshake timed out');
      },
    );
  }

  StreamSubscription<Uint8List> transferTo(StreamController<Uint8List> target) {
    _tunnel = target;
    if (_buffer.isNotEmpty) {
      target.add(Uint8List.fromList(_buffer));
      _buffer.clear();
    }
    if (_error != null) {
      target.addError(_error!, _stackTrace);
      _error = null;
      _stackTrace = null;
    }
    if (_done && !target.isClosed) {
      unawaited(target.close());
    }
    return _subscription;
  }

  Future<void> cancel() => _subscription.cancel();

  void _onData(Uint8List data) {
    final tunnel = _tunnel;
    if (tunnel != null) {
      if (!tunnel.isClosed) tunnel.add(Uint8List.fromList(data));
      return;
    }
    _buffer.addAll(data);
    final pending = _pending;
    if (pending != null && _buffer.length >= _needed) {
      final result = Uint8List.fromList(_buffer.sublist(0, _needed));
      _buffer.removeRange(0, _needed);
      _pending = null;
      _needed = 0;
      if (!pending.isCompleted) pending.complete(result);
    }
  }

  void _onError(Object error, StackTrace stackTrace) {
    final tunnel = _tunnel;
    if (tunnel != null) {
      if (!tunnel.isClosed) tunnel.addError(error, stackTrace);
      return;
    }
    _error = error;
    _stackTrace = stackTrace;
    final pending = _pending;
    _pending = null;
    _needed = 0;
    if (pending != null && !pending.isCompleted) {
      pending.completeError(error, stackTrace);
    }
  }

  void _onDone() {
    _done = true;
    final tunnel = _tunnel;
    if (tunnel != null) {
      if (!tunnel.isClosed) unawaited(tunnel.close());
      return;
    }
    final pending = _pending;
    _pending = null;
    _needed = 0;
    if (pending != null && !pending.isCompleted) {
      pending.completeError(
        Socks5Exception('Connection closed during SOCKS5 handshake'),
      );
    }
  }
}
