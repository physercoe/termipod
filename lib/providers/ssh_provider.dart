import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
// `KeepAliveLink` was moved out of the top-level export in
// flutter_riverpod 3.x — it now lives behind the `misc.dart` opt-in
// surface alongside the other "low-level / advanced" types. Import it
// explicitly so [SshNotifier] can hold the link returned by
// `ref.keepAlive()` (3.1.0+).
import 'package:flutter_riverpod/misc.dart' show KeepAliveLink;

import '../services/background/foreground_task_service.dart';
import '../services/network/network_monitor.dart';
import '../services/ssh/ssh_client.dart';
import 'connection_provider.dart';

/// Factory kept behind a provider so reconnection ownership can be exercised
/// without opening real sockets in unit tests.
final sshClientFactoryProvider = Provider<SshClient Function()>((ref) {
  return SshClient.new;
});

/// SSH connection state
class SshState {
  final SshConnectionState connectionState;
  final String? error;
  final String? sessionTitle;
  final bool isReconnecting;
  final int reconnectAttempt;
  final int? reconnectDelayMs;

  /// Whether network is available
  final bool isNetworkAvailable;

  /// Next retry time
  final DateTime? nextRetryAt;

  /// Whether reconnection is paused (network unavailable)
  final bool isPaused;

  const SshState({
    this.connectionState = SshConnectionState.disconnected,
    this.error,
    this.sessionTitle,
    this.isReconnecting = false,
    this.reconnectAttempt = 0,
    this.reconnectDelayMs,
    this.isNetworkAvailable = true,
    this.nextRetryAt,
    this.isPaused = false,
  });

  SshState copyWith({
    SshConnectionState? connectionState,
    String? error,
    String? sessionTitle,
    bool? isReconnecting,
    int? reconnectAttempt,
    int? reconnectDelayMs,
    bool? isNetworkAvailable,
    DateTime? nextRetryAt,
    bool? isPaused,
  }) {
    return SshState(
      connectionState: connectionState ?? this.connectionState,
      error: error,
      sessionTitle: sessionTitle ?? this.sessionTitle,
      isReconnecting: isReconnecting ?? this.isReconnecting,
      reconnectAttempt: reconnectAttempt ?? this.reconnectAttempt,
      reconnectDelayMs: reconnectDelayMs,
      isNetworkAvailable: isNetworkAvailable ?? this.isNetworkAvailable,
      nextRetryAt: nextRetryAt,
      isPaused: isPaused ?? this.isPaused,
    );
  }

  bool get isConnected => connectionState == SshConnectionState.connected;
  bool get isConnecting => connectionState == SshConnectionState.connecting;
  bool get isDisconnected => connectionState == SshConnectionState.disconnected;
  bool get hasError => connectionState == SshConnectionState.error;

  /// Whether waiting for network while offline
  bool get isWaitingForNetwork => isPaused && !isNetworkAvailable;
}

/// SSH connection manager — one instance per connectionId via .family provider.
///
/// Each connection gets its own isolated SshNotifier. A per-instance
/// generation guard still prevents lifecycle probes and user retries from
/// racing to replace that connection's client.
///
/// Lifetime model: the provider is `autoDispose`, but after a successful
/// connect we grab a `KeepAliveLink` so the SSH socket survives across
/// screen navigation (Hosts → Project → back to Hosts → back into
/// Terminal). The user releases the link by tapping **Disconnect** in
/// the terminal overflow menu, which calls [disconnect]. The Hosts row
/// shows a live-dot indicator by watching [activeSshConnectionIdsProvider],
/// which this notifier joins on connect and leaves on disconnect.
class SshNotifier extends Notifier<SshState> {
  final String connectionId;

  SshNotifier(this.connectionId);

  SshClient? _client;
  final SshForegroundTaskService _foregroundService = SshForegroundTaskService();

  // Keep-alive link grabbed after the first successful connect, released
  // by [disconnect] (the manual teardown path) or by the `ref.onDispose`
  // belt-and-braces. While the link is held, popping TerminalScreen no
  // longer tears down the SSH socket.
  KeepAliveLink? _keepAliveLink;

  // Cached connection info for reconnection
  Connection? _lastConnection;
  SshConnectOptions? _lastOptions;

