import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import 'package:flutter_muxpod/l10n/app_localizations.dart';

import '../../providers/history_provider.dart';
import '../../providers/snippet_provider.dart';
import '../../theme/design_colors.dart';

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

  void _showSaveAsSnippet(BuildContext context, WidgetRef ref) {
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
