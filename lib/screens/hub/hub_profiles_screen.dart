import 'package:flutter/material.dart';
import 'package:termipod/l10n/app_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../services/hub/hub_profiles.dart';
import '../../theme/design_colors.dart';
import 'hub_bootstrap_screen.dart';

/// List view for managing saved hub connection profiles.
///
/// Reached from the team-switcher menu's "Manage profiles…" entry.
/// Shows every saved profile with rename/edit/delete affordances; the
/// active profile is marked with a check. Tap to switch active.
class HubProfilesScreen extends ConsumerWidget {
  const HubProfilesScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context)!;
    final hub = ref.watch(hubProvider).value;
    final profiles = hub?.profiles ?? const <HubProfile>[];
    final activeId = hub?.activeProfileId;

    return Scaffold(
      appBar: AppBar(
        title: Text(
          l10n.hubProfilesTitle,
          style: GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700),
        ),
        actions: [
          IconButton(
            tooltip: l10n.addProfile,
            icon: const Icon(Icons.add),
            onPressed: () => Navigator.of(context).push(MaterialPageRoute(
              builder: (_) => const HubBootstrapScreen(addNew: true),
            )),
          ),
        ],
      ),
      body: profiles.isEmpty
          ? const _EmptyState()
          : ListView.separated(
              padding: const EdgeInsets.symmetric(vertical: 8),
              itemCount: profiles.length,
              separatorBuilder: (_, __) => const Divider(height: 1),
              itemBuilder: (ctx, i) => _ProfileTile(
                profile: profiles[i],
                isActive: profiles[i].id == activeId,
                ref: ref,
              ),
            ),
    );
  }
}

class _ProfileTile extends StatelessWidget {
  final HubProfile profile;
  final bool isActive;
  final WidgetRef ref;

  const _ProfileTile({
    required this.profile,
    required this.isActive,
    required this.ref,
  });

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final scheme = Theme.of(context).colorScheme;
    return ListTile(
      leading: Icon(
        isActive ? Icons.check_circle : Icons.circle_outlined,
        color: isActive ? scheme.primary : scheme.onSurfaceVariant,
      ),
      title: Text(
        profile.name,
        style: GoogleFonts.spaceGrotesk(
          fontWeight: isActive ? FontWeight.w700 : FontWeight.w500,
        ),
      ),
      subtitle: Text(
        '${profile.teamId} · ${profile.baseUrl}',
        style: GoogleFonts.jetBrainsMono(
          fontSize: 11,
          color: scheme.onSurfaceVariant,
        ),
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      onTap: isActive
          ? null
          : () => ref.read(hubProvider.notifier).activateProfile(profile.id),
      trailing: PopupMenuButton<String>(
        onSelected: (action) => _handleAction(context, action),
        itemBuilder: (_) => [
          PopupMenuItem(value: 'rename', child: Text(l10n.renameAction)),
          PopupMenuItem(
              value: 'edit', child: Text(l10n.editConnectionAction)),
          PopupMenuItem(value: 'delete', child: Text(l10n.buttonDelete)),
        ],
      ),
    );
  }

  Future<void> _handleAction(BuildContext context, String action) async {
    switch (action) {
      case 'rename':
        await _renameDialog(context);
        return;
      case 'edit':
        await Navigator.of(context).push(MaterialPageRoute(
          builder: (_) => HubBootstrapScreen(profileId: profile.id),
        ));
        return;
      case 'delete':
        await _confirmDelete(context);
        return;
    }
  }

  Future<void> _renameDialog(BuildContext context) async {
    final l10n = AppLocalizations.of(context)!;
    final ctrl = TextEditingController(text: profile.name);
    final newName = await showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.renameProfileTitle),
        content: TextField(
          controller: ctrl,
          autofocus: true,
          decoration: InputDecoration(labelText: l10n.fieldDisplayName),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(),
            child: Text(l10n.buttonCancel),
          ),
          FilledButton(
            onPressed: () => Navigator.of(ctx).pop(ctrl.text.trim()),
            child: Text(l10n.buttonSave),
          ),
        ],
      ),
    );
    ctrl.dispose();
    if (newName == null || newName.isEmpty || newName == profile.name) return;
    await ref.read(hubProvider.notifier).renameProfile(profile.id, newName);
  }

  Future<void> _confirmDelete(BuildContext context) async {
    final l10n = AppLocalizations.of(context)!;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.deleteProfileTitle),
        content: Text(l10n.deleteProfileBody(profile.name)),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: Text(l10n.buttonCancel),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: DesignColors.error),
            onPressed: () => Navigator.of(ctx).pop(true),
            child: Text(l10n.buttonDelete),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    await ref.read(hubProvider.notifier).deleteProfile(profile.id);
  }
}

class _EmptyState extends StatelessWidget {
  const _EmptyState();

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.cloud_off_outlined, size: 48),
            const SizedBox(height: 12),
            Text(
              l10n.noHubProfiles,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 16,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 6),
            Text(
              l10n.noHubProfilesDesc,
              style: GoogleFonts.spaceGrotesk(fontSize: 13),
              textAlign: TextAlign.center,
            ),
          ],
        ),
      ),
    );
  }
}
