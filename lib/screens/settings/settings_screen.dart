import 'package:flutter/material.dart';
import 'package:termipod/l10n/app_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:url_launcher/url_launcher.dart';
import 'package:file_picker/file_picker.dart';
import 'package:share_plus/share_plus.dart';
import 'dart:io';
import 'package:path_provider/path_provider.dart';

import '../../providers/hub_provider.dart';
import '../../providers/settings_provider.dart';
import '../../providers/key_provider.dart';
import '../../services/data_port_service.dart';
import '../../services/notifications/local_notifications.dart';
import '../../services/public_file_store.dart';
import 'dart:convert';
import '../../models/action_bar_config.dart';
import '../../models/action_bar_presets.dart';
import '../../providers/action_bar_provider.dart';
import '../../theme/design_colors.dart';
import 'action_bar_settings_screen.dart';
import 'file_browser_screen.dart';
import '../vault/vault_screen.dart';
import '../../widgets/dialogs/font_size_dialog.dart';
import '../../widgets/dialogs/font_family_dialog.dart';
import '../../widgets/dialogs/min_font_size_dialog.dart';
import '../../widgets/dialogs/theme_dialog.dart';
import '../../services/update_service.dart';
import '../../services/version_info.dart';
import 'licenses_screen.dart';
import 'voice_settings_screen.dart';

/// Settings home — six-category landing screen.
///
/// IA defined by docs/discussions/settings-and-team-scope-ia.md (the
/// two-scope mental model: device-scoped Settings vs team-scoped
/// TeamSwitcher). Each category card opens [_CategoryPage] which
/// dispatches to the appropriate row set. Team-shared settings
/// (profiles / templates / members / policies / channels) intentionally
/// live in the TeamSwitcher pill at the top of every Tier-1 tab —
/// not duplicated here.
///
/// Sub-screens for dense areas (Action-bar preset, Voice) remain as
/// dedicated routes (`ActionBarSettingsScreen`, `VoiceSettingsScreen`).
/// NavPad config + Image/File transfer + Floating pad render inline
/// in their parent category — splitting them off would be marginal
/// extra ergonomics at the cost of one more tap each.
class SettingsScreen extends ConsumerWidget {
  const SettingsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final settings = ref.watch(settingsProvider);
    final l10n = AppLocalizations.of(context)!;

    return Scaffold(
      body: CustomScrollView(
        slivers: [
          _buildAppBar(context, l10n),
          SliverPadding(
            padding: const EdgeInsets.fromLTRB(0, 8, 0, 100),
            sliver: SliverList(
              delegate: SliverChildListDelegate([
                _CategoryCard(
                  category: _Category.display,
                  icon: Icons.palette_outlined,
                  title: l10n.scopeDisplay,
                  subtitle:
                      '${settings.darkMode ? l10n.themeDark : l10n.themeLight}'
                      ' · ${_localeLabelStatic(context, settings.locale)}',
                ),
                _CategoryCard(
                  category: _Category.input,
                  icon: Icons.touch_app_outlined,
                  title: l10n.scopeInput,
                  subtitle: _inputSubtitle(l10n, settings),
                ),
                _CategoryCard(
                  category: _Category.filesMedia,
                  icon: Icons.attach_file,
                  title: l10n.scopeFilesMedia,
                  subtitle: settings.imageOutputFormat.toUpperCase(),
                ),
                _CategoryCard(
                  category: _Category.data,
                  icon: Icons.storage_outlined,
                  title: l10n.scopeData,
                  subtitle: null,
                ),
                _CategoryCard(
                  category: _Category.system,
                  icon: Icons.notifications_outlined,
                  title: l10n.scopeSystem,
                  subtitle: settings.enableNotifications
                      ? l10n.scopeSystemNotificationsOn
                      : l10n.scopeSystemNotificationsOff,
                ),
                _CategoryCard(
                  category: _Category.about,
                  icon: Icons.info_outline,
                  title: l10n.scopeAbout,
                  subtitle: VersionInfo.version,
                ),
              ]),
            ),
          ),
        ],
      ),
    );
  }

  String _inputSubtitle(AppLocalizations l10n, AppSettings settings) {
    // Surface NavPad mode as the representative subtitle. Voice state
    // lives in a separate provider (voiceSettingsProvider) and reading
    // it here would couple the home rebuild to that provider for
    // marginal scent — defer until W2 if the subtitle feels thin.
    return settings.navPadMode != 'off'
        ? l10n.navPadModeCompact
        : l10n.navPadModeOff;
  }

  static String _localeLabelStatic(BuildContext context, String locale) {
    switch (locale) {
      case 'en':
        return 'English';
      case 'zh':
        return '中文 (简体)';
      default:
        return AppLocalizations.of(context)!.systemDefault;
    }
  }

  Widget _buildAppBar(BuildContext context, AppLocalizations l10n) {
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
          l10n.tabSettings,
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
}

/// One row on the settings home — the six category cards.
class _CategoryCard extends StatelessWidget {
  final _Category category;
  final IconData icon;
  final String title;
  final String? subtitle;

