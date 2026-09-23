import 'dart:async';
import 'dart:convert';
import 'package:flutter/foundation.dart';
import 'dart:io';
import 'dart:typed_data';

import 'package:dartssh2/dartssh2.dart';

import 'persistent_shell.dart';
import 'command_executor.dart';
import 'socks5_socket.dart';

/// SSH接続エラー
class SshConnectionError implements Exception {
  final String message;
  final Object? cause;

  SshConnectionError(this.message, [this.cause]);

  @override
  String toString() =>
      'SshConnectionError: $message${cause != null ? ' ($cause)' : ''}';
}

/// SSH認証エラー
class SshAuthenticationError implements Exception {
  final String message;
  final Object? cause;

  SshAuthenticationError(this.message, [this.cause]);

  @override
  String toString() =>
      'SshAuthenticationError: $message${cause != null ? ' ($cause)' : ''}';
}

/// SSH接続オプション
class SshConnectOptions {
  /// パスワード認証時のパスワード
  final String? password;

  /// 鍵認証時の秘密鍵（PEM形式）
  final String? privateKey;

  /// 秘密鍵のパスフレーズ
  final String? passphrase;

  /// ユーザー指定のtmuxパス（nullなら自動検出）
  final String? tmuxPath;

  /// 接続タイムアウト（秒）
  final int timeout;

  // Jump host (ProxyJump) fields
  final String? jumpHost;
  final int? jumpPort;
  final String? jumpUsername;
  final String? jumpPassword;
  final String? jumpPrivateKey;
  final String? jumpPassphrase;

  // SOCKS5 proxy fields
  final String? proxyHost;
  final int? proxyPort;
  final String? proxyUsername;
  final String? proxyPassword;

  const SshConnectOptions({
    this.password,
    this.privateKey,
    this.passphrase,
    this.tmuxPath,
    this.timeout = 30,
    this.jumpHost,
    this.jumpPort,
    this.jumpUsername,
    this.jumpPassword,
    this.jumpPrivateKey,
    this.jumpPassphrase,
    this.proxyHost,
    this.proxyPort,
    this.proxyUsername,
    this.proxyPassword,
  });
}

/// シェルオプション
class ShellOptions {
  /// ターミナルタイプ
  final String term;

  /// カラム数
  final int cols;

  /// 行数
  final int rows;

  const ShellOptions({
    this.term = 'xterm-256color',
    this.cols = 80,
    this.rows = 24,
  });
}

/// SSH接続イベント
class SshEvents {
  /// データ受信時
  final void Function(Uint8List data)? onData;

  /// 接続クローズ時
  final void Function()? onClose;

  /// エラー発生時
  final void Function(Object error)? onError;

  /// Fires when the interactive shell session started by [SshClient.startShell]
  /// reaches EOF (user typed `exit` / `logout`, Ctrl-D, or the remote shell
  /// crashed). Raw-PTY mode uses this to pop the terminal screen instead of
  /// silently waiting for the keep-alive watchdog to misread the exit as a
  /// network drop and trigger a reconnect into a brand-new shell.
  final void Function()? onShellEnd;

  const SshEvents({this.onData, this.onClose, this.onError, this.onShellEnd});

  SshEvents copyWith({
    void Function(Uint8List data)? onData,
    void Function()? onClose,
    void Function(Object error)? onError,
    void Function()? onShellEnd,
  }) {
    return SshEvents(
      onData: onData ?? this.onData,
      onClose: onClose ?? this.onClose,
      onError: onError ?? this.onError,
      onShellEnd: onShellEnd ?? this.onShellEnd,
    );
  }
}

/// SSH接続状態
enum SshConnectionState { disconnected, connecting, connected, error }

