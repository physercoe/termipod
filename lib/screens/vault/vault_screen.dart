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

/// Vaults screen — vertical list of Keys, Snippets, and History sections.
///
/// Each section is collapsible via tap on the section header.
class VaultScreen extends ConsumerStatefulWidget {
  const VaultScreen({super.key});

  @override
  ConsumerState<VaultScreen> createState() => _VaultScreenState();
}

class _VaultScreenState extends ConsumerState<VaultScreen> {
  bool _keysExpanded = true;
  bool _snippetsExpanded = true;
  bool _historyExpanded = true;

  @override
  Widget build(BuildContext context) {
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
                  expanded: _keysExpanded,
                  onToggle: () => setState(() => _keysExpanded = !_keysExpanded),
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
                if (_keysExpanded) ...[
                  const SizedBox(height: 8),
                  const KeysScreenBody(),
                ],

                const SizedBox(height: 24),

                // Snippets section
                _SectionCard(
                  icon: Icons.content_paste,
                  title: AppLocalizations.of(context)!.tabSnippets,
                  expanded: _snippetsExpanded,
                  onToggle: () => setState(() => _snippetsExpanded = !_snippetsExpanded),
                  trailing: _SmallActionButton(
                    icon: Icons.add,
                    label: AppLocalizations.of(context)!.buttonCreate,
                    onTap: () => _showAddSnippetDialog(context, ref),
                  ),
                ),
                if (_snippetsExpanded) ...[
                  const SizedBox(height: 8),
                  const SnippetsScreen(),
                ],

                const SizedBox(height: 24),

                // History section
                _SectionCard(
                  icon: Icons.history,
                  title: AppLocalizations.of(context)!.history,
                  expanded: _historyExpanded,
                  onToggle: () => setState(() => _historyExpanded = !_historyExpanded),
                  trailing: _SmallActionButton(
                    icon: Icons.delete_outline,
                    label: AppLocalizations.of(context)!.clear,
                    onTap: () => _confirmClearHistory(context, ref),
                  ),
                ),
                if (_historyExpanded) ...[
                  const SizedBox(height: 8),
                  const HistoryScreen(),
                ],
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
        title: Text(AppLocalizations.of(context)!.clearHistory),
        content: Text(AppLocalizations.of(context)!.clearHistoryContent),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: Text(AppLocalizations.of(context)!.buttonCancel),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: Colors.red),
            onPressed: () {
              ref.read(historyProvider.notifier).clear();
              Navigator.pop(ctx);
            },
            child: Text(AppLocalizations.of(context)!.clear),
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

/// Section header card with icon, title, expand/collapse toggle, and trailing action buttons.
class _SectionCard extends StatelessWidget {
  final IconData icon;
  final String title;
  final Widget? trailing;
  final bool expanded;
  final VoidCallback? onToggle;

  const _SectionCard({
    required this.icon,
    required this.title,
    this.trailing,
    this.expanded = true,
    this.onToggle,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return InkWell(
      onTap: onToggle,
      borderRadius: BorderRadius.circular(8),
      child: Row(
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
          const SizedBox(width: 4),
          AnimatedRotation(
            turns: expanded ? 0.0 : -0.25,
            duration: const Duration(milliseconds: 200),
            child: Icon(
              Icons.expand_more,
              size: 20,
              color: isDark ? DesignColors.textMuted : DesignColors.textMutedLight,
            ),
          ),
          const Spacer(),
          if (trailing != null) trailing!,
        ],
      ),
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
