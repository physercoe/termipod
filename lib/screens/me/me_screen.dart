import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import 'package:termipod/l10n/app_localizations.dart';

import '../../providers/activity_provider.dart';
import '../../providers/hub_provider.dart';
import '../../providers/sessions_provider.dart';
import '../../providers/urgent_tasks_provider.dart';
import '../../providers/vocab_provider.dart';
import '../../services/host_label.dart';
import '../../services/hub/open_steward_session.dart';
import '../../services/hub/session_display.dart';
import '../../services/steward_handle.dart';
import '../../theme/design_colors.dart';
import '../../theme/task_priority_style.dart';
import '../../theme/tokens.dart';
import '../../widgets/activity_digest_card.dart';
import '../../widgets/agent_category_style.dart';
import '../../widgets/app_chip.dart';
import '../../widgets/home/persistent_steward_card.dart';
import '../../widgets/me_stats_card.dart';
import '../../widgets/steward_badge.dart';
import '../../widgets/team_switcher.dart';
import '../projects/search_screen.dart';
import '../projects/task_detail_screen.dart';
import '../sessions/sessions_screen.dart';
import 'approval_detail_screen.dart';
import 'decision_history_screen.dart';
import 'inline_actions.dart';
import 'widgets/propose_addressee.dart';
import 'widgets/propose_card_router.dart';
import 'widgets/stalled_decisions_digest.dart';

/// Me tab — Tier-0 default landing per `docs/ia-redesign.md` §6.1.
///
/// Sections, top-down:
///   - Active sessions — horizontal strip of in-flight sessions
///     (tap → SessionChatScreen). Replaces the prior "My work"
///     project strip; sessions are what the principal is actually
///     in the middle of, and the Projects tab already covers the
///     full project list for navigation.
///   - Attention — open attention items assigned to or relevant to me,
///     filterable by kind.
///
/// Wedge 5 adds the "Since you were last here" digest at the bottom,
/// mirroring the Activity tab's top-of-feed digest card (identical data;
/// Activity is the firehose, Me is the summary).
///
/// Attention sources by filter chip:
///   - Requests  — agent asks for principal input. kind ∈
///                 {approval_request, select, help_request,
///                 template_proposal}. Renamed from "Approvals" once
///                 the filter grew beyond binary approve/deny — every
///                 kind in this bucket is "agent waiting on the user".
///   - Agents    — kind='idle' / 'agent_error'.
///   - Messages  — every other attention kind.
class MeScreen extends ConsumerWidget {
  const MeScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final hub = ref.watch(hubProvider);
    final l10n = AppLocalizations.of(context)!;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final colorScheme = Theme.of(context).colorScheme;

    final hubState = hub.value ?? const HubState();
    final items = _buildItems(hubState.attention);
    final filter = ref.watch(_filterProvider);
    // ADR-030 W19.6-mobile: when the stalled-only toggle is ON (set
    // by tapping the top digest card), AND the chip-filter with the
    // stalled predicate so only stalled propose rows remain. OFF
    // preserves the existing chip-filter behavior unchanged.
    final stalledOnly = ref.watch(stalledFilterProvider);
    final filtered = items
        .where(filter.matches)
        .where((it) =>
            !stalledOnly ||
            (it.attention != null && isStalledPropose(it.attention!)))
        .toList();
    final stalledCount = stalledDecisionsCount(hubState.attention);
    final stalledWithPrincipal =
        stalledOverDayDecisionsCount(hubState.attention);
    final audit = ref.watch(recentAuditProvider);
    final sessionsState = ref.watch(sessionsProvider).value;
    final activeSessions = _activeSessions(sessionsState);

