import 'package:flutter/material.dart';
import 'package:flutter_muxpod/l10n/app_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../providers/settings_provider.dart';
import 'dart:convert';
import '../../models/action_bar_config.dart';
import '../../models/action_bar_presets.dart';
import '../../providers/action_bar_provider.dart';
import '../../theme/design_colors.dart';
import 'action_bar_settings_screen.dart';
import '../../widgets/dialogs/font_size_dialog.dart';
import '../../widgets/dialogs/font_family_dialog.dart';
import '../../widgets/dialogs/min_font_size_dialog.dart';
import '../../widgets/dialogs/theme_dialog.dart';
import '../../services/version_info.dart';
import 'licenses_screen.dart';

/// 設定画面
class SettingsScreen extends ConsumerWidget {
  const SettingsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final settings = ref.watch(settingsProvider);
    final l10n = AppLocalizations.of(context)!;

    return Scaffold(
      body: CustomScrollView(
        slivers: [
          _buildAppBar(context),
          SliverPadding(
            padding: const EdgeInsets.fromLTRB(0, 0, 0, 100),
            sliver: SliverList(
              delegate: SliverChildListDelegate([
                _SectionHeader(title: l10n.sectionTerminal),
                SwitchListTile(
                  secondary: const Icon(Icons.abc),
                  title: Text(l10n.settingShowCursor),
                  subtitle: Text(l10n.settingShowCursorDesc),
                  value: settings.showTerminalCursor,
                  onChanged: (value) {
                    ref.read(settingsProvider.notifier).setShowTerminalCursor(value);
                  },
                ),
                ListTile(
                  leading: const Icon(Icons.tune),
                  title: Text(l10n.settingAdjustMode),
                  subtitle: Text(_adjustModeLabel(context, settings.adjustMode)),
                  onTap: () => _showAdjustModePicker(context, ref, settings.adjustMode),
                ),
                ListTile(
                  leading: const Icon(Icons.text_fields),
                  title: Text(l10n.settingFontSize),
                  subtitle: Text(
                    settings.isAutoFit
                        ? '${settings.fontSize.toInt()} pt ${l10n.autoFitEnabled}'
                        : '${settings.fontSize.toInt()} pt',
                  ),
                  enabled: !settings.isAutoFit,
                  onTap: settings.isAutoFit
                      ? null
                      : () async {
                          final size = await showDialog<double>(
                            context: context,
                            builder: (context) => FontSizeDialog(
                              currentSize: settings.fontSize,
                            ),
                          );
                          if (size != null) {
                            ref.read(settingsProvider.notifier).setFontSize(size);
                          }
                        },
                ),
                ListTile(
                  leading: const Icon(Icons.font_download),
                  title: Text(l10n.settingFontFamily),
                  subtitle: Text(settings.fontFamily),
                  onTap: () async {
                    final family = await showDialog<String>(
                      context: context,
                      builder: (context) => FontFamilyDialog(
                        currentFamily: settings.fontFamily,
                      ),
                    );
                    if (family != null) {
                      ref.read(settingsProvider.notifier).setFontFamily(family);
                    }
                  },
                ),
                ListTile(
                  leading: const Icon(Icons.format_size),
                  title: Text(l10n.settingMinFontSize),
                  subtitle: Text(
                    settings.isAutoFit
                        ? '${settings.minFontSize.toInt()} pt ${l10n.autoFitLimit}'
                        : '${settings.minFontSize.toInt()} pt ${l10n.notUsed}',
                  ),
                  enabled: settings.isAutoFit,
                  onTap: settings.isAutoFit
                      ? () async {
                          final size = await showDialog<double>(
                            context: context,
                            builder: (context) => MinFontSizeDialog(
                              currentSize: settings.minFontSize,
                            ),
                          );
                          if (size != null) {
                            ref.read(settingsProvider.notifier).setMinFontSize(size);
                          }
                        }
                      : null,
                ),
                ListTile(
                  leading: const Icon(Icons.history),
                  title: Text(l10n.settingScrollbackLines),
                  subtitle: Text(l10n.scrollbackLinesValue(settings.scrollbackLines)),
                  onTap: () => _showScrollbackPicker(context, ref, settings.scrollbackLines),
                ),
                const Divider(),
                _SectionHeader(title: l10n.settingKeyOverlay),
                SwitchListTile(
                  secondary: const Icon(Icons.visibility),
                  title: Text(l10n.settingKeyOverlay),
                  subtitle: Text(l10n.settingKeyOverlayDesc),
                  value: settings.showKeyOverlay,
                  onChanged: (value) {
                    ref.read(settingsProvider.notifier).setShowKeyOverlay(value);
                  },
                ),
                if (settings.showKeyOverlay) ...[
                  SwitchListTile(
                    secondary: const Icon(Icons.keyboard),
                    title: Text(l10n.keyOverlayModifierKeys),
                    subtitle: Text(l10n.keyOverlayModifierKeysDesc),
                    value: settings.keyOverlayModifier,
                    onChanged: (value) {
                      ref.read(settingsProvider.notifier).setKeyOverlayModifier(value);
                    },
                  ),
                  SwitchListTile(
                    secondary: const Icon(Icons.space_bar),
                    title: Text(l10n.keyOverlaySpecialKeys),
                    subtitle: Text(l10n.keyOverlaySpecialKeysDesc),
                    value: settings.keyOverlaySpecial,
                    onChanged: (value) {
                      ref.read(settingsProvider.notifier).setKeyOverlaySpecial(value);
                    },
                  ),
                  SwitchListTile(
                    secondary: const Icon(Icons.arrow_upward),
                    title: Text(l10n.keyOverlayArrowKeys),
                    subtitle: Text(l10n.keyOverlayArrowKeysDesc),
                    value: settings.keyOverlayArrow,
                    onChanged: (value) {
                      ref.read(settingsProvider.notifier).setKeyOverlayArrow(value);
                    },
                  ),
                  SwitchListTile(
                    secondary: const Icon(Icons.shortcut),
                    title: Text(l10n.keyOverlayShortcutKeys),
                    subtitle: Text(l10n.keyOverlayShortcutKeysDesc),
                    value: settings.keyOverlayShortcut,
                    onChanged: (value) {
                      ref.read(settingsProvider.notifier).setKeyOverlayShortcut(value);
                    },
                  ),
                  ListTile(
                    leading: const Icon(Icons.place),
                    title: Text(l10n.keyOverlayPosition),
                    subtitle: Text(
                      switch (settings.keyOverlayPosition) {
                        'center' => l10n.overlayCenterTerminal,
                        'belowHeader' => l10n.overlayBelowHeader,
                        _ => l10n.overlayAboveKeyboard,
                      },
                    ),
                    onTap: () async {
                      final result = await showDialog<String>(
                        context: context,
                        builder: (context) => SimpleDialog(
                          title: Text(l10n.overlayPositionTitle),
                          children: [
                            _buildPositionOption(context, 'aboveKeyboard', l10n.overlayAboveKeyboard, settings.keyOverlayPosition),
                            _buildPositionOption(context, 'center', l10n.overlayCenterTerminal, settings.keyOverlayPosition),
                            _buildPositionOption(context, 'belowHeader', l10n.overlayBelowHeader, settings.keyOverlayPosition),
                          ],
                        ),
                      );
                      if (result != null) {
                        ref.read(settingsProvider.notifier).setKeyOverlayPosition(result);
                      }
                    },
                  ),
                ],
                const Divider(),
                _SectionHeader(title: l10n.sectionNavPad),
                ListTile(
                  leading: const Icon(Icons.gamepad),
                  title: Text(l10n.navPadMode),
                  subtitle: Text(
                    switch (settings.navPadMode) {
                      'full' => l10n.navPadModeFull,
                      'compact' => l10n.navPadModeCompact,
                      _ => l10n.navPadModeOff,
                    },
                  ),
                  onTap: () async {
                    final result = await showDialog<String>(
                      context: context,
                      builder: (context) => SimpleDialog(
                        title: Text(l10n.navPadMode),
                        children: [
                          SimpleDialogOption(
                            onPressed: () => Navigator.pop(context, 'full'),
                            child: Text(l10n.navPadModeFull),
                          ),
                          SimpleDialogOption(
                            onPressed: () => Navigator.pop(context, 'compact'),
                            child: Text(l10n.navPadModeCompact),
                          ),
                          SimpleDialogOption(
                            onPressed: () => Navigator.pop(context, 'off'),
                            child: Text(l10n.navPadModeOff),
                          ),
                        ],
                      ),
                    );
                    if (result != null) {
                      ref.read(settingsProvider.notifier).setNavPadMode(result);
                    }
                  },
                ),
                if (settings.navPadMode != 'off') ...[
                  ListTile(
                    leading: const Icon(Icons.radio_button_checked),
                    title: Text(l10n.navPadDpadStyle),
                    subtitle: Text(
                      settings.navPadDpadStyle == 'joystick'
                          ? l10n.navPadStyleJoystick
                          : l10n.navPadStyleDpad,
                    ),
                    onTap: () async {
                      final result = await showDialog<String>(
                        context: context,
                        builder: (context) => SimpleDialog(
                          title: Text(l10n.navPadDpadStyle),
                          children: [
                            SimpleDialogOption(
                              onPressed: () => Navigator.pop(context, 'dpad'),
                              child: Text(l10n.navPadStyleDpad),
                            ),
                            SimpleDialogOption(
                              onPressed: () => Navigator.pop(context, 'joystick'),
                              child: Text(l10n.navPadStyleJoystick),
                            ),
                          ],
                        ),
                      );
                      if (result != null) {
                        ref.read(settingsProvider.notifier).setNavPadDpadStyle(result);
                      }
                    },
                  ),
                  ListTile(
                    leading: const Icon(Icons.dashboard_customize),
                    title: Text(l10n.navPadCustomizeButtons),
                    subtitle: Text(l10n.navPadCustomizeButtonsDesc),
                    onTap: () => _showNavPadButtonPicker(context, ref, settings),
                  ),
                  ListTile(
                    leading: const Icon(Icons.speed),
                    title: Text(l10n.navPadRepeatRate),
                    subtitle: Text(l10n.navPadRepeatRateMs(settings.navPadRepeatRate)),
                    onTap: () async {
                      final result = await showDialog<int>(
                        context: context,
                        builder: (context) => SimpleDialog(
                          title: Text(l10n.navPadRepeatRate),
                          children: [
                            for (final ms in [50, 80, 100, 120, 150, 200])
                              SimpleDialogOption(
                                onPressed: () => Navigator.pop(context, ms),
                                child: Text(
                                  l10n.navPadRepeatRateMs(ms),
                                  style: TextStyle(
                                    fontWeight: ms == settings.navPadRepeatRate
                                        ? FontWeight.bold
                                        : FontWeight.normal,
                                  ),
                                ),
                              ),
                          ],
                        ),
                      );
                      if (result != null) {
                        ref.read(settingsProvider.notifier).setNavPadRepeatRate(result);
                      }
                    },
                  ),
                  SwitchListTile(
                    secondary: const Icon(Icons.vibration),
                    title: Text(l10n.navPadHaptic),
                    subtitle: Text(l10n.navPadHapticDesc),
                    value: settings.navPadHaptic,
                    onChanged: (value) {
                      ref.read(settingsProvider.notifier).setNavPadHaptic(value);
                    },
                  ),
                ],
                const Divider(),
                _SectionHeader(title: l10n.sectionExperimental),
                SwitchListTile(
                  secondary: const Icon(Icons.science),
                  title: Text(l10n.floatingPad),
                  subtitle: Text(l10n.floatingPadDesc),
                  value: settings.floatingPadEnabled,
                  onChanged: (value) {
                    ref.read(settingsProvider.notifier).setFloatingPadEnabled(value);
                  },
                ),
                if (settings.floatingPadEnabled) ...[
                  ListTile(
                    leading: const Icon(Icons.photo_size_select_small),
                    title: Text(l10n.floatingPadSize),
                    subtitle: Slider(
                      value: settings.floatingPadSize.clamp(48.0, 128.0),
                      min: 48.0,
                      max: 128.0,
                      divisions: 16,
                      label: '${settings.floatingPadSize.round()}px',
                      onChanged: (v) {
                        ref.read(settingsProvider.notifier).setFloatingPadSize(v);
                      },
                    ),
                    trailing: Text('${settings.floatingPadSize.round()}'),
                  ),
                  ListTile(
                    leading: const Icon(Icons.keyboard_return),
                    title: Text(l10n.floatingPadCenterKey),
                    subtitle: Text(settings.floatingPadCenterKey),
                    onTap: () => _showFloatingPadCenterKeyPicker(context, ref, settings),
                  ),
                ],
                const Divider(),
                _SectionHeader(title: l10n.sectionToolbar),
                Consumer(
                  builder: (context, ref, _) {
                    final abState = ref.watch(actionBarProvider);
                    return ListTile(
                      leading: const Icon(Icons.view_column),
                      title: Text(l10n.activeProfile),
                      subtitle: Text(abState.activeProfile.name),
                      onTap: () => _showProfilePicker(context, ref),
                    );
                  },
                ),
                ListTile(
                  leading: const Icon(Icons.tune),
                  title: Text(l10n.customizeGroups),
                  subtitle: Text(l10n.customizeGroupsDesc),
                  onTap: () {
                    Navigator.push(
                      context,
                      MaterialPageRoute(
                        builder: (context) => const ActionBarSettingsScreen(),
                      ),
                    );
                  },
                ),
                ListTile(
                  leading: const Icon(Icons.add_circle_outline),
                  title: Text(l10n.addNewProfile),
                  subtitle: Text(l10n.addNewProfileDesc),
                  onTap: () => _showCreateProfileDialog(context, ref),
                ),
                const Divider(),
                _SectionHeader(title: l10n.sectionBehavior),
                SwitchListTile(
                  secondary: const Icon(Icons.vibration),
                  title: Text(l10n.settingHapticFeedback),
                  subtitle: Text(l10n.settingHapticFeedbackDesc),
                  value: settings.enableVibration,
                  onChanged: (value) {
                    ref.read(settingsProvider.notifier).setEnableVibration(value);
                  },
                ),
                SwitchListTile(
                  secondary: const Icon(Icons.brightness_high),
                  title: Text(l10n.settingKeepScreenOn),
                  subtitle: Text(l10n.settingKeepScreenOnDesc),
                  value: settings.keepScreenOn,
                  onChanged: (value) {
                    ref.read(settingsProvider.notifier).setKeepScreenOn(value);
                  },
                ),
                SwitchListTile(
                  secondary: const Icon(Icons.swipe),
                  title: Text(l10n.settingInvertPaneNav),
                  subtitle: Text(l10n.settingInvertPaneNavDesc),
                  value: settings.invertPaneNavigation,
                  onChanged: (value) {
                    ref.read(settingsProvider.notifier).setInvertPaneNavigation(value);
                  },
                ),
                const Divider(),
                _SectionHeader(title: l10n.sectionAppearance),
                ListTile(
                  leading: const Icon(Icons.dark_mode),
                  title: Text(l10n.settingTheme),
                  subtitle: Text(settings.darkMode ? l10n.themeDark : l10n.themeLight),
                  onTap: () async {
                    final isDark = await showDialog<bool>(
                      context: context,
                      builder: (context) => ThemeDialog(
                        isDarkMode: settings.darkMode,
                      ),
                    );
                    if (isDark != null) {
                      ref.read(settingsProvider.notifier).setDarkMode(isDark);
                    }
                  },
                ),
                ListTile(
                  leading: const Icon(Icons.language),
                  title: Text(l10n.settingLanguage),
                  subtitle: Text(_localeLabel(context, settings.locale)),
                  onTap: () => _showLocalePicker(context, ref, settings.locale),
                ),
                const Divider(),
                _SectionHeader(title: l10n.sectionImageTransfer),
                ListTile(
                  leading: const Icon(Icons.folder),
                  title: Text(l10n.settingRemotePath),
                  subtitle: Text(settings.imageRemotePath),
                  onTap: () => _showTextInputDialog(
                    context, ref,
                    title: l10n.settingRemotePath,
                    currentValue: settings.imageRemotePath,
                    onSave: (v) => ref.read(settingsProvider.notifier).setImageRemotePath(v),
                  ),
                ),
                ListTile(
                  leading: const Icon(Icons.image),
                  title: Text(l10n.settingImageOutputFormat),
                  subtitle: Text(settings.imageOutputFormat),
                  onTap: () => _showFormatPicker(context, ref, settings.imageOutputFormat),
                ),
                if (settings.imageOutputFormat == 'jpeg')
                  ListTile(
                    leading: const Icon(Icons.high_quality),
                    title: Text(l10n.settingJpegQuality),
                    subtitle: Text(l10n.jpegQualityValue(settings.imageJpegQuality)),
                    onTap: () => _showSliderDialog(
                      context, ref,
                      title: l10n.settingJpegQuality,
                      value: settings.imageJpegQuality.toDouble(),
                      min: 1, max: 100,
                      onSave: (v) => ref.read(settingsProvider.notifier).setImageJpegQuality(v.round()),
                    ),
                  ),
                ListTile(
                  leading: const Icon(Icons.photo_size_select_large),
                  title: Text(l10n.settingImageResize),
                  subtitle: Text(settings.imageResizePreset.toUpperCase()),
                  onTap: () => _showResizePresetPicker(context, ref, settings.imageResizePreset),
                ),
                if (settings.imageResizePreset == 'custom') ...[
                  ListTile(
                    leading: const SizedBox(width: 24),
                    title: Text(l10n.settingMaxWidth),
                    subtitle: Text(l10n.maxWidthValue(settings.imageMaxWidth)),
                    onTap: () => _showNumberInputDialog(
                      context, ref,
                      title: l10n.settingMaxWidth,
                      currentValue: settings.imageMaxWidth,
                      onSave: (v) => ref.read(settingsProvider.notifier).setImageMaxWidth(v),
                    ),
                  ),
                  ListTile(
                    leading: const SizedBox(width: 24),
                    title: Text(l10n.settingMaxHeight),
                    subtitle: Text(l10n.maxHeightValue(settings.imageMaxHeight)),
                    onTap: () => _showNumberInputDialog(
                      context, ref,
                      title: l10n.settingMaxHeight,
                      currentValue: settings.imageMaxHeight,
                      onSave: (v) => ref.read(settingsProvider.notifier).setImageMaxHeight(v),
                    ),
                  ),
                ],
                ListTile(
                  leading: const Icon(Icons.text_format),
                  title: Text(l10n.settingPathFormat),
                  subtitle: Text(settings.imagePathFormat),
                  onTap: () => _showTextInputDialog(
                    context, ref,
                    title: l10n.settingPathFormat,
                    currentValue: settings.imagePathFormat,
                    hint: 'Use {path} as placeholder. e.g. @{path}',
                    onSave: (v) => ref.read(settingsProvider.notifier).setImagePathFormat(v),
                  ),
                ),
                SwitchListTile(
                  secondary: const Icon(Icons.keyboard_return),
                  title: Text(l10n.settingAutoEnter),
                  subtitle: Text(l10n.settingAutoEnterDesc),
                  value: settings.imageAutoEnter,
                  onChanged: (v) => ref.read(settingsProvider.notifier).setImageAutoEnter(v),
                ),
                SwitchListTile(
                  secondary: const Icon(Icons.paste),
                  title: Text(l10n.settingBracketedPaste),
                  subtitle: Text(l10n.settingBracketedPasteDesc),
                  value: settings.imageBracketedPaste,
                  onChanged: (v) => ref.read(settingsProvider.notifier).setImageBracketedPaste(v),
                ),
                const Divider(),
                _SectionHeader(title: l10n.sectionFileTransfer),
                ListTile(
                  leading: const Icon(Icons.folder),
                  title: Text(l10n.settingRemotePath),
                  subtitle: Text(settings.fileRemotePath),
                  onTap: () => _showTextInputDialog(
                    context, ref,
                    title: l10n.settingRemotePath,
                    currentValue: settings.fileRemotePath,
                    onSave: (v) => ref.read(settingsProvider.notifier).setFileRemotePath(v),
                  ),
                ),
                ListTile(
                  leading: const Icon(Icons.download),
                  title: Text(l10n.settingDownloadPath),
                  subtitle: Text(settings.fileDownloadPath.isEmpty
                      ? l10n.settingDownloadPathDefault
                      : settings.fileDownloadPath),
                  onTap: () => _showTextInputDialog(
                    context, ref,
                    title: l10n.settingDownloadPath,
                    currentValue: settings.fileDownloadPath,
                    hint: l10n.settingDownloadPathHint,
                    onSave: (v) => ref.read(settingsProvider.notifier).setFileDownloadPath(v),
                  ),
                ),
                ListTile(
                  leading: const Icon(Icons.text_format),
                  title: Text(l10n.settingPathFormat),
                  subtitle: Text(settings.filePathFormat),
                  onTap: () => _showTextInputDialog(
                    context, ref,
                    title: l10n.settingPathFormat,
                    currentValue: settings.filePathFormat,
                    hint: 'Use {path} as placeholder. e.g. @{path}',
                    onSave: (v) => ref.read(settingsProvider.notifier).setFilePathFormat(v),
                  ),
                ),
                SwitchListTile(
                  secondary: const Icon(Icons.keyboard_return),
                  title: Text(l10n.settingAutoEnter),
                  subtitle: Text(l10n.settingAutoEnterDesc),
                  value: settings.fileAutoEnter,
                  onChanged: (v) => ref.read(settingsProvider.notifier).setFileAutoEnter(v),
                ),
                SwitchListTile(
                  secondary: const Icon(Icons.paste),
                  title: Text(l10n.settingBracketedPaste),
                  subtitle: Text(l10n.settingBracketedPasteDesc),
                  value: settings.fileBracketedPaste,
                  onChanged: (v) => ref.read(settingsProvider.notifier).setFileBracketedPaste(v),
                ),
                const Divider(),
                _SectionHeader(title: l10n.sectionAbout),
                Padding(
                  padding: const EdgeInsets.symmetric(vertical: 12),
                  child: Center(
                    child: ClipRRect(
                      borderRadius: BorderRadius.circular(16),
                      child: Image.asset('assets/icon/icon.png', width: 72, height: 72),
                    ),
                  ),
                ),
                ListTile(
                  leading: const Icon(Icons.info),
                  title: Text(l10n.settingVersion),
                  subtitle: Text(VersionInfo.version),
                ),
                ListTile(
                  leading: const Icon(Icons.code),
                  title: Text(l10n.settingSourceCode),
                  subtitle: Text(l10n.settingSourceCodeUrl),
                  onTap: () async {
                    final url = Uri.parse('https://github.com/physercoe/mux-pod');
                    if (await canLaunchUrl(url)) {
                      await launchUrl(url, mode: LaunchMode.externalApplication);
                    }
                  },
                ),
                ListTile(
                  leading: const Icon(Icons.description),
                  title: Text(l10n.settingLicenses),
                  subtitle: Text(l10n.settingLicensesDesc),
                  onTap: () {
                    Navigator.push(
                      context,
                      MaterialPageRoute(
                        builder: (context) => const LicensesScreen(),
                      ),
                    );
                  },
                ),
              ]),
            ),
          ),
        ],
      ),
    );
  }

  void _showTextInputDialog(
    BuildContext context,
    WidgetRef ref, {
    required String title,
    required String currentValue,
    String? hint,
    required void Function(String) onSave,
  }) {
    final l10n = AppLocalizations.of(context)!;
    final controller = TextEditingController(text: currentValue);
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(title),
        content: TextField(
          controller: controller,
          decoration: InputDecoration(
            hintText: hint,
            border: const OutlineInputBorder(),
          ),
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx), child: Text(l10n.buttonCancel)),
          FilledButton(
            onPressed: () {
              onSave(controller.text.trim());
              Navigator.pop(ctx);
            },
            child: Text(l10n.buttonSave),
          ),
        ],
      ),
    );
  }

  void _showNumberInputDialog(
    BuildContext context,
    WidgetRef ref, {
    required String title,
    required int currentValue,
    required void Function(int) onSave,
  }) {
    final l10n = AppLocalizations.of(context)!;
    final controller = TextEditingController(text: currentValue.toString());
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(title),
        content: TextField(
          controller: controller,
          keyboardType: TextInputType.number,
          decoration: const InputDecoration(border: OutlineInputBorder()),
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx), child: Text(l10n.buttonCancel)),
          FilledButton(
            onPressed: () {
              final v = int.tryParse(controller.text.trim());
              if (v != null) onSave(v);
              Navigator.pop(ctx);
            },
            child: Text(l10n.buttonSave),
          ),
        ],
      ),
    );
  }

  void _showSliderDialog(
    BuildContext context,
    WidgetRef ref, {
    required String title,
    required double value,
    required double min,
    required double max,
    required void Function(double) onSave,
  }) {
    final l10n = AppLocalizations.of(context)!;
    var current = value;
    showDialog(
      context: context,
      builder: (ctx) => StatefulBuilder(
        builder: (ctx, setState) => AlertDialog(
          title: Text(title),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Slider(value: current, min: min, max: max, onChanged: (v) => setState(() => current = v)),
              Text('${current.round()}'),
            ],
          ),
          actions: [
            TextButton(onPressed: () => Navigator.pop(ctx), child: Text(l10n.buttonCancel)),
            FilledButton(
              onPressed: () {
                onSave(current);
                Navigator.pop(ctx);
              },
              child: Text(l10n.buttonSave),
            ),
          ],
        ),
      ),
    );
  }

  void _showFormatPicker(BuildContext context, WidgetRef ref, String current) {
    final l10n = AppLocalizations.of(context)!;
    showDialog(
      context: context,
      builder: (ctx) => SimpleDialog(
        title: Text(l10n.outputFormatTitle),
        children: [
          for (final format in ['original', 'png', 'jpeg'])
            RadioListTile<String>(
              title: Text(format.toUpperCase()),
              value: format,
              groupValue: current,
              onChanged: (v) {
                if (v != null) ref.read(settingsProvider.notifier).setImageOutputFormat(v);
                Navigator.pop(ctx);
              },
            ),
        ],
      ),
    );
  }

  String _adjustModeLabel(BuildContext context, String mode) {
    final l10n = AppLocalizations.of(context)!;
    switch (mode) {
      case 'autoFit':
        return l10n.adjustModeAutoFit;
      case 'autoResize':
        return l10n.adjustModeAutoResize;
      default:
        return l10n.adjustModeNone;
    }
  }

  void _showAdjustModePicker(BuildContext context, WidgetRef ref, String current) {
    final l10n = AppLocalizations.of(context)!;
    showDialog(
      context: context,
      builder: (ctx) => SimpleDialog(
        title: Text(l10n.adjustModeTitle),
        children: [
          for (final entry in [
            ('none', l10n.adjustModeNone, l10n.adjustModeNoneDesc),
            ('autoFit', l10n.adjustModeAutoFit, l10n.adjustModeAutoFitDesc),
            ('autoResize', l10n.adjustModeAutoResize, l10n.adjustModeAutoResizeDesc),
          ])
            RadioListTile<String>(
              title: Text(entry.$2),
              subtitle: Text(entry.$3),
              value: entry.$1,
              groupValue: current,
              onChanged: (v) {
                if (v != null) ref.read(settingsProvider.notifier).setAdjustMode(v);
                Navigator.pop(ctx);
              },
            ),
        ],
      ),
    );
  }

  void _showScrollbackPicker(BuildContext context, WidgetRef ref, int current) {
    final l10n = AppLocalizations.of(context)!;
    showDialog(
      context: context,
      builder: (ctx) => SimpleDialog(
        title: Text(l10n.scrollbackLinesTitle),
        children: [
          for (final value in [50, 100, 200, 500, 1000, 5000])
            RadioListTile<int>(
              title: Text(l10n.scrollbackLinesDisplay(value)),
              value: value,
              groupValue: current,
              onChanged: (v) {
                if (v != null) ref.read(settingsProvider.notifier).setScrollbackLines(v);
                Navigator.pop(ctx);
              },
            ),
        ],
      ),
    );
  }

  void _showResizePresetPicker(BuildContext context, WidgetRef ref, String current) {
    final l10n = AppLocalizations.of(context)!;
    showDialog(
      context: context,
      builder: (ctx) => SimpleDialog(
        title: Text(l10n.resizePresetTitle),
        children: [
          for (final entry in [
            ('original', l10n.resizePresetOriginal),
            ('small', l10n.resizeSmall),
            ('medium', l10n.resizeMedium),
            ('large', l10n.resizeLarge),
            ('custom', l10n.resizeCustom),
          ])
            RadioListTile<String>(
              title: Text(entry.$2),
              value: entry.$1,
              groupValue: current,
              onChanged: (v) {
                if (v != null) ref.read(settingsProvider.notifier).setImageResizePreset(v);
                Navigator.pop(ctx);
              },
            ),
        ],
      ),
    );
  }

  String _localeLabel(BuildContext context, String locale) {
    switch (locale) {
      case 'en':
        return 'English';
      case 'zh':
        return '中文 (简体)';
      default:
        return AppLocalizations.of(context)!.systemDefault;
    }
  }

  void _showLocalePicker(BuildContext context, WidgetRef ref, String current) {
    final l10n = AppLocalizations.of(context)!;
    showDialog(
      context: context,
      builder: (ctx) => SimpleDialog(
        title: Text(l10n.settingLanguage),
        children: [
          for (final entry in [
            ('system', l10n.systemDefault),
            ('en', 'English'),
            ('zh', '中文 (简体)'),
          ])
            RadioListTile<String>(
              title: Text(entry.$2),
              value: entry.$1,
              groupValue: current,
              onChanged: (v) {
                if (v != null) ref.read(settingsProvider.notifier).setLocale(v);
                Navigator.pop(ctx);
              },
            ),
        ],
      ),
    );
  }

  void _showNavPadButtonPicker(
    BuildContext context,
    WidgetRef ref,
    AppSettings settings,
  ) {
    final l10n = AppLocalizations.of(context)!;

    // Parse current buttons
    List<ActionBarButton> currentButtons;
    if (settings.navPadButtons != null) {
      try {
        final list = jsonDecode(settings.navPadButtons!) as List;
        currentButtons = list
            .map((e) => ActionBarButton.fromJson(e as Map<String, dynamic>))
            .toList();
      } catch (_) {
        currentButtons = [];
      }
    } else {
      currentButtons = [];
    }

    // Default buttons for display
    final defaults = [
      ActionBarButton(id: 'np-esc', label: 'ESC', type: ActionBarButtonType.specialKey, value: 'Escape'),
      ActionBarButton(id: 'np-tab', label: 'TAB', type: ActionBarButtonType.specialKey, value: 'Tab'),
      ActionBarButton(id: 'np-cc', label: 'C-C', type: ActionBarButtonType.ctrlCombo, value: 'C-c'),
      ActionBarButton(id: 'np-ent', label: 'ENT', type: ActionBarButtonType.specialKey, value: 'Enter'),
    ];

    final buttons = currentButtons.length == 4 ? currentButtons : defaults;
    final isCustom = settings.navPadButtons != null;

    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      useSafeArea: true,
      builder: (ctx) {
        return StatefulBuilder(
          builder: (context, setSheetState) {
            return DraggableScrollableSheet(
              initialChildSize: 0.6,
              minChildSize: 0.3,
              maxChildSize: 0.9,
              expand: false,
              builder: (context, scrollController) {
                return Column(
                  children: [
                    Container(
                      margin: const EdgeInsets.only(top: 8, bottom: 4),
                      width: 40,
                      height: 4,
                      decoration: BoxDecoration(
                        color: Theme.of(context).colorScheme.onSurfaceVariant.withValues(alpha: 0.4),
                        borderRadius: BorderRadius.circular(2),
                      ),
                    ),
                    Padding(
                      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
                      child: Row(
                        children: [
                          Text(l10n.navPadCustomizeButtons, style: Theme.of(context).textTheme.titleMedium),
                          const Spacer(),
                          if (isCustom)
                            TextButton(
                              onPressed: () {
                                ref.read(settingsProvider.notifier).setNavPadButtons(null);
                                Navigator.pop(ctx);
                              },
                              child: Text(l10n.resetToDefault),
                            ),
                        ],
                      ),
                    ),
                    const Divider(height: 1),
                    // Current 4 buttons
                    Padding(
                      padding: const EdgeInsets.all(16),
                      child: Row(
                        mainAxisAlignment: MainAxisAlignment.spaceEvenly,
                        children: [
                          for (var i = 0; i < 4; i++)
                            _NavPadSlot(
                              index: i,
                              button: buttons[i],
                              onReplace: (newButton) {
                                buttons[i] = newButton;
                                // Save all 4 buttons
                                final json = jsonEncode(buttons.map((b) => b.toJson()).toList());
                                ref.read(settingsProvider.notifier).setNavPadButtons(json);
                                setSheetState(() {});
                              },
                            ),
                        ],
                      ),
                    ),
                    const Divider(height: 1),
                    Padding(
                      padding: const EdgeInsets.fromLTRB(16, 8, 16, 4),
                      child: Align(
                        alignment: Alignment.centerLeft,
                        child: Text(
                          l10n.navPadPickButton,
                          style: Theme.of(context).textTheme.labelMedium?.copyWith(
                                color: DesignColors.primary,
                                fontWeight: FontWeight.w600,
                              ),
                        ),
                      ),
                    ),
                    // Button catalog
                    Expanded(
                      child: _NavPadButtonCatalog(
                        scrollController: scrollController,
                        onPick: (button) {
                          // Find first slot to show visual feedback
                          // For now, just show a message — user taps a slot first
                          ScaffoldMessenger.of(context).showSnackBar(
                            SnackBar(
                              content: Text('Tap a button slot above, then pick a replacement'),
                              duration: const Duration(seconds: 2),
                            ),
                          );
                        },
                      ),
                    ),
                  ],
                );
              },
            );
          },
        );
      },
    );
  }

  /// Show a picker for the floating joystick center-button key.
  /// Offers common terminal keys plus a text field for arbitrary values.
  void _showFloatingPadCenterKeyPicker(
      BuildContext context, WidgetRef ref, AppSettings settings) {
    const commonKeys = <(String, String)>[
      ('Enter', 'Enter (⏎)'),
      ('Escape', 'Escape (ESC)'),
      ('Tab', 'Tab (⇥)'),
      ('Space', 'Space'),
      ('BSpace', 'Backspace'),
      ('C-c', 'Ctrl+C'),
      ('C-d', 'Ctrl+D'),
      ('C-z', 'Ctrl+Z'),
    ];
    final customController = TextEditingController(
      text: commonKeys.any((k) => k.$1 == settings.floatingPadCenterKey)
          ? ''
          : settings.floatingPadCenterKey,
    );
    showDialog(
      context: context,
      builder: (ctx) => SimpleDialog(
        title: Text(AppLocalizations.of(context)!.floatingPadCenterKey),
        children: [
          for (final (value, label) in commonKeys)
            RadioListTile<String>(
              title: Text(label),
              value: value,
              groupValue: settings.floatingPadCenterKey,
              onChanged: (v) {
                if (v != null) {
                  ref.read(settingsProvider.notifier).setFloatingPadCenterKey(v);
                }
                Navigator.pop(ctx);
              },
            ),
          const Divider(),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 8),
            child: Row(
              children: [
                Expanded(
                  child: TextField(
                    controller: customController,
                    decoration: const InputDecoration(
                      labelText: 'Custom (tmux key name)',
                      hintText: 'e.g. C-x or F5',
                      border: OutlineInputBorder(),
                      isDense: true,
                    ),
                  ),
                ),
                const SizedBox(width: 8),
                FilledButton(
                  onPressed: () {
                    final v = customController.text.trim();
                    if (v.isNotEmpty) {
                      ref.read(settingsProvider.notifier).setFloatingPadCenterKey(v);
                    }
                    Navigator.pop(ctx);
                  },
                  child: const Text('OK'),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }

  void _showProfilePicker(BuildContext context, WidgetRef ref) {
    final state = ref.read(actionBarProvider);
    showDialog(
      context: context,
      builder: (ctx) => SimpleDialog(
        title: Text(AppLocalizations.of(context)!.selectProfile),
        children: [
          for (final profile in state.profiles)
            RadioListTile<String>(
              title: Text(profile.name),
              subtitle: profile.isBuiltIn ? Text(AppLocalizations.of(context)!.builtIn) : null,
              value: profile.id,
              groupValue: state.activeProfileId,
              onChanged: (v) {
                if (v != null) {
                  ref.read(actionBarProvider.notifier).setActiveProfile(v);
                }
                Navigator.pop(ctx);
              },
            ),
        ],
      ),
    );
  }

  void _showCreateProfileDialog(BuildContext context, WidgetRef ref) {
    final nameController = TextEditingController();
    final l10n = AppLocalizations.of(context)!;
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.newProfile),
        content: TextField(
          controller: nameController,
          autofocus: true,
          decoration: InputDecoration(
            labelText: l10n.profileName,
            border: const OutlineInputBorder(),
            isDense: true,
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: Text(l10n.buttonCancel),
          ),
          FilledButton(
            onPressed: () {
              final name = nameController.text.trim();
              if (name.isNotEmpty) {
                final profile = ActionBarProfile(
                  id: 'custom_${DateTime.now().millisecondsSinceEpoch}',
                  name: name,
                  groups: const [
                    ActionBarGroup(
                      id: 'default-keys',
                      name: 'Keys',
                      buttons: [
                        ActionBarButton(
                          id: 'esc',
                          label: 'ESC',
                          type: ActionBarButtonType.specialKey,
                          value: 'Escape',
                        ),
                        ActionBarButton(
                          id: 'tab',
                          label: 'TAB',
                          type: ActionBarButtonType.specialKey,
                          value: 'Tab',
                        ),
                        ActionBarButton(
                          id: 'ctrl',
                          label: 'CTRL',
                          type: ActionBarButtonType.modifier,
                          value: 'ctrl',
                        ),
                        ActionBarButton(
                          id: 'alt',
                          label: 'ALT',
                          type: ActionBarButtonType.modifier,
                          value: 'alt',
                        ),
                        ActionBarButton(
                          id: 'enter',
                          label: 'RET',
                          type: ActionBarButtonType.specialKey,
                          value: 'Enter',
                        ),
                      ],
                    ),
                  ],
                );
                ref.read(actionBarProvider.notifier).addCustomProfile(profile);
                ref.read(actionBarProvider.notifier).setActiveProfile(profile.id);
                Navigator.pop(ctx);
              }
            },
            child: Text(l10n.buttonCreate),
          ),
        ],
      ),
    );
  }

  Widget _buildAppBar(BuildContext context) {
    final colorScheme = Theme.of(context).colorScheme;
    return SliverAppBar(
      floating: true,
      pinned: true,
      expandedHeight: 100,
      backgroundColor: colorScheme.surface.withValues(alpha: 0.95),
      surfaceTintColor: Colors.transparent,
      flexibleSpace: FlexibleSpaceBar(
        titlePadding: const EdgeInsets.only(left: 24, bottom: 16),
        title: Text(
          AppLocalizations.of(context)!.tabSettings,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 24,
            fontWeight: FontWeight.w700,
            color: colorScheme.onSurface,
            letterSpacing: -0.5,
          ),
        ),
      ),
    );
  }

  Widget _buildPositionOption(
    BuildContext context,
    String value,
    String label,
    String currentValue,
  ) {
    return SimpleDialogOption(
      onPressed: () => Navigator.pop(context, value),
      child: Row(
        children: [
          Icon(
            value == currentValue ? Icons.radio_button_checked : Icons.radio_button_unchecked,
            size: 20,
          ),
          const SizedBox(width: 12),
          Text(label),
        ],
      ),
    );
  }
}

