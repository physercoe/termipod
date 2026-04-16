import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart' show ScrollDirection;
import 'package:flutter/scheduler.dart';
import 'package:flutter/services.dart';
import 'package:termipod/l10n/app_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:wakelock_plus/wakelock_plus.dart';

import '../../providers/active_session_provider.dart';
import '../../providers/connection_provider.dart';
import '../../providers/settings_provider.dart';
import '../../providers/ssh_provider.dart';
import '../../providers/tmux_provider.dart';
import '../../services/keychain/secure_storage.dart';
import '../../services/network/network_monitor.dart';
import '../../services/ssh/input_queue.dart';
import '../../services/ssh/ssh_client.dart' show SshConnectOptions;
import '../../services/terminal/terminal_backend.dart';
import '../../services/terminal/tmux_backend.dart';
import '../../services/terminal/raw_pty_backend.dart';
import '../../services/tmux/pane_navigator.dart';
import '../../services/terminal/font_calculator.dart';
import '../../services/tmux/tmux_commands.dart';
import '../../services/tmux/tmux_parser.dart';
import '../../services/tmux/tmux_version.dart';
import '../../widgets/dialogs/resize_dialog.dart';
import '../../theme/design_colors.dart';
import '../../widgets/help_sheet.dart';
import '../../widgets/gesture_surface.dart';
import '../../providers/download_manager_provider.dart';
import '../../widgets/download_manager_sheet.dart';
import '../../widgets/custom_keyboard.dart';
import '../../widgets/floating_joystick.dart';
import '../../widgets/navigation_pad.dart';
import '../../widgets/onboarding_overlay.dart';
import '../../widgets/scroll_to_bottom_button.dart';
import '../../widgets/action_bar/action_bar.dart';
import '../../widgets/action_bar/compose_bar.dart';
import '../../widgets/action_bar/insert_menu.dart';
import '../../widgets/action_bar/snippet_picker_sheet.dart';
import '../../widgets/action_bar/profile_sheet.dart';
import '../../providers/action_bar_provider.dart';
import '../../models/action_bar_presets.dart';
import '../../widgets/image_transfer_confirm_dialog.dart';
import '../../widgets/tmux_tiles.dart';
import '../../providers/terminal_display_provider.dart';
import '../../providers/image_transfer_provider.dart';
import '../../providers/file_transfer_provider.dart';
import '../../widgets/file_transfer_confirm_dialog.dart';
import '../../widgets/remote_file_browser_dialog.dart';
import 'package:image_picker/image_picker.dart';
import 'package:share_plus/share_plus.dart';
import '../settings/settings_screen.dart';
import 'widgets/ansi_text_view.dart';

/// スクロールモードのソース
enum ScrollModeSource {
  /// 通常モード（スクロールモードではない）
  none,

  /// ユーザーがUIから手動で有効化
  manual,

  /// tmux copy-modeを自動検出
  tmux,
}

/// ポーリングで頻繁に更新されるターミナル表示データ
///
/// ValueNotifierで管理し、親ウィジェットのsetState()を回避する。
/// これによりBottomSheet表示中の親リビルドを防ぎ、
/// isDismissible: trueでも安定して動作する。
class _TerminalViewData {
  final String content;
  final int latency;
  final int paneWidth;
  final int paneHeight;
  final int scrollbackSize;
  // Cursor position for raw mode (tmux mode still reads from activePane).
  // null means "use the tmux activePane cursor instead".
  final int? rawCursorX;
  final int? rawCursorY;

  const _TerminalViewData({
    this.content = '',
    this.latency = 0,
    this.paneWidth = 80,
    this.paneHeight = 24,
    this.scrollbackSize = 0,
    this.rawCursorX,
    this.rawCursorY,
  });

  _TerminalViewData copyWith({
    String? content,
    int? latency,
    int? paneWidth,
    int? paneHeight,
    int? scrollbackSize,
    int? rawCursorX,
    int? rawCursorY,
  }) =>
      _TerminalViewData(
        content: content ?? this.content,
        latency: latency ?? this.latency,
        paneWidth: paneWidth ?? this.paneWidth,
        paneHeight: paneHeight ?? this.paneHeight,
        scrollbackSize: scrollbackSize ?? this.scrollbackSize,
        rawCursorX: rawCursorX ?? this.rawCursorX,
        rawCursorY: rawCursorY ?? this.rawCursorY,
      );
}

/// ターミナル画面（HTMLデザイン仕様準拠）
class TerminalScreen extends ConsumerStatefulWidget {
  final String connectionId;
  final String? sessionName;

  /// 復元用: 最後に開いていたウィンドウインデックス
  final int? lastWindowIndex;

  /// 復元用: 最後に開いていたペインID
  final String? lastPaneId;

  /// ディープリンク用: ウィンドウ名で指定（インデックスではなく名前で検索）
  final String? deepLinkWindowName;

  /// ディープリンク用: ペインインデックス
  final int? deepLinkPaneIndex;

  const TerminalScreen({
    super.key,
    required this.connectionId,
    this.sessionName,
    this.lastWindowIndex,
    this.lastPaneId,
    this.deepLinkWindowName,
    this.deepLinkPaneIndex,
  });

  @override
  ConsumerState<TerminalScreen> createState() => _TerminalScreenState();
}