    return Scaffold(
      // Per docs/ia-redesign.md §8 forbidden pattern #15 + W2-S3:
      // this FAB used to open the team-wide hub-meta channel as if
      // that were the steward 1:1 chat. They're different surfaces
      // (team broadcast vs director↔steward session). Now it opens
      // the steward's active session; hub-meta stays reachable via
      // the team switcher.
      floatingActionButton: hubState.configured
          ? FloatingActionButton.extended(
              onPressed: () => openStewardSession(context, ref),
              icon: const Icon(Icons.auto_awesome),
              label: Text(ref.watch(vocabularyProvider).steward),
            )
          : null,
      body: RefreshIndicator(
        onRefresh: () async {
          if (hubState.configured) {
            await ref.read(hubProvider.notifier).refreshAll();
          }
        },
        child: CustomScrollView(
          slivers: [
            SliverAppBar(
              floating: true,
              pinned: true,
              elevation: 0,
              backgroundColor: isDark
                  ? DesignColors.backgroundDark.withValues(alpha: 0.95)
                  : DesignColors.backgroundLight.withValues(alpha: 0.95),
              title: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  ClipRRect(
                    borderRadius: BorderRadius.circular(8),
                    child: Image.asset(
                      'assets/icon/icon.png',
                      width: 32,
                      height: 32,
                    ),
                  ),
                  const SizedBox(width: 10),
                  Text(
                    l10n.tabMe,
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 24,
                      fontWeight: FontWeight.w700,
                      color: colorScheme.onSurface,
                    ),
                  ),
                ],
              ),
              actions: [
                const TeamSwitcher(),
                IconButton(
                  icon: const Icon(Icons.forum_outlined),
                  tooltip: 'Sessions',
                  onPressed: () => Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => const SessionsScreen(),
                    ),
                  ),
                ),
                IconButton(
                  icon: const Icon(Icons.history),
                  tooltip: 'Decision history',
                  onPressed: () => Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => const DecisionHistoryScreen(),
                    ),
                  ),
                ),
                IconButton(
                  icon: const Icon(Icons.search),
                  tooltip: 'Search events',
                  onPressed: () => Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => const SearchScreen(),
                    ),
                  ),
                ),
              ],
            ),
            // Persistent general-steward (W4 / research-demo lifecycle).
            // Sits above the active-sessions strip so the always-on
            // concierge is the first actor visible — distinct from
            // project-scoped domain stewards which live on each
            // project page.
            const SliverToBoxAdapter(child: PersistentStewardCard()),
            if (activeSessions.isNotEmpty)
              SliverToBoxAdapter(
                child: _ActiveSessionsStrip(
                  sessions: activeSessions,
                  agents: hubState.agents,
                  projects: hubState.projects,
                  hosts: hubState.hosts,
                ),
              ),
            // ADR-030 W19.6-mobile — stalled-decisions digest card.
            // Sits above the section label so the count is the first
            // thing the principal sees when opening Me. Renders the
            // SizedBox.shrink() when count=0 (no visual gap).
            SliverToBoxAdapter(
              child: StalledDecisionsDigest(
                stalledCount: stalledCount,
                stalledOverDayCount: stalledWithPrincipal,
              ),
            ),
            SliverToBoxAdapter(
              child: _SectionLabel(
                text: l10n.meAttentionSection,
              ),
            ),
            SliverToBoxAdapter(
              child: _FilterBar(
                counts: _countsByFilter(items),
                selected: filter,
                onChanged: (f) => ref.read(_filterProvider.notifier).set(f),
              ),
            ),
            if (hubState.loading && items.isEmpty)
              const SliverToBoxAdapter(
                child: SizedBox(
                  height: 240,
                  child: Center(child: CircularProgressIndicator()),
                ),
              )
            else if (filtered.isEmpty)
              SliverToBoxAdapter(
                child: SizedBox(
                  height: 240,
                  child: _EmptyState(filter: filter),
                ),
              )
            else
              SliverPadding(
                padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
                sliver: SliverList.separated(
                  itemCount: filtered.length,
                  separatorBuilder: (_, __) => const SizedBox(height: 10),
                  itemBuilder: (_, i) => _MeCard(item: filtered[i]),
                ),
              ),
            const SliverToBoxAdapter(child: _UrgentTasksSection()),
            SliverToBoxAdapter(
              child: _SectionLabel(text: l10n.meDigestSection),
            ),
            SliverToBoxAdapter(
              child: ActivityDigestCard(events: audit.value ?? const []),
            ),
            // Phase 2 W3 — small Stats card glanced at the bottom of
            // the digest section. Hidden until we have a configured
            // hub + a non-empty teamId; otherwise the two-window read
            // would 400 immediately.
            if (hubState.configured &&
                (hubState.config?.teamId.isNotEmpty ?? false))
              SliverToBoxAdapter(
                child: MeStatsCard(teamId: hubState.config!.teamId),
              ),
            const SliverToBoxAdapter(child: SizedBox(height: 100)),
          ],
        ),
      ),
    );
  }

  List<_MeItem> _buildItems(List<Map<String, dynamic>> attention) {
    final out = <_MeItem>[];
    for (final a in attention) {
      out.add(_MeItem.attention(a));
    }
    out.sort((a, b) => b.ts.compareTo(a.ts));
    return out;
  }

  /// Active sessions for the strip — only `status='active'`, sorted
  /// by `last_active_at` desc. Capped at 6 so the strip fits one
  /// horizontal flick; the full list (including paused/archived) is
  /// reachable via the Sessions icon in the AppBar.
  ///
  /// Tolerates the legacy status string `open` during the brief
  /// rollout window between hub/app updates (ADR-009).
  List<Map<String, dynamic>> _activeSessions(SessionsState? state) {
    if (state == null || state.active.isEmpty) return const [];
    final live = <Map<String, dynamic>>[
      for (final s in state.active)
        if ((s['status'] ?? '').toString() == 'active') s,
    ];
    live.sort((a, b) {
      final ta =
          (a['last_active_at'] ?? a['opened_at'] ?? '').toString();
      final tb =
          (b['last_active_at'] ?? b['opened_at'] ?? '').toString();
      return tb.compareTo(ta);
    });
    return live.take(6).toList();
  }

  Map<_Filter, int> _countsByFilter(List<_MeItem> items) {
    final counts = {for (final f in _Filter.values) f: 0};
    for (final item in items) {
      counts[_Filter.all] = (counts[_Filter.all] ?? 0) + 1;
      final f = item.filter;
      counts[f] = (counts[f] ?? 0) + 1;
    }
    return counts;
  }
}

