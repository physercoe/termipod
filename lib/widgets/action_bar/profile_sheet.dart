import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../providers/action_bar_provider.dart';
import '../../theme/design_colors.dart';

/// Profile selection bottom sheet from the [⋮] button.
///
/// Shows radio list of profiles, plus links to settings.
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
            // Title
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 4),
              child: Text(
                'Toolbar Profile',
                style: TextStyle(
                  fontSize: 16,
                  fontWeight: FontWeight.w600,
                  color: isDark
                      ? DesignColors.textPrimary
                      : DesignColors.textPrimaryLight,
                ),
              ),
            ),
            const SizedBox(height: 8),
            // Profile radio list
            ...state.profiles.map((profile) {
              final isActive = profile.id == state.activeProfileId;
              return InkWell(
                onTap: () {
                  HapticFeedback.selectionClick();
                  ref
                      .read(actionBarProvider.notifier)
                      .setActiveProfile(profile.id);
                },
                child: Padding(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
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
                      Text(
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
              );
            }),
            const Divider(height: 1),
            // Action buttons
            if (onEditGroups != null)
              InkWell(
                onTap: onEditGroups,
                child: Padding(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 20, vertical: 14),
                  child: Row(
                    children: [
                      Icon(
                        Icons.tune,
                        size: 20,
                        color: isDark
                            ? DesignColors.textSecondary
                            : DesignColors.textSecondaryLight,
                      ),
                      const SizedBox(width: 12),
                      Text(
                        'Customize Groups',
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
              ),
            if (onManageSnippets != null)
              InkWell(
                onTap: onManageSnippets,
                child: Padding(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 20, vertical: 14),
                  child: Row(
                    children: [
                      Icon(
                        Icons.code,
                        size: 20,
                        color: isDark
                            ? DesignColors.textSecondary
                            : DesignColors.textSecondaryLight,
                      ),
                      const SizedBox(width: 12),
                      Text(
                        'Manage Snippets',
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
              ),
            // Reset to default
            InkWell(
              onTap: () {
                HapticFeedback.selectionClick();
                ref
                    .read(actionBarProvider.notifier)
                    .resetProfileToDefault(state.activeProfileId);
                Navigator.pop(context);
              },
              child: Padding(
                padding:
                    const EdgeInsets.symmetric(horizontal: 20, vertical: 14),
                child: Row(
                  children: [
                    Icon(
                      Icons.restore,
                      size: 20,
                      color: isDark
                          ? DesignColors.textSecondary
                          : DesignColors.textSecondaryLight,
                    ),
                    const SizedBox(width: 12),
                    Text(
                      'Reset to Default',
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
            ),
            const SizedBox(height: 8),
          ],
        ),
      ),
    );
  }
}
