import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';

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
    final hubAsync = ref.watch(hubProvider);
    final stats = hubAsync.value?.hubStats;
    final cfg = hubAsync.value?.config;

    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Hub',
          style: GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700),
        ),
        actions: [
          IconButton(
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
                  _IdentityCard(stats: stats, baseUrl: cfg?.baseUrl),
                  const SizedBox(height: 16),
                  _SectionHeader(label: 'MACHINE'),
                  _MachineCard(machine: stats['machine']),
                  const SizedBox(height: 16),
                  _SectionHeader(label: 'DATABASE'),
                  _DatabaseCard(db: stats['db']),
                  const SizedBox(height: 16),
                  _SectionHeader(label: 'LIVE'),
                  _LiveCard(live: stats['live']),
                  if (_hasRelayBlock(stats['live'])) ...[
                    const SizedBox(height: 16),
                    _SectionHeader(label: 'A2A RELAY'),
                    _RelayCard(live: stats['live']),
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
      padding: const EdgeInsets.fromLTRB(14, 10, 14, 10),
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
  const _IdentityCard({required this.stats, this.baseUrl});

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
            _Row(label: 'URL', value: baseUrl!),
          _Row(label: 'Version', value: version),
          if (commit != null && commit.isNotEmpty)
            _Row(label: 'Commit', value: _shortCommit(commit)),
          _Row(label: 'Uptime', value: _humanDuration(uptimeSec)),
        ],
      ),
    );
  }
}

class _MachineCard extends StatelessWidget {
  final Object? machine;
  const _MachineCard({required this.machine});

  @override
  Widget build(BuildContext context) {
    if (machine is! Map) return const _Card(child: Text('—'));
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
            _Row(label: 'Hostname', value: hostname),
          _Row(label: 'OS / Arch', value: '$os / $arch'),
          _Row(label: 'CPU', value: '$cpu cores'),
          _Row(label: 'Memory', value: _bytesToHuman(mem)),
          if (kernel != null && kernel.isNotEmpty)
            _Row(label: 'Kernel', value: kernel),
        ],
      ),
    );
  }
}

class _DatabaseCard extends StatelessWidget {
  final Object? db;
  const _DatabaseCard({required this.db});

  @override
  Widget build(BuildContext context) {
    if (db is! Map) return const _Card(child: Text('—'));
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
          _Row(label: 'Size', value: _bytesToHuman(size)),
          if (wal > 0) _Row(label: 'WAL', value: _bytesToHuman(wal)),
          _Row(label: 'Schema', value: 'v$schema'),
          if (entries.isNotEmpty) ...[
            const SizedBox(height: 8),
            Padding(
              padding: const EdgeInsets.only(bottom: 4, top: 4),
              child: Text('TABLES',
                  style: GoogleFonts.spaceGrotesk(
                      fontSize: 10,
                      fontWeight: FontWeight.w700,
                      color: muted,
                      letterSpacing: 0.8)),
            ),
            for (final e in entries) _TableRow(name: e.key, data: e.value),
          ],
        ],
      ),
    );
  }
}

class _TableRow extends StatelessWidget {
  final String name;
  final Object? data;
  const _TableRow({required this.name, required this.data});

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
          Text('${_humanCount(rows)} rows',
              style:
                  GoogleFonts.jetBrainsMono(fontSize: 11, color: muted)),
          if (bytes > 0) ...[
            const SizedBox(width: 8),
            Text(_bytesToHuman(bytes),
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
  const _LiveCard({required this.live});

  @override
  Widget build(BuildContext context) {
    if (live is! Map) return const _Card(child: Text('—'));
    final l = (live as Map).cast<String, dynamic>();
    return _Card(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _Row(label: 'Active agents', value: _toInt(l['active_agents']).toString()),
          _Row(label: 'Open sessions', value: _toInt(l['open_sessions']).toString()),
          _Row(
              label: 'SSE subscribers',
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
  const _RelayCard({required this.live});

  @override
  Widget build(BuildContext context) {
    if (live is! Map) return const _Card(child: Text('—'));
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
          _Row(label: 'In-flight', value: active.toString()),
          _Row(label: 'Throughput', value: _bytesPerSec(bps)),
          _Row(label: 'Dropped', value: dropped.toString()),
          if (pairs.isNotEmpty) ...[
            const SizedBox(height: 8),
            const Divider(height: 1),
            const SizedBox(height: 8),
            for (final p in pairs)
              _PairRow(
                host: p['host']?.toString() ?? '',
                agent: p['agent']?.toString() ?? '',
                bytesPerSec: _toInt(p['bytes_per_sec']),
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
  const _PairRow({
    required this.host,
    required this.agent,
    required this.bytesPerSec,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 3),
      child: Row(
        children: [
          Expanded(
            child: Text('$host / $agent',
                style: GoogleFonts.jetBrainsMono(
                    fontSize: 12, fontWeight: FontWeight.w600)),
          ),
          Text(_bytesPerSec(bytesPerSec),
              style:
                  GoogleFonts.jetBrainsMono(fontSize: 11, color: muted)),
        ],
      ),
    );
  }
}

String _bytesPerSec(int bps) {
  if (bps <= 0) return '0 B/s';
  return '${_bytesToHuman(bps)}/s';
}

int _toInt(Object? v) {
  if (v is int) return v;
  if (v is num) return v.toInt();
  if (v is String) return int.tryParse(v) ?? 0;
  return 0;
}

String _bytesToHuman(int bytes) {
  if (bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  var v = bytes.toDouble();
  var i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  if (i == 0) return '$bytes ${units[i]}';
  if (v >= 100) return '${v.toStringAsFixed(0)} ${units[i]}';
  if (v >= 10) return '${v.toStringAsFixed(1)} ${units[i]}';
  return '${v.toStringAsFixed(2)} ${units[i]}';
}

String _humanCount(int n) {
  if (n < 1000) return n.toString();
  if (n < 1000000) return '${(n / 1000).toStringAsFixed(1)}k';
  if (n < 1000000000) return '${(n / 1000000).toStringAsFixed(1)}M';
  return '${(n / 1000000000).toStringAsFixed(1)}B';
}

String _humanDuration(int seconds) {
  if (seconds <= 0) return '—';
  final d = seconds ~/ 86400;
  final h = (seconds % 86400) ~/ 3600;
  final m = (seconds % 3600) ~/ 60;
  if (d > 0) return '${d}d ${h}h';
  if (h > 0) return '${h}h ${m}m';
  if (m > 0) return '${m}m';
  return '${seconds}s';
}

String _shortCommit(String c) =>
    c.length <= 12 ? c : c.substring(0, 12);
