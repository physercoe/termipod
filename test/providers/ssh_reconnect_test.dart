import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:termipod/providers/connection_provider.dart';
import 'package:termipod/providers/ssh_provider.dart';
import 'package:termipod/providers/web_tunnel_provider.dart';
import 'package:termipod/services/network/network_monitor.dart';
import 'package:termipod/services/ssh/ssh_client.dart';

class _OnlineNetworkMonitor extends NetworkMonitor {
  @override
  NetworkStatus get currentStatus => NetworkStatus.online;

  @override
  bool get isOnline => true;

  @override
  Stream<NetworkStatus> get statusStream => const Stream.empty();
}

class _ControlledSshClient extends SshClient {
  _ControlledSshClient({Future<void>? connectGate, this.connectError})
    : _connectGate = connectGate ?? Future<void>.value();

  final Future<void> _connectGate;
  final Object? connectError;
  bool _connected = false;
  int connectCalls = 0;
  int disposeCalls = 0;
  Object? probeError;
  final _states = StreamController<SshConnectionState>.broadcast();

  @override
  Stream<SshConnectionState> get connectionStateStream => _states.stream;

  void loseTransport() {
    _connected = false;
    _states.add(SshConnectionState.disconnected);
  }

  @override
  Future<String> exec(String command, {Duration? timeout}) async {
    if (probeError != null) throw probeError!;
    return 'p';
  }

  @override
  bool get isConnected => _connected;

  @override
  Future<void> connect({
    required String host,
    required int port,
    required String username,
    required SshConnectOptions options,
  }) async {
    connectCalls++;
    await _connectGate;
    if (connectError != null) throw connectError!;
    _connected = true;
  }

  @override
  Future<void> dispose() async {
    disposeCalls++;
    _connected = false;
    if (!_states.isClosed) await _states.close();
  }
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  final connection = Connection(
    id: 'race',
    name: 'test',
    host: 'example.test',
    username: 'user',
    createdAt: DateTime(2026),
  );
  const options = SshConnectOptions(password: 'secret');

  ProviderContainer fixture(List<_ControlledSshClient> clients) {
    SharedPreferences.setMockInitialValues({});
    var index = 0;
    final container = ProviderContainer(
      overrides: [
        sshClientFactoryProvider.overrideWithValue(() => clients[index++]),
        networkMonitorProvider.overrideWithValue(_OnlineNetworkMonitor()),
      ],
    );
    final subscription = container.listen(sshProvider('race'), (_, _) {});
    addTearDown(() {
      subscription.close();
      container.dispose();
    });
    return container;
  }

  test(
    'a stale transport replaced by another screen notifies the terminal',
    () async {
      final initial = _ControlledSshClient();
      final next = _ControlledSshClient();
      final container = fixture([initial, next]);
      final notifier = container.read(sshProvider('race').notifier);
      var callbacks = 0;
      notifier.onReconnectSuccess = () async {
        callbacks++;
      };
      await notifier.connectWithoutShell(connection, options);
      expect(callbacks, 0);
      initial.probeError = SshConnectionError('stale');
      await notifier.ensureConnected(connection, () async => options);
      expect(callbacks, 1);
      expect(notifier.client, same(next));
    },
  );

  test(
    'manual retry skips backoff instead of ignoring the user action',
    () async {
      final next = _ControlledSshClient();
      final container = fixture([_ControlledSshClient(), next]);
      final notifier = container.read(sshProvider('race').notifier);
      await notifier.connectWithoutShell(connection, options);
      final scheduled = notifier.reconnect();
      expect(container.read(sshProvider('race')).nextRetryAt, isNotNull);
      expect(await notifier.reconnectNow(), isTrue);
      expect(await scheduled, isFalse);
      expect(notifier.client, same(next));
    },
  );

  test('transient transport EOF does not stop web tunnels', () async {
    final initial = _ControlledSshClient();
    final container = fixture([initial]);
    final notifier = container.read(sshProvider('race').notifier);
    await notifier.connectWithoutShell(connection, options);
    final tunnel = await container
        .read(webTunnelProvider('race').notifier)
        .start(remoteHost: '127.0.0.1', remotePort: 8080);
    initial.loseTransport();
    await Future<void>.delayed(Duration.zero);
    expect(container.read(sshProvider('race')).isReconnecting, isTrue);
    expect(container.read(webTunnelProvider('race')), [same(tunnel)]);
    await notifier.disconnect();
    expect(container.read(webTunnelProvider('race')), isEmpty);
  });

  test(
    'reopen joins a reconnect without loading credentials or a third dial',
    () async {
      final gate = Completer<void>();
      final next = _ControlledSshClient(connectGate: gate.future);
      final container = fixture([_ControlledSshClient(), next]);
      final notifier = container.read(sshProvider('race').notifier);
      await notifier.connectWithoutShell(connection, options);
      final retry = notifier.reconnectNow();
      await Future<void>.delayed(Duration.zero);
      final reopen = notifier.ensureConnected(
        connection,
        () => throw StateError('must not load credentials'),
      );
      gate.complete();
      expect(await retry, isTrue);
      await reopen;
      expect(notifier.client, same(next));
      expect(next.connectCalls, 1);
      expect(next.disposeCalls, 0);
    },
  );