class _TerminalScreenState extends ConsumerState<TerminalScreen>
    with WidgetsBindingObserver {
  final _secureStorage = SecureStorageService();
  final _scaffoldKey = GlobalKey<ScaffoldState>();
  final _ansiTextViewKey = GlobalKey<AnsiTextViewState>();
  final _scrollToBottomKey = GlobalKey<ScrollToBottomButtonState>();
  final _terminalScrollController = ScrollController();

  // 接続状態（ローカルで管理）
  bool _isConnecting = false;
  String? _connectionError;
  SshState _sshState = const SshState();

  // ポーリングで頻繁に更新されるターミナル表示データ（ValueNotifierで管理）
  // 親のsetState()を回避し、ValueListenableBuilderでサブツリーのみリビルドする
  final _viewNotifier = ValueNotifier<_TerminalViewData>(const _TerminalViewData());

  // ポーリング用タイマー
  Timer? _pollTimer;
  Timer? _treeRefreshTimer;
  Timer? _staleWatchdog;
  bool _isPolling = false;
  bool _isDisposed = false;
  // Timestamp of the last successful pane poll. Used by the
  // stale-connection watchdog + latency indicator to flag when the
  // SSH socket looks alive (`isConnected == true`) but no fresh data
  // has arrived in a while — a symptom of the "half-dead" connection
  // bug where text is queued locally but never reaches the server.
  DateTime _lastSuccessfulPoll = DateTime.now();
  // Mirrored into the view notifier so the latency indicator can
  // visually flag staleness without a separate Consumer rebuild.
  bool _isConnectionStale = false;

  // フレームスキップ用（高頻度更新の最適化）
  static const _minFrameInterval = Duration(milliseconds: 16); // ~60fps
  DateTime _lastFrameTime = DateTime.now();
  bool _pendingUpdate = false;
  String _pendingContent = '';
  int _pendingLatency = 0;
  int _pendingScrollbackSize = 0;

  // 適応型ポーリング用
  int _currentPollingInterval = 100;
  static const int _minPollingInterval = 50;
  static const int _maxPollingInterval = 2000;

  /// True when the active pane is on tmux's alternate screen OR is
  /// running a known fullscreen TUI command (vi, less, htop, …).
  /// Drives scrollback suppression in the duplicate poll path so
  /// the editor's screen isn't sandwiched below stale shell history.
  /// Mirrors the same flag in [TmuxBackend]; both paths must agree
  /// because either one can win the race to update `_viewNotifier`.
  bool _pollIsFullscreen = false;

  // 選択状態保持用（スクロールモード中の更新抑制）
  String _bufferedContent = '';
  int _bufferedLatency = 0;
  int _bufferedScrollbackSize = 0;
  bool _hasBufferedUpdate = false;

  // 初回スクロール完了フラグ
  bool _hasInitialScrolled = false;

  // ターミナルモード
  TerminalMode _terminalMode = TerminalMode.normal;

  // Gesture surface mode (mutually exclusive with scroll mode)
  bool _gestureModeActive = false;

  // スクロールモードのソース（none / manual / tmux）
  ScrollModeSource _scrollModeSource = ScrollModeSource.none;

  // ズームスケール
  double _zoomScale = 1.0;


  // 入力キュー（切断中の入力を保持）
  final _inputQueue = InputQueue();

  // バックグラウンド状態
  bool _isInBackground = false;

  // ウィンドウ作成中フラグ（連打防止）
  bool _isCreatingWindow = false;

  // ComposeBar key for inserting text
  final _composeBarKey = GlobalKey<ComposeBarState>();

  // リサイズ中フラグ（排他制御）
  bool _isResizing = false;

  // Scroll to bottom after next content update (post-resize)
  bool _pendingScrollToBottom = false;

  // --- Scrollback auto-extension state -------------------------------------
  //
  // When the user scrolls up to the top of the captured buffer, we ask the
  // backend to extend its scrollback window by [_scrollbackExtendStep] lines.
  // The next poll brings back a longer buffer, and we compensate the scroll
  // offset so the visible content stays anchored on the same line instead of
  // jumping upward.
  //
  // [_pendingScrollbackCompensation] flags that the upcoming content update
  // should perform that compensation. [_scrollbackExtendOldLineCount] and
  // [_scrollbackExtendOldPixels] snapshot the state at the moment extension
  // was triggered. [_lastScrollbackExtendTime] throttles rapid re-triggers so
  // a single swipe up only fires one extension.
  static const int _scrollbackExtendStep = 100;
  static const Duration _scrollbackExtendCooldown = Duration(milliseconds: 1500);
  bool _pendingScrollbackCompensation = false;
  int _scrollbackExtendOldLineCount = 0;
  double _scrollbackExtendOldPixels = 0;
  DateTime _lastScrollbackExtendTime =
      DateTime.fromMillisecondsSinceEpoch(0);

  // 自動リサイズのdebounceタイマー（画面サイズ変更時）
  Timer? _autoResizeDebounceTimer;

  // tmuxバージョン情報（リサイズ機能判定用）
  TmuxVersionInfo? _tmuxVersion;

  // Terminal backend (tmux or raw PTY)
  TerminalBackend? _backend;
  StreamSubscription<void>? _backendContentSub;

  // Riverpodリスナー
  ProviderSubscription<SshState>? _sshSubscription;
  ProviderSubscription<TmuxState>? _tmuxSubscription;
  ProviderSubscription<AppSettings>? _settingsSubscription;
  ProviderSubscription<AsyncValue<NetworkStatus>>? _networkSubscription;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);

    // スクロール時にスクロールボタンを表示
    _terminalScrollController.addListener(_onTerminalScroll);

    // 次フレームでリスナーを設定（ref使用のため）
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      _setupListeners();
      _connectAndSetup();
      _applyKeepScreenOn();
      maybeShowOnboarding(context);
    });
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    super.didChangeAppLifecycleState(state);
    switch (state) {
      case AppLifecycleState.paused:
      case AppLifecycleState.inactive:
      case AppLifecycleState.hidden:
        _pausePolling();
        break;
      case AppLifecycleState.resumed:
        _resumePolling();
        break;
      case AppLifecycleState.detached:
        break;
    }
  }

  @override
  void didChangeMetrics() {
    super.didChangeMetrics();

    final settings = ref.read(settingsProvider);

    // For non-auto-resize, scroll immediately on any metrics change.
    // For auto-resize, DON'T set _pendingScrollToBottom here — it gets
    // consumed prematurely by regular polling before the resize executes.
    // _executeAutoResize's finally block handles the deferred scroll.
    if (!settings.isAutoResize) {
      _ansiTextViewKey.currentState?.scrollToBottom();
      return;
    }

    // debounce: 画面回転・折りたたみの連続サイズ変更を抑制
    _autoResizeDebounceTimer?.cancel();
    _autoResizeDebounceTimer = Timer(const Duration(milliseconds: 500), () {
      if (!mounted || _isDisposed) return;
      final activePane = ref.read(tmuxProvider(widget.connectionId)).activePane;
      if (activePane != null) {
        _executeAutoResize(activePane);
      }
    });
  }

  /// バックグラウンド移行時にポーリングを停止
  void _pausePolling() {
    _isInBackground = true;
    _pollTimer?.cancel();
    _pollTimer = null;
    _treeRefreshTimer?.cancel();
    _treeRefreshTimer = null;
    _staleWatchdog?.cancel();
    _staleWatchdog = null;
    WakelockPlus.disable();
  }

  /// Watchdog that fires every 2s and flags the connection as stale
  /// when no successful poll has landed for >8s. If staleness exceeds
  /// 15s, auto-triggers a reconnect via [_probeConnectionOnResume]
  /// which ping-tests then force-reconnects on failure. Users can
  /// also tap the latency indicator to trigger reconnect manually.
  void _startStaleWatchdog() {
    _staleWatchdog?.cancel();
    _staleWatchdog = Timer.periodic(const Duration(seconds: 2), (_) {
      if (_isDisposed) return;
      final sshState = ref.read(sshProvider(widget.connectionId));
      // Don't flag stale while actively reconnecting — the UI already
      // shows that state through _buildReconnectingIndicator.
      if (sshState.isReconnecting) return;
      final age = DateTime.now().difference(_lastSuccessfulPoll);
      final stale = age.inSeconds >= 8;
      if (stale != _isConnectionStale) {
        if (mounted) setState(() => _isConnectionStale = stale);
      }
      // Auto-probe once staleness passes 15s — this catches the
      // "half-dead socket" case where isConnected stays true forever
      // but the remote side has silently stopped responding.
      if (age.inSeconds >= 15) {
        _probeConnectionOnResume();
      }
    });
  }

  /// フォアグラウンド復帰時にポーリングを再開
  void _resumePolling() {
    if (!_isInBackground || _isDisposed) return;
    _isInBackground = false;
    _startPolling();
    _startTreeRefresh();
    _startStaleWatchdog();
    _applyKeepScreenOn();
    // Android frequently kills the SSH TCP socket while the app is
    // backgrounded. `client.isConnected` stays optimistically true until
    // the next I/O attempt fails, which means the polling loop would show
    // stale content for several seconds before noticing. Probe the
    // connection immediately and force a reconnect if it's dead.
    _probeConnectionOnResume();
  }

  /// Probe the SSH connection after foreground resume. If the probe fails
  /// (or the client is already gone), force an immediate reconnect via
  /// [SshNotifier.reconnectNow] instead of waiting for the adaptive
  /// polling loop to discover the dead socket.
  Future<void> _probeConnectionOnResume() async {
    if (_isDisposed) return;

    final sshNotifier = ref.read(sshProvider(widget.connectionId).notifier);
    final sshState = ref.read(sshProvider(widget.connectionId));

    // Already reconnecting — let the existing machinery finish its work.
    if (sshState.isReconnecting) return;

    final client = sshNotifier.client;
    if (client == null || !client.isConnected) {
      sshNotifier.reconnectNow();
      return;
    }

    try {
      await client.execPersistent(
        'echo 1',
        timeout: const Duration(milliseconds: 1500),
      );
    } catch (_) {
      if (_isDisposed) return;
      final latest = ref.read(sshProvider(widget.connectionId));
      if (!latest.isReconnecting) {
        sshNotifier.reconnectNow();
      }
    }
  }

  /// Keep screen on設定を適用
  void _applyKeepScreenOn() {
    final settings = ref.read(settingsProvider);
    if (settings.keepScreenOn) {
      WakelockPlus.enable();
    } else {
      WakelockPlus.disable();
    }
  }

  /// Providerのリスナーを設定
  void _setupListeners() {
    // SSH状態の変化を監視
    _sshSubscription = ref.listenManual<SshState>(
      sshProvider(widget.connectionId),
      (previous, next) {
        if (!mounted || _isDisposed) return;
        setState(() {
          _sshState = next;
        });
      },
      fireImmediately: true,
    );

    // Tmux状態の変化を監視
    // 注意: 親のsetState()は不要。ブレッドクラムやペインインジケーターは
    // Consumer widgetでtmuxProviderを直接watchするため、
    // サブツリー内でのみリビルドされる。
    _tmuxSubscription = ref.listenManual<TmuxState>(
      tmuxProvider(widget.connectionId),
      (previous, next) {
        // Profile auto-detection from pane_current_command. Writes
        // directly into the per-panel profile map so each tmux pane
        // gets its own auto-detected profile, but only if the user
        // hasn't already picked one for that pane — otherwise a
        // transient shell command (`git`, `vim`, etc.) could clobber
        // the user's explicit choice.
        final activePane = next.activePane;
        if (activePane == null) return;
        final suggestedId = ActionBarPresets.detectProfileId(
          activePane.currentCommand,
        );
        if (suggestedId == null) return;
        final panelKey = '${widget.connectionId}|${activePane.id}';
        final abState = ref.read(actionBarProvider);
        if (abState.activeProfileByPanel.containsKey(panelKey)) return;
        ref
            .read(actionBarProvider.notifier)
            .setActiveProfileForPanel(panelKey, suggestedId);
      },
      fireImmediately: true,
    );

    // 設定の変化を監視（Keep screen on / directInput用）
    _settingsSubscription = ref.listenManual<AppSettings>(
      settingsProvider,
      (previous, next) {
        if (!mounted || _isDisposed) return;
        if (previous?.keepScreenOn != next.keepScreenOn) {
          _applyKeepScreenOn();
        }
        // (directInputEnabled moved to action_bar_provider)
      },
      fireImmediately: false,
    );

    // Action bar state is managed by actionBarProvider

    // ネットワーク状態の変化を監視（実際の接続状態変化時のみ更新）
    _networkSubscription = ref.listenManual<AsyncValue<NetworkStatus>>(
      networkStatusProvider,
      (previous, next) {
        if (!mounted || _isDisposed) return;
        final prevStatus = previous?.value;
        final nextStatus = next.value;
        if (prevStatus != nextStatus) {
          setState(() {});
        }
      },
      fireImmediately: true,
    );

    // 再接続成功時の処理を設定
    final sshNotifier = ref.read(sshProvider(widget.connectionId).notifier);
    sshNotifier.onReconnectSuccess = _onReconnectSuccess;
  }

  /// 再接続成功時の処理
  Future<void> _onReconnectSuccess() async {
    if (!mounted || _isDisposed) return;

    // Rebind the backend to the newly-created SshClient. The backend
    // captured the old client at construction and would otherwise keep
    // polling (tmux) or holding a dead shell stream (raw PTY) against
    // a disposed socket — that's why latency can tick while the
    // terminal content stays frozen across a reconnect.
    final sshNotifier = ref.read(sshProvider(widget.connectionId).notifier);
    final newClient = sshNotifier.client;
    if (newClient != null && _backend != null) {
      try {
        await _backend!.rebindSshClient(newClient);
      } catch (e) {
        debugPrint('[Terminal] Backend rebind failed: $e');
      }
    }

    if (!mounted || _isDisposed) return;

    // ポーリングフラグをリセット
    _isPolling = false;
    _lastSuccessfulPoll = DateTime.now();
    if (_isConnectionStale && mounted) {
      setState(() => _isConnectionStale = false);
    }

    // ポーリングを再開
    _startPolling();

    // tmux モードではセッションツリーを即座に再取得して、
    // リコネクト後に欠落しているウィンドウ/ペイン情報を補う。
    // Raw PTY では不要（ツリーという概念がない）。
    if (_backend?.supportsNavigation ?? false) {
      await _refreshSessionTree();
      _backend?.boostRefresh();
    }

    if (!mounted || _isDisposed) return;

    // セッションツリー周期更新を再開
    _startTreeRefresh();

    // 接続状態監視ウォッチドッグを再開
    _startStaleWatchdog();

    // キューされた入力を送信
    await _flushInputQueue();

    // UIを更新
    if (mounted) setState(() {});
  }

  /// キューされた入力を送信
  Future<void> _flushInputQueue() async {
    if (_inputQueue.isEmpty) return;

    final queuedInput = _inputQueue.flush();
    if (queuedInput.isNotEmpty) {
      await _sendKeyData(queuedInput);
    }
  }

  /// SSH接続してtmuxセッションをセットアップ
  Future<void> _connectAndSetup() async {
    if (!mounted) {
      return;
    }
    setState(() {
      _isConnecting = true;
      _connectionError = null;
    });

    try {
      // 1. 接続情報を取得
      final connection = ref.read(connectionsProvider.notifier).getById(widget.connectionId);
      if (connection == null) {
        throw Exception('Connection not found');
      }

      // 2. 認証情報を取得
      final options = await _getAuthOptions(connection);
      if (!mounted || _isDisposed) {
        return;
      }

      // 3. SSH接続（シェルは起動しない - execのみ使用）
      final sshNotifier = ref.read(sshProvider(widget.connectionId).notifier);
      await sshNotifier.connectWithoutShell(connection, options);
      if (!mounted || _isDisposed) {
        return;
      }

      if (connection.isRawMode) {
        // --- Raw PTY mode: direct shell, no tmux ---
        await _setupRawPtyBackend(sshNotifier);
      } else {
        // --- Tmux mode: existing flow ---
        await _setupTmuxBackend(connection, sshNotifier);
      }

      if (!mounted) return;
      setState(() {
        _isConnecting = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _isConnecting = false;
        _connectionError = e.toString();
      });
      _showErrorSnackBar(e.toString());
    }
  }

  /// Raw PTY backend setup — start shell, subscribe to content stream.
  Future<void> _setupRawPtyBackend(SshNotifier sshNotifier) async {
    final sshClient = sshNotifier.client;
    if (sshClient == null) throw Exception('SSH client not available');

    final displayState = ref.read(terminalDisplayProvider);
    final cols = displayState.paneWidth > 0 ? displayState.paneWidth : 80;
    final rows = displayState.paneHeight > 0 ? displayState.paneHeight : 24;

    _backend = RawPtyBackend(sshClient: sshClient, cols: cols, rows: rows);
    await _backend!.initialize(cols: cols, rows: rows);

    _viewNotifier.value = _viewNotifier.value.copyWith(
      paneWidth: cols,
      paneHeight: rows,
    );

    _backendContentSub = _backend!.contentUpdates.listen((_) {
      if (!mounted || _isDisposed) return;
      _onBackendContentUpdate();
    });
  }

  /// Tmux backend setup — detect tmux, create/attach session, start polling.
  Future<void> _setupTmuxBackend(Connection connection, SshNotifier sshNotifier) async {
    final sshClient = sshNotifier.client;
    if (sshClient == null) throw Exception('SSH client not available');

    // tmux version check
    try {
      final versionOutput = await sshClient.exec(TmuxCommands.version());
      _tmuxVersion = TmuxVersionInfo.parse(versionOutput);
    } catch (_) {
      _tmuxVersion = null;
    }

    // Session tree
    await _refreshSessionTree();
    if (!mounted || _isDisposed) return;

    final tmuxState = ref.read(tmuxProvider(widget.connectionId));
    final sessions = tmuxState.sessions;

    // Select or create session
    String sessionName;
    if (widget.sessionName != null) {
      final existingIndex = sessions.indexWhere(
        (s) => s.name == widget.sessionName,
      );
      if (existingIndex >= 0) {
        sessionName = sessions[existingIndex].name;
      } else {
        await sshClient.exec(TmuxCommands.newSession(
          name: widget.sessionName!,
          detached: true,
        ));
        if (!mounted || _isDisposed) return;
        await _refreshSessionTree();
        if (!mounted || _isDisposed) return;
        sessionName = widget.sessionName!;
      }
    } else if (sessions.isNotEmpty) {
      sessionName = sessions.first.name;
    } else {
      sessionName = 'termipod-${DateTime.now().millisecondsSinceEpoch}';
      await sshClient.exec(TmuxCommands.newSession(name: sessionName, detached: true));
      if (!mounted || _isDisposed) return;
      await _refreshSessionTree();
      if (!mounted || _isDisposed) return;
    }

    // Set active session/window/pane
    ref.read(tmuxProvider(widget.connectionId).notifier).setActiveSession(sessionName);

    // Deep link or saved position restore
    if (widget.deepLinkWindowName != null) {
      final tmuxState = ref.read(tmuxProvider(widget.connectionId));
      final session = tmuxState.activeSession;
      if (session != null) {
        final targetName = widget.deepLinkWindowName!;
        TmuxWindow? window;
        for (final w in session.windows) {
          if (w.name == targetName || w.name.endsWith(':$targetName')) {
            window = w;
            break;
          }
        }
        if (window != null) {
          ref.read(tmuxProvider(widget.connectionId).notifier).setActiveWindow(window.index);
          if (widget.deepLinkPaneIndex != null && widget.deepLinkPaneIndex! < window.panes.length) {
            final pane = window.panes[widget.deepLinkPaneIndex!];
            ref.read(tmuxProvider(widget.connectionId).notifier).setActivePane(pane.id);
          }
        }
      }
    } else if (widget.lastWindowIndex != null) {
      final tmuxState = ref.read(tmuxProvider(widget.connectionId));
      final session = tmuxState.activeSession;
      if (session != null) {
        final window = session.windows.firstWhere(
          (w) => w.index == widget.lastWindowIndex,
          orElse: () => session.windows.first,
        );
        ref.read(tmuxProvider(widget.connectionId).notifier).setActiveWindow(window.index);
        if (widget.lastPaneId != null) {
          final pane = window.panes.firstWhere(
            (p) => p.id == widget.lastPaneId,
            orElse: () => window.panes.first,
          );
          ref.read(tmuxProvider(widget.connectionId).notifier).setActivePane(pane.id);
        }
      }
    }

    // TerminalDisplayProvider pane info
    final activePane = ref.read(tmuxProvider(widget.connectionId)).activePane;
    if (activePane != null) {
      debugPrint('[Terminal] Pane size: ${activePane.width}x${activePane.height}');
      ref.read(terminalDisplayProvider.notifier).updatePane(activePane);
      _viewNotifier.value = _viewNotifier.value.copyWith(
        paneWidth: activePane.width,
        paneHeight: activePane.height,
      );
    }

    // Create TmuxBackend
    final settings = ref.read(settingsProvider);
    final tmuxNotifier = ref.read(tmuxProvider(widget.connectionId).notifier);
    _backend = TmuxBackend(
      sshClient: sshClient,
      getCurrentTarget: () => tmuxNotifier.currentTarget,
      scrollbackLines: settings.scrollbackLines,
      onCursorUpdate: (cursorX, cursorY, w, h, historySize) {
        if (!mounted || _isDisposed) return;
        // Update pane size if changed
        if (w != null && h != null &&
            (w != _viewNotifier.value.paneWidth || h != _viewNotifier.value.paneHeight)) {
          _viewNotifier.value = _viewNotifier.value.copyWith(paneWidth: w, paneHeight: h);
          final currentActivePane = ref.read(tmuxProvider(widget.connectionId)).activePane;
          if (currentActivePane != null) {
            ref.read(terminalDisplayProvider.notifier).updatePane(
              currentActivePane.copyWith(width: w, height: h),
            );
          }
        }
        // Update cursor position
        final activePaneId = ref.read(tmuxProvider(widget.connectionId)).activePaneId;
        if (activePaneId != null) {
          ref.read(tmuxProvider(widget.connectionId).notifier).updateCursorPosition(
            activePaneId, cursorX, cursorY,
          );
        }
      },
      onCopyModeChange: (isInCopyMode) {
        if (!mounted || _isDisposed) return;
        if (isInCopyMode && _scrollModeSource == ScrollModeSource.none) {
          setState(() {
            _terminalMode = TerminalMode.scroll;
            _scrollModeSource = ScrollModeSource.tmux;
            _gestureModeActive = false;
          });
        } else if (!isInCopyMode && _scrollModeSource == ScrollModeSource.tmux) {
          setState(() {
            _terminalMode = TerminalMode.normal;
            _scrollModeSource = ScrollModeSource.none;
          });
          _applyBufferedUpdate();
        }
      },
      getRecommendedInterval: () {
        final ansiTextViewState = _ansiTextViewKey.currentState;
        return ansiTextViewState?.recommendedPollingInterval ?? 100;
      },
    );

    await _backend!.initialize(
      cols: activePane?.width ?? 80,
      rows: activePane?.height ?? 24,
    );

    _backendContentSub = _backend!.contentUpdates.listen((_) {
      if (!mounted || _isDisposed) return;
      _onBackendContentUpdate();
    });

    // Tree refresh timer
    _startTreeRefresh();
    // Stale-connection watchdog — starts alongside polling so the
    // latency indicator can flag "no fresh data" even when the SSH
    // socket still appears connected.
    _lastSuccessfulPoll = DateTime.now();
    _startStaleWatchdog();
  }

  /// Handle content update from backend (both tmux and raw).
  void _onBackendContentUpdate() {
    final backend = _backend;
    if (backend == null) return;

    // Any backend-driven update proves fresh data reached us, so
    // refresh the stale watchdog timestamp (covers raw PTY mode where
    // _pollPaneContent isn't the primary heartbeat).
    _lastSuccessfulPoll = DateTime.now();
    if (_isConnectionStale && mounted && !_isDisposed) {
      setState(() => _isConnectionStale = false);
    }

    final content = backend.currentContent;
    final scrollback = backend.scrollbackSize;
    final latency = backend is TmuxBackend ? backend.latency : 0;

    // For raw backend: push cursor directly into _viewNotifier so AnsiTextView
    // can draw it. Tmux backend routes cursor via tmuxProvider.activePane.
    if (!backend.supportsNavigation) {
      final cursor = backend.cursorPosition;
      final dims = backend.dimensions;
      if (mounted && !_isDisposed) {
        _viewNotifier.value = _viewNotifier.value.copyWith(
          rawCursorX: cursor.x,
          rawCursorY: cursor.y,
          paneWidth: dims.width,
          paneHeight: dims.height,
        );
      }
    }

    // For tmux backend: scroll mode buffering
    if (backend.supportsNavigation &&
        _terminalMode == TerminalMode.scroll &&
        _scrollModeSource == ScrollModeSource.manual) {
      _bufferedContent = content;
      _bufferedLatency = latency;
      _bufferedScrollbackSize = scrollback;
      _hasBufferedUpdate = true;
      if (mounted && !_isDisposed) {
        _viewNotifier.value = _viewNotifier.value.copyWith(latency: latency);
      }
    } else {
      _scheduleUpdate(content, latency, scrollback);
    }
  }

  /// セッションツリー全体を取得して更新
  Future<void> _refreshSessionTree() async {
    if (_isDisposed) return;
    final sshClient = ref.read(sshProvider(widget.connectionId).notifier).client;
    if (sshClient == null || !sshClient.isConnected) return;

    try {
      final cmd = TmuxCommands.listAllPanes();
      final output = await sshClient.exec(cmd);
      if (!mounted || _isDisposed) return;
      ref.read(tmuxProvider(widget.connectionId).notifier).parseAndUpdateFullTree(output);
    } catch (_) {
      // Tree update errors are silently ignored (retried on next poll)
    }
  }

  /// 10秒ごとにセッションツリーを更新
  void _startTreeRefresh() {
    _treeRefreshTimer?.cancel();
    _treeRefreshTimer = Timer.periodic(
      const Duration(seconds: 10),
      (_) {
        // ポーリング中はSSH競合を回避するためスキップ
        if (!_isPolling) {
          _refreshSessionTree();
        }
      },
    );
  }

  /// 適応型ポーリングでcapture-paneを実行してターミナル内容を更新
  ///
  /// コンテンツの変化頻度に応じてポーリング間隔を動的に調整:
  /// - 高頻度更新時（htop等）: 50ms
  /// - 通常時: 100ms
  /// - アイドル時: 500ms
  void _startPolling() {
    _pollTimer?.cancel();
    _scheduleNextPoll();
  }

  /// 次のポーリングをスケジュール
  void _scheduleNextPoll() {
    if (_isDisposed) return;
    _pollTimer?.cancel();
    _pollTimer = Timer(
      Duration(milliseconds: _currentPollingInterval),
      () async {
        await _pollPaneContent();
        _scheduleNextPoll();
      },
    );
  }

  /// キー入力後にポーリングを即座にブースト（アイドル時の応答性改善）
  void _boostPolling() {
    _currentPollingInterval = _minPollingInterval;
    _pollTimer?.cancel();
    _scheduleNextPoll();
  }

  /// ポーリング間隔を更新
  void _updatePollingInterval() {
    final ansiTextViewState = _ansiTextViewKey.currentState;
    if (ansiTextViewState != null) {
      final recommended = ansiTextViewState.recommendedPollingInterval;
      // tmux copy-mode 検出中はポーリング間隔の上限を500msに制限
      // copy-mode終了の検出遅延を最大0.5秒に改善
      final maxInterval = _scrollModeSource == ScrollModeSource.tmux ? 500 : _maxPollingInterval;
      _currentPollingInterval = recommended.clamp(
        _minPollingInterval,
        maxInterval,
      );
    }
  }

  /// ペイン内容をポーリング取得
  Future<void> _pollPaneContent() async {
    if (_isPolling || _isDisposed) return;
    _isPolling = true;

    try {
      final sshNotifier = ref.read(sshProvider(widget.connectionId).notifier);
      final sshClient = sshNotifier.client;

      // 接続が切れている場合は自動再接続を試みる
      if (sshClient == null || !sshClient.isConnected) {
        // すでに再接続中でなければ再接続を開始
        final currentState = ref.read(sshProvider(widget.connectionId));
        if (!currentState.isReconnecting) {
          _attemptReconnect();
        }
        _isPolling = false;
        return;
      }

      // tmux_providerからターゲットを取得
      final target = ref.read(tmuxProvider(widget.connectionId).notifier).currentTarget;
      if (target == null) {
        _isPolling = false;
        return;
      }

      final startTime = DateTime.now();

      // 3つのコマンドを1つに統合して実行（持続的シェルは同時に1コマンドのみ）
      // capture-pane + カーソル位置情報 + ペインモード を1回で取得
      //
      // Honour the tmux backend's (possibly extended) scrollback window so
      // this parallel polling path stays in lock-step with the backend
      // after [TmuxBackend.extendScrollback] / [resetScrollback] calls. If
      // no tmux backend is active (raw mode), fall back to the setting.
      //
      // When the pane is running a fullscreen TUI (vi, less, htop, …) or
      // is on tmux's alternate screen, request 0 scrollback so the editor
      // screen isn't preceded by stale shell history. `_pollIsFullscreen`
      // is updated below from `pane_current_command`; we use the value
      // from the *previous* poll here, then re-check after capture and
      // strip the scrollback portion if a one-poll detection lag let it
      // slip through. This mirrors the same logic in [TmuxBackend].
      final backend = _backend;
      final settingsScrollback = backend is TmuxBackend
          ? backend.scrollbackLines
          : ref.read(settingsProvider).scrollbackLines;
      final effectiveScrollback = _pollIsFullscreen ? 0 : settingsScrollback;

      // Use a unique \x01META\x01 delimiter between capture-pane output
      // and the metadata fields so parsing doesn't depend on counting
      // lines from the end. The previous `removeLast`-based split was
      // fragile: any trailing newline variation in the persistent shell
      // could pop a line of cursor metadata into the visible terminal
      // (e.g. "46,53,198,54,1988,0,vi" appearing as a line of text).
      final captureCmd = effectiveScrollback > 0
          ? TmuxCommands.capturePane(
              target,
              escapeSequences: true,
              startLine: -effectiveScrollback,
            )
          : TmuxCommands.capturePane(target, escapeSequences: true);
      final combinedCommand =
          '$captureCmd; '
          "printf '\\x01META\\x01\\n'; "
          '${TmuxCommands.getCursorPosition(target)}; '
          '${TmuxCommands.getPaneMode(target)}';

      final combinedOutput = await sshClient.execPersistent(
        combinedCommand,
        timeout: const Duration(seconds: 2),
      );

      // Split on the delimiter — everything before is capture-pane
      // content, everything after is cursor + pane_mode lines.
      String contentRaw;
      String cursorOutput = '';
      String paneModeOutput = '';
      const metaDelim = '\x01META\x01';
      final delimIndex = combinedOutput.lastIndexOf(metaDelim);
      if (delimIndex != -1) {
        contentRaw = combinedOutput.substring(0, delimIndex);
        final metaPart = combinedOutput.substring(delimIndex + metaDelim.length);
        final metaLines = metaPart.split('\n').where((l) => l.isNotEmpty).toList();
        cursorOutput = metaLines.isNotEmpty ? metaLines[0] : '';
        paneModeOutput = metaLines.length >= 2 ? metaLines[1] : '';
      } else {
        // Delimiter not found — fall back to treating the whole blob as
        // content. Better to show too much than to leak cursor metadata.
        contentRaw = combinedOutput;
      }
      // capture-paneの出力末尾にある改行を削除
      var processedOutput = contentRaw.endsWith('\n')
          ? contentRaw.substring(0, contentRaw.length - 1)
          : contentRaw;

      final endTime = DateTime.now();

      if (!mounted || _isDisposed) return;

      // カーソル位置・ペインサイズ・スクリーンモード・現コマンドを更新
      // Format: cursor_x,cursor_y,pane_width,pane_height,history_size,
      //         alternate_on,pane_current_command
      int? historySize;
      int? paneHeightForStrip;
      if (cursorOutput.isNotEmpty) {
        final parts = cursorOutput.trim().split(',');
        if (parts.length >= 4) {
          final x = int.tryParse(parts[0]);
          final y = int.tryParse(parts[1]);
          final w = int.tryParse(parts[2]);
          final h = int.tryParse(parts[3]);
          historySize = parts.length >= 5 ? int.tryParse(parts[4]) : null;
          final isAlternate = parts.length >= 6 && parts[5].trim() == '1';
          final currentCommand = parts.length >= 7 ? parts[6].trim() : null;
          // Update fullscreen flag for the NEXT poll's effectiveScrollback
          // and for the strip-on-lag check below.
          _pollIsFullscreen =
              isAlternate || TmuxBackend.isFullscreenCommandName(currentCommand);
          paneHeightForStrip = h;

          // ペインサイズの更新検知
          if (w != null && h != null && (w != _viewNotifier.value.paneWidth || h != _viewNotifier.value.paneHeight)) {
            _viewNotifier.value = _viewNotifier.value.copyWith(paneWidth: w, paneHeight: h);
            // フォントサイズ再計算のために通知
            final currentActivePane = ref.read(tmuxProvider(widget.connectionId)).activePane;
            if (currentActivePane != null) {
              ref.read(terminalDisplayProvider.notifier).updatePane(
                    currentActivePane.copyWith(width: w, height: h),
                  );
            }
          }

          final activePaneId = ref.read(tmuxProvider(widget.connectionId)).activePaneId;
          if (activePaneId != null && x != null && y != null) {
            ref.read(tmuxProvider(widget.connectionId).notifier).updateCursorPosition(
              activePaneId, x, y,
            );
          }
        }
      }

      // One-poll detection lag: the previous poll didn't yet know we
      // were in a fullscreen TUI, so this capture asked tmux for
      // scrollback and got `<shell history>\n<vi screen>`. Strip the
      // history portion down to the visible pane so the user doesn't
      // see stale content above the editor and have to scroll.
      if (_pollIsFullscreen &&
          effectiveScrollback > 0 &&
          paneHeightForStrip != null &&
          paneHeightForStrip > 0) {
        final stripLines = processedOutput.split('\n');
        if (stripLines.length > paneHeightForStrip) {
          processedOutput = stripLines
              .sublist(stripLines.length - paneHeightForStrip)
              .join('\n');
        }
      }

      // レイテンシを更新
      final latency = endTime.difference(startTime).inMilliseconds;

      // Mark this poll as successful for the stale watchdog. A
      // response — even an empty one — proves the SSH pipe is still
      // alive end-to-end, which `isConnected` alone can't guarantee.
      _lastSuccessfulPoll = DateTime.now();
      if (_isConnectionStale && mounted && !_isDisposed) {
        setState(() => _isConnectionStale = false);
      }

      // 差分があれば更新（スロットリング適用）
      final scrollback = historySize ?? _viewNotifier.value.scrollbackSize;
      final currentView = _viewNotifier.value;
      if (processedOutput != currentView.content || latency != currentView.latency) {
        // 手動スクロールモード中のみ更新をバッファリングして選択状態を保持
        // tmux copy-mode中はcapture-paneがスクロール位置の内容を返すためリアルタイム表示
        if (_terminalMode == TerminalMode.scroll && _scrollModeSource == ScrollModeSource.manual) {
          _bufferedContent = processedOutput;
          _bufferedLatency = latency;
          _bufferedScrollbackSize = scrollback;
          _hasBufferedUpdate = true;
          // レイテンシのみ更新（選択に影響しない）
          if (mounted && !_isDisposed) {
            _viewNotifier.value = currentView.copyWith(latency: latency);
          }
        } else {
          _scheduleUpdate(processedOutput, latency, scrollback);
        }
      }

      // tmux copy-mode 検出による自動モード切替
      if (mounted && !_isDisposed) {
        final paneMode = paneModeOutput.trim();
        final isTmuxCopyMode = paneMode.isNotEmpty;

        if (isTmuxCopyMode && _scrollModeSource == ScrollModeSource.none) {
          // tmux copy-mode に入った → スクロールモードに自動切替
          setState(() {
            _terminalMode = TerminalMode.scroll;
            _scrollModeSource = ScrollModeSource.tmux;
            _gestureModeActive = false; // mutually exclusive
          });
        } else if (!isTmuxCopyMode && _scrollModeSource == ScrollModeSource.tmux) {
          // tmux copy-mode が終了した → 自動で通常モードに復帰
          setState(() {
            _terminalMode = TerminalMode.normal;
            _scrollModeSource = ScrollModeSource.none;
          });
          _applyBufferedUpdate();
        }
      }

      // 適応型ポーリング間隔を更新
      _updatePollingInterval();
    } catch (e) {
      // 通信エラーの場合は自動再接続を試みる
      if (!_isDisposed) {
        final currentState = ref.read(sshProvider(widget.connectionId));
        if (!currentState.isReconnecting) {
          _attemptReconnect();
        }
      }
    } finally {
      _isPolling = false;
    }
  }

  /// バッファリングされた更新を適用（スクロールモード終了時に呼び出し）
  void _applyBufferedUpdate() {
    if (_hasBufferedUpdate) {
      _scheduleUpdate(_bufferedContent, _bufferedLatency, _bufferedScrollbackSize);
      _hasBufferedUpdate = false;
      _bufferedContent = '';
      _bufferedLatency = 0;
      _bufferedScrollbackSize = 0;
    }
  }

  /// フレームスキップを考慮して更新をスケジュール
  ///
  /// 高頻度更新時（htop等）に毎フレーム更新しないようスロットリングを行う。
  /// 16ms（約60fps）以内の連続更新は次フレームに延期される。
  void _scheduleUpdate(String content, int latency, int scrollbackSize) {
    _pendingContent = content;
    _pendingLatency = latency;
    _pendingScrollbackSize = scrollbackSize;

    // すでに更新がスケジュール済みなら何もしない
    if (_pendingUpdate) return;

    final now = DateTime.now();
    final elapsed = now.difference(_lastFrameTime);

    if (elapsed >= _minFrameInterval) {
      // 十分な時間が経過しているので即時更新
      _applyUpdate();
    } else {
      // フレームスキップ: 次のフレームで更新
      _pendingUpdate = true;
      SchedulerBinding.instance.addPostFrameCallback((_) {
        if (!mounted || _isDisposed) return;
        _pendingUpdate = false;
        _applyUpdate();
      });
    }
  }

  /// 保留中の更新を適用
  void _applyUpdate() {
    if (!mounted || _isDisposed) return;
    _lastFrameTime = DateTime.now();
    // ValueNotifier更新（親のsetState()を回避し、ValueListenableBuilderのみリビルド）
    _viewNotifier.value = _viewNotifier.value.copyWith(
      content: _pendingContent,
      latency: _pendingLatency,
      scrollbackSize: _pendingScrollbackSize,
    );

    // 初回コンテンツ受信時に一番下へスクロール
    if (!_hasInitialScrolled && _pendingContent.isNotEmpty) {
      _hasInitialScrolled = true;
      _scrollToCaret();
    }

    // Scroll to cursor position after resize content arrives
    if (_pendingScrollToBottom && _pendingContent.isNotEmpty) {
      _pendingScrollToBottom = false;
      _ansiTextViewKey.currentState?.scrollToBottom();
      Future.delayed(const Duration(milliseconds: 200), () {
        if (mounted && !_isDisposed) _scrollToBottomKey.currentState?.hide();
      });
    }

    // Anchor the viewport after a scrollback extension so the visible
    // content stays on the same line. New history lines were prepended
    // to the buffer; without compensation the user's reading position
    // would suddenly be in the middle of the newly-loaded older text.
    if (_pendingScrollbackCompensation && _pendingContent.isNotEmpty) {
      _pendingScrollbackCompensation = false;
      final newLineCount = '\n'.allMatches(_pendingContent).length + 1;
      final addedLines = newLineCount - _scrollbackExtendOldLineCount;
      if (addedLines > 0) {
        final lineHeight =
            _ansiTextViewKey.currentState?.lineHeight ?? 20.0;
        final targetOffset =
            _scrollbackExtendOldPixels + addedLines * lineHeight;
        SchedulerBinding.instance.addPostFrameCallback((_) {
          if (!mounted || _isDisposed) return;
          if (!_terminalScrollController.hasClients) return;
          final maxExtent =
              _terminalScrollController.position.maxScrollExtent;
          _terminalScrollController.jumpTo(
            targetOffset.clamp(0.0, maxExtent),
          );
        });
      }
    }
  }

  /// 自動再接続を試みる
  Future<void> _attemptReconnect() async {
    if (_isDisposed) return;

    final sshNotifier = ref.read(sshProvider(widget.connectionId).notifier);
    final success = await sshNotifier.reconnect();

    if (!mounted || _isDisposed) return;

    if (!success) {
      // 再接続失敗時は再試行（最大回数に達するまで）
      final currentState = ref.read(sshProvider(widget.connectionId));
      if (currentState.reconnectAttempt < 5) {
        // 次のポーリングで再試行される
      }
    }
  }

  /// 認証オプションを取得
  Future<SshConnectOptions> _getAuthOptions(Connection connection) async {
    String? password;
    String? privateKey;
    String? passphrase;

    if (connection.authMethod == 'key' && connection.keyId != null) {
      privateKey = await _secureStorage.getPrivateKey(connection.keyId!);
      passphrase = await _secureStorage.getPassphrase(connection.keyId!);
    } else {
      password = await _secureStorage.getPassword(connection.id);
    }

    // Jump host auth
    String? jumpPassword;
    String? jumpPrivateKey;
    String? jumpPassphrase;
    if (connection.jumpHost != null) {
      if (connection.jumpAuthMethod == 'key' && connection.jumpKeyId != null) {
        jumpPrivateKey = await _secureStorage.getPrivateKey(connection.jumpKeyId!);
        jumpPassphrase = await _secureStorage.getPassphrase(connection.jumpKeyId!);
      } else {
        // Reuse main password for jump host password auth
        jumpPassword = password ?? await _secureStorage.getPassword(connection.id);
      }
    }

    return SshConnectOptions(
      password: password,
      privateKey: privateKey,
      passphrase: passphrase,
      jumpHost: connection.jumpHost,
      jumpPort: connection.jumpPort,
      jumpUsername: connection.jumpUsername,
      jumpPassword: jumpPassword,
      jumpPrivateKey: jumpPrivateKey,
      jumpPassphrase: jumpPassphrase,
      proxyHost: connection.proxyHost,
      proxyPort: connection.proxyPort,
      proxyUsername: connection.proxyUsername,
      proxyPassword: connection.proxyPassword,
    );
  }

  /// エラーSnackBar表示
  void _showErrorSnackBar(String message) {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(message),
        backgroundColor: Colors.red,
        action: SnackBarAction(
          label: AppLocalizations.of(context)!.buttonRetry,
          textColor: Colors.white,
          onPressed: _connectAndSetup,
        ),
      ),
    );
  }

  /// スクロール時にスクロールボタンを表示
  void _onTerminalScroll() {
    _scrollToBottomKey.currentState?.show();
    _maybeExtendScrollback();
  }

  /// When the user scrolls within ~2 line-heights of the top of the
  /// captured buffer, ask the backend for more scrollback so they can
  /// keep scrolling. Debounced so a single swipe fires at most one
  /// extension, and gated on tmux (raw PTY's buffer is fixed).
  void _maybeExtendScrollback() {
    if (_isDisposed) return;
    final backend = _backend;
    if (backend is! TmuxBackend) return;
    if (!_terminalScrollController.hasClients) return;

    final position = _terminalScrollController.position;
    // Only react to user-driven scroll, not programmatic jumps.
    if (position.userScrollDirection == ScrollDirection.idle) return;

    final lineHeight = _ansiTextViewKey.currentState?.lineHeight ?? 20.0;
    final topThreshold = position.minScrollExtent + lineHeight * 2;
    if (position.pixels > topThreshold) return;

    final now = DateTime.now();
    if (now.difference(_lastScrollbackExtendTime) <
        _scrollbackExtendCooldown) {
      return;
    }
    _lastScrollbackExtendTime = now;

    // Snapshot state so the next content update can anchor the view.
    _scrollbackExtendOldLineCount =
        '\n'.allMatches(_viewNotifier.value.content).length + 1;
    _scrollbackExtendOldPixels = position.pixels;
    _pendingScrollbackCompensation = true;

    backend.extendScrollback(_scrollbackExtendStep).then((added) {
      if (added <= 0) {
        // Hit the cap or backend rejected — don't leave the flag set.
        _pendingScrollbackCompensation = false;
      }
    });
  }

  @override
  void deactivate() {
    // Clear callbacks to prevent firing after widget is deactivated.
    // SSH disconnect and tmux state cleanup are handled by auto-dispose
    // when subscriptions are closed in dispose().
    final sshNotifier = ref.read(sshProvider(widget.connectionId).notifier);
    sshNotifier.onReconnectSuccess = null;
    sshNotifier.onDisconnectDetected = null;

    super.deactivate();
  }

  @override
  void dispose() {
    // まず_isDisposedをセットして非同期処理を停止
    _isDisposed = true;
    WidgetsBinding.instance.removeObserver(this);
    // WakeLockを無効化
    WakelockPlus.disable();
    // Riverpodサブスクリプションをキャンセル
    _sshSubscription?.close();
    _sshSubscription = null;
    _tmuxSubscription?.close();
    _tmuxSubscription = null;
    _settingsSubscription?.close();
    _settingsSubscription = null;
    _networkSubscription?.close();
    _networkSubscription = null;
    _imageTransferSub?.close();
    _imageTransferSub = null;
    _fileTransferSub?.close();
    _fileTransferSub = null;
    // Backend cleanup
    _backendContentSub?.cancel();
    _backendContentSub = null;
    _backend?.dispose();
    _backend = null;
    // タイマーを停止
    _pollTimer?.cancel();
    _pollTimer = null;
    _treeRefreshTimer?.cancel();
    _treeRefreshTimer = null;
    _staleWatchdog?.cancel();
    _staleWatchdog = null;
    _autoResizeDebounceTimer?.cancel();
    _autoResizeDebounceTimer = null;
    // (compose resources are managed by ComposeBar widget)
    // ValueNotifierを破棄
    _viewNotifier.dispose();
    // スクロールコントローラーのリスナーを削除して破棄
    _terminalScrollController.removeListener(_onTerminalScroll);
    _terminalScrollController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    // ローカル状態を使用（ref.watchは使わない）
    // 注意: tmuxProviderは各Consumer内でref.watchして取得する
    // これにより親build()がポーリングで呼ばれず、BottomSheetが安定する
    final sshState = _sshState;
    final isDark = Theme.of(context).brightness == Brightness.dark;

    return Scaffold(
      key: _scaffoldKey,
      backgroundColor: Theme.of(context).scaffoldBackgroundColor,
      body: Stack(
        children: [
          Column(
            children: [
              // Breadcrumb header: tmux navigation or simple connection name
              if (_backend?.supportsNavigation ?? true)
                Consumer(
                  builder: (context, ref, _) {
                    final tmuxState = ref.watch(tmuxProvider(widget.connectionId));
                    return _buildBreadcrumbHeader(tmuxState);
                  },
                )
              else
                _buildRawModeHeader(),
              Expanded(
                child: AnimatedContainer(
                  duration: const Duration(milliseconds: 200),
                  decoration: BoxDecoration(
                    border: Border.all(
                      color: _gestureModeActive
                          ? DesignColors.primary
                          : _terminalMode == TerminalMode.scroll
                              ? DesignColors.warning
                              : Colors.transparent,
                      width: 3,
                    ),
                  ),
                  child: Stack(
                    children: [
                      // ターミナル表示: ValueListenableBuilder + Consumer
                      // ポーリング更新はValueNotifier経由でこのサブツリーのみリビルド
                      RepaintBoundary(
                        child: ValueListenableBuilder<_TerminalViewData>(
                          valueListenable: _viewNotifier,
                          builder: (context, viewData, _) {
                            return Consumer(
                              builder: (context, ref, _) {
                                // Raw PTY backend writes cursor directly into viewData;
                                // tmux backend routes cursor through tmuxProvider.activePane.
                                final ({int x, int y}) cursor;
                                if (viewData.rawCursorX != null && viewData.rawCursorY != null) {
                                  cursor = (x: viewData.rawCursorX!, y: viewData.rawCursorY!);
                                } else {
                                  cursor = ref.watch(tmuxProvider(widget.connectionId).select((s) => (
                                    x: s.activePane?.cursorX ?? 0,
                                    y: s.activePane?.cursorY ?? 0,
                                  )));
                                }
                                return AnsiTextView(
                                  key: _ansiTextViewKey,
                                  text: viewData.content,
                                  paneWidth: viewData.paneWidth,
                                  paneHeight: viewData.paneHeight,
                                  backgroundColor: Theme.of(context).scaffoldBackgroundColor,
                                  foregroundColor: Theme.of(context).colorScheme.onSurface.withValues(alpha: 0.9),
                                  onKeyInput: _handleKeyInput,
                                  onTap: () {
                                    _scrollToBottomKey.currentState?.show();
                                  },
                                  onDoubleTap: () {
                                    ref.read(actionBarProvider.notifier).toggleInputMode();
                                  },
                                  mode: _terminalMode,
                                  zoomEnabled: true,
                                  onZoomChanged: (scale) {
                                    setState(() {
                                      _zoomScale = scale;
                                    });
                                  },
                                  verticalScrollController: _terminalScrollController,
                                  cursorX: cursor.x,
                                  cursorY: cursor.y,
                                  scrollbackSize: viewData.scrollbackSize,
                                  onArrowSwipe: _dispatchSpecialKey,
                                  onTwoFingerSwipe: _handleTwoFingerSwipe,
                                  navigableDirections: _getNavigableDirections(),
                                );
                              },
                            );
                          },
                        ),
                      ),
                      // Pane indicator: ConsumerでtmuxProviderを直接watch
                      Positioned(
                        top: 8,
                        right: 8,
                        child: Consumer(
                          builder: (context, ref, _) {
                            final tmuxState = ref.watch(tmuxProvider(widget.connectionId));
                            return _buildPaneIndicator(tmuxState);
                          },
                        ),
                      ),
                      // スクロール位置インジケーター: ターミナルエリア左下
                      Positioned(
                        bottom: 8,
                        left: 12,
                        child: _buildScrollPositionIndicator(),
                      ),
                      // スクロールボタン: ターミナルエリア右下
                      Positioned(
                        bottom: 8,
                        right: 16,
                        child: ScrollToBottomButton(
                          key: _scrollToBottomKey,
                          scrollController: _terminalScrollController,
                          onPressed: () {
                            // Extension is a temporary lookup aid — dropping
                            // it on jump-to-bottom avoids paying the ongoing
                            // capture cost after the user is done browsing.
                            _pendingScrollbackCompensation = false;
                            _backend?.resetScrollback();
                            _ansiTextViewKey.currentState?.scrollToBottom();
                            // Hide after scroll completes (cursor end != maxScrollExtent)
                            Future.delayed(const Duration(milliseconds: 200), () {
                              _scrollToBottomKey.currentState?.hide();
                            });
                          },
                        ),
                      ),
                      // Gesture surface overlay
                      if (_gestureModeActive)
                        Positioned.fill(
                          child: GestureSurface(
                            onSpecialKeyPressed: _dispatchSpecialKey,
                            onPaste: (text) => _sendMultilineText(text),
                            onDeactivate: _deactivateGestureMode,
                            haptic: ref.read(settingsProvider).navPadHaptic,
                          ),
                        ),
                    ],
                  ),
                ),
              ),
              // 画像アップロード進捗バー
              Consumer(
                builder: (context, ref, _) {
                  final transfer = ref.watch(imageTransferProvider(widget.connectionId));
                  final isActive = transfer.phase == ImageTransferPhase.uploading ||
                      transfer.phase == ImageTransferPhase.converting;
                  if (!isActive) return const SizedBox.shrink();
                  return LinearProgressIndicator(
                    value: transfer.uploadProgress > 0 ? transfer.uploadProgress : null,
                    minHeight: 3,
                    backgroundColor: Colors.transparent,
                  );
                },
              ),
              // File transfer progress bar (upload or download)
              Consumer(
                builder: (context, ref, _) {
                  final transfer = ref.watch(fileTransferProvider(widget.connectionId));
                  final isUploading = transfer.phase == FileTransferPhase.uploading;
                  final isDownloading = transfer.phase == FileTransferPhase.downloading;
                  if (!isUploading && !isDownloading) return const SizedBox.shrink();
                  final progress = isUploading ? transfer.uploadProgress : transfer.downloadProgress;
                  return LinearProgressIndicator(
                    value: progress > 0 ? progress : null,
                    minHeight: 3,
                    backgroundColor: Colors.transparent,
                  );
                },
              ),
              // Navigation Pad (D-pad + action buttons)
              NavigationPad(
                onSpecialKeyPressed: _dispatchSpecialKey,
                onKeyPressed: _dispatchKey,
                onGestureToggle: _toggleGestureMode,
              ),
              // Action bar (swipeable button groups). Wrapped in a
              // Consumer so it rebuilds when the active pane changes
              // (e.g. tmux select-pane, window switch) and passes the
              // per-pane panelKey down to the profile-sheet invocation
              // as well, so the sheet mutates the right entry in the
              // per-panel profile map.
              Consumer(
                builder: (context, ref, _) {
                  final paneId = ref.watch(
                    tmuxProvider(widget.connectionId)
                        .select((s) => s.activePane?.id),
                  );
                  final panelKey =
                      paneId == null ? null : '${widget.connectionId}|$paneId';
                  return ActionBar(
                    panelKey: panelKey,
                    onKeyPressed: _dispatchKey,
                    onSpecialKeyPressed: _dispatchSpecialKey,
                    onFileTransfer: _handleFileTransfer,
                    onImageTransfer: _handleImageTransfer,
                    onSnippetPicker: () =>
                        _showSnippetPicker(context, panelKey: panelKey),
                    onDirectInputToggle: () {
                      ref.read(actionBarProvider.notifier).toggleInputMode();
                    },
                    onProfileSettings: () =>
                        _showProfileSheet(context, panelKey: panelKey),
                  );
                },
              ),
              // Compose bar (primary input)
              ComposeBar(
                key: _composeBarKey,
                connectionId: widget.connectionId,
                onSend: (text, {bool withEnter = true}) {
                  if (withEnter) {
                    _sendMultilineText(text);
                  } else {
                    _sendMultilineTextNoEnter(text);
                  }
                  _boostPolling();
                },
                onInsertMenu: () => _showInsertMenu(context),
                onSpecialKeyPressed: _dispatchSpecialKey,
                onKeyPressed: _dispatchKey,
              ),
              // Custom Flutter-native keyboard (direct input mode only,
              // gated on settings toggle — legacy hidden-TextField path
              // still used when disabled, for CJK/voice input).
              Consumer(
                builder: (context, ref, _) {
                  final composeMode = ref.watch(
                    actionBarProvider.select((s) => s.composeMode),
                  );
                  final useCustom = ref.watch(
                    settingsProvider.select((s) => s.useCustomKeyboard),
                  );
                  if (composeMode || !useCustom) {
                    return const SizedBox.shrink();
                  }
                  return CustomKeyboard(
                    onKeyPressed: _dispatchKey,
                    onSpecialKeyPressed: _dispatchSpecialKey,
                    haptic: ref.watch(settingsProvider).navPadHaptic,
                  );
                },
              ),
            ],
          ),
          // Floating joystick overlay (experimental)
          if (ref.watch(settingsProvider).floatingPadEnabled)
            Consumer(
              builder: (context, ref, _) {
                final composeMode = ref.watch(
                  actionBarProvider.select((s) => s.composeMode),
                );
                final useCustom = ref.watch(
                  settingsProvider.select((s) => s.useCustomKeyboard),
                );
                // Custom keyboard is ~220dp; shift joystick up when visible
                final kbOffset = (!composeMode && useCustom) ? 220.0 : 0.0;
                return FloatingJoystick(
                  onSpecialKeyPressed: _dispatchSpecialKey,
                  haptic: ref.watch(settingsProvider).navPadHaptic,
                  repeatRate: ref.watch(settingsProvider).navPadRepeatRate,
                  size: ref.watch(settingsProvider).floatingPadSize,
                  centerKey: ref.watch(settingsProvider).floatingPadCenterKey,
                  extraBottomOffset: kbOffset,
                );
              },
            ),
          // ローディングオーバーレイ
          if (_isConnecting || sshState.isConnecting)
            Container(
              color: isDark ? Colors.black54 : Colors.white70,
              child: const Center(
                child: CircularProgressIndicator(),
              ),
            ),
          // エラーオーバーレイ
          if (_connectionError != null || sshState.hasError)
            _buildErrorOverlay(sshState.error ?? _connectionError),
        ],
      ),
    );
  }

  // Fire-and-forget wrappers: convert the Future-returning send methods
  // into void callbacks for widget event handlers.
  void _dispatchSpecialKey(String tmuxKey) {
    _sendSpecialKey(tmuxKey);
  }

  void _dispatchKey(String key) {
    _sendKey(key);
  }

  /// AnsiTextViewからのキー入力を処理
  void _handleKeyInput(KeyInputEvent event) {
    if (event.isSpecialKey && event.tmuxKeyName != null) {
      _dispatchSpecialKey(event.tmuxKeyName!);
    } else {
      // 通常の文字はリテラル送信
      _sendKeyData(event.data);
    }
  }

  /// 2本指スワイプによるペイン切り替え
  void _handleTwoFingerSwipe(SwipeDirection direction) {
    final tmuxState = ref.read(tmuxProvider(widget.connectionId));
    final window = tmuxState.activeWindow;
    final activePane = tmuxState.activePane;
    if (window == null || activePane == null) return;

    // 設定に応じてスワイプ方向を反転
    final settings = ref.read(settingsProvider);
    final actualDirection = settings.invertPaneNavigation
        ? direction.inverted
        : direction;

    final targetPane = PaneNavigator.findAdjacentPane(
      panes: window.panes,
      current: activePane,
      direction: actualDirection,
    );

    if (targetPane != null) {
      _selectPane(targetPane.id);
    }
  }

  /// 現在のペインからナビゲーション可能な方向を取得
  Map<SwipeDirection, bool>? _getNavigableDirections() {
    final tmuxState = ref.read(tmuxProvider(widget.connectionId));
    final window = tmuxState.activeWindow;
    final activePane = tmuxState.activePane;
    if (window == null || activePane == null) return null;

    final rawDirections = PaneNavigator.getNavigableDirections(
      panes: window.panes,
      current: activePane,
    );

    // 反転設定が有効な場合、方向キーを入れ替える
    final settings = ref.read(settingsProvider);
    if (settings.invertPaneNavigation) {
      return {
        for (final dir in SwipeDirection.values)
          dir: rawDirections[dir.inverted] ?? false,
      };
    }

    return rawDirections;
  }

  /// キーデータをtmux send-keysで送信
  Future<void> _sendKeyData(String data) async {
    final sshClient = ref.read(sshProvider(widget.connectionId).notifier).client;

    // 接続が切れている場合はキューに追加
    if (sshClient == null || !sshClient.isConnected) {
      _inputQueue.enqueue(data);
      if (mounted) setState(() {}); // キューイング状態を更新
      return;
    }

    final target = ref.read(tmuxProvider(widget.connectionId).notifier).currentTarget;
    if (target == null) return;

    try {
      // エスケープシーケンスや特殊キーはリテラルで送信
      await sshClient.exec(TmuxCommands.sendKeys(target, data, literal: true));
      _boostPolling();
    } catch (_) {
      // キー送信エラーは静かに無視
    }
  }

  /// セッションを選択
  Future<void> _selectSession(String sessionName) async {
    final sshClient = ref.read(sshProvider(widget.connectionId).notifier).client;
    if (sshClient == null) return;

    // tmux_providerでアクティブセッションを更新
    ref.read(tmuxProvider(widget.connectionId).notifier).setActiveSession(sessionName);

    // アクティブなペインを選択状態にする（select-paneコマンドを実行）
    final activePaneId = ref.read(tmuxProvider(widget.connectionId)).activePaneId;
    if (activePaneId != null) {
      await _selectPane(activePaneId);
    } else {
      // ターミナル内容をクリアして再取得
      _viewNotifier.value = _viewNotifier.value.copyWith(content: '');
      _hasInitialScrolled = false;
    }
  }

  /// ウィンドウを選択
  Future<void> _selectWindow(String sessionName, int windowIndex) async {
    final sshClient = ref.read(sshProvider(widget.connectionId).notifier).client;
    if (sshClient == null || !sshClient.isConnected) return;

    // セッションが異なる場合はセッションも切り替え
    final currentSession = ref.read(tmuxProvider(widget.connectionId)).activeSessionName;
    if (currentSession != sessionName) {
      ref.read(tmuxProvider(widget.connectionId).notifier).setActiveSession(sessionName);
    }

    try {
      // tmux select-windowを実行
      await sshClient.exec(TmuxCommands.selectWindow(sessionName, windowIndex));
    } catch (e) {
      // SSH接続が閉じている場合は無視
      debugPrint('[Terminal] Failed to select window: $e');
      return;
    }
    if (!mounted || _isDisposed) return;

    // tmux_providerでアクティブウィンドウを更新
    ref.read(tmuxProvider(widget.connectionId).notifier).setActiveWindow(windowIndex);

    // アクティブなペインを選択状態にする（select-paneコマンドを実行）
    final activePaneId = ref.read(tmuxProvider(widget.connectionId)).activePaneId;
    if (activePaneId != null) {
      await _selectPane(activePaneId);
    } else {
      // ターミナル内容をクリアして再取得
      _viewNotifier.value = _viewNotifier.value.copyWith(content: '');
      _hasInitialScrolled = false;
    }
  }

  /// ペインを選択
  Future<void> _selectPane(String paneId) async {
    final sshClient = ref.read(sshProvider(widget.connectionId).notifier).client;
    if (sshClient == null || !sshClient.isConnected) return;

    try {
      // tmux select-paneを実行
      await sshClient.exec(TmuxCommands.selectPane(paneId));

      // Note: Focus events (\x1b[I / \x1b[O) are NOT sent here.
      // These are terminal-to-application signals (DECSET 1004) that cannot
      // be correctly injected via send-keys -l, which goes through shell input
      // processing. Apps that don't handle focus events would see literal '[I'
      // text. The proper mechanism is tmux's native focus-events option with
      // a real client, which TermiPod's exec-based architecture doesn't support.
    } catch (e) {
      // SSH接続が閉じている場合は無視
      debugPrint('[Terminal] Failed to select pane: $e');
      return;
    }
    if (!mounted || _isDisposed) return;

    // tmux_providerでアクティブペインを更新
    ref.read(tmuxProvider(widget.connectionId).notifier).setActivePane(paneId);

    // TerminalDisplayProviderにペイン情報を通知（フォントサイズ計算用）
    final activePane = ref.read(tmuxProvider(widget.connectionId)).activePane;
    final tmuxState = ref.read(tmuxProvider(widget.connectionId));
    if (activePane != null) {
      ref.read(terminalDisplayProvider.notifier).updatePane(activePane);
      _viewNotifier.value = _viewNotifier.value.copyWith(
        paneWidth: activePane.width,
        paneHeight: activePane.height,
        content: '',
      );
      // ペイン切り替え時は初回スクロールフラグをリセット
      // 次のコンテンツ受信時に最下部へスクロールされる
      _hasInitialScrolled = false;

      // 自動リサイズ: ペイン選択時に画面サイズに合わせてtmuxペインをリサイズ
      final settings = ref.read(settingsProvider);
      if (settings.isAutoResize) {
        await _executeAutoResize(activePane);
      }

      // セッション情報を保存（復元用）
      final sessionName = tmuxState.activeSessionName;
      final windowIndex = tmuxState.activeWindowIndex;
      if (sessionName != null && windowIndex != null) {
        ref.read(activeSessionsProvider.notifier).updateLastPane(
              connectionId: widget.connectionId,
              sessionName: sessionName,
              windowIndex: windowIndex,
              paneId: paneId,
            );
      }
    }
  }

  /// キャレット位置にスクロール
  ///
  /// パネル/ウィンドウ切り替え後の初回表示時に呼ばれ、
  /// カーソル行が画面中央付近に来るようスクロールする
  void _scrollToCaret() {
    Future.delayed(const Duration(milliseconds: 100), () {
      if (!mounted || _isDisposed) return;
      _ansiTextViewKey.currentState?.scrollToCaret();
    });
  }

  /// スクロール位置インジケーター
  ///
  /// ユーザーが最下部にいない時に現在行/総行数を表示する。
  Widget _buildScrollPositionIndicator() {
    return ListenableBuilder(
      listenable: _terminalScrollController,
      builder: (context, _) {
        if (!_terminalScrollController.hasClients) {
          return const SizedBox.shrink();
        }
        final position = _terminalScrollController.position;
        final maxExtent = position.maxScrollExtent;
        if (maxExtent <= 0) return const SizedBox.shrink();

        final currentOffset = position.pixels;

        final ansiState = _ansiTextViewKey.currentState;
        if (ansiState == null) return const SizedBox.shrink();

        final lineHeight = ansiState.lineHeight;
        if (lineHeight <= 0) return const SizedBox.shrink();

        final viewportHeight = position.viewportDimension;
        // Use effective line count (up to cursor + margin) instead of
        // raw total which includes trailing empty pane rows after resize
        final totalLines = ansiState.effectiveLineCount;
        if (totalLines <= 0) return const SizedBox.shrink();

        final currentTopLine = (currentOffset / lineHeight).round() + 1;
        final visibleLines = (viewportHeight / lineHeight).round();
        final currentBottomLine = (currentTopLine + visibleLines - 1).clamp(1, totalLines);

        // Hide when at or past the effective end
        if (currentBottomLine >= totalLines) return const SizedBox.shrink();

        final isDark = Theme.of(context).brightness == Brightness.dark;
        return Container(
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
          decoration: BoxDecoration(
            color: (isDark ? Colors.black : Colors.white).withValues(alpha: 0.7),
            borderRadius: BorderRadius.circular(8),
            border: Border.all(
              color: (isDark ? Colors.white : Colors.black).withValues(alpha: 0.15),
            ),
          ),
          child: Text(
            '$currentBottomLine / $totalLines',
            style: TextStyle(
              fontSize: 10,
              fontWeight: FontWeight.w500,
              color: (isDark ? Colors.white : Colors.black).withValues(alpha: 0.6),
            ),
          ),
        );
      },
    );
  }

  /// エラーオーバーレイ
  Widget _buildErrorOverlay(String? error) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final colorScheme = Theme.of(context).colorScheme;
    final queuedCount = _inputQueue.length;
    final isWaitingForNetwork = _sshState.isWaitingForNetwork;

    return Container(
      color: isDark ? Colors.black87 : Colors.white.withValues(alpha: 0.95),
      child: Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              isWaitingForNetwork ? Icons.signal_wifi_off : Icons.error_outline,
              color: isWaitingForNetwork ? DesignColors.warning : colorScheme.error,
              size: 48,
            ),
            const SizedBox(height: 16),
            Text(
              isWaitingForNetwork
                  ? 'Waiting for network...'
                  : (error ?? 'Connection error'),
              style: TextStyle(color: colorScheme.onSurface),
              textAlign: TextAlign.center,
            ),

            // キューイング状態
            if (queuedCount > 0) ...[
              const SizedBox(height: 12),
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
                decoration: BoxDecoration(
                  color: DesignColors.primary.withValues(alpha: 0.1),
                  borderRadius: BorderRadius.circular(8),
                ),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Icon(
                      Icons.keyboard,
                      size: 16,
                      color: DesignColors.primary,
                    ),
                    const SizedBox(width: 8),
                    Text(
                      '$queuedCount chars queued',
                      style: TextStyle(
                        color: DesignColors.primary,
                        fontSize: 12,
                      ),
                    ),
                    const SizedBox(width: 8),
                    GestureDetector(
                      onTap: () {
                        _inputQueue.clear();
                        setState(() {});
                      },
                      child: Icon(
                        Icons.clear,
                        size: 16,
                        color: DesignColors.primary.withValues(alpha: 0.7),
                      ),
                    ),
                  ],
                ),
              ),
            ],

            const SizedBox(height: 16),
            Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                ElevatedButton(
                  onPressed: () {
                    ref.read(sshProvider(widget.connectionId).notifier).reconnectNow();
                  },
                  child: Text(AppLocalizations.of(context)!.retryNow),
                ),
                if (_sshState.isReconnecting) ...[
                  const SizedBox(width: 12),
                  SizedBox(
                    width: 16,
                    height: 16,
                    child: CircularProgressIndicator(
                      strokeWidth: 2,
                      color: colorScheme.primary,
                    ),
                  ),
                ],
              ],
            ),
          ],
        ),
      ),
    );
  }

  /// Simple header for raw PTY mode (no tmux navigation).
  Widget _buildRawModeHeader() {
    final connection = ref.read(connectionsProvider.notifier).getById(widget.connectionId);
    final colorScheme = Theme.of(context).colorScheme;
    return SafeArea(
      bottom: false,
      child: Container(
        height: 40,
        decoration: BoxDecoration(
          color: colorScheme.surface.withValues(alpha: 0.9),
          border: Border(
            bottom: BorderSide(color: colorScheme.outline, width: 1),
          ),
        ),
        child: Row(
          children: [
            const SizedBox(width: 8),
            Icon(Icons.terminal, size: 16, color: colorScheme.onSurface.withValues(alpha: 0.7)),
            const SizedBox(width: 6),
            Text(
              connection?.name ?? 'Shell',
              style: TextStyle(
                color: colorScheme.onSurface,
                fontSize: 13,
                fontWeight: FontWeight.w500,
              ),
            ),
            const SizedBox(width: 8),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
              decoration: BoxDecoration(
                color: DesignColors.primary.withValues(alpha: 0.15),
                borderRadius: BorderRadius.circular(4),
              ),
              child: Text(
                'RAW',
                style: TextStyle(
                  color: DesignColors.primary,
                  fontSize: 9,
                  fontWeight: FontWeight.w700,
                  letterSpacing: 0.5,
                ),
              ),
            ),
            const Spacer(),
            // Terminal menu button
            IconButton(
              icon: Icon(Icons.more_vert, size: 18, color: colorScheme.onSurface.withValues(alpha: 0.7)),
              onPressed: _showTerminalMenu,
              padding: EdgeInsets.zero,
              constraints: const BoxConstraints(minWidth: 36, minHeight: 36),
            ),
            const SizedBox(width: 4),
          ],
        ),
      ),
    );
  }

  /// 上部のパンくずナビゲーションヘッダー
  Widget _buildBreadcrumbHeader(TmuxState tmuxState) {
    final currentSession = tmuxState.activeSessionName ?? '';
    final activeWindow = tmuxState.activeWindow;
    final currentWindow = activeWindow?.name ?? '';
    final activePane = tmuxState.activePane;
    final colorScheme = Theme.of(context).colorScheme;

    // SafeAreaを外側に配置してステータスバー分のスペースを確保
    return SafeArea(
      bottom: false,
      child: Container(
        height: 40,
        decoration: BoxDecoration(
          color: colorScheme.surface.withValues(alpha: 0.9),
          border: Border(
            bottom: BorderSide(color: colorScheme.outline, width: 1),
          ),
        ),
        child: Row(
          children: [
            const SizedBox(width: 8),
            // Breadcrumb navigation
            Expanded(
              child: SingleChildScrollView(
                scrollDirection: Axis.horizontal,
                child: Row(
                  children: [
                    // セッション名（タップで切り替え）
                    _buildBreadcrumbItem(
                      currentSession,
                      icon: Icons.folder,
                      isActive: true,
                      onTap: () => _showSessionSelector(tmuxState),
                    ),
                    _buildBreadcrumbSeparator(),
                    // ウィンドウ名（タップで切り替え）
                    _buildBreadcrumbItem(
                      currentWindow,
                      icon: Icons.tab,
                      isSelected: true,
                      onTap: () => _showWindowSelector(tmuxState),
                    ),
                    // ペインがあれば表示
                    if (activePane != null) ...[
                      _buildBreadcrumbSeparator(),
                      _buildBreadcrumbItem(
                        'Pane ${activePane.index}',
                        icon: Icons.terminal,
                        isActive: false,
                        onTap: () => _showPaneSelector(tmuxState),
                      ),
                    ],
                  ],
                ),
              ),
            ),
            // Scroll mode indicator
            if (_terminalMode == TerminalMode.scroll)
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                margin: const EdgeInsets.only(right: 8),
                decoration: BoxDecoration(
                  color: DesignColors.warning.withValues(alpha: 0.2),
                  borderRadius: BorderRadius.circular(4),
                  border: Border.all(color: DesignColors.warning.withValues(alpha: 0.5)),
                ),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Icon(Icons.unfold_more, size: 12, color: DesignColors.warning),
                    const SizedBox(width: 4),
                    Text(
                      'Scroll',
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 10,
                        color: DesignColors.warning,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ],
                ),
              ),
            // Zoom indicator
            if (_zoomScale != 1.0)
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 4),
                margin: const EdgeInsets.only(right: 8),
                decoration: BoxDecoration(
                  color: DesignColors.warning.withValues(alpha: 0.2),
                  borderRadius: BorderRadius.circular(4),
                ),
                child: Text(
                  '${(_zoomScale * 100).toStringAsFixed(0)}%',
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 10,
                    color: DesignColors.warning,
                  ),
                ),
              ),
            // Latency / Reconnect indicator（ValueListenableBuilderでポーリング更新をスコープ）
            ValueListenableBuilder<_TerminalViewData>(
              valueListenable: _viewNotifier,
              builder: (context, viewData, _) => _buildConnectionIndicator(viewData.latency),
            ),
            // Settings button
            IconButton(
              onPressed: _showTerminalMenu,
              icon: Icon(
                Icons.settings,
                size: 16,
                color: colorScheme.onSurface.withValues(alpha: 0.6),
              ),
              padding: const EdgeInsets.all(8),
              constraints: const BoxConstraints(),
            ),
            const SizedBox(width: 4),
          ],
        ),
      ),
    );
  }

  /// セッション選択ダイアログを表示
  void _showSessionSelector(TmuxState tmuxState) {
    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      isDismissible: true,
      enableDrag: true,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
      builder: (sheetContext) {
        final colorScheme = Theme.of(sheetContext).colorScheme;
        final maxHeight = MediaQuery.of(sheetContext).size.height * 0.6;
        return SafeArea(
          child: ConstrainedBox(
            constraints: BoxConstraints(maxHeight: maxHeight),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Padding(
                  padding: const EdgeInsets.all(16),
                  child: Row(
                    children: [
                      Icon(Icons.folder, color: colorScheme.primary),
                      const SizedBox(width: 8),
                      Text(
                        'Select Session',
                        style: GoogleFonts.spaceGrotesk(
                          fontSize: 18,
                          fontWeight: FontWeight.w700,
                          color: colorScheme.onSurface,
                        ),
                      ),
                    ],
                  ),
                ),
                Divider(height: 1, color: colorScheme.outline),
                Flexible(
                  child: ListView.builder(
                    shrinkWrap: true,
                    itemCount: tmuxState.sessions.length,
                    itemBuilder: (context, index) {
                      final session = tmuxState.sessions[index];
                      final isActive = session.name == tmuxState.activeSessionName;
                      return TmuxSessionTile(
                        session: session,
                        isActive: isActive,
                        onTap: () {
                          Navigator.pop(context);
                          _selectSession(session.name);
                        },
                      );
                    },
                  ),
                ),
                const SizedBox(height: 16),
              ],
            ),
          ),
        );
      },
    ).then((_) {
      _scrollToBottomKey.currentState?.show();
    });
  }

  /// ウィンドウ選択ダイアログを表示
  void _showWindowSelector(TmuxState tmuxState) {
    final session = tmuxState.activeSession;
    if (session == null) return;

    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      isDismissible: true,
      enableDrag: true,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
      builder: (sheetContext) {
        final colorScheme = Theme.of(sheetContext).colorScheme;
        final maxHeight = MediaQuery.of(sheetContext).size.height * 0.6;
        return SafeArea(
          child: ConstrainedBox(
            constraints: BoxConstraints(maxHeight: maxHeight),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Padding(
                  padding: const EdgeInsets.all(16),
                  child: Row(
                    children: [
                      Icon(Icons.tab, color: colorScheme.primary),
                      const SizedBox(width: 8),
                      Text(
                        'Select Window',
                        style: GoogleFonts.spaceGrotesk(
                          fontSize: 18,
                          fontWeight: FontWeight.w700,
                          color: colorScheme.onSurface,
                        ),
                      ),
                      const Spacer(),
                      IconButton(
                        icon: Icon(Icons.open_in_full, color: colorScheme.primary),
                        tooltip: AppLocalizations.of(context)!.resizeWindowTooltip,
                        onPressed: () {
                          Navigator.pop(sheetContext);
                          Future.delayed(const Duration(milliseconds: 200), () {
                            if (mounted) _showResizeWindowChooser(tmuxState);
                          });
                        },
                      ),
                      IconButton(
                        icon: Icon(Icons.add, color: colorScheme.primary),
                        tooltip: AppLocalizations.of(context)!.newWindowTooltip,
                        onPressed: () {
                          Navigator.pop(sheetContext);
                          Future.delayed(const Duration(milliseconds: 200), () {
                            if (mounted) _showCreateWindowDialog(session);
                          });
                        },
                      ),
                    ],
                  ),
                ),
                Divider(height: 1, color: colorScheme.outline),
                Flexible(
                  child: ListView.builder(
                    shrinkWrap: true,
                    itemCount: session.windows.length,
                    itemBuilder: (context, index) {
                      final window = session.windows[index];
                      final isActive = window.index == tmuxState.activeWindowIndex;
                      return TmuxWindowTile(
                        window: window,
                        isActive: isActive,
                        onTap: () {
                          Navigator.pop(context);
                          _selectWindow(session.name, window.index);
                        },
                        onResize: () {
                          Navigator.pop(context);
                          _handleResizeWindow(window);
                        },
                        onClose: () {
                          Navigator.pop(context);
                          _confirmAndKillWindow(
                            sessionName: session.name,
                            windowIndex: window.index,
                            windowName: window.name,
                            isLastWindow: session.windows.length == 1,
                          );
                        },
                      );
                    },
                  ),
                ),
                const SizedBox(height: 16),
              ],
            ),
          ),
        );
      },
    ).then((_) {
      _scrollToBottomKey.currentState?.show();
    });
  }

  /// ウィンドウ作成ダイアログを表示
  void _showCreateWindowDialog(TmuxSession session) {
    final existingNames = session.windows.map((w) => w.name).toList();
    showDialog<String>(
      context: context,
      builder: (dialogContext) => _NewWindowDialog(
        existingWindowNames: existingNames,
      ),
    ).then((windowName) {
      if (windowName != null) {
        _createWindow(windowName.isEmpty ? null : windowName);
      }
    });
  }

  /// 新しいウィンドウを作成
  Future<void> _createWindow(String? windowName) async {
    if (_isCreatingWindow) return;
    _isCreatingWindow = true;
    try {
      final sshClient = ref.read(sshProvider(widget.connectionId).notifier).client;
      if (sshClient == null || !sshClient.isConnected) {
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(content: Text(AppLocalizations.of(context)!.sshNotAvailable)),
          );
        }
        return;
      }
      final session = ref.read(tmuxProvider(widget.connectionId)).activeSession;
      if (session == null) return;

      await sshClient.exec(TmuxCommands.newWindow(
        sessionName: session.name,
        windowName: windowName,
      ));
      await _refreshSessionTree();
      if (!mounted) return;

      // active=1のウィンドウを検出して自動切替
      final updatedSession = ref.read(tmuxProvider(widget.connectionId)).activeSession;
      final activeWindow =
          updatedSession?.windows.where((w) => w.active).firstOrNull;
      if (activeWindow != null) {
        ref.read(tmuxProvider(widget.connectionId).notifier).setActiveWindow(activeWindow.index);
        _viewNotifier.value = _viewNotifier.value.copyWith(content: '');
        _hasInitialScrolled = false;
        final activePaneId = ref.read(tmuxProvider(widget.connectionId)).activePaneId;
        if (activePaneId != null) {
          await _selectPane(activePaneId);
        }
      }
      _boostPolling();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(AppLocalizations.of(context)!.createWindowFailed(e.toString()))),
        );
      }
    } finally {
      _isCreatingWindow = false;
    }
  }

  /// ペインを分割
  Future<void> _splitPane(String paneId, SplitDirection direction) async {
    final sshClient = ref.read(sshProvider(widget.connectionId).notifier).client;
    if (sshClient == null || !sshClient.isConnected) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(AppLocalizations.of(context)!.sshNotAvailable)),
        );
      }
      return;
    }

    try {
      final command = direction == SplitDirection.horizontal
          ? TmuxCommands.splitWindowHorizontal(target: paneId)
          : TmuxCommands.splitWindowVertical(target: paneId);
      await sshClient.exec(command);
      await _refreshSessionTree();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(AppLocalizations.of(context)!.splitPaneFailed(e.toString()))),
        );
      }
    }
  }

  /// ペインを閉じる確認ダイアログを表示
  void _confirmAndKillPane({
    required String paneId,
    required String paneTitle,
    required bool isLastPane,
    required bool isLastWindow,
  }) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    showDialog(
      context: context,
      builder: (dialogContext) {
        return AlertDialog(
          backgroundColor:
              isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
          title: Text(
            AppLocalizations.of(context)!.closePaneTitle,
            style: TextStyle(
              color: isDark
                  ? DesignColors.textPrimary
                  : DesignColors.textPrimaryLight,
            ),
          ),
          content: Text(
            isLastPane && isLastWindow
                ? AppLocalizations.of(context)!.closeLastPaneWarning
                : isLastPane
                    ? AppLocalizations.of(context)!.closeLastPaneInWindowWarning
                    : AppLocalizations.of(context)!.closePaneConfirm(paneTitle),
            style: TextStyle(
              color: isDark
                  ? DesignColors.textSecondary
                  : DesignColors.textSecondaryLight,
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(dialogContext),
              child: Text(
                AppLocalizations.of(context)!.buttonCancel,
                style: TextStyle(
                  color: isDark
                      ? DesignColors.textSecondary
                      : DesignColors.textSecondaryLight,
                ),
              ),
            ),
            ElevatedButton(
              onPressed: () {
                Navigator.pop(dialogContext);
                final currentWindow = ref.read(tmuxProvider(widget.connectionId)).activeWindow;
                if (currentWindow == null ||
                    !currentWindow.panes.any((p) => p.id == paneId)) {
                  if (mounted) {
                    ScaffoldMessenger.of(context).showSnackBar(
                      SnackBar(
                          content:
                              Text(AppLocalizations.of(context)!.paneNoLongerExists)),
                    );
                  }
                  return;
                }
                _killPane(
                  paneId: paneId,
                  isLastPane: isLastPane,
                  isLastWindow: isLastWindow,
                );
              },
              style: ElevatedButton.styleFrom(
                backgroundColor: DesignColors.error,
                foregroundColor: Colors.white,
              ),
              child: Text(AppLocalizations.of(context)!.buttonClose),
            ),
          ],
        );
      },
    );
  }

  /// リサイズ対象のペインをグラフィカルに選択するダイアログ
  void _showResizePaneChooser(TmuxState tmuxState) {
    final window = tmuxState.activeWindow;
    if (window == null || window.panes.isEmpty) return;

    showDialog(
      context: context,
      builder: (dialogContext) {
        return _ResizePaneChooserDialog(
          panes: window.panes,
          activePaneId: tmuxState.activePaneId,
          onResize: (selectedPane) {
            Navigator.pop(dialogContext);
            _handleResizePane(selectedPane);
          },
        );
      },
    );
  }

  /// リサイズ対象のウィンドウをグラフィカルに選択するダイアログ
  void _showResizeWindowChooser(TmuxState tmuxState) {
    final session = tmuxState.activeSession;
    if (session == null || session.windows.isEmpty) return;

    showDialog(
      context: context,
      builder: (dialogContext) {
        return _ResizeWindowChooserDialog(
          windows: session.windows,
          activeWindowIndex: tmuxState.activeWindowIndex,
          onResize: (selectedWindow) {
            Navigator.pop(dialogContext);
            _handleResizeWindow(selectedWindow);
          },
        );
      },
    );
  }

  /// 自動リサイズ: 画面サイズに合わせてtmuxペインをリサイズ
  Future<void> _executeAutoResize(TmuxPane pane) async {
    if (_isResizing) return;
    if (_tmuxVersion != null && !_tmuxVersion!.supportsResizePaneToSize) return;

    final displayState = ref.read(terminalDisplayProvider);
    final settings = ref.read(settingsProvider);

    final fontSize = settings.fontSize;
    final targetCols = FontCalculator.calculateMaxCols(
      screenWidth: displayState.screenWidth,
      fontSize: fontSize,
      fontFamily: settings.fontFamily,
    );
    final targetRows = FontCalculator.calculateMaxRows(
      screenHeight: displayState.screenHeight,
      fontSize: fontSize,
      fontFamily: settings.fontFamily,
    );

    debugPrint('[AutoResize] screenWidth=${displayState.screenWidth} '
        'screenHeight=${displayState.screenHeight} '
        'fontSize=$fontSize '
        'fontFamily=${settings.fontFamily} '
        'pane=${pane.id} current=${pane.width}x${pane.height} '
        'target=${targetCols}x$targetRows');

    // Same size — no resize needed, but viewport may have changed
    // (e.g. keyboard, system UI). Scroll to bottom directly.
    if (pane.width == targetCols && pane.height == targetRows) {
      _ansiTextViewKey.currentState?.scrollToBottom();
      _scrollToBottomKey.currentState?.show();
      return;
    }

    _isResizing = true;
    _pollTimer?.cancel();
    try {
      final sshClient = ref.read(sshProvider(widget.connectionId).notifier).client;
      if (sshClient == null || !sshClient.isConnected) return;
      await sshClient.exec(
        TmuxCommands.resizePaneToSize(pane.id, cols: targetCols, rows: targetRows),
      );
      await _refreshSessionTree();
      final updatedPane = ref.read(tmuxProvider(widget.connectionId)).activePane;
      if (updatedPane != null) {
        ref.read(terminalDisplayProvider.notifier).updatePane(updatedPane);
      }
    } catch (e) {
      debugPrint('[AutoResize] Failed: $e');
    } finally {
      _isResizing = false;
      if (mounted && !_isDisposed) {
        _startPolling();
        // Defer scroll-to-bottom until first poll delivers new content
        _pendingScrollToBottom = true;
        _scrollToBottomKey.currentState?.show();
      }
    }
  }

  /// ペインをリサイズ
  Future<void> _handleResizePane(TmuxPane pane) async {
    if (_isResizing) return;

    final displayState = ref.read(terminalDisplayProvider);
    final settings = ref.read(settingsProvider);
    final tmuxState = ref.read(tmuxProvider(widget.connectionId));

    // 現在のウィンドウの全ペインを取���
    final activeWindow = tmuxState.activeWindow;
    final allPanes = activeWindow?.panes ?? [pane];

    final result = await showDialog<ResizeResult>(
      context: context,
      builder: (context) => ResizePaneDialog(
        targetPane: pane,
        allPanesInWindow: allPanes,
        currentCols: pane.width,
        currentRows: pane.height,
        screenWidth: displayState.screenWidth,
        screenHeight: displayState.screenHeight,
        fontSize: displayState.calculatedFontSize,
        fontFamily: settings.fontFamily,
      ),
    );

    if (result == null || !mounted) return;

    _isResizing = true;
    _pollTimer?.cancel();
    try {
      final sshClient = ref.read(sshProvider(widget.connectionId).notifier).client;
      if (sshClient == null) return;
      await sshClient.exec(
        TmuxCommands.resizePaneToSize(pane.id, cols: result.cols, rows: result.rows),
      );
      await _refreshSessionTree();
      // 明示的にupdatePaneを呼んでフォント再計算
      final activePane = ref.read(tmuxProvider(widget.connectionId)).activePane;
      if (activePane != null) {
        ref.read(terminalDisplayProvider.notifier).updatePane(activePane);
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(AppLocalizations.of(context)!.resizeFailed(e.toString()))),
        );
      }
    } finally {
      _isResizing = false;
      if (mounted && !_isDisposed) _startPolling();
    }
  }

  /// ウィンドウをリサイズ
  Future<void> _handleResizeWindow(TmuxWindow window) async {
    if (_isResizing) return;

    final displayState = ref.read(terminalDisplayProvider);
    final settings = ref.read(settingsProvider);

    // ウィンドウサイズはペインのwidth+leftの最大値で推定
    final panes = window.panes;
    int windowCols = 80;
    int windowRows = 24;
    if (panes.isNotEmpty) {
      windowCols = panes.map((p) => p.left + p.width).reduce((a, b) => a > b ? a : b);
      windowRows = panes.map((p) => p.top + p.height).reduce((a, b) => a > b ? a : b);
    }

    final result = await showDialog<ResizeResult>(
      context: context,
      builder: (context) => ResizeWindowDialog(
        window: window,
        panes: panes,
        currentCols: windowCols,
        currentRows: windowRows,
        screenWidth: displayState.screenWidth,
        screenHeight: displayState.screenHeight,
        fontSize: displayState.calculatedFontSize,
        fontFamily: settings.fontFamily,
        supportsResizeWindow: _tmuxVersion?.supportsResizeWindow ?? false,
      ),
    );

    if (result == null || !mounted) return;

    _isResizing = true;
    _pollTimer?.cancel();
    try {
      final sshClient = ref.read(sshProvider(widget.connectionId).notifier).client;
      if (sshClient == null) return;
      final tmuxState = ref.read(tmuxProvider(widget.connectionId));
      final target = '${tmuxState.activeSessionName}:${window.index}';
      await sshClient.exec(
        TmuxCommands.resizeWindow(target, cols: result.cols, rows: result.rows),
      );
      await _refreshSessionTree();
      final activePane = ref.read(tmuxProvider(widget.connectionId)).activePane;
      if (activePane != null) {
        ref.read(terminalDisplayProvider.notifier).updatePane(activePane);
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(AppLocalizations.of(context)!.resizeFailed(e.toString()))),
        );
      }
    } finally {
      _isResizing = false;
      if (mounted && !_isDisposed) _startPolling();
    }
  }

  /// ペインを閉じる（SSH経由でkill-pane実行）
  Future<void> _killPane({
    required String paneId,
    required bool isLastPane,
    required bool isLastWindow,
  }) async {
    final sshClient = ref.read(sshProvider(widget.connectionId).notifier).client;
    if (sshClient == null || !sshClient.isConnected) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(AppLocalizations.of(context)!.sshNotAvailable)),
        );
      }
      return;
    }

    // ポーリング停止（SSH競合回避）
    _pollTimer?.cancel();

    try {
      await sshClient.exec(TmuxCommands.killPane(paneId));
      await _refreshSessionTree();
      if (!mounted || _isDisposed) return;

      // セッション消滅確認（最後のウィンドウの最後のペインだった場合）
      if (isLastPane && isLastWindow) {
        final sessionsOutput =
            await sshClient.exec('tmux list-sessions 2>/dev/null || true');
        if (!mounted || _isDisposed) return;
        if (sessionsOutput.trim().isEmpty) {
          await _disconnect();
          return;
        }
      }

      // 最後のペインだった場合→tmuxが自動選択した新ウィンドウに同期
      if (isLastPane) {
        final newTmuxState = ref.read(tmuxProvider(widget.connectionId));
        final newSession = newTmuxState.activeSession;
        if (newSession != null) {
          final newActiveWindow =
              newSession.windows.where((w) => w.active).firstOrNull ??
                  newSession.windows.firstOrNull;
          if (newActiveWindow != null) {
            await _selectWindow(newSession.name, newActiveWindow.index);
          }
        }
      } else {
        // 同じウィンドウ内の残りペインに同期
        final newTmuxState = ref.read(tmuxProvider(widget.connectionId));
        final activeWindow = newTmuxState.activeWindow;
        if (activeWindow != null) {
          final newActivePane =
              activeWindow.panes.where((p) => p.active).firstOrNull ??
                  activeWindow.panes.firstOrNull;
          if (newActivePane != null) {
            await _selectPane(newActivePane.id);
          }
        }
      }
    } catch (e) {
      debugPrint('[Terminal] Failed to kill pane: $e');
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(AppLocalizations.of(context)!.closePaneFailed(e.toString()))),
        );
      }
    } finally {
      // ポーリング再開
      if (mounted && !_isDisposed) {
        _startPolling();
      }
    }
  }

  /// ペイン選択ダイアログを表示
  void _showPaneSelector(TmuxState tmuxState) {
    final window = tmuxState.activeWindow;
    if (window == null) return;

    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      isDismissible: true,
      enableDrag: true,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
      builder: (sheetContext) {
        final colorScheme = Theme.of(sheetContext).colorScheme;
        final maxHeight = MediaQuery.of(sheetContext).size.height * 0.7;
        return SafeArea(
          child: ConstrainedBox(
            constraints: BoxConstraints(maxHeight: maxHeight),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Padding(
                  padding: const EdgeInsets.all(16),
                  child: Row(
                    children: [
                      Icon(Icons.terminal, color: colorScheme.primary),
                      const SizedBox(width: 8),
                      Text(
                        'Select Pane',
                        style: GoogleFonts.spaceGrotesk(
                          fontSize: 18,
                          fontWeight: FontWeight.w700,
                          color: colorScheme.onSurface,
                        ),
                      ),
                      const Spacer(),
                      IconButton(
                        icon: Icon(Icons.open_in_full, color: colorScheme.primary),
                        tooltip: AppLocalizations.of(context)!.resizePaneTooltip,
                        onPressed: () {
                          Navigator.pop(sheetContext);
                          Future.delayed(const Duration(milliseconds: 200), () {
                            if (mounted) _showResizePaneChooser(tmuxState);
                          });
                        },
                      ),
                    ],
                  ),
                ),
                Divider(height: 1, color: colorScheme.outline),
                // ペインレイアウトのビジュアル表示
                _PaneLayoutVisualizer(
                  panes: window.panes,
                  activePaneId: tmuxState.activePaneId,
                  onPaneSelected: (paneId) {
                    Navigator.pop(sheetContext);
                    _selectPane(paneId);
                  },
                  onSplitRequested: (paneId, direction) {
                    Navigator.pop(sheetContext);
                    _splitPane(paneId, direction);
                  },
                ),
                Divider(height: 1, color: colorScheme.outline),
                // ペイン一覧
                Flexible(
                  child: ListView.builder(
                    shrinkWrap: true,
                    itemCount: window.panes.length,
                    itemBuilder: (context, index) {
                      final pane = window.panes[index];
                      final isActive = pane.id == tmuxState.activePaneId;
                      // タイトルを優先表示、なければコマンド名、それもなければPaneインデックス
                      final paneTitle = pane.title?.isNotEmpty == true
                          ? pane.title!
                          : (pane.currentCommand?.isNotEmpty == true
                              ? pane.currentCommand!
                              : 'Pane ${pane.index}');
                      return TmuxPaneTile(
                        pane: pane,
                        paneTitle: paneTitle,
                        isActive: isActive,
                        onTap: () {
                          Navigator.pop(context);
                          _selectPane(pane.id);
                        },
                        onLongPress: () {
                          Navigator.pop(context);
                          _confirmAndKillPane(
                            paneId: pane.id,
                            paneTitle: paneTitle,
                            isLastPane: window.panes.length == 1,
                            isLastWindow:
                                (tmuxState.activeSession?.windows.length ??
                                        0) ==
                                    1,
                          );
                        },
                        onResize: () {
                          Navigator.pop(context);
                          _handleResizePane(pane);
                        },
                        onClose: () {
                          Navigator.pop(context);
                          _confirmAndKillPane(
                            paneId: pane.id,
                            paneTitle: paneTitle,
                            isLastPane: window.panes.length == 1,
                            isLastWindow:
                                (tmuxState.activeSession?.windows.length ??
                                        0) ==
                                    1,
                          );
                        },
                      );
                    },
                  ),
                ),
                const SizedBox(height: 16),
              ],
            ),
          ),
        );
      },
    ).then((_) {
      _scrollToBottomKey.currentState?.show();
    });
  }

  Widget _buildBreadcrumbItem(
    String label, {
    IconData? icon,
    bool isActive = false,
    bool isSelected = false,
    VoidCallback? onTap,
  }) {
    final colorScheme = Theme.of(context).colorScheme;
    return GestureDetector(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
        decoration: isSelected
            ? BoxDecoration(
                color: colorScheme.onSurface.withValues(alpha: 0.05),
                borderRadius: BorderRadius.circular(4),
                border: Border.all(color: colorScheme.onSurface.withValues(alpha: 0.05)),
              )
            : null,
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (icon != null) ...[
              Icon(
                icon,
                size: 12,
                color: isActive
                    ? colorScheme.primary
                    : (isSelected ? colorScheme.onSurface : colorScheme.onSurface.withValues(alpha: 0.6)),
              ),
              const SizedBox(width: 4),
            ],
            Text(
              label.isEmpty ? '...' : label,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 11,
                fontWeight: isActive || isSelected ? FontWeight.w700 : FontWeight.w400,
                color: isActive
                    ? colorScheme.primary
                    : (isSelected ? colorScheme.onSurface : colorScheme.onSurface.withValues(alpha: 0.5)),
              ),
            ),
            if (onTap != null) ...[
              const SizedBox(width: 2),
              Icon(
                Icons.arrow_drop_down,
                size: 14,
                color: isActive
                    ? colorScheme.primary.withValues(alpha: 0.7)
                    : colorScheme.onSurface.withValues(alpha: 0.38),
              ),
            ],
          ],
        ),
      ),
    );
  }

  Widget _buildBreadcrumbSeparator() {
    final colorScheme = Theme.of(context).colorScheme;
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 4),
      child: Text(
        '/',
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
          fontWeight: FontWeight.w300,
          color: colorScheme.onSurface.withValues(alpha: 0.2),
        ),
      ),
    );
  }

  /// ターミナルメニューを表示
  void _showTerminalMenu() {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final menuBgColor = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final textColor = isDark ? Colors.white : Colors.black87;
    final mutedTextColor = isDark ? Colors.white38 : Colors.black38;
    final inactiveIconColor = isDark ? Colors.white60 : Colors.black45;

    showModalBottomSheet(
      context: context,
      backgroundColor: menuBgColor,
      isDismissible: true,
      enableDrag: true,
      isScrollControlled: true,
      constraints: BoxConstraints(
        maxHeight: MediaQuery.of(context).size.height * 0.85,
      ),
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
      builder: (context) {
        return SafeArea(
          child: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 16, 8, 16),
                child: Row(
                  children: [
                    Icon(Icons.tune, color: DesignColors.primary),
                    const SizedBox(width: 8),
                    Expanded(
                      child: Text(
                        'Terminal Options',
                        style: GoogleFonts.spaceGrotesk(
                          fontSize: 18,
                          fontWeight: FontWeight.w700,
                          color: textColor,
                        ),
                      ),
                    ),
                    // Help moved inline with the title — was previously a
                    // ListTile lower down, but it's a high-frequency action
                    // that deserves a one-tap entry from the menu header.
                    IconButton(
                      icon: Icon(Icons.help_outline, color: textColor),
                      tooltip: 'Help',
                      onPressed: () {
                        Navigator.pop(context);
                        showHelpSheet(context, ref);
                      },
                    ),
                  ],
                ),
              ),
              Divider(height: 1, color: isDark ? const Color(0xFF2A2B36) : Colors.grey.shade300),
              // モード切り替え（Normal / Scroll & Select）
              ListTile(
                leading: Icon(
                  _terminalMode == TerminalMode.scroll
                      ? Icons.unfold_more
                      : Icons.keyboard,
                  color: _terminalMode == TerminalMode.scroll
                      ? DesignColors.warning
                      : inactiveIconColor,
                ),
                title: Text(
                  _terminalMode == TerminalMode.scroll
                      ? AppLocalizations.of(context)!.scrollSelectMode
                      : AppLocalizations.of(context)!.normalMode,
                  style: TextStyle(
                    color: _terminalMode == TerminalMode.scroll
                        ? DesignColors.warning
                        : textColor,
                    fontWeight: _terminalMode == TerminalMode.scroll
                        ? FontWeight.bold
                        : FontWeight.normal,
                  ),
                ),
                subtitle: Text(
                  _terminalMode == TerminalMode.scroll
                      ? AppLocalizations.of(context)!.scrollModeHintOn
                      : AppLocalizations.of(context)!.scrollModeHintOff,
                  style: TextStyle(color: mutedTextColor, fontSize: 12),
                ),
                trailing: Switch(
                  value: _terminalMode == TerminalMode.scroll,
                  onChanged: (value) {
                    final newMode = value
                        ? TerminalMode.scroll
                        : TerminalMode.normal;
                    setState(() {
                      _terminalMode = newMode;
                      _scrollModeSource = value ? ScrollModeSource.manual : ScrollModeSource.none;
                      if (value) _gestureModeActive = false;
                    });
                    if (newMode == TerminalMode.scroll) {
                      _enterTmuxCopyMode();
                    } else {
                      _cancelTmuxCopyMode();
                      _applyBufferedUpdate();
                    }
                    Navigator.pop(context);
                  },
                  activeThumbColor: DesignColors.warning,
                ),
                onTap: () {
                  final isScrolling = _terminalMode == TerminalMode.scroll;
                  final newMode = isScrolling
                      ? TerminalMode.normal
                      : TerminalMode.scroll;
                  setState(() {
                    _terminalMode = newMode;
                    _scrollModeSource = isScrolling ? ScrollModeSource.none : ScrollModeSource.manual;
                    if (!isScrolling) _gestureModeActive = false;
                  });
                  if (newMode == TerminalMode.scroll) {
                    _enterTmuxCopyMode();
                  } else {
                    _cancelTmuxCopyMode();
                    _applyBufferedUpdate();
                  }
                  Navigator.pop(context);
                },
              ),
              // ズームリセット
              ListTile(
                leading: Icon(
                  Icons.zoom_out_map,
                  color: _zoomScale != 1.0 ? DesignColors.warning : inactiveIconColor,
                ),
                title: Text(
                  AppLocalizations.of(context)!.resetZoom,
                  style: TextStyle(
                    color: _zoomScale != 1.0 ? textColor : mutedTextColor,
                  ),
                ),
                subtitle: Text(
                  _zoomScale != 1.0
                      ? AppLocalizations.of(context)!.zoomCurrent((_zoomScale * 100).toStringAsFixed(0))
                      : AppLocalizations.of(context)!.zoomHint,
                  style: TextStyle(color: mutedTextColor, fontSize: 12),
                ),
                enabled: _zoomScale != 1.0,
                onTap: _zoomScale != 1.0
                    ? () {
                        _ansiTextViewKey.currentState?.resetZoom();
                        setState(() {
                          _zoomScale = 1.0;
                        });
                        Navigator.pop(context);
                      }
                    : null,
              ),
              // Gesture mode toggle — makes the GestureSurface overlay
              // discoverable regardless of Navigation Pad state. The other
              // activation path is double-tapping the D-pad/joystick center.
              ListTile(
                leading: Icon(
                  Icons.touch_app,
                  color: _gestureModeActive ? DesignColors.primary : inactiveIconColor,
                ),
                title: Text(
                  AppLocalizations.of(context)!.gestureModeMenuTitle,
                  style: TextStyle(
                    color: _gestureModeActive ? DesignColors.primary : textColor,
                    fontWeight: _gestureModeActive ? FontWeight.bold : FontWeight.normal,
                  ),
                ),
                subtitle: Text(
                  AppLocalizations.of(context)!.gestureModeMenuDesc,
                  style: TextStyle(color: mutedTextColor, fontSize: 12),
                ),
                trailing: Switch(
                  value: _gestureModeActive,
                  onChanged: (_) {
                    _toggleGestureMode();
                    Navigator.pop(context);
                  },
                  activeThumbColor: DesignColors.primary,
                ),
                onTap: () {
                  _toggleGestureMode();
                  Navigator.pop(context);
                },
              ),
              // Navigation Pad mode cycle
              Consumer(
                builder: (context, menuRef, _) {
                  final navMode = menuRef.watch(settingsProvider).navPadMode;
                  final l10n = AppLocalizations.of(context)!;
                  final modeLabel = switch (navMode) {
                    'off' => l10n.navPadModeOff,
                    _ => l10n.navPadModeCompact,
                  };
                  return ListTile(
                    leading: Icon(
                      Icons.gamepad,
                      color: navMode != 'off' ? DesignColors.primary : inactiveIconColor,
                    ),
                    title: Text(
                      l10n.navPadMenuTitle,
                      style: TextStyle(color: textColor),
                    ),
                    subtitle: Text(
                      modeLabel,
                      style: TextStyle(color: mutedTextColor, fontSize: 12),
                    ),
                    onTap: () {
                      menuRef.read(settingsProvider.notifier).cycleNavPadMode();
                      Navigator.pop(context);
                    },
                  );
                },
              ),
              // Floating Joystick toggle — also lives in Settings under
              // Experimental, but exposing it here makes it discoverable
              // without leaving the terminal screen.
              Consumer(
                builder: (context, menuRef, _) {
                  final l10n = AppLocalizations.of(context)!;
                  final fpEnabled = menuRef.watch(
                    settingsProvider.select((s) => s.floatingPadEnabled),
                  );
                  return SwitchListTile(
                    secondary: Icon(
                      Icons.gamepad_outlined,
                      color: fpEnabled ? DesignColors.primary : inactiveIconColor,
                    ),
                    title: Text(
                      l10n.floatingPad,
                      style: TextStyle(
                        color: fpEnabled ? DesignColors.primary : textColor,
                        fontWeight: fpEnabled ? FontWeight.bold : FontWeight.normal,
                      ),
                    ),
                    subtitle: Text(
                      l10n.floatingPadDesc,
                      style: TextStyle(color: mutedTextColor, fontSize: 12),
                    ),
                    value: fpEnabled,
                    onChanged: (v) {
                      menuRef.read(settingsProvider.notifier).setFloatingPadEnabled(v);
                    },
                    activeThumbColor: DesignColors.primary,
                  );
                },
              ),
              Divider(height: 1, color: isDark ? const Color(0xFF2A2B36) : Colors.grey.shade300),
              // Downloads
              Consumer(
                builder: (context, menuRef, _) {
                  final dmState = menuRef.watch(downloadManagerProvider);
                  final activeCount = dmState.activeCount;
                  final totalCount = dmState.entries.length;
                  return ListTile(
                    leading: Badge(
                      isLabelVisible: activeCount > 0,
                      label: Text('$activeCount'),
                      child: Icon(
                        Icons.download,
                        color: activeCount > 0 ? DesignColors.primary : inactiveIconColor,
                      ),
                    ),
                    title: Text(
                      'Downloads',
                      style: TextStyle(color: textColor),
                    ),
                    subtitle: Text(
                      totalCount == 0
                          ? 'No downloads'
                          : activeCount > 0
                              ? '$activeCount active, $totalCount total'
                              : '$totalCount downloads',
                      style: TextStyle(color: mutedTextColor, fontSize: 12),
                    ),
                    onTap: () {
                      Navigator.pop(context);
                      showDownloadManagerSheet(context);
                    },
                  );
                },
              ),
              Divider(height: 1, color: isDark ? const Color(0xFF2A2B36) : Colors.grey.shade300),
              // 設定画面へ
              ListTile(
                leading: Icon(
                  Icons.settings,
                  color: inactiveIconColor,
                ),
                title: Text(
                  AppLocalizations.of(context)!.settingsLabel,
                  style: TextStyle(color: textColor),
                ),
                subtitle: Text(
                  AppLocalizations.of(context)!.settingsDesc,
                  style: TextStyle(color: mutedTextColor, fontSize: 12),
                ),
                onTap: () {
                  Navigator.pop(context);
                  Navigator.push(
                    context,
                    MaterialPageRoute(
                      builder: (context) => const SettingsScreen(),
                    ),
                  );
                },
              ),
              Divider(height: 1, color: isDark ? const Color(0xFF2A2B36) : Colors.grey.shade300),
              // 切断ボタン
              ListTile(
                leading: Icon(
                  Icons.power_settings_new,
                  color: DesignColors.error,
                ),
                title: Text(
                  AppLocalizations.of(context)!.disconnectLabel,
                  style: TextStyle(
                    color: DesignColors.error,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                subtitle: Text(
                  AppLocalizations.of(context)!.disconnectDesc,
                  style: TextStyle(color: mutedTextColor, fontSize: 12),
                ),
                onTap: () {
                  Navigator.pop(context);
                  _showDisconnectConfirmation();
                },
              ),
              const SizedBox(height: 16),
            ],
            ),
          ),
        );
      },
    ).then((_) {
      _scrollToBottomKey.currentState?.show();
    });
  }

  /// ウィンドウ閉じる確認ダイアログを表示
  void _confirmAndKillWindow({
    required String sessionName,
    required int windowIndex,
    required String windowName,
    required bool isLastWindow,
  }) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    showDialog(
      context: context,
      builder: (dialogContext) {
        return AlertDialog(
          backgroundColor: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
          title: Text(
            AppLocalizations.of(context)!.closeWindowTitle,
            style: TextStyle(color: isDark ? Colors.white : Colors.black87),
          ),
          content: Text(
            isLastWindow
                ? AppLocalizations.of(context)!.closeLastWindowWarning
                : AppLocalizations.of(context)!.closeWindowConfirm(windowName),
            style: TextStyle(color: isDark ? Colors.white70 : Colors.black54),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(dialogContext),
              child: Text(
                AppLocalizations.of(context)!.buttonCancel,
                style: TextStyle(color: isDark ? Colors.white60 : Colors.black54),
              ),
            ),
            ElevatedButton(
              onPressed: () {
                Navigator.pop(dialogContext);
                final wasActive = windowIndex == ref.read(tmuxProvider(widget.connectionId)).activeWindowIndex;
                _killWindow(
                  sessionName: sessionName,
                  windowIndex: windowIndex,
                  wasActiveWindow: wasActive,
                );
              },
              style: ElevatedButton.styleFrom(
                backgroundColor: DesignColors.error,
                foregroundColor: Colors.white,
              ),
              child: Text(AppLocalizations.of(context)!.buttonClose),
            ),
          ],
        );
      },
    );
  }

  /// ウィンドウを閉じる
  Future<void> _killWindow({
    required String sessionName,
    required int windowIndex,
    required bool wasActiveWindow,
  }) async {
    final sshClient = ref.read(sshProvider(widget.connectionId).notifier).client;
    if (sshClient == null || !sshClient.isConnected) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(AppLocalizations.of(context)!.sshNotAvailable)),
        );
      }
      return;
    }

    try {
      debugPrint('[Terminal] Killing window: $sessionName:$windowIndex');
      await sshClient.exec(TmuxCommands.killWindow(sessionName, windowIndex));
      await _refreshSessionTree();

      if (!mounted || _isDisposed) return;

      // セッション消滅判定: list-sessionsで直接確認
      final sessionsOutput = await sshClient.exec('tmux list-sessions 2>/dev/null || true');
      if (sessionsOutput.trim().isEmpty) {
        debugPrint('[Terminal] Last window closed, session terminated. Disconnecting...');
        await _disconnect();
        return;
      }

      // アクティブウィンドウを閉じた場合、tmuxが自動選択した新ウィンドウに同期
      if (wasActiveWindow) {
        final newTmuxState = ref.read(tmuxProvider(widget.connectionId));
        final newSession = newTmuxState.activeSession;
        if (newSession != null) {
          final newActiveWindow = newSession.windows.where((w) => w.active).firstOrNull
              ?? newSession.windows.firstOrNull;
          if (newActiveWindow != null) {
            await _selectWindow(newSession.name, newActiveWindow.index);
          }
        }
      }
    } catch (e) {
      debugPrint('[Terminal] Failed to kill window: $e');
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(AppLocalizations.of(context)!.closeWindowFailed(e.toString()))),
        );
      }
    }
  }

  /// 切断確認ダイアログを表示
  void _showDisconnectConfirmation() {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    showDialog(
      context: context,
      builder: (context) {
        return AlertDialog(
          backgroundColor: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
          title: Text(
            AppLocalizations.of(this.context)!.disconnectTitle,
            style: TextStyle(color: isDark ? Colors.white : Colors.black87),
          ),
          content: Text(
            AppLocalizations.of(this.context)!.disconnectConfirm,
            style: TextStyle(color: isDark ? Colors.white70 : Colors.black54),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context),
              child: Text(
                AppLocalizations.of(this.context)!.buttonCancel,
                style: TextStyle(color: isDark ? Colors.white60 : Colors.black54),
              ),
            ),
            ElevatedButton(
              onPressed: () async {
                Navigator.pop(context);
                await _disconnect();
              },
              style: ElevatedButton.styleFrom(
                backgroundColor: DesignColors.error,
                foregroundColor: Colors.white,
              ),
              child: Text(AppLocalizations.of(this.context)!.buttonDisconnect),
            ),
          ],
        );
      },
    );
  }

  /// SSH接続を切断して前の画面に戻る
  Future<void> _disconnect() async {
    // ポーリングを停止
    _pollTimer?.cancel();
    _treeRefreshTimer?.cancel();

    // SSH切断
    await ref.read(sshProvider(widget.connectionId).notifier).disconnect();

    // 前の画面に戻る
    if (mounted) {
      Navigator.pop(context);
    }
  }

  /// 接続状態インジケーター（レイテンシまたは再接続状態を表示）
  ///
  /// Tap = manual reconnect fallback. Users reported cases where the
  /// screen goes stale after long idle — socket stays "connected" but
  /// no data flows — and tapping the indicator now forces a reconnect
  /// via [SshNotifier.reconnectNow], bypassing the backoff schedule.
  Widget _buildConnectionIndicator(int latency) {
    final colorScheme = Theme.of(context).colorScheme;
    return GestureDetector(
      behavior: HitTestBehavior.opaque,
      onTap: _manualReconnect,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 8),
        decoration: BoxDecoration(
          border: Border(
            left: BorderSide(color: colorScheme.outline, width: 1),
          ),
        ),
        child: _sshState.isReconnecting
            ? _buildReconnectingIndicator()
            : _buildLatencyIndicator(latency),
      ),
    );
  }

  /// Force an immediate reconnect. Wired to the latency-indicator tap
  /// so users have a one-tap fallback when the session looks stale
  /// but the auto-reconnect machinery hasn't fired yet.
  void _manualReconnect() {
    if (_isDisposed) return;
    HapticFeedback.mediumImpact();
    final sshNotifier = ref.read(sshProvider(widget.connectionId).notifier);
    final sshState = ref.read(sshProvider(widget.connectionId));
    // Already reconnecting — don't stack another attempt, but give the
    // user visible confirmation the tap registered.
    if (sshState.isReconnecting) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Reconnect already in progress…'),
          duration: Duration(seconds: 2),
        ),
      );
      return;
    }
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(
        content: Text('Reconnecting…'),
        duration: Duration(seconds: 2),
      ),
    );
    sshNotifier.reconnectNow();
  }

  /// レイテンシ表示
  Widget _buildLatencyIndicator(int latency) {
    // Stale connection: no fresh poll for >8s. Visually degrade to
    // grey + "?" so users know the latency number is not live data.
    if (_isConnectionStale) {
      return Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(
            Icons.sync_problem,
            size: 12,
            color: DesignColors.warning.withValues(alpha: 0.9),
          ),
          const SizedBox(width: 4),
          Text(
            'stale',
            style: GoogleFonts.jetBrainsMono(
              fontSize: 10,
              color: DesignColors.warning.withValues(alpha: 0.9),
            ),
          ),
        ],
      );
    }
    // レイテンシに応じた色を決定
    Color indicatorColor;
    if (latency < 100) {
      indicatorColor = DesignColors.success; // 緑: 良好
    } else if (latency < 300) {
      indicatorColor = DesignColors.primary; // シアン: 普通
    } else if (latency < 500) {
      indicatorColor = DesignColors.warning; // オレンジ: やや遅い
    } else {
      indicatorColor = DesignColors.error; // 赤: 遅い
    }

    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(
          Icons.bolt,
          size: 10,
          color: indicatorColor.withValues(alpha: 0.8),
        ),
        const SizedBox(width: 4),
        Text(
          '${latency}ms',
          style: GoogleFonts.jetBrainsMono(
            fontSize: 10,
            color: indicatorColor.withValues(alpha: 0.8),
          ),
        ),
      ],
    );
  }

  /// 再接続中インジケーター
  Widget _buildReconnectingIndicator() {
    final attempt = _sshState.reconnectAttempt;
    final isWaitingForNetwork = _sshState.isWaitingForNetwork;
    final nextRetryAt = _sshState.nextRetryAt;
    final queuedCount = _inputQueue.length;

    // 次回リトライまでの秒数を計算
    String? countdownText;
    if (nextRetryAt != null && !isWaitingForNetwork) {
      final remaining = nextRetryAt.difference(DateTime.now()).inSeconds;
      if (remaining > 0) {
        countdownText = '${remaining}s';
      }
    }

    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        // スピナーまたは圏外アイコン
        if (isWaitingForNetwork)
          Icon(
            Icons.signal_wifi_off,
            size: 12,
            color: DesignColors.warning.withValues(alpha: 0.8),
          )
        else
          SizedBox(
            width: 10,
            height: 10,
            child: CircularProgressIndicator(
              strokeWidth: 1.5,
              color: DesignColors.warning.withValues(alpha: 0.8),
            ),
          ),
        const SizedBox(width: 6),

        // ステータステキスト
        Text(
          isWaitingForNetwork
              ? 'Offline'
              : 'Reconnecting${attempt > 1 ? ' ($attempt)' : ''}',
          style: GoogleFonts.jetBrainsMono(
            fontSize: 10,
            color: DesignColors.warning.withValues(alpha: 0.8),
          ),
        ),

        // カウントダウン
        if (countdownText != null) ...[
          const SizedBox(width: 4),
          Text(
            countdownText,
            style: GoogleFonts.jetBrainsMono(
              fontSize: 9,
              color: DesignColors.textMuted,
            ),
          ),
        ],

        // キューイング状態
        if (queuedCount > 0) ...[
          const SizedBox(width: 8),
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 1),
            decoration: BoxDecoration(
              color: DesignColors.primary.withValues(alpha: 0.2),
              borderRadius: BorderRadius.circular(4),
            ),
            child: Text(
              '$queuedCount chars',
              style: GoogleFonts.jetBrainsMono(
                fontSize: 9,
                color: DesignColors.primary,
              ),
            ),
          ),
        ],

        // 今すぐ再接続ボタン
        const SizedBox(width: 8),
        GestureDetector(
          onTap: () {
            ref.read(sshProvider(widget.connectionId).notifier).reconnectNow();
          },
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
            decoration: BoxDecoration(
              border: Border.all(
                color: DesignColors.warning.withValues(alpha: 0.5),
              ),
              borderRadius: BorderRadius.circular(4),
            ),
            child: Text(
              AppLocalizations.of(context)!.buttonRetry,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 9,
                color: DesignColors.warning,
              ),
            ),
          ),
        ),
      ],
    );
  }

  /// tmux send-keysでキーを送信
  ///
  /// [key] 送信するキー
  /// [literal] trueの場合はリテラル送信（-l フラグ）
  Future<void> _sendKey(String key, {bool literal = true}) async {
    if (_backend != null) {
      try {
        if (literal) {
          await _backend!.sendText(key);
        } else {
          await _backend!.sendSpecialKey(key);
        }
        _backend!.boostRefresh();
      } catch (_) {}
      return;
    }

    // Fallback: direct tmux path (used during init before backend is ready)
    final sshClient = ref.read(sshProvider(widget.connectionId).notifier).client;

    if (sshClient == null || !sshClient.isConnected) {
      if (literal) {
        _inputQueue.enqueue(key);
        if (mounted) setState(() {});
      }
      return;
    }

    final target = ref.read(tmuxProvider(widget.connectionId).notifier).currentTarget;
    if (target == null) return;

    try {
      await sshClient.exec(TmuxCommands.sendKeys(target, key, literal: literal));
      _boostPolling();
    } catch (_) {}
  }

  /// Enter tmux copy-mode (tmux backend only).
  Future<void> _enterTmuxCopyMode() async {
    final backend = _backend;
    if (backend is TmuxBackend) {
      await backend.enterCopyMode();
      return;
    }
    // Fallback
    final sshClient = ref.read(sshProvider(widget.connectionId).notifier).client;
    if (sshClient == null || !sshClient.isConnected) return;
    final target = ref.read(tmuxProvider(widget.connectionId).notifier).currentTarget;
    if (target == null) return;
    try {
      await sshClient.exec(TmuxCommands.enterCopyMode(target));
      _boostPolling();
    } catch (_) {}
  }

  /// Cancel tmux copy-mode (tmux backend only).
  Future<void> _cancelTmuxCopyMode() async {
    final backend = _backend;
    if (backend is TmuxBackend) {
      await backend.cancelCopyMode();
      return;
    }
    // Fallback
    final sshClient = ref.read(sshProvider(widget.connectionId).notifier).client;
    if (sshClient == null || !sshClient.isConnected) return;
    final target = ref.read(tmuxProvider(widget.connectionId).notifier).currentTarget;
    if (target == null) return;
    try {
      await sshClient.exec(TmuxCommands.cancelCopyMode(target));
      _boostPolling();
    } catch (_) {}
  }

  /// Toggle gesture surface mode on/off.
  void _toggleGestureMode() {
    setState(() {
      _gestureModeActive = !_gestureModeActive;
      if (_gestureModeActive) {
        // Exit scroll mode if active (mutually exclusive)
        if (_terminalMode == TerminalMode.scroll) {
          _terminalMode = TerminalMode.normal;
          _scrollModeSource = ScrollModeSource.none;
          _cancelTmuxCopyMode();
          _applyBufferedUpdate();
        }
      }
    });
  }

  /// Deactivate gesture surface mode.
  void _deactivateGestureMode() {
    if (_gestureModeActive) {
      setState(() => _gestureModeActive = false);
    }
  }

  /// Send a special key (Ctrl+C, Escape, arrows, etc.) via backend.
  Future<void> _sendSpecialKey(String tmuxKey) async {
    if (_backend != null) {
      try {
        await _backend!.sendSpecialKey(tmuxKey);
        _backend!.boostRefresh();
      } catch (_) {}
      return;
    }

    // Fallback: direct tmux path
    final sshClient = ref.read(sshProvider(widget.connectionId).notifier).client;
    if (sshClient == null || !sshClient.isConnected) return;
    final target = ref.read(tmuxProvider(widget.connectionId).notifier).currentTarget;
    if (target == null) return;
    try {
      await sshClient.exec(TmuxCommands.sendKeys(target, tmuxKey, literal: false));
      _boostPolling();
    } catch (_) {}
  }

  ProviderSubscription? _imageTransferSub;

  /// 画像転送の状態リスナーを初期化（1回のみ）
  void _ensureImageTransferListener() {
    if (_imageTransferSub != null) return;
    _imageTransferSub = ref.listenManual(imageTransferProvider(widget.connectionId), (prev, next) async {

      if (next.phase == ImageTransferPhase.confirming &&
          next.pickedImageBytes != null &&
          next.pendingRemotePath != null &&
          (prev?.phase == ImageTransferPhase.picking)) {
        if (!mounted) return;
        final settings = ref.read(settingsProvider);
        final options = await ImageTransferConfirmDialog.show(
          context,
          remotePath: next.pendingRemotePath!,
          imageBytes: next.pickedImageBytes!,
          imageName: next.pickedImageName,
          settings: settings,
        );

        if (options != null) {
          final uploadedPath = await ref
              .read(imageTransferProvider(widget.connectionId).notifier)
              .confirmAndUpload(options: options);

          if (uploadedPath != null && mounted) {
            await _injectImagePath(uploadedPath, options);
          }
        } else {
          ref.read(imageTransferProvider(widget.connectionId).notifier).cancel();
        }
      }

      if (next.phase == ImageTransferPhase.error && mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(next.errorMessage ?? 'Image transfer failed'),
            backgroundColor: Colors.redAccent,
          ),
        );
      }

      if (next.phase == ImageTransferPhase.completed &&
          next.lastUploadedPath != null &&
          mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(AppLocalizations.of(context)!.imageUploaded(next.lastUploadedPath!)),
            backgroundColor: Colors.green,
          ),
        );
      }
    });
  }

  /// 画像転送フローを開始
  void _handleImageTransfer() {
    _ensureImageTransferListener();

    showModalBottomSheet(
      context: context,
      backgroundColor: Theme.of(context).colorScheme.surface,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              leading: const Icon(Icons.photo_library),
              title: Text(AppLocalizations.of(context)!.imageSourceGallery),
              onTap: () {
                Navigator.pop(ctx);
                ref.read(imageTransferProvider(widget.connectionId).notifier).pickImage(
                      ImageSource.gallery,
                    );
              },
            ),
            ListTile(
              leading: const Icon(Icons.camera_alt),
              title: Text(AppLocalizations.of(context)!.imageSourceCamera),
              onTap: () {
                Navigator.pop(ctx);
                ref.read(imageTransferProvider(widget.connectionId).notifier).pickImage(
                      ImageSource.camera,
                    );
              },
            ),
          ],
        ),
      ),
    );
  }

  /// アップロード済み画像のパスをターミナルに注入
  Future<void> _injectImagePath(String remotePath, ImageTransferOptions options) async {
    final sshClient = ref.read(sshProvider(widget.connectionId).notifier).client;
    if (sshClient == null || !sshClient.isConnected) return;

    final activePaneId = ref.read(tmuxProvider(widget.connectionId)).activePaneId;
    if (activePaneId == null) return;

    // パスフォーマット適用（optionsから取得）
    final formattedPath = options.pathFormat.replaceAll('{path}', remotePath);

    if (options.bracketedPaste) {
      sshClient.write('\x1b[200~$formattedPath\x1b[201~');
    } else {
      await sshClient.exec(
        TmuxCommands.sendKeys(activePaneId, formattedPath, literal: true),
      );
    }

    if (options.autoEnter) {
      await sshClient.exec(
        TmuxCommands.sendKeys(activePaneId, 'Enter'),
      );
    }

    _boostPolling();
  }

  ProviderSubscription? _fileTransferSub;

  /// Initialize file transfer state listener (once)
  void _ensureFileTransferListener() {
    if (_fileTransferSub != null) return;
    _fileTransferSub = ref.listenManual(fileTransferProvider(widget.connectionId), (prev, next) async {
      if (next.phase == FileTransferPhase.confirming &&
          next.pickedFiles != null &&
          next.pendingRemoteDir != null &&
          (prev?.phase == FileTransferPhase.picking)) {
        if (!mounted) return;
        final settings = ref.read(settingsProvider);
        final options = await FileTransferConfirmDialog.show(
          context,
          files: next.pickedFiles!,
          remoteDir: next.pendingRemoteDir!,
          settings: settings,
        );

        if (options != null) {
          final uploadedPaths = await ref
              .read(fileTransferProvider(widget.connectionId).notifier)
              .confirmAndUpload(options: options);

          if (uploadedPaths != null && uploadedPaths.isNotEmpty && mounted) {
            await _injectFilePaths(uploadedPaths, options);
          }
        } else {
          ref.read(fileTransferProvider(widget.connectionId).notifier).cancel();
        }
      }

      if (next.phase == FileTransferPhase.error && mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(next.errorMessage ?? 'File transfer failed'),
            backgroundColor: Colors.redAccent,
          ),
        );
      }

      if (next.phase == FileTransferPhase.completed &&
          next.lastUploadedPaths != null &&
          mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(AppLocalizations.of(context)!.fileUploaded(next.lastUploadedPaths!.length)),
            backgroundColor: Colors.green,
          ),
        );
      }
    });
  }

  /// Start file transfer flow
  void _handleFileTransfer() async {
    _ensureFileTransferListener();
    // Prefer the backend's current working directory; falls back inside
    // pickFiles() to settings.fileRemotePath when null.
    final cwd = await _backend?.getCurrentPath();
    if (!mounted) return;
    ref
        .read(fileTransferProvider(widget.connectionId).notifier)
        .pickFiles(initialRemoteDir: cwd);
  }

  /// Inject uploaded file paths into terminal
  Future<void> _injectFilePaths(List<String> remotePaths, FileTransferOptions options) async {
    final sshClient = ref.read(sshProvider(widget.connectionId).notifier).client;
    if (sshClient == null || !sshClient.isConnected) return;

    final activePaneId = ref.read(tmuxProvider(widget.connectionId)).activePaneId;
    if (activePaneId == null) return;

    // Join all paths with space separator
    final joinedPaths = remotePaths
        .map((p) => options.pathFormat.replaceAll('{path}', p))
        .join(' ');

    if (options.bracketedPaste) {
      sshClient.write('\x1b[200~$joinedPaths\x1b[201~');
    } else {
      await sshClient.exec(
        TmuxCommands.sendKeys(activePaneId, joinedPaths, literal: true),
      );
    }

    if (options.autoEnter) {
      await sshClient.exec(
        TmuxCommands.sendKeys(activePaneId, 'Enter'),
      );
    }

    _boostPolling();
  }

  /// Start file download flow
  void _handleFileDownload() async {
    _ensureFileTransferListener();

    final settings = ref.read(settingsProvider);
    // Prefer the backend's current working directory; fall back to the
    // user's static setting when the backend can't determine it.
    final cwd = await _backend?.getCurrentPath();
    if (!mounted) return;
    final initialPath =
        (cwd != null && cwd.isNotEmpty) ? cwd : settings.fileRemotePath;

    RemoteFileBrowserDialog.show(
      context,
      initialPath: initialPath,
      onListDir: (path) =>
          ref.read(fileTransferProvider(widget.connectionId).notifier).browseRemote(path),
    ).then((selectedPath) async {
      if (selectedPath == null || !mounted) return;

      final localPath = await ref
          .read(fileTransferProvider(widget.connectionId).notifier)
          .downloadFile(selectedPath);

      if (localPath != null && mounted) {
        // Share the downloaded file via Android share intent
        await Share.shareXFiles([XFile(localPath)]);
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(
              content: Text(AppLocalizations.of(context)!.fileDownloaded),
              backgroundColor: Colors.green,
            ),
          );
        }
      }
    });
  }

  /// Send multiline text without appending a final Enter
  Future<void> _sendMultilineTextNoEnter(String text) async {
    final lines = text.split('\n');
    for (int i = 0; i < lines.length; i++) {
      final line = lines[i];
      if (line.isNotEmpty) {
        await _sendKey(line);
      }
      // Send Enter between lines, but NOT after the last line
      if (i < lines.length - 1) {
        await _sendSpecialKey('Enter');
      }
    }
  }

  /// Show the [+] insert menu
  void _showInsertMenu(BuildContext context) {
    InsertMenu.show(
      context,
      ref: ref,
      onFileTransfer: _handleFileTransfer,
      onFileDownload: _handleFileDownload,
      onImageTransfer: _handleImageTransfer,
      onDirectInput: () {
        ref.read(actionBarProvider.notifier).toggleInputMode();
      },
    );
  }

  /// Show the snippet picker sheet. [panelKey] scopes preset snippet
  /// selection to the current pane's profile — null falls back to the
  /// global default.
  void _showSnippetPicker(BuildContext context, {String? panelKey}) {
    SnippetPickerSheet.show(
      context,
      ref: ref,
      panelKey: panelKey,
      onInsert: (content) {
        _composeBarKey.currentState?.insertText(content);
      },
      onSendImmediately: (content) {
        _sendMultilineText(content);
        _boostPolling();
      },
    );
  }

  /// Show the Key Palette sheet (formerly the profile selection sheet).
  ///
  /// The sheet exposes the full key palette of the active profile plus
  /// profile switching. Key taps route through the same overlay-wrapped
  /// callbacks as the action bar. [panelKey] scopes profile switching
  /// to the current pane — when null, the sheet falls back to the
  /// global default.
  void _showProfileSheet(BuildContext context, {String? panelKey}) {
    ProfileSheet.show(
      context,
      ref: ref,
      panelKey: panelKey,
      onKeyTap: _dispatchKey,
      onSpecialKeyTap: _dispatchSpecialKey,
      onModifierTap: (modifier) {
        if (modifier == 'ctrl') {
          ref.read(actionBarProvider.notifier).toggleCtrl();
        } else if (modifier == 'alt') {
          ref.read(actionBarProvider.notifier).toggleAlt();
        }
      },
      // Route action-type palette chips to the same handlers the action
      // bar uses, so tapping ⚡ Snippets in the palette opens the picker,
      // tapping file transfer opens the sheet, etc. Without this the
      // action chips in the palette would be no-ops.
      onActionTap: (actionValue) {
        switch (actionValue) {
          case 'file_transfer':
            _handleFileTransfer();
          case 'image_transfer':
            _handleImageTransfer();
          case 'snippet':
            _showSnippetPicker(context, panelKey: panelKey);
          case 'direct_input':
            ref.read(actionBarProvider.notifier).toggleInputMode();
        }
      },
    );
  }

  /// 複数行テキストを送信（行ごとにテキスト+Enterを送信）
  ///
  /// 注: _sendKey/_sendSpecialKeyを直接呼び出す。
  /// オーバーレイラッパーを経由しないため、複数行送信時にオーバーレイは表示されない。
  /// これは意図的な動作。
  Future<void> _sendMultilineText(String text) async {
    final lines = text.split('\n');
    for (int i = 0; i < lines.length; i++) {
      final line = lines[i];
      if (line.isNotEmpty) {
        await _sendKey(line);
      }
      // 最後の行以外はEnterを送信、または空行でもEnterを送信
      if (i < lines.length - 1 || line.isEmpty) {
        await _sendSpecialKey('Enter');
      }
    }
    // 最後の行が空でなければEnterを送信
    if (lines.isNotEmpty && lines.last.isNotEmpty) {
      await _sendSpecialKey('Enter');
    }
  }

  /// 右上のペインインジケーター
  ///
  /// ペインの実際のサイズ比率に基づいてレイアウトを表示
  Widget _buildPaneIndicator(TmuxState tmuxState) {
    final window = tmuxState.activeWindow;
    final panes = window?.panes ?? [];
    final activePaneId = tmuxState.activePaneId;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final colorScheme = Theme.of(context).colorScheme;

    if (panes.isEmpty) {
      return const SizedBox.shrink();
    }

    // インジケーター全体のサイズ
    const double indicatorSize = 48.0;

    return GestureDetector(
      onTap: () => _showPaneSelector(tmuxState),
      child: Opacity(
        opacity: 0.5,
        child: Container(
          width: indicatorSize,
          height: indicatorSize,
          padding: const EdgeInsets.all(2),
          decoration: BoxDecoration(
            color: isDark ? Colors.black26 : Colors.black12,
            borderRadius: BorderRadius.circular(4),
          ),
          child: CustomPaint(
            size: Size(indicatorSize - 4, indicatorSize - 4),
            painter: _PaneLayoutPainter(
              panes: panes,
              activePaneId: activePaneId,
              activeColor: colorScheme.primary,
              isDark: isDark,
            ),
          ),
        ),
      ),
    );
  }
}

