import 'dart:io';

import 'package:flutter_foreground_task/flutter_foreground_task.dart';

/// SSH接続をバックグラウンドで維持するためのForeground Serviceを管理
///
/// Refcounted by `connectionId` — once we keep SSH connections alive
/// across screen navigation (see `ssh_provider.dart` keep-alive link),
/// it's possible for two or more sockets to be in flight at once. A
/// global start-on-first / stop-on-any-disconnect model would let the
/// second SSH's disconnect yank the foreground service out from under
/// the first. We stop the service only when the last connection
/// releases its hold.
class SshForegroundTaskService {
  static final SshForegroundTaskService _instance =
      SshForegroundTaskService._internal();
  factory SshForegroundTaskService() => _instance;
  SshForegroundTaskService._internal();

  bool _isInitialized = false;
  bool _isRunning = false;
  String? _currentConnectionName;

  // Refcount of live SSH sockets that need the Android foreground
  // service to keep the process alive while backgrounded. Adds happen
  // in [startService]; removes in [stopService]. The service shuts
  // down only when this set drains.
  final Set<String> _activeConnectionIds = <String>{};

  /// サービスが実行中かどうか
  bool get isRunning => _isRunning;

  /// 現在接続中の接続名
  String? get currentConnectionName => _currentConnectionName;

  /// Foreground Taskを初期化
  Future<void> initialize() async {
    if (_isInitialized) return;
    if (!Platform.isAndroid) {
      _isInitialized = true;
      return;
    }

    FlutterForegroundTask.init(
      androidNotificationOptions: AndroidNotificationOptions(
        channelId: 'termipod_ssh_foreground',
        channelName: 'SSH Connection',
        channelDescription: 'Keeps SSH connection alive in background',
        channelImportance: NotificationChannelImportance.LOW,
        priority: NotificationPriority.LOW,
        playSound: false,
        visibility: NotificationVisibility.VISIBILITY_SECRET,
      ),
      iosNotificationOptions: const IOSNotificationOptions(
        showNotification: false,
        playSound: false,
      ),
      foregroundTaskOptions: ForegroundTaskOptions(
        eventAction: ForegroundTaskEventAction.nothing(),
        autoRunOnBoot: false,
        autoRunOnMyPackageReplaced: false,
        allowWakeLock: true,
        allowWifiLock: true,
      ),
    );

    _isInitialized = true;
  }

  /// 通知権限を要求
  Future<bool> requestPermissions() async {
    if (!Platform.isAndroid) return true;

    // Android 13以降は通知権限が必要
    final notificationPermission =
        await FlutterForegroundTask.checkNotificationPermission();
    if (notificationPermission != NotificationPermission.granted) {
      await FlutterForegroundTask.requestNotificationPermission();
    }

    // バッテリー最適化の除外をリクエスト（オプション）
    final batteryOptimization =
        await FlutterForegroundTask.isIgnoringBatteryOptimizations;
    if (!batteryOptimization) {
      await FlutterForegroundTask.requestIgnoreBatteryOptimization();
    }

    return await FlutterForegroundTask.checkNotificationPermission() ==
        NotificationPermission.granted;
  }

  /// SSH接続時にForeground Serviceを開始
  ///
  /// [connectionId] joins the refcount. Subsequent calls with new IDs
  /// keep the same service running and only refresh the notification.
  Future<bool> startService({
    required String connectionId,
    required String connectionName,
    required String host,
  }) async {
    _activeConnectionIds.add(connectionId);

    if (!Platform.isAndroid) return true;

    if (_isRunning) {
      // Another connection already drives the service; surface the
      // newest connection in the notification text so the user has a
      // sense of which host they last reached.
      _currentConnectionName = connectionName;
      await updateNotification(
        title: 'SSH connected: $connectionName',
        text: 'Host: $host',
      );
      return true;
    }

    await initialize();

    final hasPermission = await requestPermissions();
    if (!hasPermission) {
      return false;
    }

    _currentConnectionName = connectionName;

    final result = await FlutterForegroundTask.startService(
      notificationTitle: 'SSH connected: $connectionName',
      notificationText: 'Host: $host',
      callback: _startCallback,
    );

    _isRunning = result is ServiceRequestSuccess;
    return _isRunning;
  }

  /// 通知テキストを更新
  Future<void> updateNotification({
    String? title,
    String? text,
  }) async {
    if (!Platform.isAndroid || !_isRunning) return;

    await FlutterForegroundTask.updateService(
      notificationTitle: title,
      notificationText: text,
    );
  }

  /// SSH切断時にForeground Serviceを停止
  ///
  /// [connectionId] leaves the refcount; the service only actually
  /// stops when the last connection releases its hold.
  Future<void> stopService({required String connectionId}) async {
    _activeConnectionIds.remove(connectionId);

    if (_activeConnectionIds.isNotEmpty) {
      // Other connections still need the service running.
      return;
    }

    if (!Platform.isAndroid || !_isRunning) return;

    await FlutterForegroundTask.stopService();
    _isRunning = false;
    _currentConnectionName = null;
  }

  /// サービスが実行可能か確認
  Future<bool> canStartService() async {
    if (!Platform.isAndroid) return false;

    final permission =
        await FlutterForegroundTask.checkNotificationPermission();
    return permission == NotificationPermission.granted;
  }
}

/// Foreground Task開始時のコールバック（必須だが、SSH接続はメインisolateで管理）
@pragma('vm:entry-point')
void _startCallback() {
  FlutterForegroundTask.setTaskHandler(_SshTaskHandler());
}

/// SSH接続維持用のTaskHandler
class _SshTaskHandler extends TaskHandler {
  @override
  Future<void> onStart(DateTime timestamp, TaskStarter starter) async {
    // SSH接続はメインisolateで管理されるため、ここでは何もしない
    // このHandlerはForeground Serviceを維持するためだけに存在
  }

  @override
  void onRepeatEvent(DateTime timestamp) {
    // 定期実行イベント（使用しない）
  }

  @override
  Future<void> onDestroy(DateTime timestamp) async {
    // サービス終了時の処理（必要に応じてクリーンアップ）
  }

  @override
  void onNotificationButtonPressed(String id) {
    // 通知ボタンがタップされた時（使用しない）
  }

  @override
  void onNotificationPressed() {
    // 通知がタップされた時 - アプリを前面に持ってくる
    FlutterForegroundTask.launchApp();
  }

  @override
  void onNotificationDismissed() {
    // 通知がスワイプで削除された時
  }
}