/// SSHクライアント
///
/// dartssh2をラップし、SSH接続を管理する。
class SshClient {
  SshClient({
    Future<SSHSocket> Function(String, int, Duration)? socketConnector,
    SSHClient Function(SSHSocket, String, SshConnectOptions)? transportFactory,
  }) : _socketConnector = socketConnector ?? _connectSocket,
       _transportFactory = transportFactory ?? _createTransport;

  final Future<SSHSocket> Function(String, int, Duration) _socketConnector;
  final SSHClient Function(SSHSocket, String, SshConnectOptions)
  _transportFactory;

  static Future<SSHSocket> _connectSocket(
    String host,
    int port,
    Duration timeout,
  ) => SSHSocket.connect(host, port, timeout: timeout);

  static SSHClient _createTransport(
    SSHSocket socket,
    String username,
    SshConnectOptions options,
  ) => SSHClient(
    socket,
    username: username,
    keepAliveInterval: null,
    identities: options.privateKey == null
        ? null
        : _parsePrivateKey(options.privateKey!, options.passphrase),
    onPasswordRequest: options.password == null
        ? null
        : () => options.password!,
  );

  /// Open a byte stream using the authenticated transport (including jumps).
  /// This does not execute a command or acquire the terminal exec lock.
  Future<SSHSocket> openForward(String host, int port) async {
    final client = _client;
    if (client == null || !isConnected)
      throw SshConnectionError('SSH connection unavailable');
    return client.forwardLocal(host, port);
  }

  SSHClient? _client;
  SSHClient? _jumpClient;
  SSHSession? _session;
  SSHSocket? _socket;

  SshConnectionState _state = SshConnectionState.disconnected;
  SshEvents _events = const SshEvents();
  String? _lastError;

  StreamSubscription<Uint8List>? _stdoutSubscription;
  StreamSubscription<Uint8List>? _stderrSubscription;

  /// 持続的シェルセッション（ポーリング用）
  PersistentShell? _persistentShell;

  /// 検出されたtmuxバイナリの絶対パス
  String? _tmuxPath;

  CommandExecutor? _commands;
  SshConnectOptions? _options;
  Future<void>? _terminalSetup;
  Future<void>? _probeInFlight;
  bool _closing = false;
  bool _disposed = false;

  /// A protocol request, independent of shell startup, polling and exec locks.
  /// All callers join the same probe; a late reply gets a second grace window
  /// without sending a second request or mis-associating SSH global replies.
  Future<void> probeTransport() {
    final existing = _probeInFlight;
    if (existing != null) return existing;
    late final Future<void> operation;
    operation = _probeTransport().whenComplete(() {
      if (identical(_probeInFlight, operation)) _probeInFlight = null;
    });
    _probeInFlight = operation;
    return operation;
  }

  Future<void> _probeTransport() async {
    final transport = _client;
    if (!isConnected || transport == null || transport.isClosed) {
      throw SshConnectionError('SSH transport closed');
    }
    final reply = transport.ping();
    try {
      await reply.timeout(const Duration(seconds: 5));
    } on TimeoutException {
      await reply.timeout(const Duration(seconds: 5));
    }
    if (_closing || !identical(_client, transport) || transport.isClosed) {
      throw SshConnectionError('SSH transport changed during probe');
    }
  }

  /// Terminal-only work has its own budget and cannot fail SSH authentication.
  Future<void> prepareTerminal() {
    return _terminalSetup ??= _prepareTerminal();
  }

  Future<void> _prepareTerminal() async {
    if (!isConnected) throw SshConnectionError('Not connected');
    final configured = _options?.tmuxPath;
    if (configured != null && configured.isNotEmpty) {
      try {
        final result = await execWithExitCode(
          'test -x ${_shellEscape(configured)}',
          timeout: const Duration(seconds: 3),
        );
        if (result.exitCode == 0) _tmuxPath = configured;
      } catch (_) {
        /* Fall through to bounded discovery. */
      }
    }
    if (_tmuxPath == null) await _detectTmuxPath();
    await _startPersistentShell(timeout: const Duration(seconds: 3));
  }

