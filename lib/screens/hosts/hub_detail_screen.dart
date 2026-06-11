import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:termipod/l10n/app_localizations.dart';

import '../../providers/hub_provider.dart';
import '../../providers/vocab_provider.dart';
import '../../services/vocab/vocab_axis.dart';
import '../../theme/design_colors.dart';
import '../../theme/tokens.dart';
import '../admin/admin_screen.dart';
import 'hub_config_screen.dart';

/// Fullscreen breakdown of `/v1/hub/stats`. Sibling of the per-hostrunner
/// detail sheet, but reads from `state.hubStats` instead of a hub_host
/// row. ADR-022 D2 / insights-phase-1 W1.
///
/// Sections:
///   1. Machine — OS / arch / CPU / RAM / kernel
///   2. Database — total size / WAL / schema version / per-table rows + bytes
///   3. Live — active agents / open sessions / SSE subscribers
///   4. Relay — A2A throughput aggregate + per-pair list. Only rendered
///      when the hub has shipped any of the `live.a2a_relay_*` keys.
///      Quiet hubs ship `a2a_relay_active` + `a2a_dropped_total` only,
///      so the section still appears (with zero rows) — confirms the
///      relay loop is alive even before any traffic flows.
///
/// Pull-to-refresh re-runs `refreshHubStats()`.
class HubDetailScreen extends ConsumerWidget {
  const HubDetailScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context)!;
    final vocab = ref.watch(vocabularyProvider);
    final agent = vocab.term(VocabAxis.roleAgent);
    final hubAsync = ref.watch(hubProvider);
    final stats = hubAsync.value?.hubStats;
    final cfg = hubAsync.value?.config;

    return Scaffold(
      appBar: AppBar(
        title: Text(
          l10n.tabHub,
          style: GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700),
        ),
        actions: [
          // Hub-wide governance config (owner-only). The screen
          // itself handles the 403 surfacing — non-owners see a
          // clear "owner token required" message. Living on the
          // AppBar (vs the overflow) keeps it discoverable but the
          // owner audience small. ADR-016 + Q1a 2026-05-13.
          IconButton(
            tooltip: l10n.hubConfigOwnerTooltip,
            icon: const Icon(Icons.tune),
            onPressed: () => Navigator.of(context).push(
              MaterialPageRoute(
                builder: (_) => const HubRolesConfigScreen(),
              ),
            ),
          ),
          // Fleet ops cockpit (owner-only) — ADR-028 Phase 5. Sits
          // beside Hub config: same owner audience, same 403-self-
          // surfacing idiom, so the Admin pane is one AppBar action
          // here rather than a sixth bottom-nav tab.
          IconButton(
            tooltip: l10n.adminOwnerTooltip,
            icon: const Icon(Icons.admin_panel_settings_outlined),
            onPressed: () => Navigator.of(context).push(
              MaterialPageRoute(builder: (_) => const AdminScreen()),
            ),
          ),
          IconButton(
            tooltip: l10n.buttonRefresh,
            icon: const Icon(Icons.refresh),
            onPressed: () =>
                ref.read(hubProvider.notifier).refreshHubStats(),
          ),
        ],
      ),
      body: RefreshIndicator(
        onRefresh: () => ref.read(hubProvider.notifier).refreshHubStats(),
        child: stats == null
            ? const _LoadingState()
            : ListView(
                padding: const EdgeInsets.fromLTRB(16, 12, 16, 32),
                children: [
                  _IdentityCard(
                      stats: stats, baseUrl: cfg?.baseUrl, l10n: l10n),
                  const SizedBox(height: 16),
                  _SectionHeader(label: l10n.sectionMachine),
                  _MachineCard(machine: stats['machine'], l10n: l10n),
                  const SizedBox(height: 16),
                  _SectionHeader(label: l10n.sectionDatabase),
                  _DatabaseCard(db: stats['db'], l10n: l10n),
                  const SizedBox(height: 16),
                  _SectionHeader(label: l10n.sectionLive),
                  _LiveCard(
                      live: stats['live'],
                      l10n: l10n,
                      agent: agent.pluralLower),
                  if (_hasRelayBlock(stats['live'])) ...[
                    const SizedBox(height: 16),
                    _SectionHeader(label: l10n.sectionA2aRelay),
                    _RelayCard(live: stats['live'], l10n: l10n),
                  ],
                ],
              ),
      ),
    );
  }
}

