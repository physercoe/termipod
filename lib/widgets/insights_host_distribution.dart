import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../providers/hub_provider.dart';
import '../providers/insights_provider.dart';
import '../theme/design_colors.dart';

/// Multi-host distribution drilldown — Phase 2 W5b of
/// insights-phase-2.md. Pure-mobile: reads `hubProvider.hosts` and
/// `hubProvider.agents` from the snapshot cache and folds them into
/// per-host rows scoped to whatever the surrounding view is showing.
///
/// Scope semantics:
///   - **project**: agents whose session has scope_kind=project &
///     scope_id matches → grouped by host_id. The hub doesn't surface
///     project↔agent linkage in the agents list, so we approximate
///     via the active sessions table — close enough at MVP scale.
///     Falls back to "everything" when sessions are missing.
///   - **team / hub**: every agent in the team.
///   - **agent / host / engine**: degenerate (one host or zero
///     useful rows); the section hides itself.
///
/// Renders only when the scope produces at least 2 hosts with
/// agents — a single-host scope under "BY HOST" is noise.
///
/// GPU/CPU split + disk-per-host are part of the plan but require
/// host capabilities the runner doesn't probe yet (no `gpu_count`
/// in capabilities_json). Token spend per host needs a `by_host`
/// rollup the hub doesn't compute. Both deferred — see plan W5b
/// notes.
class InsightsHostDistribution extends ConsumerWidget {
  final InsightsScope scope;
  const InsightsHostDistribution({super.key, required this.scope});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final hub = ref.watch(hubProvider).value;
    if (hub == null) return const SizedBox.shrink();

    // Single-host scopes are degenerate by definition.
    if (scope.kind == InsightsScopeKind.host ||
        scope.kind == InsightsScopeKind.agent) {
      return const SizedBox.shrink();
    }

    final agents = _scopedAgents(scope, hub);
    if (agents.isEmpty) return const SizedBox.shrink();

    final byHost = <String, int>{};
    for (final a in agents) {
      final hostId = (a['host_id'] ?? '').toString();
      if (hostId.isEmpty) continue;
      byHost[hostId] = (byHost[hostId] ?? 0) + 1;
    }
    if (byHost.length < 2) return const SizedBox.shrink();

    final rows = byHost.entries.map((e) {
      final host = hub.hosts.firstWhere(
        (h) => (h['id']?.toString() ?? '') == e.key,
        orElse: () => const <String, dynamic>{},
      );
      final caps = _parseCapabilities(host);
      final hostInfo = (caps['host'] is Map)
          ? (caps['host'] as Map).cast<String, dynamic>()
          : const <String, dynamic>{};
      final cpuCount = hostInfo['cpu_count'];
      final memBytes = hostInfo['mem_bytes'];
      return _HostRow(
        hostId: e.key,
        name: (host['name'] ?? e.key).toString(),
        agentCount: e.value,
        status: (host['status'] ?? '').toString(),
        cpuCount: cpuCount is num ? cpuCount.toInt() : 0,
        memBytes: memBytes is num ? memBytes.toInt() : 0,
      );
    }).toList()
      ..sort((a, b) => b.agentCount.compareTo(a.agentCount));

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const SizedBox(height: 16),
        const _Header(label: 'BY HOST'),
        _HostTable(rows: rows),
      ],
    );
  }

  /// Project / team scopes restrict the agent set; team-or-hub
  /// returns everything; degenerate scopes already returned above.
  /// Engine scope filters by `agents.kind`.
  List<Map<String, dynamic>> _scopedAgents(
      InsightsScope scope, HubState hub) {
    final all = hub.agents;
    switch (scope.kind) {
      case InsightsScopeKind.project:
        // Approximation: the hub agents endpoint doesn't carry
        // project_id directly (the linkage is via active sessions).
        // For W5b's "where are these agents running" use we accept
        // the team-wide set — worst case we over-report, which is
        // still informative for an alpha-stage demo. The strict
        // session-join lives in W5d's lifecycle wedge.
        return all;
      case InsightsScopeKind.engine:
        return all
            .where((a) => (a['kind'] ?? '').toString() == scope.id)
            .toList();
      case InsightsScopeKind.team:
        return all;
      case InsightsScopeKind.host:
      case InsightsScopeKind.agent:
        return const [];
    }
  }

  Map<String, dynamic> _parseCapabilities(Map<String, dynamic> host) {
    final raw = host['capabilities'];
    if (raw is Map) return raw.cast<String, dynamic>();
    if (raw is String && raw.trim().isNotEmpty) {
      try {
        final decoded = jsonDecode(raw);
        if (decoded is Map) return decoded.cast<String, dynamic>();
      } catch (_) {/* malformed payload — show empty */}
    }
    return const {};
  }
}

