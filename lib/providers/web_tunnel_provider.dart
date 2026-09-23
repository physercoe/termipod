import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/misc.dart' show KeepAliveLink;
import '../services/ssh/local_forward.dart';
import '../services/ssh/ssh_client.dart';
import 'ssh_provider.dart';

class WebTunnel {
  const WebTunnel({
    required this.id,
    required this.remoteHost,
    required this.remotePort,
    required this.path,
    required this.name,
    required this.forward,
  });
  final String id;
  final String remoteHost;
  final int remotePort;
  final String path;
  final String name;
  final LocalForward forward;
  Uri get uri => Uri.parse('http://127.0.0.1:${forward.port}$path');
}

/// Runtime-only configurations, scoped to a saved SSH connection, not a route.
class WebTunnelNotifier extends Notifier<List<WebTunnel>> {
  WebTunnelNotifier(this.connectionId);
  final String connectionId;
  int _generation = 0;
  int _nextId = 0;
  bool _disposed = false;
  final Set<LocalForward> _forwards = {};
  KeepAliveLink? _keepAlive;

  @override
  List<WebTunnel> build() {
    ref.listen(sshProvider(connectionId), (previous, next) {
      if (next.isDisconnected && !next.isReconnecting) {
        stopAll();
      } else {
        for (final tunnel in state) {
          tunnel.forward.setAvailable(next.isConnected);
        }
      }
    });
    ref.onDispose(() {
      _disposed = true;
      ++_generation;
      for (final forward in _forwards) {
        unawaited(forward.close());
      }
      _forwards.clear();
    });
    return [];
  }

  Future<WebTunnel> start({
    required String remoteHost,
    required int remotePort,
    String path = '/',
    String name = '',
  }) async {
    remoteHost = remoteHost.trim();
    if (remoteHost.isEmpty ||
        RegExp(r'[\s/\\]').hasMatch(remoteHost) ||
        remotePort < 1 ||
        remotePort > 65535) {
      throw ArgumentError('Invalid remote host or port');
    }
    path = path.trim();
    if (path.isEmpty) path = '/';
    if (!path.startsWith('/')) path = '/$path';
    Uri.parse(
      'http://127.0.0.1:1$path',
    ); // Validate before allocating a listener.
    final generation = _generation;
    if (!ref.read(sshProvider(connectionId)).isConnected) {
      throw SshConnectionError('SSH connection unavailable');
    }
    final forward = await LocalForward.start(() {
      if (_disposed || generation != _generation)
        throw StateError('Tunnel stopped');
      final ssh = ref.read(sshProvider(connectionId).notifier);
      final client = ssh.client;
      if (client == null)
        throw SshConnectionError('SSH connection unavailable');
      return client.openForward(remoteHost, remotePort);
    });
    if (_disposed || generation != _generation) {
      await forward.close();
      throw StateError('Tunnel stopped');
    }
    forward.setAvailable(ref.read(sshProvider(connectionId)).isConnected);
    _forwards.add(forward);
    _keepAlive ??= ref.keepAlive();
    final tunnel = WebTunnel(
      id: '${_nextId++}',
      remoteHost: remoteHost,
      remotePort: remotePort,
      path: path,
      name: name.trim().isEmpty ? '$remoteHost:$remotePort' : name.trim(),
      forward: forward,
    );
    state = [...state, tunnel];
    return tunnel;
  }

  void stop(String id) {
    final matches = state.where((t) => t.id == id).toList();
    state = state.where((t) => t.id != id).toList();
    for (final tunnel in matches) {
      _forwards.remove(tunnel.forward);
      unawaited(tunnel.forward.close());
    }
    if (state.isEmpty) {
      _keepAlive?.close();
      _keepAlive = null;
    }
  }

  void stopAll() {
    ++_generation;
    final old = state;
    state = [];
    _forwards.clear();
    _keepAlive?.close();
    _keepAlive = null;
    for (final tunnel in old) {
      unawaited(tunnel.forward.close());
    }
  }
}

final webTunnelProvider = NotifierProvider.autoDispose
    .family<WebTunnelNotifier, List<WebTunnel>, String>(WebTunnelNotifier.new);