  // Unlimited retry mode (0 = unlimited)
  static const int _maxReconnectAttempts = 0;

  // Exponential backoff (max 60s)
  static const int _baseDelayMs = 1000;
  static const int _maxDelayMs = 60000;
  static const double _backoffMultiplier = 1.5;

  // Connection state monitoring
  StreamSubscription<SshConnectionState>? _connectionStateSubscription;

  // Network state monitoring
  StreamSubscription<NetworkStatus>? _networkStatusSubscription;

  // Reconnect timer
  Timer? _reconnectTimer;
  Completer<bool>? _reconnectTimerCompleter;

  // At most one dial/authentication attempt may own [_client]. Mobile
  // lifecycle probes, the stale watchdog, and the Retry button can all fire
  // close together, so every immediate reconnect is routed through this
  // shared future.
  Future<bool>? _reconnectInFlight;
  int _connectionGeneration = 0;
  bool _manuallyDisconnected = false;

  // Disconnect callback (set externally by terminal screen)
  void Function()? onDisconnectDetected;

  // Reconnect success callback (set externally by terminal screen)
  Future<void> Function()? onReconnectSuccess;

  @override
  SshState build() {
    // Monitor network state
    final networkMonitor = ref.read(networkMonitorProvider);
    _startNetworkMonitoring(networkMonitor);

    // Register cleanup — auto-dispose handles calling this. Reached
    // only after [disconnect] (which closes [_keepAliveLink]) or app
    // teardown; otherwise the keep-alive link blocks disposal so the
    // SSH socket survives screen navigation.
    ref.onDispose(() {
      _reconnectTimer?.cancel();
      _connectionStateSubscription?.cancel();
      _networkStatusSubscription?.cancel();
      _client?.dispose();
      _foregroundService.stopService(connectionId: connectionId);
      // Provider lifecycle callbacks may not mutate another provider in
      // Riverpod 3. Manual disconnect removes the live-dot entry before
      // releasing the keep-alive link; app teardown discards both providers.
    });
    return SshState(isNetworkAvailable: networkMonitor.isOnline);
  }

  /// Hold the provider alive across navigation and surface this
  /// connection in [activeSshConnectionIdsProvider] so the Hosts row
  /// can render its live-dot indicator. Idempotent — safe to call on
  /// every successful (re)connect.
  void _markConnectionLive() {
    _keepAliveLink ??= ref.keepAlive();
    ref.read(activeSshConnectionIdsProvider.notifier).add(connectionId);
  }

  /// Start network state monitoring
  void _startNetworkMonitoring(NetworkMonitor monitor) {
    _networkStatusSubscription = monitor.statusStream.listen(_onNetworkStatusChanged);
  }

  /// Network state change handler
  void _onNetworkStatusChanged(NetworkStatus status) {
    final isOnline = status == NetworkStatus.online;

    state = state.copyWith(isNetworkAvailable: isOnline);

    if (isOnline) {
      if (state.isPaused && state.isReconnecting) {
        state = state.copyWith(isPaused: false, reconnectAttempt: 0);
        _cancelReconnectTimer();
        reconnectNow();
      }
    } else {
      if (state.isReconnecting) {
        state = state.copyWith(isPaused: true);
        _cancelReconnectTimer();
      }
    }
  }

  /// Calculate reconnect delay (exponential backoff)
  int _calculateDelay(int attempt) {
    final delay = (_baseDelayMs * _pow(_backoffMultiplier, attempt)).round();
    return delay.clamp(_baseDelayMs, _maxDelayMs);
  }

  double _pow(double base, int exponent) {
    double result = 1.0;
    for (int i = 0; i < exponent; i++) {
      result *= base;
    }
    return result;
  }

  /// Get the SSH client
  SshClient? get client => _client;

  /// Last connection info
  Connection? get lastConnection => _lastConnection;

  /// Last connection options
  SshConnectOptions? get lastOptions => _lastOptions;