class _HostRow {
  final String hostId;
  final String name;
  final int agentCount;
  final String status;
  final int cpuCount;
  final int memBytes;
  const _HostRow({
    required this.hostId,
    required this.name,
    required this.agentCount,
    required this.status,
    required this.cpuCount,
    required this.memBytes,
  });
}

class _Header extends StatelessWidget {
  final String label;
  const _Header({required this.label});
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
          letterSpacing: 0.8,
        ),
      ),
    );
  }
}

class _HostTable extends StatelessWidget {
  final List<_HostRow> rows;
  const _HostTable({required this.rows});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final cardBg =
        isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final maxCount =
        rows.fold<int>(0, (m, r) => r.agentCount > m ? r.agentCount : m);

    return Container(
      decoration: BoxDecoration(
        color: cardBg,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: border),
      ),
      padding: const EdgeInsets.fromLTRB(14, 8, 14, 8),
      child: Column(
        children: [
          for (var i = 0; i < rows.length; i++) ...[
            if (i > 0)
              Divider(
                height: 12,
                thickness: 1,
                color: border.withValues(alpha: 0.5),
              ),
            _Row(
              row: rows[i],
              maxCount: maxCount,
              muted: muted,
            ),
          ],
        ],
      ),
    );
  }
}

class _Row extends StatelessWidget {
  final _HostRow row;
  final int maxCount;
  final Color muted;
  const _Row({
    required this.row,
    required this.maxCount,
    required this.muted,
  });

  @override
  Widget build(BuildContext context) {
    final share = maxCount == 0 ? 0.0 : row.agentCount / maxCount;
    final capacity = [
      if (row.cpuCount > 0) '${row.cpuCount} CPU',
      if (row.memBytes > 0) _humanBytes(row.memBytes),
    ].join(' · ');
    final statusColor = _statusColor(row.status);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  row.name,
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              if (statusColor != null) ...[
                Container(
                  width: 6,
                  height: 6,
                  decoration: BoxDecoration(
                    color: statusColor,
                    shape: BoxShape.circle,
                  ),
                ),
                const SizedBox(width: 6),
              ],
              Text(
                '${row.agentCount} agent${row.agentCount == 1 ? '' : 's'}',
                style: GoogleFonts.jetBrainsMono(
                    fontSize: 11, fontWeight: FontWeight.w700),
              ),
            ],
          ),
          const SizedBox(height: 4),
          ClipRRect(
            borderRadius: BorderRadius.circular(2),
            child: LinearProgressIndicator(
              minHeight: 4,
              value: share.clamp(0.0, 1.0),
              backgroundColor: muted.withValues(alpha: 0.15),
              valueColor:
                  const AlwaysStoppedAnimation(DesignColors.primary),
            ),
          ),
          if (capacity.isNotEmpty) ...[
            const SizedBox(height: 3),
            Text(
              capacity,
              style: GoogleFonts.jetBrainsMono(fontSize: 10, color: muted),
            ),
          ],
        ],
      ),
    );
  }
}

Color? _statusColor(String status) {
  switch (status.toLowerCase()) {
    case 'connected':
    case 'online':
      return DesignColors.success;
    case 'offline':
    case 'disconnected':
      return DesignColors.error;
    case 'degraded':
      return DesignColors.warning;
  }
  return null;
}

String _humanBytes(int bytes) {
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
