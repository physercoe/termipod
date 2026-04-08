import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../providers/action_bar_provider.dart';
import '../../theme/design_colors.dart';

/// The [+] insert/action menu that appears from the compose bar.
///
/// Shows a vertical popup menu with: Snippets, Command Menu, History,
/// File Transfer, Image Transfer, Paste Clipboard, Direct Input.
class InsertMenu {
  InsertMenu._();

  static Future<void> show(
    BuildContext context, {
    required WidgetRef ref,
    VoidCallback? onSnippets,
    VoidCallback? onCommandMenu,
    VoidCallback? onHistory,
    VoidCallback? onFileTransfer,
    VoidCallback? onFileDownload,
    VoidCallback? onImageTransfer,
    VoidCallback? onDirectInput,
  }) async {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final state = ref.read(actionBarProvider);

    await showModalBottomSheet<void>(
      context: context,
      backgroundColor: Colors.transparent,
      builder: (context) {
        return Container(
          decoration: BoxDecoration(
            color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
            borderRadius: const BorderRadius.vertical(top: Radius.circular(16)),
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
                // Menu items
                if (onSnippets != null)
                  _buildItem(
                    context,
                    icon: Icons.code,
                    label: 'Snippets',
                    isDark: isDark,
                    onTap: () {
                      Navigator.pop(context);
                      onSnippets();
                    },
                  ),
                if (onCommandMenu != null)
                  _buildItem(
                    context,
                    icon: Icons.terminal,
                    label: 'Command Menu',
                    isDark: isDark,
                    onTap: () {
                      Navigator.pop(context);
                      onCommandMenu();
                    },
                  ),
                if (onHistory != null && state.commandHistory.isNotEmpty)
                  _buildItem(
                    context,
                    icon: Icons.history,
                    label: 'History',
                    isDark: isDark,
                    onTap: () {
                      Navigator.pop(context);
                      onHistory();
                    },
                  ),
                const Divider(height: 1),
                if (onFileTransfer != null)
                  _buildItem(
                    context,
                    icon: Icons.upload_file,
                    label: 'File Upload',
                    isDark: isDark,
                    onTap: () {
                      Navigator.pop(context);
                      onFileTransfer();
                    },
                  ),
                if (onFileDownload != null)
                  _buildItem(
                    context,
                    icon: Icons.download,
                    label: 'File Download',
                    isDark: isDark,
                    onTap: () {
                      Navigator.pop(context);
                      onFileDownload();
                    },
                  ),
                if (onImageTransfer != null)
                  _buildItem(
                    context,
                    icon: Icons.image,
                    label: 'Image Transfer',
                    isDark: isDark,
                    onTap: () {
                      Navigator.pop(context);
                      onImageTransfer();
                    },
                  ),
                if (onDirectInput != null) ...[
                  const Divider(height: 1),
                  _buildItem(
                    context,
                    icon: state.composeMode
                        ? Icons.keyboard
                        : Icons.edit_note_rounded,
                    label: state.composeMode
                        ? 'Direct Input Mode'
                        : 'Compose Mode',
                    isDark: isDark,
                    onTap: () {
                      Navigator.pop(context);
                      onDirectInput();
                    },
                  ),
                ],
                const SizedBox(height: 8),
              ],
            ),
          ),
        );
      },
    );
  }

  static Widget _buildItem(
    BuildContext context, {
    required IconData icon,
    required String label,
    required bool isDark,
    required VoidCallback onTap,
  }) {
    return InkWell(
      onTap: () {
        HapticFeedback.selectionClick();
        onTap();
      },
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 14),
        child: Row(
          children: [
            Icon(
              icon,
              size: 22,
              color: isDark
                  ? DesignColors.textSecondary
                  : DesignColors.textSecondaryLight,
            ),
            const SizedBox(width: 16),
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
}
