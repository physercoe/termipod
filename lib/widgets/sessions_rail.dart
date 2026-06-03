// SessionsRail — the left "Sessions" drawer of the Insight workbench (ADR-041
// §4): a *scoped* quick-switcher for the analysed run. It lists the sibling
// runs you'd switch between *in this scope* and retargets the Insight surface
// when one is picked. Scoped, not a global tree — a convenience inside the
// surface, never a second top-level navigator competing with the five-tab IA.
//
// Scope is read off the analysed agent, so the rail matches its entry context
// with no extra plumbing:
//   - a project agent (`project_id` set) → the project's agent roster, the
//     same warm list the Project-detail Agents tab shows;
//   - a team-level steward (no `project_id`) → the team steward roster.
//
// **No latency:** the roster is read synchronously from the warm hub snapshot
// (`hubProvider.value.agents`) — the exact source the Project-detail Agents tab
// reads — so the list paints on open instead of awaiting a fetch. (Only the
// rare cold case — an archived agent absent from the snapshot — falls back to a
// one-shot fetch to learn its project.)
//
// Phone-first: a self-contained overlay (tap-to-dismiss scrim + a left-aligned
// panel), mirroring the right Navigator drawer. The host (`SessionAnalysisView`)
// renders it when open, holds the active target in state, and keeps the left
// and right drawers mutually exclusive. Picking a row retargets but leaves the
// rail open (ADR-041 §4) — the user closes it explicitly.
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../providers/hub_provider.dart';
import '../providers/sessions_provider.dart';
import '../services/steward_handle.dart';
import '../theme/design_colors.dart';

/// Retarget callback: `(agentId, sessionId, live)` for the picked run.
typedef RetargetCallback = void Function(
    String agentId, String sessionId, bool live);

class SessionsRail extends ConsumerStatefulWidget {
  /// The agent currently being analysed — the scope anchor and the active-row
  /// highlight (the rail switches *agents*, so it highlights the active agent).
  final String agentId;
  final RetargetCallback onSelect;
  final VoidCallback onClose;

  const SessionsRail({
    super.key,
    required this.agentId,
    required this.onSelect,
    required this.onClose,
  });

  @override
  ConsumerState<SessionsRail> createState() => _SessionsRailState();
}

class _SessionsRailState extends ConsumerState<SessionsRail> {
  // The agent row whose session is being resolved on tap (a tiny inline spinner).
  String? _resolvingAgentId;
  // Project id learned from a one-shot fetch when the analysed agent isn't in
  // the warm hub snapshot (the archived-agent case). Null until/unless fetched.
  String? _coldProjectId;
  bool _coldTried = false;
  bool _coldLoading = false;

  static bool _agentLive(String status) =>
      status == 'running' || status == 'idle' || status == 'pending';

  /// The analysed agent's row from the warm hub snapshot, or null if it isn't
  /// in it (archived).
  Map<String, dynamic>? _meIn(List<Map<String, dynamic>> agents) {
    for (final a in agents) {
      if ((a['id'] ?? '').toString() == widget.agentId) return a;
    }
    return null;
  }