class _LoadingState extends StatelessWidget {
  const _LoadingState();
  @override
  Widget build(BuildContext context) {
    return ListView(
      // Need a scrollable child so pull-to-refresh works even when
      // stats haven't landed yet — otherwise the gesture is dead.
      children: const [
        SizedBox(height: 200),
        Center(child: CircularProgressIndicator()),
      ],
    );
  }
}

class _SectionHeader extends StatelessWidget {
  final String label;
  const _SectionHeader({required this.label});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Padding(
      padding: const EdgeInsets.fromLTRB(4, 0, 4, 8),
      child: Text(
        label,
        style: GoogleFonts.spaceGrotesk(
            fontSize: 11,
            fontWeight: FontWeight.w700,
            color: muted,
            letterSpacing: 0.8),
      ),
    );
  }
}

class _Card extends StatelessWidget {
  final Widget child;
  const _Card({required this.child});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    return Container(
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: border),
      ),
      padding: const EdgeInsets.fromLTRB(Spacing.s12, Spacing.s8, Spacing.s12, Spacing.s8),
      child: child,
    );
  }
}

class _Row extends StatelessWidget {
  final String label;
  final String value;
  const _Row({required this.label, required this.value});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 110,
            child: Text(label,
                style: GoogleFonts.jetBrainsMono(
                    fontSize: 11, color: muted)),
          ),
          Expanded(
            child: Text(value,
                style: GoogleFonts.jetBrainsMono(
                    fontSize: 12, fontWeight: FontWeight.w600)),
          ),
        ],
      ),
    );
  }
}

class _IdentityCard extends StatelessWidget {
  final Map<String, dynamic> stats;
  final String? baseUrl;
  final AppLocalizations l10n;
  const _IdentityCard(
      {required this.stats, this.baseUrl, required this.l10n});

  @override
  Widget build(BuildContext context) {
    final version = stats['version']?.toString() ?? '';
    final commit = stats['commit']?.toString();
    final uptimeSec = _toInt(stats['uptime_seconds']);
    return _Card(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (baseUrl != null && baseUrl!.isNotEmpty)
            _Row(label: l10n.fieldUrl, value: baseUrl!),
          _Row(label: l10n.fieldVersion, value: version),
          if (commit != null && commit.isNotEmpty)
            _Row(label: l10n.fieldCommit, value: _shortCommit(commit)),
          _Row(label: l10n.fieldUptime, value: _humanDuration(l10n, uptimeSec)),
        ],
      ),
    );
  }
}

class _MachineCard extends StatelessWidget {
  final Object? machine;
  final AppLocalizations l10n;
  const _MachineCard({required this.machine, required this.l10n});

  @override
  Widget build(BuildContext context) {
    if (machine is! Map) return _Card(child: Text(l10n.unavailable));
    final m = (machine as Map).cast<String, dynamic>();
    final hostname = m['hostname']?.toString();
    final os = m['os']?.toString() ?? '';
    final arch = m['arch']?.toString() ?? '';
    final cpu = _toInt(m['cpu_count']);
    final mem = _toInt(m['mem_bytes']);
    final kernel = m['kernel']?.toString();
    return _Card(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (hostname != null && hostname.isNotEmpty)
            _Row(label: l10n.fieldHostname, value: hostname),
          _Row(label: l10n.fieldOsArch, value: '$os / $arch'),
          _Row(label: l10n.fieldCpu, value: l10n.coreCount(cpu)),
          _Row(label: l10n.fieldMemory, value: _bytesToHuman(l10n, mem)),
          if (kernel != null && kernel.isNotEmpty)
            _Row(label: l10n.fieldKernel, value: kernel),
        ],
      ),
    );
  }
}

