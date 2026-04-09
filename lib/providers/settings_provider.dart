import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../services/settings_migration.dart';

/// アプリ設定
class AppSettings {
  final bool darkMode;
  final double fontSize;
  final String fontFamily;
  final bool requireBiometricAuth;
  final bool enableNotifications;
  final bool enableVibration;
  final bool keepScreenOn;
  final int scrollbackLines;
  final double minFontSize;

  /// 表示調整モード: 'none', 'autoFit', 'autoResize'
  final String adjustMode;

  /// DirectInputモード（入力した文字を即座にターミナルに送信）
  final bool directInputEnabled;

  /// ターミナルカーソルの表示設定
  final bool showTerminalCursor;

  /// ペインナビゲーション方向の反転
  final bool invertPaneNavigation;

  // --- キーオーバーレイ設定 ---
  /// キーオーバーレイ全体ON/OFF
  final bool showKeyOverlay;

  /// キーオーバーレイ: 修飾キー組み合わせ（Ctrl+x, Alt+x, Shift+x）
  final bool keyOverlayModifier;

  /// キーオーバーレイ: 単独特殊キー（ESC, TAB, ENTER, S-Enter）
  final bool keyOverlaySpecial;

  /// キーオーバーレイ: 矢印キー
  final bool keyOverlayArrow;

  /// キーオーバーレイ: ショートカットキー（/, -, 1-4）
  final bool keyOverlayShortcut;

  /// キーオーバーレイ: 表示位置
  final String keyOverlayPosition;

  // --- File transfer settings ---
  final String fileRemotePath;
  final String filePathFormat;
  final bool fileAutoEnter;
  final bool fileBracketedPaste;

  // --- 画像転送設定 ---
  final String imageRemotePath;
  final String imageOutputFormat;
  final int imageJpegQuality;
  final String imageResizePreset; // 'original'/'small'/'medium'/'large'/'custom'
  final int imageMaxWidth;
  final int imageMaxHeight;
  final String imagePathFormat;
  final bool imageAutoEnter;
  final bool imageBracketedPaste;

  /// Language override: 'system', 'en', 'zh'
  final String locale;

  // --- Navigation Pad settings ---
  /// Navigation pad mode: 'full', 'compact', 'off'
  final String navPadMode;
  /// D-pad style: 'dpad' or 'joystick'
  final String navPadDpadStyle;
  final int navPadRepeatRate;
  final bool navPadHaptic;
  /// JSON-encoded list of 4 custom action buttons (null = defaults)
  final String? navPadButtons;

  // --- File download settings ---
  /// Local download directory (empty = app external storage/TermiPod)
  final String fileDownloadPath;

  // --- Experimental features ---
  /// Floating joystick overlay on terminal (experimental)
  final bool floatingPadEnabled;
  /// Floating joystick outer radius in logical pixels (48..128)
  final double floatingPadSize;
  /// Tmux key name sent when the joystick center is tapped (default 'Enter')
  final String floatingPadCenterKey;

  const AppSettings({
    this.darkMode = true,
    this.fontSize = 14.0,
    this.fontFamily = 'JetBrains Mono',
    this.requireBiometricAuth = false,
    this.enableNotifications = true,
    this.enableVibration = true,
    this.keepScreenOn = true,
    this.scrollbackLines = 200,
    this.minFontSize = 8.0,
    this.adjustMode = 'autoFit',
    this.directInputEnabled = false,
    this.showTerminalCursor = true,
    this.invertPaneNavigation = false,
    this.showKeyOverlay = true,
    this.keyOverlayModifier = true,
    this.keyOverlaySpecial = true,
    this.keyOverlayArrow = true,
    this.keyOverlayShortcut = true,
    this.keyOverlayPosition = 'aboveKeyboard',
    this.fileRemotePath = '.',
    this.filePathFormat = '{path}',
    this.fileAutoEnter = false,
    this.fileBracketedPaste = false,
    this.imageRemotePath = '.',
    this.imageOutputFormat = 'original',
    this.imageJpegQuality = 85,
    this.imageResizePreset = 'original',
    this.imageMaxWidth = 1920,
    this.imageMaxHeight = 1080,
    this.imagePathFormat = '{path}',
    this.imageAutoEnter = false,
    this.imageBracketedPaste = false,
    this.locale = 'system',
    this.navPadMode = 'off',
    this.navPadDpadStyle = 'dpad',
    this.navPadRepeatRate = 80,
    this.navPadHaptic = true,
    this.navPadButtons,
    this.fileDownloadPath = '',
    this.floatingPadEnabled = false,
    this.floatingPadSize = 64.0,
    this.floatingPadCenterKey = 'Enter',
  });

