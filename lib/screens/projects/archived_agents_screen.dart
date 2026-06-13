import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../l10n/app_localizations.dart';
import '../../providers/hub_provider.dart';
import '../../providers/sessions_provider.dart';
import '../../providers/vocab_provider.dart';
import '../../services/hub/agent_status.dart';
import '../../services/hub/open_steward_session.dart' show openAgentSession;
import '../../services/vocab/vocab_axis.dart';
import '../../theme/design_colors.dart';
import '../../theme/tokens.dart';
import '../../widgets/agent_actions_menu.dart';
import '../../widgets/live_feed.dart';
import '../../widgets/insights_panel.dart';
import '../../widgets/session_analysis_view.dart';
import '../../widgets/session_header.dart';

/// Read-only list of historical agents. Two modes:
/// - Global (default; projectId null): shows agents with
///   `archived_at != null` across the whole team. Operators reach this
///   from Settings to audit cross-project agent history.
/// - Project-scoped (projectId set, v1.0.619): shows every non-live
///   agent that ever belonged to one project — terminated, crashed,
///   failed, AND archived. This is what the project detail page's
///   "Agent history" button opens; an operator who terminated a worker
///   moments ago expects to see it here without having to manually
///   archive it first.
class ArchivedAgentsScreen extends ConsumerStatefulWidget {
  final String? projectId;
  const ArchivedAgentsScreen({super.key, this.projectId});

  @override
  ConsumerState<ArchivedAgentsScreen> createState() =>
      _ArchivedAgentsScreenState();
}

class _ArchivedAgentsScreenState extends ConsumerState<ArchivedAgentsScreen> {
  List<Map<String, dynamic>> _rows = const [];
  bool _loading = false;
  bool _hubMissing = false;
  String? _error;

  bool get _projectScoped =>
      widget.projectId != null && widget.projectId!.isNotEmpty;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() {
        _loading = false;
        _hubMissing = true;
      });
      return;
    }
    try {
      // Archived rows are typically also terminated; without
      // includeTerminated the post-v1.0.606 default hides them and
      // the screen renders empty. Project-scoped mode broadens the
      // filter to include every non-live agent (terminated / crashed
      // / failed / archived) for the named project.
      final all = await client.listAgents(
        includeArchived: true,
        includeTerminated: true,
        projectId: _projectScoped ? widget.projectId : null,
      );
      final filtered = <Map<String, dynamic>>[];
      for (final a in all) {
        if (_projectScoped) {
          if ((a['project_id'] ?? '').toString() != widget.projectId) continue;
          final status = (a['status'] ?? '').toString();
          final archivedAt = (a['archived_at'] ?? '').toString();
          final isTerminal = status == 'terminated' ||
              status == 'crashed' ||
              status == 'failed';
          if (!isTerminal && archivedAt.isEmpty) continue;
          filtered.add(a);
        } else {
          if (a['archived_at'] == null) continue;
          filtered.add(a);
        }
      }
      // Most-recent first so a freshly-terminated worker is at the top.
      filtered.sort((a, b) {
        final ka = (a['terminated_at'] ?? a['archived_at'] ?? a['created_at'] ?? '')
            .toString();
        final kb = (b['terminated_at'] ?? b['archived_at'] ?? b['created_at'] ?? '')
            .toString();
        return kb.compareTo(ka);
      });
      if (!mounted) return;
      setState(() {
        _rows = filtered;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final vocab = ref.watch(vocabularyProvider);
    final agentTerm = vocab.term(VocabAxis.roleAgent);
    return Scaffold(
      appBar: AppBar(
        title: Text(
          _projectScoped
              ? l10n.agentHistoryTitle(agentTerm.title)
              : l10n.archivedAgentsTitle(agentTerm.plural),
          style: GoogleFonts.spaceGrotesk(
              fontSize: 18, fontWeight: FontWeight.w700),
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh),
            onPressed: _loading ? null : _load,
          ),
        ],
      ),
      body: _buildBody(),
    );
  }

  Widget _buildBody() {
    final l10n = AppLocalizations.of(context)!;
    final vocab = ref.watch(vocabularyProvider);
    final agentTerm = vocab.term(VocabAxis.roleAgent);
    final projectTerm = vocab.term(VocabAxis.entityProject);
    if (_loading && _rows.isEmpty) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_hubMissing || _error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            _hubMissing ? l10n.hubNotConfigured : _error!,
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(color: DesignColors.error),
          ),
        ),
      );
    }
    // Stopped agents (session paused → resumable) are active, recoverable
    // work — not history. They belong in the live agent / sessions view, so
    // drop them from the project history list (the project Agents tab surfaces
    // them instead). Resumability is read off the warm sessions snapshot; the
    // global audit view (no project) still shows everything for forensics.
    final sessions = ref.watch(sessionsProvider).value;
    final visible = _projectScoped
        ? _rows
            .where((r) =>
                agentResumability(sessionStatusForAgent(
                    sessions, (r['id'] ?? '').toString())) !=
                AgentResumability.resumable)
            .toList()
        : _rows;
    if (visible.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            _projectScoped
                ? l10n.agentHistoryEmpty(
                    agentTerm.pluralLower, projectTerm.lower)
                : l10n.archivedAgentsEmpty(
                    agentTerm.pluralLower, agentTerm.lower),
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(color: DesignColors.textMuted),
          ),
        ),
      );
    }
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView.separated(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
        itemCount: visible.length,
        separatorBuilder: (_, __) => const SizedBox(height: 8),
        itemBuilder: (_, i) => _ArchivedTile(
          row: visible[i],
          onTap: () {
            Navigator.of(context).push(MaterialPageRoute(
              builder: (_) => ArchivedAgentDetailScreen(summary: visible[i]),
            ));
          },
          onChanged: _load,
        ),
      ),
    );
  }
}

