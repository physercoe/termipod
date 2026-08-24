import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:termipod/providers/connection_provider.dart';
import 'package:termipod/providers/ssh_provider.dart';
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
    _connected = false;
  }
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('concurrent immediate retries share one SSH dial', () async {
    SharedPreferences.setMockInitialValues({});
    final reconnectGate = Completer<void>();
    final initial = _ControlledSshClient();
    final reconnecting = _ControlledSshClient(connectGate: reconnectGate.future);
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
    expect(factoryCalls, 2, reason: 'both retries must share the second client');
    expect(reconnecting.connectCalls, 1);

    reconnectGate.complete();
    expect(await firstRetry, isTrue);
    expect(await secondRetry, isTrue);
    expect(container.read(provider).isConnected, isTrue);
  });

  test('initial connection failure is rethrown and enters retry mode', () async {
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
  });
}
