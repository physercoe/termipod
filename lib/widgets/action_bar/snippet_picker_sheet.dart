import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'package:flutter_muxpod/l10n/app_localizations.dart';

import '../../models/snippet_presets.dart';
import '../../providers/action_bar_provider.dart';
import '../../providers/snippet_provider.dart';
import '../../theme/design_colors.dart';

/// Unified snippet picker bottom sheet.
///
/// Shows preset agent commands (for active profile) and user snippets,
/// grouped by category with search. Tap to insert, double-tap to send.
class SnippetPickerSheet extends ConsumerStatefulWidget {
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
        initialChildSize: 0.5,
        minChildSize: 0.3,
        maxChildSize: 0.8,
        expand: false,
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
  ConsumerState<SnippetPickerSheet> createState() =>
      _SnippetPickerSheetState();
}

class _SnippetPickerSheetState extends ConsumerState<SnippetPickerSheet> {
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
    final snippetsState = ref.watch(snippetsProvider);
    final userSnippets = snippetsState.snippets;
    final activeProfileId = ref.watch(actionBarProvider).activeProfileId;
    final presetSnippets = SnippetPresets.forProfile(activeProfileId);

    // Filter by search
    final filteredPresets = _filter(presetSnippets);
    final filteredUser = _filter(userSnippets);

    // Group user snippets by category
    final userByCategory = <String, List<Snippet>>{};
    for (final s in filteredUser) {
      userByCategory.putIfAbsent(s.category, () => []).add(s);
    }

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
                Icon(Icons.bolt, size: 20, color: DesignColors.primary),
                const SizedBox(width: 6),
                Text(
                  AppLocalizations.of(context)!.snippets,
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
                    _showAddSnippetDialog(context, ref);
                  },
                  icon: const Icon(Icons.add, size: 18),
                  label: Text(AppLocalizations.of(context)!.newLabel),
                  style: TextButton.styleFrom(
                    foregroundColor: DesignColors.primary,
                    padding: const EdgeInsets.symmetric(horizontal: 8),
                  ),
                ),
              ],
            ),
          ),
          // Search field
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
            child: TextField(
              controller: _searchController,
              autofocus: false,
              onChanged: (v) => setState(() => _searchQuery = v),
              decoration: InputDecoration(
                hintText: AppLocalizations.of(context)!.searchSnippets,
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
          const SizedBox(height: 4),
          // Content
          Expanded(
            child: (filteredPresets.isEmpty && filteredUser.isEmpty)
                ? _buildEmptyState(isDark)
                : ListView(
                    padding: const EdgeInsets.symmetric(horizontal: 8),
                    children: [
                      // Preset agent commands section
                      if (filteredPresets.isNotEmpty) ...[
                        _buildSectionHeader(
                          SnippetPresets.categoryLabel(activeProfileId),
                          isDark,
                        ),
                        ...filteredPresets
                            .map((s) => _buildSnippetItem(s, isDark,
                                isPreset: true)),
                      ],
                      // User snippet sections by category
                      for (final entry in userByCategory.entries) ...[
                        _buildSectionHeader(
                          SnippetPresets.categoryLabel(entry.key),
                          isDark,
                        ),
                        ...entry.value
                            .map((s) => _buildSnippetItem(s, isDark)),
                      ],
                    ],
                  ),
          ),
        ],
      ),
    );
  }

  List<Snippet> _filter(List<Snippet> items) {
    if (_searchQuery.isEmpty) return items;
    final q = _searchQuery.toLowerCase();
    return items
        .where((s) =>
            s.name.toLowerCase().contains(q) ||
            s.content.toLowerCase().contains(q))
        .toList();
  }

  Widget _buildEmptyState(bool isDark) {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(
            Icons.bolt_outlined,
            size: 48,
            color:
                isDark ? DesignColors.textMuted : DesignColors.textMutedLight,
          ),
          const SizedBox(height: 8),
          Text(
            _searchQuery.isEmpty ? AppLocalizations.of(context)!.noSnippetsYet : AppLocalizations.of(context)!.noMatchingSnippets,
            style: TextStyle(
              color: isDark
                  ? DesignColors.textMuted
                  : DesignColors.textMutedLight,
            ),
          ),
          if (_searchQuery.isEmpty) ...[
            const SizedBox(height: 4),
            Text(
              AppLocalizations.of(context)!.createOneHint,
              style: TextStyle(
                fontSize: 12,
                color: isDark
                    ? DesignColors.textMuted
                    : DesignColors.textMutedLight,
              ),
            ),
          ],
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
          color:
              isDark ? DesignColors.textMuted : DesignColors.textMutedLight,
        ),
      ),
    );
  }

  Widget _buildSnippetItem(Snippet snippet, bool isDark,
      {bool isPreset = false}) {
    return InkWell(
      onTap: () {
        HapticFeedback.selectionClick();
        if (snippet.variables.isNotEmpty) {
          _showVariableDialog(context, snippet);
        } else if (snippet.sendImmediately) {
          widget.onSendImmediately(snippet.content);
        } else {
          widget.onInsert(snippet.content);
        }
      },
      onDoubleTap: () {
        HapticFeedback.lightImpact();
        widget.onSendImmediately(snippet.content);
      },
      borderRadius: BorderRadius.circular(8),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        child: Row(
          children: [
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    snippet.name,
                    style: TextStyle(
                      fontSize: 14,
                      fontWeight: FontWeight.w500,
                      color: isPreset
                          ? DesignColors.primary
                          : (isDark
                              ? DesignColors.textPrimary
                              : DesignColors.textPrimaryLight),
                    ),
                  ),
                  if (snippet.content != snippet.name) ...[
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
                ],
              ),
            ),
            if (snippet.sendImmediately)
              Padding(
                padding: const EdgeInsets.only(left: 8),
                child: Icon(
                  Icons.send,
                  size: 14,
                  color: isDark
                      ? DesignColors.textMuted
                      : DesignColors.textMutedLight,
                ),
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
              child: Text(AppLocalizations.of(context)!.buttonCancel),
            ),
            FilledButton(
              onPressed: () {
                final values = <String, String>{};
                for (final entry in controllers.entries) {
                  values[entry.key] = entry.value.text;
                }
                final resolved = snippet.resolve(values);
                Navigator.pop(dialogContext);
                Navigator.pop(context);
                if (snippet.sendImmediately) {
                  widget.onSendImmediately(resolved);
                } else {
                  widget.onInsert(resolved);
                }
              },
              child: Text(AppLocalizations.of(context)!.insert),
            ),
          ],
        );
      },
    );
  }

  void _showAddSnippetDialog(BuildContext context, WidgetRef ref) {
    final nameController = TextEditingController();
    final contentController = TextEditingController();

    showDialog<void>(
      context: context,
      builder: (dialogContext) {
        return AlertDialog(
          title: Text(AppLocalizations.of(context)!.newSnippet),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              TextField(
                controller: nameController,
                decoration: InputDecoration(
                  labelText: AppLocalizations.of(context)!.nameLabel,
                  border: OutlineInputBorder(),
                  isDense: true,
                ),
                autofocus: true,
              ),
              const SizedBox(height: 12),
              TextField(
                controller: contentController,
                decoration: InputDecoration(
                  labelText: AppLocalizations.of(context)!.commandTextLabel,
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
              child: Text(AppLocalizations.of(context)!.buttonCancel),
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
              child: Text(AppLocalizations.of(context)!.buttonSave),
            ),
          ],
        );
      },
    );
  }
}