enum _Filter { all, approvals, agents, messages }

extension _FilterX on _Filter {
  String get label {
    switch (this) {
      case _Filter.all:
        return 'All';
      case _Filter.approvals:
        return 'Requests';
      case _Filter.agents:
        return 'Agents';
      case _Filter.messages:
        return 'Messages';
    }
  }

  bool matches(_MeItem item) {
    if (this == _Filter.all) return true;
    return item.filter == this;
  }
}

class _FilterNotifier extends Notifier<_Filter> {
  @override
  _Filter build() => _Filter.all;
  void set(_Filter f) => state = f;
}

final _filterProvider =
    NotifierProvider<_FilterNotifier, _Filter>(_FilterNotifier.new);

class _MeItem {
  final String id;
  final _Filter filter;
  final String kind;
  final String title;
  final String? subtitle;
  final DateTime ts;
  final String? severity;
  final String? actor;
  final Map<String, dynamic>? attention;

  const _MeItem({
    required this.id,
    required this.filter,
    required this.kind,
    required this.title,
    required this.ts,
    this.subtitle,
    this.severity,
    this.actor,
    this.attention,
  });

  factory _MeItem.attention(Map<String, dynamic> a) {
    final kind = (a['kind'] ?? 'attention').toString();
    final filter = _filterForAttention(a);
    // actor_kind + actor_handle are the authoritative columns stamped by
    // the hub (migration 0016). An agent-raised attention gets actor for
    // badge rendering; system/user rows stay unbadged.
    final actorKind = (a['actor_kind'] ?? '').toString();
    final actorHandle = (a['actor_handle'] ?? '').toString();
    final actor = (actorKind == 'agent' && actorHandle.isNotEmpty)
        ? actorHandle
        : null;
    return _MeItem(
      id: (a['id'] ?? '').toString(),
      filter: filter,
      kind: kind,
      title: (a['summary'] ?? '').toString(),
      subtitle: (a['scope_kind'] ?? '').toString(),
      ts: _parseTs(a['created_at']?.toString()),
      severity: a['severity']?.toString(),
      actor: actor,
      attention: a,
    );
  }

  // Known kinds whose whole point is a director action. Kept only as a
  // belt-and-suspenders fallback so a decision kind still classifies as a
  // Request even if a future variant ever ships without a pending payload.
  static const _actionableKinds = {
    'approval_request',
    'permission_prompt',
    'select',
    'help_request',
    'elicit',
    'template_proposal',
    // ADR-025 W4: principal-action is "spawn the project steward".
    'project_steward_request',
    // ADR-030: the generic governed `propose` verb (carries change_kind).
    'propose',
    // ADR-020 W2: a deliverable sent back for revision — actionable (the
    // steward must resolve the redlines) but resolved through the detail
    // screen, not the binary decide endpoint.
    'revision_requested',
  };

