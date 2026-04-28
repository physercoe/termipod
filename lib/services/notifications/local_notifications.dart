import 'dart:io';

import 'package:flutter_local_notifications/flutter_local_notifications.dart';

/// Thin wrapper around `flutter_local_notifications` for the Phase
/// 1.5a in-app notification path: when the app receives an event
/// worth surfacing (a new attention item, an agent turn finishing,
/// a session pausing) and the user isn't actively staring at the
/// relevant screen, we emit a system notification.
///
/// Limit by design: this only fires while the app process is alive
/// (foreground or backgrounded). Killed-state delivery comes from
/// ntfy in Phase 1.5b. The MVP-parity-gaps plan documents this.
///
/// Singleton — there's one notification surface per device, so a
/// global instance avoids double-init pain in tests and hot-reload.
class LocalNotifications {
  LocalNotifications._();
  static final LocalNotifications instance = LocalNotifications._();

  final FlutterLocalNotificationsPlugin _plugin =
      FlutterLocalNotificationsPlugin();
  bool _initialized = false;
  bool _permissionRequested = false;

  /// Channel for attention-item raised notifications (highest user
  /// salience — these are decisions waiting on the principal).
  static const String attentionChannelId = 'termipod_attention';
  static const String attentionChannelName = 'Approval requests';
  static const String attentionChannelDescription =
      'Notifies when the steward raises a decision or approval that '
      'needs your attention.';

  /// Channel for lower-salience hub events (turn finished, session
  /// paused). Disabled by default in settings.
  static const String hubEventsChannelId = 'termipod_hub_events';
  static const String hubEventsChannelName = 'Hub events';
  static const String hubEventsChannelDescription =
      'Lower-priority notifications for turn-end and session-paused '
      'events.';

  Future<void> init() async {
    if (_initialized) return;
    // Skip platform plugin calls on non-mobile (tests, desktop) so
    // widget tests don't blow up on MissingPluginException.
    if (!Platform.isAndroid && !Platform.isIOS) {
      _initialized = true;
      return;
    }
    const android = AndroidInitializationSettings('@mipmap/ic_launcher');
    const ios = DarwinInitializationSettings(
      requestAlertPermission: false,
      requestBadgePermission: false,
      requestSoundPermission: false,
    );
    try {
      await _plugin.initialize(
        const InitializationSettings(android: android, iOS: ios),
      );
      if (Platform.isAndroid) {
        final androidImpl =
            _plugin.resolvePlatformSpecificImplementation<
                AndroidFlutterLocalNotificationsPlugin>();
        // Channels need to exist before show() can target them on
        // Android 8+. Created idempotently — re-creating is a no-op.
        await androidImpl?.createNotificationChannel(
          const AndroidNotificationChannel(
            attentionChannelId,
            attentionChannelName,
            description: attentionChannelDescription,
            importance: Importance.high,
          ),
        );
        await androidImpl?.createNotificationChannel(
          const AndroidNotificationChannel(
            hubEventsChannelId,
            hubEventsChannelName,
            description: hubEventsChannelDescription,
            importance: Importance.low,
          ),
        );
      }
    } catch (_) {
      // Best-effort init — if the plugin isn't registered (e.g.
      // headless test runs on CI), don't break the rest of the app.
    }
    _initialized = true;
  }

  /// Asks the OS for permission to post notifications. On Android 13+
  /// the user must grant POST_NOTIFICATIONS at runtime; on iOS this
  /// raises the alert/badge/sound prompt. No-op pre-13 / on platforms
  /// where the OS doesn't gate.
  Future<bool> requestPermission() async {
    if (!_initialized) await init();
    if (Platform.isAndroid) {
      final androidImpl =
          _plugin.resolvePlatformSpecificImplementation<
              AndroidFlutterLocalNotificationsPlugin>();
      final granted =
          await androidImpl?.requestNotificationsPermission() ?? true;
      return granted;
    }
    if (Platform.isIOS) {
      final iosImpl =
          _plugin.resolvePlatformSpecificImplementation<
              IOSFlutterLocalNotificationsPlugin>();
      final granted = await iosImpl?.requestPermissions(
            alert: true,
            badge: true,
            sound: true,
          ) ??
          true;
      return granted;
    }
    return true;
  }

  /// Lazy permission prompt — fires once per app session the first
  /// time a notification is about to be shown. Don't ambush the
  /// user at app launch with a permission dialog before there's a
  /// concrete reason for one.
  Future<void> _ensurePermission() async {
    if (_permissionRequested) return;
    _permissionRequested = true;
    await requestPermission();
  }

  /// Shows an attention notification (high importance — for
  /// approvals/decisions requiring user input).
  Future<void> showAttention({
    required int id,
    required String title,
    required String body,
  }) async {
    if (!_initialized) await init();
    if (!Platform.isAndroid && !Platform.isIOS) return;
    await _ensurePermission();
    try {
      await _plugin.show(
        id,
        title,
        body,
        const NotificationDetails(
          android: AndroidNotificationDetails(
            attentionChannelId,
            attentionChannelName,
            channelDescription: attentionChannelDescription,
            importance: Importance.high,
            priority: Priority.high,
            category: AndroidNotificationCategory.reminder,
          ),
          iOS: DarwinNotificationDetails(
            interruptionLevel: InterruptionLevel.timeSensitive,
          ),
        ),
      );
    } catch (_) {
      // Plugin not registered (test/desktop) — silently no-op.
    }
  }

  /// Shows a hub-event notification (low importance — for
  /// turn-finished / session-paused signals).
  Future<void> showHubEvent({
    required int id,
    required String title,
    required String body,
  }) async {
    if (!_initialized) await init();
    if (!Platform.isAndroid && !Platform.isIOS) return;
    await _ensurePermission();
    try {
      await _plugin.show(
        id,
        title,
        body,
        const NotificationDetails(
          android: AndroidNotificationDetails(
            hubEventsChannelId,
            hubEventsChannelName,
            channelDescription: hubEventsChannelDescription,
            importance: Importance.low,
            priority: Priority.low,
          ),
          iOS: DarwinNotificationDetails(),
        ),
      );
    } catch (_) {
      // Plugin not registered (test/desktop) — silently no-op.
    }
  }

  Future<void> cancel(int id) async {
    if (!Platform.isAndroid && !Platform.isIOS) return;
    try {
      await _plugin.cancel(id);
    } catch (_) {}
  }

  Future<void> cancelAll() async {
    if (!Platform.isAndroid && !Platform.isIOS) return;
    try {
      await _plugin.cancelAll();
    } catch (_) {}
  }
}
