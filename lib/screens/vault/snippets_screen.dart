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
class SnippetsScreen extends ConsumerWidget {
  const SnippetsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final snippetsState = ref.watch(snippetsProvider);
    final userSnippets = snippetsState.snippets;
    final isDark = Theme.of(context).brightness == Brightness.dark;

    if (snippetsState.isLoading) {
      return const Center(child: CircularProgressIndicator());
    }

    // Preset snippets grouped by profile, filtered through user
    // overrides + deletion list so the vault view matches what the
    // snippet picker shows. Empty groups (fully deleted) are hidden.
    final presetGroups = <String, List<Snippet>>{};
    for (final profileId in SnippetPresets.profileIds) {
      final raw = SnippetPresets.forProfile(profileId);
      final visible = raw
          .where((p) => !snippetsState.deletedPresetIds.contains(p.id))
          .map((p) => snippetsState.presetOverrides[p.id] ?? p)
          .toList();
      if (visible.isNotEmpty) presetGroups[profileId] = visible;
    }

    // Group user snippets by category
    final categories = <String, List<Snippet>>{};
    for (final s in userSnippets) {
      categories.putIfAbsent(s.category, () => []).add(s);
    }

    if (presetGroups.isEmpty && userSnippets.isEmpty) {
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 24),
        child: Center(
          child: Text(
            'No snippets yet',
            style: GoogleFonts.spaceGrotesk(
              fontSize: 14,
              color: isDark ? DesignColors.textMuted : DesignColors.textMutedLight,
            ),
          ),
        ),
      );
    }

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        for (final entry in presetGroups.entries) ...[
          _buildSectionHeader(
            context,
            _profileLabel(entry.key),
            isDark,
            badge: 'PRESET',
          ),
          for (final preset in entry.value)
            _PresetSnippetTile(preset: preset),
        ],
        for (final entry in categories.entries) ...[
          _buildSectionHeader(
            context,
            _categoryLabel(context, entry.key),
            isDark,
          ),
          for (final snippet in entry.value)
            _SnippetTile(snippet: snippet),
        ],
      ],
    );
  }

  Widget _buildSectionHeader(
    BuildContext context,
    String label,
    bool isDark, {
    String? badge,
  }) {
    return Padding(
      padding: const EdgeInsets.only(top: 12, bottom: 8),
      child: Row(
        children: [
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
              padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
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
        ],
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
        onSave: (name, content, category) {
          ref.read(snippetsProvider.notifier).updateSnippet(
                snippet.id,
                name: name,
                content: content,
                category: category,
              );
        },
      ),
    );
  }
}

/// スニペット作成/編集ダイアログ
class SnippetEditDialog extends StatefulWidget {
  final Snippet? snippet;
  final void Function(String name, String content, String category) onSave;

  const SnippetEditDialog({
    super.key,
    this.snippet,
    required this.onSave,
  });

  @override
  State<SnippetEditDialog> createState() => _SnippetEditDialogState();
}

class _SnippetEditDialogState extends State<SnippetEditDialog> {
  late final TextEditingController _nameController;
  late final TextEditingController _contentController;
  late String _category;

  static const _categoryKeys = [
    'general',
    'tmux',
    'cli-agent',
    'claude-code',
    'codex',
  ];

  static String _categoryDisplayName(AppLocalizations l10n, String key) {
    return switch (key) {
      'general' => l10n.categoryGeneral,
      'tmux' => l10n.categoryTmux,
      'cli-agent' => l10n.categoryCliAgent,
      'claude-code' => l10n.categoryClaude,
      'codex' => l10n.categoryCodex,
      _ => key,
    };
  }

  @override
  void initState() {
    super.initState();
    _nameController = TextEditingController(text: widget.snippet?.name ?? '');
    _contentController = TextEditingController(text: widget.snippet?.content ?? '');
    _category = widget.snippet?.category ?? 'general';
  }

  @override
  void dispose() {
    _nameController.dispose();
    _contentController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final isEdit = widget.snippet != null;
    final l10n = AppLocalizations.of(context)!;
    return AlertDialog(
      title: Text(isEdit ? l10n.editSnippet : l10n.newSnippetTitle),
      content: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
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
              value: _category,
              decoration: InputDecoration(labelText: l10n.snippetCategoryLabel),
              items: [
                for (final key in _categoryKeys)
                  DropdownMenuItem(
                    value: key,
                    child: Text(_categoryDisplayName(l10n, key)),
                  ),
              ],
              onChanged: (v) {
                if (v != null) setState(() => _category = v);
              },
            ),
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
            widget.onSave(name, content, _category);
            Navigator.pop(context);
          },
          child: Text(l10n.buttonSave),
        ),
      ],
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
        onSave: (name, content, category) {
          final edited = preset.copyWith(
            name: name,
            content: content,
            category: category,
          );
          ref
              .read(snippetsProvider.notifier)
              .savePresetOverride(preset.id, edited);
        },
      ),
    );
  }
}
