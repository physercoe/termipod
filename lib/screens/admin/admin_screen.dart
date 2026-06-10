import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../services/hub/hub_client.dart';
import '../../theme/design_colors.dart';
import '../../theme/tokens.dart';
import 'admin_audit_screen.dart';
import 'admin_teams_controller.dart';
import 'confirm_action_tile.dart';

/// The operator's hub-management cockpit (ADR-028 Phase 5 / ADR-037).
///
/// Reached from the second AppBar action on `HubDetailScreen` — *not* a
/// bottom-nav tab. Like `HubRolesConfigScreen` it does not pre-probe the
/// token scope: it opens for anyone and surfaces the hub's own 403 when a
/// non-operator token hits an `/v1/admin/*` route.
///
/// Management splits into four kinds, one tab each:
///   · Fleet — fleet-wide + per-host lifecycle (update / restart / shutdown)
///   · Teams — provision teams and rotate their owner tokens (ADR-037 D3)
///   · Upkeep — host-token rotation and DB vacuum
///   · Audit — recent admin actions (tap through to the full query screen)
///
/// Every destructive action goes through [ConfirmActionTile] — a plain tap
/// never fires one — and one-time secrets (owner tokens) surface in a
/// copy-once dialog, never a snackbar.
class AdminScreen extends ConsumerStatefulWidget {
  const AdminScreen({super.key});

  @override
  ConsumerState<AdminScreen> createState() => _AdminScreenState();
}

class _AdminScreenState extends ConsumerState<AdminScreen> {
  bool _loading = true;
  String? _error;
  List<Map<String, dynamic>> _hosts = const [];
  List<Map<String, dynamic>> _teams = const [];
  List<Map<String, dynamic>> _audit = const [];

  /// Key of the action whose network call is in flight, so the matching
  /// tile shows a spinner and the rest stay inert. Null when idle.
  String? _busyKey;

  @override
  void initState() {
    super.initState();
    _load();
  }

  HubClient? get _client => ref.read(hubProvider.notifier).client;

