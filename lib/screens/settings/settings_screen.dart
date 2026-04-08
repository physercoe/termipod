import 'package:flutter/material.dart';
import 'package:flutter_muxpod/l10n/app_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../providers/settings_provider.dart';
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
                  subtitle: Text(_adjustModeLabel(settings.adjustMode)),
                  onTap: () => _showAdjustModePicker(context, ref, settings.adjustMode),
                ),
                ListTile(
                  leading: const Icon(Icons.text_fields),
                  title: Text(l10n.settingFontSize),
                  subtitle: Text(
                    settings.isAutoFit
                        ? '${settings.fontSize.toInt()} pt (auto-fit enabled)'
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
                        ? '${settings.minFontSize.toInt()} pt (auto-fit limit)'
                        : '${settings.minFontSize.toInt()} pt (not used)',
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
                        'center' => 'Center of terminal',
                        'belowHeader' => 'Below header',
                        _ => 'Above keyboard',
                      },
                    ),
                    onTap: () async {
                      final result = await showDialog<String>(
                        context: context,
                        builder: (context) => SimpleDialog(
                          title: Text(l10n.overlayPositionTitle),
                          children: [
                            _buildPositionOption(context, 'aboveKeyboard', 'Above Keyboard', settings.keyOverlayPosition),
                            _buildPositionOption(context, 'center', 'Center of Terminal', settings.keyOverlayPosition),
                            _buildPositionOption(context, 'belowHeader', 'Below Header', settings.keyOverlayPosition),
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
                const _SectionHeader(title: 'Toolbar'),
                Consumer(
                  builder: (context, ref, _) {
                    final abState = ref.watch(actionBarProvider);
                    return ListTile(
                      leading: const Icon(Icons.view_column),
                      title: const Text('Active Profile'),
                      subtitle: Text(abState.activeProfile.name),
                      onTap: () => _showProfilePicker(context, ref),
                    );
                  },
                ),
                ListTile(
                  leading: const Icon(Icons.tune),
                  title: const Text('Customize Groups'),
                  subtitle: const Text('Reorder and edit button groups'),
                  onTap: () {
                    Navigator.push(
                      context,
                      MaterialPageRoute(
                        builder: (context) => const ActionBarSettingsScreen(),
                      ),
                    );
                  },
                ),
                const Divider(),
                const _SectionHeader(title: 'Behavior'),
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
                const _SectionHeader(title: 'Appearance'),
                ListTile(
                  leading: const Icon(Icons.dark_mode),
                  title: Text(l10n.settingTheme),
                  subtitle: Text(settings.darkMode ? 'Dark' : 'Light'),
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
                  subtitle: Text(_localeLabel(settings.locale)),
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

  String _adjustModeLabel(String mode) {
    switch (mode) {
      case 'autoFit':
        return 'Auto Fit';
      case 'autoResize':
        return 'Auto Resize';
      default:
        return 'None';
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
            ('none', 'None', 'Manual font and pane size'),
            ('autoFit', 'Auto Fit', 'Adjust font size to fit screen width'),
            ('autoResize', 'Auto Resize', 'Resize tmux pane to fit screen'),
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
            ('original', 'Original'),
            ('small', 'Small (480px)'),
            ('medium', 'Medium (1080px)'),
            ('large', 'Large (1920px)'),
            ('custom', 'Custom'),
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

  String _localeLabel(String locale) {
    switch (locale) {
      case 'en':
        return 'English';
      case 'zh':
        return '中文 (简体)';
      default:
        return 'System Default';
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
            ('system', 'System Default'),
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

  void _showProfilePicker(BuildContext context, WidgetRef ref) {
    final state = ref.read(actionBarProvider);
    showDialog(
      context: context,
      builder: (ctx) => SimpleDialog(
        title: const Text('Select Profile'),
        children: [
          for (final profile in state.profiles)
            RadioListTile<String>(
              title: Text(profile.name),
              subtitle: profile.isBuiltIn ? const Text('Built-in') : null,
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
          'Settings',
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