  /// tmuxの絶対パス（未検出なら null）
  String? get tmuxPath => _tmuxPath;

  /// Keep-aliveタイマー
  Timer? _keepAliveTimer;

  /// 接続監視用のStreamController
  final _connectionStateController =
      StreamController<SshConnectionState>.broadcast();

  /// 接続状態のストリーム（外部から監視用）
  Stream<SshConnectionState> get connectionStateStream =>
      _connectionStateController.stream;

  /// Keep-alive最小間隔（秒）
  static const int _minKeepAliveIntervalSeconds = 5;

  /// Keep-alive最大間隔（秒）
  static const int _maxKeepAliveIntervalSeconds = 30;

  /// 現在のKeep-alive間隔（動的に調整）
  int _currentKeepAliveIntervalSeconds = 10;

  /// Keep-alive連続成功回数
  int _keepAliveSuccessCount = 0;

  /// 現在の接続状態
  SshConnectionState get state => _state;

  /// 接続中かどうか
  bool get isConnected => _state == SshConnectionState.connected;

  /// 最後のエラーメッセージ
  String? get lastError => _lastError;

  /// SFTPクライアントを取得
  ///
  /// 接続済みの場合のみSFTPセッションを開始して返す。
  /// 使用後は呼び出し側で close() すること。
  Future<SftpClient> openSftp() async {
    if (!isConnected || _client == null) {
      throw SshConnectionError('SFTP requires an active SSH connection');
    }
    return await _client!.sftp();
  }

  /// SSH接続を確立する
  ///
  /// [host] ホスト名またはIPアドレス
  /// [port] ポート番号
  /// [username] ユーザー名
  /// [options] 接続オプション（認証情報など）
  Future<void> connect({
    required String host,
    required int port,
    required String username,
    required SshConnectOptions options,
  }) async {
    // バリデーション
    if (_disposed) throw SshConnectionError('SSH client disposed');
    _validateConnectionParams(host, port, username, options);
    _options = options;
    _closing = false;

    _state = SshConnectionState.connecting;
    _lastError = null;
    _tmuxPath = null;
    final deadline = DateTime.now().add(Duration(seconds: options.timeout));
    var phase = 'socket connection';

    try {
      // Step 1: Create the initial socket (optionally through SOCKS5 proxy)
      SSHSocket initialSocket;
      if (options.proxyHost != null && options.proxyHost!.isNotEmpty) {
        // Connect through SOCKS5 proxy
        final proxyTarget = options.jumpHost ?? host;
        final proxyTargetPort = options.jumpPort ?? port;
        initialSocket = await Socks5Socket.connect(
          proxyHost: options.proxyHost!,
          proxyPort: options.proxyPort ?? 1080,
          targetHost: proxyTarget,
          targetPort: proxyTargetPort,
          username: options.proxyUsername,
          password: options.proxyPassword,
          timeout: Duration(seconds: options.timeout),
        );
      } else {
        final directHost = options.jumpHost ?? host;
        final directPort = options.jumpPort ?? port;
        initialSocket = await _socketConnector(
          directHost,
          directPort,
          _remaining(deadline),
        );
      }

      // Retain ownership even if parsing credentials or jump setup fails.
      _socket = initialSocket;
      if (_disposed) throw SshConnectionError('SSH connection cancelled');
      // Step 2: If jump host is configured, establish jump connection first
      if (options.jumpHost != null && options.jumpHost!.isNotEmpty) {
        phase = 'jump-host authentication';
        final jumpUsername = options.jumpUsername ?? username;
        if (options.jumpPrivateKey != null) {
          _jumpClient = SSHClient(
            initialSocket,
            keepAliveInterval: null,
            username: jumpUsername,
            identities: _parsePrivateKey(
              options.jumpPrivateKey!,
              options.jumpPassphrase,
            ),
          );
        } else if (options.jumpPassword != null) {
          _jumpClient = SSHClient(
            initialSocket,
            keepAliveInterval: null,
            username: jumpUsername,
            onPasswordRequest: () => options.jumpPassword!,
          );
        } else {
          throw SshAuthenticationError(
            'No authentication method for jump host',
          );
        }
        await _jumpClient!.authenticated.timeout(_remaining(deadline));

        // Forward through jump host to the actual target
        phase = 'jump-host forwarding';
        _socket = await _jumpClient!
            .forwardLocal(host, port)
            .timeout(_remaining(deadline));
      } else {
        _socket = initialSocket;
      }

      if (_disposed) throw SshConnectionError('SSH connection cancelled');
      phase = 'SSH authentication';
      _client = _transportFactory(_socket!, username, options);

      // 認証完了を待機
      await _client!.authenticated.timeout(_remaining(deadline));

      if (_disposed) throw SshConnectionError('SSH connection cancelled');
      final transport = _client!;
      if (transport.isClosed)
        throw SshConnectionError('SSH closed after authentication');
      _commands = CommandExecutor(transport.execute);
      // Observe real EOF directly, not only a later shell-command failure.
      unawaited(
        transport.done.then(
          (_) => _transportClosed(transport),
          onError: (Object error) => _transportClosed(transport, error),
        ),
      );

      _state = SshConnectionState.connected;
      _connectionStateController.add(_state);

      // Keep-aliveを開始
      _startKeepAlive();
    } on SocketException catch (e) {
      _state = SshConnectionState.error;
      _lastError = 'Connection failed during $phase: ${e.message}';
      await _cleanup();
      throw SshConnectionError(_lastError!, e);
    } on SshAuthenticationError catch (e) {
      _state = SshConnectionState.error;
      _lastError = e.message;
      await _cleanup();
      rethrow;
    } on SSHAuthFailError catch (e) {
      _state = SshConnectionState.error;
      _lastError = 'Authentication failed: ${e.message}';
      await _cleanup();
      throw SshAuthenticationError(_lastError!, e);
    } catch (e) {
      _state = SshConnectionState.error;
      _lastError = 'Connection failed during $phase: $e';
      await _cleanup();
      throw SshConnectionError(_lastError!, e);
    }
  }