  const _CategoryCard({
    required this.category,
    required this.icon,
    required this.title,
    required this.subtitle,
  });

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return ListTile(
      leading: Container(
        width: 36,
        height: 36,
        decoration: BoxDecoration(
          color: scheme.primaryContainer.withValues(alpha: 0.35),
          borderRadius: BorderRadius.circular(10),
        ),
        child: Icon(icon, color: scheme.primary, size: 20),
      ),
      title: Text(
        title,
        style: GoogleFonts.spaceGrotesk(
          fontSize: 16,
          fontWeight: FontWeight.w600,
        ),
      ),
      subtitle: (subtitle == null || subtitle!.isEmpty)
          ? null
          : Text(
              subtitle!,
              style: TextStyle(
                color: scheme.onSurfaceVariant,
                fontSize: 12,
              ),
            ),
      trailing: const Icon(Icons.chevron_right),
      onTap: () {
        Navigator.push(
          context,
          MaterialPageRoute(
            builder: (_) => _CategoryPage(category: category),
          ),
        );
      },
    );
  }
}

/// The six top-level categories on the Settings home. Each maps to a
/// page rendered by [_CategoryPage].
enum _Category { display, input, filesMedia, data, system, about }

/// Renders one category's rows. Dispatches on the category enum so all
/// row-building + helper code stays in this file (no cross-file
/// imports for picker dialogs, dialog widgets, or state handlers).
///
/// Single file is a deliberate choice over the 10-file split the plan
/// originally proposed — it keeps the wedge low-risk without a local
/// Dart toolchain to validate refactors. A later polish wedge can
/// split this file by category if maintenance friction warrants.
class _CategoryPage extends ConsumerWidget {
  final _Category category;
  const _CategoryPage({required this.category});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final settings = ref.watch(settingsProvider);
    final l10n = AppLocalizations.of(context)!;
    return Scaffold(
      appBar: AppBar(
        title: Text(_titleFor(category, l10n)),
      ),
      body: ListView(
        padding: const EdgeInsets.only(bottom: 100),
        children: switch (category) {
          _Category.display => _buildDisplay(context, ref, settings, l10n),
          _Category.input => _buildInput(context, ref, settings, l10n),
          _Category.filesMedia => _buildFilesMedia(context, ref, settings, l10n),
          _Category.data => _buildData(context, ref, l10n),
          _Category.system => _buildSystem(context, ref, settings, l10n),
          _Category.about => _buildAbout(context, ref, l10n),
        },
      ),
    );
  }

  static String _titleFor(_Category c, AppLocalizations l10n) {
    return switch (c) {
      _Category.display => l10n.scopeDisplay,
      _Category.input => l10n.scopeInput,
      _Category.filesMedia => l10n.scopeFilesMedia,
      _Category.data => l10n.scopeData,
      _Category.system => l10n.scopeSystem,
      _Category.about => l10n.scopeAbout,
    };
  }

  // ─── Display ────────────────────────────────────────────────────
  List<Widget> _buildDisplay(BuildContext context, WidgetRef ref,
      AppSettings settings, AppLocalizations l10n) {
    return [
      ListTile(
        leading: const Icon(Icons.dark_mode),
        title: Text(l10n.settingTheme),
        subtitle: Text(settings.darkMode ? l10n.themeDark : l10n.themeLight),
        onTap: () async {
          final isDark = await showDialog<bool>(
            context: context,
            builder: (context) => ThemeDialog(isDarkMode: settings.darkMode),
          );
          if (isDark != null) {
            ref.read(settingsProvider.notifier).setDarkMode(isDark);
          }
        },
      ),
      ListTile(
        leading: const Icon(Icons.language),
        title: Text(l10n.settingLanguage),
        subtitle: Text(SettingsScreen._localeLabelStatic(context, settings.locale)),
        onTap: () => _showLocalePicker(context, ref, settings.locale),
      ),
      const Divider(),
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
                  builder: (context) =>
                      FontSizeDialog(currentSize: settings.fontSize),
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
            builder: (context) =>
                FontFamilyDialog(currentFamily: settings.fontFamily),
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
                  builder: (context) =>
                      MinFontSizeDialog(currentSize: settings.minFontSize),
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
        onTap: () =>
            _showScrollbackPicker(context, ref, settings.scrollbackLines),
      ),
    ];
  }

  // ─── Input ──────────────────────────────────────────────────────
  List<Widget> _buildInput(BuildContext context, WidgetRef ref,
      AppSettings settings, AppLocalizations l10n) {
    return [
      // ── NavPad ──────────────────────────────────────────────────
      ListTile(
        leading: const Icon(Icons.gamepad),
        title: Text(l10n.navPadMode),
        subtitle: Text(
          switch (settings.navPadMode) {
            'off' => l10n.navPadModeOff,
            _ => l10n.navPadModeCompact,
          },
        ),
        onTap: () async {
          final result = await showDialog<String>(
            context: context,
            builder: (context) => SimpleDialog(
              title: Text(l10n.navPadMode),
              children: [
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
              ref
                  .read(settingsProvider.notifier)
                  .setNavPadDpadStyle(result);
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
              ref
                  .read(settingsProvider.notifier)
                  .setNavPadRepeatRate(result);
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
      SwitchListTile(
        secondary: const Icon(Icons.keyboard_alt_outlined),
        title: Text(l10n.useCustomKeyboardTitle),
        subtitle: Text(l10n.useCustomKeyboardDesc),
        value: settings.useCustomKeyboard,
        onChanged: (value) {
          ref.read(settingsProvider.notifier).setUseCustomKeyboard(value);
        },
      ),
      const Divider(),
      // ── Action-bar preset (was "Toolbar Profile" — renamed per
      //    ADR/discussion 2026-05-14 to disambiguate from hub
      //    profile / member profile) ─────────────────────────────
      Consumer(
        builder: (context, ref, _) {
          final abState = ref.watch(actionBarProvider);
          return ListTile(
            leading: const Icon(Icons.view_column),
            title: Text(l10n.activeToolbarPreset),
            subtitle: Text(abState.activeProfile.name),
            onTap: () => _showPresetPicker(context, ref),
          );
        },
      ),
      ListTile(
        leading: const Icon(Icons.tune),
        title: Text(l10n.customizeToolbarPreset),
        subtitle: Text(l10n.customizeToolbarPresetDesc),
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
        title: Text(l10n.addNewToolbarPreset),
        subtitle: Text(l10n.addNewToolbarPresetDesc),
        onTap: () => _showCreatePresetDialog(context, ref),
      ),
      const Divider(),
      // ── Voice (moved from standalone Behavior row into Input) ───
      ListTile(
        leading: const Icon(Icons.mic_outlined),
        title: Text(l10n.scopeInputVoiceTitle),
        subtitle: Text(l10n.scopeInputVoiceDesc),
        trailing: const Icon(Icons.chevron_right),
        onTap: () {
          Navigator.push(
            context,
            MaterialPageRoute(
              builder: (_) => const VoiceSettingsScreen(),
            ),
          );
        },
      ),
      const Divider(),
      // ── General input behavior ─────────────────────────────────
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
          ref
              .read(settingsProvider.notifier)
              .setInvertPaneNavigation(value);
        },
      ),
      const Divider(),
      // ── Floating pad (was "Experimental" — now a sibling of
      //    NavPad with a "Beta" sublabel per ADR/discussion). ────
      SwitchListTile(
        secondary: const Icon(Icons.science),
        title: Row(
          children: [
            Text(l10n.floatingPad),
            const SizedBox(width: 8),
            _BetaChip(),
          ],
        ),
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
          onTap: () =>
              _showFloatingPadCenterKeyPicker(context, ref, settings),
        ),
      ],
      // ── Steward overlay (kept with floating pad — both are
      //    floating-overlay UI surfaces). ────────────────────────
      SwitchListTile(
        secondary: const Icon(Icons.support_agent_outlined),
        title: const Text('Steward overlay'),
        subtitle: const Text(
            'Floating chat puck for agent-driven navigation. '
            'Drag the puck or panel header; resize from the '
            'bottom-right corner. Disable to hide entirely.'),
        value: settings.stewardOverlayEnabled,
        onChanged: (value) {
          ref
              .read(settingsProvider.notifier)
              .setStewardOverlayEnabled(value);
        },
      ),
      if (settings.stewardOverlayEnabled)
        ListTile(
          leading: const Icon(Icons.opacity),
          title: const Text('Panel opacity'),
          subtitle: Slider(
            value:
                settings.stewardOverlayPanelOpacity.clamp(0.5, 1.0),
            min: 0.5,
            max: 1.0,
            divisions: 10,
            label:
                '${(settings.stewardOverlayPanelOpacity * 100).round()}%',
            onChanged: (v) {
              ref
                  .read(settingsProvider.notifier)
                  .setStewardOverlayPanelOpacity(v);
            },
          ),
          trailing: Text(
              '${(settings.stewardOverlayPanelOpacity * 100).round()}%'),
        ),
    ];
  }

  // ─── Files & Media ──────────────────────────────────────────────
  // Combines the previous Image Transfer + File Transfer sections,
  // with the shared toggles (auto-enter, bracketed paste) appearing
  // once per transfer type they apply to. Image-specific knobs nest
  // under the format/quality row to avoid visual clutter.
  List<Widget> _buildFilesMedia(BuildContext context, WidgetRef ref,
      AppSettings settings, AppLocalizations l10n) {
    return [
      // ── Image transfer ─────────────────────────────────────────
      Padding(
        padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
        child: Text(
          l10n.scopeFilesImageTransfer.toUpperCase(),
          style: GoogleFonts.spaceGrotesk(
            fontSize: 11,
            fontWeight: FontWeight.w700,
            letterSpacing: 1.2,
            color: Theme.of(context).colorScheme.onSurfaceVariant,
          ),
        ),
      ),
      ListTile(
        leading: const Icon(Icons.folder),
        title: Text(l10n.settingRemotePath),
        subtitle: Text(settings.imageRemotePath),
        onTap: () => _showTextInputDialog(
          context, ref,
          title: l10n.settingRemotePath,
          currentValue: settings.imageRemotePath,
          onSave: (v) =>
              ref.read(settingsProvider.notifier).setImageRemotePath(v),
        ),
      ),
      ListTile(
        leading: const Icon(Icons.image),
        title: Text(l10n.settingImageOutputFormat),
        subtitle: Text(settings.imageOutputFormat),
        onTap: () =>
            _showFormatPicker(context, ref, settings.imageOutputFormat),
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
            onSave: (v) => ref
                .read(settingsProvider.notifier)
                .setImageJpegQuality(v.round()),
          ),
        ),
      ListTile(
        leading: const Icon(Icons.photo_size_select_large),
        title: Text(l10n.settingImageResize),
        subtitle: Text(settings.imageResizePreset.toUpperCase()),
        onTap: () =>
            _showResizePresetPicker(context, ref, settings.imageResizePreset),
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
            onSave: (v) =>
                ref.read(settingsProvider.notifier).setImageMaxWidth(v),
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
            onSave: (v) =>
                ref.read(settingsProvider.notifier).setImageMaxHeight(v),
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
          onSave: (v) =>
              ref.read(settingsProvider.notifier).setImagePathFormat(v),
        ),
      ),
      SwitchListTile(
        secondary: const Icon(Icons.keyboard_return),
        title: Text(l10n.settingAutoEnter),
        subtitle: Text(l10n.settingAutoEnterDesc),
        value: settings.imageAutoEnter,
        onChanged: (v) =>
            ref.read(settingsProvider.notifier).setImageAutoEnter(v),
      ),
      SwitchListTile(
        secondary: const Icon(Icons.paste),
        title: Text(l10n.settingBracketedPaste),
        subtitle: Text(l10n.settingBracketedPasteDesc),
        value: settings.imageBracketedPaste,
        onChanged: (v) =>
            ref.read(settingsProvider.notifier).setImageBracketedPaste(v),
      ),
      const Divider(),
      // ── File transfer ──────────────────────────────────────────
      Padding(
        padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
        child: Text(
          l10n.scopeFilesFileTransfer.toUpperCase(),
          style: GoogleFonts.spaceGrotesk(
            fontSize: 11,
            fontWeight: FontWeight.w700,
            letterSpacing: 1.2,
            color: Theme.of(context).colorScheme.onSurfaceVariant,
          ),
        ),
      ),
      ListTile(
        leading: const Icon(Icons.folder),
        title: Text(l10n.settingRemotePath),
        subtitle: Text(settings.fileRemotePath),
        onTap: () => _showTextInputDialog(
          context, ref,
          title: l10n.settingRemotePath,
          currentValue: settings.fileRemotePath,
          onSave: (v) =>
              ref.read(settingsProvider.notifier).setFileRemotePath(v),
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
          onSave: (v) =>
              ref.read(settingsProvider.notifier).setFileDownloadPath(v),
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
          onSave: (v) =>
              ref.read(settingsProvider.notifier).setFilePathFormat(v),
        ),
      ),
      SwitchListTile(
        secondary: const Icon(Icons.keyboard_return),
        title: Text(l10n.settingAutoEnter),
        subtitle: Text(l10n.settingAutoEnterDesc),
        value: settings.fileAutoEnter,
        onChanged: (v) =>
            ref.read(settingsProvider.notifier).setFileAutoEnter(v),
      ),
      SwitchListTile(
        secondary: const Icon(Icons.paste),
        title: Text(l10n.settingBracketedPaste),
        subtitle: Text(l10n.settingBracketedPasteDesc),
        value: settings.fileBracketedPaste,
        onChanged: (v) =>
            ref.read(settingsProvider.notifier).setFileBracketedPaste(v),
      ),
    ];
  }

  // ─── Data ───────────────────────────────────────────────────────
  List<Widget> _buildData(
      BuildContext context, WidgetRef ref, AppLocalizations l10n) {
    return [
      ListTile(
        leading: const Icon(Icons.upload),
        title: Text(l10n.exportBackup),
        subtitle: Text(l10n.exportBackupDesc),
        onTap: () => _handleExport(context, ref),
      ),
      ListTile(
        leading: const Icon(Icons.download),
        title: Text(l10n.importBackup),
        subtitle: Text(l10n.importBackupDesc),
        onTap: () => _handleImport(context, ref),
      ),
      ListTile(
        leading: const Icon(Icons.cloud_off),
        title: Text(l10n.clearOfflineCache),
        subtitle: Text(l10n.clearOfflineCacheDesc),
        onTap: () => _handleClearOfflineCache(context, ref),
      ),
      ListTile(
        leading: const Icon(Icons.folder_open),
        title: Text(l10n.browseFiles),
        subtitle: Text(l10n.browseFilesDesc),
        onTap: () {
          Navigator.of(context).push(
            MaterialPageRoute(builder: (_) => const FileBrowserScreen()),
          );
        },
      ),
      ListTile(
        leading: const Icon(Icons.shield_outlined),
        title: Text(l10n.vaultLegacy),
        subtitle: Text(l10n.vaultLegacyDesc),
        onTap: () {
          Navigator.of(context).push(
            MaterialPageRoute(builder: (_) => const VaultScreen()),
          );
        },
      ),
    ];
  }

  // ─── System ─────────────────────────────────────────────────────
  // Today: just the local-notifications OS gate. Load-bearing slot
  // for future OS-permission-level toggles (background sync, app
  // badge, OS-permission resets, data-saver). See ADR-026-adjacent
  // discussion: docs/discussions/settings-and-team-scope-ia.md §5.
  List<Widget> _buildSystem(BuildContext context, WidgetRef ref,
      AppSettings settings, AppLocalizations l10n) {
    return [
      SwitchListTile(
        secondary: const Icon(Icons.notifications_outlined),
        title: Text(l10n.settingNotifications),
        subtitle: Text(l10n.settingNotificationsDesc),
        value: settings.enableNotifications,
        onChanged: (value) async {
          await ref
              .read(settingsProvider.notifier)
              .setEnableNotifications(value);
          // Lazy permission prompt — fire when the user explicitly
          // opts in. Plugin no-ops on platforms without the OS gate
          // (iOS pre-13 / web).
          if (value) {
            await LocalNotifications.instance.requestPermission();
          }
        },
      ),
    ];
  }

  // ─── About ──────────────────────────────────────────────────────
  List<Widget> _buildAbout(
      BuildContext context, WidgetRef ref, AppLocalizations l10n) {
    return [
      Padding(
        padding: const EdgeInsets.symmetric(vertical: 12),
        child: Center(
          child: ClipRRect(
            borderRadius: BorderRadius.circular(16),
            child:
                Image.asset('assets/icon/icon.png', width: 72, height: 72),
          ),
        ),
      ),
      ListTile(
        leading: const Icon(Icons.info),
        title: Text(l10n.settingVersion),
        subtitle: Text(VersionInfo.version),
      ),
      ListTile(
        leading: const Icon(Icons.system_update_alt),
        title: Text(l10n.settingCheckUpdate),
        subtitle: Text(l10n.settingCheckUpdateDesc),
        onTap: () => _handleCheckForUpdate(context),
      ),
      ListTile(
        leading: const Icon(Icons.code),
        title: Text(l10n.settingSourceCode),
        subtitle: Text(l10n.settingSourceCodeUrl),
        onTap: () async {
          final url = Uri.parse('https://github.com/physercoe/termipod');
          if (await canLaunchUrl(url)) {
            await launchUrl(url, mode: LaunchMode.externalApplication);
          }
        },
      ),
      ListTile(
        leading: const Icon(Icons.mail_outline),
        title: Text(l10n.settingFeedback),
        subtitle: Text(l10n.settingFeedbackDesc),
        onTap: () async {
          final uri = Uri(
            scheme: 'mailto',
            path: 'whereilive@gmail.com',
            queryParameters: {
              'subject': 'TermiPod Feedback (v${VersionInfo.version})',
            },
          );
          try {
            await launchUrl(uri);
          } catch (_) {
            final fallback =
                Uri.parse('https://github.com/physercoe/termipod/issues');
            if (context.mounted) {
              await launchUrl(fallback,
                  mode: LaunchMode.externalApplication);
            }
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
    ];
  }

  // ─── Dialog & picker helpers ────────────────────────────────────

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
          TextButton(
              onPressed: () => Navigator.pop(ctx),
              child: Text(l10n.buttonCancel)),
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
          TextButton(
              onPressed: () => Navigator.pop(ctx),
              child: Text(l10n.buttonCancel)),
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
              Slider(
                  value: current,
                  min: min,
                  max: max,
                  onChanged: (v) => setState(() => current = v)),
              Text('${current.round()}'),
            ],
          ),
          actions: [
            TextButton(
                onPressed: () => Navigator.pop(ctx),
                child: Text(l10n.buttonCancel)),
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

  void _showFormatPicker(
      BuildContext context, WidgetRef ref, String current) {
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
                if (v != null) {
                  ref
                      .read(settingsProvider.notifier)
                      .setImageOutputFormat(v);
                }
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

  void _showAdjustModePicker(
      BuildContext context, WidgetRef ref, String current) {
    final l10n = AppLocalizations.of(context)!;
    showDialog(
      context: context,
      builder: (ctx) => SimpleDialog(
        title: Text(l10n.adjustModeTitle),
        children: [
          for (final entry in [
            ('none', l10n.adjustModeNone, l10n.adjustModeNoneDesc),
            ('autoFit', l10n.adjustModeAutoFit, l10n.adjustModeAutoFitDesc),
            ('autoResize', l10n.adjustModeAutoResize,
                l10n.adjustModeAutoResizeDesc),
          ])
            RadioListTile<String>(
              title: Text(entry.$2),
              subtitle: Text(entry.$3),
              value: entry.$1,
              groupValue: current,
              onChanged: (v) {
                if (v != null) {
                  ref.read(settingsProvider.notifier).setAdjustMode(v);
                }
                Navigator.pop(ctx);
              },
            ),
        ],
      ),
    );
  }

  void _showScrollbackPicker(
      BuildContext context, WidgetRef ref, int current) {
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
                if (v != null) {
                  ref.read(settingsProvider.notifier).setScrollbackLines(v);
                }
                Navigator.pop(ctx);
              },
            ),
        ],
      ),
    );
  }

  void _showResizePresetPicker(
      BuildContext context, WidgetRef ref, String current) {
    final l10n = AppLocalizations.of(context)!;
    showDialog(
      context: context,
      builder: (ctx) => SimpleDialog(
        title: Text(l10n.resizePresetTitle),
        children: [
          for (final entry in [
            ('original', l10n.resizePresetOriginal),
            ('1080p', '1080p'),
            ('720p', '720p'),
            ('480p', '480p'),
            ('custom', l10n.resizePresetCustom),
          ])
            RadioListTile<String>(
              title: Text(entry.$2),
              value: entry.$1,
              groupValue: current,
              onChanged: (v) {
                if (v != null) {
                  ref
                      .read(settingsProvider.notifier)
                      .setImageResizePreset(v);
                }
                Navigator.pop(ctx);
              },
            ),
        ],
      ),
    );
  }

  void _showLocalePicker(
      BuildContext context, WidgetRef ref, String current) {
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
                if (v != null) {
                  ref.read(settingsProvider.notifier).setLocale(v);
                }
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

    final defaults = [
      ActionBarButton(
          id: 'np-esc',
          label: 'ESC',
          type: ActionBarButtonType.specialKey,
          value: 'Escape'),
      ActionBarButton(
          id: 'np-tab',
          label: 'TAB',
          type: ActionBarButtonType.specialKey,
          value: 'Tab'),
      ActionBarButton(
          id: 'np-cc',
          label: 'C-C',
          type: ActionBarButtonType.ctrlCombo,
          value: 'C-c'),
      ActionBarButton(
          id: 'np-ent',
          label: 'ENT',
          type: ActionBarButtonType.specialKey,
          value: 'Enter'),
    ];

    final buttons =
        currentButtons.length == 4 ? currentButtons : defaults;
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
                        color: Theme.of(context)
                            .colorScheme
                            .onSurfaceVariant
                            .withValues(alpha: 0.4),
                        borderRadius: BorderRadius.circular(2),
                      ),
                    ),
                    Padding(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 16, vertical: 8),
                      child: Row(
                        children: [
                          Text(l10n.navPadCustomizeButtons,
                              style:
                                  Theme.of(context).textTheme.titleMedium),
                          const Spacer(),
                          if (isCustom)
                            TextButton(
                              onPressed: () {
                                ref
                                    .read(settingsProvider.notifier)
                                    .setNavPadButtons(null);
                                Navigator.pop(ctx);
                              },
                              child: Text(l10n.resetToDefault),
                            ),
                        ],
                      ),
                    ),
                    const Divider(height: 1),
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
                                final json = jsonEncode(buttons
                                    .map((b) => b.toJson())
                                    .toList());
                                ref
                                    .read(settingsProvider.notifier)
                                    .setNavPadButtons(json);
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
                          style: Theme.of(context)
                              .textTheme
                              .labelMedium
                              ?.copyWith(
                                color: DesignColors.primary,
                                fontWeight: FontWeight.w600,
                              ),
                        ),
                      ),
                    ),
                    Expanded(
                      child: _NavPadButtonCatalog(
                        scrollController: scrollController,
                        onPick: (button) {
                          ScaffoldMessenger.of(context).showSnackBar(
                            const SnackBar(
                              content: Text(
                                  'Tap a button slot above, then pick a replacement'),
                              duration: Duration(seconds: 2),
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
                  ref
                      .read(settingsProvider.notifier)
                      .setFloatingPadCenterKey(v);
                }
                Navigator.pop(ctx);
              },
            ),
          const Divider(),
          Padding(
            padding:
                const EdgeInsets.symmetric(horizontal: 24, vertical: 8),
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
                      ref
                          .read(settingsProvider.notifier)
                          .setFloatingPadCenterKey(v);
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

  // ── Preset picker (was _showProfilePicker; renamed for the
  //    Profile→Preset rename in the action-bar UI) ──────────────
  void _showPresetPicker(BuildContext context, WidgetRef ref) {
    final state = ref.read(actionBarProvider);
    showDialog(
      context: context,
      builder: (ctx) => SimpleDialog(
        title: Text(AppLocalizations.of(context)!.selectToolbarPreset),
        children: [
          for (final profile in state.profiles)
            RadioListTile<String>(
              title: Text(profile.name),
              subtitle: profile.isBuiltIn
                  ? Text(AppLocalizations.of(context)!.builtIn)
                  : null,
              value: profile.id,
              groupValue: state.activeProfileId,
              onChanged: (v) {
                if (v != null) {
                  ref
                      .read(actionBarProvider.notifier)
                      .setActiveProfile(v);
                }
                Navigator.pop(ctx);
              },
            ),
        ],
      ),
    );
  }

  void _showCreatePresetDialog(BuildContext context, WidgetRef ref) {
    final nameController = TextEditingController();
    final l10n = AppLocalizations.of(context)!;
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.newToolbarPreset),
        content: TextField(
          controller: nameController,
          autofocus: true,
          decoration: InputDecoration(
            labelText: l10n.toolbarPresetName,
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
                ref
                    .read(actionBarProvider.notifier)
                    .addCustomProfile(profile);
                ref
                    .read(actionBarProvider.notifier)
                    .setActiveProfile(profile.id);
                Navigator.pop(ctx);
              }
            },
            child: Text(l10n.buttonCreate),
          ),
        ],
      ),
    );
  }

  Future<void> _handleCheckForUpdate(BuildContext context) async {
    final l10n = AppLocalizations.of(context)!;
    final checker = UpdateService.defaultChecker();

    showDialog<void>(
      context: context,
      barrierDismissible: false,
      builder: (_) => const Center(child: CircularProgressIndicator()),
    );

    UpdateInfo? info;
    String? errorMessage;
    try {
      info = await checker.checkForUpdateOrThrow(VersionInfo.version);
    } catch (e) {
      errorMessage = e.toString();
    }

    if (!context.mounted) return;
    Navigator.of(context, rootNavigator: true).pop();

    if (errorMessage != null) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(l10n.updateCheckFailed(errorMessage))),
      );
      return;
    }

    if (info == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(l10n.updateUpToDate)),
      );
      return;
    }

    final update = info;
    await showDialog<void>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.updateAvailable),
        content: SingleChildScrollView(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(l10n.updateAvailableMessage(
                  update.version, VersionInfo.version)),
              if (update.releaseNotes.isNotEmpty) ...[
                const SizedBox(height: 12),
                Text(
                  l10n.updateReleaseNotes,
                  style: Theme.of(ctx).textTheme.titleSmall,
                ),
                const SizedBox(height: 4),
                Text(update.releaseNotes),
              ],
            ],
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(),
            child: Text(l10n.updateLater),
          ),
          TextButton(
            onPressed: () async {
              Navigator.of(ctx).pop();
              final target = update.downloadUrl ?? update.releasePageUrl;
              final uri = Uri.parse(target);
              await launchUrl(uri, mode: LaunchMode.externalApplication);
            },
            child: Text(update.downloadUrl != null
                ? l10n.updateDownload
                : l10n.updateViewRelease),
          ),
        ],
      ),
    );
  }

  Future<void> _handleExport(BuildContext context, WidgetRef ref) async {
    final l10n = AppLocalizations.of(context)!;

    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.exportWarningTitle),
        content: Text(l10n.exportWarningContent),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(ctx, false),
              child: Text(l10n.buttonCancel)),
          FilledButton(
              onPressed: () => Navigator.pop(ctx, true),
              child: Text(l10n.exportButton)),
        ],
      ),
    );
    if (confirmed != true || !context.mounted) return;

    try {
      final storage = ref.read(secureStorageProvider);
      final service = DataPortService(storage);
      final data = await service.exportData();
      final jsonStr = const JsonEncoder.withIndent('  ').convert(data);

      final now = DateTime.now();
      final fileName =
          'termipod-backup-${now.year}-${now.month.toString().padLeft(2, '0')}-${now.day.toString().padLeft(2, '0')}.json';

      final savedPath = await PublicFileStore.writeBytes(
        fileName,
        utf8.encode(jsonStr),
      );

      if (savedPath == null) {
        throw Exception('Failed to save backup to public storage');
      }

      if (!context.mounted) return;
      final tmpDir = await getTemporaryDirectory();
      final shareFile = File('${tmpDir.path}/$fileName');
      await shareFile.writeAsString(jsonStr);

      if (!context.mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(l10n.exportSavedTo(savedPath)),
          action: SnackBarAction(
            label: l10n.fileActionShare,
            onPressed: () {
              Share.shareXFiles([XFile(shareFile.path)]);
            },
          ),
          duration: const Duration(seconds: 6),
        ),
      );
    } catch (e) {
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(l10n.exportFailed(e.toString())),
            backgroundColor: Theme.of(context).colorScheme.error,
          ),
        );
      }
    }
  }

  Future<void> _handleImport(BuildContext context, WidgetRef ref) async {
    final l10n = AppLocalizations.of(context)!;

    try {
      final result = await FilePicker.platform.pickFiles(
        type: FileType.custom,
        allowedExtensions: ['json'],
        withData: true,
      );
      if (result == null || result.files.isEmpty) return;

      final file = result.files.first;
      String content;
      if (file.bytes != null) {
        content = String.fromCharCodes(file.bytes!);
      } else if (file.path != null) {
        content = await File(file.path!).readAsString();
      } else {
        throw Exception('Could not read file');
      }

      final backup = jsonDecode(content) as Map<String, dynamic>;
      DataPortService.validate(backup);
      final summary = DataPortService.summarize(backup);

      if (!context.mounted) return;

      final selectedCategories = await showDialog<Set<ImportCategory>>(
        context: context,
        builder: (ctx) => _ImportDialog(summary: summary, l10n: l10n),
      );
      if (selectedCategories == null ||
          selectedCategories.isEmpty ||
          !context.mounted) return;

      final storage = ref.read(secureStorageProvider);
      final service = DataPortService(storage);
      final importResult = await service.importData(backup,
          categories: selectedCategories);

      if (!context.mounted) return;

      final lines = <String>[];
      if (selectedCategories.contains(ImportCategory.connections)) {
        lines.add(l10n.importResultConnections(
            importResult.connectionsAdded, importResult.connectionsSkipped));
      }
      if (selectedCategories.contains(ImportCategory.sshKeys)) {
        lines.add(l10n.importResultKeys(
            importResult.keysAdded, importResult.keysSkipped));
      }
      if (selectedCategories.contains(ImportCategory.snippets)) {
        lines.add(l10n.importResultSnippets(importResult.snippetsAdded));
      }
      if (importResult.passwordsAdded > 0) {
        lines.add(l10n.importResultPasswords(importResult.passwordsAdded));
      }
      if (selectedCategories.contains(ImportCategory.history)) {
        lines.add(l10n.importResultHistory(importResult.historyMerged));
      }
      if (importResult.settingsImported) {
        lines.add(l10n.importResultSettings);
      }
      if (importResult.profilesImported) {
        lines.add(l10n.importResultProfiles);
      }

      ref.invalidate(settingsProvider);

      showDialog(
        context: context,
        builder: (ctx) => AlertDialog(
          title: Text(l10n.importSuccess),
          content: Text(lines.join('\n')),
          actions: [
            FilledButton(
                onPressed: () => Navigator.pop(ctx),
                child: Text(l10n.buttonClose)),
          ],
        ),
      );
    } on FormatException catch (e) {
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(e.message.contains('version')
                ? l10n.importUnsupportedVersion(int.tryParse(e.message
                        .replaceAll(RegExp(r'[^0-9]'), '')) ??
                    0)
                : l10n.importInvalidFormat),
            backgroundColor: Theme.of(context).colorScheme.error,
          ),
        );
      }
    } catch (e) {
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(l10n.importFailed(e.toString())),
            backgroundColor: Theme.of(context).colorScheme.error,
          ),
        );
      }
    }
  }

  Future<void> _handleClearOfflineCache(
      BuildContext context, WidgetRef ref) async {
    final l10n = AppLocalizations.of(context)!;
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.clearOfflineCacheConfirmTitle),
        content: Text(l10n.clearOfflineCacheConfirmBody),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: Text(l10n.buttonCancel),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, true),
            child: Text(l10n.clearOfflineCacheConfirm),
          ),
        ],
      ),
    );
    if (ok != true) return;
    final (rows, files) =
        await ref.read(hubProvider.notifier).clearOfflineCache();
    if (!context.mounted) return;
    final total = rows + files;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(total == 0
            ? l10n.clearOfflineCacheEmpty
            : l10n.clearOfflineCacheCleared(total)),
      ),
    );
  }
}

