import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../services/hub/hub_client.dart';
import '../../theme/design_colors.dart';
import 'admin_audit_screen.dart';
import 'confirm_action_tile.dart';

/// The owner's fleet-control cockpit (ADR-028 Phase 5 / plan W23).
///
/// Reached from the second AppBar action on `HubDetailScreen` — *not* a
/// bottom-nav tab. Like `HubRolesConfigScreen` it does not pre-probe the
/// token scope: it opens for anyone and surfaces the hub's own 403 when
/// a non-owner token hits an `/v1/admin/*` route.
///
/// Three bands: fleet-wide actions, a per-host card list, and a strip of
/// recent admin audit events (tap through to the full query screen).
/// Every destructive action goes through [ConfirmActionTile] — a plain
/// tap never fires one.
class AdminScreen extends ConsumerStatefulWidget {
  const AdminScreen({super.key});

  @override
  ConsumerState<AdminScreen> createState() => _AdminScreenState();
}

class _AdminScreenState extends ConsumerState<AdminScreen> {
  bool _loading = true;
  String? _error;
  List<Map<String, dynamic>> _hosts = const [];
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
      final audit = await client.adminListAudit(
          actionPrefix: 'host.', limit: 50);
      if (!mounted) return;
      setState(() {
        _hosts = hosts;
        _audit = audit;
        _loading = false;
      });
    } on HubApiError catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = e.status == 403
            ? 'The Admin pane requires an owner-kind token.'
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
  /// outcome, then reloads the fleet so the rows reflect reality.
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
          ? 'Owner token required.'
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
    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Admin',
          style: GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700),
        ),
        actions: [
          IconButton(
            tooltip: 'Audit log',
            icon: const Icon(Icons.receipt_long),
            onPressed: () => Navigator.of(context).push(
              MaterialPageRoute(builder: (_) => const AdminAuditScreen()),
            ),
          ),
          IconButton(
            tooltip: 'Refresh',
            icon: const Icon(Icons.refresh),
            onPressed: _loading ? null : _load,
          ),
        ],
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : _error != null
              ? _ErrorState(message: _error!)
              : RefreshIndicator(
                  onRefresh: _load,
                  child: ListView(
                    padding: const EdgeInsets.fromLTRB(16, 12, 16, 32),
                    children: [
                      _sectionHeader('FLEET'),
                      ..._fleetActions(),
                      const SizedBox(height: 20),
                      _sectionHeader('HOSTS (${_hosts.length})'),
                      if (_hosts.isEmpty)
                        _emptyNote('No hosts registered.')
                      else
                        ..._hosts.map(_hostCard),
                      const SizedBox(height: 20),
                      _auditStrip(),
                    ],
                  ),
                ),
    );
  }

  Widget _sectionHeader(String label) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Text(
        label,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
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

  List<Widget> _fleetActions() {
    final tiles = <Widget>[
      ConfirmActionTile(
        label: 'Update all hosts + hub',
        icon: Icons.system_update_alt,
        destructive: false,
        busy: _busyKey == 'fleet.update',
        enabled: _busyKey == null,
        hint: 'Long-press, then slide right to update the whole fleet.',
        onConfirmed: () => _run('fleet.update', (c) => c.adminFleetUpdate(),
            (r) => _fleetSummary('update', r)),
      ),
      ConfirmActionTile(
        label: 'Restart all hosts',
        icon: Icons.restart_alt,
        busy: _busyKey == 'fleet.restart',
        enabled: _busyKey == null,
        hint: 'Long-press, then slide right to restart every host.',
        onConfirmed: () => _run('fleet.restart',
            (c) => c.adminFleetRestart(), (r) => _fleetSummary('restart', r)),
      ),
      ConfirmActionTile(
        label: 'Shutdown all hosts',
        icon: Icons.power_settings_new,
        busy: _busyKey == 'fleet.shutdown',
        enabled: _busyKey == null,
        hint: 'Long-press, then slide right to shut the fleet down.',
        onConfirmed: () => _run('fleet.shutdown',
            (c) => c.adminFleetShutdown(), (r) => _fleetSummary('shutdown', r)),
      ),
      ConfirmActionTile(
        label: 'Rotate host tokens',
        icon: Icons.key,
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
      ),
      ConfirmActionTile(
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
      ),
    ];
    return [
      for (final t in tiles)
        Padding(padding: const EdgeInsets.only(bottom: 8), child: t),
    ];
  }

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
        borderRadius: BorderRadius.circular(10),
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
            padding: const EdgeInsets.only(left: 18),
            child: Text(
              subtitle.toString(),
              style: GoogleFonts.jetBrainsMono(fontSize: 10, color: muted),
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

  Widget _auditStrip() {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
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
          _emptyNote('No host admin actions recorded yet.')
        else
          ..._audit.take(50).map((e) => _auditRow(e, muted)),
      ],
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
            width: 96,
            child: Text(
              action,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 10,
                color: DesignColors.primary,
              ),
            ),
          ),
          Expanded(
            child: Text(
              summary,
              style: GoogleFonts.jetBrainsMono(fontSize: 10),
            ),
          ),
          Text(
            ts.length >= 16 ? ts.substring(5, 16) : ts,
            style: GoogleFonts.jetBrainsMono(fontSize: 9, color: muted),
          ),
        ],
      ),
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
