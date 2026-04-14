/// Automated screenshot generator for store listings and README.
///
/// Renders each main screen with mock data at phone-sized resolution
/// and saves golden PNG files to test/screenshots/goldens/.
///
/// Usage (on dev machine with Flutter SDK):
///   flutter test test/screenshots/generate_screenshots.dart --update-goldens
///
/// After running, the generated PNGs are in test/screenshots/goldens/.
/// Copy them to docs/screens/ or use directly for store assets.
///
/// Google Fonts (Space Grotesk, JetBrains Mono) are pre-bundled in
/// assets/fonts/google/ so they load from the asset bundle without
/// network access. Text renders with real fonts.
library;

import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:google_fonts/google_fonts.dart';

import 'package:termipod/l10n/app_localizations.dart';
import 'package:termipod/providers/active_session_provider.dart';
import 'package:termipod/providers/connection_provider.dart';
import 'package:termipod/providers/history_provider.dart';
import 'package:termipod/providers/key_provider.dart';
import 'package:termipod/providers/notification_panes_provider.dart';
import 'package:termipod/providers/settings_provider.dart';
import 'package:termipod/providers/snippet_provider.dart';
import 'package:termipod/screens/connections/connections_screen.dart';
import 'package:termipod/screens/dashboard/dashboard_screen.dart';
import 'package:termipod/screens/notifications/notification_panes_screen.dart';
import 'package:termipod/screens/settings/settings_screen.dart';
import 'package:termipod/screens/vault/vault_screen.dart';
import 'package:termipod/models/action_bar_presets.dart';
import 'package:termipod/providers/action_bar_provider.dart';
import 'package:termipod/theme/app_theme.dart';
import 'package:termipod/theme/design_colors.dart';
import 'package:termipod/widgets/action_bar/action_bar.dart';
import 'package:termipod/widgets/action_bar/compose_bar.dart';
import 'package:termipod/widgets/custom_keyboard.dart';
import 'package:termipod/widgets/action_bar/insert_menu.dart' show InsertMenu;
import 'package:termipod/widgets/action_bar/profile_sheet.dart';
import 'package:termipod/widgets/action_bar/snippet_picker_sheet.dart';
import 'package:termipod/widgets/floating_joystick.dart';
import 'package:termipod/widgets/navigation_pad.dart';

import '../helpers/mock_data.dart';

// ---------------------------------------------------------------------------
// Mock Notifiers — return pre-built state, ignore storage/network
// ---------------------------------------------------------------------------

class MockConnectionsNotifier extends ConnectionsNotifier {
  @override
  ConnectionsState build() => mockConnectionsState;
}

class MockActiveSessionsNotifier extends ActiveSessionsNotifier {
  @override
  ActiveSessionsState build() => mockActiveSessions;
}

class _MockSettingsNotifier extends SettingsNotifier {
  final bool _dark;
  _MockSettingsNotifier({required bool dark}) : _dark = dark;

  @override
  AppSettings build() => AppSettings(darkMode: _dark);
}

class MockKeysNotifier extends KeysNotifier {
  @override
  KeysState build() => mockKeysState;
}

class MockSnippetsNotifier extends SnippetsNotifier {
  @override
  SnippetsState build() => mockSnippetsState;
}

class MockHistoryNotifier extends HistoryNotifier {
  @override
  HistoryState build() => mockHistoryState;
}

class MockAlertPanesNotifier extends AlertPanesNotifier {
  @override
  AlertPanesState build() => mockAlertPanesState;
}

class MockActionBarNotifier extends ActionBarNotifier {
  @override
  ActionBarState build() => ActionBarState(
        profiles: ActionBarPresets.all,
        composeMode: true,
      );
}

class MockActionBarDirectNotifier extends ActionBarNotifier {
  @override
  ActionBarState build() => ActionBarState(
        profiles: ActionBarPresets.all,
        composeMode: false,
      );
}

// ---------------------------------------------------------------------------
// Test wrapper — provides mock providers + theme + l10n
// ---------------------------------------------------------------------------