  /// Establish SSH connection (with shell - legacy mode)
  Future<void> connect(Connection connection, SshConnectOptions options) async {
    state = state.copyWith(
      connectionState: SshConnectionState.connecting,
      error: null,
    );

    try {
      _client = ref.read(sshClientFactoryProvider)();

      await _client!.connect(
        host: connection.host,
        port: connection.port,
        username: connection.username,
        options: options,
      );

      await _client!.startShell();

      state = state.copyWith(
        connectionState: SshConnectionState.connected,
      );

      ref.read(connectionsProvider.notifier).updateLastConnected(connection.id);

      await _foregroundService.startService(
        connectionId: connectionId,
        connectionName: connection.name,
        host: connection.host,
      );
      _markConnectionLive();
    } on SshConnectionError catch (e) {
      state = state.copyWith(
        connectionState: SshConnectionState.error,
        error: e.message,
      );
      _client?.dispose();
      _client = null;
    } on SshAuthenticationError catch (e) {
      state = state.copyWith(
        connectionState: SshConnectionState.error,
        error: e.message,
      );
      _client?.dispose();
      _client = null;
    } catch (e) {
      state = state.copyWith(
        connectionState: SshConnectionState.error,
        error: e.toString(),
      );
      _client?.dispose();
      _client = null;
    }
  }

  /// Establish SSH connection (without shell - for tmux command mode)
  Future<void> connectWithoutShell(Connection connection, SshConnectOptions options) async {
    // Cache for reconnection
    _lastConnection = connection;
    _lastOptions = options;

    // Clean up any existing connection
    _manuallyDisconnected = false;
    _cancelReconnectTimer();
    final generation = ++_connectionGeneration;
    await _connectionStateSubscription?.cancel();
    _connectionStateSubscription = null;
    await _client?.dispose();
    _client = null;

    state = state.copyWith(
      connectionState: SshConnectionState.connecting,
      error: null,
      isReconnecting: false,
      reconnectAttempt: 0,
    );

    try {
      final client = ref.read(sshClientFactoryProvider)();
      _client = client;

      _connectionStateSubscription = client.connectionStateStream.listen(
        _onConnectionStateChanged,
      );

      await client.connect(
        host: connection.host,
        port: connection.port,
        username: connection.username,
        options: options,
      );
      if (generation != _connectionGeneration || _manuallyDisconnected) {
        await client.dispose();
        return;
      }

      state = state.copyWith(
        connectionState: SshConnectionState.connected,
        isReconnecting: false,
        reconnectAttempt: 0,
      );

      ref.read(connectionsProvider.notifier).updateLastConnected(connection.id);

      await _foregroundService.startService(
        connectionId: connectionId,
        connectionName: connection.name,
        host: connection.host,
      );
      _markConnectionLive();
    } on SshConnectionError catch (e) {
      state = state.copyWith(
        connectionState: SshConnectionState.error,
        error: e.message,
      );
      await _connectionStateSubscription?.cancel();
      _connectionStateSubscription = null;
      await _client?.dispose();
      _client = null;
      if (!_manuallyDisconnected && generation == _connectionGeneration) {
        Future.microtask(reconnect);
      }
      rethrow;
    } on SshAuthenticationError catch (e) {
      state = state.copyWith(
        connectionState: SshConnectionState.error,
        error: e.message,
      );
      await _connectionStateSubscription?.cancel();
      _connectionStateSubscription = null;
      await _client?.dispose();
      _client = null;
      rethrow;
    } catch (e) {
      state = state.copyWith(
        connectionState: SshConnectionState.error,
        error: e.toString(),
      );
      await _connectionStateSubscription?.cancel();
      _connectionStateSubscription = null;
      await _client?.dispose();
      _client = null;
      if (!_manuallyDisconnected && generation == _connectionGeneration) {
        Future.microtask(reconnect);
      }
      rethrow;
    }
  }

  /// Connection state change handler
  void _onConnectionStateChanged(SshConnectionState newState) {
    if (state.isConnected &&
        (newState == SshConnectionState.error ||
         newState == SshConnectionState.disconnected)) {
      state = state.copyWith(
        connectionState: newState,
        error: newState == SshConnectionState.error ? 'Connection lost' : null,
      );

      onDisconnectDetected?.call();

      if (!state.isReconnecting) {
        reconnect();
      }
    }
  }

