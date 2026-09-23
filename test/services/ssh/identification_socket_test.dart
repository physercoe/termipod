import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:dartssh2/dartssh2.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/ssh/identification_socket.dart';
import 'package:termipod/services/ssh/ssh_client.dart';

const greeting = 'SSH-2.0-OpenSSH_9.6\r\n';
Uint8List bytes(String text) => Uint8List.fromList(ascii.encode(text));

class _Socket implements SSHForwardChannel {
  final incoming = StreamController<Uint8List>();
  final outgoing = StreamController<List<int>>();
  late final _sink = outgoing.sink;
  final ended = Completer<void>();
  bool destroyed = false;
  bool cancelled = false;
  _Socket() {
    outgoing.stream.listen((_) {});
    incoming.onCancel = () {
      cancelled = true;
    };
  }
  @override
  Stream<Uint8List> get stream => incoming.stream;
  @override
  StreamSink<List<int>> get sink => _sink;
  @override
  Future<void> get done => ended.future;
  @override
  void destroy() {
    if (destroyed) return;
    destroyed = true;
    ended.complete();
    unawaited(incoming.close());
    unawaited(outgoing.close());
  }

  @override
  Future<void> close() async {
    destroy();
  }
}

class _Jump extends Fake implements SSHClient {
  final _Socket initial;
  final _Socket target;
  _Jump(this.initial, this.target);
  @override
  Future<void> get authenticated async {}
  @override
  Future<SSHForwardChannel> forwardLocal(
    String host,
    int port, {
    String localHost = 'localhost',
    int localPort = 0,
  }) async => target;
  @override
  void close() {
    initial.destroy();
    target.destroy();
  }
}

class _ObfuscatedHandshakeError extends SSHHandshakeError {
  _ObfuscatedHandshakeError(super.message);
  @override
  String toString() => 'gO($message)';
}

