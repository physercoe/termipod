import 'dart:async';
import 'dart:io' show Platform;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:media_store_plus/media_store_plus.dart';
import 'package:pdfrx/pdfrx.dart';
import 'package:termipod/l10n/app_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:termipod/providers/connection_provider.dart';
import 'package:termipod/providers/hub_provider.dart';
import 'package:termipod/providers/settings_provider.dart';
import 'package:termipod/screens/home_screen.dart';
import 'package:termipod/screens/terminal/terminal_screen.dart';
import 'package:termipod/services/deep_link/deep_link_service.dart';
import 'package:termipod/services/license_service.dart';
import 'package:termipod/services/notifications/local_notifications.dart';
import 'package:termipod/services/public_file_store.dart';
import 'package:termipod/theme/app_theme.dart';
import 'package:termipod/widgets/steward_overlay/steward_overlay.dart';
import 'package:termipod/widgets/steward_overlay/steward_overlay_controller.dart';

// Used by the local notification tap handler (Phase 1.5a) to switch
// the bottom-nav to the Me tab when the user taps a notification.
const int _meTabIndex = 2;

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();

  // pdfrx (PDF artifact viewer) needs an explicit init call before
  // any PdfViewer widget builds — without this, pdfium silently
  // fails and pages render as a blank white sheet. Caught after
  // v1.0.510/.511 testers reported "white page" on every PDF (both
  // the synthetic seed and uploaded real PDFs). See pdfrx 2.3.x
  // docs: `pdfrxFlutterInitialize` is the canonical entry point.
  pdfrxFlutterInitialize();

  // MediaStore for public Download/TermiPod writes. Android-only; the
  // plugin errors on iOS if we call ensureInitialized there.
  if (Platform.isAndroid) {
    await MediaStore.ensureInitialized();
    MediaStore.appFolder = PublicFileStore.appFolderName;
  }

  // フォントライセンスを登録
  LicenseService.registerLicenses();

  // Local notifications channel registration. Permission prompt is
  // deferred to the settings screen / first new-attention event so
  // we don't ambush a fresh user with a permission dialog at launch.
  await LocalNotifications.instance.init();

  // ステータスバーを透明に
  SystemChrome.setSystemUIOverlayStyle(const SystemUiOverlayStyle(
    statusBarColor: Colors.transparent,
    statusBarIconBrightness: Brightness.light,
  ));

  // Shared navigator key — used by both MyApp's MaterialApp and the
  // steward overlay's intent dispatcher. The overlay's controller
  // reads this provider to push routes from outside the widget tree
  // (the SSE listener may fire while the chat panel is collapsed).
  final navigatorKey = GlobalKey<NavigatorState>();

  runApp(
    ProviderScope(
      overrides: [
        overlayNavigatorKeyProvider.overrideWithValue(navigatorKey),
      ],
      child: MyApp(navigatorKey: navigatorKey),
    ),
  );
}

class MyApp extends ConsumerStatefulWidget {
  final GlobalKey<NavigatorState> navigatorKey;
  const MyApp({super.key, required this.navigatorKey});

  @override
  ConsumerState<MyApp> createState() => _MyAppState();
}

class _MyAppState extends ConsumerState<MyApp> {
  GlobalKey<NavigatorState> get _navigatorKey => widget.navigatorKey;
  final _deepLinkService = DeepLinkService();
  StreamSubscription<DeepLinkData>? _linkSubscription;
  bool _initialLinkHandled = false;

  @override
  void initState() {
    super.initState();
    _initDeepLinks();
    _wireNotificationTap();
  }

  /// Phase 1.5a: when the user taps a notification, pop to the
  /// home screen and switch the bottom-nav to Me. The plugin's
  /// onDidReceiveNotificationResponse trampolines into this
  /// callback (LocalNotifications.setOnTap).
  void _wireNotificationTap() {
    LocalNotifications.instance.setOnTap((response) {
      final nav = _navigatorKey.currentState;
      if (nav == null) return;
      nav.popUntil((route) => route.isFirst);
      ref.read(currentTabProvider.notifier).setTab(_meTabIndex);
    });
  }

