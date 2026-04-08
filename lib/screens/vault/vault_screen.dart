import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import 'package:flutter_gen/gen_l10n/app_localizations.dart';

import '../../providers/snippet_provider.dart';
import '../../theme/design_colors.dart';
import '../keys/key_generate_screen.dart';
import '../keys/key_import_screen.dart';
import '../keys/keys_screen.dart';
import 'snippets_screen.dart';

/// Vault画面（Keys + Snippets のタブ切り替え）
///
/// Termiusのように、鍵とスニペットを「Vault」カテゴリにまとめる。
class VaultScreen extends ConsumerStatefulWidget {
  const VaultScreen({super.key});

  @override
  ConsumerState<VaultScreen> createState() => _VaultScreenState();
}

class _VaultScreenState extends ConsumerState<VaultScreen>
    with SingleTickerProviderStateMixin {
  late final TabController _tabController;

  @override
  void initState() {
    super.initState();
    _tabController = TabController(length: 2, vsync: this);
  }

  @override
  void dispose() {
    _tabController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final colorScheme = Theme.of(context).colorScheme;

    return Scaffold(
      body: NestedScrollView(
        headerSliverBuilder: (context, innerBoxIsScrolled) => [
          SliverAppBar(
            floating: true,
            pinned: true,
            expandedHeight: 100,
            backgroundColor: colorScheme.surface.withValues(alpha: 0.95),
            surfaceTintColor: Colors.transparent,
            flexibleSpace: FlexibleSpaceBar(
              titlePadding: const EdgeInsets.only(left: 24, bottom: 50),
              title: Text(
                'Vault',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 24,
                  fontWeight: FontWeight.w700,
                  color: colorScheme.onSurface,
                  letterSpacing: -0.5,
                ),
              ),
            ),
            bottom: TabBar(
              controller: _tabController,
              indicatorColor: DesignColors.primary,
              labelColor: DesignColors.primary,
              unselectedLabelColor:
                  isDark ? DesignColors.textMuted : DesignColors.textMutedLight,
              labelStyle: GoogleFonts.spaceGrotesk(
                fontSize: 13,
                fontWeight: FontWeight.w600,
              ),
              tabs: [
                Tab(icon: const Icon(Icons.key, size: 18), text: AppLocalizations.of(context)!.tabKeys),
                Tab(icon: const Icon(Icons.content_paste, size: 18), text: AppLocalizations.of(context)!.tabSnippets),
              ],
            ),
          ),
        ],
        body: TabBarView(
          controller: _tabController,
          children: const [
            KeysScreenBody(),
            SnippetsScreen(),
          ],
        ),
      ),
      floatingActionButton: ListenableBuilder(
        listenable: _tabController,
        builder: (context, _) {
          return FloatingActionButton(
            heroTag: 'fab_vault',
            onPressed: () => _onFabPressed(context),
            elevation: 0,
            backgroundColor: colorScheme.primary,
            foregroundColor: colorScheme.onPrimary,
            child: const Icon(Icons.add),
          );
        },
      ),
    );
  }

  void _onFabPressed(BuildContext context) {
    if (_tabController.index == 0) {
      _showAddKeyOptions(context);
    } else {
      _showAddSnippetDialog(context);
    }
  }

  void _showAddKeyOptions(BuildContext context) {
    showModalBottomSheet(
      context: context,
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              leading: const Icon(Icons.auto_fix_high),
              title: Text(AppLocalizations.of(context)!.generateNewKey),
              subtitle: Text(AppLocalizations.of(context)!.generateNewKeyDesc),
              onTap: () {
                Navigator.pop(ctx);
                Navigator.of(context).push(
                  MaterialPageRoute(builder: (_) => const KeyGenerateScreen()),
                );
              },
            ),
            ListTile(
              leading: const Icon(Icons.file_upload),
              title: Text(AppLocalizations.of(context)!.importKey),
              subtitle: Text(AppLocalizations.of(context)!.importKeyDesc),
              onTap: () {
                Navigator.pop(ctx);
                Navigator.of(context).push(
                  MaterialPageRoute(builder: (_) => const KeyImportScreen()),
                );
              },
            ),
          ],
        ),
      ),
    );
  }

  void _showAddSnippetDialog(BuildContext context) {
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