  bool get isAutoFit => adjustMode == 'autoFit';
  bool get isAutoResize => adjustMode == 'autoResize';

  AppSettings copyWith({
    bool? darkMode,
    double? fontSize,
    String? fontFamily,
    bool? requireBiometricAuth,
    bool? enableNotifications,
    bool? enableVibration,
    bool? keepScreenOn,
    int? scrollbackLines,
    double? minFontSize,
    String? adjustMode,
    bool? directInputEnabled,
    bool? showTerminalCursor,
    bool? invertPaneNavigation,
    bool? showKeyOverlay,
    bool? keyOverlayModifier,
    bool? keyOverlaySpecial,
    bool? keyOverlayArrow,
    bool? keyOverlayShortcut,
    String? keyOverlayPosition,
    String? fileRemotePath,
    String? filePathFormat,
    bool? fileAutoEnter,
    bool? fileBracketedPaste,
    String? imageRemotePath,
    String? imageOutputFormat,
    int? imageJpegQuality,
    String? imageResizePreset,
    int? imageMaxWidth,
    int? imageMaxHeight,
    String? imagePathFormat,
    bool? imageAutoEnter,
    bool? imageBracketedPaste,
    String? locale,
    String? navPadMode,
    String? navPadDpadStyle,
    int? navPadRepeatRate,
    bool? navPadHaptic,
    String? navPadButtons,
    bool clearNavPadButtons = false,
    String? fileDownloadPath,
    bool? floatingPadEnabled,
    double? floatingPadSize,
    String? floatingPadCenterKey,
  }) {
    return AppSettings(
      darkMode: darkMode ?? this.darkMode,
      fontSize: fontSize ?? this.fontSize,
      fontFamily: fontFamily ?? this.fontFamily,
      requireBiometricAuth: requireBiometricAuth ?? this.requireBiometricAuth,
      enableNotifications: enableNotifications ?? this.enableNotifications,
      enableVibration: enableVibration ?? this.enableVibration,
      keepScreenOn: keepScreenOn ?? this.keepScreenOn,
      scrollbackLines: scrollbackLines ?? this.scrollbackLines,
      minFontSize: minFontSize ?? this.minFontSize,
      adjustMode: adjustMode ?? this.adjustMode,
      directInputEnabled: directInputEnabled ?? this.directInputEnabled,
      showTerminalCursor: showTerminalCursor ?? this.showTerminalCursor,
      invertPaneNavigation: invertPaneNavigation ?? this.invertPaneNavigation,
      showKeyOverlay: showKeyOverlay ?? this.showKeyOverlay,
      keyOverlayModifier: keyOverlayModifier ?? this.keyOverlayModifier,
      keyOverlaySpecial: keyOverlaySpecial ?? this.keyOverlaySpecial,
      keyOverlayArrow: keyOverlayArrow ?? this.keyOverlayArrow,
      keyOverlayShortcut: keyOverlayShortcut ?? this.keyOverlayShortcut,
      keyOverlayPosition: keyOverlayPosition ?? this.keyOverlayPosition,
      fileRemotePath: fileRemotePath ?? this.fileRemotePath,
      filePathFormat: filePathFormat ?? this.filePathFormat,
      fileAutoEnter: fileAutoEnter ?? this.fileAutoEnter,
      fileBracketedPaste: fileBracketedPaste ?? this.fileBracketedPaste,
      imageRemotePath: imageRemotePath ?? this.imageRemotePath,
      imageOutputFormat: imageOutputFormat ?? this.imageOutputFormat,
      imageJpegQuality: imageJpegQuality ?? this.imageJpegQuality,
      imageResizePreset: imageResizePreset ?? this.imageResizePreset,
      imageMaxWidth: imageMaxWidth ?? this.imageMaxWidth,
      imageMaxHeight: imageMaxHeight ?? this.imageMaxHeight,
      imagePathFormat: imagePathFormat ?? this.imagePathFormat,
      imageAutoEnter: imageAutoEnter ?? this.imageAutoEnter,
      imageBracketedPaste: imageBracketedPaste ?? this.imageBracketedPaste,
      locale: locale ?? this.locale,
      navPadMode: navPadMode ?? this.navPadMode,
      navPadDpadStyle: navPadDpadStyle ?? this.navPadDpadStyle,
      navPadRepeatRate: navPadRepeatRate ?? this.navPadRepeatRate,
      navPadHaptic: navPadHaptic ?? this.navPadHaptic,
      navPadButtons:
          clearNavPadButtons ? null : (navPadButtons ?? this.navPadButtons),
      fileDownloadPath: fileDownloadPath ?? this.fileDownloadPath,
      floatingPadEnabled: floatingPadEnabled ?? this.floatingPadEnabled,
      floatingPadSize: floatingPadSize ?? this.floatingPadSize,
      floatingPadCenterKey: floatingPadCenterKey ?? this.floatingPadCenterKey,
    );
  }
}