Widget _buildScreenshot({
  required Widget child,
  bool dark = true,
  bool directInputMode = false,
}) {
  return ProviderScope(
    overrides: [
      connectionsProvider.overrideWith(() => MockConnectionsNotifier()),
      activeSessionsProvider.overrideWith(() => MockActiveSessionsNotifier()),
      settingsProvider.overrideWith(() => _MockSettingsNotifier(dark: dark)),
      keysProvider.overrideWith(() => MockKeysNotifier()),
      snippetsProvider.overrideWith(() => MockSnippetsNotifier()),
      historyProvider.overrideWith(() => MockHistoryNotifier()),
      alertPanesProvider.overrideWith(() => MockAlertPanesNotifier()),
      actionBarProvider.overrideWith(() => directInputMode
          ? MockActionBarDirectNotifier()
          : MockActionBarNotifier()),
    ],
    child: MaterialApp(
      debugShowCheckedModeBanner: false,
      theme: dark ? AppTheme.dark : AppTheme.light,
      localizationsDelegates: const [
        AppLocalizations.delegate,
        GlobalMaterialLocalizations.delegate,
        GlobalWidgetsLocalizations.delegate,
        GlobalCupertinoLocalizations.delegate,
      ],
      supportedLocales: AppLocalizations.supportedLocales,
      locale: const Locale('en'),
      home: child,
    ),
  );
}

// ---------------------------------------------------------------------------
// Font loading — load bundled custom fonts
// ---------------------------------------------------------------------------

Future<void> _loadAppFonts() async {
  final fontManifest = <String, List<String>>{
    'HackGenConsole': [
      'assets/fonts/HackGenConsole-Regular.ttf',
      'assets/fonts/HackGenConsole-Bold.ttf',
    ],
    'UDEVGothicNF': [
      'assets/fonts/UDEVGothicNF-Regular.ttf',
      'assets/fonts/UDEVGothicNF-Bold.ttf',
    ],
    'JetBrainsMono': [
      'assets/fonts/google/JetBrainsMono-Regular.ttf',
      'assets/fonts/google/JetBrainsMono-Medium.ttf',
      'assets/fonts/google/JetBrainsMono-Bold.ttf',
    ],
    // Generic 'monospace' used by key fingerprints, snippet content, etc.
    'monospace': [
      'assets/fonts/google/JetBrainsMono-Regular.ttf',
    ],
  };

  for (final entry in fontManifest.entries) {
    for (final path in entry.value) {
      final file = File(path);
      if (file.existsSync()) {
        final loader = FontLoader(entry.key);
        loader.addFont(Future.value(ByteData.sublistView(file.readAsBytesSync())));
        await loader.load();
      }
    }
  }

  // Load MaterialIcons font from Flutter SDK so icons render in goldens.
  await _loadMaterialIcons();
}

Future<void> _loadMaterialIcons() async {
  // Find Flutter SDK root. Try multiple strategies:
  // 1. FLUTTER_ROOT env var
  // 2. Derive from Platform.resolvedExecutable
  //    (<flutter>/bin/cache/dart-sdk/bin/dart → 5 parents up)
  // 3. `which flutter` and resolve symlinks
  String? flutterRoot = Platform.environment['FLUTTER_ROOT'];

  if (flutterRoot == null || flutterRoot.isEmpty) {
    final dartExe = Platform.resolvedExecutable;
    final candidate = File(dartExe).parent.parent.parent.parent.parent.path;
    final check = File('$candidate/bin/cache/artifacts/material_fonts/MaterialIcons-Regular.otf');
    if (check.existsSync()) {
      flutterRoot = candidate;
    }
  }

  if (flutterRoot == null) {
    // Try resolving 'flutter' from PATH
    final result = Process.runSync('which', ['flutter']);
    if (result.exitCode == 0) {
      final flutterBin = (result.stdout as String).trim();
      // Follow symlinks: readlink -f
      final linkResult = Process.runSync('readlink', ['-f', flutterBin]);
      if (linkResult.exitCode == 0) {
        final resolved = (linkResult.stdout as String).trim();
        flutterRoot = File(resolved).parent.parent.path;
      }
    }
  }

  if (flutterRoot == null) return;

  final iconFont = File(
    '$flutterRoot/bin/cache/artifacts/material_fonts/MaterialIcons-Regular.otf',
  );
  if (iconFont.existsSync()) {
    final loader = FontLoader('MaterialIcons');
    loader.addFont(
      Future.value(ByteData.sublistView(iconFont.readAsBytesSync())),
    );
    await loader.load();
  }
}

// ---------------------------------------------------------------------------
// Phone surface size — matches common Android phone (412x915 dp)
// ---------------------------------------------------------------------------

const _phoneSize = Size(412, 915);

// ---------------------------------------------------------------------------
// Screenshot capture helper
// ---------------------------------------------------------------------------

