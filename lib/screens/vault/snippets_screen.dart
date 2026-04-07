import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/snippet_provider.dart';
import '../../theme/design_colors.dart';

/// スニペット一覧・管理画面
class SnippetsScreen extends ConsumerWidget {
  const SnippetsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final snippetsState = ref.watch(snippetsProvider);
    final snippets = snippetsState.snippets;
    final isDark = Theme.of(context).brightness == Brightness.dark;

    if (snippetsState.isLoading) {
      return const Center(child: CircularProgressIndicator());
    }

    if (snippets.isEmpty) {
      return _buildEmptyView(context);
    }

    // カテゴリでグループ化
    final categories = <String, List<Snippet>>{};
    for (final s in snippets) {
      categories.putIfAbsent(s.category, () => []).add(s);
    }

    return ListView(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 100),
      children: [
        for (final entry in categories.entries) ...[
          Padding(
            padding: const EdgeInsets.only(top: 12, bottom: 8),
            child: Text(
              _categoryLabel(entry.key),
              style: GoogleFonts.spaceGrotesk(
                fontSize: 12,
                fontWeight: FontWeight.w600,
                color: isDark ? DesignColors.textMuted : DesignColors.textMutedLight,
                letterSpacing: 1.0,
              ),
            ),
          ),
          for (final snippet in entry.value)
            _SnippetTile(snippet: snippet),
        ],
      ],
    );
  }

  String _categoryLabel(String category) {
    return switch (category) {
      'general' => 'GENERAL',
      'tmux' => 'TMUX',
      'cli-agent' => 'CLI AGENT',
      _ => category.toUpperCase(),
    };
  }

  Widget _buildEmptyView(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Container(
            padding: const EdgeInsets.all(24),
            decoration: BoxDecoration(
              color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
              borderRadius: BorderRadius.circular(20),
              border: Border.all(
                color: isDark ? DesignColors.borderDark : DesignColors.borderLight,
              ),
            ),
            child: Icon(
              Icons.content_paste,
              size: 64,
              color: isDark ? DesignColors.textMuted : DesignColors.textMutedLight,
            ),
          ),
          const SizedBox(height: 24),
          Text(
            'No Snippets',
            style: GoogleFonts.spaceGrotesk(
              fontSize: 20,
              fontWeight: FontWeight.w600,
              color: isDark ? DesignColors.textSecondary : DesignColors.textSecondaryLight,
            ),
          ),
          const SizedBox(height: 8),
          Text(
            'Add reusable commands and text snippets',
            style: GoogleFonts.spaceGrotesk(
              fontSize: 14,
              color: isDark ? DesignColors.textMuted : DesignColors.textMutedLight,
            ),
          ),
        ],
      ),
    );
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
              title: const Text('Delete Snippet?'),
              content: Text('Delete "${snippet.name}"?'),
              actions: [
                TextButton(
                  onPressed: () => Navigator.pop(ctx, false),
                  child: const Text('Cancel'),
                ),
                TextButton(
                  onPressed: () => Navigator.pop(ctx, true),
                  style: TextButton.styleFrom(foregroundColor: DesignColors.error),
                  child: const Text('Delete'),
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
              const SnackBar(content: Text('Copied to clipboard'), duration: Duration(seconds: 1)),
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

  static const _categories = [
    ('general', 'General'),
    ('tmux', 'Tmux'),
    ('cli-agent', 'CLI Agent'),
  ];

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
    return AlertDialog(
      title: Text(isEdit ? 'Edit Snippet' : 'New Snippet'),
      content: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            TextField(
              controller: _nameController,
              decoration: const InputDecoration(
                labelText: 'Name',
                hintText: 'e.g. Git status',
              ),
              autofocus: true,
            ),
            const SizedBox(height: 12),
            TextField(
              controller: _contentController,
              decoration: const InputDecoration(
                labelText: 'Content',
                hintText: 'e.g. git status',
              ),
              maxLines: 4,
              minLines: 2,
            ),
            const SizedBox(height: 12),
            DropdownButtonFormField<String>(
              value: _category,
              decoration: const InputDecoration(labelText: 'Category'),
              items: [
                for (final c in _categories)
                  DropdownMenuItem(value: c.$1, child: Text(c.$2)),
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
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: () {
            final name = _nameController.text.trim();
            final content = _contentController.text.trim();
            if (name.isEmpty || content.isEmpty) return;
            widget.onSave(name, content, _category);
            Navigator.pop(context);
          },
          child: const Text('Save'),
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