/// Small "Beta" chip rendered next to the Floating pad row title so
/// the experimental status is visible without a dedicated category.
class _BetaChip extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: scheme.tertiaryContainer,
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        'BETA',
        style: TextStyle(
          fontSize: 9,
          fontWeight: FontWeight.w700,
          letterSpacing: 0.5,
          color: scheme.onTertiaryContainer,
        ),
      ),
    );
  }
}

class _ImportDialog extends StatefulWidget {
  final BackupSummary summary;
  final AppLocalizations l10n;

  const _ImportDialog({required this.summary, required this.l10n});

  @override
  State<_ImportDialog> createState() => _ImportDialogState();
}

class _ImportDialogState extends State<_ImportDialog> {
  final _selected = <ImportCategory>{
    ImportCategory.connections,
    ImportCategory.sshKeys,
    ImportCategory.snippets,
    ImportCategory.history,
    ImportCategory.actionBar,
  };

  @override
  Widget build(BuildContext context) {
    final l10n = widget.l10n;
    final s = widget.summary;

    return AlertDialog(
      title: Text(l10n.importDialogTitle),
      content: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              l10n.backupSummary(
                  s.connections, s.keys, s.snippets, s.historyItems),
              style: Theme.of(context).textTheme.bodySmall,
            ),
            const SizedBox(height: 16),
            _checkbox(
                ImportCategory.connections, l10n.importCategoryConnections),
            _checkbox(ImportCategory.sshKeys, l10n.importCategorySshKeys),
            _checkbox(ImportCategory.snippets, l10n.importCategorySnippets),
            _checkbox(ImportCategory.history, l10n.importCategoryHistory),
            _checkbox(
                ImportCategory.actionBar, l10n.importCategoryActionBar),
            _checkbox(ImportCategory.settings, l10n.importCategorySettings),
          ],
        ),
      ),
      actions: [
        TextButton(
            onPressed: () => Navigator.pop(context),
            child: Text(l10n.buttonCancel)),
        FilledButton(
          onPressed: _selected.isEmpty
              ? null
              : () => Navigator.pop(context, _selected),
          child: Text(l10n.importConfirm),
        ),
      ],
    );
  }

  Widget _checkbox(ImportCategory category, String label) {
    return CheckboxListTile(
      value: _selected.contains(category),
      title: Text(label),
      dense: true,
      controlAffinity: ListTileControlAffinity.leading,
      onChanged: (v) {
        setState(() {
          if (v == true) {
            _selected.add(category);
          } else {
            _selected.remove(category);
          }
        });
      },
    );
  }
}