  void _transportClosed(SSHClient transport, [Object? error]) {
    if (_closing || !identical(_client, transport)) return;
    _lastError = error == null
        ? 'SSH transport closed'
        : 'SSH transport closed: $error';
    _updateState(SshConnectionState.error);
    _events.onError?.call(SshConnectionError(_lastError!));
  }

  Duration _remaining(DateTime deadline) {
    final remaining = deadline.difference(DateTime.now());
    if (remaining <= Duration.zero) {
      throw TimeoutException('SSH connection timed out');
    }
    return remaining;
  }

  String _shellEscape(String value) {
    return "'${value.replaceAll("'", "'\\''")}'";
  }

  /// 接続パラメータをバリデート
  void _validateConnectionParams(
    String host,
    int port,
    String username,
    SshConnectOptions options,
  ) {
    if (host.trim().isEmpty) {
      throw SshConnectionError('Host is required');
    }
    if (username.trim().isEmpty) {
      throw SshConnectionError('Username is required');
    }
    if (port < 1 || port > 65535) {
      throw SshConnectionError('Invalid port number: $port');
    }
    if (options.password == null && options.privateKey == null) {
      throw SshAuthenticationError(
        'Either password or privateKey must be provided',
      );
    }
  }

  /// 秘密鍵をパース
  static List<SSHKeyPair> _parsePrivateKey(
    String privateKey,
    String? passphrase,
  ) {
    try {
      // SSHKeyPair.fromPem は List<SSHKeyPair> を返す
      final keyPairs = SSHKeyPair.fromPem(privateKey, passphrase);
      if (keyPairs.isEmpty) {
        throw SshAuthenticationError('No valid key found in PEM data');
      }
      return keyPairs;
    } on FormatException catch (e) {
      throw SshAuthenticationError('Invalid private key format: ${e.message}');
    } catch (e) {
      if (e is SshAuthenticationError) rethrow;
      if (passphrase == null && privateKey.contains('ENCRYPTED')) {
        throw SshAuthenticationError(
          'Private key is encrypted, passphrase required',
        );
      }
      throw SshAuthenticationError('Failed to parse private key: $e');
    }
  }

