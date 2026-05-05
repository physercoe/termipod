import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../providers/hub_provider.dart';
import '../services/hub/open_steward_session.dart';
import '../theme/design_colors.dart';

/// Project Detail steward strip (W3) — replaces the legacy
/// `_StewardChip` with the seven-state pill from D1's discussion §6.6
/// plus the handoff indicator from §B.6. Polls
/// `GET /v1/teams/{team}/projects/{project_id}/steward/state` every 5s
/// while mounted; backs off when the screen is hidden via lifecycle
/// observation in higher-level widgets (we just stop the timer in
/// dispose).
///
/// Each state surfaces its own primary affordance:
/// `not-spawned` → Start, `idle` → Direct, `active-session` → Resume,
/// `working` → View, `worker-dispatched` → Workers,
/// `awaiting-director` → Respond, `error` → Inspect.
class StewardStrip extends ConsumerStatefulWidget {
  final String projectId;
  final String stewardAgentId;

  const StewardStrip({
    super.key,
    required this.projectId,
    required this.stewardAgentId,
  });

  @override
  ConsumerState<StewardStrip> createState() => _StewardStripState();
}

class _StewardStripState extends ConsumerState<StewardStrip> {
  Map<String, dynamic>? _state;
  Timer? _poll;

  @override
  void initState() {
    super.initState();
    _refresh();
    _poll = Timer.periodic(
        const Duration(seconds: 5), (_) => _refresh());
  }

  @override
  void dispose() {
    _poll?.cancel();
    super.dispose();
  }

  Future<void> _refresh() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null || widget.projectId.isEmpty) return;
    try {
      final out = await client.getStewardState(widget.projectId);
      if (!mounted) return;
      setState(() => _state = out);
    } catch (_) {
      // Network blips don't blank the strip — we keep the last
      // known state so the demo stays visually stable.
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final state = (_state?['state'] ?? '').toString();
    final handoff = _state?['handoff'] as Map?;
    final stateInfo = _stateInfoFor(state);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
      decoration: BoxDecoration(
        color: stateInfo.color.withValues(alpha: 0.08),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(
          color: stateInfo.color.withValues(alpha: 0.4),
        ),
      ),
      child: Row(
        children: [
          Container(
            width: 8,
            height: 8,
            decoration: BoxDecoration(
              shape: BoxShape.circle,
              color: stateInfo.color,
            ),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'Steward · ${stateInfo.label}',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    fontWeight: FontWeight.w700,
                    color: isDark
                        ? DesignColors.textPrimary
                        : DesignColors.textPrimaryLight,
                  ),
                ),
                if (handoff != null) ...[
                  const SizedBox(height: 2),
                  Row(
                    children: [
                      const Icon(Icons.swap_horiz, size: 12,
                          color: DesignColors.warning),
                      const SizedBox(width: 4),
                      Flexible(
                        child: Text(
                          'asking general steward${_handoffPurpose(handoff)}',
                          overflow: TextOverflow.ellipsis,
                          style: GoogleFonts.jetBrainsMono(
                            fontSize: 10,
                            color: DesignColors.warning,
                          ),
                        ),
                      ),
                    ],
                  ),
                ],
              ],
            ),
          ),
          if (stateInfo.affordance != null)
            TextButton(
              onPressed: () => _onAffordanceTap(stateInfo.affordance!),
              style: TextButton.styleFrom(
                padding: const EdgeInsets.symmetric(
                    horizontal: 12, vertical: 6),
                minimumSize: Size.zero,
                tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                foregroundColor: stateInfo.color,
              ),
              child: Text(
                stateInfo.affordance!,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 12,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ),
        ],
      ),
    );
  }

  String _handoffPurpose(Map handoff) {
    final p = (handoff['purpose'] ?? '').toString();
    return p.isEmpty ? '...' : ' · $p';
  }

  void _onAffordanceTap(String affordance) {
    if (widget.stewardAgentId.isEmpty || widget.projectId.isEmpty) return;
    // Every affordance routes to the project-scoped steward session for
    // MVP; later wedges can deeplink "Workers" → agents tab and
    // "Inspect" → agent detail. ADR-009 D7 keeps the session opener
    // scope-aware so this is a single call.
    openStewardSession(
      context,
      ref,
      scopeKind: 'project',
      scopeId: widget.projectId,
    );
  }

  /// Maps the seven canonical states (plus the not-yet-seen empty
  /// case) to a human label, status colour, and a primary verb.
  static _StateInfo _stateInfoFor(String state) {
    switch (state) {
      case 'not-spawned':
        return const _StateInfo('not started', DesignColors.textMuted, 'Start');
      case 'idle':
        return const _StateInfo('idle', DesignColors.terminalCyan, 'Direct');
      case 'active-session':
        return const _StateInfo('in session',
            DesignColors.primary, 'Resume');
      case 'working':
        return const _StateInfo('working',
            DesignColors.terminalGreen, 'View');
      case 'worker-dispatched':
        return const _StateInfo('workers running',
            DesignColors.terminalGreen, 'Workers');
      case 'awaiting-director':
        return const _StateInfo('awaiting you',
            DesignColors.warning, 'Respond');
      case 'error':
        return const _StateInfo('paused',
            DesignColors.terminalRed, 'Inspect');
      case 'handoff_in_progress':
        return const _StateInfo('handoff',
            DesignColors.warning, 'View');
      default:
        return const _StateInfo('…', DesignColors.textMuted, null);
    }
  }
}

class _StateInfo {
  final String label;
  final Color color;
  final String? affordance;
  const _StateInfo(this.label, this.color, this.affordance);
}
