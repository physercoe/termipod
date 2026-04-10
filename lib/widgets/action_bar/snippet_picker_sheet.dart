import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'package:flutter_muxpod/l10n/app_localizations.dart';

import '../../models/snippet_presets.dart';
import '../../providers/action_bar_provider.dart';
import '../../providers/history_provider.dart';
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

  /// Panel identifier so preset snippets match the profile that is
  /// active on *this pane*, not the global default. Mirrors
  /// [ActionBar.panelKey]. Null falls back to the global profile.
  final String? panelKey;

  const SnippetPickerSheet({
    super.key,
    required this.onInsert,
    required this.onSendImmediately,
    this.panelKey,
  });

  static Future<void> show(
    BuildContext context, {
    required WidgetRef ref,
    required void Function(String content) onInsert,
    required void Function(String content) onSendImmediately,
    String? panelKey,
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
            panelKey: panelKey,
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
  /// - 'presets' — preset snippets for the active profile
  /// - `<category>` — user snippets in that category
  /// - 'history' — recent commands sent to the terminal (always last tab)
  ///
  /// The previous 'all' aggregate view was removed so tabs map 1:1 to
  /// distinct categories instead of showing everything jumbled together.
  /// Default is set lazily in build() based on what categories exist.
  String? _activeTab;

  /// Currently highlighted enum option per snippet (keyed by snippet id).
  /// Tapping a chip only selects it; the user has to press the row's send
  /// button to actually send/insert. Values fall back to the variable's
  /// [SnippetVariable.defaultValue] when no selection has been made yet.
  final Map<String, String> _selectedOption = {};

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
    // Resolve the profile for this panel so presets match the action
    // bar the user is currently looking at. Falls back to the global
    // default when panelKey is null.
    final activeProfileId = ref.watch(
      actionBarProvider.select((s) => s.profileIdForPanel(widget.panelKey)),
    );
    // Apply user overrides + hide deleted preset IDs.
    final rawPresets = SnippetPresets.forProfile(activeProfileId);
    final presetSnippets = rawPresets
        .where((p) => !snippetsState.deletedPresetIds.contains(p.id))
        .map((p) => snippetsState.presetOverrides[p.id] ?? p)
        .toList();
    final history = ref.watch(historyProvider).items;

    // Filter by search
    final filteredPresets = _filter(presetSnippets);
    final filteredUser = _filter(userSnippets);
    final filteredHistory = _filterStrings(history);

    // Group user snippets by category
    final userByCategory = <String, List<Snippet>>{};
    for (final s in filteredUser) {
      userByCategory.putIfAbsent(s.category, () => []).add(s);
    }

    // Build category tab list:
    //   - 'presets' first (if the active profile has any presets)
    //   - user categories in sorted order (using raw names from ALL user
    //     snippets, not just filtered — so a search that empties one
    //     category doesn't make its tab disappear)
    //   - 'history' always last
    final categories = <String>[];
    if (presetSnippets.isNotEmpty) categories.add('presets');
    final userCategorySet = <String>{};
    for (final s in userSnippets) {
      userCategorySet.add(s.category);
    }
    final sortedUserCategories = userCategorySet.toList()..sort();
    categories.addAll(sortedUserCategories);
    categories.add('history');

    // Default the active tab the first time we build, and clamp to a
    // valid tab if the previously-active one disappeared.
    if (_activeTab == null || !categories.contains(_activeTab)) {
      _activeTab = categories.first;
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
                        hintText: AppLocalizations.of(context)!.searchHint,
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
                    'presets' =>
                      SnippetPresets.categoryLabel(activeProfileId),
                    'history' => AppLocalizations.of(context)!.history,
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
              filteredHistory: filteredHistory,
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
    required List<String> filteredHistory,
    required bool isDark,
  }) {
    // History tab — flat list of recent command strings, newest first.
    if (_activeTab == 'history') {
      if (filteredHistory.isEmpty) return _buildEmptyState(isDark);
      return ListView.builder(
        padding: const EdgeInsets.symmetric(horizontal: 8),
        itemCount: filteredHistory.length,
        itemBuilder: (context, i) =>
            _buildHistoryItem(filteredHistory[i], isDark),
      );
    }

    // Snippet tabs — presets or a single user category. The old "all"
    // aggregate view is gone, so we always render a single section.
    final snippets = _activeTab == 'presets'
        ? filteredPresets
        : (userByCategory[_activeTab] ?? const <Snippet>[]);
    final isPreset = _activeTab == 'presets';

    if (snippets.isEmpty) return _buildEmptyState(isDark);

    return ListView.builder(
      padding: const EdgeInsets.symmetric(horizontal: 8),
      itemCount: snippets.length,
      itemBuilder: (context, i) =>
          _buildSnippetItem(snippets[i], isDark, isPreset: isPreset),
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

  List<String> _filterStrings(List<String> items) {
    if (_searchQuery.isEmpty) return items;
    final q = _searchQuery.toLowerCase();
    return items.where((s) => s.toLowerCase().contains(q)).toList();
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

  /// Render one recent-command row for the History tab.
  ///
  /// Behavior mirrors the old RecentSheet: tap-insert, double-tap-send,
  /// long-press-save-as-snippet, swipe-end-to-start to delete from history.
  Widget _buildHistoryItem(String cmd, bool isDark) {
    return Dismissible(
      key: ValueKey('history-$cmd'),
      direction: DismissDirection.endToStart,
      onDismissed: (_) {
        ref.read(historyProvider.notifier).delete(cmd);
      },
      background: Container(
        alignment: Alignment.centerRight,
        padding: const EdgeInsets.only(right: 16),
        color: Colors.red.withValues(alpha: 0.2),
        child: const Icon(Icons.delete_outline, color: Colors.red, size: 20),
      ),
      child: InkWell(
        onTap: () {
          HapticFeedback.selectionClick();
          widget.onInsert(cmd);
        },
        onDoubleTap: () {
          HapticFeedback.lightImpact();
          widget.onSendImmediately(cmd);
        },
        onLongPress: () {
          HapticFeedback.mediumImpact();
          _showSaveHistoryAsSnippet(context, cmd);
        },
        borderRadius: BorderRadius.circular(8),
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
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
                    fontFamily: 'monospace',
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
  }

  void _showSaveHistoryAsSnippet(BuildContext context, String command) {
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
            border: const OutlineInputBorder(),
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
                    content:
                        Text(AppLocalizations.of(context)!.savedToSnippets),
                    duration: const Duration(seconds: 2),
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

  /// Returns the snippet's single option-kind variable when that's the
  /// *only* variable. In that case the picker renders inline pill
  /// buttons instead of opening a dialog — one tap = one keystroke
  /// for the most common slash-command shape `/cmd {enum}`.
  ///
  /// Snippets with multiple option vars or any text vars fall back to
  /// the dialog flow because their UX would be ambiguous in a row.
  SnippetVariable? _inlineOptionVar(Snippet snippet) {
    if (snippet.variables.length != 1) return null;
    final v = snippet.variables.first;
    if (v.kind != SnippetVarKind.option) return null;
    if (v.options.isEmpty) return null;
    return v;
  }

  /// Current highlighted option for [snippet]'s inline enum variable,
  /// falling back to the variable's default or first option.
  String _selectedFor(Snippet snippet, SnippetVariable variable) {
    final picked = _selectedOption[snippet.id];
    if (picked != null && variable.options.contains(picked)) return picked;
    if (variable.defaultValue.isNotEmpty) return variable.defaultValue;
    return variable.options.first;
  }

  /// Select an option for the inline enum variable without sending.
  /// Mirrors the "tap chip = highlight only" interaction the user asked
  /// for after the 0.9.5 release; the actual send happens when the row's
  /// send button (or the name itself) is pressed.
  void _selectInlineOption(String snippetId, String value) {
    HapticFeedback.selectionClick();
    setState(() => _selectedOption[snippetId] = value);
  }

  /// Resolve the snippet using the currently highlighted chip and
  /// send/insert per the snippet's policy.
  void _sendInlineOption(Snippet snippet, SnippetVariable variable) {
    HapticFeedback.lightImpact();
    final value = _selectedFor(snippet, variable);
    final resolved = snippet.resolve({variable.name: value});
    if (snippet.sendImmediately) {
      widget.onSendImmediately(resolved);
    } else {
      widget.onInsert(resolved);
    }
  }

  Widget _buildSnippetItem(Snippet snippet, bool isDark,
      {bool isPreset = false}) {
    final inlineVar = _inlineOptionVar(snippet);
    if (inlineVar != null) {
      return _buildInlineEnumItem(snippet, inlineVar, isDark,
          isPreset: isPreset);
    }
    return _buildPlainItem(snippet, isDark, isPreset: isPreset);
  }

  /// Snippet row with an inline enum picker. Layout:
  ///
  ///     [Name] [chip chip chip …scrollable→] [send]
  ///
  /// Tapping a chip only highlights it (see [_selectInlineOption]);
  /// the actual send/insert happens on the trailing send button or on
  /// a tap of the name itself.
  Widget _buildInlineEnumItem(
    Snippet snippet,
    SnippetVariable variable,
    bool isDark, {
    required bool isPreset,
  }) {
    final selected = _selectedFor(snippet, variable);
    final nameColor = isPreset
        ? DesignColors.primary
        : (isDark
            ? DesignColors.textPrimary
            : DesignColors.textPrimaryLight);
    return InkWell(
      onTap: () => _sendInlineOption(snippet, variable),
      onLongPress: () {
        HapticFeedback.mediumImpact();
        _showSnippetActions(snippet, isPreset: isPreset);
      },
      borderRadius: BorderRadius.circular(8),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.center,
          children: [
            ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 140),
              child: Text(
                snippet.name,
                style: TextStyle(
                  fontSize: 14,
                  fontWeight: FontWeight.w500,
                  color: nameColor,
                ),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: SingleChildScrollView(
                scrollDirection: Axis.horizontal,
                child: Row(
                  children: [
                    for (final opt in variable.options) ...[
                      _OptionChip(
                        label: opt,
                        isSelected: opt == selected,
                        isDark: isDark,
                        onTap: () => _selectInlineOption(snippet.id, opt),
                      ),
                      const SizedBox(width: 6),
                    ],
                  ],
                ),
              ),
            ),
            const SizedBox(width: 4),
            _SendIconButton(
              isDark: isDark,
              sendImmediately: snippet.sendImmediately,
              onTap: () => _sendInlineOption(snippet, variable),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildPlainItem(Snippet snippet, bool isDark,
      {required bool isPreset}) {
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
      onLongPress: () {
        HapticFeedback.mediumImpact();
        _showSnippetActions(snippet, isPreset: isPreset);
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
                      // Collapse newlines so multi-line snippets preview
                      // inline. Pairs with maxLines: 1 + ellipsis to keep
                      // the row compact — users who need the full content
                      // long-press to edit.
                      snippet.content.replaceAll('\n', ' ⏎ '),
                      style: TextStyle(
                        fontSize: 12,
                        color: isDark
                            ? DesignColors.textSecondary
                            : DesignColors.textSecondaryLight,
                        fontFamily: 'monospace',
                      ),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      softWrap: false,
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

  /// Long-press menu for a snippet row. Offers Edit and Delete; for
  /// preset snippets we use the override path so the original built-in
  /// definition stays intact and can be restored later.
  void _showSnippetActions(Snippet snippet, {required bool isPreset}) {
    showModalBottomSheet<void>(
      context: context,
      builder: (sheetContext) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              leading: const Icon(Icons.edit),
              title: Text(AppLocalizations.of(context)!.buttonEdit),
              onTap: () {
                Navigator.pop(sheetContext);
                _showEditSnippetDialog(snippet, isPreset: isPreset);
              },
            ),
            if (isPreset && ref
                .read(snippetsProvider)
                .presetOverrides
                .containsKey(snippet.id))
              ListTile(
                leading: const Icon(Icons.restore),
                title: Text(AppLocalizations.of(context)!.resetToDefault),
                onTap: () {
                  Navigator.pop(sheetContext);
                  ref
                      .read(snippetsProvider.notifier)
                      .revertPresetOverride(snippet.id);
                },
              ),
            ListTile(
              leading: const Icon(Icons.delete_outline, color: Colors.red),
              title: Text(
                AppLocalizations.of(context)!.buttonDelete,
                style: const TextStyle(color: Colors.red),
              ),
              onTap: () {
                Navigator.pop(sheetContext);
                if (isPreset) {
                  ref
                      .read(snippetsProvider.notifier)
                      .deletePreset(snippet.id);
                } else {
                  ref
                      .read(snippetsProvider.notifier)
                      .deleteSnippet(snippet.id);
                }
              },
            ),
          ],
        ),
      ),
    );
  }

  /// Edit dialog reused for preset overrides and user snippets.
  /// Editing a preset stores a [Snippet] under [SnippetsNotifier.savePresetOverride]
  /// keyed by the preset's id; editing a user snippet calls
  /// [SnippetsNotifier.updateSnippet]. Variables are preserved as-is —
  /// editing variable schemas is rare and would inflate this dialog
  /// past usefulness.
  void _showEditSnippetDialog(Snippet snippet, {required bool isPreset}) {
    final nameController = TextEditingController(text: snippet.name);
    final contentController = TextEditingController(text: snippet.content);
    bool sendImmediately = snippet.sendImmediately;

    showDialog<void>(
      context: context,
      builder: (dialogContext) => StatefulBuilder(
        builder: (ctx, setDialogState) => AlertDialog(
          title: Text(AppLocalizations.of(context)!.editSnippet),
          content: SizedBox(
            width: 320,
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextField(
                  controller: nameController,
                  decoration: InputDecoration(
                    labelText: AppLocalizations.of(context)!.nameLabel,
                    border: const OutlineInputBorder(),
                    isDense: true,
                  ),
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: contentController,
                  decoration: InputDecoration(
                    labelText:
                        AppLocalizations.of(context)!.commandTextLabel,
                    border: const OutlineInputBorder(),
                    isDense: true,
                  ),
                  maxLines: 3,
                ),
                const SizedBox(height: 8),
                SwitchListTile(
                  contentPadding: EdgeInsets.zero,
                  dense: true,
                  title: const Text(
                    'Send immediately',
                    style: TextStyle(fontSize: 13),
                  ),
                  value: sendImmediately,
                  onChanged: (v) =>
                      setDialogState(() => sendImmediately = v),
                ),
              ],
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(dialogContext),
              child: Text(AppLocalizations.of(context)!.buttonCancel),
            ),
            FilledButton(
              onPressed: () {
                if (nameController.text.isEmpty ||
                    contentController.text.isEmpty) {
                  return;
                }
                final edited = snippet.copyWith(
                  name: nameController.text,
                  content: contentController.text,
                  sendImmediately: sendImmediately,
                );
                if (isPreset) {
                  ref
                      .read(snippetsProvider.notifier)
                      .savePresetOverride(snippet.id, edited);
                } else {
                  ref.read(snippetsProvider.notifier).updateSnippet(
                        snippet.id,
                        name: edited.name,
                        content: edited.content,
                        sendImmediately: edited.sendImmediately,
                      );
                }
                Navigator.pop(dialogContext);
              },
              child: Text(AppLocalizations.of(context)!.buttonSave),
            ),
          ],
        ),
      ),
    );
  }

  void _showVariableDialog(BuildContext context, Snippet snippet) {
    // For text-kind vars we hold a TextEditingController.
    // For option-kind vars we hold the current selected value directly
    // and update it via StatefulBuilder.
    final textControllers = <String, TextEditingController>{};
    final optionValues = <String, String>{};
    for (final v in snippet.variables) {
      if (v.kind == SnippetVarKind.option) {
        optionValues[v.name] = v.defaultValue.isNotEmpty
            ? v.defaultValue
            : (v.options.isNotEmpty ? v.options.first : '');
      } else {
        textControllers[v.name] =
            TextEditingController(text: v.defaultValue);
      }
    }

    showDialog<void>(
      context: context,
      builder: (dialogContext) {
        return StatefulBuilder(
          builder: (ctx, setDialogState) {
            return AlertDialog(
              title: Text(snippet.name),
              content: SizedBox(
                width: 320,
                child: SingleChildScrollView(
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      for (final v in snippet.variables)
                        Padding(
                          padding: const EdgeInsets.only(bottom: 10),
                          child: v.kind == SnippetVarKind.option
                              ? DropdownButtonFormField<String>(
                                  initialValue: optionValues[v.name],
                                  isDense: true,
                                  decoration: InputDecoration(
                                    labelText: v.name,
                                    border: const OutlineInputBorder(),
                                    isDense: true,
                                  ),
                                  items: [
                                    for (final opt in v.options)
                                      DropdownMenuItem(
                                        value: opt,
                                        child: Text(opt),
                                      ),
                                  ],
                                  onChanged: (val) {
                                    if (val == null) return;
                                    setDialogState(() {
                                      optionValues[v.name] = val;
                                    });
                                  },
                                )
                              : TextField(
                                  controller: textControllers[v.name],
                                  decoration: InputDecoration(
                                    labelText: v.optional
                                        ? '${v.name} (optional)'
                                        : v.name,
                                    hintText: v.hint,
                                    border: const OutlineInputBorder(),
                                    isDense: true,
                                  ),
                                ),
                        ),
                    ],
                  ),
                ),
              ),
              actions: [
                TextButton(
                  onPressed: () => Navigator.pop(dialogContext),
                  child: Text(AppLocalizations.of(context)!.buttonCancel),
                ),
                FilledButton(
                  onPressed: () {
                    final values = <String, String>{};
                    for (final v in snippet.variables) {
                      values[v.name] = v.kind == SnippetVarKind.option
                          ? (optionValues[v.name] ?? '')
                          : (textControllers[v.name]?.text ?? '');
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

/// Compact pill button used to render an enum option inline in a snippet
/// row. The currently selected value gets a primary tint so the user can
/// see at a glance which choice the send button will resolve to. Tapping
/// a chip only updates [isSelected] — sending is a separate action.
class _OptionChip extends StatelessWidget {
  final String label;
  final bool isSelected;
  final bool isDark;
  final VoidCallback onTap;

  const _OptionChip({
    required this.label,
    required this.isSelected,
    required this.isDark,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final bg = isSelected
        ? DesignColors.primary.withValues(alpha: 0.22)
        : (isDark
            ? DesignColors.keyBackground
            : DesignColors.keyBackgroundLight);
    final border = isSelected
        ? DesignColors.primary
        : (isDark ? DesignColors.borderDark : DesignColors.borderLight);
    final fg = isSelected
        ? DesignColors.primary
        : (isDark
            ? DesignColors.textPrimary
            : DesignColors.textPrimaryLight);

    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(14),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
          decoration: BoxDecoration(
            color: bg,
            borderRadius: BorderRadius.circular(14),
            border: Border.all(
              color: border,
              width: isSelected ? 1.5 : 1,
            ),
          ),
          child: Text(
            label,
            style: TextStyle(
              fontSize: 12,
              fontWeight: isSelected ? FontWeight.w700 : FontWeight.w500,
              color: fg,
              fontFamily: 'monospace',
            ),
          ),
        ),
      ),
    );
  }
}

/// Trailing send button for rows with an inline enum picker. Uses a
/// filled send icon when the snippet is configured to auto-send so the
/// affordance matches the existing send indicator used on plain rows.
class _SendIconButton extends StatelessWidget {
  final bool isDark;
  final bool sendImmediately;
  final VoidCallback onTap;

  const _SendIconButton({
    required this.isDark,
    required this.sendImmediately,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final accent = DesignColors.primary;
    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(18),
        child: Container(
          width: 36,
          height: 36,
          alignment: Alignment.center,
          decoration: BoxDecoration(
            color: accent.withValues(alpha: 0.15),
            shape: BoxShape.circle,
            border: Border.all(
              color: accent.withValues(alpha: 0.6),
              width: 1,
            ),
          ),
          child: Icon(
            sendImmediately ? Icons.send : Icons.keyboard_return,
            size: 16,
            color: accent,
          ),
        ),
      ),
    );
  }
}
