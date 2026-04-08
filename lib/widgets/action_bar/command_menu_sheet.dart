import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../models/action_bar_config.dart';
import '../../providers/action_bar_provider.dart';
import '../../theme/design_colors.dart';

/// Command menu bottom sheet with search.
///
/// Shows slash commands from the active profile, recent commands, and snippets.
/// Tap to insert into compose, double-tap to send immediately.
class CommandMenuSheet extends ConsumerStatefulWidget {
  /// Called when a command is selected to insert into compose
  final void Function(String command) onInsert;

  /// Called when a command should be sent immediately
  final void Function(String command) onSendImmediately;

  const CommandMenuSheet({
    super.key,
    required this.onInsert,
    required this.onSendImmediately,
  });

  @override
  ConsumerState<CommandMenuSheet> createState() => _CommandMenuSheetState();

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
        initialChildSize: 0.5,
        minChildSize: 0.3,
        maxChildSize: 0.8,
        builder: (context, scrollController) {
          return CommandMenuSheet(
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
}

class _CommandMenuSheetState extends ConsumerState<CommandMenuSheet> {
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
    final state = ref.watch(actionBarProvider);
    final slashCommands = state.activeSlashCommands;
    final history = state.commandHistory;

    // Filter by search query
    final filteredCommands = _searchQuery.isEmpty
        ? slashCommands
        : slashCommands
            .where((c) =>
                c.label.toLowerCase().contains(_searchQuery.toLowerCase()) ||
                (c.description
                        ?.toLowerCase()
                        .contains(_searchQuery.toLowerCase()) ??
                    false))
            .toList();

    final filteredHistory = _searchQuery.isEmpty
        ? history.take(10).toList()
        : history
            .where(
                (h) => h.toLowerCase().contains(_searchQuery.toLowerCase()))
            .take(10)
            .toList();

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
          // Search field
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
            child: TextField(
              controller: _searchController,
              autofocus: true,
              onChanged: (v) => setState(() => _searchQuery = v),
              decoration: InputDecoration(
                hintText: 'Search commands...',
                prefixIcon:
                    const Icon(Icons.search, size: 20),
                filled: true,
                fillColor: isDark
                    ? DesignColors.inputDark
                    : DesignColors.inputLight,
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
          const SizedBox(height: 8),
          // Content
          Expanded(
            child: ListView(
              padding: const EdgeInsets.symmetric(horizontal: 8),
              children: [
                // Agent commands section
                if (filteredCommands.isNotEmpty) ...[
                  _buildSectionHeader(
                    '${state.activeProfile.name} Commands',
                    isDark,
                  ),
                  ...filteredCommands
                      .map((c) => _buildCommandItem(c, isDark)),
                ],
                // Recent commands section
                if (filteredHistory.isNotEmpty) ...[
                  _buildSectionHeader('Recent', isDark),
                  ...filteredHistory
                      .map((h) => _buildHistoryItem(h, isDark)),
                ],
                // Empty state
                if (filteredCommands.isEmpty && filteredHistory.isEmpty)
                  Padding(
                    padding: const EdgeInsets.all(32),
                    child: Center(
                      child: Text(
                        _searchQuery.isEmpty
                            ? 'No commands available'
                            : 'No matching commands',
                        style: TextStyle(
                          color: isDark
                              ? DesignColors.textMuted
                              : DesignColors.textMutedLight,
                        ),
                      ),
                    ),
                  ),
              ],
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildSectionHeader(String title, bool isDark) {
    return Padding(
      padding: const EdgeInsets.only(left: 12, top: 12, bottom: 4),
      child: Text(
        title.toUpperCase(),
        style: TextStyle(
          fontSize: 11,
          fontWeight: FontWeight.w600,
          letterSpacing: 0.8,
          color: isDark
              ? DesignColors.textMuted
              : DesignColors.textMutedLight,
        ),
      ),
    );
  }

  Widget _buildCommandItem(CommandMenuItem item, bool isDark) {
    return InkWell(
      onTap: () {
        HapticFeedback.selectionClick();
        widget.onInsert(item.command);
      },
      onDoubleTap: () {
        HapticFeedback.lightImpact();
        widget.onSendImmediately(item.command);
      },
      borderRadius: BorderRadius.circular(8),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        child: Row(
          children: [
            Text(
              item.label,
              style: TextStyle(
                fontSize: 14,
                fontWeight: FontWeight.w500,
                color: DesignColors.primary,
              ),
            ),
            if (item.description != null) ...[
              const SizedBox(width: 12),
              Expanded(
                child: Text(
                  item.description!,
                  style: TextStyle(
                    fontSize: 13,
                    color: isDark
                        ? DesignColors.textSecondary
                        : DesignColors.textSecondaryLight,
                  ),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }

  Widget _buildHistoryItem(String command, bool isDark) {
    return InkWell(
      onTap: () {
        HapticFeedback.selectionClick();
        widget.onInsert(command);
      },
      onDoubleTap: () {
        HapticFeedback.lightImpact();
        widget.onSendImmediately(command);
      },
      borderRadius: BorderRadius.circular(8),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        child: Text(
          command,
          style: TextStyle(
            fontSize: 14,
            color: isDark
                ? DesignColors.textPrimary
                : DesignColors.textPrimaryLight,
          ),
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
        ),
      ),
    );
  }
}
