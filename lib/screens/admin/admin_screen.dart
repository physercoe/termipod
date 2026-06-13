import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:termipod/l10n/app_localizations.dart';

import '../../providers/hub_provider.dart';
import '../../providers/vocab_provider.dart';
import '../../services/hub/hub_client.dart';
import '../../services/vocab/vocab_axis.dart';
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
  bool _hubMissing = false;
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
      _hubMissing = false;
      _error = null;
    });
    final client = _client;
    if (client == null) {
      setState(() {
        _loading = false;
        _hubMissing = true;
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
            ? AppLocalizations.of(context)!
                .adminOperatorTokenRequiredForPane
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
    final l10n = AppLocalizations.of(context)!;
    final client = _client;
    if (client == null || _busyKey != null) return;
    setState(() => _busyKey = key);
    String message;
    try {
      final res = await action(client);
      message = summarise(res);
    } on HubApiError catch (e) {
      message = e.status == 403
          ? l10n.adminOperatorTokenRequired
          : l10n.failedStatusMessage(e.status, e.message);
    } catch (e) {
      message = l10n.failedMessage('$e');
    }
    if (!mounted) return;
    setState(() => _busyKey = null);
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(SnackBar(content: Text(message)));
    await _load();
  }

  // ---- action summarisers ----

  String _fleetSummary(
    AppLocalizations l10n,
    String verb,
    String hostsLabel,
    Map<String, dynamic> res,
  ) {
    final hosts = (res['hosts'] as List?) ?? const [];
    final acked = hosts.where((h) => (h as Map)['acked'] == true).length;
    return l10n.adminFleetActionSummary(
      verb,
      acked,
      hosts.length,
      hostsLabel,
    );
  }

  String _hostSummary(
    AppLocalizations l10n,
    String verb,
    Map<String, dynamic> res,
  ) {
    final acked = res['acked'] == true;
    final err = (res['error'] as String?) ?? '';
    if (acked) return l10n.adminHostActionAcked(verb);
    return err.isEmpty
        ? l10n.adminHostActionSent(verb)
        : l10n.adminHostActionError(verb, err);
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    return DefaultTabController(
      length: 4,
      child: Scaffold(
        appBar: AppBar(
          title: Text(
            l10n.adminTitle,
            style: GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700),
          ),
          actions: [
            IconButton(
              tooltip: l10n.buttonRefresh,
              icon: const Icon(Icons.refresh),
              onPressed: _loading ? null : _load,
            ),
          ],
          bottom: TabBar(
            isScrollable: true,
            tabs: [
              Tab(text: l10n.adminTabFleet),
              Tab(text: l10n.adminTabTeams),
              Tab(text: l10n.adminTabUpkeep),
              Tab(text: l10n.adminTabAudit),
            ],
          ),
        ),
        body: _loading
            ? const Center(child: CircularProgressIndicator())
            : _hubMissing || _error != null
                ? _ErrorState(
                    message: _hubMissing ? l10n.hubNotConfigured : _error!,
                  )
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
    final l10n = AppLocalizations.of(context)!;
    final vocab = ref.watch(vocabularyProvider);
    final host = vocab.term(VocabAxis.entityHost);
    final hostsLower = host.pluralLower;
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 32),
        children: [
          _sectionHeader(l10n.adminSectionFleetWide),
          _bottomGap(ConfirmActionTile(
            label: l10n.adminUpdateAllHosts(hostsLower),
            icon: Icons.system_update_alt,
            destructive: false,
            busy: _busyKey == 'fleet.update',
            enabled: _busyKey == null,
            hint: l10n.adminUpdateAllHostsHint,
            onConfirmed: () => _run('fleet.update', (c) => c.adminFleetUpdate(),
                (r) => _fleetSummary(
                      l10n,
                      l10n.adminVerbUpdate,
                      hostsLower,
                      r,
                    )),
          )),
          _bottomGap(ConfirmActionTile(
            label: l10n.adminRestartAllHosts(hostsLower),
            icon: Icons.restart_alt,
            busy: _busyKey == 'fleet.restart',
            enabled: _busyKey == null,
            hint: l10n.adminRestartAllHostsHint(host.lower),
            onConfirmed: () => _run('fleet.restart',
                (c) => c.adminFleetRestart(),
                (r) => _fleetSummary(
                      l10n,
                      l10n.adminVerbRestart,
                      hostsLower,
                      r,
                    )),
          )),
          _bottomGap(ConfirmActionTile(
            label: l10n.adminShutdownAllHosts(hostsLower),
            icon: Icons.power_settings_new,
            busy: _busyKey == 'fleet.shutdown',
            enabled: _busyKey == null,
            hint: l10n.adminShutdownAllHostsHint,
            onConfirmed: () => _run('fleet.shutdown',
                (c) => c.adminFleetShutdown(),
                (r) => _fleetSummary(
                      l10n,
                      l10n.adminVerbShutdown,
                      hostsLower,
                      r,
                    )),
          )),
          const SizedBox(height: 20),
          _sectionHeader(l10n.adminHostsSection(host.plural, _hosts.length)),
          if (_hosts.isEmpty)
            _emptyNote(l10n.adminNoHostsRegistered(hostsLower))
          else
            ..._hosts.map(_hostCard),
        ],
      ),
    );
  }

  Widget _bottomGap(Widget child) =>
      Padding(padding: const EdgeInsets.only(bottom: 8), child: child);

  Widget _hostCard(Map<String, dynamic> h) {
    final l10n = AppLocalizations.of(context)!;
    final vocab = ref.watch(vocabularyProvider);
    final host = vocab.term(VocabAxis.entityHost).lower;
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

    final subtitle = StringBuffer(
      live ? l10n.adminHostStatusLive : l10n.adminHostStatusOffline,
    );
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
            label: l10n.adminRestartNamedHost(name),
            icon: Icons.restart_alt,
            enabled: live && _busyKey == null,
            busy: _busyKey == 'host.restart.$id',
            hint: l10n.adminRestartHostHint(host),
            onConfirmed: () => _run('host.restart.$id',
                (c) => c.adminHostRestart(id),
                (r) => _hostSummary(l10n, l10n.adminVerbRestart, r)),
          ),
          const SizedBox(height: 8),
          ConfirmActionTile(
            label: l10n.adminUpdateNamedHost(name),
            icon: Icons.system_update_alt,
            destructive: false,
            enabled: live && _busyKey == null,
            busy: _busyKey == 'host.update.$id',
            hint: l10n.adminUpdateHostHint(host),
            onConfirmed: () => _run('host.update.$id',
                (c) => c.adminHostUpdate(id),
                (r) => _hostSummary(l10n, l10n.adminVerbUpdate, r)),
          ),
          const SizedBox(height: 8),
          ConfirmActionTile(
            label: l10n.adminShutdownNamedHost(name),
            icon: Icons.power_settings_new,
            enabled: live && _busyKey == null,
            busy: _busyKey == 'host.shutdown.$id',
            hint: l10n.adminShutdownHostHint(host),
            onConfirmed: () => _run('host.shutdown.$id',
                (c) => c.adminHostShutdown(id),
                (r) => _hostSummary(l10n, l10n.adminVerbShutdown, r)),
          ),
        ],
      ),
    );
  }

  // ---- Teams tab: list + create + rotate owner token (ADR-037 D3) ----

  Widget _teamsTab() {
    final l10n = AppLocalizations.of(context)!;
    final vocab = ref.watch(vocabularyProvider);
    final team = vocab.term(VocabAxis.entityTeam);
    final teams = sortTeamsForDisplay(_teams, _activeTeamId);
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 32),
        children: [
          Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              _sectionHeader(l10n.adminTeamsSection(team.plural, teams.length)),
              TextButton.icon(
                onPressed: _busyKey == null ? _showCreateTeamDialog : null,
                icon: const Icon(Icons.add, size: 16),
                label: Text(l10n.adminNewTeam(team.title)),
              ),
            ],
          ),
          if (teams.isEmpty)
            _emptyNote(l10n.adminNoTeamsFound(team.pluralLower))
          else
            ...teams.map(_teamCard),
        ],
      ),
    );
  }

  Widget _teamCard(Map<String, dynamic> t) {
    final l10n = AppLocalizations.of(context)!;
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
            label: l10n.adminRotateOwnerToken,
            icon: Icons.key,
            enabled: _busyKey == null,
            busy: _busyKey == 'team.rotate.$id',
            hint: l10n.adminRotateOwnerTokenHint(name),
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
        AppLocalizations.of(context)!.adminActiveChip,
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
    final l10n = AppLocalizations.of(context)!;
    final vocab = ref.read(vocabularyProvider);
    final team = vocab.term(VocabAxis.entityTeam);
    final result = await showDialog<_NewTeam>(
      context: context,
      builder: (_) => _TeamCreateDialog(
        l10n: l10n,
        teamTitle: team.title,
      ),
    );
    if (result == null || !mounted) return;
    await _createTeam(result);
  }

  Future<void> _createTeam(_NewTeam spec) async {
    final l10n = AppLocalizations.of(context)!;
    final vocab = ref.read(vocabularyProvider);
    final team = vocab.term(VocabAxis.entityTeam);
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
        title: l10n.adminTeamCreatedTitle(
          team.title,
          (res['team_id'] as String?) ?? spec.id,
        ),
        subtitle: l10n.adminTeamCreatedSubtitle(team.lower),
        secret: (res['owner_token'] as String?) ?? '',
      );
    } on HubApiError catch (e) {
      _failSnack(e.status == 409
          ? l10n.adminTeamAlreadyExists(team.lower)
          : e.status == 403
              ? l10n.adminOperatorTokenRequired
              : l10n.failedStatusMessage(e.status, e.message));
    } catch (e) {
      _failSnack(l10n.failedMessage('$e'));
    }
  }

  Future<void> _rotateTeamToken(String id, String name) async {
    final l10n = AppLocalizations.of(context)!;
    final vocab = ref.read(vocabularyProvider);
    final team = vocab.term(VocabAxis.entityTeam).lower;
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
        title: l10n.adminTeamOwnerTokenRotatedTitle(name),
        subtitle: l10n.adminTeamOwnerTokenRotatedSubtitle(revoked, team),
        secret: (res['new_token'] as String?) ?? '',
      );
    } on HubApiError catch (e) {
      _failSnack(e.status == 404
          ? l10n.adminTeamNotFound(team)
          : e.status == 403
              ? l10n.adminOperatorTokenRequired
              : l10n.failedStatusMessage(e.status, e.message));
    } catch (e) {
      _failSnack(l10n.failedMessage('$e'));
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
            label: Text(AppLocalizations.of(context)!.buttonCopy),
            onPressed: () {
              Clipboard.setData(ClipboardData(text: secret));
              ScaffoldMessenger.of(ctx)
                ..hideCurrentSnackBar()
                ..showSnackBar(SnackBar(
                  content: Text(AppLocalizations.of(ctx)!.tokenCopied),
                ));
            },
          ),
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(),
            child: Text(AppLocalizations.of(context)!.buttonDone),
          ),
        ],
      ),
    );
  }

  // ---- Upkeep tab: host-token rotation + DB vacuum ----

  Widget _upkeepTab() {
    final l10n = AppLocalizations.of(context)!;
    final vocab = ref.watch(vocabularyProvider);
    final host = vocab.term(VocabAxis.entityHost).lower;
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 32),
        children: [
          _sectionHeader(l10n.adminSectionCredentials),
          _bottomGap(ConfirmActionTile(
            label: l10n.adminRotateHostTokens(host),
            icon: Icons.vpn_key,
            busy: _busyKey == 'tokens.rotate',
            enabled: _busyKey == null,
            hint: l10n.adminRotateHostTokensHint(host),
            onConfirmed: () => _run('tokens.rotate',
                (c) => c.adminRotateTokens(), (r) {
              final revoked = r['old_tokens_revoked'] == true;
              return revoked
                  ? l10n.adminHostTokensRotatedRevoked
                  : l10n.adminHostTokensRotatedKept(
                      (r['note'] as String?) ??
                          l10n.adminNotAllHostsAcked(host),
                    );
            }),
          )),
          const SizedBox(height: 20),
          _sectionHeader(l10n.adminSectionDatabase),
          _bottomGap(ConfirmActionTile(
            label: l10n.adminVacuumHubDatabase,
            icon: Icons.cleaning_services,
            destructive: false,
            busy: _busyKey == 'db.vacuum',
            enabled: _busyKey == null,
            hint: l10n.adminVacuumHubDatabaseHint,
            onConfirmed: () => _run('db.vacuum', (c) => c.adminDbVacuum(), (r) {
              final kb = ((r['reclaimed'] as num?) ?? 0) / 1024;
              return l10n.adminVacuumedSummary(kb.toStringAsFixed(1));
            }),
          )),
        ],
      ),
    );
  }

  // ---- Audit tab: recent admin actions ----

  Widget _auditTab() {
    final l10n = AppLocalizations.of(context)!;
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
                _sectionHeader(l10n.adminSectionRecentAdminActions),
                Icon(Icons.chevron_right, size: 16, color: muted),
              ],
            ),
          ),
          if (_audit.isEmpty)
            _emptyNote(l10n.adminNoAdminActionsRecorded)
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
  const _TeamCreateDialog({
    required this.l10n,
    required this.teamTitle,
  });

  final AppLocalizations l10n;
  final String teamTitle;

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
      setState(() => _idError = widget.l10n.adminTeamSlugError);
      return;
    }
    Navigator.of(context).pop(
      _NewTeam(id, _name.text.trim(), _handle.text.trim()),
    );
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: Text(
        widget.l10n.adminNewTeam(widget.teamTitle),
        style: GoogleFonts.spaceGrotesk(fontSize: 16),
      ),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          TextField(
            controller: _id,
            autofocus: true,
            decoration: InputDecoration(
              labelText: widget.l10n.adminTeamIdLabel(widget.teamTitle),
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
            decoration: InputDecoration(
              labelText: widget.l10n.adminDisplayNameOptional,
            ),
          ),
          const SizedBox(height: 8),
          TextField(
            controller: _handle,
            decoration: InputDecoration(
              labelText: widget.l10n.adminOwnerHandleOptional,
            ),
          ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: Text(widget.l10n.buttonCancel),
        ),
        FilledButton(onPressed: _submit, child: Text(widget.l10n.buttonCreate)),
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