class _ArchivedTile extends ConsumerWidget {
  final Map<String, dynamic> row;
  final VoidCallback onTap;
  // Re-fetches the history list after a lifecycle action (Delete removes the
  // row from the live roster; Resume session revives it elsewhere).
  final Future<void> Function() onChanged;
  const _ArchivedTile({
    required this.row,
    required this.onTap,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context)!;
    final agentTerm = ref.watch(vocabularyProvider).term(VocabAxis.roleAgent);
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final id = (row['id'] ?? '').toString();
    final handle = (row['handle'] ?? '?').toString();
    final kind = (row['kind'] ?? '').toString();
    final status = (row['status'] ?? '').toString();
    final isPaused = (row['pause_state'] ?? '').toString() == 'paused';
    // Resolve the run's fate from the warm sessions snapshot: Stop (session
    // paused → "stopped", Resume offered) vs Archive (session archived →
    // "archived", Resume hidden — it would 409). Both carry agent
    // status='terminated', so the session is the only discriminator.
    final resumable = agentResumability(
        sessionStatusForAgent(ref.watch(sessionsProvider).value, id));
    final archivedAt = (row['archived_at'] ?? '').toString();
    final terminatedAt = (row['terminated_at'] ?? '').toString();
    // Subtitle line: kind + "<fate> <timestamp>". The fate verb is the
    // glossary-friendly status (stopped / archived / crashed / failed) — never
    // the raw "terminated", which is ambiguous between Stop and Archive. The
    // timestamp prefers archived_at (operator Delete) then terminated_at.
    final tsVerb = agentStatusLabelResumable(status, resumable);
    final tsWhen = archivedAt.isNotEmpty
        ? archivedAt
        : terminatedAt.isNotEmpty
            ? terminatedAt
            : '';
    final tsLabel = tsWhen.isEmpty ? '' : '$tsVerb ${_trim(tsWhen)}';
    // Icon hints at provenance: archive bin for archived, ghost for
    // terminal-without-archive. Both share the same muted opacity so
    // the visual weight stays consistent.
    final leadIcon = archivedAt.isNotEmpty
        ? Icons.inventory_2_outlined
        : Icons.power_settings_new;
    return Opacity(
      opacity: 0.72,
      child: Material(
        color: Colors.transparent,
        child: InkWell(
          // A stopped (resumable) row that slips through to this list opens the
          // full session surface — which carries the left rail + resume — not
          // the read-only tombstone. Genuinely-dead rows keep the tombstone.
          onTap: resumable == AgentResumability.resumable
              ? () => openAgentSession(context, ref, row)
              : onTap,
          borderRadius: Radii.mdBorder,
          child: Container(
            padding:
                const EdgeInsets.symmetric(horizontal: Spacing.s12, vertical: 12),
            decoration: BoxDecoration(
              color: isDark
                  ? DesignColors.surfaceDark
                  : DesignColors.surfaceLight,
              borderRadius: Radii.mdBorder,
              border: Border.all(
                color: isDark
                    ? DesignColors.borderDark
                    : DesignColors.borderLight,
              ),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Icon(leadIcon,
                        size: 18, color: DesignColors.textMuted),
                    const SizedBox(width: 8),
                    Expanded(
                      child: Text(handle,
                          style: GoogleFonts.spaceGrotesk(
                              fontSize: 14, fontWeight: FontWeight.w600)),
                    ),
                    // Friendly status that folds in the session's fate:
                    // terminated → "stopped" (resumable) or "archived"
                    // (permanent), not the raw hub vocabulary.
                    Text(agentStatusLabelResumable(status, resumable),
                        style: GoogleFonts.jetBrainsMono(
                            fontSize: FontSizes.label, color: DesignColors.textMuted)),
                    const SizedBox(width: 2),
                    // Dead-agent actions — Resume session (continue the paused
                    // session) + Delete (drop from the list). Respawn lives in
                    // the detail screen, which fetches the spawn spec.
                    PopupMenuButton<String>(
                      tooltip: l10n.agentActionsTooltip(agentTerm.title),
                      icon: const Icon(Icons.more_vert,
                          size: 18, color: DesignColors.textMuted),
                      padding: EdgeInsets.zero,
                      onSelected: (v) async {
                        final out = await runAgentLifecycleAction(
                          context,
                          ref,
                          v,
                          agentId: id,
                          handle: handle,
                          isPaused: isPaused,
                        );
                        if (out == AgentActionOutcome.removed ||
                            out == AgentActionOutcome.sessionResumed) {
                          await onChanged();
                        }
                      },
                      itemBuilder: (ctx) => agentLifecycleMenuItems(
                        ctx,
                        ref,
                        isDead: true,
                        isPaused: isPaused,
                        hasPane: false,
                        canRespawn: false,
                        resumable: resumable,
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 4),
                Text(
                  [kind, if (tsLabel.isNotEmpty) tsLabel]
                      .where((s) => s.isNotEmpty)
                      .join(' · '),
                  style: GoogleFonts.jetBrainsMono(
                      fontSize: 11, color: DesignColors.textMuted),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  String _trim(String iso) => iso.length >= 19 ? iso.substring(0, 19) : iso;
}

/// Tombstone detail for an archived agent. Fetches the full agent row
/// (to pick up spawn_spec_yaml) and the journal so operators can audit
/// what the agent actually did before it was put to rest.
///
/// Intentionally read-only — there are no lifecycle actions once an
/// agent is archived; the live _AgentDetailSheet on hub_screen owns
/// pause/resume/terminate for the active list.
class ArchivedAgentDetailScreen extends ConsumerStatefulWidget {
  final Map<String, dynamic> summary;
  const ArchivedAgentDetailScreen({super.key, required this.summary});

  @override
  ConsumerState<ArchivedAgentDetailScreen> createState() =>
      _ArchivedAgentDetailScreenState();
}

class _ArchivedAgentDetailScreenState
    extends ConsumerState<ArchivedAgentDetailScreen> {
  Map<String, dynamic>? _full;
  String? _journal;
  String? _error;
  bool _loading = true;
  // Hub session id for this agent's run — resolved from its newest event's
  // top-level session_id so the Insights tab can render the full analysis
  // surface (digest + turns) over the finished run.
  String _sessionId = '';
  // Active view in the `View ▾` switcher: 0 Feed · 1 Summary · 2 Journal ·
  // 3 Insights. Replaces the old TabBar to reclaim vertical space (parity
  // with the session-detail surface, sessions_screen.dart:2647).
  int _view = 0;

  String get _id => (widget.summary['id'] ?? '').toString();

  /// The hub session id for the Insights surface: the event-resolved
  /// [_sessionId] if present, else the warm sessions snapshot's session whose
  /// `current_agent_id` is this agent. Reactive via watch.
  String _resolveAnalysisSessionId() {
    if (_sessionId.isNotEmpty) return _sessionId;
    final s = ref.watch(sessionsProvider).value;
    if (s != null) {
      for (final sess in [...s.active, ...s.previous]) {
        if ((sess['current_agent_id'] ?? '').toString() == _id) {
          final sid = (sess['id'] ?? '').toString();
          if (sid.isNotEmpty) return sid;
        }
      }
    }
    return '';
  }

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final full = (await client.getAgentCached(_id)).body;
      // Resolve the hub session id (newest event's session_id) for Insights.
      // Best-effort — the tab self-gates to the Feed-only fallback if absent.
      try {
        final ev = await client.listAgentEvents(_id, tail: true, limit: 1);
        if (ev.isNotEmpty) {
          _sessionId = (ev.first['session_id'] ?? '').toString();
        }
      } catch (_) {
        // Non-fatal: leave _sessionId empty, Insights falls back.
      }
      String? journal;
      try {
        journal = await client.readAgentJournal(_id);
      } catch (_) {
        // Journal may be unreadable or empty on archived agents; don't
        // block the rest of the detail on a journal-fetch failure.
        journal = null;
      }
      if (!mounted) return;
      setState(() {
        _full = full;
        _journal = journal;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final handle = (widget.summary['handle'] ?? _id).toString();
    final row = _full ?? widget.summary;
    final kind = (row['kind'] ?? '').toString();
    final status = (row['status'] ?? '').toString();
    // Feed / Summary / Journal / Insights now reach via the header's
    // `View ▾` switcher instead of a TabBar — no second bar eating vertical
    // space, matching the session-detail surface (sessions_screen.dart:2647).
    // Insights is always offered — a finished run's analysis is exactly what
    // an operator opening a terminated agent wants — degrading to agent-scoped
    // tiles ([InsightsPanel]) until the hub session id resolves (or if the run
    // never stamped one). Matches the project-agent sheet
    // (projects_screen.dart:2043).
    // Prefer the event-resolved session id; fall back to the warm sessions
    // snapshot (the session fronting this agent) so the rich Insights surface
    // still shows when the newest event carries no top-level session_id
    // (engine/older-data dependent — see the project-agent sheet's note).
    final analysisSessionId = _resolveAnalysisSessionId();
    final hasSession = analysisSessionId.isNotEmpty;
    final subtitle = [
      if (kind.isNotEmpty) kind,
      if (status.isNotEmpty) status,
    ].join(' · ');
    return Scaffold(
      // No Material AppBar — SessionHeader carries the title + close + the
      // `View ▾` switcher; SafeArea supplies the status-bar inset.
      body: SafeArea(
        bottom: false,
        child: Column(
          children: [
            SessionHeader(
              leading: const BackButton(),
              title: '@$handle',
              subtitle: subtitle.isEmpty ? null : subtitle,
              views: [
                SessionView(label: l10n.sessionViewFeed, icon: Icons.forum_outlined),
                SessionView(label: l10n.sessionViewSummary, icon: Icons.info_outline),
                SessionView(label: l10n.sessionViewJournal, icon: Icons.menu_book_outlined),
                SessionView(label: l10n.sessionViewInsights, icon: Icons.insights_outlined),
              ],
              currentView: _view,
              onSelectView: (i) => setState(() => _view = i),
            ),
            if (_error != null)
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
                child: Text(_error!,
                    style: GoogleFonts.jetBrainsMono(
                        color: DesignColors.error, fontSize: 12)),
              ),
            Expanded(
              child: _loading
                  ? const Center(child: CircularProgressIndicator())
                  // IndexedStack keeps each view's scroll + state across
                  // `View ▾` switches (as TabBarView did). Indexed by [_view].
                  : IndexedStack(
                      index: _view,
                      children: [
                        // Feed = the agent_events stream the agent emitted in
                        // its lifetime. Static for terminated agents but
                        // historical events render identically to live ones —
                        // the same widget Me → Sessions uses.
                        LiveFeed(agentId: _id),
                        _summaryTab(),
                        _journalTab(),
                        hasSession
                            ? SessionAnalysisView(
                                agentId: _id,
                                sessionId: analysisSessionId,
                                live: false,
                              )
                            : ListView(
                                padding:
                                    const EdgeInsets.fromLTRB(16, 12, 16, 16),
                                children: [
                                  InsightsPanel(
                                      scope: InsightsScope.agent(_id)),
                                ],
                              ),
                      ],
                    ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _summaryTab() {
    final l10n = AppLocalizations.of(context)!;
    final projectTerm = ref.watch(vocabularyProvider).term(VocabAxis.entityProject);
    final row = _full ?? widget.summary;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final kind = (row['kind'] ?? '').toString();
    final status = (row['status'] ?? '').toString();
    final archivedAt = (row['archived_at'] ?? '').toString();
    final terminatedAt = (row['terminated_at'] ?? '').toString();
    final createdAt = (row['created_at'] ?? '').toString();
    final projectId = (row['project_id'] ?? '').toString();
    final spawnSpec = (row['spawn_spec_yaml'] ?? '').toString();
    final spent = (row['spent_cents'] as num?)?.toInt() ?? 0;

    return ListView(
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 24),
      children: [
        _section(
          l10n.summaryLabel,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              _kv(l10n.statusLabel, status),
              _kv(l10n.fieldKind, kind),
              if (projectId.isNotEmpty) _kv(projectTerm.title, projectId),
              if (createdAt.isNotEmpty)
                _kv(l10n.createdLabel, _trimIso(createdAt)),
              if (terminatedAt.isNotEmpty)
                _kv(l10n.terminatedLabel, _trimIso(terminatedAt)),
              if (archivedAt.isNotEmpty)
                _kv(l10n.archivedLabel, _trimIso(archivedAt)),
              _kv(l10n.totalSpendLabel, '\$${(spent / 100).toStringAsFixed(2)}'),
            ],
          ),
          isDark: isDark,
        ),
        if (spawnSpec.isNotEmpty) ...[
          const SizedBox(height: 16),
          _section(
            l10n.spawnSpecLabel,
            child: SelectableText(
              spawnSpec,
              style: GoogleFonts.jetBrainsMono(fontSize: 12, height: 1.35),
            ),
            isDark: isDark,
          ),
        ],
      ],
    );
  }

  Widget _journalTab() {
    final l10n = AppLocalizations.of(context)!;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return ListView(
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 24),
      children: [
        _section(
          l10n.journalLabel,
          child: (_journal ?? '').trim().isEmpty
              ? Text(
                  l10n.noJournalEntries,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 13,
                    color: DesignColors.textMuted,
                  ),
                )
              : SelectableText(
                  _journal!,
                  style: GoogleFonts.jetBrainsMono(
                      fontSize: 12, height: 1.35),
                ),
          isDark: isDark,
        ),
      ],
    );
  }

  Widget _section(String label,
      {required Widget child, required bool isDark}) {
    return Container(
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
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: Text(
              label,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                fontWeight: FontWeight.w700,
                letterSpacing: 0.5,
                color: DesignColors.textMuted,
              ),
            ),
          ),
          child,
        ],
      ),
    );
  }

  Widget _kv(String label, String value) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 96,
            child: Text(
              label,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 12,
                color: DesignColors.textMuted,
              ),
            ),
          ),
          Expanded(
            child: SelectableText(
              value,
              style: GoogleFonts.jetBrainsMono(fontSize: 12, height: 1.35),
            ),
          ),
        ],
      ),
    );
  }

  String _trimIso(String iso) =>
      iso.length >= 19 ? iso.substring(0, 19).replaceFirst('T', ' ') : iso;
}
