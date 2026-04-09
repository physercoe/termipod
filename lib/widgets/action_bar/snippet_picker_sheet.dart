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
        initialChildSize: 0.7,
        minChildSize: 0.3,
        maxChildSize: 0.95,
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

  /// Active category tab. Values:
  /// - 'all' — show presets + all user categories (default)
  /// - 'presets' — show only preset snippets for the active profile
  /// - <category> — show only user snippets in that category
  String _activeTab = 'all';

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

    // Build category tab list. 'all' is always present. 'presets' appears
    // when preset snippets exist for the active profile. User categories
    // are added in sorted order.
    final categories = <String>['all'];
    if (presetSnippets.isNotEmpty) categories.add('presets');
    final userCategorySet = <String>{};
    for (final s in userSnippets) {
      userCategorySet.add(s.category);
    }
    final sortedUserCategories = userCategorySet.toList()..sort();
    categories.addAll(sortedUserCategories);

    // Clamp active tab if it no longer exists (e.g. category was deleted).
    if (!categories.contains(_activeTab)) {
      _activeTab = 'all';
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
          // Title row — bolt icon + inline search + [+] new button
          Padding(
            padding: const EdgeInsets.fromLTRB(12, 4, 8, 8),
            child: Row(
              children: [
                Icon(Icons.bolt, size: 20, color: DesignColors.primary),
                const SizedBox(width: 8),
                Expanded(
                  child: SizedBox(
                    height: 36,
                    child: TextField(
                      controller: _searchController,
                      autofocus: false,
                      onChanged: (v) => setState(() => _searchQuery = v),
                      decoration: InputDecoration(
                        hintText:
                            AppLocalizations.of(context)!.searchSnippets,
                        prefixIcon: const Icon(Icons.search, size: 18),
                        prefixIconConstraints: const BoxConstraints(
                          minWidth: 32,
                          minHeight: 32,
                        ),
                        filled: true,
                        fillColor: isDark
                            ? DesignColors.inputDark
                            : DesignColors.inputLight,
                        border: OutlineInputBorder(
                          borderRadius: BorderRadius.circular(18),
                          borderSide: BorderSide.none,
                        ),
                        contentPadding: const EdgeInsets.symmetric(
                          horizontal: 4,
                          vertical: 0,
                        ),
                        isDense: true,
                      ),
                      style: const TextStyle(fontSize: 14),
                    ),
                  ),
                ),
                const SizedBox(width: 4),
                IconButton(
                  onPressed: () {
                    Navigator.pop(context);
                    _showAddSnippetDialog(context, ref);
                  },
                  icon: const Icon(Icons.add_circle_outline, size: 22),
                  color: DesignColors.primary,
                  tooltip: AppLocalizations.of(context)!.newLabel,
                  visualDensity: VisualDensity.compact,
                ),
              ],
            ),
          ),
          // Category tabs — shown only when there are ≥2 categories to
          // switch between (['all'] alone is meaningless).
          if (categories.length >= 2)
            SizedBox(
              height: 36,
              child: ListView.builder(
                scrollDirection: Axis.horizontal,
                padding: const EdgeInsets.symmetric(horizontal: 12),
                itemCount: categories.length,
                itemBuilder: (context, i) {
                  final cat = categories[i];
                  final isActive = cat == _activeTab;
                  final label = switch (cat) {
                    'all' => AppLocalizations.of(context)!.tabAll,
                    'presets' =>
                      SnippetPresets.categoryLabel(activeProfileId),
                    _ => SnippetPresets.categoryLabel(cat),
                  };
                  return Padding(
                    padding: const EdgeInsets.only(right: 8),
                    child: InkWell(
                      onTap: () => setState(() => _activeTab = cat),
                      borderRadius: BorderRadius.circular(16),
                      child: Container(
                        padding: const EdgeInsets.symmetric(
                          horizontal: 12,
                          vertical: 6,
                        ),
                        decoration: BoxDecoration(
                          color: isActive
                              ? DesignColors.primary.withValues(alpha: 0.15)
                              : Colors.transparent,
                          border: Border.all(
                            color: isActive
                                ? DesignColors.primary
                                : (isDark
                                    ? DesignColors.borderDark
                                    : DesignColors.borderLight),
                            width: 1,
                          ),
                          borderRadius: BorderRadius.circular(16),
                        ),
                        child: Center(
                          child: Text(
                            label,
                            style: TextStyle(
                              fontSize: 12,
                              fontWeight: isActive
                                  ? FontWeight.w600
                                  : FontWeight.w400,
                              color: isActive
                                  ? DesignColors.primary
                                  : (isDark
                                      ? DesignColors.textSecondary
                                      : DesignColors
                                          .textSecondaryLight),
                            ),
                          ),
                        ),
                      ),
                    ),
                  );
                },
              ),
            ),
          const SizedBox(height: 4),
          // Content — filtered by active tab
          Expanded(
            child: _buildTabContent(
              activeProfileId: activeProfileId,
              filteredPresets: filteredPresets,
              userByCategory: userByCategory,
              isDark: isDark,
            ),
          ),
        ],
      ),
    );
  }

  /// Build the list content for the currently active category tab.
  Widget _buildTabContent({
    required String activeProfileId,
    required List<Snippet> filteredPresets,
    required Map<String, List<Snippet>> userByCategory,
    required bool isDark,
  }) {
    // Determine which sections to render based on the active tab.
    final sections =
        <({String? header, List<Snippet> snippets, bool isPreset})>[];

    if (_activeTab == 'all') {
      if (filteredPresets.isNotEmpty) {
        sections.add((
          header: SnippetPresets.categoryLabel(activeProfileId),
          snippets: filteredPresets,
          isPreset: true,
        ));
      }
      for (final entry in userByCategory.entries) {
        sections.add((
          header: SnippetPresets.categoryLabel(entry.key),
          snippets: entry.value,
          isPreset: false,
        ));
      }
    } else if (_activeTab == 'presets') {
      if (filteredPresets.isNotEmpty) {
        sections.add((
          header: null,
          snippets: filteredPresets,
          isPreset: true,
        ));
      }
    } else {
      final cat = userByCategory[_activeTab];
      if (cat != null && cat.isNotEmpty) {
        sections.add((header: null, snippets: cat, isPreset: false));
      }
    }

    if (sections.isEmpty) return _buildEmptyState(isDark);

    return ListView(
      padding: const EdgeInsets.symmetric(horizontal: 8),
      children: [
        for (final s in sections) ...[
          if (s.header != null) _buildSectionHeader(s.header!, isDark),
          ...s.snippets.map(
            (snip) => _buildSnippetItem(snip, isDark, isPreset: s.isPreset),
          ),
        ],
      ],
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