/// ペインレイアウトを描画するCustomPainter
///
/// tmuxから取得したpane_left/pane_topを使用して
/// 実際のレイアウトを正確に再現する
class _PaneLayoutPainter extends CustomPainter {
  final List<TmuxPane> panes;
  final String? activePaneId;
  final Color activeColor;
  final bool isDark;

  _PaneLayoutPainter({
    required this.panes,
    this.activePaneId,
    required this.activeColor,
    required this.isDark,
  });

  @override
  void paint(Canvas canvas, Size size) {
    if (panes.isEmpty) return;

    // ウィンドウ全体のサイズを計算（全ペインを含む範囲）
    int maxRight = 0;
    int maxBottom = 0;
    for (final pane in panes) {
      final right = pane.left + pane.width;
      final bottom = pane.top + pane.height;
      if (right > maxRight) maxRight = right;
      if (bottom > maxBottom) maxBottom = bottom;
    }

    if (maxRight == 0 || maxBottom == 0) return;

    // スケール係数を計算
    final scaleX = size.width / maxRight;
    final scaleY = size.height / maxBottom;
    final gap = 1.0;

    // ペインごとに描画
    for (final pane in panes) {
      final isActive = pane.id == activePaneId;

      // 実際の位置とサイズからRectを計算
      final left = pane.left * scaleX;
      final top = pane.top * scaleY;
      final width = pane.width * scaleX - gap;
      final height = pane.height * scaleY - gap;

      final rect = Rect.fromLTWH(left, top, width, height);

      // 背景
      final bgPaint = Paint()
        ..color = isActive
            ? activeColor.withValues(alpha: 0.3)
            : (isDark ? Colors.black45 : Colors.grey.shade300);
      canvas.drawRect(rect, bgPaint);

      // 枠線
      final borderPaint = Paint()
        ..color = isActive ? activeColor : (isDark ? Colors.white30 : Colors.grey.shade500)
        ..style = PaintingStyle.stroke
        ..strokeWidth = isActive ? 1.5 : 1.0;
      canvas.drawRect(rect, borderPaint);
    }
  }

