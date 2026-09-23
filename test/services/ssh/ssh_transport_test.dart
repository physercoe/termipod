import 'dart:async';

import 'package:dartssh2/dartssh2.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/ssh/ssh_client.dart';

class _Socket implements SSHSocket {
  @override
  Future<void> close() async {}
  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class _Transport implements SSHClient {
  final ended = Completer<void>();
  Completer<void>? pong;
  int pings = 0;
  int commands = 0;
  @override
  bool isClosed = false;
  @override
  Future<void> get authenticated async {}
  @override
  Future<void> get done => ended.future;
  @override
  Future<void> ping() async {
    pings++;
    await pong?.future;
  }

  @override
  Future<SSHSession> execute(
    String command, {
    SSHPtyConfig? pty,
    Map<String, String>? environment,
  }) async {
    commands++;
    throw StateError('Shell startup unavailable');
  }

  @override
  void close() {
    isClosed = true;
    if (!ended.isCompleted) ended.complete();
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

Future<SshClient> connect(_Transport transport) async {
  final client = SshClient(
    socketConnector: (_, _, _) async => _Socket(),
    transportFactory: (_, _, _) => transport,
  );
  await client.connect(
    host: 'host',
    port: 22,
    username: 'user',
    options: const SshConnectOptions(password: 'test'),
  );
  return client;
}

void main() {
  test(
    'authentication does not execute terminal commands or start shells',
    () async {
      final transport = _Transport();
      final client = await connect(transport);
      expect(client.isConnected, isTrue);
      expect(transport.commands, 0);
      await client.prepareTerminal();
      expect(transport.commands, greaterThan(0));
      expect(
        client.isConnected,
        isTrue,
        reason: 'optional shell setup must not discard authenticated SSH',
      );
      await client.dispose();
    },
  );

  testWidgets(
    'resume tolerates a slow pong and concurrent probes share one request',
    (tester) async {
      final transport = _Transport()..pong = Completer<void>();
      final client = await connect(transport);
      final first = client.probeTransport();
      final second = client.probeTransport();
      expect(identical(first, second), isTrue);
      await tester.pump(const Duration(seconds: 6));
      transport.pong!.complete();
      await tester.pump();
      await Future.wait([first, second]);
      expect(transport.pings, 1);
      expect(transport.commands, 0);
      expect(client.isConnected, isTrue);
      await client.dispose();
    },
  );

  testWidgets('a dead peer has a bounded protocol probe', (tester) async {
    final transport = _Transport()..pong = Completer<void>();
    final client = await connect(transport);
    final failure = expectLater(
      client.probeTransport(),
      throwsA(isA<TimeoutException>()),
    );
    await tester.pump(const Duration(seconds: 5));
    await tester.pump(const Duration(seconds: 5));
    await failure;
    await client.dispose();
  });

  test(
    'transport EOF notifies observers without waiting for a shell check',
    () async {
      final transport = _Transport();
      final client = await connect(transport);
      final lost = client.connectionStateStream.firstWhere(
        (s) => s == SshConnectionState.error,
      );
      transport.close();
      await lost;
      expect(client.isConnected, isFalse);
      await client.dispose();
    },
  );
}
