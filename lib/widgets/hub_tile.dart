import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../theme/design_colors.dart';

/// Hub group tile rendered atop the Hosts tab — the visual sibling of
/// the per-hostrunner row but backed by `/v1/hub/stats` instead of
/// `hosts.capabilities_json`. ADR-022 D2 / insights-phase-1 W1: the hub
/// box never appears in the multi-tenant `hosts` table; the tile uses
/// matching shape + a different data source so users don't have to learn
/// a new UI primitive.
///
/// Tap opens the Hub Detail screen with the full machine + DB + live
/// breakdown. Long-press (TODO post-W3) will deep-link the relay
/// section. No Enter-pane action — this row is stat-focused only.
class HubTile extends StatelessWidget {
  /// Display label, typically the active hub URL or its hostname.
  final String name;

  /// Hub stats payload (`/v1/hub/stats` response). Null while the first
  /// fetch is in flight; the tile still renders with a "loading" subtitle
  /// so the section doesn't blink in/out.
  final Map<String, dynamic>? stats;

  /// Snapshot is older than the cache-first staleness window. Surfaces
  /// as a small "stale" pill on the right; cache-first per ADR-006.
  final bool stale;

  final VoidCallback onTap;

  const HubTile({
    super.key,
    required this.name,
    required this.stats,
    required this.onTap,
    this.stale = false,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final cardBg =
        isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;

    final version = stats?['version']?.toString();
    final db = stats?['db'];
    final live = stats?['live'];
    final dbSize = (db is Map) ? _bytesToHuman(_int64(db['size_bytes'])) : null;
    final activeAgents =
        (live is Map) ? _int64(live['active_agents']).toString() : null;

    final subtitleParts = <String>[];
    if (dbSize != null) subtitleParts.add(dbSize);
    if (activeAgents != null) subtitleParts.add('$activeAgents agents now');
    final subtitle =
        subtitleParts.isEmpty ? 'loading…' : subtitleParts.join(' · ');

    return Material(
      color: cardBg,
      borderRadius: BorderRadius.circular(12),
      child: InkWell(
        borderRadius: BorderRadius.circular(12),
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(14, 12, 10, 12),
          child: Row(
            children: [
              Icon(Icons.hub_outlined,
                  size: 22, color: DesignColors.primary),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      children: [
                        Expanded(
                          child: Text(name,
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                              style: GoogleFonts.spaceGrotesk(
                                  fontSize: 15,
                                  fontWeight: FontWeight.w700)),
                        ),
                        if (version != null)
                          Text(version,
                              style: GoogleFonts.jetBrainsMono(
                                  fontSize: 11,
                                  color: muted)),
                      ],
                    ),
                    const SizedBox(height: 2),
                    Row(
                      children: [
                        Text(subtitle,
                            style: GoogleFonts.jetBrainsMono(
                                fontSize: 11, color: muted)),
                        if (stale) ...[
                          const SizedBox(width: 6),
                          _StalePill(),
                        ],
                      ],
                    ),
                  ],
                ),
              ),
              Icon(Icons.chevron_right, size: 18, color: muted),
            ],
          ),
        ),
      ),
    );
  }
}

class _StalePill extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    final color = Theme.of(context).hintColor;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Text(
        'stale',
        style: GoogleFonts.jetBrainsMono(
            fontSize: 9, fontWeight: FontWeight.w700, color: color),
      ),
    );
  }
}

int _int64(Object? v) {
  if (v is int) return v;
  if (v is num) return v.toInt();
  if (v is String) return int.tryParse(v) ?? 0;
  return 0;
}

/// Format a byte count as a short human label: 142 MB, 12 GB, 4.1 KB.
/// Mirrors what the Hub Detail screen uses, deduped here so the tile
/// doesn't pull in the screen file just for a helper.
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
