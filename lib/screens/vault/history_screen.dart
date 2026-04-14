import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import 'package:termipod/l10n/app_localizations.dart';

import '../../providers/history_provider.dart';
import '../../providers/snippet_provider.dart';
import '../../theme/design_colors.dart';
import 'snippets_screen.dart';

/// Full history screen body widget for the Vault.
///
/// Shows all history items with search, swipe-to-delete,
/// and save-as-snippet action.
class HistoryScreen extends ConsumerStatefulWidget {
  const HistoryScreen({super.key});

  @override
  ConsumerState<HistoryScreen> createState() => _HistoryScreenState();
}

class _HistoryScreenState extends ConsumerState<HistoryScreen> {
  final _searchController = TextEditingController();
  String _searchQuery = '';

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final historyState = ref.watch(historyProvider);
    final items = historyState.items;

    if (historyState.isLoading) {
      return const Center(child: CircularProgressIndicator());
    }

    if (items.isEmpty) {
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 24),
        child: Center(
          child: Text(
            AppLocalizations.of(context)!.noHistoryYet,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 14,
              color: isDark
                  ? DesignColors.textMuted
                  : DesignColors.textMutedLight,
            ),
          ),
        ),
      );
    }

    final filtered = _searchQuery.isEmpty
        ? items
        : items
            .where(
                (h) => h.toLowerCase().contains(_searchQuery.toLowerCase()))
            .toList();

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        // Search field
        Padding(
          padding: const EdgeInsets.only(bottom: 12),
          child: TextField(
            controller: _searchController,
            autofocus: false,
            onChanged: (v) => setState(() => _searchQuery = v),
            decoration: InputDecoration(
              hintText: AppLocalizations.of(context)!.searchHistory,
              prefixIcon: const Icon(Icons.search, size: 20),
              filled: true,
              fillColor:
                  isDark ? DesignColors.inputDark : DesignColors.inputLight,
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(12),
                borderSide: BorderSide.none,
              ),
              contentPadding: const EdgeInsets.symmetric(vertical: 8),
              isDense: true,
            ),
            style: const TextStyle(fontSize: 14),
          ),
        ),
        if (filtered.isEmpty)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 24),
            child: Center(
              child: Text(
                AppLocalizations.of(context)!.noMatchingHistory,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 14,
                  color: isDark
                      ? DesignColors.textMuted
                      : DesignColors.textMutedLight,
                ),
              ),
            ),
          )
        else
          for (final cmd in filtered) _HistoryTile(command: cmd),
      ],
    );
  }
}

class _HistoryTile extends ConsumerWidget {
  final String command;

  const _HistoryTile({required this.command});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final colorScheme = Theme.of(context).colorScheme;

    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Dismissible(
        key: Key('hist-$command'),
        direction: DismissDirection.endToStart,
        background: Container(
          alignment: Alignment.centerRight,
          padding: const EdgeInsets.only(right: 20),
          decoration: BoxDecoration(
            color: DesignColors.error.withValues(alpha: 0.2),
            borderRadius: BorderRadius.circular(12),
          ),
          child: const Icon(Icons.delete, color: DesignColors.error),
        ),
        onDismissed: (_) {
          ref.read(historyProvider.notifier).delete(command);
        },
        child: InkWell(
          onTap: () {
            Clipboard.setData(ClipboardData(text: command));
            ScaffoldMessenger.of(context).showSnackBar(
              SnackBar(
                content: Text(AppLocalizations.of(context)!.copiedToClipboard),
                duration: Duration(seconds: 1),
              ),
            );
          },
          onLongPress: () {
            HapticFeedback.mediumImpact();
            _showSaveAsSnippet(context, ref);
          },
          borderRadius: BorderRadius.circular(12),
          child: Container(
            padding: const EdgeInsets.all(14),
            decoration: BoxDecoration(
              color: isDark
                  ? DesignColors.surfaceDark
                  : DesignColors.surfaceLight,
              borderRadius: BorderRadius.circular(12),
              border: Border.all(
                color: isDark
                    ? DesignColors.borderDark
                    : DesignColors.borderLight,
              ),
            ),
            child: Row(
              children: [
                Icon(
                  Icons.terminal,
                  size: 18,
                  color: isDark
                      ? DesignColors.textMuted
                      : DesignColors.textMutedLight,
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: Text(
                    command,
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 13,
                      color: colorScheme.onSurface,
                    ),
                    maxLines: 3,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                IconButton(
                  icon: Icon(
                    Icons.edit_outlined,
                    size: 18,
                    color: isDark
                        ? DesignColors.textMuted
                        : DesignColors.textMutedLight,
                  ),
                  onPressed: () {
                    HapticFeedback.selectionClick();
                    _showEditDialog(context, ref);
                  },
                  tooltip: AppLocalizations.of(context)!.editHistory,
                  visualDensity: VisualDensity.compact,
                ),
                IconButton(
                  icon: Icon(
                    Icons.bookmark_add_outlined,
                    size: 18,
                    color: isDark
                        ? DesignColors.textMuted
                        : DesignColors.textMutedLight,
                  ),
                  onPressed: () {
                    HapticFeedback.selectionClick();
                    _showSaveAsSnippet(context, ref);
                  },
                  tooltip: AppLocalizations.of(context)!.saveAsSnippet,
                  visualDensity: VisualDensity.compact,
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  void _showEditDialog(BuildContext context, WidgetRef ref) {
    final controller = TextEditingController(text: command);

    showDialog<void>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: Text(AppLocalizations.of(context)!.editHistoryTitle),
        content: TextField(
          controller: controller,
          decoration: InputDecoration(
            labelText: AppLocalizations.of(context)!.editHistoryLabel,
            border: const OutlineInputBorder(),
            isDense: true,
          ),
          autofocus: true,
          maxLines: 5,
          minLines: 1,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(dialogContext),
            child: Text(AppLocalizations.of(context)!.buttonCancel),
          ),
          FilledButton(
            onPressed: () {
              final newCmd = controller.text.trim();
              if (newCmd.isNotEmpty && newCmd != command) {
                ref.read(historyProvider.notifier).update(command, newCmd);
              }
              Navigator.pop(dialogContext);
            },
            child: Text(AppLocalizations.of(context)!.buttonSave),
          ),
        ],
      ),
    );
  }

  void _showSaveAsSnippet(BuildContext context, WidgetRef ref) {
    // Use the full SnippetEditDialog so users can pick a category and
    // add variable placeholders from history — the name-only AlertDialog
    // that used to live here couldn't do either.
    final messenger = ScaffoldMessenger.of(context);
    final savedLabel = AppLocalizations.of(context)!.savedToSnippets;
    final notifier = ref.read(snippetsProvider.notifier);

    showDialog<void>(
      context: context,
      builder: (dialogContext) => SnippetEditDialog(
        initialName: command.length > 30 ? command.substring(0, 30) : command,
        initialContent: command,
        onSave: (name, content, category, variables) {
          notifier.addSnippet(
            name: name,
            content: content,
            category: category,
            variables: variables,
          );
          messenger.showSnackBar(
            SnackBar(
              content: Text(savedLabel),
              duration: const Duration(seconds: 2),
            ),
          );
        },
      ),
    );
  }
}
