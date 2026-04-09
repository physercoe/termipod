import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'package:flutter_muxpod/l10n/app_localizations.dart';

import '../../models/action_bar_config.dart';
import '../../providers/action_bar_provider.dart';
import '../../screens/settings/action_bar_settings_screen.dart';
import '../../theme/design_colors.dart';

/// Key Palette bottom sheet opened from the [⋮] button in the action bar.
///
/// In 0.9.1 this sheet was repurposed from a pure profile switcher into
/// a full key palette: it shows every group of the active profile as
/// chip rows, so users can reach infrequent keys without swiping through
/// action-bar pages. Profile selection collapses into a compact header.
///
/// Actions like file transfer, snippet picker, and direct input toggle
/// are intentionally NOT rendered in the palette — those live in the
/// action bar and would duplicate dispatch logic if handled here.
class ProfileSheet extends ConsumerWidget {
  final VoidCallback? onEditGroups;
  final VoidCallback? onManageSnippets;
  final void Function(String literal)? onKeyTap;
  final void Function(String tmuxKey)? onSpecialKeyTap;
  final void Function(String modifier)? onModifierTap;
  final ScrollController? scrollController;

  const ProfileSheet({
    super.key,
    this.onEditGroups,
    this.onManageSnippets,
    this.onKeyTap,
    this.onSpecialKeyTap,
    this.onModifierTap,
    this.scrollController,
  });

  static Future<void> show(
    BuildContext context, {
    required WidgetRef ref,
    VoidCallback? onEditGroups,
    VoidCallback? onManageSnippets,
    void Function(String literal)? onKeyTap,
    void Function(String tmuxKey)? onSpecialKeyTap,
    void Function(String modifier)? onModifierTap,
  }) async {
    await showModalBottomSheet<void>(
      context: context,
      backgroundColor: Colors.transparent,
      isScrollControlled: true,
      builder: (sheetContext) => DraggableScrollableSheet(
        initialChildSize: 0.6,
        minChildSize: 0.3,
        maxChildSize: 0.9,
        expand: false,
        builder: (context, scrollController) => ProfileSheet(
          scrollController: scrollController,
          onEditGroups: onEditGroups != null
              ? () {
                  Navigator.pop(context);
                  onEditGroups();
                }
              : null,
          onManageSnippets: onManageSnippets != null
              ? () {
                  Navigator.pop(context);
                  onManageSnippets();
                }
              : null,
          onKeyTap: onKeyTap,
          onSpecialKeyTap: onSpecialKeyTap,
          onModifierTap: onModifierTap,
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final state = ref.watch(actionBarProvider);
    final activeProfile = state.profiles.firstWhere(
      (p) => p.id == state.activeProfileId,
      orElse: () => state.profiles.isNotEmpty
          ? state.profiles.first
          : const ActionBarProfile(id: '', name: '', groups: []),
    );

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
                    // Key Palette section header
                    Padding(
                      padding: const EdgeInsets.fromLTRB(20, 14, 20, 4),
                      child: Text(
                        AppLocalizations.of(context)!
                            .keyPaletteLabel
                            .toUpperCase(),
                        style: TextStyle(
                          fontSize: 11,
                          fontWeight: FontWeight.w600,
                          letterSpacing: 0.8,
                          color: isDark
                              ? DesignColors.textMuted
                              : DesignColors.textMutedLight,
                        ),
                      ),
                    ),
                    // Per-group rows — always includes Navigate group,
                    // unlike the action bar which filters it when the
                    // nav pad is enabled.
                    for (final group in activeProfile.groups)
                      _buildPaletteGroup(context, group, isDark),
                    const SizedBox(height: 8),
                    const Divider(height: 1),
                    // Meta actions
                    if (onManageSnippets != null)
                      _buildAction(
                        context,
                        icon: Icons.code,
                        label: AppLocalizations.of(context)!.manageSnippets,
                        isDark: isDark,
                        onTap: onManageSnippets!,
                      ),
                    _buildAction(
                      context,
                      icon: Icons.restore,
                      label: AppLocalizations.of(context)!.resetToDefault,
                      isDark: isDark,
                      onTap: () {
                        HapticFeedback.selectionClick();
                        ref
                            .read(actionBarProvider.notifier)
                            .resetProfileToDefault(state.activeProfileId);
                        Navigator.pop(context);
                      },
                    ),
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
                      final isActive = profile.id == state.activeProfileId;
                      return _ProfileRow(
                        profile: profile,
                        isActive: isActive,
                        isDark: isDark,
                        onSelect: () {
                          HapticFeedback.selectionClick();
                          ref
                              .read(actionBarProvider.notifier)
                              .setActiveProfile(profile.id);
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
                          ref
                              .read(actionBarProvider.notifier)
                              .setActiveProfile(profile.id);
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
    // Filter out action-type buttons: they're dispatched through
    // _handleButtonTap in the action bar and would require duplicating
    // that switch here. Users access them via the action bar directly.
    final chips = group.buttons
        .where((b) => b.type != ActionBarButtonType.action)
        .toList();
    if (chips.isEmpty) return const SizedBox.shrink();

    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            group.name,
            style: TextStyle(
              fontSize: 12,
              fontWeight: FontWeight.w500,
              color: isDark
                  ? DesignColors.textSecondary
                  : DesignColors.textSecondaryLight,
            ),
          ),
          const SizedBox(height: 6),
          Wrap(
            spacing: 6,
            runSpacing: 6,
            children: chips
                .map((b) => _buildPaletteChip(context, b, isDark))
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
    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: () => _handlePaletteTap(context, btn),
        borderRadius: BorderRadius.circular(8),
        child: Container(
          constraints: const BoxConstraints(minWidth: 44, minHeight: 36),
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
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
            borderRadius: BorderRadius.circular(8),
          ),
          child: Center(
            child: Text(
              btn.label,
              style: TextStyle(
                fontSize: 13,
                fontWeight: FontWeight.w500,
                fontFamily: 'monospace',
                color: isDark
                    ? DesignColors.textPrimary
                    : DesignColors.textPrimaryLight,
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
        // Filtered out in _buildPaletteGroup — unreachable here.
        break;
    }
  }

  // ---------------------------------------------------------------------------
  // Meta actions + dialogs (preserved from pre-0.9.1 ProfileSheet)
  // ---------------------------------------------------------------------------

  Widget _buildAction(
    BuildContext context, {
    required IconData icon,
    required String label,
    required bool isDark,
    required VoidCallback onTap,
  }) {
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 14),
        child: Row(
          children: [
            Icon(
              icon,
              size: 20,
              color: isDark
                  ? DesignColors.textSecondary
                  : DesignColors.textSecondaryLight,
            ),
            const SizedBox(width: 12),
            Text(
              label,
              style: TextStyle(
                fontSize: 15,
                color: isDark
                    ? DesignColors.textPrimary
                    : DesignColors.textPrimaryLight,
              ),
            ),
          ],
        ),
      ),
    );
  }

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
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(AppLocalizations.of(context)!.newProfile),
        content: TextField(
          controller: nameController,
          autofocus: true,
          decoration: InputDecoration(
            labelText: AppLocalizations.of(context)!.profileName,
            border: OutlineInputBorder(),
            isDense: true,
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
                ref
                    .read(actionBarProvider.notifier)
                    .setActiveProfile(profile.id);
                Navigator.pop(ctx);
              }
            },
            child: Text(AppLocalizations.of(context)!.buttonCreate),
          ),
        ],
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
