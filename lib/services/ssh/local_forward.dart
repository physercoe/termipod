import 'dart:async';
import 'dart:io';

import 'package:dartssh2/dartssh2.dart';

/// One loopback-only TCP listener. Each accepted socket owns one SSH channel.
/// The listener survives transport recovery; old streams never survive it.
class LocalForward {
  LocalForward._(this._server, this._openChannel);

  final ServerSocket _server;
  final Future<SSHSocket> Function() _openChannel;
  final Set<_ForwardPair> _pairs = {};
  bool _closed = false;
  bool _available = true;
  int _generation = 0;

  int get port => _server.port;
  InternetAddress get address => _server.address;

  static Future<LocalForward> start(
    Future<SSHSocket> Function() openChannel,
  ) async {
    final server = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
    final forward = LocalForward._(server, openChannel);
    server.listen(
      forward._accept,
      onError: (Object _) {
        forward.close();
      },
    );
    return forward;
  }

  void setAvailable(bool available) {
    _available = available;
    if (!available) {
      ++_generation;
      for (final pair in _pairs.toList()) {
        _drop(pair);
      }
    }
  }

  Future<void> _accept(Socket socket) async {
    if (_closed || !_available) {
      socket.destroy();
      return;
    }
    final generation = _generation;
    final pair = _ForwardPair(socket);
    _pairs.add(pair);
    // Observe errors even while SSH channel-open is pending. Stream.pipe below
    // supplies backpressure in both directions once the channel is ready.
    unawaited(
      socket.done.then<void>(
        (_) => _drop(pair),
        onError: (Object _) => _drop(pair),
      ),
    );
    try {
      final pending = _openChannel();
      var expired = false;
      unawaited(
        pending.then<void>((channel) {
          if (expired ||
              _closed ||
              generation != _generation ||
              !_pairs.contains(pair))
            channel.destroy();
        }, onError: (Object _) {}),
      );
      final channel = await pending.timeout(
        const Duration(seconds: 10),
        onTimeout: () {
          expired = true;
          throw TimeoutException('SSH forwarding channel timed out');
        },
      );
      if (_closed || generation != _generation || !_pairs.contains(pair)) {
        channel.destroy();
        return;
      }
      pair.channel = channel;
      unawaited(
        channel.done.then<void>(
          (_) => _drop(pair),
          onError: (Object _) => _drop(pair),
        ),
      );
      await Future.wait([
        socket.cast<List<int>>().pipe(channel.sink),
        channel.stream.cast<List<int>>().pipe(socket),
      ]);
    } catch (_) {
      // Refusal affects this request, not other streams or the SSH transport.
    } finally {
      _drop(pair);
    }
  }

  void _drop(_ForwardPair pair) {
    _pairs.remove(pair);
    pair.socket.destroy();
    pair.channel?.destroy();
  }

  Future<void> close() async {
    if (_closed) return;
    _closed = true;
    setAvailable(false);
    await _server.close();
  }
}

class _ForwardPair {
  _ForwardPair(this.socket);
  final Socket socket;
  SSHSocket? channel;
}