  @override
  bool shouldRepaint(covariant _PaneLayoutPainter oldDelegate) {
    return panes != oldDelegate.panes ||
        activePaneId != oldDelegate.activePaneId ||
        activeColor != oldDelegate.activeColor ||
        isDark != oldDelegate.isDark;
  }
}

/// ペインレイアウトをインタラクティブに表示するウィジェット
///
/// 各ペインをタップで選択可能。ペイン番号も表示。
class _PaneLayoutVisualizer extends StatefulWidget {
  final List<TmuxPane> panes;
  final String? activePaneId;
  final void Function(String paneId) onPaneSelected;
  final void Function(String paneId, SplitDirection direction)? onSplitRequested;

  const _PaneLayoutVisualizer({
    required this.panes,
    this.activePaneId,
    required this.onPaneSelected,
    this.onSplitRequested,
  });

  @override
  State<_PaneLayoutVisualizer> createState() => _PaneLayoutVisualizerState();
}

class _PaneLayoutVisualizerState extends State<_PaneLayoutVisualizer> {
  /// 分割モードが有効なペインID（nullなら通常表示）
  String? _splitModeActivePaneId;

  @override
  Widget build(BuildContext context) {
    if (widget.panes.isEmpty) return const SizedBox.shrink();

    // ウィンドウ全体のサイズを計算（全ペインを含む範囲）
    int maxRight = 0;
    int maxBottom = 0;
    for (final pane in widget.panes) {
      final right = pane.left + pane.width;
      final bottom = pane.top + pane.height;
      if (right > maxRight) maxRight = right;
      if (bottom > maxBottom) maxBottom = bottom;
    }

    if (maxRight == 0 || maxBottom == 0) return const SizedBox.shrink();

    // アスペクト比を計算
    final aspectRatio = maxRight / maxBottom;

    return Container(
      padding: const EdgeInsets.all(16),
      child: AspectRatio(
        aspectRatio: aspectRatio.clamp(0.5, 3.0),
        child: LayoutBuilder(
          builder: (context, constraints) {
            final containerWidth = constraints.maxWidth;
            final containerHeight = constraints.maxHeight;

            // スケール係数を計算
            final scaleX = containerWidth / maxRight;
            final scaleY = containerHeight / maxBottom;
            const gap = 2.0;

            return Stack(
              children: widget.panes.map((pane) {
                final isActive = pane.id == widget.activePaneId;
                final isSplitMode = _splitModeActivePaneId == pane.id;

                // 実際の位置とサイズからRectを計算
                final left = pane.left * scaleX;
                final top = pane.top * scaleY;
                final width = pane.width * scaleX - gap;
                final height = pane.height * scaleY - gap;

                return Positioned(
                  left: left,
                  top: top,
                  width: width,
                  height: height,
                  child: GestureDetector(
                    onTap: () => _handlePaneTap(pane, isActive, width, height),
                    child: AnimatedContainer(
                      duration: const Duration(milliseconds: 200),
                      decoration: BoxDecoration(
                        color: isActive
                            ? DesignColors.primary.withValues(alpha: 0.3)
                            : Colors.black45,
                        borderRadius: BorderRadius.circular(4),
                        border: Border.all(
                          color: isActive
                              ? DesignColors.primary
                              : Colors.white.withValues(alpha: 0.3),
                          width: isActive ? 2 : 1,
                        ),
                      ),
                      child: Center(
                        child: _buildPaneContent(
                          pane: pane,
                          isActive: isActive,
                          isSplitMode: isSplitMode,
                          width: width,
                          height: height,
                        ),
                      ),
                    ),
                  ),
                );
              }).toList(),
            );
          },
        ),
      ),
    );
  }