  // Requests vs Messages is decided by whether the item carries an action
  // the director must take — a `pending_payload` (the structured ask) or a
  // governed `change_kind` — NOT by a closed kind allowlist. This
  // property-first rule is the clear boundary: anything actionable is a
  // Request, everything else is an FYI Message. It also means a newly
  // introduced actionable kind can no longer silently fall into Messages
  // and lose its inline affordance (the bug `revision_requested` exposed).
  static _Filter _filterForAttention(Map<String, dynamic> a) {
    final kind = (a['kind'] ?? '').toString();
    // Agent-state items keep their own bucket.
    if (kind == 'idle' || kind == 'agent_error') return _Filter.agents;
    final hasPendingAction = a['pending_payload'] != null;
    final hasChangeKind = (a['change_kind'] ?? '').toString().isNotEmpty;
    if (hasPendingAction || hasChangeKind || _actionableKinds.contains(kind)) {
      return _Filter.approvals;
    }
    return _Filter.messages;
  }

  static DateTime _parseTs(String? raw) {
    if (raw == null || raw.isEmpty) return DateTime.now();
    return DateTime.tryParse(raw) ?? DateTime.now();
  }
}

class _FilterBar extends StatelessWidget {
  final Map<_Filter, int> counts;
  final _Filter selected;
  final ValueChanged<_Filter> onChanged;

  const _FilterBar({
    required this.counts,
    required this.selected,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      child: Row(
        children: [
          for (final f in _Filter.values) ...[
            _FilterChip(
              label: f.label,
              count: counts[f] ?? 0,
              selected: selected == f,
              onTap: () => onChanged(f),
            ),
            const SizedBox(width: 8),
          ],
        ],
      ),
    );
  }
}

class _FilterChip extends StatelessWidget {
  final String label;
  final int count;
  final bool selected;
  final VoidCallback onTap;

  const _FilterChip({
    required this.label,
    required this.count,
    required this.selected,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = selected
        ? DesignColors.primary
        : (isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight);
    final fg = selected
        ? Colors.white
        : (isDark
            ? DesignColors.textSecondary
            : DesignColors.textSecondaryLight);
    return InkWell(
      onTap: onTap,
      borderRadius: Radii.lgBorder,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: Spacing.s12, vertical: 8),
        decoration: BoxDecoration(
          color: bg,
          borderRadius: Radii.lgBorder,
          border: Border.all(
            color: selected
                ? DesignColors.primary
                : (isDark
                    ? DesignColors.borderDark
                    : DesignColors.borderLight),
          ),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              label,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 12,
                fontWeight: FontWeight.w600,
                color: fg,
              ),
            ),
            if (count > 0) ...[
              const SizedBox(width: 6),
              Container(
                padding: const EdgeInsets.symmetric(horizontal: Spacing.s8, vertical: Spacing.s2),
                decoration: BoxDecoration(
                  color: selected
                      ? Colors.white.withValues(alpha: 0.2)
                      : DesignColors.primary.withValues(alpha: 0.15),
                  borderRadius: BorderRadius.circular(8),
                ),
                child: Text(
                  '$count',
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: FontSizes.label,
                    fontWeight: FontWeight.w700,
                    color: selected ? Colors.white : DesignColors.primary,
                  ),
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

class _EmptyState extends StatelessWidget {
  final _Filter filter;
  const _EmptyState({required this.filter});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final msg = filter == _Filter.all
        ? 'Nothing in the inbox'
        : 'Nothing here';
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(Icons.inbox_outlined, size: 64, color: muted),
          const SizedBox(height: 16),
          Text(
            msg,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 16,
              fontWeight: FontWeight.w600,
              color: muted,
            ),
          ),
        ],
      ),
    );
  }
}

class _MeCard extends ConsumerWidget {
  final _MeItem item;
  const _MeCard({required this.item});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final colorScheme = Theme.of(context).colorScheme;
    final accent = _accentColor(item);

