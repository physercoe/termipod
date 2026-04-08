import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'package:flutter_muxpod/l10n/app_localizations.dart';

import '../../providers/history_provider.dart';
import '../../providers/snippet_provider.dart';
import '../../theme/design_colors.dart';

/// Recent commands bottom sheet (hot, last ~10).
///
/// Quick-access from [+] insert menu. Tap to insert into compose,
/// double-tap to send immediately. Swipe to delete, long-press to save as snippet.
class RecentSheet extends ConsumerWidget {
  final void Function(String command) onInsert;
  final void Function(String command) onSendImmediately;

  const RecentSheet({
    super.key,
    required this.onInsert,
    required this.onSendImmediately,
  });

  static Future<void> show(
    BuildContext context, {
    required WidgetRef ref,
    required void Function(String command) onInsert,
    required void Function(String command) onSendImmediately,
  }) async {
    await showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.transparent,
      builder: (context) => DraggableScrollableSheet(
        initialChildSize: 0.4,
        minChildSize: 0.25,
        maxChildSize: 0.7,
        expand: false,
        builder: (context, scrollController) {
          return RecentSheet(
            onInsert: (cmd) {
              Navigator.pop(context);
              onInsert(cmd);
            },
            onSendImmediately: (cmd) {
              Navigator.pop(context);
              onSendImmediately(cmd);
            },
          );
        },
      ),
    );
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final historyState = ref.watch(historyProvider);
    final recent = historyState.recent;

    return Container(
      decoration: BoxDecoration(
        color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        borderRadius: const BorderRadius.vertical(top: Radius.circular(16)),
      ),
      child: Column(
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
          // Title row
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
            child: Row(
              children: [
                Icon(Icons.history, size: 20,
                    color: isDark
                        ? DesignColors.textSecondary
                        : DesignColors.textSecondaryLight),
                const SizedBox(width: 6),
                Text(
                  AppLocalizations.of(context)!.recent,
                  style: TextStyle(
                    fontSize: 16,
                    fontWeight: FontWeight.w600,
                    color: isDark
                        ? DesignColors.textPrimary
                        : DesignColors.textPrimaryLight,
                  ),
                ),
                const Spacer(),
                if (recent.isNotEmpty)
                  TextButton(
                    onPressed: () {
                      ref.read(historyProvider.notifier).clear();
                    },
                    style: TextButton.styleFrom(
                      foregroundColor: Colors.red,
                      padding: const EdgeInsets.symmetric(horizontal: 8),
                    ),
                    child: Text(AppLocalizations.of(context)!.clear),
                  ),
              ],
            ),
          ),
          const SizedBox(height: 4),
          // Recent list
          Expanded(
            child: recent.isEmpty
                ? Center(
                    child: Text(
                      AppLocalizations.of(context)!.noRecentCommands,
                      style: TextStyle(
                        color: isDark
                            ? DesignColors.textMuted
                            : DesignColors.textMutedLight,
                      ),
                    ),
                  )
                : ListView.builder(
                    padding: const EdgeInsets.symmetric(horizontal: 8),
                    itemCount: recent.length,
                    itemBuilder: (context, index) {
                      final cmd = recent[index];
                      return Dismissible(
                        key: ValueKey('recent-$index-$cmd'),
                        direction: DismissDirection.endToStart,
                        onDismissed: (_) {
                          ref.read(historyProvider.notifier).delete(cmd);
                        },
                        background: Container(
                          alignment: Alignment.centerRight,
                          padding: const EdgeInsets.only(right: 16),
                          color: Colors.red.withValues(alpha: 0.2),
                          child: const Icon(Icons.delete_outline,
                              color: Colors.red, size: 20),
                        ),
                        child: InkWell(
                          onTap: () {
                            HapticFeedback.selectionClick();
                            onInsert(cmd);
                          },
                          onDoubleTap: () {
                            HapticFeedback.lightImpact();
                            onSendImmediately(cmd);
                          },
                          onLongPress: () {
                            HapticFeedback.mediumImpact();
                            _showSaveAsSnippet(context, ref, cmd);
                          },
                          borderRadius: BorderRadius.circular(8),
                          child: Padding(
                            padding: const EdgeInsets.symmetric(
                                horizontal: 12, vertical: 10),
                            child: Row(
                              children: [
                                Expanded(
                                  child: Text(
                                    cmd,
                                    style: TextStyle(
                                      fontSize: 14,
                                      color: isDark
                                          ? DesignColors.textPrimary
                                          : DesignColors.textPrimaryLight,
                                    ),
                                    maxLines: 2,
                                    overflow: TextOverflow.ellipsis,
                                  ),
                                ),
                                Icon(
                                  Icons.content_paste,
                                  size: 14,
                                  color: isDark
                                      ? DesignColors.textMuted
                                      : DesignColors.textMutedLight,
                                ),
                              ],
                            ),
                          ),
                        ),
                      );
                    },
                  ),
          ),
        ],
      ),
    );
  }

  void _showSaveAsSnippet(
      BuildContext context, WidgetRef ref, String command) {
    final nameController = TextEditingController(
      text: command.length > 30 ? command.substring(0, 30) : command,
    );

    showDialog<void>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: Text(AppLocalizations.of(context)!.saveAsSnippet),
        content: TextField(
          controller: nameController,
          decoration: InputDecoration(
            labelText: AppLocalizations.of(context)!.snippetName,
            border: OutlineInputBorder(),
            isDense: true,
          ),
          autofocus: true,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(dialogContext),
            child: Text(AppLocalizations.of(context)!.buttonCancel),
          ),
          FilledButton(
            onPressed: () {
              if (nameController.text.isNotEmpty) {
                ref.read(snippetsProvider.notifier).addSnippet(
                      name: nameController.text,
                      content: command,
                    );
                Navigator.pop(dialogContext);
                ScaffoldMessenger.of(context).showSnackBar(
                  SnackBar(
                    content: Text(AppLocalizations.of(context)!.savedToSnippets),
                    duration: Duration(seconds: 2),
                  ),
                );
              }
            },
            child: Text(AppLocalizations.of(context)!.buttonSave),
          ),
        ],
      ),
    );
  }
}
