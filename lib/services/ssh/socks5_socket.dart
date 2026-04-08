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
/// Performs RFC 1928 handshake, then proxies stream/sink to dartssh2.
class Socks5Socket implements SSHSocket {
  final Socket _socket;
  final StreamController<Uint8List> _streamController =
      StreamController<Uint8List>.broadcast();
  StreamSubscription<Uint8List>? _subscription;

  Socks5Socket._(this._socket) {
    _subscription = _socket.listen(
      (data) => _streamController.add(Uint8List.fromList(data)),
      onError: (e) => _streamController.addError(e),
      onDone: () => _streamController.close(),
    );
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
    final socket = await Socket.connect(proxyHost, proxyPort, timeout: timeout);
    try {
      await _handshake(
        socket,
        targetHost: targetHost,
        targetPort: targetPort,
        username: username,
        password: password,
      );
      return Socks5Socket._(socket);
    } catch (e) {
      socket.destroy();
      rethrow;
    }
  }

  static Future<void> _handshake(
    Socket socket, {
    required String targetHost,
    required int targetPort,
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
    await socket.flush();

    // Server auth method selection
    final authReply = await _readExact(socket, 2);
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
      await socket.flush();

      final authResult = await _readExact(socket, 2);
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
    await socket.flush();

    // Server reply: VER, REP, RSV, ATYP, BND.ADDR, BND.PORT
    final reply = await _readExact(socket, 4);
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
        await _readExact(socket, 4 + 2);
      case 0x03: // Domain
        final lenBuf = await _readExact(socket, 1);
        await _readExact(socket, lenBuf[0] + 2);
      case 0x04: // IPv6
        await _readExact(socket, 16 + 2);
      default:
        throw Socks5Exception('Unknown address type: $atyp');
    }
    // Handshake complete — socket now tunnels to target
  }

  /// Read exactly [count] bytes from [socket].
  static Future<Uint8List> _readExact(Socket socket, int count) async {
    final buffer = BytesBuilder(copy: false);
    final completer = Completer<Uint8List>();
    late StreamSubscription<List<int>> sub;
    sub = socket.listen(
      (data) {
        buffer.add(data);
        if (buffer.length >= count) {
          sub.cancel();
          completer.complete(Uint8List.fromList(buffer.takeBytes().sublist(0, count)));
        }
      },
      onError: (e) {
        sub.cancel();
        if (!completer.isCompleted) completer.completeError(e);
      },
      onDone: () {
        sub.cancel();
        if (!completer.isCompleted) {
          completer.completeError(
            Socks5Exception('Connection closed during SOCKS5 handshake'),
          );
        }
      },
    );
    return completer.future;
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
    _subscription?.cancel();
    await _socket.close();
  }

  @override
  void destroy() {
    _subscription?.cancel();
    _socket.destroy();
  }
}
