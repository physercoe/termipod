import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'package:termipod/l10n/app_localizations.dart';

import '../../models/action_bar_config.dart';
import '../../providers/action_bar_provider.dart';
import '../../screens/settings/action_bar_settings_screen.dart';
import '../../theme/design_colors.dart';
import '../help_sheet.dart';

/// Key Palette bottom sheet opened from the [⋮] button in the action bar.
///
/// In 0.9.1 this sheet was repurposed from a pure profile switcher into
/// a full key palette: it shows every group of the active profile as
/// chip rows, so users can reach infrequent keys without swiping through
/// action-bar pages. Profile selection collapses into a compact header.
///
/// In 0.9.7 action-type buttons (file transfer, snippets, direct input)
/// are included in the palette as chips via [onActionTap], and the meta
/// footer ("Reset to default") is also rendered as a chip wrap — the
/// whole palette aims to fit on one page without scrolling.
class ProfileSheet extends ConsumerWidget {
  final VoidCallback? onEditGroups;
  final void Function(String literal)? onKeyTap;
  final void Function(String tmuxKey)? onSpecialKeyTap;
  final void Function(String modifier)? onModifierTap;
  final void Function(String actionValue)? onActionTap;
  final ScrollController? scrollController;

  /// Panel identifier so profile switches are scoped to the pane this
  /// sheet was opened from. Mirrors [ActionBar.panelKey]. Null falls
  /// back to the global default.
  final String? panelKey;

  const ProfileSheet({
    super.key,
    this.onEditGroups,
    this.onKeyTap,
    this.onSpecialKeyTap,
    this.onModifierTap,
    this.onActionTap,
    this.scrollController,
    this.panelKey,
  });

