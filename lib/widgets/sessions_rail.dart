// SessionsRail — the left "Sessions" drawer of the Insight workbench (ADR-041
// §4): a *scoped* quick-switcher for the analysed run. It lists the current
// agent's project siblings ("Agents · <project>") and the current agent's own
// sessions ("This agent"), and retargets the Insight surface when one is picked.
// Scoped, not a global tree — a convenience inside the surface, never a second
// top-level navigator competing with the app's five-tab IA.
//
// Phone-first: a self-contained overlay (tap-to-dismiss scrim + a left-aligned
// panel), mirroring the right Navigator drawer. The host (`SessionAnalysisView`)
// just renders it when open and holds the active target in state.
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../providers/hub_provider.dart';
import '../theme/design_colors.dart';

/// Retarget callback: `(agentId, sessionId, live)` for the picked run.
typedef RetargetCallback = void Function(
    String agentId, String sessionId, bool live);

class SessionsRail extends ConsumerStatefulWidget {
  /// The agent currently being analysed (the scope anchor + active highlight).
  final String agentId;

  /// The session currently being analysed (the active highlight in the
  /// "This agent" group).
  final String activeSessionId;
  final RetargetCallback onSelect;
  final VoidCallback onClose;

  const SessionsRail({
    super.key,
    required this.agentId,
    required this.activeSessionId,
    required this.onSelect,
    required this.onClose,
  });

  @override
  ConsumerState<SessionsRail> createState() => _SessionsRailState();
}

class _SessionsRailState extends ConsumerState<SessionsRail> {
  bool _loading = true;
  String? _error;
  String _projectId = '';
  String _projectLabel = '';
  // Sibling agents in the project (includes the current one, marked active).
  List<Map<String, dynamic>> _agents = const [];
  // The current agent's sessions (this agent is their current_agent_id).
  List<Map<String, dynamic>> _sessions = const [];
  // The agent row whose session is being resolved on tap (a tiny inline spinner).
  String? _resolvingAgentId;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() {
        _error = 'Not connected to a hub';
        _loading = false;
      });
      return;
    }
    try {
      // The current agent gives us the project scope + handle.
      final agent = (await client.getAgentCached(widget.agentId)).body;
      final projectId = (agent['project_id'] ?? '').toString();
      // Project siblings (include terminated/archived so a finished run is still
      // reachable for analysis). Empty project → just this agent.
      List<Map<String, dynamic>> agents;
      if (projectId.isNotEmpty) {
        agents = (await client.listAgentsCached(
          projectId: projectId,
          includeTerminated: true,
          includeArchived: true,
        ))
            .body;
      } else {
        agents = [agent];
      }
      // This agent's sessions — the durable frames it currently fronts.
      List<Map<String, dynamic>> sessions = const [];
      try {
        final all = (await client.listSessionsCached()).body;
        sessions = [
          for (final s in all)
            if ((s['current_agent_id'] ?? '').toString() == widget.agentId) s,
        ];
      } catch (_) {
        // Non-fatal: the Agents group still works.
      }
      if (!mounted) return;
      setState(() {
        _projectId = projectId;
        _projectLabel = (agent['handle'] ?? '').toString();
        _agents = agents;
        _sessions = sessions;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = 'Could not load sessions';
        _loading = false;
      });
    }
  }

  static bool _agentLive(String status) =>
      status == 'running' || status == 'idle' || status == 'pending';

  /// Resolve an agent's hub session (its newest event's session_id) and
  /// retarget. Mirrors the archived-agent screen's resolution.
  Future<void> _pickAgent(Map<String, dynamic> agent) async {
    final id = (agent['id'] ?? '').toString();
    if (id.isEmpty) return;
    final status = (agent['status'] ?? '').toString();
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() => _resolvingAgentId = id);
    String sid = '';
    try {
      final ev = await client.listAgentEvents(id, tail: true, limit: 1);
      if (ev.isNotEmpty) sid = (ev.first['session_id'] ?? '').toString();
    } catch (_) {
      // Leave sid empty — InsightTranscript falls back to the loaded window.
    }
    if (!mounted) return;
    setState(() => _resolvingAgentId = null);
    widget.onSelect(id, sid, _agentLive(status));
    widget.onClose();
  }

  void _pickSession(Map<String, dynamic> session) {
    final sid = (session['id'] ?? '').toString();
    if (sid.isEmpty) return;
    final agentId =
        (session['current_agent_id'] ?? '').toString().isNotEmpty
            ? (session['current_agent_id']).toString()
            : widget.agentId;
    final live = (session['status'] ?? '').toString() == 'active';
    widget.onSelect(agentId, sid, live);
    widget.onClose();
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
                      Expanded(child: _body(isDark, fg, muted)),
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

  Widget _body(bool isDark, Color fg, Color muted) {
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            _error!,
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(fontSize: 12, color: muted),
          ),
        ),
      );
    }
    return ListView(
      padding: const EdgeInsets.only(bottom: 12),
      children: [
        _groupHeader(
          _projectLabel.isNotEmpty && _projectId.isNotEmpty
              ? 'Agents · ${_projectLabel.split('/').first}'
              : 'Agents',
          muted,
        ),
        if (_agents.isEmpty)
          _emptyRow('No sibling agents.', muted)
        else
          for (final a in _agents) _agentRow(a, fg, muted),
        _groupHeader('This agent', muted),
        if (_sessions.isEmpty)
          _emptyRow('No other sessions.', muted)
        else
          for (final s in _sessions) _sessionRow(s, fg, muted),
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

  Widget _sessionRow(Map<String, dynamic> s, Color fg, Color muted) {
    final sid = (s['id'] ?? '').toString();
    final title = (s['title'] ?? '').toString();
    final status = (s['status'] ?? '').toString();
    final active = sid == widget.activeSessionId;
    final label = title.isNotEmpty
        ? title
        : (sid.length > 8 ? 'sess-${sid.substring(0, 6)}' : sid);
    final live = status == 'active';
    final dot = live ? DesignColors.success : muted;
    return Material(
      color: active
          ? DesignColors.primary.withValues(alpha: 0.10)
          : Colors.transparent,
      child: InkWell(
        onTap: active ? null : () => _pickSession(s),
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
                  label,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 13,
                    fontWeight: active ? FontWeight.w700 : FontWeight.w500,
                    color: fg,
                  ),
                ),
              ),
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