  String get _activeTeamId => _client?.cfg.teamId ?? '';

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    final client = _client;
    if (client == null) {
      setState(() {
        _loading = false;
        _error = 'Hub not configured.';
      });
      return;
    }
    try {
      final hosts = await client.adminListHosts(ping: true);
      final teams = await client.adminListTeams();
      final audit = await client.adminListAudit(limit: 50);
      if (!mounted) return;
      setState(() {
        _hosts = hosts;
        _teams = teams;
        _audit = audit;
        _loading = false;
      });
    } on HubApiError catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = e.status == 403
            ? 'The Admin pane requires an operator-kind token.'
            : '${e.status}: ${e.message}';
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
  }

  /// Runs one admin action: marks [key] busy, awaits [action], shows the
  /// outcome in a snackbar, then reloads so the rows reflect reality.
  Future<void> _run(
    String key,
    Future<Map<String, dynamic>> Function(HubClient) action,
    String Function(Map<String, dynamic>) summarise,
  ) async {
    final client = _client;
    if (client == null || _busyKey != null) return;
    setState(() => _busyKey = key);
    String message;
    try {
      final res = await action(client);
      message = summarise(res);
    } on HubApiError catch (e) {
      message = e.status == 403
          ? 'Operator token required.'
          : 'Failed (${e.status}): ${e.message}';
    } catch (e) {
      message = 'Failed: $e';
    }
    if (!mounted) return;
    setState(() => _busyKey = null);
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(SnackBar(content: Text(message)));
    await _load();
  }

  // ---- action summarisers ----

  String _fleetSummary(String verb, Map<String, dynamic> res) {
    final hosts = (res['hosts'] as List?) ?? const [];
    final acked = hosts.where((h) => (h as Map)['acked'] == true).length;
    return '$verb: $acked/${hosts.length} host(s) acked';
  }

  String _hostSummary(String verb, Map<String, dynamic> res) {
    final acked = res['acked'] == true;
    final err = (res['error'] as String?) ?? '';
    if (acked) return '$verb acked';
    return err.isEmpty ? '$verb sent' : '$verb: $err';
  }

  @override
  Widget build(BuildContext context) {
    return DefaultTabController(
      length: 4,
      child: Scaffold(
        appBar: AppBar(
          title: Text(
            'Hub admin',
            style: GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700),
          ),
          actions: [
            IconButton(
              tooltip: 'Refresh',
              icon: const Icon(Icons.refresh),
              onPressed: _loading ? null : _load,
            ),
          ],
          bottom: const TabBar(
            isScrollable: true,
            tabs: [
              Tab(text: 'Fleet'),
              Tab(text: 'Teams'),
              Tab(text: 'Upkeep'),
              Tab(text: 'Audit'),
            ],
          ),
        ),
        body: _loading
            ? const Center(child: CircularProgressIndicator())
            : _error != null
                ? _ErrorState(message: _error!)
                : TabBarView(
                    children: [
                      _fleetTab(),
                      _teamsTab(),
                      _upkeepTab(),
                      _auditTab(),
                    ],
                  ),
      ),
    );
  }

  // ---- shared chrome ----

  Widget _sectionHeader(String label) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Text(
        label,
        style: GoogleFonts.jetBrainsMono(
          fontSize: FontSizes.label,
          fontWeight: FontWeight.w700,
          letterSpacing: 1.2,
          color: isDark
              ? DesignColors.textSecondary
              : DesignColors.textSecondaryLight,
        ),
      ),
    );
  }

  Widget _emptyNote(String text) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 8),
      child: Text(
        text,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 11,
          color:
              isDark ? DesignColors.textMuted : DesignColors.textMutedLight,
        ),
      ),
    );
  }

  // ---- Fleet tab: fleet-wide host ops + per-host cards ----

  Widget _fleetTab() {
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 32),
        children: [
          _sectionHeader('FLEET-WIDE'),
          _bottomGap(ConfirmActionTile(
            label: 'Update all hosts + hub',
            icon: Icons.system_update_alt,
            destructive: false,
            busy: _busyKey == 'fleet.update',
            enabled: _busyKey == null,
            hint: 'Long-press, then slide right to update the whole fleet.',
            onConfirmed: () => _run('fleet.update', (c) => c.adminFleetUpdate(),
                (r) => _fleetSummary('update', r)),
          )),
          _bottomGap(ConfirmActionTile(
            label: 'Restart all hosts',
            icon: Icons.restart_alt,
            busy: _busyKey == 'fleet.restart',
            enabled: _busyKey == null,
            hint: 'Long-press, then slide right to restart every host.',
            onConfirmed: () => _run('fleet.restart',
                (c) => c.adminFleetRestart(), (r) => _fleetSummary('restart', r)),
          )),
          _bottomGap(ConfirmActionTile(
            label: 'Shutdown all hosts',
            icon: Icons.power_settings_new,
            busy: _busyKey == 'fleet.shutdown',
            enabled: _busyKey == null,
            hint: 'Long-press, then slide right to shut the fleet down.',
            onConfirmed: () => _run('fleet.shutdown',
                (c) => c.adminFleetShutdown(), (r) => _fleetSummary('shutdown', r)),
          )),
          const SizedBox(height: 20),
          _sectionHeader('HOSTS (${_hosts.length})'),
          if (_hosts.isEmpty)
            _emptyNote('No hosts registered.')
          else
            ..._hosts.map(_hostCard),
        ],
      ),
    );
  }

  Widget _bottomGap(Widget child) =>
      Padding(padding: const EdgeInsets.only(bottom: 8), child: child);

  Widget _hostCard(Map<String, dynamic> h) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final id = (h['host_id'] as String?) ?? '';
    final name = (h['name'] as String?)?.isNotEmpty == true
        ? h['name'] as String
        : id;
    final live = h['live'] == true;
    final version = (h['version'] as String?) ?? '';
    final pingErr = (h['ping_error'] as String?) ?? '';
    final pingMs = (h['ping_ms'] as num?)?.toInt();
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;

    final subtitle = StringBuffer(live ? 'live' : 'offline');
    if (version.isNotEmpty) subtitle.write(' · $version');
    if (pingMs != null && pingMs > 0 && pingErr.isEmpty) {
      subtitle.write(' · ${pingMs}ms');
    }
    if (pingErr.isNotEmpty) subtitle.write(' · ping: $pingErr');

    return Container(
      margin: const EdgeInsets.only(bottom: 12),
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        borderRadius: Radii.mdBorder,
        border: Border.all(
          color:
              isDark ? DesignColors.borderDark : DesignColors.borderLight,
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Icon(
                live ? Icons.circle : Icons.circle_outlined,
                size: 10,
                color: live ? DesignColors.success : muted,
              ),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  name,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 14,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 2),
          Padding(
            padding: const EdgeInsets.only(left: Spacing.s16),
            child: Text(
              subtitle.toString(),
              style: GoogleFonts.jetBrainsMono(fontSize: FontSizes.label, color: muted),
            ),
          ),
          const SizedBox(height: 10),
          ConfirmActionTile(
            label: 'Restart $name',
            icon: Icons.restart_alt,
            enabled: live && _busyKey == null,
            busy: _busyKey == 'host.restart.$id',
            hint: 'Long-press, then slide right to restart this host.',
            onConfirmed: () => _run('host.restart.$id',
                (c) => c.adminHostRestart(id), (r) => _hostSummary('restart', r)),
          ),
          const SizedBox(height: 8),
          ConfirmActionTile(
            label: 'Update $name',
            icon: Icons.system_update_alt,
            destructive: false,
            enabled: live && _busyKey == null,
            busy: _busyKey == 'host.update.$id',
            hint: 'Long-press, then slide right to update this host.',
            onConfirmed: () => _run('host.update.$id',
                (c) => c.adminHostUpdate(id), (r) => _hostSummary('update', r)),
          ),
          const SizedBox(height: 8),
          ConfirmActionTile(
            label: 'Shutdown $name',
            icon: Icons.power_settings_new,
            enabled: live && _busyKey == null,
            busy: _busyKey == 'host.shutdown.$id',
            hint: 'Long-press, then slide right to shut this host down.',
            onConfirmed: () => _run('host.shutdown.$id',
                (c) => c.adminHostShutdown(id),
                (r) => _hostSummary('shutdown', r)),
          ),
        ],
      ),
    );
  }

  // ---- Teams tab: list + create + rotate owner token (ADR-037 D3) ----

  Widget _teamsTab() {
    final teams = sortTeamsForDisplay(_teams, _activeTeamId);
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 32),
        children: [
          Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              _sectionHeader('TEAMS (${teams.length})'),
              TextButton.icon(
                onPressed: _busyKey == null ? _showCreateTeamDialog : null,
                icon: const Icon(Icons.add, size: 16),
                label: const Text('New team'),
              ),
            ],
          ),
          if (teams.isEmpty)
            _emptyNote('No teams found.')
          else
            ...teams.map(_teamCard),
        ],
      ),
    );
  }

  Widget _teamCard(Map<String, dynamic> t) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final id = (t['id'] as String?) ?? '';
    final name = (t['name'] as String?)?.isNotEmpty == true
        ? t['name'] as String
        : id;
    final created = (t['created_at'] as String?) ?? '';
    final active = isActiveTeam(t, _activeTeamId);
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;

    final sub = StringBuffer(id);
    if (created.length >= 10) sub.write(' · ${created.substring(0, 10)}');

    return Container(
      margin: const EdgeInsets.only(bottom: 12),
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        borderRadius: Radii.mdBorder,
        border: Border.all(
          color: active
              ? DesignColors.primary
              : (isDark ? DesignColors.borderDark : DesignColors.borderLight),
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  name,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 14,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
              if (active) _activeChip(),
            ],
          ),
          const SizedBox(height: 2),
          Text(
            sub.toString(),
            style: GoogleFonts.jetBrainsMono(fontSize: FontSizes.label, color: muted),
          ),
          const SizedBox(height: 10),
          ConfirmActionTile(
            label: 'Rotate owner token',
            icon: Icons.key,
            enabled: _busyKey == null,
            busy: _busyKey == 'team.rotate.$id',
            hint: 'Long-press, then slide right to rotate $name’s owner token.',
            onConfirmed: () => _rotateTeamToken(id, name),
          ),
        ],
      ),
    );
  }

  Widget _activeChip() {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: DesignColors.primary.withValues(alpha: 0.18),
        borderRadius: Radii.smBorder,
      ),
      child: Text(
        'ACTIVE',
        style: GoogleFonts.jetBrainsMono(
          fontSize: FontSizes.label,
          fontWeight: FontWeight.w700,
          letterSpacing: 0.8,
          color: DesignColors.primary,
        ),
      ),
    );
  }

  Future<void> _showCreateTeamDialog() async {
    final result = await showDialog<_NewTeam>(
      context: context,
      builder: (_) => const _TeamCreateDialog(),
    );
    if (result == null || !mounted) return;
    await _createTeam(result);
  }

  Future<void> _createTeam(_NewTeam spec) async {
    final client = _client;
    if (client == null || _busyKey != null) return;
    setState(() => _busyKey = 'team.create');
    try {
      final res = await client.adminCreateTeam(
        spec.id,
        name: spec.name,
        handle: spec.handle,
      );
      if (!mounted) return;
      setState(() => _busyKey = null);
      await _load();
      if (!mounted) return;
      await _showSecretDialog(
        title: 'Team “${res['team_id'] ?? spec.id}” created',
        subtitle:
            'This is the new team’s owner token — shown once. Hand it to '
            'the team’s director, or add a hub profile with it to switch '
            'into the team.',
        secret: (res['owner_token'] as String?) ?? '',
      );
    } on HubApiError catch (e) {
      _failSnack(e.status == 409
          ? 'A team with that id already exists.'
          : e.status == 403
              ? 'Operator token required.'
              : 'Failed (${e.status}): ${e.message}');
    } catch (e) {
      _failSnack('Failed: $e');
    }
  }

  Future<void> _rotateTeamToken(String id, String name) async {
    final client = _client;
    if (client == null || _busyKey != null) return;
    setState(() => _busyKey = 'team.rotate.$id');
    try {
      final res = await client.adminRotateTeamToken(id);
      if (!mounted) return;
      setState(() => _busyKey = null);
      await _load();
      if (!mounted) return;
      final revoked = (res['revoked_count'] as num?)?.toInt() ?? 0;
      await _showSecretDialog(
        title: 'Rotated $name’s owner token',
        subtitle: revoked > 0
            ? 'New owner token below — shown once. $revoked prior token(s) '
                'revoked, so update any profile or director still on the old one.'
            : 'New owner token below — shown once. No prior owner token existed '
                '(this team’s director may be the operator credential), so '
                'nothing was revoked.',
        secret: (res['new_token'] as String?) ?? '',
      );
    } on HubApiError catch (e) {
      _failSnack(e.status == 404
          ? 'Team not found.'
          : e.status == 403
              ? 'Operator token required.'
              : 'Failed (${e.status}): ${e.message}');
    } catch (e) {
      _failSnack('Failed: $e');
    }
  }

  void _failSnack(String message) {
    if (!mounted) return;
    setState(() => _busyKey = null);
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(SnackBar(content: Text(message)));
  }

  /// A modal that reveals a one-time secret with a copy button. The token
  /// is never logged or persisted — closing the dialog drops it.
  Future<void> _showSecretDialog({
    required String title,
    required String subtitle,
    required String secret,
  }) async {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    await showDialog<void>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(title, style: GoogleFonts.spaceGrotesk(fontSize: 16)),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text(
              subtitle,
              style: GoogleFonts.jetBrainsMono(fontSize: 11, height: 1.4),
            ),
            const SizedBox(height: 12),
            Container(
              padding: const EdgeInsets.all(Spacing.s8),
              decoration: BoxDecoration(
                color: isDark
                    ? DesignColors.inputDark
                    : DesignColors.inputLight,
                borderRadius: BorderRadius.circular(8),
                border: Border.all(
                  color: isDark
                      ? DesignColors.borderDark
                      : DesignColors.borderLight,
                ),
              ),
              child: SelectableText(
                secret,
                style: GoogleFonts.jetBrainsMono(fontSize: 12),
              ),
            ),
          ],
        ),
        actions: [
          TextButton.icon(
            icon: const Icon(Icons.copy, size: 16),
            label: const Text('Copy'),
            onPressed: () {
              Clipboard.setData(ClipboardData(text: secret));
              ScaffoldMessenger.of(ctx)
                ..hideCurrentSnackBar()
                ..showSnackBar(
                    const SnackBar(content: Text('Token copied')));
            },
          ),
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(),
            child: const Text('Done'),
          ),
        ],
      ),
    );
  }

  // ---- Upkeep tab: host-token rotation + DB vacuum ----

  Widget _upkeepTab() {
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 32),
        children: [
          _sectionHeader('CREDENTIALS'),
          _bottomGap(ConfirmActionTile(
            label: 'Rotate host tokens',
            icon: Icons.vpn_key,
            busy: _busyKey == 'tokens.rotate',
            enabled: _busyKey == null,
            hint: 'Long-press, then slide right to rotate the host bearer.',
            onConfirmed: () => _run('tokens.rotate',
                (c) => c.adminRotateTokens(), (r) {
              final revoked = r['old_tokens_revoked'] == true;
              return revoked
                  ? 'Token rotated — old tokens revoked'
                  : 'Token rotated — old tokens kept (${r['note'] ?? 'not all hosts acked'})';
            }),
          )),
          const SizedBox(height: 20),
          _sectionHeader('DATABASE'),
          _bottomGap(ConfirmActionTile(
            label: 'Vacuum hub database',
            icon: Icons.cleaning_services,
            destructive: false,
            busy: _busyKey == 'db.vacuum',
            enabled: _busyKey == null,
            hint: 'Long-press, then slide right to vacuum the database.',
            onConfirmed: () => _run('db.vacuum', (c) => c.adminDbVacuum(), (r) {
              final kb = ((r['reclaimed'] as num?) ?? 0) / 1024;
              return 'Vacuumed — ${kb.toStringAsFixed(1)} KiB reclaimed';
            }),
          )),
        ],
      ),
    );
  }

  // ---- Audit tab: recent admin actions ----

  Widget _auditTab() {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 32),
        children: [
          GestureDetector(
            onTap: () => Navigator.of(context).push(
              MaterialPageRoute(builder: (_) => const AdminAuditScreen()),
            ),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                _sectionHeader('RECENT ADMIN ACTIONS'),
                Icon(Icons.chevron_right, size: 16, color: muted),
              ],
            ),
          ),
          if (_audit.isEmpty)
            _emptyNote('No admin actions recorded yet.')
          else
            ..._audit.take(50).map((e) => _auditRow(e, muted)),
        ],
      ),
    );
  }

  Widget _auditRow(Map<String, dynamic> e, Color muted) {
    final action = (e['action'] as String?) ?? '';
    final summary = (e['summary'] as String?) ?? '';
    final ts = (e['ts'] as String?) ?? '';
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 112,
            child: Text(
              action,
              style: GoogleFonts.jetBrainsMono(
                fontSize: FontSizes.label,
                color: DesignColors.primary,
              ),
            ),
          ),
          Expanded(
            child: Text(
              summary,
              style: GoogleFonts.jetBrainsMono(fontSize: FontSizes.label),
            ),
          ),
          Text(
            ts.length >= 16 ? ts.substring(5, 16) : ts,
            style: GoogleFonts.jetBrainsMono(fontSize: FontSizes.label, color: muted),
          ),
        ],
      ),
    );
  }
}

