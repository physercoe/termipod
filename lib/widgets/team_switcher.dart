import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import 'package:termipod/l10n/app_localizations.dart';

import '../providers/hub_provider.dart';
import '../screens/hub/hub_bootstrap_screen.dart';
import '../screens/hub/hub_profiles_screen.dart';
import '../screens/team/team_screen.dart';
import '../screens/team/templates_screen.dart';
import '../services/hub/hub_profiles.dart';
import '../theme/design_colors.dart';

/// Persistent profile + team switcher pill, shown in the AppBar of every
/// Tier-1 tab.
///
/// Tapping opens a popup menu with:
/// - the list of saved profiles (a checkmark marks the active one) — tap
///   one to switch, which re-binds the hub client and refreshes
///   dashboards (the cache partition for that profile rehydrates
///   instantly so the UI doesn't blink to empty)
/// - "Add profile" → bootstrap wizard in add-new mode
/// - "Manage profiles" → list view for rename / delete / re-edit
/// - "Templates & engines" — moved here from the project app bar
/// - "Team settings"
class TeamSwitcher extends ConsumerWidget {
  const TeamSwitcher({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final hub = ref.watch(hubProvider).value;
    if (hub == null) return const SizedBox.shrink();
    final activeId = hub.activeProfileId;
    final HubProfile? active =
        (activeId == null) ? null : _findProfile(hub.profiles, activeId);
    if (active == null && hub.profiles.isEmpty) {
      // First run / no profiles yet — bootstrap screen owns onboarding.
      return const SizedBox.shrink();
    }

    final l10n = AppLocalizations.of(context)!;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final fg = isDark
        ? DesignColors.textSecondary
        : DesignColors.textSecondaryLight;

    final pillLabel = active?.name ?? 'Choose profile';

    return Center(
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 4),
        child: PopupMenuButton<_MenuAction>(
          tooltip: l10n.teamSwitcherTooltip,
          position: PopupMenuPosition.under,
          onSelected: (action) => _handleSelection(context, ref, action),
          itemBuilder: (ctx) => _buildItems(ctx, hub.profiles, activeId),
          child: Container(
            padding:
                const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
            decoration: BoxDecoration(
              color: bg,
              borderRadius: BorderRadius.circular(16),
              border: Border.all(color: border),
            ),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(Icons.groups_2_outlined, size: 14, color: fg),
                const SizedBox(width: 6),
                ConstrainedBox(
                  constraints: const BoxConstraints(maxWidth: 120),
                  child: Text(
                    pillLabel,
                    overflow: TextOverflow.ellipsis,
                    maxLines: 1,
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                      color: fg,
                    ),
                  ),
                ),
                const SizedBox(width: 2),
                Icon(Icons.expand_more, size: 14, color: fg),
              ],
            ),
          ),
        ),
      ),
    );
  }

  static HubProfile? _findProfile(List<HubProfile> profiles, String id) {
    for (final p in profiles) {
      if (p.id == id) return p;
    }
    return null;
  }

  List<PopupMenuEntry<_MenuAction>> _buildItems(
    BuildContext ctx,
    List<HubProfile> profiles,
    String? activeId,
  ) {
    final scheme = Theme.of(ctx).colorScheme;
    final items = <PopupMenuEntry<_MenuAction>>[];

    if (profiles.isNotEmpty) {
      items.add(PopupMenuItem<_MenuAction>(
        enabled: false,
        height: 28,
        child: Text(
          'Profiles',
          style: GoogleFonts.spaceGrotesk(
            fontSize: 11,
            fontWeight: FontWeight.w700,
            letterSpacing: 0.5,
            color: scheme.onSurfaceVariant,
          ),
        ),
      ));
      for (final p in profiles) {
        final isActive = p.id == activeId;
        items.add(PopupMenuItem<_MenuAction>(
          value: _MenuAction.activateProfile(p.id),
          child: Row(
            children: [
              Icon(
                isActive ? Icons.check : Icons.circle_outlined,
                size: 16,
                color: isActive ? scheme.primary : scheme.onSurfaceVariant,
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Text(
                      p.name,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: GoogleFonts.spaceGrotesk(
                        fontSize: 13,
                        fontWeight:
                            isActive ? FontWeight.w700 : FontWeight.w500,
                      ),
                    ),
                    Text(
                      '${p.teamId} · ${_hostOf(p.baseUrl)}',
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 11,
                        color: scheme.onSurfaceVariant,
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
        ));
      }
      items.add(const PopupMenuDivider());
    }

    items.add(PopupMenuItem<_MenuAction>(
      value: const _MenuAction.addProfile(),
      child: Row(children: const [
        Icon(Icons.add, size: 18),
        SizedBox(width: 10),
        Text('Add profile…'),
      ]),
    ));
    items.add(PopupMenuItem<_MenuAction>(
      value: const _MenuAction.manageProfiles(),
      child: Row(children: const [
        Icon(Icons.tune, size: 18),
        SizedBox(width: 10),
        Text('Manage profiles…'),
      ]),
    ));
    items.add(const PopupMenuDivider());
    items.add(PopupMenuItem<_MenuAction>(
      value: const _MenuAction.openTemplates(),
      child: Row(children: const [
        Icon(Icons.description_outlined, size: 18),
        SizedBox(width: 10),
        Text('Templates & engines'),
      ]),
    ));
    items.add(PopupMenuItem<_MenuAction>(
      value: const _MenuAction.openTeamSettings(),
      child: Row(children: const [
        Icon(Icons.settings_outlined, size: 18),
        SizedBox(width: 10),
        Text('Team settings'),
      ]),
    ));
    return items;
  }

  Future<void> _handleSelection(
    BuildContext context,
    WidgetRef ref,
    _MenuAction action,
  ) async {
    switch (action.kind) {
      case _MenuKind.activate:
        final id = action.profileId!;
        final cur = ref.read(hubProvider).value?.activeProfileId;
        if (cur == id) return;
        await ref.read(hubProvider.notifier).activateProfile(id);
        return;
      case _MenuKind.add:
        await Navigator.of(context).push(MaterialPageRoute(
          builder: (_) => const HubBootstrapScreen(addNew: true),
        ));
        return;
      case _MenuKind.manage:
        await Navigator.of(context).push(MaterialPageRoute(
          builder: (_) => const HubProfilesScreen(),
        ));
        return;
      case _MenuKind.templates:
        await Navigator.of(context).push(MaterialPageRoute(
          builder: (_) => const TemplatesScreen(),
        ));
        return;
      case _MenuKind.teamSettings:
        await Navigator.of(context).push(MaterialPageRoute(
          builder: (_) => const TeamScreen(),
        ));
        return;
    }
  }

  static String _hostOf(String baseUrl) {
    final u = Uri.tryParse(baseUrl);
    if (u == null) return baseUrl;
    final port = u.hasPort ? ':${u.port}' : '';
    return '${u.host}$port';
  }
}

enum _MenuKind { activate, add, manage, templates, teamSettings }

class _MenuAction {
  final _MenuKind kind;
  final String? profileId;

  const _MenuAction._(this.kind, [this.profileId]);
  const _MenuAction.addProfile() : this._(_MenuKind.add);
  const _MenuAction.manageProfiles() : this._(_MenuKind.manage);
  const _MenuAction.openTemplates() : this._(_MenuKind.templates);
  const _MenuAction.openTeamSettings() : this._(_MenuKind.teamSettings);
  const _MenuAction.activateProfile(String id)
      : this._(_MenuKind.activate, id);
}