  /// インライン分割アイコンが収まる最小サイズ
  static const _minInlineWidth = 80.0;
  static const _minInlineHeight = 60.0;

  void _handlePaneTap(TmuxPane pane, bool isActive, double width, double height) {
    if (isActive && widget.onSplitRequested != null) {
      if (width < _minInlineWidth || height < _minInlineHeight) {
        // 小さいペイン → モーダルダイアログで分割方向を選択
        _showSplitDialog(pane);
      } else {
        // 大きいペイン → インラインで分割モード切り替え
        setState(() {
          _splitModeActivePaneId =
              _splitModeActivePaneId == pane.id ? null : pane.id;
        });
      }
    } else {
      // 非アクティブペインをタップ → ペイン選択
      widget.onPaneSelected(pane.id);
    }
  }

  void _showSplitDialog(TmuxPane pane) {
    showDialog(
      context: context,
      builder: (dialogContext) {
        final colorScheme = Theme.of(dialogContext).colorScheme;
        return AlertDialog(
          title: Text(
            AppLocalizations.of(context)!.splitPaneTitle(pane.index),
            style: GoogleFonts.spaceGrotesk(
              fontWeight: FontWeight.w700,
            ),
          ),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              ListTile(
                leading: CustomPaint(
                  size: const Size(24, 24),
                  painter: _SplitRightIconPainter(color: colorScheme.primary),
                ),
                title: Text(AppLocalizations.of(context)!.splitRight),
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(8),
                ),
                onTap: () {
                  Navigator.pop(dialogContext);
                  widget.onSplitRequested!(pane.id, SplitDirection.horizontal);
                },
              ),
              ListTile(
                leading: CustomPaint(
                  size: const Size(24, 24),
                  painter: _SplitDownIconPainter(color: colorScheme.primary),
                ),
                title: Text(AppLocalizations.of(context)!.splitDown),
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(8),
                ),
                onTap: () {
                  Navigator.pop(dialogContext);
                  widget.onSplitRequested!(pane.id, SplitDirection.vertical);
                },
              ),
            ],
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(dialogContext),
              child: Text(AppLocalizations.of(context)!.buttonCancel),
            ),
          ],
        );
      },
    );
  }

  Widget _buildPaneContent({
    required TmuxPane pane,
    required bool isActive,
    required bool isSplitMode,
    required double width,
    required double height,
  }) {
    if (isActive && isSplitMode) {
      // 分割モード: アイコンボタン表示
      return Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Text(
            '${pane.index}',
            style: GoogleFonts.jetBrainsMono(
              fontSize: width > 60 ? 18 : 14,
              fontWeight: FontWeight.w700,
              color: DesignColors.primary,
            ),
          ),
          const SizedBox(height: 4),
          Row(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              _buildSplitButton(
                painter: _SplitRightIconPainter(color: DesignColors.primary),
                onTap: () => widget.onSplitRequested!(
                  pane.id,
                  SplitDirection.horizontal,
                ),
              ),
              const SizedBox(width: 8),
              _buildSplitButton(
                painter: _SplitDownIconPainter(color: DesignColors.primary),
                onTap: () => widget.onSplitRequested!(
                  pane.id,
                  SplitDirection.vertical,
                ),
              ),
            ],
          ),
        ],
      );
    }

    // 通常表示
    return Column(
      mainAxisAlignment: MainAxisAlignment.center,
      children: [
        Text(
          '${pane.index}',
          style: GoogleFonts.jetBrainsMono(
            fontSize: width > 60 ? 18 : 14,
            fontWeight: FontWeight.w700,
            color: isActive
                ? DesignColors.primary
                : Colors.white.withValues(alpha: 0.7),
          ),
        ),
        if (isActive && widget.onSplitRequested != null && width > 60 && height > 40) ...[
          const SizedBox(height: 2),
          Text(
            'Tap to split',
            style: GoogleFonts.jetBrainsMono(
              fontSize: 8,
              color: DesignColors.primary.withValues(alpha: 0.7),
            ),
          ),
        ] else if (width > 80 && height > 50) ...[
          const SizedBox(height: 2),
          Text(
            '${pane.width}x${pane.height}',
            style: GoogleFonts.jetBrainsMono(
              fontSize: 9,
              color: Colors.white.withValues(alpha: 0.5),
            ),
          ),
        ],
      ],
    );
  }

  Widget _buildSplitButton({
    required CustomPainter painter,
    required VoidCallback onTap,
  }) {
    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(6),
        child: Container(
          padding: const EdgeInsets.all(6),
          decoration: BoxDecoration(
            color: DesignColors.primary.withValues(alpha: 0.15),
            borderRadius: BorderRadius.circular(6),
            border: Border.all(
              color: DesignColors.primary.withValues(alpha: 0.4),
            ),
          ),
          child: CustomPaint(
            size: const Size(20, 20),
            painter: painter,
          ),
        ),
      ),
    );
  }
}