  /// Attempt reconnection with exponential backoff
  Future<bool> reconnect() async {
    if (_lastConnection == null || _lastOptions == null) {
      return false;
    }

    final inFlight = _reconnectInFlight;
    if (inFlight != null) return inFlight;
    final scheduled = _reconnectTimerCompleter;
    if (scheduled != null) return scheduled.future;

    if (!state.isNetworkAvailable) {
      state = state.copyWith(
        isReconnecting: true,
        isPaused: true,
        error: 'Waiting for network...',
      );
      return false;
    }

    final attempt = state.reconnectAttempt;

    if (_maxReconnectAttempts > 0 && attempt >= _maxReconnectAttempts) {
      state = state.copyWith(
        isReconnecting: false,
        error: 'Max reconnect attempts reached',
      );
      return false;
    }

    final delayMs = _calculateDelay(attempt);
    final nextRetry = DateTime.now().add(Duration(milliseconds: delayMs));

    state = state.copyWith(
      isReconnecting: true,
      isPaused: false,
      reconnectAttempt: attempt + 1,
      reconnectDelayMs: delayMs,
      nextRetryAt: nextRetry,
    );

    final completer = Completer<bool>();
    _cancelReconnectTimer();
    _reconnectTimerCompleter = completer;
    _reconnectTimer = Timer(Duration(milliseconds: delayMs), () async {
      _reconnectTimer = null;
      _reconnectTimerCompleter = null;
      final result = await _startReconnectAttempt();
      if (!completer.isCompleted) {
        completer.complete(result);
      }
    });

    return completer.future;
  }

  Future<bool> _startReconnectAttempt() {
    final existing = _reconnectInFlight;
    if (existing != null) return existing;

    late final Future<bool> attempt;
    attempt = _doReconnect().whenComplete(() {
      if (identical(_reconnectInFlight, attempt)) {
        _reconnectInFlight = null;
      }
    });
    _reconnectInFlight = attempt;
    return attempt;
  }

  /// Perform the actual reconnection.
  Future<bool> _doReconnect() async {
    if (_lastConnection == null || _lastOptions == null) {
      return false;
    }

    if (!state.isNetworkAvailable) {
      state = state.copyWith(isPaused: true);
      return false;
    }

    final generation = ++_connectionGeneration;
    SshClient? candidate;
    try {
      state = state.copyWith(
        connectionState: SshConnectionState.connecting,
        isReconnecting: true,
        isPaused: false,
        error: null,
      );

      await _connectionStateSubscription?.cancel();
      _connectionStateSubscription = null;

      await _client?.dispose();
      _client = null;
      candidate = ref.read(sshClientFactoryProvider)();

      await candidate.connect(
        host: _lastConnection!.host,
        port: _lastConnection!.port,
        username: _lastConnection!.username,
        options: _lastOptions!,
      );
      if (generation != _connectionGeneration || _manuallyDisconnected) {
        await candidate.dispose();
        return false;
      }

      _client = candidate;
      _connectionStateSubscription = candidate.connectionStateStream.listen(
        _onConnectionStateChanged,
      );

      state = state.copyWith(
        connectionState: SshConnectionState.connected,
        isReconnecting: false,
        isPaused: false,
        reconnectAttempt: 0,
        error: null,
        nextRetryAt: null,
      );

      ref.read(connectionsProvider.notifier).updateLastConnected(_lastConnection!.id);
      await _foregroundService.startService(
        connectionId: connectionId,
        connectionName: _lastConnection!.name,
        host: _lastConnection!.host,
      );
      _markConnectionLive();

      try {
        await onReconnectSuccess?.call();
      } catch (_) {
        // The transport is healthy even if rebuilding the screen-local
        // terminal backend fails. The screen surfaces that setup error and a
        // later retry can rebuild it without discarding this socket.
      }

      return true;
    } catch (e) {
      if (identical(_client, candidate)) _client = null;
      await candidate?.dispose();
      if (generation != _connectionGeneration || _manuallyDisconnected) {
        return false;
      }
      state = state.copyWith(
        connectionState: SshConnectionState.error,
        isReconnecting: true,
        error: 'Reconnect failed: $e',
      );

      if (_maxReconnectAttempts == 0 || state.reconnectAttempt < _maxReconnectAttempts) {
        // Run after [_startReconnectAttempt]'s whenComplete clears the
        // single-flight future; a microtask can run too early and accidentally
        // reuse the just-failed attempt instead of scheduling the next one.
        Timer.run(reconnect);
      }

      return false;
    }
  }