    return InkWell(
      onTap: () => _primaryAction(context, ref),
      borderRadius: BorderRadius.circular(12),
      child: Container(
        padding: const EdgeInsets.all(Spacing.s12),
        decoration: BoxDecoration(
          color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
          borderRadius: BorderRadius.circular(12),
          border: Border.all(color: accent.withValues(alpha: 0.4)),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                AppStatusChip(label: _kindLabel(item.kind), color: accent),
                if (item.severity != null && item.severity!.isNotEmpty) ...[
                  const SizedBox(width: 6),
                  AppStatusChip(
                    label: item.severity!,
                    color: _severityColor(item.severity!),
                  ),
                ],
                if (item.actor != null && StewardBadge.matches(item.actor!))
                  const StewardBadge(),
                const Spacer(),
                Text(
                  _shortTs(item.ts),
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: FontSizes.label,
                    color: isDark
                        ? DesignColors.textMuted
                        : DesignColors.textMutedLight,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 8),
            Text(
              item.title,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 14,
                fontWeight: FontWeight.w600,
                color: colorScheme.onSurface,
              ),
            ),
            if (item.subtitle != null && item.subtitle!.isNotEmpty) ...[
              const SizedBox(height: 4),
              Text(
                item.subtitle!,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  color: isDark
                      ? DesignColors.textMuted
                      : DesignColors.textMutedLight,
                ),
              ),
            ],
            if (item.filter == _Filter.approvals) ...[
              const SizedBox(height: 10),
              if (item.kind == 'help_request' || item.kind == 'elicit')
                InlineHelpRequestActions(
                  id: item.id,
                  kind: item.kind,
                  pendingPayload: _pendingPayload(item.attention),
                )
              else if (item.kind == 'propose' && item.attention != null)
                // ADR-030 W15-W18 per-kind propose cards routed via
                // ProposeCardRouter: each registered change_kind gets
                // its own card (deliverable.set_state / phase.advance
                // / task.set_status / agent.spawn / template.install);
                // unrecognised change_kinds fall back to the legacy
                // Approve/Reject pair. MVP tier is 'principal'; the
                // W19 steward-side inbox will pass its own tier.
                ProposeCardRouter(
                  attention: item.attention!,
                  myTier: 'principal',
                )
              else if (item.kind == 'revision_requested')
                // ADR-020 W2: not a binary approve/reject — the steward
                // resolves the director's redlines. The decide endpoint
                // doesn't accept this kind, so route to the detail screen
                // (where _RevisionRequestedBlock renders the notes +
                // linked annotations) via the action button below.
                const SizedBox.shrink()
              else
                InlineApprovalActions(
                  id: item.id,
                  kind: item.kind,
                  pendingPayload: _pendingPayload(item.attention),
                ),
              const SizedBox(height: 6),
              Align(
                alignment: Alignment.centerRight,
                child: TextButton.icon(
                  onPressed: item.attention == null
                      ? null
                      : () => Navigator.of(context).push(
                            MaterialPageRoute(
                              builder: (_) => ApprovalDetailScreen(
                                attention: item.attention!,
                              ),
                            ),
                          ),
                  icon: Icon(
                    item.kind == 'revision_requested'
                        ? Icons.rate_review_outlined
                        : Icons.info_outline,
                    size: 14,
                  ),
                  label: Text(
                    item.kind == 'revision_requested' ? 'Review redlines' : 'Details',
                  ),
                  style: TextButton.styleFrom(
                    visualDensity: VisualDensity.compact,
                    textStyle: GoogleFonts.jetBrainsMono(fontSize: 11),
                  ),
                ),
              ),
            ] else if (item.attention != null) ...[
              // Non-action attention items (idle, agent_error, generic
              // FYI messages) still have a useful detail view. Messages
              // (notice, budget_exceeded, …) also get a Dismiss — they
              // ask for no decision, so /resolve clears them from the
              // inbox; without it they pile up unbounded.
              const SizedBox(height: 6),
              Row(
                mainAxisAlignment: MainAxisAlignment.end,
                children: [
                  if (item.filter == _Filter.messages)
                    TextButton.icon(
                      onPressed: () => _dismiss(context, ref),
                      icon: const Icon(Icons.check_circle_outline, size: 14),
                      label: const Text('Dismiss'),
                      style: TextButton.styleFrom(
                        visualDensity: VisualDensity.compact,
                        textStyle: GoogleFonts.jetBrainsMono(fontSize: 11),
                      ),
                    ),
                  TextButton.icon(
                    onPressed: () => Navigator.of(context).push(
                      MaterialPageRoute(
                        builder: (_) => ApprovalDetailScreen(
                          attention: item.attention!,
                        ),
                      ),
                    ),
                    icon: const Icon(Icons.info_outline, size: 14),
                    label: const Text('Details'),
                    style: TextButton.styleFrom(
                      visualDensity: VisualDensity.compact,
                      textStyle: GoogleFonts.jetBrainsMono(fontSize: 11),
                    ),
                  ),
                ],
              ),
            ],
          ],
        ),
      ),
    );
  }

  void _primaryAction(BuildContext context, WidgetRef ref) {
    // Any attention item with a row available routes to the detail
    // screen — the director-note + linked-annotations block (ADR-020)
    // and origin/transcript sections give every kind something useful
    // to land on, even those without inline actions.
    if (item.attention != null) {
      Navigator.of(context).push(
        MaterialPageRoute(
          builder: (_) => ApprovalDetailScreen(attention: item.attention!),
        ),
      );
    }
  }

  // Acknowledge / clear an FYI Message (notice, budget_exceeded, …) via
  // the no-decision /resolve path. The hub refuses kinds that owe an
  // agent a reply, so this is only wired for the Messages filter; on
  // success the row drops out of the open list via _reloadAttention.
  Future<void> _dismiss(BuildContext context, WidgetRef ref) async {
    try {
      await ref.read(hubProvider.notifier).resolve(item.id);
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Dismissed')),
        );
      }
    } catch (e) {
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Dismiss failed: $e')),
        );
      }
    }
  }

  // Decode pending_payload — the hub sends it raw on the wire; we need it
  // pre-parsed so the action row can render decision options without
  // every consumer re-doing the JSON dance.
  Map<String, dynamic>? _pendingPayload(Map<String, dynamic>? attention) {
    final raw = attention?['pending_payload'];
    if (raw is Map) return raw.cast<String, dynamic>();
    if (raw is String && raw.isNotEmpty) {
      try {
        final decoded = jsonDecode(raw);
        if (decoded is Map) return decoded.cast<String, dynamic>();
      } catch (_) {}
    }
    return null;
  }

  Color _accentColor(_MeItem item) {
    switch (item.filter) {
      case _Filter.approvals:
        return DesignColors.primary;
      case _Filter.agents:
        return DesignColors.warning;
      case _Filter.messages:
        return DesignColors.primary;
      case _Filter.all:
        return DesignColors.primary;
    }
  }

  Color _severityColor(String s) {
    switch (s) {
      case 'critical':
        return DesignColors.error;
      case 'major':
        return DesignColors.warning;
      case 'minor':
      default:
        return DesignColors.primary;
    }
  }

  String _kindLabel(String kind) {
    if (kind.isEmpty) return 'event';
    return kind.replaceAll('_', ' ');
  }

  String _shortTs(DateTime ts) {
    final diff = DateTime.now().difference(ts);
    if (diff.inMinutes < 1) return 'now';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m';
    if (diff.inHours < 24) return '${diff.inHours}h';
    if (diff.inDays < 7) return '${diff.inDays}d';
    return '${(diff.inDays / 7).floor()}w';
  }
}