/// 設定を管理するNotifier
class SettingsNotifier extends Notifier<AppSettings> {
  static const String _darkModeKey = 'settings_dark_mode';
  static const String _fontSizeKey = 'settings_font_size';
  static const String _fontFamilyKey = 'settings_font_family';
  static const String _biometricKey = 'settings_biometric_auth';
  static const String _notificationsKey = 'settings_notifications';
  static const String _vibrationKey = 'settings_vibration';
  static const String _keepScreenOnKey = 'settings_keep_screen_on';
  static const String _scrollbackKey = 'settings_scrollback';
  static const String _minFontSizeKey = 'settings_min_font_size';
  static const String _adjustModeKey = 'settings_adjust_mode';
  static const String _directInputEnabledKey = 'settings_direct_input_enabled';
  static const String _showTerminalCursorKey = 'settings_show_terminal_cursor';
  static const String _invertPaneNavKey = 'settings_invert_pane_nav';
  static const String _fileRemotePathKey = 'settings_file_remote_path';
  static const String _filePathFormatKey = 'settings_file_path_format';
  static const String _fileAutoEnterKey = 'settings_file_auto_enter';
  static const String _fileBracketedPasteKey = 'settings_file_bracketed_paste';
  static const String _imageRemotePathKey = 'settings_image_remote_path';
  static const String _imageOutputFormatKey = 'settings_image_output_format';
  static const String _imageJpegQualityKey = 'settings_image_jpeg_quality';
  static const String _imageResizePresetKey = 'settings_image_resize_preset';
  static const String _imageMaxWidthKey = 'settings_image_max_width';
  static const String _imageMaxHeightKey = 'settings_image_max_height';
  static const String _imagePathFormatKey = 'settings_image_path_format';
  static const String _imageAutoEnterKey = 'settings_image_auto_enter';
  static const String _imageBracketedPasteKey = 'settings_image_bracketed_paste';
  static const String _showKeyOverlayKey = 'settings_show_key_overlay';
  static const String _keyOverlayModifierKey = 'settings_key_overlay_modifier';
  static const String _keyOverlaySpecialKey = 'settings_key_overlay_special';
  static const String _keyOverlayArrowKey = 'settings_key_overlay_arrow';
  static const String _keyOverlayShortcutKey = 'settings_key_overlay_shortcut';
  static const String _keyOverlayPositionKey = 'settings_key_overlay_position';
  static const String _localeKey = 'settings_locale';
  static const String _navPadModeKey = 'settings_nav_pad_mode';
  static const String _navPadDpadStyleKey = 'settings_nav_pad_dpad_style';
  static const String _navPadRepeatRateKey = 'settings_nav_pad_repeat_rate';
  static const String _navPadHapticKey = 'settings_nav_pad_haptic';
  static const String _navPadButtonsKey = 'settings_nav_pad_buttons';
  static const String _fileDownloadPathKey = 'settings_file_download_path';
  static const String _floatingPadEnabledKey = 'settings_floating_pad_enabled';
  static const String _floatingPadSizeKey = 'settings_floating_pad_size';
  static const String _floatingPadCenterKeyKey = 'settings_floating_pad_center_key';

  @override
  AppSettings build() {
    _loadSettings();
    return const AppSettings();
  }

