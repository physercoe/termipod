import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../providers/action_bar_provider.dart';
import '../../theme/design_colors.dart';

/// History bottom sheet showing recent commands.
///
/// Tap to insert into compose, double-tap to send immediately.
class HistorySheet extends ConsumerWidget {
  final void Function(String command) onInsert;
  final void Function(String command) onSendImmediately;

  const HistorySheet({
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
          return HistorySheet(
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
    final state = ref.watch(actionBarProvider);
    final history = state.commandHistory;

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
                Text(
                  'History',
                  style: TextStyle(
                    fontSize: 16,
                    fontWeight: FontWeight.w600,
                    color: isDark
                        ? DesignColors.textPrimary
                        : DesignColors.textPrimaryLight,
                  ),
                ),
                const Spacer(),
                if (history.isNotEmpty)
                  TextButton(
                    onPressed: () {
                      ref.read(actionBarProvider.notifier).clearHistory();
                    },
                    style: TextButton.styleFrom(
                      foregroundColor: Colors.red,
                      padding: const EdgeInsets.symmetric(horizontal: 8),
                    ),
                    child: const Text('Clear'),
                  ),
              ],
            ),
          ),
          const SizedBox(height: 4),
          // History list
          Expanded(
            child: history.isEmpty
                ? Center(
                    child: Text(
                      'No history yet',
                      style: TextStyle(
                        color: isDark
                            ? DesignColors.textMuted
                            : DesignColors.textMutedLight,
                      ),
                    ),
                  )
                : ListView.builder(
                    padding: const EdgeInsets.symmetric(horizontal: 8),
                    itemCount: history.length,
                    itemBuilder: (context, index) {
                      final cmd = history[index];
                      return InkWell(
                        onTap: () {
                          HapticFeedback.selectionClick();
                          onInsert(cmd);
                        },
                        onDoubleTap: () {
                          HapticFeedback.lightImpact();
                          onSendImmediately(cmd);
                        },
                        borderRadius: BorderRadius.circular(8),
                        child: Padding(
                          padding: const EdgeInsets.symmetric(
                              horizontal: 12, vertical: 10),
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
                      );
                    },
                  ),
          ),
        ],
      ),
    );
  }
}