/// 右分割アイコン: 左に既存ペイン、右に新ペイン（+マーク付き）
class _SplitRightIconPainter extends CustomPainter {
  final Color color;

  _SplitRightIconPainter({required this.color});

  @override
  void paint(Canvas canvas, Size size) {
    final paint = Paint()
      ..color = color
      ..strokeWidth = 1.5
      ..style = PaintingStyle.stroke;

    final w = size.width;
    final h = size.height;
    final pad = w * 0.1;
    final mid = w * 0.5;

    // 外枠
    canvas.drawRRect(
      RRect.fromRectAndRadius(
        Rect.fromLTWH(pad, pad, w - pad * 2, h - pad * 2),
        const Radius.circular(2),
      ),
      paint,
    );

    // 分割線（中央縦線）
    canvas.drawLine(Offset(mid, pad), Offset(mid, h - pad), paint);

    // 右側に+マーク
    final plusPaint = Paint()
      ..color = color
      ..strokeWidth = 2.0
      ..strokeCap = StrokeCap.round;
    final cx = mid + (w - pad - mid) / 2;
    final cy = h / 2;
    final plusSize = w * 0.12;
    canvas.drawLine(Offset(cx - plusSize, cy), Offset(cx + plusSize, cy), plusPaint);
    canvas.drawLine(Offset(cx, cy - plusSize), Offset(cx, cy + plusSize), plusPaint);
  }