class _DatabaseCard extends StatelessWidget {
  final Object? db;
  final AppLocalizations l10n;
  const _DatabaseCard({required this.db, required this.l10n});

  @override
  Widget build(BuildContext context) {
    if (db is! Map) return _Card(child: Text(l10n.unavailable));
    final d = (db as Map).cast<String, dynamic>();
    final size = _toInt(d['size_bytes']);
    final wal = _toInt(d['wal_bytes']);
    final schema = _toInt(d['schema_version']);
    final tablesRaw = d['tables'];
    final tables = tablesRaw is Map
        ? tablesRaw.cast<String, dynamic>()
        : <String, dynamic>{};
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final entries = tables.entries.toList()
      ..sort((a, b) =>
          _toInt((b.value as Map?)?['rows']).compareTo(_toInt((a.value as Map?)?['rows'])));
    return _Card(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _Row(label: l10n.fieldSize, value: _bytesToHuman(l10n, size)),
          if (wal > 0)
            _Row(label: l10n.fieldWal, value: _bytesToHuman(l10n, wal)),
          _Row(label: l10n.fieldSchema, value: 'v$schema'),
          if (entries.isNotEmpty) ...[
            const SizedBox(height: 8),
            Padding(
              padding: const EdgeInsets.only(bottom: 4, top: 4),
              child: Text(l10n.sectionTables,
                  style: GoogleFonts.spaceGrotesk(
                      fontSize: FontSizes.label,
                      fontWeight: FontWeight.w700,
                      color: muted,
                      letterSpacing: 0.8)),
            ),
            for (final e in entries)
              _TableRow(name: e.key, data: e.value, l10n: l10n),
          ],
        ],
      ),
    );
  }
}

class _TableRow extends StatelessWidget {
  final String name;
  final Object? data;
  final AppLocalizations l10n;
  const _TableRow(
      {required this.name, required this.data, required this.l10n});

  @override
  Widget build(BuildContext context) {
    final m = data is Map
        ? (data as Map).cast<String, dynamic>()
        : <String, dynamic>{};
    final rows = _toInt(m['rows']);
    final bytes = _toInt(m['bytes']);
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Row(
        children: [
          Expanded(
            child: Text(name,
                style: GoogleFonts.jetBrainsMono(
                    fontSize: 12, fontWeight: FontWeight.w600)),
          ),
          Text(l10n.tableRows(_humanCount(rows)),
              style:
                  GoogleFonts.jetBrainsMono(fontSize: 11, color: muted)),
          if (bytes > 0) ...[
            const SizedBox(width: 8),
            Text(_bytesToHuman(l10n, bytes),
                style:
                    GoogleFonts.jetBrainsMono(fontSize: 11, color: muted)),
          ],
        ],
      ),
    );
  }
}

class _LiveCard extends StatelessWidget {
  final Object? live;
  final AppLocalizations l10n;
  final String agent;
  const _LiveCard(
      {required this.live, required this.l10n, required this.agent});

  @override
  Widget build(BuildContext context) {
    if (live is! Map) return _Card(child: Text(l10n.unavailable));
    final l = (live as Map).cast<String, dynamic>();
    return _Card(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _Row(
              label: l10n.activeAgents(agent),
              value: _toInt(l['active_agents']).toString()),
          _Row(
              label: l10n.openSessions,
              value: _toInt(l['open_sessions']).toString()),
          _Row(
              label: l10n.sseSubscribers,
              value: _toInt(l['sse_subscribers']).toString()),
        ],
      ),
    );
  }
}

bool _hasRelayBlock(Object? live) {
  if (live is! Map) return false;
  final l = (live).cast<String, dynamic>();
  return l.containsKey('a2a_relay_active') ||
      l.containsKey('a2a_dropped_total') ||
      l.containsKey('a2a_bytes_per_sec') ||
      l.containsKey('a2a_relay_pairs');
}

/// Surfaces the W3 throughput block from `/v1/hub/stats.live`. Aggregate
/// rows always render (active gauge + dropped counter); the per-pair
/// list only appears when at least one destination is currently active.
/// Pair labels are `host/agent` because the relay path is token-less and
/// can't observe the source agent — see relay_metrics.go.
class _RelayCard extends StatelessWidget {
  final Object? live;
  final AppLocalizations l10n;
  const _RelayCard({required this.live, required this.l10n});

