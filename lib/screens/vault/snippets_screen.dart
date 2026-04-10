import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import 'package:flutter_muxpod/l10n/app_localizations.dart';

import '../../models/snippet_presets.dart';
import '../../providers/snippet_provider.dart';
import '../../theme/design_colors.dart';

/// スニペット一覧・管理画面
///
/// Shows preset Agent CLI snippets (grouped by profile) first, then user
/// snippets grouped by category. Presets can be edited in place — the
/// edits are stored as overrides via
/// [SnippetsNotifier.savePresetOverride] so the original built-in
/// definition is preserved and can be restored via swipe-to-reset.
///
/// As of 0.9.8 the screen has a top search box and each section is
/// collapsible — preset groups and user categories default to expanded,
/// but a search query auto-expands matching sections so results are
/// visible without manual unfolding.
class SnippetsScreen extends ConsumerStatefulWidget {
  const SnippetsScreen({super.key});

  @override
  ConsumerState<SnippetsScreen> createState() => _SnippetsScreenState();
}

class _SnippetsScreenState extends ConsumerState<SnippetsScreen> {
  final _searchController = TextEditingController();
  String _searchQuery = '';

  /// Section keys that are currently collapsed. Keys use the format
  /// `preset:<profileId>` or `user:<category>` so the two axes don't
  /// collide. Session-local state — no persistence.
  final Set<String> _collapsed = <String>{};

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  bool _matchesQuery(Snippet s) {
    if (_searchQuery.isEmpty) return true;
    final q = _searchQuery.toLowerCase();
    return s.name.toLowerCase().contains(q) ||
        s.content.toLowerCase().contains(q);
  }

  bool _isCollapsed(String sectionKey) {
    // Active search auto-expands matching sections so results are
    // visible without manual unfolding.
    if (_searchQuery.isNotEmpty) return false;
    return _collapsed.contains(sectionKey);
  }