/// The result of the create-team dialog.
class _NewTeam {
  const _NewTeam(this.id, this.name, this.handle);
  final String id;
  final String name;
  final String handle;
}

/// Create-team form. Returns a [_NewTeam] via Navigator.pop, or null on
/// cancel. The id is validated against the same slug shape the hub
/// enforces so an obvious typo is caught before the round-trip.
class _TeamCreateDialog extends StatefulWidget {
  const _TeamCreateDialog();

  @override
  State<_TeamCreateDialog> createState() => _TeamCreateDialogState();
}

class _TeamCreateDialogState extends State<_TeamCreateDialog> {
  final _id = TextEditingController();
  final _name = TextEditingController();
  final _handle = TextEditingController();
  String? _idError;

  // Mirrors teamIDRe in hub/internal/server/provision.go.
  static final _slug = RegExp(r'^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$');

  @override
  void dispose() {
    _id.dispose();
    _name.dispose();
    _handle.dispose();
    super.dispose();
  }

  void _submit() {
    final id = _id.text.trim();
    if (!_slug.hasMatch(id)) {
      setState(() => _idError =
          'Lowercase letters, digits and hyphens; no leading/trailing hyphen.');
      return;
    }
    Navigator.of(context).pop(
      _NewTeam(id, _name.text.trim(), _handle.text.trim()),
    );
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: Text('New team', style: GoogleFonts.spaceGrotesk(fontSize: 16)),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          TextField(
            controller: _id,
            autofocus: true,
            decoration: InputDecoration(
              labelText: 'Team ID',
              hintText: 'acme-research',
              errorText: _idError,
            ),
            onChanged: (_) {
              if (_idError != null) setState(() => _idError = null);
            },
          ),
          const SizedBox(height: 8),
          TextField(
            controller: _name,
            decoration: const InputDecoration(
              labelText: 'Display name (optional)',
            ),
          ),
          const SizedBox(height: 8),
          TextField(
            controller: _handle,
            decoration: const InputDecoration(
              labelText: 'Owner handle (optional)',
            ),
          ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Cancel'),
        ),
        FilledButton(onPressed: _submit, child: const Text('Create')),
      ],
    );
  }
}

class _ErrorState extends StatelessWidget {
  const _ErrorState({required this.message});
  final String message;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Text(
          message,
          textAlign: TextAlign.center,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 12,
            color: DesignColors.error,
          ),
        ),
      ),
    );
  }
}
