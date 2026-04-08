import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import 'package:flutter_muxpod/l10n/app_localizations.dart';

import '../../providers/history_provider.dart';
import '../../providers/snippet_provider.dart';
import '../../theme/design_colors.dart';
import '../keys/key_generate_screen.dart';
import '../keys/key_import_screen.dart';
import '../keys/keys_screen.dart';
import 'history_screen.dart';
import 'snippets_screen.dart';

/// Vaults screen — vertical list of Keys and Snippets sections.
///
/// Uses a single scrollable column instead of horizontal tabs,
/// since the vault may contain more item types in the future and
/// horizontal space is limited on mobile.
class VaultScreen extends ConsumerWidget {
  const VaultScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colorScheme = Theme.of(context).colorScheme;

    return Scaffold(
      body: CustomScrollView(
        slivers: [
          SliverAppBar(
            floating: true,
            pinned: true,
            expandedHeight: 100,
            backgroundColor: colorScheme.surface.withValues(alpha: 0.95),
            surfaceTintColor: Colors.transparent,
            flexibleSpace: FlexibleSpaceBar(
              titlePadding: const EdgeInsets.only(left: 24, bottom: 16),
              title: Text(
                AppLocalizations.of(context)!.tabVault,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 24,
                  fontWeight: FontWeight.w700,
                  color: colorScheme.onSurface,
                  letterSpacing: -0.5,
                ),
              ),
            ),
          ),
          SliverPadding(
            padding: const EdgeInsets.fromLTRB(16, 16, 16, 100),
            sliver: SliverList(
              delegate: SliverChildListDelegate([
                // Keys section
                _SectionCard(
                  icon: Icons.key,
                  title: AppLocalizations.of(context)!.tabKeys,
                  trailing: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      _SmallActionButton(
                        icon: Icons.auto_fix_high,
                        label: AppLocalizations.of(context)!.buttonGenerate,
                        onTap: () => Navigator.of(context).push(
                          MaterialPageRoute(builder: (_) => const KeyGenerateScreen()),
                        ),
                      ),
                      const SizedBox(width: 8),
                      _SmallActionButton(
                        icon: Icons.file_upload,
                        label: AppLocalizations.of(context)!.buttonImport,
                        onTap: () => Navigator.of(context).push(
                          MaterialPageRoute(builder: (_) => const KeyImportScreen()),
                        ),
                      ),
                    ],
                  ),
                ),
                const SizedBox(height: 8),
                const KeysScreenBody(),

                const SizedBox(height: 24),

                // Snippets section
                _SectionCard(
                  icon: Icons.content_paste,
                  title: AppLocalizations.of(context)!.tabSnippets,
                  trailing: _SmallActionButton(
                    icon: Icons.add,
                    label: AppLocalizations.of(context)!.buttonCreate,
                    onTap: () => _showAddSnippetDialog(context, ref),
                  ),
                ),
                const SizedBox(height: 8),
                const SnippetsScreen(),

                const SizedBox(height: 24),

                // History section
                _SectionCard(
                  icon: Icons.history,
                  title: 'History',
                  trailing: _SmallActionButton(
                    icon: Icons.delete_outline,
                    label: 'Clear',
                    onTap: () => _confirmClearHistory(context, ref),
                  ),
                ),
                const SizedBox(height: 8),
                const HistoryScreen(),
              ]),
            ),
          ),
        ],
      ),
    );
  }

  void _confirmClearHistory(BuildContext context, WidgetRef ref) {
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Clear History'),
        content: const Text('Delete all command history? This cannot be undone.'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('Cancel'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: Colors.red),
            onPressed: () {
              ref.read(historyProvider.notifier).clear();
              Navigator.pop(ctx);
            },
            child: const Text('Clear'),
          ),
        ],
      ),
    );
  }

  void _showAddSnippetDialog(BuildContext context, WidgetRef ref) {
    showDialog(
      context: context,
      builder: (ctx) => SnippetEditDialog(
        onSave: (name, content, category) {
          ref.read(snippetsProvider.notifier).addSnippet(
                name: name,
                content: content,
                category: category,
              );
        },
      ),
    );
  }
}

/// Section header card with icon, title, and trailing action buttons.
class _SectionCard extends StatelessWidget {
  final IconData icon;
  final String title;
  final Widget? trailing;

  const _SectionCard({
    required this.icon,
    required this.title,
    this.trailing,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Row(
      children: [
        Icon(
          icon,
          size: 20,
          color: DesignColors.primary,
        ),
        const SizedBox(width: 8),
        Text(
          title,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 16,
            fontWeight: FontWeight.w700,
            color: isDark ? DesignColors.textPrimary : DesignColors.textPrimaryLight,
          ),
        ),
        const Spacer(),
        if (trailing != null) trailing!,
      ],
    );
  }
}

/// Small action button used in section headers.
class _SmallActionButton extends StatelessWidget {
  final IconData icon;
  final String label;
  final VoidCallback onTap;

  const _SmallActionButton({
    required this.icon,
    required this.label,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(8),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
        decoration: BoxDecoration(
          color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
          borderRadius: BorderRadius.circular(8),
          border: Border.all(
            color: isDark ? DesignColors.borderDark : DesignColors.borderLight,
          ),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 14, color: DesignColors.primary),
            const SizedBox(width: 4),
            Text(
              label,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 12,
                fontWeight: FontWeight.w500,
                color: DesignColors.primary,
              ),
            ),
          ],
        ),
      ),
    );
  }
}