  /// 接続を切断する
  Future<void> disconnect() async {
    await _cleanup();
    _updateState(SshConnectionState.disconnected);
    _events.onClose?.call();
  }

  /// 状態を更新してストリームに通知
  void _updateState(SshConnectionState newState) {
    if (_state != newState) {
      _state = newState;
      _connectionStateController.add(newState);
    }
  }

  /// リソースをクリーンアップ
  Future<void> _cleanup() async {
    _closing = true;
    _commands = null;
    // Keep-aliveを停止
    _stopKeepAlive();

    // 持続的シェルを解放
    await _persistentShell?.dispose();
    _persistentShell = null;

    await _stdoutSubscription?.cancel();
    await _stderrSubscription?.cancel();
    _stdoutSubscription = null;
    _stderrSubscription = null;

    _session?.close();
    _session = null;

    _client?.close();
    _client = null;

    _socket?.close();
    _socket = null;

    _jumpClient?.close();
    _jumpClient = null;
  }

  /// 持続的シェルを開始
  Future<void> _startPersistentShell({Duration? timeout}) async {
    if (_client == null) return;

    try {
      _persistentShell = PersistentShell(_client!);
      await _persistentShell!.start(timeout: timeout);
    } catch (e) {
      // 持続的シェルの開始に失敗しても接続自体は継続
      // 従来のexec()メソッドにフォールバック
      await _persistentShell?.dispose();
      _persistentShell = null;
    }
  }

  /// 持続的シェルを再起動
  Future<void> restartPersistentShell() async {
    if (_client == null || !isConnected) return;

    try {
      await _persistentShell?.dispose();
      _persistentShell = PersistentShell(_client!);
      await _persistentShell!.start();
    } catch (e) {
      _persistentShell = null;
    }
  }

  Future<void> _detectTmuxPath() async {
    // Common paths first: no interactive/login shell hooks on the fast path.
    for (final command in [
      r'''for p in /opt/homebrew/bin/tmux /usr/local/bin/tmux /usr/bin/tmux; do if test -x "$p"; then printf '%s\n' "$p"; exit; fi; done; command -v tmux''',
      r"$SHELL -lc 'command -v tmux'",
    ]) {
      try {
        final result = await execWithExitCode(
          command,
          timeout: const Duration(seconds: 3),
        );
        final path = result.stdout.trim();
        if (result.exitCode == 0 &&
            path.startsWith('/') &&
            !path.contains('\n')) {
          _tmuxPath = path;
          return;
        }
      } catch (_) {
        /* Detection failure belongs to terminal setup, not SSH. */
      }
    }
  }

  /// コマンド内の `tmux` を検出済み絶対パスに置換
  String _resolveTmuxCommand(String command) {
    if (_tmuxPath == null) {
      debugPrint('_resolveTmuxCommand: _tmuxPath=null, command unchanged');
      return command;
    }
    final resolved = command.replaceAllMapped(
      RegExp(r'(^|;\s*)tmux\b'),
      (m) => '${m[1]}$_tmuxPath',
    );
    if (resolved != command) {
      debugPrint('_resolveTmuxCommand: "$command" => "$resolved"');
    }
    return resolved;
  }