  /// Cold fallback: the analysed agent isn't in the warm snapshot, so fetch it
  /// once to learn its project and render the sibling roster from warm state.
  Future<void> _resolveCold() async {
    _coldTried = true;
    _coldLoading = true;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      if (mounted) setState(() => _coldLoading = false);
      return;
    }
    try {
      final a = (await client.getAgentCached(widget.agentId)).body;
      final pid = (a['project_id'] ?? '').toString();
      if (mounted) {
        setState(() {
          if (pid.isNotEmpty) _coldProjectId = pid;
          _coldLoading = false;
        });
      }
    } catch (_) {
      // Leave _coldProjectId null — the body shows the steward fallback.
      if (mounted) setState(() => _coldLoading = false);
    }
  }

  /// Resolve the picked agent's session and retarget. Prefer the warm sessions
  /// snapshot (instant); fall back to the newest event's session_id (inline
  /// spinner). The rail stays open afterwards — the user closes it.
  Future<void> _pickAgent(Map<String, dynamic> agent) async {
    final id = (agent['id'] ?? '').toString();
    if (id.isEmpty || id == widget.agentId) return;
    final status = (agent['status'] ?? '').toString();
    String sid = _warmSessionFor(id);
    if (sid.isEmpty) {
      final client = ref.read(hubProvider.notifier).client;
      if (client != null) {
        setState(() => _resolvingAgentId = id);
        try {
          final ev = await client.listAgentEvents(id, tail: true, limit: 1);
          if (ev.isNotEmpty) sid = (ev.first['session_id'] ?? '').toString();
        } catch (_) {
          // Leave sid empty — InsightTranscript falls back to the loaded window.
        }
        if (!mounted) return;
        setState(() => _resolvingAgentId = null);
      }
    }
    widget.onSelect(id, sid, _agentLive(status));
  }

  /// The session an agent currently fronts, from the warm sessions snapshot.
  String _warmSessionFor(String agentId) {
    final s = ref.read(sessionsProvider).value;
    if (s == null) return '';
    for (final sess in [...s.active, ...s.previous]) {
      if ((sess['current_agent_id'] ?? '').toString() == agentId) {
        return (sess['id'] ?? '').toString();
      }
    }
    return '';
  }

  String _projectName(List<Map<String, dynamic>> projects, String pid) {
    for (final p in projects) {
      if ((p['id'] ?? '').toString() == pid) {
        final n = (p['name'] ?? p['title'] ?? '').toString();
        return n.isNotEmpty ? n : pid;
      }
    }
    return pid;
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final panelBg =
        isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final fg =
        isDark ? DesignColors.textPrimary : DesignColors.textPrimaryLight;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final border = isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final width = math.min(MediaQuery.of(context).size.width * 0.84, 340.0);
    return Positioned.fill(
      child: Stack(
        children: [
          GestureDetector(
            onTap: widget.onClose,
            child: Container(color: Colors.black.withValues(alpha: 0.45)),
          ),
          Align(
            alignment: Alignment.centerLeft,
            child: Material(
              color: panelBg,
              elevation: 12,
              child: SizedBox(
                width: width,
                height: double.infinity,
                child: SafeArea(
                  right: false,
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      Padding(
                        padding: const EdgeInsets.fromLTRB(14, 10, 6, 4),
                        child: Row(
                          children: [
                            Icon(Icons.account_tree_outlined,
                                size: 18, color: muted),
                            const SizedBox(width: 8),
                            Text(
                              'Sessions',
                              style: GoogleFonts.spaceGrotesk(
                                fontSize: 14,
                                fontWeight: FontWeight.w700,
                                color: fg,
                              ),
                            ),
                            const Spacer(),
                            IconButton(
                              tooltip: 'Close',
                              visualDensity: VisualDensity.compact,
                              icon: Icon(Icons.close, size: 18, color: muted),
                              onPressed: widget.onClose,
                            ),
                          ],
                        ),
                      ),
                      Divider(height: 1, color: border),
                      Expanded(child: _body(fg, muted)),
                    ],
                  ),
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _body(Color fg, Color muted) {
    final hub = ref.watch(hubProvider).value;
    final all = hub?.agents ?? const <Map<String, dynamic>>[];
    final projects = hub?.projects ?? const <Map<String, dynamic>>[];
    final me = _meIn(all);

    // Scope from the analysed agent. project_id → the project roster; otherwise
    // a steward → the team steward roster.
    final projectId =
        (me?['project_id'] ?? _coldProjectId ?? '').toString();

    String header;
    List<Map<String, dynamic>> rows;
    if (projectId.isNotEmpty) {
      header = 'Agents · ${_projectName(projects, projectId)}';
      rows = [
        for (final a in all)
          if ((a['project_id'] ?? '').toString() == projectId) a,
      ];
    } else if (me == null && (!_coldTried || _coldLoading)) {
      // Not in the warm snapshot yet (archived) — learn its project once, then
      // re-render. Show a spinner rather than a misleading roster while we do.
      if (!_coldTried) {
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (mounted && !_coldTried) _resolveCold();
        });
      }
      return const Center(child: CircularProgressIndicator());
    } else {
      // A team-level steward (or an unresolved cold agent): the steward roster.
      header = 'Stewards';
      rows = [for (final a in all) if (isStewardAgent(a)) a];
    }

    // Live runs first, then by handle — the freshest switch targets on top.
    rows.sort((x, y) {
      final lx = _agentLive((x['status'] ?? '').toString());
      final ly = _agentLive((y['status'] ?? '').toString());
      if (lx != ly) return lx ? -1 : 1;
      return (x['handle'] ?? '')
          .toString()
          .compareTo((y['handle'] ?? '').toString());
    });

    return ListView(
      padding: const EdgeInsets.only(bottom: 12),
      children: [
        _groupHeader(header, muted),
        if (rows.isEmpty)
          _emptyRow('No other runs in scope.', muted)
        else
          for (final a in rows) _agentRow(a, fg, muted),
      ],
    );
  }

  Widget _groupHeader(String label, Color muted) => Padding(
        padding: const EdgeInsets.fromLTRB(14, 14, 14, 6),
        child: Text(
          label.toUpperCase(),
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 10,
            fontWeight: FontWeight.w700,
            letterSpacing: 0.6,
            color: muted,
          ),
        ),
      );

  Widget _emptyRow(String message, Color muted) => Padding(
        padding: const EdgeInsets.fromLTRB(14, 4, 14, 8),
        child: Text(message,
            style: GoogleFonts.spaceGrotesk(fontSize: 12, color: muted)),
      );

  Widget _agentRow(Map<String, dynamic> a, Color fg, Color muted) {
    final id = (a['id'] ?? '').toString();
    final handle = (a['handle'] ?? id).toString();
    final status = (a['status'] ?? '').toString();
    final active = id == widget.agentId;
    final live = _agentLive(status);
    final dot = live ? DesignColors.success : muted;
    return Material(
      color: active
          ? DesignColors.primary.withValues(alpha: 0.10)
          : Colors.transparent,
      child: InkWell(
        onTap: active ? null : () => _pickAgent(a),
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 9),
          child: Row(
            children: [
              Container(
                width: 8,
                height: 8,
                decoration: BoxDecoration(color: dot, shape: BoxShape.circle),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Text(
                  handle,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 13,
                    fontWeight: active ? FontWeight.w700 : FontWeight.w500,
                    color: fg,
                  ),
                ),
              ),
              if (_resolvingAgentId == id)
                const SizedBox(
                  width: 14,
                  height: 14,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              else
                Text(
                  status,
                  style:
                      GoogleFonts.jetBrainsMono(fontSize: 10, color: muted),
                ),
            ],
          ),
        ),
      ),
    );
  }
}