  Future<void> _loadSettings() async {
    final prefs = await SharedPreferences.getInstance();
    await SettingsMigrationRunner.run(prefs);

    state = AppSettings(
      darkMode: prefs.getBool(_darkModeKey) ?? true,
      fontSize: prefs.getDouble(_fontSizeKey) ?? 14.0,
      fontFamily: prefs.getString(_fontFamilyKey) ?? 'JetBrains Mono',
      requireBiometricAuth: prefs.getBool(_biometricKey) ?? false,
      enableNotifications: prefs.getBool(_notificationsKey) ?? true,
      enableVibration: prefs.getBool(_vibrationKey) ?? true,
      keepScreenOn: prefs.getBool(_keepScreenOnKey) ?? true,
      scrollbackLines: prefs.getInt(_scrollbackKey) ?? 200,
      minFontSize: prefs.getDouble(_minFontSizeKey) ?? 8.0,
      adjustMode: prefs.getString(_adjustModeKey) ?? 'autoFit',
      directInputEnabled: prefs.getBool(_directInputEnabledKey) ?? false,
      showTerminalCursor: prefs.getBool(_showTerminalCursorKey) ?? true,
      invertPaneNavigation: prefs.getBool(_invertPaneNavKey) ?? false,
      showKeyOverlay: prefs.getBool(_showKeyOverlayKey) ?? true,
      keyOverlayModifier: prefs.getBool(_keyOverlayModifierKey) ?? true,
      keyOverlaySpecial: prefs.getBool(_keyOverlaySpecialKey) ?? true,
      keyOverlayArrow: prefs.getBool(_keyOverlayArrowKey) ?? true,
      keyOverlayShortcut: prefs.getBool(_keyOverlayShortcutKey) ?? true,
      keyOverlayPosition: prefs.getString(_keyOverlayPositionKey) ?? 'aboveKeyboard',
      fileRemotePath: prefs.getString(_fileRemotePathKey) ?? '.',
      filePathFormat: prefs.getString(_filePathFormatKey) ?? '{path}',
      fileAutoEnter: prefs.getBool(_fileAutoEnterKey) ?? false,
      fileBracketedPaste: prefs.getBool(_fileBracketedPasteKey) ?? false,
      imageRemotePath: prefs.getString(_imageRemotePathKey) ?? '.',
      imageOutputFormat: prefs.getString(_imageOutputFormatKey) ?? 'original',
      imageJpegQuality: prefs.getInt(_imageJpegQualityKey) ?? 85,
      imageResizePreset: prefs.getString(_imageResizePresetKey) ?? 'original',
      imageMaxWidth: prefs.getInt(_imageMaxWidthKey) ?? 1920,
      imageMaxHeight: prefs.getInt(_imageMaxHeightKey) ?? 1080,
      imagePathFormat: prefs.getString(_imagePathFormatKey) ?? '{path}',
      imageAutoEnter: prefs.getBool(_imageAutoEnterKey) ?? false,
      imageBracketedPaste: prefs.getBool(_imageBracketedPasteKey) ?? false,
      locale: prefs.getString(_localeKey) ?? 'system',
      navPadMode: prefs.getString(_navPadModeKey) ?? 'off',
      navPadDpadStyle: prefs.getString(_navPadDpadStyleKey) ?? 'dpad',
      navPadRepeatRate: prefs.getInt(_navPadRepeatRateKey) ?? 80,
      navPadHaptic: prefs.getBool(_navPadHapticKey) ?? true,
      navPadButtons: prefs.getString(_navPadButtonsKey),
      fileDownloadPath: prefs.getString(_fileDownloadPathKey) ?? '',
      floatingPadEnabled: prefs.getBool(_floatingPadEnabledKey) ?? false,
      floatingPadSize: prefs.getDouble(_floatingPadSizeKey) ?? 64.0,
      floatingPadCenterKey: prefs.getString(_floatingPadCenterKeyKey) ?? 'Enter',
    );
  }

  Future<void> _saveSetting(String key, dynamic value) async {
    final prefs = await SharedPreferences.getInstance();
    if (value is bool) {
      await prefs.setBool(key, value);
    } else if (value is double) {
      await prefs.setDouble(key, value);
    } else if (value is int) {
      await prefs.setInt(key, value);
    } else if (value is String) {
      await prefs.setString(key, value);
    }
  }

  /// ダークモードを設定
  Future<void> setDarkMode(bool value) async {
    state = state.copyWith(darkMode: value);
    await _saveSetting(_darkModeKey, value);
  }

  /// フォントサイズを設定
  Future<void> setFontSize(double value) async {
    state = state.copyWith(fontSize: value);
    await _saveSetting(_fontSizeKey, value);
  }