  /// Reconnect immediately (user action)
  Future<bool> reconnectNow() async {
    _manuallyDisconnected = false;
    _cancelReconnectTimer();
    state = state.copyWith(
      reconnectAttempt: 0,
      isReconnecting: true,
      isPaused: false,
    );
    return _startReconnectAttempt();
  }

  void _cancelReconnectTimer() {
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
    final completer = _reconnectTimerCompleter;
    _reconnectTimerCompleter = null;
    if (completer != null && !completer.isCompleted) {
      completer.complete(false);
    }
  }

  /// Check if connection is active
  bool checkConnection() {
    return _client != null && _client!.isConnected;
  }

  /// Reset reconnection state
  void resetReconnect() {
    _cancelReconnectTimer();
    state = state.copyWith(
      isReconnecting: false,
      isPaused: false,
      reconnectAttempt: 0,
      reconnectDelayMs: null,
      nextRetryAt: null,
    );
  }

  /// Disconnect. Closes the keep-alive link so the provider can
  /// auto-dispose once the last listener (typically TerminalScreen) is
  /// gone — which restores the original "no live socket" baseline.
  Future<void> disconnect() async {
    _manuallyDisconnected = true;
    ++_connectionGeneration;
    _cancelReconnectTimer();
    await _connectionStateSubscription?.cancel();
    _connectionStateSubscription = null;

    await _foregroundService.stopService(connectionId: connectionId);
    await _client?.disconnect();
    _client = null;

    state = state.copyWith(
      connectionState: SshConnectionState.disconnected,
      error: null,
      sessionTitle: null,
      isReconnecting: false,
      isPaused: false,
      reconnectAttempt: 0,
      nextRetryAt: null,
    );

    // Drop from the Hosts-row indicator set + release keep-alive so
    // the provider becomes eligible for auto-dispose again.
    ref.read(activeSshConnectionIdsProvider.notifier).remove(connectionId);
    _keepAliveLink?.close();
    _keepAliveLink = null;
  }

  /// Update session title
  void updateSessionTitle(String title) {
    state = state.copyWith(sessionTitle: title);
  }

  /// Send data
  void write(String data) {
    _client?.write(data);
  }

  /// Resize terminal
  void resize(int cols, int rows) {
    _client?.resize(cols, rows);
  }
}

/// SSH provider — keyed by connectionId.
///
/// Each connection gets its own isolated instance. The provider is
/// declared `autoDispose`, but [SshNotifier] grabs a `KeepAliveLink`
/// after a successful connect so the socket survives screen navigation;
/// the link is released by [SshNotifier.disconnect]. See
/// [activeSshConnectionIdsProvider] for the corresponding visibility
/// signal that drives the Hosts-row live-dot indicator.
final sshProvider = NotifierProvider.autoDispose.family<SshNotifier, SshState, String>(
  (connectionId) => SshNotifier(connectionId),
);

/// IDs of personal-host SSH connections whose [SshNotifier] currently
/// holds a `KeepAliveLink` — i.e. those the user has opened at least
/// once and not explicitly disconnected from. The Hosts row watches
/// this set to render a green "alive" dot; the steward and any other
/// caller can read it to learn which personal hosts are reachable
/// without a fresh SSH handshake.
///
/// Maintained from inside [SshNotifier] on connect/disconnect; widgets
/// MUST NOT mutate it directly.
class _ActiveSshConnectionIds extends Notifier<Set<String>> {
  @override
  Set<String> build() => const <String>{};

  void add(String id) {
    if (state.contains(id)) return;
    state = {...state, id};
  }

  void remove(String id) {
    if (!state.contains(id)) return;
    final next = state.where((x) => x != id).toSet();
    state = next;
  }
}

final activeSshConnectionIdsProvider =
    NotifierProvider<_ActiveSshConnectionIds, Set<String>>(
  _ActiveSshConnectionIds.new,
);