/// W3 affordance: surfaces the count of open `priority=urgent` tasks
/// across the team's projects, with one-tap entry into the top offenders.
/// Hidden when the count is zero so the Me tab stays quiet in the
/// common case.
class _UrgentTasksSection extends ConsumerWidget {
  const _UrgentTasksSection();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final summary =
        ref.watch(urgentTasksProvider).value ?? UrgentTasksSummary.empty;
    if (summary.count == 0) return const SizedBox.shrink();
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final urgentColor = taskPriorityColor(TaskPriority.urgent);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        const _SectionLabel(text: 'Urgent'),
        Padding(
          padding: const EdgeInsets.fromLTRB(16, Spacing.s8, 16, 4),
          child: Container(
            decoration: BoxDecoration(
              color: bg,
              borderRadius: BorderRadius.circular(12),
              border: Border.all(color: urgentColor.withValues(alpha: 0.4)),
            ),
            child: Column(
              children: [
                Padding(
                  padding: const EdgeInsets.fromLTRB(Spacing.s12, 12, Spacing.s12, Spacing.s8),
                  child: Row(
                    children: [
                      Container(
                        width: 10,
                        height: 10,
                        decoration: BoxDecoration(
                          color: urgentColor,
                          shape: BoxShape.circle,
                        ),
                      ),
                      const SizedBox(width: 10),
                      Expanded(
                        child: Text(
                          '${summary.count} urgent '
                          '${summary.count == 1 ? "task" : "tasks"}',
                          style: GoogleFonts.spaceGrotesk(
                            fontSize: 14,
                            fontWeight: FontWeight.w700,
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
                for (final row in summary.top)
                  InkWell(
                    onTap: () => Navigator.of(context).push(MaterialPageRoute(
                      builder: (_) => TaskDetailScreen(
                        projectId: row.projectId,
                        taskId: row.taskId,
                      ),
                    )),
                    child: Padding(
                      padding: const EdgeInsets.symmetric(
                          horizontal: Spacing.s12, vertical: 8),
                      child: Row(
                        children: [
                          Container(
                            width: 6,
                            height: 6,
                            decoration: BoxDecoration(
                              color: urgentColor,
                              shape: BoxShape.circle,
                            ),
                          ),
                          const SizedBox(width: 10),
                          Expanded(
                            child: Column(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                Text(
                                  row.title,
                                  maxLines: 1,
                                  overflow: TextOverflow.ellipsis,
                                  style: GoogleFonts.spaceGrotesk(
                                    fontSize: 13,
                                    fontWeight: FontWeight.w600,
                                  ),
                                ),
                                const SizedBox(height: 2),
                                Text(
                                  '${row.projectName} · ${row.status}',
                                  style: GoogleFonts.jetBrainsMono(
                                    fontSize: FontSizes.label,
                                    color: muted,
                                  ),
                                ),
                              ],
                            ),
                          ),
                          Icon(Icons.chevron_right, size: 16, color: muted),
                        ],
                      ),
                    ),
                  ),
                Container(height: 1, color: border),
                const SizedBox(height: 4),
              ],
            ),
          ),
        ),
      ],
    );
  }
}

class _SectionLabel extends StatelessWidget {
  final String text;
  const _SectionLabel({required this.text});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Padding(
      padding: const EdgeInsets.fromLTRB(Spacing.s16, Spacing.s8, Spacing.s16, 2),
      child: Text(
        text.toUpperCase(),
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

/// Horizontal strip of in-flight sessions on the Me page. Each tile
/// surfaces the session title, scope (general / project: <name> /
/// attention), and the steward handle the session is bound to. Tap
/// pushes [SessionChatScreen] for direct entry.
///
/// Replaced the prior "My work" project strip: sessions are what
/// the principal is actively in the middle of, and the Projects tab
/// already owns the project-list navigation.
class _ActiveSessionsStrip extends StatelessWidget {
  final List<Map<String, dynamic>> sessions;
  final List<Map<String, dynamic>> agents;
  final List<Map<String, dynamic>> projects;
  final List<Map<String, dynamic>> hosts;
  const _ActiveSessionsStrip({
    required this.sessions,
    required this.agents,
    required this.projects,
    required this.hosts,
  });

  String _scopeLabel(Map<String, dynamic> s) {
    final kind = (s['scope_kind'] ?? '').toString();
    final id = (s['scope_id'] ?? '').toString();
    switch (kind) {
      case 'project':
        for (final p in projects) {
          if ((p['id'] ?? '').toString() == id) {
            final name =
                (p['name'] ?? p['title'] ?? '').toString();
            return name.isEmpty ? 'Project' : 'Project: $name';
          }
        }
        return 'Project';
      case 'attention':
        return 'Approving';
      case 'team':
      case '':
        // "General" is reserved for the general steward (@steward,
        // steward.general.v1); a team-scoped session reads "Team" (#65). The
        // Sessions page classifies stewards General/Project/Domain by
        // handle+kind — that taxonomy is authoritative; this scope label must
        // not collide with it.
        return 'Team';
      default:
        return kind;
    }
  }

  String _stewardName(String agentId) {
    if (agentId.isEmpty) return '(no steward)';
    for (final a in agents) {
      if ((a['id'] ?? '').toString() != agentId) continue;
      return stewardLabel((a['handle'] ?? '').toString());
    }
    return '';
  }

  /// The agent row for a session's `current_agent_id`, or null when it
  /// isn't loaded yet. Drives the engine/host line and the category accent.
  Map<String, dynamic>? _agentFor(String agentId) {
    if (agentId.isEmpty) return null;
    for (final a in agents) {
      if ((a['id'] ?? '').toString() == agentId) return a;
    }
    return null;
  }

  /// Resolve `engine · host` for the strip's third line. The session row
  /// only carries `current_agent_id`; engine + host live on the agents
  /// row. Returns empty when the agent isn't loaded yet so the caller can
  /// omit the line.
  String _engineHost(String agentId) {
    final agent = _agentFor(agentId);
    if (agent == null) return '';
    final engine = (agent['kind'] ?? '').toString();
    final hostId = (agent['host_id'] ?? '').toString();
    final host = hostLabel(hosts, hostId) ?? '';
    if (engine.isEmpty && host.isEmpty) return '';
    if (engine.isEmpty) return host;
    if (host.isEmpty) return engine;
    return '$engine @ $host';
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _SectionLabel(text: l10n.meActiveSessionsSection),
        SizedBox(
          height: 108,
          child: ListView.separated(
            scrollDirection: Axis.horizontal,
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
            itemCount: sessions.length,
            separatorBuilder: (_, __) => const SizedBox(width: 8),
            itemBuilder: (ctx, i) {
              final s = sessions[i];
              final id = (s['id'] ?? '').toString();
              final agentId = (s['current_agent_id'] ?? '').toString();
              // v1.0.705 polish — shared three-tier title precedence
              // (user title > session_name_hint > placeholder).
              final title = sessionDisplayTitle(s);
              final scope = _scopeLabel(s);
              final steward = _stewardName(agentId);
              final engineHost = _engineHost(agentId);
              // Category accent (ADR-/IA): color + icon distinguish team
              // steward · project steward · domain steward · worker at a
              // glance across the strip. Classifier is shared
              // (agentCategory) so the taxonomy never forks.
              final style =
                  agentCategoryStyle(agentCategory(_agentFor(agentId), session: s));
              return InkWell(
                onTap: () => Navigator.of(ctx).push(MaterialPageRoute(
                  builder: (_) => SessionChatScreen(
                    sessionId: id,
                    agentId: agentId,
                    title: title,
                  ),
                )),
                borderRadius: Radii.mdBorder,
                child: Container(
                  width: 200,
                  decoration: BoxDecoration(
                    color: bg,
                    borderRadius: Radii.mdBorder,
                    border: Border.all(color: border),
                  ),
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      // Category accent stripe — full-height, rounded to
                      // match the card's left edge.
                      Container(
                        width: 4,
                        decoration: BoxDecoration(
                          color: style.color,
                          borderRadius: const BorderRadius.only(
                            topLeft: Radius.circular(10),
                            bottomLeft: Radius.circular(10),
                          ),
                        ),
                      ),
                      Expanded(
                        child: Padding(
                          padding: const EdgeInsets.symmetric(
                              horizontal: 12, vertical: Spacing.s8),
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            mainAxisAlignment: MainAxisAlignment.spaceBetween,
                            children: [
                              Row(
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  Padding(
                                    padding: const EdgeInsets.only(top: Spacing.s2),
                                    child: Icon(style.icon,
                                        size: 14, color: style.color),
                                  ),
                                  const SizedBox(width: 6),
                                  Expanded(
                                    child: Text(
                                      title,
                                      maxLines: 2,
                                      overflow: TextOverflow.ellipsis,
                                      style: GoogleFonts.spaceGrotesk(
                                        fontSize: 13,
                                        fontWeight: FontWeight.w700,
                                      ),
                                    ),
                                  ),
                                ],
                              ),
                              Column(
                                crossAxisAlignment:
                                    CrossAxisAlignment.start,
                                children: [
                                  Text(
                                    scope,
                                    maxLines: 1,
                                    overflow: TextOverflow.ellipsis,
                                    style: GoogleFonts.jetBrainsMono(
                                      fontSize: FontSizes.label,
                                      color: muted,
                                    ),
                                  ),
                                  if (steward.isNotEmpty)
                                    Text(
                                      '· $steward',
                                      maxLines: 1,
                                      overflow: TextOverflow.ellipsis,
                                      style: GoogleFonts.jetBrainsMono(
                                        fontSize: FontSizes.label,
                                        color: muted,
                                      ),
                                    ),
                                  if (engineHost.isNotEmpty)
                                    Text(
                                      engineHost,
                                      maxLines: 1,
                                      overflow: TextOverflow.ellipsis,
                                      style: GoogleFonts.jetBrainsMono(
                                        fontSize: FontSizes.label,
                                        color: muted,
                                      ),
                                    ),
                                ],
                              ),
                            ],
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
              );
            },
          ),
        ),
      ],
    );
  }
}

/// Pinned CTA to direct the steward from Me. Closes the asymmetry where