  test(
    'retry and reopen join pending initial credential lookup and dial',
    () async {
      final gate = Completer<SshConnectOptions>();
      final client = _ControlledSshClient();
      final container = fixture([client]);
      final notifier = container.read(sshProvider('race').notifier);
      final first = notifier.ensureConnected(connection, () => gate.future);
      final second = notifier.connectWithoutShell(connection, options);
      final retry = notifier.reconnectNow();
      gate.complete(options);
      await Future.wait([first, second]);
      expect(await retry, isTrue);
      expect(client.connectCalls, 1);
    },
  );

  for (final fails in [true, false]) {
    test(
      'late initial ${fails ? 'failure' : 'success'} cannot replace a newer client',
      () async {
        final gate = Completer<void>();
        final old = _ControlledSshClient(
          connectGate: gate.future,
          connectError: fails ? SshConnectionError('late failure') : null,
        );
        final next = _ControlledSshClient();
        final container = fixture([old, next]);
        final notifier = container.read(sshProvider('race').notifier);
        final obsolete = notifier.connectWithoutShell(connection, options);
        final failure = expectLater(
          obsolete,
          throwsA(isA<SshConnectionError>()),
        );
        await Future<void>.delayed(Duration.zero);
        await notifier.disconnect();
        await notifier.connectWithoutShell(connection, options);
        gate.complete();
        await failure;
        expect(notifier.client, same(next));
        expect(next.disposeCalls, 0);
        expect(container.read(sshProvider('race')).isConnected, isTrue);
        expect(container.read(sshProvider('race')).error, isNull);
      },
    );
  }

  test(
    'cached live connection needs no credential lookup or replacement',
    () async {
      final client = _ControlledSshClient();
      final container = fixture([client]);
      final notifier = container.read(sshProvider('race').notifier);
      await notifier.connectWithoutShell(connection, options);
      await notifier.ensureConnected(
        connection,
        () => throw StateError('not needed'),
      );
      expect(client.connectCalls, 1);
      expect(client.disposeCalls, 0);
    },
  );

  test(
    'authentication failure does not schedule endless automatic retries',
    () async {
      final container = fixture([
        _ControlledSshClient(connectError: SshAuthenticationError('bad key')),
      ]);
      final notifier = container.read(sshProvider('race').notifier);
      await expectLater(
        notifier.connectWithoutShell(connection, options),
        throwsA(isA<SshAuthenticationError>()),
      );
      await Future<void>.delayed(Duration.zero);
      expect(container.read(sshProvider('race')).isReconnecting, isFalse);
      expect(container.read(sshProvider('race')).hasError, isTrue);
    },
  );

  test('concurrent immediate retries share one SSH dial', () async {
    SharedPreferences.setMockInitialValues({});
    final reconnectGate = Completer<void>();
    final initial = _ControlledSshClient();
    final reconnecting = _ControlledSshClient(
      connectGate: reconnectGate.future,
    );
    final clients = <_ControlledSshClient>[initial, reconnecting];
    var factoryCalls = 0;

    final container = ProviderContainer(
      overrides: [
        sshClientFactoryProvider.overrideWithValue(() {
          final client = clients[factoryCalls];
          factoryCalls++;
          return client;
        }),
        networkMonitorProvider.overrideWithValue(_OnlineNetworkMonitor()),
      ],
    );
    addTearDown(container.dispose);
    final provider = sshProvider('connection-1');
    final subscription = container.listen(provider, (previous, next) {});
    addTearDown(subscription.close);
    final notifier = container.read(provider.notifier);
    final connection = Connection(
      id: 'connection-1',
      name: 'test',
      host: 'example.test',
      username: 'user',
      createdAt: DateTime(2026),
    );

    await notifier.connectWithoutShell(
      connection,
      const SshConnectOptions(password: 'secret'),
    );
    final firstRetry = notifier.reconnectNow();
    final secondRetry = notifier.reconnectNow();

    await Future<void>.delayed(Duration.zero);
    expect(
      factoryCalls,
      2,
      reason: 'both retries must share the second client',
    );
    expect(reconnecting.connectCalls, 1);

    reconnectGate.complete();
    expect(await firstRetry, isTrue);
    expect(await secondRetry, isTrue);
    expect(container.read(provider).isConnected, isTrue);
  });

  test(
    'initial connection failure is rethrown and enters retry mode',
    () async {
      SharedPreferences.setMockInitialValues({});
      final failure = SshConnectionError('host unreachable');
      final container = ProviderContainer(
        overrides: [
          sshClientFactoryProvider.overrideWithValue(
            () => _ControlledSshClient(connectError: failure),
          ),
          networkMonitorProvider.overrideWithValue(_OnlineNetworkMonitor()),
        ],
      );
      addTearDown(container.dispose);
      final provider = sshProvider('connection-2');
      final subscription = container.listen(provider, (previous, next) {});
      addTearDown(subscription.close);
      final notifier = container.read(provider.notifier);
      final connection = Connection(
        id: 'connection-2',
        name: 'test',
        host: 'unreachable.test',
        username: 'user',
        createdAt: DateTime(2026),
      );

      await expectLater(
        notifier.connectWithoutShell(
          connection,
          const SshConnectOptions(password: 'secret'),
        ),
        throwsA(same(failure)),
      );
      await Future<void>.delayed(Duration.zero);

      final state = container.read(provider);
      expect(state.isReconnecting, isTrue);
      expect(state.nextRetryAt, isNotNull);
    },
  );
}