Future<void> _captureScreenshot(
  WidgetTester tester,
  Widget screen,
  String goldenPath,
) async {
  tester.view.physicalSize = _phoneSize * tester.view.devicePixelRatio;
  addTearDown(() => tester.view.resetPhysicalSize());

  await tester.pumpWidget(screen);
  // Use pump with duration instead of pumpAndSettle to avoid timeout
  // on screens with ongoing animations (e.g. alert badge pulses).
  for (int i = 0; i < 10; i++) {
    await tester.pump(const Duration(milliseconds: 100));
  }

  await expectLater(
    find.byType(MaterialApp),
    matchesGoldenFile(goldenPath),
  );
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

void main() {
  setUpAll(() async {
    // Fonts are pre-bundled in assets/fonts/google/ — google_fonts loads
    // them from the asset bundle, no network needed.
    GoogleFonts.config.allowRuntimeFetching = false;
    await _loadAppFonts();
  });

  group('Screenshots', () {
    testWidgets('dashboard_dark', (tester) async {
      await _captureScreenshot(tester, _buildScreenshot(child: const DashboardScreen()), 'goldens/dashboard_dark.png');
    });

    testWidgets('servers_dark', (tester) async {
      await _captureScreenshot(tester, _buildScreenshot(child: const ConnectionsScreen()), 'goldens/servers_dark.png');
    });

    testWidgets('vault_dark', (tester) async {
      await _captureScreenshot(tester, _buildScreenshot(child: const VaultScreen()), 'goldens/vault_dark.png');
    });

    testWidgets('alerts_dark', (tester) async {
      await _captureScreenshot(tester, _buildScreenshot(child: const NotificationPanesScreen()), 'goldens/alerts_dark.png');
    });

    testWidgets('settings_dark', (tester) async {
      await _captureScreenshot(tester, _buildScreenshot(child: const SettingsScreen()), 'goldens/settings_dark.png');
    });

    testWidgets('dashboard_light', (tester) async {
      await _captureScreenshot(tester, _buildScreenshot(child: const DashboardScreen(), dark: false), 'goldens/dashboard_light.png');
    });

    testWidgets('servers_light', (tester) async {
      await _captureScreenshot(tester, _buildScreenshot(child: const ConnectionsScreen(), dark: false), 'goldens/servers_light.png');
    });

    testWidgets('vault_light', (tester) async {
      await _captureScreenshot(tester, _buildScreenshot(child: const VaultScreen(), dark: false), 'goldens/vault_light.png');
    });

    testWidgets('settings_light', (tester) async {
      await _captureScreenshot(tester, _buildScreenshot(child: const SettingsScreen(), dark: false), 'goldens/settings_light.png');
    });

    // Terminal screen mock — shows action bar, compose bar, nav pad,
    // custom keyboard, and floating joystick with fake ANSI output.
    testWidgets('terminal_dark', (tester) async {
      await _captureScreenshot(
        tester,
        _buildScreenshot(
          child: const _MockTerminalScreen(),
          dark: true,
        ),
        'goldens/terminal_dark.png',
      );
    });

    testWidgets('terminal_keyboard_dark', (tester) async {
      await _captureScreenshot(
        tester,
        _buildScreenshot(
          child: const _MockTerminalScreen(showKeyboard: true),
          dark: true,
          directInputMode: true,
        ),
        'goldens/terminal_keyboard_dark.png',
      );
    });

    // Bottom sheet overlays — rendered directly in a scaffold
    // to avoid needing showModalBottomSheet + animation settling.

    testWidgets('bolt_menu_dark', (tester) async {
      await _captureScreenshot(
        tester,
        _buildScreenshot(
          child: _MockSheetScreen(
            sheet: SnippetPickerSheet(
              onInsert: (_) {},
              onSendImmediately: (_) {},
            ),
          ),
          dark: true,
        ),
        'goldens/bolt_menu_dark.png',
      );
    });

    testWidgets('key_palette_dark', (tester) async {
      await _captureScreenshot(
        tester,
        _buildScreenshot(
          child: _MockSheetScreen(
            sheet: ProfileSheet(
              onKeyTap: (_) {},
              onSpecialKeyTap: (_) {},
              onModifierTap: (_) {},
              onActionTap: (_) {},
            ),
          ),
          dark: true,
        ),
        'goldens/key_palette_dark.png',
      );
    });

    testWidgets('insert_menu_dark', (tester) async {
      await _captureScreenshot(
        tester,
        _buildScreenshot(
          child: const _MockInsertMenuScreen(),
          dark: true,
        ),
        'goldens/insert_menu_dark.png',
      );
    });
  });
}

// ---------------------------------------------------------------------------
// Mock Terminal Screen — assembles real sub-widgets around fake terminal output
// ---------------------------------------------------------------------------

class _MockTerminalScreen extends ConsumerWidget {
  final bool showKeyboard;
  const _MockTerminalScreen({this.showKeyboard = false});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;

    return Scaffold(
      backgroundColor: Theme.of(context).scaffoldBackgroundColor,
      body: Stack(
        children: [
          Column(
            children: [
              // Breadcrumb header
              _buildBreadcrumb(context, isDark),
              // Fake terminal output
              Expanded(child: _buildFakeTerminal(context, isDark)),
              // Navigation Pad
              NavigationPad(
                onSpecialKeyPressed: (_) {},
                onKeyPressed: (_) {},
              ),
              // Action Bar
              ActionBar(
                onKeyPressed: (_) {},
                onSpecialKeyPressed: (_) {},
              ),
              // Compose Bar
              ComposeBar(
                connectionId: 'mock',
                onSend: (_, {bool withEnter = true}) {},
              ),
              // Custom Keyboard (for keyboard variant screenshot)
              if (showKeyboard)
                CustomKeyboard(
                  onKeyPressed: (_) {},
                  onSpecialKeyPressed: (_) {},
                  haptic: false,
                ),
            ],
          ),
          // Floating joystick overlay
          if (!showKeyboard)
            FloatingJoystick(
              onSpecialKeyPressed: (_) {},
              haptic: false,
            ),
        ],
      ),
    );
  }

  Widget _buildBreadcrumb(BuildContext context, bool isDark) {
    return Container(
      height: 48,
      padding: const EdgeInsets.symmetric(horizontal: 16),
      decoration: BoxDecoration(
        color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        border: Border(
          bottom: BorderSide(
            color: isDark ? DesignColors.borderDark : DesignColors.borderLight,
          ),
        ),
      ),
      child: SafeArea(
        bottom: false,
        child: Row(
          children: [
            Icon(Icons.dns_rounded, size: 16, color: DesignColors.primary),
            const SizedBox(width: 8),
            Text('dev-gpu-box',
                style: TextStyle(
                    fontWeight: FontWeight.w600,
                    color: isDark
                        ? DesignColors.textPrimary
                        : DesignColors.textPrimaryLight)),
            Icon(Icons.chevron_right, size: 18, color: DesignColors.textSecondary),
            Text('claude',
                style: TextStyle(
                    color: isDark
                        ? DesignColors.textSecondary
                        : DesignColors.textSecondaryLight)),
            Icon(Icons.chevron_right, size: 18, color: DesignColors.textSecondary),
            Text('agent',
                style: TextStyle(
                    color: isDark
                        ? DesignColors.textSecondary
                        : DesignColors.textSecondaryLight)),
            const Spacer(),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
              decoration: BoxDecoration(
                color: DesignColors.success.withValues(alpha: 0.15),
                borderRadius: BorderRadius.circular(4),
              ),
              child: Text('12ms',
                  style: TextStyle(
                      fontSize: 10,
                      color: DesignColors.success,
                      fontWeight: FontWeight.w500)),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildFakeTerminal(BuildContext context, bool isDark) {
    // Simulated Claude Code session output
    final lines = [
      _TermLine('╭──────────────────────────────────────╮', DesignColors.primary),
      _TermLine('│  Claude Code  v1.0.32                │', DesignColors.primary),
      _TermLine('│  Model: claude-sonnet-4-20250514    │', DesignColors.primary),
      _TermLine('╰──────────────────────────────────────╯', DesignColors.primary),
      _TermLine('', Colors.transparent),
      _TermLine('> /model opus', Colors.white),
      _TermLine('  Model set to: claude-opus-4-20250514', const Color(0xFF4ADE80)),
      _TermLine('', Colors.transparent),
      _TermLine('> Fix the auth middleware to validate', Colors.white),
      _TermLine('  JWT tokens before checking roles.', Colors.white),
      _TermLine('', Colors.transparent),
      _TermLine('● Reading src/middleware/auth.ts', const Color(0xFF60A5FA)),
      _TermLine('● Reading src/types/jwt.ts', const Color(0xFF60A5FA)),
      _TermLine('● Reading tests/auth.test.ts', const Color(0xFF60A5FA)),
      _TermLine('', Colors.transparent),
      _TermLine('I\'ll fix the auth middleware. The issue', Colors.white),
      _TermLine('is that role checks happen before token', Colors.white),
      _TermLine('validation, so expired tokens with the', Colors.white),
      _TermLine('right role still pass.', Colors.white),
      _TermLine('', Colors.transparent),
      _TermLine('  src/middleware/auth.ts', const Color(0xFFFBBF24)),
      _TermLine('  + const decoded = verifyJWT(token);', const Color(0xFF4ADE80)),
      _TermLine('  + if (!decoded) return res.status(401);', const Color(0xFF4ADE80)),
      _TermLine('  - checkRole(req.headers.auth);', const Color(0xFFF87171)),
      _TermLine('  + checkRole(decoded.payload);', const Color(0xFF4ADE80)),
      _TermLine('', Colors.transparent),
      _TermLine('Apply changes? (y/n)', const Color(0xFFFBBF24)),
      _TermLine('█', Colors.white),
    ];

    return Container(
      padding: const EdgeInsets.all(8),
      color: isDark ? DesignColors.backgroundDark : DesignColors.backgroundLight,
      child: ListView.builder(
        itemCount: lines.length,
        itemExtent: 18,
        physics: const NeverScrollableScrollPhysics(),
        itemBuilder: (context, index) {
          final line = lines[index];
          return Text(
            line.text,
            style: TextStyle(
              fontFamily: 'JetBrainsMono',
              fontSize: 12,
              height: 1.4,
              color: line.color,
            ),
            maxLines: 1,
            overflow: TextOverflow.clip,
          );
        },
      ),
    );
  }
}

class _TermLine {
  final String text;
  final Color color;
  const _TermLine(this.text, this.color);
}

// ---------------------------------------------------------------------------
// Mock Sheet Screen — renders a bottom sheet widget inline over a dim backdrop
// ---------------------------------------------------------------------------

class _MockSheetScreen extends StatelessWidget {
  final Widget sheet;
  const _MockSheetScreen({required this.sheet});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Scaffold(
      backgroundColor: isDark
          ? DesignColors.backgroundDark
          : DesignColors.backgroundLight,
      body: Column(
        children: [
          // Dim header area to simulate backdrop behind sheet
          Container(
            height: 80,
            color: Colors.black.withValues(alpha: 0.4),
          ),
          // The sheet widget takes the rest
          Expanded(child: sheet),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Mock Insert Menu Screen — renders the insert menu items directly
// ---------------------------------------------------------------------------

class _MockInsertMenuScreen extends StatelessWidget {
  const _MockInsertMenuScreen();

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Scaffold(
      backgroundColor: isDark
          ? DesignColors.backgroundDark
          : DesignColors.backgroundLight,
      body: Column(
        children: [
          // Dim top area
          Expanded(
            child: Container(color: Colors.black.withValues(alpha: 0.4)),
          ),
          // Menu at bottom — replicate InsertMenu layout
          Container(
            decoration: BoxDecoration(
              color: isDark
                  ? DesignColors.surfaceDark
                  : DesignColors.surfaceLight,
              borderRadius:
                  const BorderRadius.vertical(top: Radius.circular(16)),
            ),
            child: SafeArea(
              top: false,
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  // Handle bar
                  Container(
                    width: 32,
                    height: 4,
                    margin: const EdgeInsets.only(top: 8, bottom: 12),
                    decoration: BoxDecoration(
                      color: isDark
                          ? DesignColors.textMuted
                          : DesignColors.textMutedLight,
                      borderRadius: BorderRadius.circular(2),
                    ),
                  ),
                  _menuItem(context, Icons.upload_file,
                      AppLocalizations.of(context)!.fileUpload, isDark),
                  _menuItem(context, Icons.download,
                      AppLocalizations.of(context)!.fileDownload, isDark),
                  _menuItem(context, Icons.image,
                      AppLocalizations.of(context)!.imageTransfer, isDark),
                  const Divider(height: 1),
                  _menuItem(context, Icons.keyboard,
                      AppLocalizations.of(context)!.directInputMode, isDark),
                  const SizedBox(height: 8),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _menuItem(
      BuildContext context, IconData icon, String label, bool isDark) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 14),
      child: Row(
        children: [
          Icon(icon,
              size: 22,
              color: isDark
                  ? DesignColors.textSecondary
                  : DesignColors.textSecondaryLight),
          const SizedBox(width: 16),
          Text(label,
              style: TextStyle(
                  fontSize: 15,
                  color: isDark
                      ? DesignColors.textPrimary
                      : DesignColors.textPrimaryLight)),
        ],
      ),
    );
  }
}