  @override
  bool shouldRepaint(covariant _SplitRightIconPainter oldDelegate) =>
      color != oldDelegate.color;
}

/// 下分割アイコン: 上に既存ペイン、下に新ペイン（+マーク付き）
class _SplitDownIconPainter extends CustomPainter {
  final Color color;

  _SplitDownIconPainter({required this.color});

  @override
  void paint(Canvas canvas, Size size) {
    final paint = Paint()
      ..color = color
      ..strokeWidth = 1.5
      ..style = PaintingStyle.stroke;

    final w = size.width;
    final h = size.height;
    final pad = w * 0.1;
    final mid = h * 0.5;

    // 外枠
    canvas.drawRRect(
      RRect.fromRectAndRadius(
        Rect.fromLTWH(pad, pad, w - pad * 2, h - pad * 2),
        const Radius.circular(2),
      ),
      paint,
    );

    // 分割線（中央横線）
    canvas.drawLine(Offset(pad, mid), Offset(w - pad, mid), paint);

    // 下側に+マーク
    final plusPaint = Paint()
      ..color = color
      ..strokeWidth = 2.0
      ..strokeCap = StrokeCap.round;
    final cx = w / 2;
    final cy = mid + (h - pad - mid) / 2;
    final plusSize = w * 0.12;
    canvas.drawLine(Offset(cx - plusSize, cy), Offset(cx + plusSize, cy), plusPaint);
    canvas.drawLine(Offset(cx, cy - plusSize), Offset(cx, cy + plusSize), plusPaint);
  }