void main() {
  test('release diagnostics use the message, not an obfuscated class name', () {
    final cause = _ObfuscatedHandshakeError('unsupported key exchange');
    expect(
      SshConnectionError('Target: unsupported key exchange', cause).toString(),
      'SshConnectionError: Target: unsupported key exchange',
    );
    expect(
      SshConnectionError('Target failed', cause).toString(),
      'SshConnectionError: Target failed (unsupported key exchange)',
    );
  });

  for (var split = 1; split < greeting.length; split++) {
    test('real SSH parser accepts greeting split at byte $split', () async {
      final source = _Socket();
      final client = SSHClient(
        SshIdentificationSocket(source),
        username: 'test',
        keepAliveInterval: null,
      );
      final auth = expectLater(
        client.authenticated,
        throwsA(isA<SSHAuthAbortError>()),
      );
      source.incoming.add(bytes(greeting.substring(0, split)));
      await Future<void>.delayed(Duration.zero);
      expect(
        client.isClosed,
        isFalse,
        reason: 'must wait for the remaining greeting',
      );
      source.incoming.add(bytes(greeting.substring(split)));
      await Future<void>.delayed(Duration.zero);
      expect(
        client.isClosed,
        isFalse,
        reason: 'complete greeting enters key exchange',
      );
      client.close();
      await auth;
    });
  }

  test(
    'one-byte chunks, notice lines and LF-only greetings are supported',
    () async {
      final source = _Socket();
      final received = SshIdentificationSocket(source).stream.toList();
      for (final byte in bytes('Notice\r\nAnother notice\nSSH-2.0-server\n')) {
        source.incoming.add(Uint8List.fromList([byte]));
      }
      await source.incoming.close();
      expect(await received, [bytes('SSH-2.0-server\n')]);
      source.destroy();
    },
  );

  test(
    'coalesced key-exchange data and later binary chunks are unchanged',
    () async {
      final source = _Socket();
      final socket = SshIdentificationSocket(source);
      final received = socket.stream.toList();
      final binary = Uint8List.fromList(List.generate(20000, (i) => i % 256));
      final later = Uint8List.fromList([0, 255, 10, 13]);
      source.incoming.add(Uint8List.fromList([...bytes(greeting), ...binary]));
      source.incoming.add(later);
      await source.incoming.close();
      final chunks = await received;
      expect(chunks, [bytes(greeting), binary, later]);
      expect(
        chunks.last,
        same(later),
        reason: 'no buffering after identification',
      );
      expect(socket.stream, same(socket.stream));
      expect(socket.sink, same(source.sink));
      expect(socket.done, same(source.done));
      await socket.close();
      expect(source.destroyed, isTrue);
    },
  );

  for (final notices in [false, true]) {
    test('identification size is bounded, with notices=$notices', () async {
      final source = _Socket();
      final result = SshIdentificationSocket(source).stream.drain<void>();
      final failed = expectLater(result, throwsA(isA<SSHHandshakeError>()));
      source.incoming.add(
        Uint8List.fromList(
          List.filled(
            SshIdentificationSocket.maxIdentificationBytes + 1,
            notices ? 10 : 65,
          ),
        ),
      );
      await failed;
      expect(source.cancelled, isTrue);
      source.destroy();
    });
  }

  test(
    'EOF before a complete identification reports a bounded failure',
    () async {
      final source = _Socket();
      final failed = expectLater(
        SshIdentificationSocket(source).stream.drain<void>(),
        throwsA(
          isA<SSHHandshakeError>().having(
            (e) => e.message,
            'reason',
            contains('complete server identification'),
          ),
        ),
      );
      source.incoming.add(bytes('SSH-2.0-partial'));
      await source.incoming.close();
      await failed;
      source.destroy();
    },
  );

  test(
    'cancelling during a partial greeting cancels the source subscription',
    () async {
      final source = _Socket();
      final socket = SshIdentificationSocket(source);
      final sub = socket.stream.listen((_) => fail('no complete greeting'));
      source.incoming.add(bytes('SSH-2.0-'));
      await Future<void>.delayed(Duration.zero);
      await sub.cancel();
      expect(source.cancelled, isTrue);
      socket.destroy();
      expect(source.destroyed, isTrue);
    },
  );

  test(
    'pausing the adapted stream retains queued bytes until resume',
    () async {
      final source = _Socket();
      final received = <Uint8List>[];
      final socket = SshIdentificationSocket(source);
      final sub = socket.stream.listen(received.add);
      sub.pause();
      source.incoming.add(bytes('SSH-2.0-'));
      source.incoming.add(bytes('server\r\n'));
      await Future<void>.delayed(Duration.zero);
      expect(received, isEmpty);
      sub.resume();
      await Future<void>.delayed(Duration.zero);
      expect(received, [bytes('SSH-2.0-server\r\n')]);
      await sub.cancel();
      socket.destroy();
    },
  );

  for (final phase in ['direct', 'jump-host', 'target']) {
    test(
      'production $phase transport buffers fragments and preserves socket errors',
      () async {
        final initial = _Socket();
        final target = _Socket();
        final source = phase == 'target' ? target : initial;
        final client = SshClient(
          socketConnector: (_, _, _) async => initial,
          jumpTransportFactory: phase == 'target'
              ? (_, _, _) => _Jump(initial, target)
              : null,
        );
        final opening = client.connect(
          host: 'target.test',
          port: 22,
          username: 'test',
          options: SshConnectOptions(
            password: 'secret',
            jumpHost: phase == 'direct' ? null : 'jump.test',
            jumpPassword: 'secret',
          ),
        );
        final failure = expectLater(
          opening,
          throwsA(
            isA<SshConnectionError>()
                .having(
                  (e) => e.message,
                  'cause',
                  contains('fixture connection reset'),
                )
                .having(
                  (e) => e.message,
                  'phase',
                  contains(
                    phase == 'direct'
                        ? 'SSH authentication'
                        : '$phase${phase == 'target' ? ' SSH' : ''} authentication',
                  ),
                ),
          ),
        );
        await Future<void>.delayed(Duration.zero);
        source.incoming.add(bytes('SSH-2.0-Open'));
        await Future<void>.delayed(Duration.zero);
        expect(source.destroyed, isFalse);
        source.incoming.add(bytes('SSH_9.6\r\n'));
        await Future<void>.delayed(Duration.zero);
        expect(source.destroyed, isFalse);
        source.incoming.addError(
          const SocketException('fixture connection reset'),
        );
        await failure;
        expect(
          client.lastError,
          isNot(contains('Connection closed before authentication')),
        );
        await client.dispose();
        initial.destroy();
        target.destroy();
      },
    );
  }

  test(
    'an actual parser rejection retains its original reason, without duplication',
    () async {
      final socket = _Socket();
      final client = SshClient(socketConnector: (_, _, _) async => socket);
      final opening = client.connect(
        host: 'test',
        port: 22,
        username: 'test',
        options: const SshConnectOptions(password: 'secret'),
      );
      final failed = expectLater(
        opening,
        throwsA(
          isA<SshConnectionError>().having(
            (e) => e.toString(),
            'display',
            'SshConnectionError: Connection failed during SSH authentication: Invalid version: SSH-1.5-old',
          ),
        ),
      );
      await Future<void>.delayed(Duration.zero);
      socket.incoming.add(bytes('SSH-1.5-old\r\ntrailing data'));
      await failed;
      await client.dispose();
    },
  );

  testWidgets('a server that never finishes its greeting still times out', (
    tester,
  ) async {
    final socket = _Socket();
    final client = SshClient(socketConnector: (_, _, _) async => socket);
    final opening = client.connect(
      host: 'test',
      port: 22,
      username: 'test',
      options: const SshConnectOptions(password: 'secret', timeout: 1),
    );
    final failed = expectLater(
      opening,
      throwsA(
        isA<SshConnectionError>().having(
          (e) => e.cause,
          'cause',
          isA<TimeoutException>(),
        ),
      ),
    );
    await tester.pump();
    socket.incoming.add(bytes('SSH-2.0-'));
    await tester.pump(const Duration(seconds: 1));
    await failed;
    expect(socket.destroyed, isTrue);
    await client.dispose();
  });
}