  /// Keep-aliveを開始
  ///
  /// 定期的に軽量なコマンドを実行して接続が生きているか確認する。
  /// 接続が切れていれば即座にエラー状態に遷移する。
  /// 間隔は動的に調整される（成功時は延長、失敗時は短縮）。
  void _startKeepAlive() {
    _stopKeepAlive();
    _currentKeepAliveIntervalSeconds = 10; // 初期値10秒
    _keepAliveSuccessCount = 0;
    _scheduleNextKeepAlive();
  }

  /// 次のKeep-aliveをスケジュール
  void _scheduleNextKeepAlive() {
    _keepAliveTimer?.cancel();
    _keepAliveTimer = Timer(
      Duration(seconds: _currentKeepAliveIntervalSeconds),
      () async {
        await _sendKeepAlive();
        if (isConnected) {
          _scheduleNextKeepAlive();
        }
      },
    );
  }

  /// Keep-aliveを停止
  void _stopKeepAlive() {
    _keepAliveTimer?.cancel();
    _keepAliveTimer = null;
  }

  /// Keep-alive間隔を調整
  void _adjustKeepAliveInterval({required bool success}) {
    if (success) {
      _keepAliveSuccessCount++;
      // 3回連続成功で間隔を延長
      if (_keepAliveSuccessCount >= 3) {
        _currentKeepAliveIntervalSeconds =
            (_currentKeepAliveIntervalSeconds + 5).clamp(
              _minKeepAliveIntervalSeconds,
              _maxKeepAliveIntervalSeconds,
            );
        _keepAliveSuccessCount = 0;
      }
    } else {
      // 失敗時は最小間隔に戻す
      _currentKeepAliveIntervalSeconds = _minKeepAliveIntervalSeconds;
      _keepAliveSuccessCount = 0;
    }
  }

  /// Keep-aliveパケットを送信
  Future<void> _sendKeepAlive() async {
    if (!isConnected || _client == null) {
      return;
    }

    try {
      await probeTransport();
      _adjustKeepAliveInterval(success: true);
    } catch (e) {
      if (_closing || !isConnected) return;
      _adjustKeepAliveInterval(success: false);
      // Keep-alive失敗 = 接続切断
      _lastError = 'Connection lost: $e';
      _updateState(SshConnectionState.error);
      _events.onError?.call(SshConnectionError(_lastError!));
      _events.onClose?.call();
    }
  }

  /// インタラクティブシェルを開始する
  ///
  /// [options] シェルオプション
  Future<void> startShell([ShellOptions options = const ShellOptions()]) async {
    if (!isConnected || _client == null) {
      throw SshConnectionError('Not connected');
    }

    try {
      _session = await _client!.shell(
        pty: SSHPtyConfig(
          type: options.term,
          width: options.cols,
          height: options.rows,
        ),
      );

      // stdout/stderrのリスナーを設定
      _stdoutSubscription = _session!.stdout.listen(
        _handleData,
        onError: _handleError,
        onDone: _handleDone,
      );

      _stderrSubscription = _session!.stderr.listen(
        _handleData,
        onError: _handleError,
      );
    } catch (e) {
      throw SshConnectionError('Failed to start shell: $e', e);
    }
  }

  /// データ受信ハンドラ
  void _handleData(Uint8List data) {
    _events.onData?.call(data);
  }

  /// エラーハンドラ
  void _handleError(Object error) {
    _lastError = error.toString();
    _events.onError?.call(error);
  }

  /// 完了ハンドラ
  void _handleDone() {
    _state = SshConnectionState.disconnected;
    // Fire the dedicated shell-end signal first so consumers (raw PTY
    // backend → terminal screen) can route a clean disconnect *before*
    // the generic onClose listeners or any background watchdog can
    // misread this as a transport failure and queue a reconnect.
    _events.onShellEnd?.call();
    _events.onClose?.call();
  }

  /// シェルにデータを書き込む
  ///
  /// [data] 送信データ（文字列）
  void write(String data) {
    if (!isConnected || _session == null) {
      throw SshConnectionError('Not connected or shell not started');
    }
    _session!.write(utf8.encode(data));
  }