  Future<void> _initDeepLinks() async {
    // ホットリンクの監視は初期化の成否に関わらず設定
    _linkSubscription = _deepLinkService.linkStream.listen(_handleDeepLink);

    await _deepLinkService.initialize();

    // コールドスタートの初期リンクは接続データロード後に処理
    if (_deepLinkService.initialLink != null) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        _waitForConnectionsAndHandleInitialLink();
      });
    }
  }

  Future<void> _waitForConnectionsAndHandleInitialLink() async {
    if (_initialLinkHandled) return;

    // 接続データがロードされるまで待つ（最大3秒）
    for (int i = 0; i < 30; i++) {
      final state = ref.read(connectionsProvider);
      if (!state.isLoading) break;
      await Future.delayed(const Duration(milliseconds: 100));
    }

    // ナビゲーターが準備完了するまで待つ（最大1秒）
    for (int i = 0; i < 10; i++) {
      if (_navigatorKey.currentState != null) break;
      await Future.delayed(const Duration(milliseconds: 100));
    }

    final initialLink = _deepLinkService.initialLink;
    if (initialLink != null && !_initialLinkHandled) {
      _initialLinkHandled = true;
      _handleDeepLink(initialLink);
    }
  }

  void _handleDeepLink(DeepLinkData data) {
    if (!data.hasTarget) return;

    final navigator = _navigatorKey.currentState;
    if (navigator == null) return;

    final connection = ref.read(connectionsProvider.notifier)
        .findByDeepLinkIdOrName(data.server!);

    if (connection == null) {
      ScaffoldMessenger.maybeOf(navigator.context)?.showSnackBar(
        SnackBar(
          content: Text(AppLocalizations.of(navigator.context)?.deepLinkServerNotFound(data.server!) ?? 'Server not found: ${data.server}'),
          backgroundColor: Colors.red,
        ),
      );
      return;
    }

    // まず既存ルートをホームまで戻す
    navigator.popUntil((route) => route.isFirst);

    // 次フレームでpushする（popUntilによるTerminalScreen.dispose()が
    // 完了してからでないとref.readが_elements assertionで失敗する）
    WidgetsBinding.instance.addPostFrameCallback((_) {
      final nav = _navigatorKey.currentState;
      if (nav == null) return;

      nav.push(
        MaterialPageRoute(
          builder: (context) => TerminalScreen(
            connectionId: connection.id,
            sessionName: data.session,
            deepLinkWindowName: data.window,
            deepLinkPaneIndex: data.pane,
          ),
        ),
      );
    });
  }

  @override
  void dispose() {
    _linkSubscription?.cancel();
    _deepLinkService.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final settings = ref.watch(settingsProvider);

    // Resolve locale override
    final localeOverride = settings.locale != 'system'
        ? Locale(settings.locale)
        : null;

    return MaterialApp(
      navigatorKey: _navigatorKey,
      title: 'TermiPod',
      theme: AppTheme.light,
      darkTheme: AppTheme.dark,
      themeMode: settings.darkMode ? ThemeMode.dark : ThemeMode.light,
      localizationsDelegates: const [
        AppLocalizations.delegate,
        GlobalMaterialLocalizations.delegate,
        GlobalWidgetsLocalizations.delegate,
        GlobalCupertinoLocalizations.delegate,
      ],
      supportedLocales: AppLocalizations.supportedLocales,
      locale: localeOverride,
      home: const HomeScreen(),
      // Wrap every route in the steward overlay so the chat puck
      // persists across Navigator.push/pop. ADR-022's persistent
      // overlay (agent-driven mobile UI prototype, v1.0.464+).
      builder: (ctx, child) {
        if (child == null) return const SizedBox.shrink();
        return _StewardOverlayHost(child: child);
      },
      debugShowCheckedModeBanner: false,
    );
  }
}

/// Tiny wrapper that ensures the overlay controller has called
/// `ensureStarted()` once the hub config is loaded. Doing this here
/// (rather than in StewardOverlay's State) means the controller is
/// alive across MaterialApp rebuilds.
class _StewardOverlayHost extends ConsumerStatefulWidget {
  final Widget child;
  const _StewardOverlayHost({required this.child});

  @override
  ConsumerState<_StewardOverlayHost> createState() =>
      _StewardOverlayHostState();
}

class _StewardOverlayHostState extends ConsumerState<_StewardOverlayHost> {
  bool _ensured = false;

  @override
  Widget build(BuildContext context) {
    final hub = ref.watch(hubProvider).value;
    final hasConfig = hub?.config != null;
    final overlayEnabled =
        ref.watch(settingsProvider.select((s) => s.stewardOverlayEnabled));
    // Lazy-start the controller once the hub config is available
    // AND the user hasn't disabled the overlay. Before config land
    // ensureGeneralSteward would 401; if the user has the toggle
    // off there's no surface to render events into.
    if (!_ensured && hasConfig && overlayEnabled) {
      _ensured = true;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        ref.read(stewardOverlayControllerProvider.notifier).ensureStarted();
      });
    }
    if (!hasConfig || !overlayEnabled) {
      return widget.child;
    }
    return StewardOverlay(child: widget.child);
  }
}
