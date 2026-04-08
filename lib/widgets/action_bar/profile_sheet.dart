import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../models/action_bar_config.dart';
import '../../providers/action_bar_provider.dart';
import '../../screens/settings/action_bar_settings_screen.dart';
import '../../theme/design_colors.dart';

/// Profile selection bottom sheet from the [⋮] button.
///
/// Shows radio list of profiles with edit/delete actions,
/// plus links to settings and profile creation.
class ProfileSheet extends ConsumerWidget {
  final VoidCallback? onEditGroups;
  final VoidCallback? onManageSnippets;

  const ProfileSheet({
    super.key,
    this.onEditGroups,
    this.onManageSnippets,
  });

  static Future<void> show(
    BuildContext context, {
    required WidgetRef ref,
    VoidCallback? onEditGroups,
    VoidCallback? onManageSnippets,
  }) async {
    await showModalBottomSheet<void>(
      context: context,
      backgroundColor: Colors.transparent,
      isScrollControlled: true,
      builder: (context) => ProfileSheet(
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
      ),
    );
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final state = ref.watch(actionBarProvider);

    return Container(
      constraints: BoxConstraints(
        maxHeight: MediaQuery.of(context).size.height * 0.7,
      ),
      decoration: BoxDecoration(
        color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        borderRadius: const BorderRadius.vertical(top: Radius.circular(16)),
      ),
      child: SafeArea(
        top: false,
        child: SingleChildScrollView(
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
              // Title
              Padding(
                padding:
                    const EdgeInsets.symmetric(horizontal: 20, vertical: 4),
                child: Row(
                  children: [
                    Text(
                      'Toolbar Profile',
                      style: TextStyle(
                        fontSize: 16,
                        fontWeight: FontWeight.w600,
                        color: isDark
                            ? DesignColors.textPrimary
                            : DesignColors.textPrimaryLight,
                      ),
                    ),
                    const Spacer(),
                    TextButton.icon(
                      onPressed: () {
                        Navigator.pop(context);
                        _showCreateProfileDialog(context, ref);
                      },
                      icon: const Icon(Icons.add, size: 18),
                      label: const Text('New'),
                      style: TextButton.styleFrom(
                        foregroundColor: DesignColors.primary,
                        padding: const EdgeInsets.symmetric(horizontal: 8),
                      ),
                    ),
                  ],
                ),
              ),
              const SizedBox(height: 4),
              // Profile list
              ...state.profiles.map((profile) {
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
                  },
                  onEdit: () {
                    Navigator.pop(context);
                    Navigator.push(
                      context,
                      MaterialPageRoute(
                        builder: (_) => const ActionBarSettingsScreen(),
                      ),
                    );
                    // Make sure we switch to this profile first
                    ref
                        .read(actionBarProvider.notifier)
                        .setActiveProfile(profile.id);
                  },
                  onDelete: profile.isBuiltIn
                      ? null
                      : () => _confirmDelete(context, ref, profile),
                );
              }),
              const Divider(height: 1),
              // Action buttons
              if (onManageSnippets != null)
                _buildAction(
                  context,
                  icon: Icons.code,
                  label: 'Manage Snippets',
                  isDark: isDark,
                  onTap: onManageSnippets!,
                ),
              // Reset to default
              _buildAction(
                context,
                icon: Icons.restore,
                label: 'Reset to Default',
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
    );
  }

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
        title: const Text('Delete Profile'),
        content: Text('Delete "${profile.name}"?'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('Cancel'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: Colors.red),
            onPressed: () {
              ref.read(actionBarProvider.notifier).deleteProfile(profile.id);
              Navigator.pop(ctx);
            },
            child: const Text('Delete'),
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
        title: const Text('New Profile'),
        content: TextField(
          controller: nameController,
          autofocus: true,
          decoration: const InputDecoration(
            labelText: 'Profile Name',
            border: OutlineInputBorder(),
            isDense: true,
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('Cancel'),
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
            child: const Text('Create'),
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
                        'built-in',
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