/// A single slot in the nav pad button customization grid.
/// Tapping opens the button catalog to pick a replacement.
class _NavPadSlot extends StatelessWidget {
  final int index;
  final ActionBarButton button;
  final void Function(ActionBarButton) onReplace;

  const _NavPadSlot({
    required this.index,
    required this.button,
    required this.onReplace,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return GestureDetector(
      onTap: () => _pickButton(context),
      child: Container(
        width: 64,
        height: 48,
        decoration: BoxDecoration(
          color: isDark ? DesignColors.keyBackground : DesignColors.keyBackgroundLight,
          borderRadius: BorderRadius.circular(8),
          border: Border.all(
            color: isDark ? DesignColors.borderDark : DesignColors.borderLight,
          ),
        ),
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            Text(
              button.label,
              style: TextStyle(
                fontSize: 12,
                fontWeight: FontWeight.w700,
                color: isDark ? DesignColors.textPrimary : DesignColors.textPrimaryLight,
              ),
            ),
            Text(
              button.value,
              style: TextStyle(
                fontSize: 8,
                color: isDark
                    ? DesignColors.textPrimary.withValues(alpha: 0.5)
                    : DesignColors.textPrimaryLight.withValues(alpha: 0.5),
              ),
            ),
          ],
        ),
      ),
    );
  }

  void _pickButton(BuildContext context) {
    final catalogByType = ActionBarPresets.buttonCatalogByType;
    const typeOrder = [
      ActionBarButtonType.specialKey,
      ActionBarButtonType.ctrlCombo,
      ActionBarButtonType.altCombo,
      ActionBarButtonType.shiftCombo,
      ActionBarButtonType.literal,
      ActionBarButtonType.confirm,
    ];

    String typeLabel(ActionBarButtonType type) {
      return switch (type) {
        ActionBarButtonType.specialKey => 'Special Keys',
        ActionBarButtonType.ctrlCombo => 'Ctrl Combos',
        ActionBarButtonType.altCombo => 'Alt Combos',
        ActionBarButtonType.shiftCombo => 'Shift Combos',
        ActionBarButtonType.literal => 'Characters',
        ActionBarButtonType.modifier => 'Modifiers',
        ActionBarButtonType.action => 'Actions',
        ActionBarButtonType.confirm => 'Confirm (y/n)',
      };
    }

    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      useSafeArea: true,
      builder: (ctx) {
        return DraggableScrollableSheet(
          initialChildSize: 0.7,
          minChildSize: 0.4,
          maxChildSize: 0.95,
          expand: false,
          builder: (context, scrollController) {
            return Column(
              children: [
                Container(
                  margin: const EdgeInsets.only(top: 8, bottom: 4),
                  width: 40,
                  height: 4,
                  decoration: BoxDecoration(
                    color: Theme.of(context).colorScheme.onSurfaceVariant.withValues(alpha: 0.4),
                    borderRadius: BorderRadius.circular(2),
                  ),
                ),
                Padding(
                  padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
                  child: Text(
                    'Pick button for slot ${index + 1}',
                    style: Theme.of(context).textTheme.titleMedium,
                  ),
                ),
                const Divider(height: 1),
                Expanded(
                  child: ListView(
                    controller: scrollController,
                    children: [
                      for (final type in typeOrder)
                        if (catalogByType.containsKey(type)) ...[
                          Padding(
                            padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
                            child: Text(
                              typeLabel(type),
                              style: Theme.of(context).textTheme.labelMedium?.copyWith(
                                    color: DesignColors.primary,
                                    fontWeight: FontWeight.w600,
                                  ),
                            ),
                          ),
                          Padding(
                            padding: const EdgeInsets.symmetric(horizontal: 12),
                            child: Wrap(
                              spacing: 6,
                              runSpacing: 4,
                              children: [
                                for (final catButton in catalogByType[type]!)
                                  ActionChip(
                                    label: Text(catButton.label),
                                    tooltip: '${catButton.value} — ${catButton.displayDescription}',
                                    onPressed: () {
                                      final newButton = ActionBarButton(
                                        id: 'np_${DateTime.now().millisecondsSinceEpoch}',
                                        label: catButton.label,
                                        type: catButton.type,
                                        value: catButton.value,
                                        longPressValue: catButton.longPressValue,
                                        iconName: catButton.iconName,
                                        description: catButton.description,
                                      );
                                      onReplace(newButton);
                                      Navigator.pop(ctx);
                                    },
                                  ),
                              ],
                            ),
                          ),
                        ],
                      const SizedBox(height: 16),
                    ],
                  ),
                ),
              ],
            );
          },
        );
      },
    );
  }
}

/// Placeholder catalog in the main sheet — shows hint to tap slots.
class _NavPadButtonCatalog extends StatelessWidget {
  final ScrollController scrollController;
  final void Function(ActionBarButton) onPick;

  const _NavPadButtonCatalog({
    required this.scrollController,
    required this.onPick,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Text(
          'Tap a button slot above to replace it from the catalog',
          textAlign: TextAlign.center,
          style: TextStyle(
            color: isDark ? Colors.white38 : Colors.black38,
            fontSize: 14,
          ),
        ),
      ),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  final String title;

  const _SectionHeader({required this.title});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
      child: Text(
        title.toUpperCase(),
        style: GoogleFonts.spaceGrotesk(
          fontSize: 11,
          fontWeight: FontWeight.w700,
          color: isDark ? DesignColors.textMuted : DesignColors.textMutedLight,
          letterSpacing: 1.5,
        ),
      ),
    );
  }
}