  void _toggleCollapsed(String sectionKey) {
    HapticFeedback.selectionClick();
    setState(() {
      if (_collapsed.contains(sectionKey)) {
        _collapsed.remove(sectionKey);
      } else {
        _collapsed.add(sectionKey);
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final snippetsState = ref.watch(snippetsProvider);
    final userSnippets = snippetsState.snippets;
    final isDark = Theme.of(context).brightness == Brightness.dark;

    if (snippetsState.isLoading) {
      return const Center(child: CircularProgressIndicator());
    }

    // Preset snippets grouped by profile, filtered through user
    // overrides + deletion list so the vault view matches what the
    // snippet picker shows. Then filtered by the active search query.
    // Empty groups (fully deleted or filtered out) are hidden.
    final presetGroups = <String, List<Snippet>>{};
    for (final profileId in SnippetPresets.profileIds) {
      final raw = SnippetPresets.forProfile(profileId);
      final visible = raw
          .where((p) => !snippetsState.deletedPresetIds.contains(p.id))
          .map((p) => snippetsState.presetOverrides[p.id] ?? p)
          .where(_matchesQuery)
          .toList();
      if (visible.isNotEmpty) presetGroups[profileId] = visible;
    }

    // Group user snippets by category + search filter
    final categories = <String, List<Snippet>>{};
    for (final s in userSnippets) {
      if (!_matchesQuery(s)) continue;
      categories.putIfAbsent(s.category, () => []).add(s);
    }

    final hasAnySnippets =
        SnippetPresets.profileIds.any((pid) => SnippetPresets
                .forProfile(pid)
                .any((p) => !snippetsState.deletedPresetIds.contains(p.id))) ||
            userSnippets.isNotEmpty;

    if (!hasAnySnippets) {
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 24),
        child: Center(
          child: Text(
            AppLocalizations.of(context)!.noSnippetsYet,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 14,
              color: isDark ? DesignColors.textMuted : DesignColors.textMutedLight,
            ),
          ),
        ),
      );
    }

    final noResults = presetGroups.isEmpty && categories.isEmpty;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        _buildSearchField(isDark),
        if (noResults)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 24),
            child: Center(
              child: Text(
                AppLocalizations.of(context)!.noMatchingSnippets,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 14,
                  color: isDark
                      ? DesignColors.textMuted
                      : DesignColors.textMutedLight,
                ),
              ),
            ),
          )
        else ...[
          for (final entry in presetGroups.entries) ...[
            _buildSectionHeader(
              context,
              _profileLabel(entry.key),
              isDark,
              sectionKey: 'preset:${entry.key}',
              count: entry.value.length,
              badge: 'PRESET',
            ),
            if (!_isCollapsed('preset:${entry.key}'))
              for (final preset in entry.value)
                _PresetSnippetTile(preset: preset),
          ],
          for (final entry in categories.entries) ...[
            _buildSectionHeader(
              context,
              _categoryLabel(context, entry.key),
              isDark,
              sectionKey: 'user:${entry.key}',
              count: entry.value.length,
            ),
            if (!_isCollapsed('user:${entry.key}'))
              for (final snippet in entry.value)
                _SnippetTile(snippet: snippet),
          ],
        ],
      ],
    );
  }

  Widget _buildSearchField(bool isDark) {
    return Padding(
      padding: const EdgeInsets.only(top: 4, bottom: 8),
      child: TextField(
        controller: _searchController,
        onChanged: (v) => setState(() => _searchQuery = v.trim()),
        style: GoogleFonts.spaceGrotesk(fontSize: 14),
        decoration: InputDecoration(
          hintText: AppLocalizations.of(context)!.searchSnippets,
          hintStyle: GoogleFonts.spaceGrotesk(
            fontSize: 14,
            color: isDark
                ? DesignColors.textMuted
                : DesignColors.textMutedLight,
          ),
          prefixIcon: Icon(
            Icons.search,
            size: 18,
            color: isDark
                ? DesignColors.textMuted
                : DesignColors.textMutedLight,
          ),
          suffixIcon: _searchQuery.isNotEmpty
              ? IconButton(
                  icon: const Icon(Icons.close, size: 18),
                  onPressed: () {
                    _searchController.clear();
                    setState(() => _searchQuery = '');
                  },
                )
              : null,
          isDense: true,
          contentPadding:
              const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
          filled: true,
          fillColor:
              isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
          border: OutlineInputBorder(
            borderRadius: BorderRadius.circular(10),
            borderSide: BorderSide(
              color: isDark
                  ? DesignColors.borderDark
                  : DesignColors.borderLight,
            ),
          ),
          enabledBorder: OutlineInputBorder(
            borderRadius: BorderRadius.circular(10),
            borderSide: BorderSide(
              color: isDark
                  ? DesignColors.borderDark
                  : DesignColors.borderLight,
            ),
          ),
        ),
      ),
    );
  }

  Widget _buildSectionHeader(
    BuildContext context,
    String label,
    bool isDark, {
    required String sectionKey,
    required int count,
    String? badge,
  }) {
    final collapsed = _isCollapsed(sectionKey);
    // Auto-expanded during search — chevron still reflects the stored
    // collapsed state so the user's preference persists when the query
    // is cleared.
    final storedCollapsed = _collapsed.contains(sectionKey);
    return InkWell(
      onTap: () => _toggleCollapsed(sectionKey),
      borderRadius: BorderRadius.circular(6),
      child: Padding(
        padding: const EdgeInsets.only(top: 12, bottom: 8),
        child: Row(
          children: [
            Icon(
              storedCollapsed
                  ? Icons.chevron_right
                  : Icons.keyboard_arrow_down,
              size: 18,
              color: isDark
                  ? DesignColors.textMuted
                  : DesignColors.textMutedLight,
            ),
            const SizedBox(width: 4),
            Text(
              label,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 12,
                fontWeight: FontWeight.w600,
                color: isDark
                    ? DesignColors.textMuted
                    : DesignColors.textMutedLight,
                letterSpacing: 1.0,
              ),
            ),
            if (badge != null) ...[
              const SizedBox(width: 8),
              Container(
                padding:
                    const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                decoration: BoxDecoration(
                  color: DesignColors.primary.withValues(alpha: 0.15),
                  borderRadius: BorderRadius.circular(4),
                ),
                child: Text(
                  badge,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 9,
                    fontWeight: FontWeight.w700,
                    color: DesignColors.primary,
                    letterSpacing: 0.8,
                  ),
                ),
              ),
            ],
            const SizedBox(width: 8),
            Text(
              '$count',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                fontWeight: FontWeight.w500,
                color: isDark
                    ? DesignColors.textMuted.withValues(alpha: 0.6)
                    : DesignColors.textMutedLight.withValues(alpha: 0.6),
              ),
            ),
            // When folded, show the previewed count used to hint at
            // content behind the collapse without having to unfold.
            if (collapsed && _searchQuery.isEmpty) ...[
              const Spacer(),
              Text(
                '…',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 14,
                  color: isDark
                      ? DesignColors.textMuted
                      : DesignColors.textMutedLight,
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }

  String _profileLabel(String profileId) {
    // Reuse the preset model's category label, which already handles the
    // well-known profile IDs ('claude-code', 'codex', …). Uppercase to
    // match the user-category section style.
    return SnippetPresets.categoryLabel(profileId).toUpperCase();
  }

  String _categoryLabel(BuildContext context, String category) {
    final l10n = AppLocalizations.of(context)!;
    return switch (category) {
      'general' => l10n.categoryGeneral.toUpperCase(),
      'tmux' => l10n.categoryTmux.toUpperCase(),
      'cli-agent' => l10n.categoryCliAgent.toUpperCase(),
      'claude-code' => l10n.categoryClaude.toUpperCase(),
      'codex' => l10n.categoryCodex.toUpperCase(),
      _ => category.toUpperCase(),
    };
  }

}

/// スニペットタイル
class _SnippetTile extends ConsumerWidget {
  final Snippet snippet;

  const _SnippetTile({required this.snippet});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final colorScheme = Theme.of(context).colorScheme;

    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Dismissible(
        key: Key(snippet.id),
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
        confirmDismiss: (_) async {
          return await showDialog<bool>(
            context: context,
            builder: (ctx) => AlertDialog(
              title: Text(AppLocalizations.of(context)!.deleteSnippetTitle),
              content: Text('Delete "${snippet.name}"?'),
              actions: [
                TextButton(
                  onPressed: () => Navigator.pop(ctx, false),
                  child: Text(AppLocalizations.of(context)!.buttonCancel),
                ),
                TextButton(
                  onPressed: () => Navigator.pop(ctx, true),
                  style: TextButton.styleFrom(foregroundColor: DesignColors.error),
                  child: Text(AppLocalizations.of(context)!.buttonDelete),
                ),
              ],
            ),
          ) ?? false;
        },
        onDismissed: (_) {
          ref.read(snippetsProvider.notifier).deleteSnippet(snippet.id);
        },
        child: InkWell(
          onTap: () => _showEditDialog(context, ref),
          onLongPress: () {
            Clipboard.setData(ClipboardData(text: snippet.content));
            ScaffoldMessenger.of(context).showSnackBar(
              SnackBar(content: Text(AppLocalizations.of(context)!.copiedToClipboard), duration: const Duration(seconds: 1)),
            );
          },
          borderRadius: BorderRadius.circular(12),
          child: Container(
            padding: const EdgeInsets.all(14),
            decoration: BoxDecoration(
              color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
              borderRadius: BorderRadius.circular(12),
              border: Border.all(
                color: isDark ? DesignColors.borderDark : DesignColors.borderLight,
              ),
            ),
            child: Row(
              children: [
                Icon(
                  Icons.code,
                  size: 20,
                  color: DesignColors.primary.withValues(alpha: 0.7),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        snippet.name,
                        style: GoogleFonts.spaceGrotesk(
                          fontSize: 15,
                          fontWeight: FontWeight.w600,
                          color: colorScheme.onSurface,
                        ),
                      ),
                      const SizedBox(height: 4),
                      Text(
                        snippet.content,
                        style: GoogleFonts.jetBrainsMono(
                          fontSize: 12,
                          color: isDark ? DesignColors.textMuted : DesignColors.textMutedLight,
                        ),
                        maxLines: 2,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ],
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  void _showEditDialog(BuildContext context, WidgetRef ref) {
    showDialog(
      context: context,
      builder: (ctx) => _SnippetEditDialog(
        snippet: snippet,
        onSave: (name, content, category, variables) {
          ref.read(snippetsProvider.notifier).updateSnippet(
                snippet.id,
                name: name,
                content: content,
                category: category,
                variables: variables,
              );
        },
      ),
    );
  }
}

/// Save callback for [SnippetEditDialog]. Variables are derived from
/// `{{name}}` placeholders in `content` and optionally annotated with
/// kind/default/options via the editor section; the callback delivers
/// them so call sites can persist through [SnippetsNotifier].
typedef SnippetEditSaveCallback = void Function(
  String name,
  String content,
  String category,
  List<SnippetVariable> variables,
);

/// スニペット作成/編集ダイアログ
class SnippetEditDialog extends ConsumerStatefulWidget {
  final Snippet? snippet;

  /// Optional initial name prefill for new-snippet creation (ignored if
  /// [snippet] is non-null). Lets callers like the history "save as
  /// snippet" flow seed the name field without fabricating a Snippet.
  final String? initialName;

  /// Optional initial content prefill for new-snippet creation (ignored
  /// if [snippet] is non-null).
  final String? initialContent;

  final SnippetEditSaveCallback onSave;

  const SnippetEditDialog({
    super.key,
    this.snippet,
    this.initialName,
    this.initialContent,
    required this.onSave,
  });

  @override
  ConsumerState<SnippetEditDialog> createState() => _SnippetEditDialogState();
}

/// Bundles the text controllers that live alongside an editable
/// variable: the "default value" field and the "add option" input.
class _VarControllers {
  final TextEditingController defaultCtl;
  final TextEditingController newOptionCtl;
  _VarControllers({required this.defaultCtl, required this.newOptionCtl});
  void dispose() {
    defaultCtl.dispose();
    newOptionCtl.dispose();
  }
}

class _SnippetEditDialogState extends ConsumerState<SnippetEditDialog> {
  late final TextEditingController _nameController;
  late final TextEditingController _contentController;
  late String _category;

  /// Editable variables kept in sync with the placeholders in
  /// _contentController. Changes to content auto-add/remove entries
  /// while preserving per-variable kind/options across edits.
  List<SnippetVariable> _variables = [];

  /// Controllers for each variable's default/add-option fields, keyed
  /// by variable name. Rebuilt whenever _variables changes so the list
  /// of fields stays consistent with detected placeholders.
  final Map<String, _VarControllers> _varControllers = {};

  static final _placeholderPattern = RegExp(r'\{\{(\w+)\}\}');

  /// Sentinel value for the dropdown item that launches the
  /// "new category" prompt. Picked so it can never collide with a
  /// legitimate user-chosen category (whitespace-only).
  static const _newCategorySentinel = '__new__';

  /// Categories the built-in UI always offers even on a fresh install.
  /// Profile IDs (claude-code, codex…) are intentionally excluded —
  /// preset snippets live in their own tab keyed by profile, and
  /// letting users create overlapping user categories under the same
  /// name is confusing in the picker.
  static const _defaultCategories = ['general', 'tmux', 'notes'];

  String _categoryDisplayName(AppLocalizations l10n, String key) {
    return switch (key) {
      'general' => l10n.categoryGeneral,
      'tmux' => l10n.categoryTmux,
      'cli-agent' => l10n.categoryCliAgent,
      'claude-code' => l10n.categoryClaude,
      'codex' => l10n.categoryCodex,
      _ => key,
    };
  }

  /// Build the dropdown's category list from defaults + whatever
  /// categories existing user snippets already use, sorted A-Z, with
  /// the current snippet's category guaranteed to appear even if it
  /// isn't in either set (e.g. a legacy preset id). Profile IDs are
  /// filtered out so the dropdown can't steer users into creating
  /// overlapping user categories under preset names.
  List<String> _availableCategories(List<Snippet> userSnippets) {
    final reserved = SnippetPresets.profileIds;
    final set = <String>{
      ..._defaultCategories,
      for (final s in userSnippets)
        if (!reserved.contains(s.category)) s.category,
    };
    // Always surface the currently-selected category even if it was
    // legacy (e.g. 'cli-agent') or reserved — hiding it would silently
    // reset the user's choice on first open of the edit dialog.
    if (_category.isNotEmpty) set.add(_category);
    final list = set.toList()..sort();
    return list;
  }

  Future<void> _promptNewCategory() async {
    final l10n = AppLocalizations.of(context)!;
    final controller = TextEditingController();
    String? errorText;
    final result = await showDialog<String>(
      context: context,
      builder: (dialogContext) {
        return StatefulBuilder(
          builder: (ctx, setLocal) {
            return AlertDialog(
              title: Text(l10n.newCategoryDialogTitle),
              content: TextField(
                controller: controller,
                autofocus: true,
                textCapitalization: TextCapitalization.none,
                decoration: InputDecoration(
                  labelText: l10n.snippetCategoryLabel,
                  hintText: l10n.newCategoryHint,
                  errorText: errorText,
                  border: const OutlineInputBorder(),
                  isDense: true,
                ),
                onSubmitted: (_) {
                  final name = controller.text.trim();
                  final err = _validateCategoryName(name, l10n);
                  if (err != null) {
                    setLocal(() => errorText = err);
                    return;
                  }
                  Navigator.pop(dialogContext, name);
                },
              ),
              actions: [
                TextButton(
                  onPressed: () => Navigator.pop(dialogContext),
                  child: Text(l10n.buttonCancel),
                ),
                FilledButton(
                  onPressed: () {
                    final name = controller.text.trim();
                    final err = _validateCategoryName(name, l10n);
                    if (err != null) {
                      setLocal(() => errorText = err);
                      return;
                    }
                    Navigator.pop(dialogContext, name);
                  },
                  child: Text(l10n.buttonSave),
                ),
              ],
            );
          },
        );
      },
    );
    controller.dispose();
    if (result != null && result.isNotEmpty) {
      setState(() => _category = result);
    }
  }

  String? _validateCategoryName(String name, AppLocalizations l10n) {
    if (name.isEmpty) return l10n.categoryNameInvalid;
    // Reserve profile IDs so user categories can't shadow presets.
    if (SnippetPresets.profileIds.contains(name)) {
      return l10n.categoryNameInvalid;
    }
    return null;
  }

  @override
  void initState() {
    super.initState();
    _nameController = TextEditingController(
      text: widget.snippet?.name ?? widget.initialName ?? '',
    );
    _contentController = TextEditingController(
      text: widget.snippet?.content ?? widget.initialContent ?? '',
    );
    _category = widget.snippet?.category ?? 'general';
    _variables = [...?widget.snippet?.variables];
    _rebuildControllers();
    _contentController.addListener(_syncVariables);
    // Initial sync in case a new placeholder was added to content but
    // the snippet wasn't saved with matching variables metadata.
    WidgetsBinding.instance.addPostFrameCallback((_) => _syncVariables());
  }

  void _rebuildControllers() {
    final keep = _variables.map((v) => v.name).toSet();
    final stale =
        _varControllers.keys.where((k) => !keep.contains(k)).toList();
    for (final k in stale) {
      _varControllers.remove(k)?.dispose();
    }
    for (final v in _variables) {
      _varControllers.putIfAbsent(
        v.name,
        () => _VarControllers(
          defaultCtl: TextEditingController(text: v.defaultValue),
          newOptionCtl: TextEditingController(),
        ),
      );
    }
  }

  void _syncVariables() {
    final content = _contentController.text;
    final seen = <String>{};
    final found = <String>[];
    for (final m in _placeholderPattern.allMatches(content)) {
      final name = m.group(1)!;
      if (seen.add(name)) found.add(name);
    }
    final byName = {for (final v in _variables) v.name: v};
    final next = <SnippetVariable>[];
    for (final name in found) {
      next.add(byName[name] ?? SnippetVariable(name: name));
    }
    // Cheap identity check: if every entry is the same instance as
    // before, nothing meaningful changed and we skip the rebuild.
    final unchanged = next.length == _variables.length &&
        List.generate(
          next.length,
          (i) => identical(next[i], _variables[i]),
        ).every((b) => b);
    if (unchanged) return;
    setState(() {
      _variables = next;
      _rebuildControllers();
    });
  }

  void _updateVariable(int index, SnippetVariable updated) {
    setState(() {
      final list = [..._variables];
      list[index] = updated;
      _variables = list;
    });
  }

  void _addOption(int index) {
    final variable = _variables[index];
    final ctl = _varControllers[variable.name]?.newOptionCtl;
    if (ctl == null) return;
    final value = ctl.text.trim();
    if (value.isEmpty || variable.options.contains(value)) return;
    _updateVariable(
      index,
      SnippetVariable(
        name: variable.name,
        kind: SnippetVarKind.option,
        defaultValue:
            variable.defaultValue.isEmpty ? value : variable.defaultValue,
        options: [...variable.options, value],
        optional: variable.optional,
      ),
    );
    ctl.clear();
  }

  void _removeOption(int index, int optionIndex) {
    final variable = _variables[index];
    final removed = variable.options[optionIndex];
    final newOpts = [...variable.options]..removeAt(optionIndex);
    final newDefault = variable.defaultValue == removed
        ? (newOpts.isEmpty ? '' : newOpts.first)
        : variable.defaultValue;
    _updateVariable(
      index,
      SnippetVariable(
        name: variable.name,
        kind: variable.kind,
        defaultValue: newDefault,
        options: newOpts,
        optional: variable.optional,
      ),
    );
    final defaultCtl = _varControllers[variable.name]?.defaultCtl;
    if (defaultCtl != null && defaultCtl.text != newDefault) {
      defaultCtl.text = newDefault;
    }
  }

  @override
  void dispose() {
    _nameController.dispose();
    _contentController.removeListener(_syncVariables);
    _contentController.dispose();
    for (final c in _varControllers.values) {
      c.dispose();
    }
    _varControllers.clear();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final isEdit = widget.snippet != null;
    final l10n = AppLocalizations.of(context)!;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final userSnippets = ref.watch(snippetsProvider).snippets;
    final categories = _availableCategories(userSnippets);
    return AlertDialog(
      title: Text(isEdit ? l10n.editSnippet : l10n.newSnippetTitle),
      content: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            TextField(
              controller: _nameController,
              decoration: InputDecoration(
                labelText: l10n.snippetNameLabel,
                hintText: l10n.snippetNameHint,
              ),
              autofocus: true,
            ),
            const SizedBox(height: 12),
            TextField(
              controller: _contentController,
              decoration: InputDecoration(
                labelText: l10n.snippetContentLabel,
                hintText: l10n.snippetContentHint,
              ),
              maxLines: 4,
              minLines: 2,
            ),
            const SizedBox(height: 12),
            DropdownButtonFormField<String>(
              initialValue: _category,
              decoration:
                  InputDecoration(labelText: l10n.snippetCategoryLabel),
              items: [
                for (final key in categories)
                  DropdownMenuItem(
                    value: key,
                    child: Text(_categoryDisplayName(l10n, key)),
                  ),
                DropdownMenuItem(
                  value: _newCategorySentinel,
                  child: Text(
                    l10n.newCategoryOption,
                    style: TextStyle(
                      color: DesignColors.primary,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
              ],
              onChanged: (v) {
                if (v == null) return;
                if (v == _newCategorySentinel) {
                  _promptNewCategory();
                  return;
                }
                setState(() => _category = v);
              },
            ),
            if (_variables.isNotEmpty) ...[
              const SizedBox(height: 16),
              Text(
                l10n.snippetVariablesLabel,
                style: TextStyle(
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                  color: isDark
                      ? DesignColors.textSecondary
                      : DesignColors.textSecondaryLight,
                  letterSpacing: 0.4,
                ),
              ),
              const SizedBox(height: 2),
              Text(
                l10n.snippetVariablesHint,
                style: TextStyle(
                  fontSize: 11,
                  color: isDark
                      ? DesignColors.textMuted
                      : DesignColors.textMutedLight,
                ),
              ),
              for (var i = 0; i < _variables.length; i++)
                _buildVariableTile(i, _variables[i], l10n, isDark),
            ],
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: Text(l10n.buttonCancel),
        ),
        FilledButton(
          onPressed: () {
            final name = _nameController.text.trim();
            final content = _contentController.text.trim();
            if (name.isEmpty || content.isEmpty) return;
            widget.onSave(name, content, _category, List.of(_variables));
            Navigator.pop(context);
          },
          child: Text(l10n.buttonSave),
        ),
      ],
    );
  }

  Widget _buildVariableTile(
    int index,
    SnippetVariable variable,
    AppLocalizations l10n,
    bool isDark,
  ) {
    final ctls = _varControllers[variable.name];
    if (ctls == null) return const SizedBox.shrink();
    return Container(
      margin: const EdgeInsets.only(top: 10),
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: (isDark
                ? DesignColors.keyBackground
                : DesignColors.keyBackgroundLight)
            .withValues(alpha: 0.5),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(
          color: (isDark
                  ? DesignColors.borderDark
                  : DesignColors.borderLight)
              .withValues(alpha: 0.6),
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.center,
            children: [
              Expanded(
                child: Text(
                  '{{${variable.name}}}',
                  style: TextStyle(
                    fontFamily: 'monospace',
                    fontSize: 13,
                    fontWeight: FontWeight.w600,
                    color: DesignColors.primary,
                  ),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              SizedBox(
                height: 32,
                child: SegmentedButton<SnippetVarKind>(
                  style: const ButtonStyle(
                    visualDensity: VisualDensity.compact,
                    tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                  ),
                  showSelectedIcon: false,
                  segments: [
                    ButtonSegment(
                      value: SnippetVarKind.text,
                      label: Text(
                        l10n.variableKindText,
                        style: const TextStyle(fontSize: 11),
                      ),
                    ),
                    ButtonSegment(
                      value: SnippetVarKind.option,
                      label: Text(
                        l10n.variableKindOption,
                        style: const TextStyle(fontSize: 11),
                      ),
                    ),
                  ],
                  selected: {variable.kind},
                  onSelectionChanged: (sel) {
                    _updateVariable(
                      index,
                      SnippetVariable(
                        name: variable.name,
                        kind: sel.first,
                        defaultValue: variable.defaultValue,
                        options: variable.options,
                        optional: variable.optional,
                      ),
                    );
                  },
                ),
              ),
            ],
          ),
          if (variable.kind == SnippetVarKind.option) ...[
            const SizedBox(height: 8),
            if (variable.options.isNotEmpty)
              Wrap(
                spacing: 6,
                runSpacing: 6,
                children: [
                  for (var i = 0; i < variable.options.length; i++)
                    InputChip(
                      label: Text(
                        variable.options[i],
                        style: const TextStyle(fontSize: 12),
                      ),
                      visualDensity: VisualDensity.compact,
                      onDeleted: () => _removeOption(index, i),
                      deleteIconColor: isDark
                          ? DesignColors.textMuted
                          : DesignColors.textMutedLight,
                    ),
                ],
              ),
            const SizedBox(height: 6),
            Row(
              children: [
                Expanded(
                  child: SizedBox(
                    height: 36,
                    child: TextField(
                      controller: ctls.newOptionCtl,
                      decoration: InputDecoration(
                        hintText: l10n.variableAddOptionHint,
                        isDense: true,
                        border: const OutlineInputBorder(),
                        contentPadding: const EdgeInsets.symmetric(
                          horizontal: 8,
                          vertical: 8,
                        ),
                      ),
                      style: const TextStyle(fontSize: 12),
                      onSubmitted: (_) => _addOption(index),
                    ),
                  ),
                ),
                const SizedBox(width: 4),
                IconButton(
                  icon: const Icon(Icons.add, size: 20),
                  tooltip: l10n.variableAddOptionTooltip,
                  visualDensity: VisualDensity.compact,
                  onPressed: () => _addOption(index),
                ),
              ],
            ),
          ],
          const SizedBox(height: 8),
          SizedBox(
            height: 36,
            child: TextField(
              controller: ctls.defaultCtl,
              decoration: InputDecoration(
                labelText: l10n.variableDefaultLabel,
                isDense: true,
                border: const OutlineInputBorder(),
                contentPadding: const EdgeInsets.symmetric(
                  horizontal: 8,
                  vertical: 8,
                ),
              ),
              style: const TextStyle(fontSize: 12),
              onChanged: (v) {
                _updateVariable(
                  index,
                  SnippetVariable(
                    name: variable.name,
                    kind: variable.kind,
                    defaultValue: v,
                    options: variable.options,
                    optional: variable.optional,
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

/// Private version used by _SnippetTile
class _SnippetEditDialog extends SnippetEditDialog {
  const _SnippetEditDialog({
    required super.snippet,
    required super.onSave,
  });
}

/// Tile for a built-in preset snippet (Agent CLI slash commands).
///
/// Reuses the [SnippetEditDialog] flow but persists edits as overrides
/// via [SnippetsNotifier.savePresetOverride] so the original built-in
/// definition is preserved. Swipe-end-to-start deletes (adds to
/// deletedPresetIds) and shows a restore chip in the trailing area
/// when the preset has a user override, tappable to revert back to the
/// built-in default.
class _PresetSnippetTile extends ConsumerWidget {
  final Snippet preset;

  const _PresetSnippetTile({required this.preset});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final colorScheme = Theme.of(context).colorScheme;
    final hasOverride =
        ref.watch(snippetsProvider).presetOverrides.containsKey(preset.id);

    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Dismissible(
        key: Key('preset-${preset.id}'),
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
        confirmDismiss: (_) async {
          return await showDialog<bool>(
                context: context,
                builder: (ctx) => AlertDialog(
                  title: Text(
                      AppLocalizations.of(context)!.deleteSnippetTitle),
                  content: Text('Delete "${preset.name}"?'),
                  actions: [
                    TextButton(
                      onPressed: () => Navigator.pop(ctx, false),
                      child:
                          Text(AppLocalizations.of(context)!.buttonCancel),
                    ),
                    TextButton(
                      onPressed: () => Navigator.pop(ctx, true),
                      style: TextButton.styleFrom(
                          foregroundColor: DesignColors.error),
                      child:
                          Text(AppLocalizations.of(context)!.buttonDelete),
                    ),
                  ],
                ),
              ) ??
              false;
        },
        onDismissed: (_) {
          ref.read(snippetsProvider.notifier).deletePreset(preset.id);
        },
        child: InkWell(
          onTap: () => _showEditDialog(context, ref),
          onLongPress: () {
            Clipboard.setData(ClipboardData(text: preset.content));
            ScaffoldMessenger.of(context).showSnackBar(
              SnackBar(
                content: Text(
                    AppLocalizations.of(context)!.copiedToClipboard),
                duration: const Duration(seconds: 1),
              ),
            );
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
                color: hasOverride
                    ? DesignColors.primary.withValues(alpha: 0.45)
                    : (isDark
                        ? DesignColors.borderDark
                        : DesignColors.borderLight),
              ),
            ),
            child: Row(
              children: [
                Icon(
                  Icons.auto_awesome,
                  size: 20,
                  color: DesignColors.primary.withValues(alpha: 0.7),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Row(
                        children: [
                          Flexible(
                            child: Text(
                              preset.name,
                              style: GoogleFonts.spaceGrotesk(
                                fontSize: 15,
                                fontWeight: FontWeight.w600,
                                color: colorScheme.onSurface,
                              ),
                              overflow: TextOverflow.ellipsis,
                            ),
                          ),
                          if (hasOverride) ...[
                            const SizedBox(width: 6),
                            Container(
                              padding: const EdgeInsets.symmetric(
                                horizontal: 5,
                                vertical: 1,
                              ),
                              decoration: BoxDecoration(
                                color: DesignColors.primary
                                    .withValues(alpha: 0.15),
                                borderRadius: BorderRadius.circular(4),
                              ),
                              child: Text(
                                'EDITED',
                                style: GoogleFonts.spaceGrotesk(
                                  fontSize: 8,
                                  fontWeight: FontWeight.w700,
                                  color: DesignColors.primary,
                                  letterSpacing: 0.6,
                                ),
                              ),
                            ),
                          ],
                        ],
                      ),
                      const SizedBox(height: 4),
                      Text(
                        preset.content,
                        style: GoogleFonts.jetBrainsMono(
                          fontSize: 12,
                          color: isDark
                              ? DesignColors.textMuted
                              : DesignColors.textMutedLight,
                        ),
                        maxLines: 2,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ],
                  ),
                ),
                if (hasOverride)
                  IconButton(
                    icon: const Icon(Icons.restore, size: 20),
                    tooltip: AppLocalizations.of(context)!.resetToDefault,
                    color: DesignColors.primary,
                    visualDensity: VisualDensity.compact,
                    onPressed: () {
                      ref
                          .read(snippetsProvider.notifier)
                          .revertPresetOverride(preset.id);
                    },
                  ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  void _showEditDialog(BuildContext context, WidgetRef ref) {
    showDialog(
      context: context,
      builder: (ctx) => _SnippetEditDialog(
        snippet: preset,
        onSave: (name, content, category, variables) {
          final edited = preset.copyWith(
            name: name,
            content: content,
            category: category,
            variables: variables,
          );
          ref
              .read(snippetsProvider.notifier)
              .savePresetOverride(preset.id, edited);
        },
      ),
    );
  }
}
