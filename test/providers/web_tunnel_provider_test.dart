import 'dart:async';
import 'dart:io';

import 'package:dartssh2/dartssh2.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/providers/ssh_provider.dart';
import 'package:termipod/providers/web_tunnel_provider.dart';
import 'package:termipod/services/ssh/ssh_client.dart';

class _Client extends SshClient {
  _Client(this.port);
  final int port;
  @override
  Future<SSHSocket> openForward(String host, int port) =>
      SSHSocket.connect('127.0.0.1', this.port);
}

class _Ssh extends SshNotifier {
  _Ssh(this.transport) : super('test');
  final SshClient transport;
  @override
  SshState build() =>
      const SshState(connectionState: SshConnectionState.connected);
  @override
  SshClient get client => transport;
  void change(SshState next) {
    state = next;
  }
}

void main() {
  test(
    'tunnels survive navigation/recovery, stop on explicit disconnect',
    () async {
      final remote = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
      addTearDown(remote.close);
      remote.listen((s) {
        s.add([42]);
        s.close();
      });
      final ssh = _Ssh(_Client(remote.port));
      final container = ProviderContainer(
        overrides: [sshProvider('test').overrideWith(() => ssh)],
      );
      addTearDown(container.dispose);
      final provider = webTunnelProvider('test');
      final subscription = container.listen(provider, (_, _) {});
      final manager = container.read(provider.notifier);
      final tunnel = await manager.start(
        remoteHost: '127.0.0.1',
        remotePort: 8080,
        path: 'hello?q=1',
      );
      expect(tunnel.uri.path, '/hello');
      expect(tunnel.uri.query, 'q=1');
      final port = tunnel.forward.port;
      subscription.close();
      await container.pump();
      expect(container.read(provider).single, same(tunnel));
      ssh.change(
        const SshState(
          connectionState: SshConnectionState.error,
          isReconnecting: true,
        ),
      );
      expect(container.read(provider).single.forward.port, port);
      ssh.change(const SshState(connectionState: SshConnectionState.connected));
      final client = await Socket.connect('127.0.0.1', port);
      expect(await client.first, [42]);
      client.destroy();
      ssh.change(const SshState());
      expect(container.read(provider), isEmpty);
      await Future<void>.delayed(Duration.zero);
      await expectLater(
        Socket.connect('127.0.0.1', port),
        throwsA(isA<SocketException>()),
      );
    },
  );

  test('stop during listener creation cannot publish a late tunnel', () async {
    final ssh = _Ssh(_Client(1));
    final container = ProviderContainer(
      overrides: [sshProvider('test').overrideWith(() => ssh)],
    );
    addTearDown(container.dispose);
    final manager = container.read(webTunnelProvider('test').notifier);
    final starting = manager.start(remoteHost: 'localhost', remotePort: 80);
    manager.stopAll();
    await expectLater(starting, throwsStateError);
    expect(container.read(webTunnelProvider('test')), isEmpty);
  });

  test('invalid service target is rejected before binding', () async {
    final container = ProviderContainer(
      overrides: [sshProvider('test').overrideWith(() => _Ssh(_Client(1)))],
    );
    addTearDown(container.dispose);
    final manager = container.read(webTunnelProvider('test').notifier);
    for (final host in ['', 'http://localhost', 'a b']) {
      await expectLater(
        manager.start(remoteHost: host, remotePort: 80),
        throwsArgumentError,
      );
    }
    await expectLater(
      manager.start(remoteHost: 'localhost', remotePort: 65536),
      throwsArgumentError,
    );
    expect(container.read(webTunnelProvider('test')), isEmpty);
  });
}