  static Future<void> show(
    BuildContext context, {
    required WidgetRef ref,
    VoidCallback? onEditGroups,
    void Function(String literal)? onKeyTap,
    void Function(String tmuxKey)? onSpecialKeyTap,
    void Function(String modifier)? onModifierTap,
    void Function(String actionValue)? onActionTap,
    String? panelKey,
  }) async {
    await showModalBottomSheet<void>(
      context: context,
      backgroundColor: Colors.transparent,
      isScrollControlled: true,
      builder: (sheetContext) => DraggableScrollableSheet(
        initialChildSize: 0.6,
        minChildSize: 0.35,
        maxChildSize: 0.92,
        expand: false,
        builder: (context, scrollController) => ProfileSheet(
          scrollController: scrollController,
          onEditGroups: onEditGroups != null
              ? () {
                  Navigator.pop(context);
                  onEditGroups();
                }
              : null,
          onKeyTap: onKeyTap,
          onSpecialKeyTap: onSpecialKeyTap,
          onModifierTap: onModifierTap,
          onActionTap: onActionTap,
          panelKey: panelKey,
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final state = ref.watch(actionBarProvider);
    // Resolve the profile for this panel (not the global default) so
    // the palette shows the keys the user is currently looking at on
    // the action bar.
    final activeProfile = state.profileForPanel(panelKey);

    return Container(
      decoration: BoxDecoration(
        color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        borderRadius: const BorderRadius.vertical(top: Radius.circular(16)),
      ),
      child: SafeArea(
        top: false,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Handle bar
            Center(
              child: Container(
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
            ),
            // Profile header — compact dropdown + [+ New]
            _buildProfileHeader(context, ref, state, activeProfile, isDark),
            const Divider(height: 1),
            // Scrollable body: Key Palette + meta actions
            Expanded(
              child: SingleChildScrollView(
                controller: scrollController,
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    const SizedBox(height: 8),
                    // Per-group rows — always includes Navigate group,
                    // unlike the action bar which filters it when the
                    // nav pad is enabled. Action-type buttons are now
                    // included too (chip layout) so users don't have
                    // to hunt for ⚡ snippet/file transfer in the
                    // action bar's scrolling carousel.
                    for (final group in activeProfile.groups)
                      _buildPaletteGroup(context, group, isDark),
                    const SizedBox(height: 6),
                    // META actions as chips — one compact wrap instead
                    // of line-by-line rows, so the whole palette stays
                    // visible without scrolling.
                    _buildMetaChips(context, ref, activeProfile, isDark),
                    const SizedBox(height: 8),
                  ],
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  // ---------------------------------------------------------------------------
  // Profile header
  // ---------------------------------------------------------------------------

  Widget _buildProfileHeader(
    BuildContext context,
    WidgetRef ref,
    ActionBarState state,
    ActionBarProfile activeProfile,
    bool isDark,
  ) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 4, 8, 8),
      child: Row(
        children: [
          Icon(
            Icons.view_carousel_outlined,
            size: 18,
            color: DesignColors.primary,
          ),
          const SizedBox(width: 8),
          Text(
            AppLocalizations.of(context)!.profileHeaderTitle,
            style: TextStyle(
              fontSize: 12,
              color: isDark
                  ? DesignColors.textMuted
                  : DesignColors.textMutedLight,
            ),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: InkWell(
              onTap: () => _showProfilePicker(context, ref, state),
              borderRadius: BorderRadius.circular(6),
              child: Padding(
                padding: const EdgeInsets.symmetric(
                  horizontal: 6,
                  vertical: 4,
                ),
                child: Row(
                  children: [
                    Flexible(
                      child: Text(
                        activeProfile.name,
                        style: TextStyle(
                          fontSize: 15,
                          fontWeight: FontWeight.w600,
                          color: DesignColors.primary,
                        ),
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                    const Icon(
                      Icons.arrow_drop_down,
                      size: 20,
                      color: DesignColors.primary,
                    ),
                  ],
                ),
              ),
            ),
          ),
          // Help (?) — opens the cheat sheet so users can look up what
          // every button in the current profile actually does without
          // digging through settings. Lives next to "+ New" so both
          // profile-meta actions share one corner.
          IconButton(
            onPressed: () {
              HapticFeedback.selectionClick();
              showHelpSheet(context, ref, panelKey: panelKey);
            },
            icon: const Icon(Icons.help_outline, size: 18),
            color: DesignColors.primary,
            tooltip: 'Help',
            visualDensity: VisualDensity.compact,
            padding: const EdgeInsets.symmetric(horizontal: 6),
            constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
          ),
          TextButton.icon(
            onPressed: () {
              Navigator.pop(context);
              _showCreateProfileDialog(context, ref);
            },
            icon: const Icon(Icons.add, size: 16),
            label: Text(AppLocalizations.of(context)!.newLabel),
            style: TextButton.styleFrom(
              foregroundColor: DesignColors.primary,
              padding: const EdgeInsets.symmetric(horizontal: 8),
              visualDensity: VisualDensity.compact,
            ),
          ),
        ],
      ),
    );
  }

  void _showProfilePicker(
    BuildContext context,
    WidgetRef ref,
    ActionBarState state,
  ) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final currentProfileId = state.profileIdForPanel(panelKey);
    void applyProfile(String profileId) {
      final key = panelKey;
      final notifier = ref.read(actionBarProvider.notifier);
      if (key != null) {
        notifier.setActiveProfileForPanel(key, profileId);
      } else {
        notifier.setActiveProfile(profileId);
      }
    }

    showDialog<void>(
      context: context,
      builder: (dialogCtx) => Dialog(
        child: ConstrainedBox(
          constraints: BoxConstraints(
            maxHeight: MediaQuery.of(context).size.height * 0.55,
            maxWidth: 360,
          ),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Padding(
                padding: const EdgeInsets.all(16),
                child: Row(
                  children: [
                    Text(
                      AppLocalizations.of(context)!.toolbarProfile,
                      style: const TextStyle(
                        fontSize: 16,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ],
                ),
              ),
              const Divider(height: 1),
              Flexible(
                child: SingleChildScrollView(
                  child: Column(
                    children: state.profiles.map((profile) {
                      final isActive = profile.id == currentProfileId;
                      return _ProfileRow(
                        profile: profile,
                        isActive: isActive,
                        isDark: isDark,
                        onSelect: () {
                          HapticFeedback.selectionClick();
                          applyProfile(profile.id);
                          Navigator.pop(dialogCtx);
                        },
                        onEdit: () {
                          Navigator.pop(dialogCtx);
                          // Close the outer palette sheet too so the
                          // settings screen isn't pushed under it.
                          Navigator.pop(context);
                          Navigator.push(
                            context,
                            MaterialPageRoute(
                              builder: (_) =>
                                  const ActionBarSettingsScreen(),
                            ),
                          );
                          applyProfile(profile.id);
                        },
                        onDelete: profile.isBuiltIn
                            ? null
                            : () => _confirmDelete(context, ref, profile),
                      );
                    }).toList(),
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  // ---------------------------------------------------------------------------
  // Key Palette
  // ---------------------------------------------------------------------------

  Widget _buildPaletteGroup(
    BuildContext context,
    ActionBarGroup group,
    bool isDark,
  ) {
    // All button types render as chips — including action-type buttons
    // (file transfer, snippets, direct input). Action dispatch is routed
    // via [onActionTap] from the caller so the palette stays as the
    // "everything in one page" view without duplicating logic.
    final chips = group.buttons;
    if (chips.isEmpty) return const SizedBox.shrink();

    // Force a fixed cross-axis count so the palette *always* renders
    // as a grid, regardless of label widths or device DPI. Earlier
    // Wrap-based layouts collapsed to 1 column on some devices when
    // label widths + padding exceeded the row budget — the user kept
    // seeing a vertical list instead of a chip grid, which this fixes.
    // 4 columns on phones ≤420dp, 5 on wider screens, 6 on tablets.
    final width = MediaQuery.of(context).size.width;
    final int crossAxisCount = width >= 600
        ? 6
        : width >= 420
            ? 5
            : 4;
    const double spacing = 6;
    const double horizontalPadding = 12;
    final double available = width - (horizontalPadding * 2);
    final double chipWidth =
        (available - spacing * (crossAxisCount - 1)) / crossAxisCount;

    return Padding(
      padding: EdgeInsets.fromLTRB(horizontalPadding, 8, horizontalPadding, 2),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.only(left: 2, bottom: 4),
            child: Text(
              group.name.toUpperCase(),
              style: TextStyle(
                fontSize: 10,
                fontWeight: FontWeight.w700,
                letterSpacing: 0.8,
                color: isDark
                    ? DesignColors.textMuted
                    : DesignColors.textMutedLight,
              ),
            ),
          ),
          Wrap(
            spacing: spacing,
            runSpacing: spacing,
            children: chips
                .map((b) => SizedBox(
                      width: chipWidth,
                      child: _buildPaletteChip(context, b, isDark),
                    ))
                .toList(),
          ),
        ],
      ),
    );
  }

  Widget _buildPaletteChip(
    BuildContext context,
    ActionBarButton btn,
    bool isDark,
  ) {
    // Action-type chips get a subtle accent tint so they're visually
    // distinct from regular key chips — users scanning for "snippets"
    // can spot the accent without reading every label.
    final isAction = btn.type == ActionBarButtonType.action;
    final bgColor = isAction
        ? DesignColors.primary.withValues(alpha: isDark ? 0.18 : 0.10)
        : (isDark
            ? DesignColors.keyBackground
            : DesignColors.keyBackgroundLight);
    final borderColor = isAction
        ? DesignColors.primary.withValues(alpha: 0.4)
        : (isDark ? DesignColors.borderDark : DesignColors.borderLight);
    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: () => _handlePaletteTap(context, btn),
        borderRadius: BorderRadius.circular(8),
        child: Container(
          height: 38,
          padding: const EdgeInsets.symmetric(horizontal: 6),
          decoration: BoxDecoration(
            color: bgColor,
            border: Border.all(color: borderColor, width: 1),
            borderRadius: BorderRadius.circular(8),
          ),
          child: Center(
            child: Text(
              btn.label,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(
                fontSize: 13,
                fontWeight: FontWeight.w600,
                fontFamily: 'monospace',
                color: isAction
                    ? DesignColors.primary
                    : (isDark
                        ? DesignColors.textPrimary
                        : DesignColors.textPrimaryLight),
              ),
            ),
          ),
        ),
      ),
    );
  }

  void _handlePaletteTap(BuildContext context, ActionBarButton btn) {
    HapticFeedback.selectionClick();
    // Close the sheet before firing the key so the resulting character
    // lands in the visible terminal rather than being obscured.
    Navigator.pop(context);
    switch (btn.type) {
      case ActionBarButtonType.specialKey:
      case ActionBarButtonType.ctrlCombo:
      case ActionBarButtonType.altCombo:
      case ActionBarButtonType.shiftCombo:
        onSpecialKeyTap?.call(btn.value);
      case ActionBarButtonType.literal:
        onKeyTap?.call(btn.value);
      case ActionBarButtonType.modifier:
        onModifierTap?.call(btn.value);
      case ActionBarButtonType.confirm:
        // Palette simplifies confirm semantics to "send literal + Enter".
        onKeyTap?.call(btn.value);
        onSpecialKeyTap?.call('Enter');
      case ActionBarButtonType.action:
        onActionTap?.call(btn.value);
    }
  }

  // ---------------------------------------------------------------------------
  // Meta chips row (formerly full-width "Reset to default" row)
  // ---------------------------------------------------------------------------

  Widget _buildMetaChips(
    BuildContext context,
    WidgetRef ref,
    ActionBarProfile activeProfile,
    bool isDark,
  ) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 4, 12, 2),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.only(left: 2, bottom: 3),
            child: Text(
              AppLocalizations.of(context)!.paletteMetaHeader,
              style: TextStyle(
                fontSize: 10,
                fontWeight: FontWeight.w600,
                letterSpacing: 0.6,
                color: isDark
                    ? DesignColors.textMuted
                    : DesignColors.textMutedLight,
              ),
            ),
          ),
          Wrap(
            spacing: 4,
            runSpacing: 4,
            children: [
              _buildMetaChip(
                context,
                icon: Icons.restore,
                label: AppLocalizations.of(context)!.resetToDefault,
                isDark: isDark,
                onTap: () {
                  HapticFeedback.selectionClick();
                  // Reset the profile currently displayed in this
                  // panel's palette — not the global default — so the
                  // user's "restore" action matches what they see.
                  ref
                      .read(actionBarProvider.notifier)
                      .resetProfileToDefault(activeProfile.id);
                  Navigator.pop(context);
                },
              ),
            ],
          ),
        ],
      ),
    );
  }

  Widget _buildMetaChip(
    BuildContext context, {
    required IconData icon,
    required String label,
    required bool isDark,
    required VoidCallback onTap,
  }) {
    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(6),
        child: Container(
          constraints: const BoxConstraints(minHeight: 30),
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
          decoration: BoxDecoration(
            color: isDark
                ? DesignColors.keyBackground
                : DesignColors.keyBackgroundLight,
            border: Border.all(
              color: isDark
                  ? DesignColors.borderDark
                  : DesignColors.borderLight,
              width: 0.5,
            ),
            borderRadius: BorderRadius.circular(6),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(
                icon,
                size: 14,
                color: isDark
                    ? DesignColors.textSecondary
                    : DesignColors.textSecondaryLight,
              ),
              const SizedBox(width: 6),
              Text(
                label,
                style: TextStyle(
                  fontSize: 12,
                  fontWeight: FontWeight.w500,
                  color: isDark
                      ? DesignColors.textPrimary
                      : DesignColors.textPrimaryLight,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  // ---------------------------------------------------------------------------
  // Profile dialogs (preserved from pre-0.9.1 ProfileSheet)
  // ---------------------------------------------------------------------------

  void _confirmDelete(
      BuildContext context, WidgetRef ref, ActionBarProfile profile) {
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(AppLocalizations.of(context)!.deleteProfile),
        content: Text(
            AppLocalizations.of(context)!.deleteProfileContent(profile.name)),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: Text(AppLocalizations.of(context)!.buttonCancel),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: Colors.red),
            onPressed: () {
              ref.read(actionBarProvider.notifier).deleteProfile(profile.id);
              Navigator.pop(ctx);
            },
            child: Text(AppLocalizations.of(context)!.buttonDelete),
          ),
        ],
      ),
    );
  }

  void _showCreateProfileDialog(BuildContext context, WidgetRef ref) {
    final nameController = TextEditingController();
    final state = ref.read(actionBarProvider);
    // Default template = the profile currently active on this panel,
    // so a "save-as" off the user's current toolbar is one tap away.
    String? templateId = state.profileForPanel(panelKey).id;
    showDialog(
      context: context,
      builder: (ctx) => StatefulBuilder(
        builder: (ctx, setDialogState) => AlertDialog(
          title: Text(AppLocalizations.of(context)!.newProfile),
          content: SizedBox(
            width: 320,
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                TextField(
                  controller: nameController,
                  autofocus: true,
                  decoration: InputDecoration(
                    labelText: AppLocalizations.of(context)!.profileName,
                    border: const OutlineInputBorder(),
                    isDense: true,
                  ),
                ),
                const SizedBox(height: 12),
                // "Start from" dropdown — pick any existing profile to
                // copy its groups as a template. This replaces the old
                // hardcoded ESC/TAB/CTRL/ALT/RET starter so a new
                // profile inherits the user's curated layout instead of
                // a stale 5-button stub.
                DropdownButtonFormField<String>(
                  initialValue: templateId,
                  isDense: true,
                  decoration: InputDecoration(
                    labelText: AppLocalizations.of(context)!.startFromProfile,
                    border: const OutlineInputBorder(),
                    isDense: true,
                  ),
                  items: [
                    for (final p in state.profiles)
                      DropdownMenuItem(
                        value: p.id,
                        child: Text(p.name, overflow: TextOverflow.ellipsis),
                      ),
                  ],
                  onChanged: (v) => setDialogState(() => templateId = v),
                ),
              ],
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(ctx),
              child: Text(AppLocalizations.of(context)!.buttonCancel),
            ),
            FilledButton(
              onPressed: () {
                final name = nameController.text.trim();
                if (name.isEmpty) return;
                final source = state.profiles
                    .firstWhere((p) => p.id == templateId,
                        orElse: () => state.profiles.first);
                // Deep-copy groups so editing the new profile won't
                // mutate the template's button list. Each group/button
                // also needs a fresh ID — reusing IDs would make the
                // reorderable list confuse identities across profiles.
                final cloned = <ActionBarGroup>[];
                final ts = DateTime.now().millisecondsSinceEpoch;
                for (var gi = 0; gi < source.groups.length; gi++) {
                  final g = source.groups[gi];
                  cloned.add(ActionBarGroup(
                    id: 'g_${ts}_$gi',
                    name: g.name,
                    buttons: [
                      for (var bi = 0; bi < g.buttons.length; bi++)
                        g.buttons[bi].copyWith(id: 'b_${ts}_${gi}_$bi'),
                    ],
                  ));
                }
                final profile = ActionBarProfile(
                  id: 'custom_$ts',
                  name: name,
                  groups: cloned,
                );
                final notifier = ref.read(actionBarProvider.notifier);
                notifier.addCustomProfile(profile);
                // Newly created profiles become active on the current
                // panel only — not globally — so other panes keep
                // whatever profile they had.
                final key = panelKey;
                if (key != null) {
                  notifier.setActiveProfileForPanel(key, profile.id);
                } else {
                  notifier.setActiveProfile(profile.id);
                }
                Navigator.pop(ctx);
              },
              child: Text(AppLocalizations.of(context)!.buttonCreate),
            ),
          ],
        ),
      ),
    );
  }
}

class _ProfileRow extends StatelessWidget {
  final ActionBarProfile profile;
  final bool isActive;
  final bool isDark;
  final VoidCallback onSelect;
  final VoidCallback onEdit;
  final VoidCallback? onDelete;

  const _ProfileRow({
    required this.profile,
    required this.isActive,
    required this.isDark,
    required this.onSelect,
    required this.onEdit,
    this.onDelete,
  });

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onSelect,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
        child: Row(
          children: [
            Icon(
              isActive
                  ? Icons.radio_button_checked
                  : Icons.radio_button_unchecked,
              size: 20,
              color: isActive
                  ? DesignColors.primary
                  : (isDark
                      ? DesignColors.textMuted
                      : DesignColors.textMutedLight),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Row(
                children: [
                  Flexible(
                    child: Text(
                      profile.name,
                      style: TextStyle(
                        fontSize: 15,
                        fontWeight:
                            isActive ? FontWeight.w600 : FontWeight.normal,
                        color: isActive
                            ? DesignColors.primary
                            : (isDark
                                ? DesignColors.textPrimary
                                : DesignColors.textPrimaryLight),
                      ),
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                  if (profile.isBuiltIn) ...[
                    const SizedBox(width: 8),
                    Container(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 6, vertical: 1),
                      decoration: BoxDecoration(
                        color: isDark
                            ? DesignColors.textMuted.withValues(alpha: 0.2)
                            : DesignColors.textMutedLight
                                .withValues(alpha: 0.2),
                        borderRadius: BorderRadius.circular(4),
                      ),
                      child: Text(
                        AppLocalizations.of(context)!.builtIn.toLowerCase(),
                        style: TextStyle(
                          fontSize: 10,
                          color: isDark
                              ? DesignColors.textMuted
                              : DesignColors.textMutedLight,
                        ),
                      ),
                    ),
                  ],
                ],
              ),
            ),
            // Edit button
            IconButton(
              icon: Icon(
                Icons.edit_outlined,
                size: 18,
                color: isDark
                    ? DesignColors.textMuted
                    : DesignColors.textMutedLight,
              ),
              onPressed: onEdit,
              visualDensity: VisualDensity.compact,
            ),
            // Delete button (only for non-built-in)
            if (onDelete != null)
              IconButton(
                icon: Icon(
                  Icons.delete_outline,
                  size: 18,
                  color: isDark
                      ? DesignColors.textMuted
                      : DesignColors.textMutedLight,
                ),
                onPressed: onDelete,
                visualDensity: VisualDensity.compact,
              ),
          ],
        ),
      ),
    );
  }
}