/// A single slot in the nav pad button customization grid.
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
          color: isDark
              ? DesignColors.keyBackground
              : DesignColors.keyBackgroundLight,
          borderRadius: BorderRadius.circular(8),
          border: Border.all(
            color:
                isDark ? DesignColors.borderDark : DesignColors.borderLight,
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
                color: isDark
                    ? DesignColors.textPrimary
                    : DesignColors.textPrimaryLight,
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
                    color: Theme.of(context)
                        .colorScheme
                        .onSurfaceVariant
                        .withValues(alpha: 0.4),
                    borderRadius: BorderRadius.circular(2),
                  ),
                ),
                Padding(
                  padding: const EdgeInsets.symmetric(
                      horizontal: 16, vertical: 8),
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
                            padding:
                                const EdgeInsets.fromLTRB(16, 12, 16, 4),
                            child: Text(
                              typeLabel(type),
                              style: Theme.of(context)
                                  .textTheme
                                  .labelMedium
                                  ?.copyWith(
                                    color: DesignColors.primary,
                                    fontWeight: FontWeight.w600,
                                  ),
                            ),
                          ),
                          Padding(
                            padding:
                                const EdgeInsets.symmetric(horizontal: 12),
                            child: Wrap(
                              spacing: 6,
                              runSpacing: 4,
                              children: [
                                for (final catButton
                                    in catalogByType[type]!)
                                  ActionChip(
                                    label: Text(catButton.label),
                                    tooltip:
                                        '${catButton.value} — ${catButton.displayDescription}',
                                    onPressed: () {
                                      final newButton = ActionBarButton(
                                        id: 'np_${DateTime.now().millisecondsSinceEpoch}',
                                        label: catButton.label,
                                        type: catButton.type,
                                        value: catButton.value,
                                        longPressValue:
                                            catButton.longPressValue,
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
