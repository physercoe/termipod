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
import '../services/hub/agent_status.dart';
import '../services/hub/session_display.dart';
import '../services/steward_handle.dart';
import '../theme/design_colors.dart';
import 'agent_category_style.dart';

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
  // The project roster INCLUDING terminated runs (a clean finish is reviewable),
  // fetched fresh. The warm hub snapshot is live-only (the hub's default agents
  // list excludes terminated/failed/crashed/archived), but the rail is a
  // *switch-to-analyse* roster. We DON'T pull archived — a "deleted" (archived)
  // agent shouldn't reappear here. Null until the fetch lands — the warm
  // snapshot paints the live roster instantly meanwhile (no open latency).
  List<Map<String, dynamic>>? _fullRoster;
  bool _fullTried = false;

  static bool _agentLive(String status) =>
      status == 'running' || status == 'idle' || status == 'pending';

  /// The analysed agent's row from [agents], or null if absent.
  Map<String, dynamic>? _meIn(List<Map<String, dynamic>> agents) {
    for (final a in agents) {
      if ((a['id'] ?? '').toString() == widget.agentId) return a;
    }
    return null;
  }

  /// Fetch the roster including terminated runs (the warm snapshot omits them).
  /// Fetched FRESH (not the read-through cache) so a pull-to-refresh reflects
  /// deletions. Doubles as the resolver for a terminated analysed agent: once
  /// this lands, `_meIn` finds it. Archived agents are deliberately NOT pulled.
  Future<void> _fetchFull() async {
    _fullTried = true;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final out = await client.listAgents(includeTerminated: true);
      if (mounted) setState(() => _fullRoster = out);
    } catch (_) {
      // Non-fatal — the warm snapshot's live roster still shows.
    }
  }

  /// Pull-to-refresh: refresh the hub snapshot + sessions, then re-fetch the
  /// terminated roster (so a freshly deleted/archived agent drops off).
  Future<void> _onRefresh() async {
    await ref.read(hubProvider.notifier).refreshAll();
    try {
      await ref.read(sessionsProvider.notifier).refresh();
    } catch (_) {}
    await _fetchFull();
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
    final projects = hub?.projects ?? const <Map<String, dynamic>>[];
    // Project roster once it lands (live + terminated); warm (live only) paints
    // instantly meanwhile.
    final all = _fullRoster ?? hub?.agents ?? const <Map<String, dynamic>>[];

    // Page in the terminated runs once (also resolves a terminated analysed
    // agent the warm snapshot omits).
    if (!_fullTried) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted && !_fullTried) _fetchFull();
      });
    }

    final me = _meIn(all);
    final projectId = (me?['project_id'] ?? '').toString();

    // Cold case: analysed agent not yet resolvable (terminated, awaiting the
    // fetch) — spinner rather than guessing scope.
    if (me == null && projectId.isEmpty && _fullRoster == null) {
      return const Center(child: CircularProgressIndicator());
    }

    final Widget list;
    if (projectId.isNotEmpty) {
      // PROJECT scope → the project's agents (live + a clean finish). Crashed /
      // failed are error states, not switch targets, so they're excluded.
      final rows = [
        for (final a in all)
          if ((a['project_id'] ?? '').toString() == projectId &&
              !agentIsCrashedOrFailed((a['status'] ?? '').toString()))
            a,
      ];
      rows.sort((x, y) {
        final lx = _agentLive((x['status'] ?? '').toString());
        final ly = _agentLive((y['status'] ?? '').toString());
        if (lx != ly) return lx ? -1 : 1;
        return (x['handle'] ?? '')
            .toString()
            .compareTo((y['handle'] ?? '').toString());
      });
      list = ListView(
        padding: const EdgeInsets.only(bottom: 12),
        physics: const AlwaysScrollableScrollPhysics(),
        children: [
          _groupHeader('Agents · ${_projectName(projects, projectId)}', muted),
          if (rows.isEmpty)
            _emptyRow('No other runs in scope.', muted)
          else
            for (final a in rows) _agentRow(a, fg, muted),
        ],
      );
    } else {
      // STEWARD / team scope → every session in scope: active, paused, AND
      // archived. The rail is a switch-to-analyse roster, so a paused or
      // archived steward session must be reachable here (not just the live
      // ones); each row carries its status so the band is legible.
      final sessions = _scopeSessions();
      list = ListView(
        padding: const EdgeInsets.only(bottom: 12),
        physics: const AlwaysScrollableScrollPhysics(),
        children: [
          _groupHeader('Sessions', muted),
          if (sessions.isEmpty)
            _emptyRow('No sessions in scope.', muted)
          else
            for (final s in sessions) _sessionRow(s, all, fg, muted),
        ],
      );
    }

    return RefreshIndicator(onRefresh: _onRefresh, child: list);
  }

  // The status bands the rail orders by: live (active/open) first, then
  // paused (paused/interrupted), then archived/other.
  static int _sessionBand(String status) {
    if (status == 'active' || status == 'open') return 0;
    if (status == 'paused' || status == 'interrupted') return 1;
    return 2;
  }

  /// Every session in scope — active, paused, AND archived (the sessions
  /// provider buckets the first two into `active`, archived into `previous`).
  /// Live first, then paused, then archived; newest within each band.
  List<Map<String, dynamic>> _scopeSessions() {
    final state = ref.watch(sessionsProvider).value;
    if (state == null) return const [];
    final rows = <Map<String, dynamic>>[...state.active, ...state.previous];
    rows.sort((a, b) {
      final ba = _sessionBand((a['status'] ?? '').toString());
      final bb = _sessionBand((b['status'] ?? '').toString());
      if (ba != bb) return ba.compareTo(bb);
      final ta =
          (a['last_active_at'] ?? a['opened_at'] ?? a['archived_at'] ?? '')
              .toString();
      final tb =
          (b['last_active_at'] ?? b['opened_at'] ?? b['archived_at'] ?? '')
              .toString();
      return tb.compareTo(ta);
    });
    return rows;
  }

  Map<String, dynamic>? _agentFor(List<Map<String, dynamic>> all, String id) {
    if (id.isEmpty) return null;
    for (final a in all) {
      if ((a['id'] ?? '').toString() == id) return a;
    }
    return null;
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
                  agentStatusLabel(status),
                  style:
                      GoogleFonts.jetBrainsMono(fontSize: 10, color: muted),
                ),
            ],
          ),
        ),
      ),
    );
  }

  /// A steward/team active-session row — mirrors the Me page's session card
  /// (category accent + session title), adapted to the rail's vertical list.
  /// Picking it retargets the Insight surface to that session.
  Widget _sessionRow(
    Map<String, dynamic> s,
    List<Map<String, dynamic>> all,
    Color fg,
    Color muted,
  ) {
    final sid = (s['id'] ?? '').toString();
    final agentId = (s['current_agent_id'] ?? '').toString();
    final agent = _agentFor(all, agentId);
    final title = sessionDisplayTitle(s);
    final style = agentCategoryStyle(agentCategory(agent, session: s));
    final steward = stewardLabel((agent?['handle'] ?? '').toString());
    final status = (s['status'] ?? '').toString();
    final band = _sessionBand(status);
    final isLive = band == 0;
    // Status dot: live=green, paused=amber, archived=muted — so the band a
    // row belongs to reads at a glance now that the rail is no longer
    // live-only.
    final statusDot = isLive
        ? DesignColors.success
        : band == 1
            ? DesignColors.warning
            : muted;
    final active = agentId == widget.agentId && agentId.isNotEmpty;
    return Material(
      color: active
          ? DesignColors.primary.withValues(alpha: 0.10)
          : Colors.transparent,
      child: InkWell(
        onTap: (active || agentId.isEmpty)
            ? null
            : () => widget.onSelect(agentId, sid, isLive),
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 9),
          child: Row(
            children: [
              Icon(style.icon, size: 16, color: style.color),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      title,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: GoogleFonts.spaceGrotesk(
                        fontSize: 13,
                        fontWeight: active ? FontWeight.w700 : FontWeight.w500,
                        color: fg,
                      ),
                    ),
                    if (steward.isNotEmpty)
                      Text(
                        steward,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: GoogleFonts.jetBrainsMono(
                            fontSize: 10, color: muted),
                      ),
                  ],
                ),
              ),
              const SizedBox(width: 8),
              Container(
                width: 7,
                height: 7,
                decoration:
                    BoxDecoration(color: statusDot, shape: BoxShape.circle),
              ),
              const SizedBox(width: 6),
              if (status.isNotEmpty)
                Text(
                  status,
                  style: GoogleFonts.jetBrainsMono(fontSize: 10, color: muted),
                ),
            ],
          ),
        ),
      ),
    );
  }
}

/// The left-edge pull handle that opens the [SessionsRail] — a slim
/// half-rounded tab hugging the screen edge. Shared by every surface that
/// hosts the rail (the Insight analysis view and the session chat screen) so
/// the affordance can't drift between them.
class SessionsRailHandle extends StatelessWidget {
  final VoidCallback onTap;
  const SessionsRailHandle({super.key, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border = isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Material(
      color: bg,
      elevation: 2,
      shape: RoundedRectangleBorder(
        borderRadius: const BorderRadius.horizontal(right: Radius.circular(10)),
        side: BorderSide(color: border),
      ),
      child: InkWell(
        onTap: onTap,
        borderRadius:
            const BorderRadius.horizontal(right: Radius.circular(10)),
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 3, vertical: 14),
          child: Icon(Icons.chevron_right, size: 18, color: muted),
        ),
      ),
    );
  }
}
