import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../providers/snippet_provider.dart';
import '../../theme/design_colors.dart';

/// Snippet picker bottom sheet.
///
/// Shows saved snippets. Tap to insert into compose, long-press to edit.
class SnippetPickerSheet extends ConsumerWidget {
  /// Called when a snippet is selected to insert into compose
  final void Function(String content) onInsert;

  /// Called when a snippet should be sent immediately
  final void Function(String content) onSendImmediately;

  const SnippetPickerSheet({
    super.key,
    required this.onInsert,
    required this.onSendImmediately,
  });

  static Future<void> show(
    BuildContext context, {
    required WidgetRef ref,
    required void Function(String content) onInsert,
    required void Function(String content) onSendImmediately,
  }) async {
    await showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.transparent,
      builder: (context) => DraggableScrollableSheet(
        initialChildSize: 0.4,
        minChildSize: 0.25,
        maxChildSize: 0.7,
        builder: (context, scrollController) {
          return SnippetPickerSheet(
            onInsert: (content) {
              Navigator.pop(context);
              onInsert(content);
            },
            onSendImmediately: (content) {
              Navigator.pop(context);
              onSendImmediately(content);
            },
          );
        },
      ),
    );
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final snippetsState = ref.watch(snippetsProvider);
    final snippets = snippetsState.snippets;

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
          // Title
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
            child: Row(
              children: [
                Text(
                  'Snippets',
                  style: TextStyle(
                    fontSize: 16,
                    fontWeight: FontWeight.w600,
                    color: isDark
                        ? DesignColors.textPrimary
                        : DesignColors.textPrimaryLight,
                  ),
                ),
                const Spacer(),
                // Add snippet button
                TextButton.icon(
                  onPressed: () {
                    Navigator.pop(context);
                    _showAddSnippetDialog(context, ref);
                  },
                  icon: const Icon(Icons.add, size: 18),
                  label: const Text('New'),
                  style: TextButton.styleFrom(
                    foregroundColor: DesignColors.primary,
                    padding:
                        const EdgeInsets.symmetric(horizontal: 8),
                  ),
                ),
              ],
            ),
          ),
          const SizedBox(height: 4),
          // Snippet list
          Expanded(
            child: snippets.isEmpty
                ? Center(
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Icon(
                          Icons.code_off,
                          size: 48,
                          color: isDark
                              ? DesignColors.textMuted
                              : DesignColors.textMutedLight,
                        ),
                        const SizedBox(height: 8),
                        Text(
                          'No snippets yet',
                          style: TextStyle(
                            color: isDark
                                ? DesignColors.textMuted
                                : DesignColors.textMutedLight,
                          ),
                        ),
                        const SizedBox(height: 4),
                        Text(
                          'Tap "New" to create one',
                          style: TextStyle(
                            fontSize: 12,
                            color: isDark
                                ? DesignColors.textMuted
                                : DesignColors.textMutedLight,
                          ),
                        ),
                      ],
                    ),
                  )
                : ListView.builder(
                    padding: const EdgeInsets.symmetric(horizontal: 8),
                    itemCount: snippets.length,
                    itemBuilder: (context, index) {
                      final snippet = snippets[index];
                      return _buildSnippetItem(
                          context, ref, snippet, isDark);
                    },
                  ),
          ),
        ],
      ),
    );
  }

  Widget _buildSnippetItem(
      BuildContext context, WidgetRef ref, Snippet snippet, bool isDark) {
    return InkWell(
      onTap: () {
        HapticFeedback.selectionClick();
        if (snippet.variables.isNotEmpty) {
          _showVariableDialog(context, snippet);
        } else if (snippet.sendImmediately) {
          onSendImmediately(snippet.content);
        } else {
          onInsert(snippet.content);
        }
      },
      borderRadius: BorderRadius.circular(8),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              snippet.name,
              style: TextStyle(
                fontSize: 14,
                fontWeight: FontWeight.w500,
                color: isDark
                    ? DesignColors.textPrimary
                    : DesignColors.textPrimaryLight,
              ),
            ),
            const SizedBox(height: 2),
            Text(
              snippet.content,
              style: TextStyle(
                fontSize: 12,
                color: isDark
                    ? DesignColors.textSecondary
                    : DesignColors.textSecondaryLight,
                fontFamily: 'monospace',
              ),
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
            ),
          ],
        ),
      ),
    );
  }

  void _showVariableDialog(BuildContext context, Snippet snippet) {
    final controllers = <String, TextEditingController>{};
    for (final v in snippet.variables) {
      controllers[v.name] = TextEditingController(text: v.defaultValue);
    }

    showDialog<void>(
      context: context,
      builder: (dialogContext) {
        return AlertDialog(
          title: Text(snippet.name),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            children: snippet.variables.map((v) {
              return Padding(
                padding: const EdgeInsets.only(bottom: 8),
                child: TextField(
                  controller: controllers[v.name],
                  decoration: InputDecoration(
                    labelText: v.name,
                    border: const OutlineInputBorder(),
                    isDense: true,
                  ),
                ),
              );
            }).toList(),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(dialogContext),
              child: const Text('Cancel'),
            ),
            FilledButton(
              onPressed: () {
                final values = <String, String>{};
                for (final entry in controllers.entries) {
                  values[entry.key] = entry.value.text;
                }
                final resolved = snippet.resolve(values);
                Navigator.pop(dialogContext);
                // Close the picker sheet too
                Navigator.pop(context);
                if (snippet.sendImmediately) {
                  onSendImmediately(resolved);
                } else {
                  onInsert(resolved);
                }
              },
              child: const Text('Insert'),
            ),
          ],
        );
      },
    );

    // Clean up controllers when dialog is dismissed
    // (TextField controllers are automatically disposed with the dialog)
  }

  void _showAddSnippetDialog(BuildContext context, WidgetRef ref) {
    final nameController = TextEditingController();
    final contentController = TextEditingController();

    showDialog<void>(
      context: context,
      builder: (dialogContext) {
        return AlertDialog(
          title: const Text('New Snippet'),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              TextField(
                controller: nameController,
                decoration: const InputDecoration(
                  labelText: 'Name',
                  border: OutlineInputBorder(),
                  isDense: true,
                ),
                autofocus: true,
              ),
              const SizedBox(height: 12),
              TextField(
                controller: contentController,
                decoration: const InputDecoration(
                  labelText: 'Command',
                  hintText: 'e.g., docker compose up -d',
                  border: OutlineInputBorder(),
                  isDense: true,
                ),
                maxLines: 3,
              ),
            ],
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(dialogContext),
              child: const Text('Cancel'),
            ),
            FilledButton(
              onPressed: () {
                if (nameController.text.isNotEmpty &&
                    contentController.text.isNotEmpty) {
                  ref.read(snippetsProvider.notifier).addSnippet(
                        name: nameController.text,
                        content: contentController.text,
                      );
                  Navigator.pop(dialogContext);
                }
              },
              child: const Text('Save'),
            ),
          ],
        );
      },
    );
  }
}