  /// フォントファミリーを設定
  Future<void> setFontFamily(String value) async {
    state = state.copyWith(fontFamily: value);
    await _saveSetting(_fontFamilyKey, value);
  }

  /// 生体認証を設定
  Future<void> setRequireBiometricAuth(bool value) async {
    state = state.copyWith(requireBiometricAuth: value);
    await _saveSetting(_biometricKey, value);
  }

  /// 通知を設定
  Future<void> setEnableNotifications(bool value) async {
    state = state.copyWith(enableNotifications: value);
    await _saveSetting(_notificationsKey, value);
  }

  /// バイブレーションを設定
  Future<void> setEnableVibration(bool value) async {
    state = state.copyWith(enableVibration: value);
    await _saveSetting(_vibrationKey, value);
  }

  /// 画面常時オンを設定
  Future<void> setKeepScreenOn(bool value) async {
    state = state.copyWith(keepScreenOn: value);
    await _saveSetting(_keepScreenOnKey, value);
  }

  /// スクロールバック行数を設定
  Future<void> setScrollbackLines(int value) async {
    state = state.copyWith(scrollbackLines: value);
    await _saveSetting(_scrollbackKey, value);
  }

  /// 最小フォントサイズを設定
  Future<void> setMinFontSize(double value) async {
    state = state.copyWith(minFontSize: value);
    await _saveSetting(_minFontSizeKey, value);
  }

  /// 表示調整モードを設定
  Future<void> setAdjustMode(String value) async {
    state = state.copyWith(adjustMode: value);
    await _saveSetting(_adjustModeKey, value);
  }

  /// DirectInputモードを設定
  Future<void> setDirectInputEnabled(bool value) async {
    state = state.copyWith(directInputEnabled: value);
    await _saveSetting(_directInputEnabledKey, value);
  }

  /// DirectInputモードをトグル
  Future<void> toggleDirectInput() async {
    await setDirectInputEnabled(!state.directInputEnabled);
  }

  /// ターミナルカーソル表示設定を設定
  Future<void> setShowTerminalCursor(bool value) async {
    state = state.copyWith(showTerminalCursor: value);
    await _saveSetting(_showTerminalCursorKey, value);
  }

  /// ペインナビゲーション方向の反転を設定
  Future<void> setInvertPaneNavigation(bool value) async {
    state = state.copyWith(invertPaneNavigation: value);
    await _saveSetting(_invertPaneNavKey, value);
  }

  // --- キーオーバーレイ設定のsetter ---
  Future<void> setShowKeyOverlay(bool value) async {
    state = state.copyWith(showKeyOverlay: value);
    await _saveSetting(_showKeyOverlayKey, value);
  }

  Future<void> setKeyOverlayModifier(bool value) async {
    state = state.copyWith(keyOverlayModifier: value);
    await _saveSetting(_keyOverlayModifierKey, value);
  }

  Future<void> setKeyOverlaySpecial(bool value) async {
    state = state.copyWith(keyOverlaySpecial: value);
    await _saveSetting(_keyOverlaySpecialKey, value);
  }

  Future<void> setKeyOverlayArrow(bool value) async {
    state = state.copyWith(keyOverlayArrow: value);
    await _saveSetting(_keyOverlayArrowKey, value);
  }

  Future<void> setKeyOverlayShortcut(bool value) async {
    state = state.copyWith(keyOverlayShortcut: value);
    await _saveSetting(_keyOverlayShortcutKey, value);
  }

  Future<void> setKeyOverlayPosition(String value) async {
    state = state.copyWith(keyOverlayPosition: value);
    await _saveSetting(_keyOverlayPositionKey, value);
  }

  // --- File transfer settings setters ---
  Future<void> setFileRemotePath(String value) async {
    state = state.copyWith(fileRemotePath: value);
    await _saveSetting(_fileRemotePathKey, value);
  }

  Future<void> setFilePathFormat(String value) async {
    state = state.copyWith(filePathFormat: value);
    await _saveSetting(_filePathFormatKey, value);
  }

  Future<void> setFileAutoEnter(bool value) async {
    state = state.copyWith(fileAutoEnter: value);
    await _saveSetting(_fileAutoEnterKey, value);
  }

  Future<void> setFileBracketedPaste(bool value) async {
    state = state.copyWith(fileBracketedPaste: value);
    await _saveSetting(_fileBracketedPasteKey, value);
  }