  @override
  bool shouldRepaint(covariant _SplitDownIconPainter oldDelegate) =>
      color != oldDelegate.color;
}

/// ウィンドウ名入力ダイアログ
class _NewWindowDialog extends StatefulWidget {
  final List<String> existingWindowNames;

  const _NewWindowDialog({required this.existingWindowNames});

  @override
  State<_NewWindowDialog> createState() => _NewWindowDialogState();
}

class _NewWindowDialogState extends State<_NewWindowDialog> {
  final _formKey = GlobalKey<FormState>();
  final _nameController = TextEditingController();

  @override
  void dispose() {
    _nameController.dispose();
    super.dispose();
  }

  String? _validateWindowName(String? value) {
    if (value == null || value.isEmpty) {
      return null; // 空入力はtmuxデフォルト名で許容
    }
    if (value.length > 50) {
      return 'Window name must be 50 characters or less';
    }
    if (!RegExp(r'^[a-zA-Z0-9_-]+$').hasMatch(value)) {
      return 'Only letters, numbers, - and _ allowed';
    }
    if (widget.existingWindowNames.contains(value)) {
      return 'Window "$value" already exists';
    }
    return null;
  }

  void _submit() {
    if (_formKey.currentState!.validate()) {
      Navigator.pop(context, _nameController.text);
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final colorScheme = Theme.of(context).colorScheme;
    return AlertDialog(
      title: Text(
        AppLocalizations.of(context)!.newWindowTitle,
        style: GoogleFonts.spaceGrotesk(
          fontWeight: FontWeight.w700,
        ),
      ),
      content: Form(
        key: _formKey,
        child: TextFormField(
          controller: _nameController,
          autofocus: true,
          maxLength: 50,
          decoration: InputDecoration(
            labelText: AppLocalizations.of(context)!.newWindowTitle,
            hintText: AppLocalizations.of(context)!.newWindowHint,
            hintStyle: GoogleFonts.jetBrainsMono(
              fontSize: 14,
              color: isDark ? DesignColors.textMuted : DesignColors.textMutedLight,
            ),
            filled: true,
            fillColor: isDark ? DesignColors.inputDark : DesignColors.inputLight,
            border: OutlineInputBorder(
              borderRadius: BorderRadius.circular(12),
              borderSide: BorderSide.none,
            ),
            focusedBorder: OutlineInputBorder(
              borderRadius: BorderRadius.circular(12),
              borderSide: BorderSide(color: colorScheme.primary),
            ),
            errorBorder: OutlineInputBorder(
              borderRadius: BorderRadius.circular(12),
              borderSide: const BorderSide(color: DesignColors.error),
            ),
            focusedErrorBorder: OutlineInputBorder(
              borderRadius: BorderRadius.circular(12),
              borderSide: const BorderSide(color: DesignColors.error),
            ),
          ),
          style: GoogleFonts.jetBrainsMono(fontSize: 14),
          validator: _validateWindowName,
          onFieldSubmitted: (_) => _submit(),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: Text(AppLocalizations.of(context)!.buttonCancel),
        ),
        FilledButton(
          onPressed: _submit,
          child: Text(AppLocalizations.of(context)!.buttonCreate),
        ),
      ],
    );
  }
}

// ====================================================================
// _ResizePaneChooserDialog
// ====================================================================

/// リサイズ対象ペインをグラフィカルに選択するダイアログ
class _ResizePaneChooserDialog extends StatefulWidget {
  final List<TmuxPane> panes;
  final String? activePaneId;
  final void Function(TmuxPane selectedPane) onResize;

  const _ResizePaneChooserDialog({
    required this.panes,
    this.activePaneId,
    required this.onResize,
  });

  @override
  State<_ResizePaneChooserDialog> createState() =>
      _ResizePaneChooserDialogState();
}

class _ResizePaneChooserDialogState extends State<_ResizePaneChooserDialog> {
  late String? _selectedPaneId;

  @override
  void initState() {
    super.initState();
    // デフォルト: 現在アクティブなペインが選択状態
    _selectedPaneId = widget.activePaneId;
  }

  TmuxPane? get _selectedPane {
    if (_selectedPaneId == null) return null;
    try {
      return widget.panes.firstWhere((p) => p.id == _selectedPaneId);
    } catch (_) {
      return null;
    }
  }

  @override
  Widget build(BuildContext context) {
    final selected = _selectedPane;

    return AlertDialog(
      backgroundColor: DesignColors.surfaceDark,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
      title: Text(
        AppLocalizations.of(context)!.resizePaneTitle,
        style: const TextStyle(color: DesignColors.textPrimary),
      ),
      content: SizedBox(
        width: MediaQuery.of(context).size.width * 0.8,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            _buildSelectablePaneGrid(),
            const SizedBox(height: 12),
            if (selected != null)
              Text(
                AppLocalizations.of(context)!.paneSelectionInfo(selected.index, selected.width, selected.height),
                style: const TextStyle(
                  fontSize: 13,
                  color: DesignColors.textSecondary,
                ),
              )
            else
              Text(
                AppLocalizations.of(context)!.selectWindowPrompt,
                style: const TextStyle(
                  fontSize: 13,
                  color: DesignColors.textSecondary,
                ),
              ),
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: Text(AppLocalizations.of(context)!.buttonCancel),
        ),
        FilledButton(
          onPressed: selected != null ? () => widget.onResize(selected) : null,
          style: FilledButton.styleFrom(
            backgroundColor: DesignColors.primary,
          ),
          child: Text(AppLocalizations.of(context)!.buttonResize),
        ),
      ],
    );
  }

  Widget _buildSelectablePaneGrid() {
    if (widget.panes.isEmpty) return const SizedBox.shrink();

    // ウィンドウ全体のサイズを計算
    int maxRight = 0;
    int maxBottom = 0;
    for (final pane in widget.panes) {
      final right = pane.left + pane.width;
      final bottom = pane.top + pane.height;
      if (right > maxRight) maxRight = right;
      if (bottom > maxBottom) maxBottom = bottom;
    }
    if (maxRight == 0) maxRight = 1;
    if (maxBottom == 0) maxBottom = 1;

    return Container(
      height: 150,
      clipBehavior: Clip.hardEdge,
      decoration: BoxDecoration(
        color: DesignColors.canvasDark,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: DesignColors.borderDark),
      ),
      child: LayoutBuilder(
          builder: (context, constraints) {
            const pad = 4.0;
            final areaW = constraints.maxWidth - pad * 2;
            final areaH = constraints.maxHeight - pad * 2;
            final scaleX = areaW / maxRight;
            final scaleY = areaH / maxBottom;

            return Padding(
              padding: const EdgeInsets.all(pad),
              child: Stack(
                children: [
                  SizedBox(width: areaW, height: areaH),
                  ...widget.panes.map((pane) {
                  final isSelected = pane.id == _selectedPaneId;
                  final left = pane.left * scaleX;
                  final top = pane.top * scaleY;
                  final width = (pane.width * scaleX).clamp(20.0, areaW - left);
                  final height = (pane.height * scaleY).clamp(14.0, areaH - top);

                  return Positioned(
                    left: left,
                    top: top,
                    width: width,
                    height: height,
                    child: GestureDetector(
                      onTap: () => setState(() => _selectedPaneId = pane.id),
                      child: AnimatedContainer(
                        duration: const Duration(milliseconds: 150),
                        decoration: BoxDecoration(
                          color: isSelected
                              ? DesignColors.primary.withValues(alpha: 0.25)
                              : DesignColors.surfaceDark,
                          borderRadius: BorderRadius.circular(4),
                          border: Border.all(
                            color: isSelected
                                ? DesignColors.primary
                                : DesignColors.borderDark,
                            width: isSelected ? 2 : 1,
                          ),
                        ),
                        alignment: Alignment.center,
                        child: FittedBox(
                          fit: BoxFit.scaleDown,
                          child: Padding(
                            padding: const EdgeInsets.symmetric(horizontal: 2),
                            child: Text(
                              '${pane.index}\n${pane.width}x${pane.height}',
                              textAlign: TextAlign.center,
                              style: TextStyle(
                                fontSize: 10,
                                color: isSelected
                                    ? DesignColors.primary
                                    : DesignColors.textSecondary,
                              ),
                            ),
                          ),
                        ),
                      ),
                    ),
                  );
                }),
              ]),
            );
          },
        ),
      );
  }
}

// ====================================================================
// _ResizeWindowChooserDialog
// ====================================================================

/// リサイズ対象ウィンドウをグラフィカルに選択するダイアログ
class _ResizeWindowChooserDialog extends StatefulWidget {
  final List<TmuxWindow> windows;
  final int? activeWindowIndex;
  final void Function(TmuxWindow selectedWindow) onResize;

  const _ResizeWindowChooserDialog({
    required this.windows,
    this.activeWindowIndex,
    required this.onResize,
  });

  @override
  State<_ResizeWindowChooserDialog> createState() =>
      _ResizeWindowChooserDialogState();
}

class _ResizeWindowChooserDialogState
    extends State<_ResizeWindowChooserDialog> {
  late int? _selectedWindowIndex;

  @override
  void initState() {
    super.initState();
    // デフォルト: 現在アクティブなウィンドウが選択状態
    _selectedWindowIndex = widget.activeWindowIndex;
  }

  TmuxWindow? get _selectedWindow {
    if (_selectedWindowIndex == null) return null;
    try {
      return widget.windows
          .firstWhere((w) => w.index == _selectedWindowIndex);
    } catch (_) {
      return null;
    }
  }

  @override
  Widget build(BuildContext context) {
    final selected = _selectedWindow;

    return AlertDialog(
      backgroundColor: DesignColors.surfaceDark,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
      title: Text(
        AppLocalizations.of(context)!.resizeWindowTitle,
        style: const TextStyle(color: DesignColors.textPrimary),
      ),
      content: SizedBox(
        width: MediaQuery.of(context).size.width * 0.8,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              // ウィンドウカード一覧
              ...widget.windows.map((window) {
                final isSelected = window.index == _selectedWindowIndex;
                return Padding(
                  padding: const EdgeInsets.only(bottom: 8),
                  child: _buildWindowCard(window, isSelected),
                );
              }),
              const SizedBox(height: 4),
              // 選択中のウィンドウ情報
              if (selected != null) ...[
                Text(
                  'Selected: ${selected.name} (${_windowSizeString(selected)})',
                  style: const TextStyle(
                    fontSize: 13,
                    color: DesignColors.textSecondary,
                  ),
                ),
              ] else
                Text(
                  AppLocalizations.of(context)!.selectWindowPrompt,
                  style: const TextStyle(
                    fontSize: 13,
                    color: DesignColors.textSecondary,
                  ),
                ),
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: Text(AppLocalizations.of(context)!.buttonCancel),
        ),
        FilledButton(
          onPressed:
              selected != null ? () => widget.onResize(selected) : null,
          style: FilledButton.styleFrom(
            backgroundColor: DesignColors.primary,
          ),
          child: Text(AppLocalizations.of(context)!.buttonResize),
        ),
      ],
    );
  }

  String _windowSizeString(TmuxWindow window) {
    final panes = window.panes;
    if (panes.isEmpty) return '?x?';
    final cols =
        panes.map((p) => p.left + p.width).reduce((a, b) => a > b ? a : b);
    final rows =
        panes.map((p) => p.top + p.height).reduce((a, b) => a > b ? a : b);
    return '${cols}x$rows';
  }

  Widget _buildWindowCard(TmuxWindow window, bool isSelected) {
    final panes = window.panes;
    return GestureDetector(
      onTap: () => setState(() => _selectedWindowIndex = window.index),
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 150),
        decoration: BoxDecoration(
          color: DesignColors.canvasDark,
          borderRadius: BorderRadius.circular(8),
          border: Border.all(
            color: isSelected ? DesignColors.primary : DesignColors.borderDark,
            width: isSelected ? 2 : 1,
          ),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // ウィンドウヘッダー
            Container(
              width: double.infinity,
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
              decoration: BoxDecoration(
                color: isSelected
                    ? DesignColors.primary.withValues(alpha: 0.15)
                    : DesignColors.surfaceDark,
                borderRadius: const BorderRadius.only(
                  topLeft: Radius.circular(7),
                  topRight: Radius.circular(7),
                ),
              ),
              child: Text(
                '${window.name}  ${_windowSizeString(window)}',
                style: TextStyle(
                  fontSize: 12,
                  color: isSelected
                      ? DesignColors.primary
                      : DesignColors.textPrimary,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
            // ペインレイアウトプレビュー
            if (panes.isNotEmpty)
              SizedBox(
                height: 60,
                child: _buildPaneLayoutPreview(panes),
              ),
          ],
        ),
      ),
    );
  }

  Widget _buildPaneLayoutPreview(List<TmuxPane> panes) {
    int maxRight = 0;
    int maxBottom = 0;
    for (final p in panes) {
      final right = p.left + p.width;
      final bottom = p.top + p.height;
      if (right > maxRight) maxRight = right;
      if (bottom > maxBottom) maxBottom = bottom;
    }
    if (maxRight == 0) maxRight = 1;
    if (maxBottom == 0) maxBottom = 1;

    return LayoutBuilder(
      builder: (context, constraints) {
        final areaW = constraints.maxWidth - 8;
        final areaH = constraints.maxHeight - 8;

        return Padding(
          padding: const EdgeInsets.all(4),
          child: Stack(
            children: [
              SizedBox(width: areaW, height: areaH),
              ...panes.map((pane) {
              final left = (pane.left / maxRight) * areaW;
              final top = (pane.top / maxBottom) * areaH;
              final width = (pane.width / maxRight) * areaW;
              final height = (pane.height / maxBottom) * areaH;

              return Positioned(
                left: left,
                top: top,
                width: width.clamp(16.0, areaW),
                height: height.clamp(10.0, areaH),
                child: Container(
                  decoration: BoxDecoration(
                    borderRadius: BorderRadius.circular(2),
                    border: Border.all(
                      color: DesignColors.borderDark,
                      width: 1,
                    ),
                  ),
                ),
              );
            }),
          ]),
        );
      },
    );
  }
}
