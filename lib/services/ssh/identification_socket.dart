import 'dart:async';
import 'dart:typed_data';

import 'package:dartssh2/dartssh2.dart';

/// Adapts the identification exchange for dartssh2 2.13's line parser, which
/// rejects a partial greeting instead of waiting for the next stream chunk.
/// TCP sockets and direct-tcpip channels may split the greeting at any byte.
/// Only identification is buffered; all subsequent SSH packets pass unchanged.
class SshIdentificationSocket implements SSHSocket {
  SshIdentificationSocket(this._socket);

  final SSHSocket _socket;

  // Match the dependency's bounded identification buffer, including any
  // pre-identification notice lines allowed by RFC 4253 section 4.2.
  static const maxIdentificationBytes = 10240;

  late final Stream<Uint8List> _stream = _identify(_socket.stream);

  @override
  Stream<Uint8List> get stream => _stream;

  Stream<Uint8List> _identify(Stream<Uint8List> source) {
    final line = <int>[];
    var received = 0;
    var identified = false;
    var failed = false;
    // A synchronous transformer propagates pause/cancel immediately, even
    // while waiting for the rest of a greeting from a silent peer.
    return source.transform(
      StreamTransformer<Uint8List, Uint8List>.fromHandlers(
        handleData: (chunk, sink) {
          if (failed) return;
          if (identified) {
            sink.add(chunk);
            return;
          }
          for (var i = 0; i < chunk.length; i++) {
            if (++received > maxIdentificationBytes) {
              failed = true;
              sink.addError(
                SSHHandshakeError(
                  'SSH identification exceeds $maxIdentificationBytes bytes',
                ),
              );
              sink.close();
              return;
            }
            line.add(chunk[i]);
            if (chunk[i] != 10) continue;
            // Keep the original identification bytes, including CRLF or LF.
            // Do not rewrite the version: it participates in the key exchange.
            if (line.length >= 4 &&
                line[0] == 83 &&
                line[1] == 83 &&
                line[2] == 72 &&
                line[3] == 45) {
              identified = true;
              sink.add(Uint8List.fromList(line));
              line.clear();
              // Greeting and binary packets may share one source chunk. Emit
              // the remainder separately so the parser's greeting limit does
              // not accidentally count binary key-exchange data as banner text.
              if (i + 1 < chunk.length) {
                sink.add(Uint8List.sublistView(chunk, i + 1));
              }
              break;
            }
            line.clear();
          }
        },
        handleError: (error, stack, sink) {
          failed = true;
          sink.addError(error, stack);
          sink.close();
        },
        handleDone: (sink) {
          if (!identified && !failed) {
            sink.addError(
              SSHHandshakeError(
                'SSH connection closed before a complete server identification',
              ),
            );
          }
          sink.close();
        },
      ),
    );
  }

  @override
  StreamSink<List<int>> get sink => _socket.sink;

  @override
  Future<void> get done => _socket.done;

  @override
  Future<void> close() => _socket.close();

  @override
  void destroy() => _socket.destroy();
}