  // --- 画像転送設定のsetter ---
  Future<void> setImageRemotePath(String value) async {
    state = state.copyWith(imageRemotePath: value);
    await _saveSetting(_imageRemotePathKey, value);
  }

  Future<void> setImageOutputFormat(String value) async {
    state = state.copyWith(imageOutputFormat: value);
    await _saveSetting(_imageOutputFormatKey, value);
  }

  Future<void> setImageJpegQuality(int value) async {
    state = state.copyWith(imageJpegQuality: value);
    await _saveSetting(_imageJpegQualityKey, value);
  }

  Future<void> setImageResizePreset(String value) async {
    state = state.copyWith(imageResizePreset: value);
    await _saveSetting(_imageResizePresetKey, value);
  }

  Future<void> setImageMaxWidth(int value) async {
    state = state.copyWith(imageMaxWidth: value);
    await _saveSetting(_imageMaxWidthKey, value);
  }

  Future<void> setImageMaxHeight(int value) async {
    state = state.copyWith(imageMaxHeight: value);
    await _saveSetting(_imageMaxHeightKey, value);
  }

  Future<void> setImagePathFormat(String value) async {
    state = state.copyWith(imagePathFormat: value);
    await _saveSetting(_imagePathFormatKey, value);
  }

  Future<void> setImageAutoEnter(bool value) async {
    state = state.copyWith(imageAutoEnter: value);
    await _saveSetting(_imageAutoEnterKey, value);
  }

  Future<void> setImageBracketedPaste(bool value) async {
    state = state.copyWith(imageBracketedPaste: value);
    await _saveSetting(_imageBracketedPasteKey, value);
  }

  Future<void> setLocale(String value) async {
    state = state.copyWith(locale: value);
    await _saveSetting(_localeKey, value);
  }

  // --- Navigation Pad settings setters ---
  Future<void> setNavPadMode(String value) async {
    state = state.copyWith(navPadMode: value);
    await _saveSetting(_navPadModeKey, value);
  }

  Future<void> cycleNavPadMode() async {
    final next = switch (state.navPadMode) {
      'full' => 'compact',
      'compact' => 'off',
      _ => 'full',
    };
    await setNavPadMode(next);
  }

  Future<void> setNavPadDpadStyle(String value) async {
    state = state.copyWith(navPadDpadStyle: value);
    await _saveSetting(_navPadDpadStyleKey, value);
  }

  Future<void> setNavPadRepeatRate(int value) async {
    state = state.copyWith(navPadRepeatRate: value);
    await _saveSetting(_navPadRepeatRateKey, value);
  }

  Future<void> setNavPadHaptic(bool value) async {
    state = state.copyWith(navPadHaptic: value);
    await _saveSetting(_navPadHapticKey, value);
  }

  Future<void> setNavPadButtons(String? value) async {
    if (value == null) {
      state = state.copyWith(clearNavPadButtons: true);
      final prefs = await SharedPreferences.getInstance();
      await prefs.remove(_navPadButtonsKey);
    } else {
      state = state.copyWith(navPadButtons: value);
      await _saveSetting(_navPadButtonsKey, value);
    }
  }

  // --- File download settings ---
  Future<void> setFileDownloadPath(String value) async {
    state = state.copyWith(fileDownloadPath: value);
    await _saveSetting(_fileDownloadPathKey, value);
  }

  // --- Experimental features ---
  Future<void> setFloatingPadEnabled(bool value) async {
    state = state.copyWith(floatingPadEnabled: value);
    await _saveSetting(_floatingPadEnabledKey, value);
  }

  Future<void> setFloatingPadSize(double value) async {
    state = state.copyWith(floatingPadSize: value);
    await _saveSetting(_floatingPadSizeKey, value);
  }

  Future<void> setFloatingPadCenterKey(String value) async {
    state = state.copyWith(floatingPadCenterKey: value);
    await _saveSetting(_floatingPadCenterKeyKey, value);
  }

  Future<void> reload() async {
    await _loadSettings();
  }
}

/// 設定プロバイダー
final settingsProvider = NotifierProvider<SettingsNotifier, AppSettings>(() {
  return SettingsNotifier();
});

/// ダークモードプロバイダー（便利アクセス）
final darkModeProvider = Provider<bool>((ref) {
  return ref.watch(settingsProvider).darkMode;
});