  /// シェルにバイトデータを書き込む
  ///
  /// [data] 送信データ（バイト）
  void writeBytes(Uint8List data) {
    if (!isConnected || _session == null) {
      throw SshConnectionError('Not connected or shell not started');
    }
    _session!.write(data);
  }

  /// ターミナルサイズを変更する
  ///
  /// [cols] カラム数
  /// [rows] 行数
  void resize(int cols, int rows) {
    if (_session == null) {
      return; // シェルが開始されていない場合は何もしない
    }

    try {
      _session!.resizeTerminal(cols, rows);
    } catch (e) {
      // リサイズエラーは警告のみ（致命的ではない）
      _lastError = 'Failed to resize: $e';
    }
  }

  /// コマンドを実行して結果を取得する
  ///
  /// [command] 実行コマンド
  /// [timeout] タイムアウト時間
  /// 戻り値: コマンド出力
  Future<String> exec(String command, {Duration? timeout}) async {
    if (!isConnected || _client == null) {
      throw SshConnectionError('Not connected');
    }

    final result = await execWithExitCode(command, timeout: timeout);
    return result.stdout + result.stderr;
  }

  /// 持続的シェル経由でコマンドを実行（高速）
  ///
  /// チャネル開閉のオーバーヘッドを排除し、1 RTT程度で実行可能。
  /// ポーリングなど高頻度のコマンド実行に適している。
  ///
  /// [command] 実行コマンド
  /// [timeout] タイムアウト時間
  /// 戻り値: コマンド出力
  Future<String> execPersistent(String command, {Duration? timeout}) async {
    if (!isConnected || _client == null) {
      throw SshConnectionError('Not connected');
    }

    final resolvedCommand = _resolveTmuxCommand(command);

    // 持続的シェルが利用できない場合は従来のexec()にフォールバック
    if (_persistentShell == null || !_persistentShell!.isStarted) {
      return exec(resolvedCommand, timeout: timeout);
    }

    try {
      return await _persistentShell!.exec(resolvedCommand, timeout: timeout);
    } on PersistentShellError catch (e) {
      // Retrying after a timeout/EOF could execute a mutation twice. Only a
      // command rejected before it was sent (for example a busy shell) may
      // use the ordinary channel fallback.
      if (e.mayHaveExecuted) rethrow;
      return exec(resolvedCommand, timeout: timeout);
    }
  }

  /// コマンドを実行して終了コードを取得する
  ///
  /// [command] 実行コマンド
  /// 戻り値: (stdout, stderr, exitCode)
  Future<({String stdout, String stderr, int? exitCode})> execWithExitCode(
    String command, {
    Duration? timeout,
  }) async {
    if (!isConnected || _client == null) {
      throw SshConnectionError('Not connected');
    }

    final commands = _commands;
    if (commands == null) throw SshConnectionError('Not connected');
    try {
      return await commands.run(
        _resolveTmuxCommand(command),
        timeout: timeout ?? const Duration(seconds: 15),
      );
    } on TimeoutException catch (e) {
      throw SshConnectionError('Command execution timed out', e);
    } catch (e) {
      throw SshConnectionError('Failed to execute command: $e', e);
    }
  }

  /// イベントハンドラを設定する
  void setEventHandlers(SshEvents events) {
    _events = events;
  }

  /// イベントハンドラを更新する
  void updateEventHandlers({
    void Function(Uint8List data)? onData,
    void Function()? onClose,
    void Function(Object error)? onError,
    void Function()? onShellEnd,
  }) {
    _events = _events.copyWith(
      onData: onData,
      onClose: onClose,
      onError: onError,
      onShellEnd: onShellEnd,
    );
  }

  /// リソースを解放する
  Future<void> dispose() async {
    _disposed = true;
    await disconnect();
    await _connectionStateController.close();
  }
}

/// SSHクライアントを作成する
SshClient createSshClient() {
  return SshClient();
}
