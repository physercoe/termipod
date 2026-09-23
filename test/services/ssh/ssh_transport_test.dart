import 'dart:async';

import 'package:dartssh2/dartssh2.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/ssh/ssh_client.dart';

class _Socket implements SSHSocket {
  int destroys = 0;
  @override
  void destroy() {
    destroys++;
  }

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
  int closes = 0;
  Completer<void>? auth;
  Completer<SSHForwardChannel>? forwarding;
  final forwarded = _ForwardSocket();
  int forwardCalls = 0;
  @override
  bool isClosed = false;
  @override
  Future<void> get authenticated async {
    await auth?.future;
  }

  @override
  Future<SSHForwardChannel> forwardLocal(
    String host,
    int port, {
    String localHost = 'localhost',
    int localPort = 0,
  }) async {
    forwardCalls++;
    return await (forwarding?.future ?? Future.value(forwarded));
  }

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
    closes++;
    isClosed = true;
    if (!ended.isCompleted) ended.complete();
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class _ForwardSocket extends _Socket implements SSHForwardChannel {}

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
  test('jump-host authentication rejection remains non-retryable', () async {
    final jump = _Transport()..auth = Completer<void>();
    final client = SshClient(
      socketConnector: (_, _, _) async => _Socket(),
      jumpTransportFactory: (_, _, _) => jump,
      transportFactory: (_, _, _) =>
          throw StateError('target must not be opened'),
    );
    final opening = client.connect(
      host: 'target',
      port: 22,
      username: 'user',
      options: const SshConnectOptions(
        password: 'secret',
        jumpHost: 'jump',
        jumpPassword: 'secret',
      ),
    );
    final failure = expectLater(
      opening,
      throwsA(
        isA<SshAuthenticationError>().having(
          (e) => e.message,
          'phase',
          contains('jump-host authentication'),
        ),
      ),
    );
    await Future<void>.delayed(Duration.zero);
    jump.auth!.completeError(SSHAuthFailError('Permission denied'));
    await failure;
    expect(jump.closes, 1);
    await client.dispose();
  });

  for (final phase in [
    'socket',
    'jump auth',
    'forwarding',
    'target auth',
    'connected',
  ]) {
    test(
      'disconnect cancels jump-host $phase and releases both hops',
      () async {
        final socket = _Socket();
        final socketGate = Completer<SSHSocket>();
        final jump = _Transport();
        final target = _Transport();
        if (phase == 'jump auth') jump.auth = Completer<void>();
        if (phase == 'forwarding')
          jump.forwarding = Completer<SSHForwardChannel>();
        if (phase == 'target auth') target.auth = Completer<void>();
        var targetCreations = 0;
        final client = SshClient(
          socketConnector: (_, _, _) =>
              phase == 'socket' ? socketGate.future : Future.value(socket),
          jumpTransportFactory: (_, username, options) {
            expect(username, 'jumper');
            expect(options.password, 'jump secret');
            return jump;
          },
          transportFactory: (_, _, _) {
            targetCreations++;
            return target;
          },
        );
        final opening = client.connect(
          host: 'target',
          port: 22,
          username: 'user',
          options: const SshConnectOptions(
            password: 'target secret',
            jumpHost: 'jump',
            jumpUsername: 'jumper',
            jumpPassword: 'jump secret',
          ),
        );
        final completion = phase == 'connected'
            ? opening
            : expectLater(
                opening,
                throwsA(
                  isA<SshConnectionError>().having(
                    (e) => e.message,
                    'reason',
                    contains('cancelled'),
                  ),
                ),
              );
        await Future<void>.delayed(Duration.zero);
        if (phase == 'connected') await opening;
        await client.dispose();
        await completion;
        expect(client.state, SshConnectionState.disconnected);
        expect(target.closes, targetCreations);
        expect(jump.closes, phase == 'socket' ? 0 : 1);
        final lateForward = _ForwardSocket();
        if (phase == 'socket') socketGate.complete(socket);
        jump.auth?.complete();
        jump.forwarding?.complete(lateForward);
        target.auth?.complete();
        await Future<void>.delayed(Duration.zero);
        expect(
          targetCreations,
          ['target auth', 'connected'].contains(phase) ? 1 : 0,
        );
        if (phase == 'socket' ||
            phase == 'jump auth' ||
            phase == 'forwarding') {
          expect(socket.destroys, 1);
        }
        if (phase == 'forwarding') expect(lateForward.destroys, 1);
        if (targetCreations == 1) expect(jump.forwarded.destroys, 1);
        await client.dispose();
        expect(
          jump.closes,
          phase == 'socket' ? 0 : 1,
          reason: 'dispose is idempotent',
        );
      },
    );
  }

  for (final phase in [
    'jump-host authentication',
    'jump-host forwarding',
    'target SSH authentication',
  ]) {
    test(
      'quick $phase failure keeps its stage and closes the jump host',
      () async {
        final jump = _Transport();
        final target = _Transport();
        if (phase == 'jump-host authentication') jump.auth = Completer<void>();
        if (phase == 'jump-host forwarding')
          jump.forwarding = Completer<SSHForwardChannel>();
        if (phase == 'target SSH authentication')
          target.auth = Completer<void>();
        final client = SshClient(
          socketConnector: (_, _, _) async => _Socket(),
          jumpTransportFactory: (_, _, _) => jump,
          transportFactory: (_, _, _) => target,
        );
        final opening = client.connect(
          host: 'target',
          port: 22,
          username: 'user',
          options: const SshConnectOptions(
            password: 'secret',
            jumpHost: 'jump',
            jumpPassword: 'secret',
          ),
        );
        final failure = expectLater(
          opening,
          throwsA(
            isA<SshConnectionError>().having(
              (e) => e.message,
              'phase',
              contains(phase),
            ),
          ),
        );
        await Future<void>.delayed(Duration.zero);
        if (jump.auth != null)
          jump.auth!.completeError(StateError('peer closed'));
        if (jump.forwarding != null)
          jump.forwarding!.completeError(StateError('peer closed'));
        if (target.auth != null)
          target.auth!.completeError(StateError('peer closed'));
        await failure;
        expect(jump.closes, 1);
        expect(client.lastError, contains(phase));
        await client.dispose();
      },
    );
  }

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