  @override
  Widget build(BuildContext context) {
    if (live is! Map) return _Card(child: Text(l10n.unavailable));
    final l = (live as Map).cast<String, dynamic>();
    final active = _toInt(l['a2a_relay_active']);
    final dropped = _toInt(l['a2a_dropped_total']);
    final bps = _toInt(l['a2a_bytes_per_sec']);
    final pairsRaw = l['a2a_relay_pairs'];
    final pairs = pairsRaw is List
        ? pairsRaw
            .whereType<Map>()
            .map((p) => p.cast<String, dynamic>())
            .toList()
        : const <Map<String, dynamic>>[];

    return _Card(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _Row(label: l10n.relayInFlight, value: active.toString()),
          _Row(label: l10n.relayThroughput, value: _bytesPerSec(l10n, bps)),
          _Row(label: l10n.relayDropped, value: dropped.toString()),
          if (pairs.isNotEmpty) ...[
            const SizedBox(height: 8),
            const Divider(height: 1),
            const SizedBox(height: 8),
            for (final p in pairs)
              _PairRow(
                host: p['host']?.toString() ?? '',
                agent: p['agent']?.toString() ?? '',
                bytesPerSec: _toInt(p['bytes_per_sec']),
                l10n: l10n,
              ),
          ],
        ],
      ),
    );
  }
}

class _PairRow extends StatelessWidget {
  final String host;
  final String agent;
  final int bytesPerSec;
  final AppLocalizations l10n;
  const _PairRow({
    required this.host,
    required this.agent,
    required this.bytesPerSec,
    required this.l10n,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: Spacing.s4),
      child: Row(
        children: [
          Expanded(
            child: Text('$host / $agent',
                style: GoogleFonts.jetBrainsMono(
                    fontSize: 12, fontWeight: FontWeight.w600)),
          ),
          Text(_bytesPerSec(l10n, bytesPerSec),
              style:
                  GoogleFonts.jetBrainsMono(fontSize: 11, color: muted)),
        ],
      ),
    );
  }
}

String _bytesPerSec(AppLocalizations l10n, int bps) {
  if (bps <= 0) return l10n.zeroBytesPerSecond;
  return l10n.bytesPerSecond(_bytesToHuman(l10n, bps));
}

int _toInt(Object? v) {
  if (v is int) return v;
  if (v is num) return v.toInt();
  if (v is String) return int.tryParse(v) ?? 0;
  return 0;
}

String _bytesToHuman(AppLocalizations l10n, int bytes) {
  if (bytes <= 0) return l10n.zeroBytes;
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  var v = bytes.toDouble();
  var i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  if (i == 0) return l10n.bytesValue('$bytes', units[i]);
  if (v >= 100) return l10n.bytesValue(v.toStringAsFixed(0), units[i]);
  if (v >= 10) return l10n.bytesValue(v.toStringAsFixed(1), units[i]);
  return l10n.bytesValue(v.toStringAsFixed(2), units[i]);
}

String _humanCount(int n) {
  if (n < 1000) return n.toString();
  if (n < 1000000) return '${(n / 1000).toStringAsFixed(1)}k';
  if (n < 1000000000) return '${(n / 1000000).toStringAsFixed(1)}M';
  return '${(n / 1000000000).toStringAsFixed(1)}B';
}

String _humanDuration(AppLocalizations l10n, int seconds) {
  if (seconds <= 0) return l10n.unavailable;
  final d = seconds ~/ 86400;
  final h = (seconds % 86400) ~/ 3600;
  final m = (seconds % 3600) ~/ 60;
  if (d > 0) return l10n.durationDaysHours(d, h);
  if (h > 0) return l10n.durationHoursMinutes(h, m);
  if (m > 0) return l10n.durationMinutes(m);
  return l10n.durationSeconds(seconds);
}

String _shortCommit(String c) =>
    c.length <= 12 ? c : c.substring(0, 12);
