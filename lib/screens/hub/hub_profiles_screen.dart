import 'package:flutter/material.dart';
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
    final hub = ref.watch(hubProvider).value;
    final profiles = hub?.profiles ?? const <HubProfile>[];
    final activeId = hub?.activeProfileId;

    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Hub profiles',
          style: GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700),
        ),
        actions: [
          IconButton(
            tooltip: 'Add profile',
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
          const PopupMenuItem(value: 'rename', child: Text('Rename…')),
          const PopupMenuItem(value: 'edit', child: Text('Edit connection…')),
          const PopupMenuItem(value: 'delete', child: Text('Delete')),
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
    final ctrl = TextEditingController(text: profile.name);
    final newName = await showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Rename profile'),
        content: TextField(
          controller: ctrl,
          autofocus: true,
          decoration: const InputDecoration(labelText: 'Display name'),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(ctx).pop(ctrl.text.trim()),
            child: const Text('Save'),
          ),
        ],
      ),
    );
    ctrl.dispose();
    if (newName == null || newName.isEmpty || newName == profile.name) return;
    await ref.read(hubProvider.notifier).renameProfile(profile.id, newName);
  }

  Future<void> _confirmDelete(BuildContext context) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Delete profile?'),
        content: Text(
          'Removes "${profile.name}" and its saved token. The offline '
          'cache for this hub will be wiped. This cannot be undone.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: DesignColors.error),
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('Delete'),
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
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.cloud_off_outlined, size: 48),
            const SizedBox(height: 12),
            Text(
              'No hub profiles yet',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 16,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 6),
            Text(
              'Use the + button above to add one.',
              style: GoogleFonts.spaceGrotesk(fontSize: 13),
              textAlign: TextAlign.center,
            ),
          ],
        ),
      ),
    );
  }
}
